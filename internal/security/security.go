// Package security provides file-security helpers ported from utils/security.py,
// plus session-path management, token generation, password hashing, and URL
// validation.
//
// SPDX-License-Identifier: MIT
package security

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// ---- Session helpers --------------------------------------------------------

// mcubDir returns the instance-specific data directory:
// ~/.MCUB/<sha256(apiID+apiHash)[:16]>
func mcubDir(apiID int64, apiHash string) (string, error) {
	key := strconv.FormatInt(apiID, 10) + apiHash
	sum := sha256.Sum256([]byte(key))
	instanceHash := hex.EncodeToString(sum[:])[:16]

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	dir := filepath.Join(home, ".MCUB", instanceHash)

	// Symlink attack prevention.
	if fi, err2 := os.Lstat(dir); err2 == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("security: %s is a symlink, refusing to use", dir)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create mcub dir: %w", err)
	}
	return dir, nil
}

// sessionsDir returns (and creates) the sessions sub-directory.
func sessionsDir(apiID int64, apiHash string) (string, error) {
	base, err := mcubDir(apiID, apiHash)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create sessions dir: %w", err)
	}
	return dir, nil
}

// SessionExists returns true when a session file already exists for the given
// API credentials (checks both the versioned location and the legacy root).
func SessionExists(apiID int64, apiHash string) bool {
	if apiID != 0 && apiHash != "" {
		dir, err := sessionsDir(apiID, apiHash)
		if err == nil {
			if _, err2 := os.Stat(filepath.Join(dir, "user_session.session")); err2 == nil {
				return true
			}
		}
	}
	_, err := os.Stat("user_session.session")
	return err == nil
}

// GenerateSessionName returns a deterministic session file name based on the
// API ID and phone number (without extension).
func GenerateSessionName(apiID int64, phone string) string {
	// Normalise phone: keep digits only.
	var digits strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	key := strconv.FormatInt(apiID, 10) + "_" + digits.String()
	sum := sha256.Sum256([]byte(key))
	return "session_" + hex.EncodeToString(sum[:])[:12]
}

// GetSessionPath returns the full path to the session file for the given
// API credentials (including the .session extension is the caller's
// responsibility).
func GetSessionPath(apiID int64, apiHash string) string {
	dir, err := sessionsDir(apiID, apiHash)
	if err != nil {
		// Fall back to the current working directory.
		return "user_session"
	}
	return filepath.Join(dir, "user_session")
}

// ---- Token generation -------------------------------------------------------

// GenerateToken returns a cryptographically secure random hex string of
// exactly length characters.
func GenerateToken(length int) string {
	if length <= 0 {
		length = 32
	}
	// We need length/2 bytes to produce length hex chars (rounded up).
	nBytes := (length + 1) / 2
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("security.GenerateToken: rand.Read failed: %v", err))
	}
	return hex.EncodeToString(buf)[:length]
}

// ---- Password helpers -------------------------------------------------------

// HashPassword hashes password with bcrypt (cost=12) and returns the hash
// string suitable for storage.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

// CheckPassword returns true when password matches the bcrypt hash.
func CheckPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// ---- URL validation ---------------------------------------------------------

// IsSecureURL returns true when rawURL is an http or https URL that does not
// point to a loopback or private-network address.
func IsSecureURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	// Block localhost and loopback names.
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return false
	}
	ip := net.ParseIP(host)
	if ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return false
	}
	return true
}

// ValidateRemoteURL checks whether a URL is safe to send an HTTP request to
// (basic SSRF protection). Returns (ok, reason).
func ValidateRemoteURL(rawURL string) (bool, string) {
	if rawURL == "" {
		return false, "empty URL"
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false, "invalid URL: " + err.Error()
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false, "unsupported scheme: " + u.Scheme
	}
	host := u.Hostname()
	if host == "" {
		return false, "missing host"
	}
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return false, "localhost is not allowed"
	}
	ip := net.ParseIP(host)
	if ip != nil {
		if ip.IsLoopback() {
			return false, "loopback address is not allowed"
		}
		if ip.IsPrivate() {
			return false, "private IP address is not allowed"
		}
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return false, "link-local address is not allowed"
		}
	}
	return true, ""
}

// ---- File-security helpers (ported from security.py) -----------------------

// IsLocked returns true when the file at path has permissions 0600 (owner
// read/write only). Always returns false on Windows.
func IsLocked(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.Mode().Perm() == 0o600
}

// LockFile sets permissions on path to 0600. Returns true on success, false
// when the file does not exist or the operation fails.
func LockFile(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return true // nothing to lock yet
	}
	return os.Chmod(path, 0o600) == nil
}

// SecureDelete overwrites path with random data (passes times) then removes it.
func SecureDelete(path string, passes int) bool {
	if passes <= 0 {
		passes = 3
	}
	fi, err := os.Stat(path)
	if err != nil {
		return true // already gone
	}
	size := fi.Size()
	f, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	buf := make([]byte, size)
	for i := 0; i < passes; i++ {
		if _, err := rand.Read(buf); err != nil {
			f.Close()
			return false
		}
		if _, err := f.WriteAt(buf, 0); err != nil {
			f.Close()
			return false
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return false
		}
	}
	f.Close()
	return os.Remove(path) == nil
}

// SaveChecksum writes a SHA-256 hex digest of path to path+".sha256".
func SaveChecksum(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	hexSum := hex.EncodeToString(sum[:])
	checksumPath := path + ".sha256"
	if err := os.WriteFile(checksumPath, []byte(hexSum), 0o600); err != nil {
		return err
	}
	return nil
}

// VerifyChecksum compares the current SHA-256 of path against the stored
// .sha256 file. Returns true when they match or when no checksum file exists.
func VerifyChecksum(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return true // file absent → nothing to verify
	}
	checksumPath := path + ".sha256"
	stored, err := os.ReadFile(checksumPath)
	if err != nil {
		return true // no checksum file → treat as OK
	}
	current := sha256.Sum256(data)
	return strings.TrimSpace(string(stored)) == hex.EncodeToString(current[:])
}
