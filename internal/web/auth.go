package web

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Session
// ─────────────────────────────────────────────────────────────────────────────

// Session represents an authenticated web-panel session.
type Session struct {
	Token     string
	CreatedAt time.Time
	ExpiresAt time.Time
	IP        string
}

// IsExpired reports whether the session has passed its expiry time.
func (s Session) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

// ─────────────────────────────────────────────────────────────────────────────
// AuthManager
// ─────────────────────────────────────────────────────────────────────────────

const (
	sessionTTL = 24 * time.Hour // default session lifetime
)

// AuthManager manages web-panel authentication.
// Passwords are stored as SHA-256 hashes.
type AuthManager struct {
	mu           sync.RWMutex
	sessions     map[string]Session
	passwordHash string // SHA-256 hex of the configured password
}

// NewAuthManager creates an AuthManager using the given plaintext password.
// If password is empty, authentication is effectively disabled.
func NewAuthManager(password string) *AuthManager {
	return &AuthManager{
		sessions:     make(map[string]Session),
		passwordHash: hashPassword(password),
	}
}

// Authenticate checks the supplied plaintext password and, on success, creates
// and returns a new session token.  Returns an error if the password is wrong.
func (a *AuthManager) Authenticate(password string) (string, error) {
	if hashPassword(password) != a.passwordHash {
		return "", fmt.Errorf("invalid password")
	}
	token, err := GenerateToken()
	if err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	now := time.Now()
	sess := Session{
		Token:     token,
		CreatedAt: now,
		ExpiresAt: now.Add(sessionTTL),
	}

	a.mu.Lock()
	a.sessions[token] = sess
	a.mu.Unlock()

	return token, nil
}

// Validate reports whether token is a known, non-expired session token.
func (a *AuthManager) Validate(token string) bool {
	a.mu.RLock()
	sess, ok := a.sessions[token]
	a.mu.RUnlock()
	if !ok {
		return false
	}
	if sess.IsExpired() {
		// Lazily remove expired session
		a.mu.Lock()
		delete(a.sessions, token)
		a.mu.Unlock()
		return false
	}
	return true
}

// Revoke removes a session by token.
func (a *AuthManager) Revoke(token string) {
	a.mu.Lock()
	delete(a.sessions, token)
	a.mu.Unlock()
}

// PruneExpired removes all expired sessions from the internal map.
func (a *AuthManager) PruneExpired() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for tok, sess := range a.sessions {
		if sess.IsExpired() {
			delete(a.sessions, tok)
		}
	}
}

// AuthMiddleware returns an HTTP middleware that requires a valid Bearer token
// (from the Authorization header) for every request.  The token is checked
// against the AuthManager's session store.
func (a *AuthManager) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if !a.Validate(token) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// GenerateToken creates a cryptographically secure random 32-byte hex token.
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// hashPassword returns the hex-encoded SHA-256 digest of the password.
func hashPassword(password string) string {
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:])
}

// bearerToken extracts the token from the "Authorization: Bearer <token>"
// header.  Returns an empty string if the header is absent or malformed.
func bearerToken(r *http.Request) string {
	hdr := r.Header.Get("Authorization")
	if !strings.HasPrefix(hdr, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(hdr, "Bearer ")
}
