package security_test

import (
	"strings"
	"testing"

	"github.com/nulls-brawl-site/mcub-go/internal/security"
)

// ---- GenerateToken ----------------------------------------------------------

func TestGenerateTokenLength(t *testing.T) {
	tok := security.GenerateToken(32)
	if len(tok) != 32 {
		t.Errorf("expected length 32, got %d", len(tok))
	}
}

func TestGenerateTokenUnique(t *testing.T) {
	t1 := security.GenerateToken(32)
	t2 := security.GenerateToken(32)
	if t1 == t2 {
		t.Fatal("tokens should be unique")
	}
}

func TestGenerateTokenNotEmpty(t *testing.T) {
	tok := security.GenerateToken(16)
	if tok == "" {
		t.Fatal("empty token")
	}
}

func TestGenerateTokenHex(t *testing.T) {
	tok := security.GenerateToken(20)
	for _, r := range tok {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			t.Errorf("non-hex char %q in token %q", r, tok)
		}
	}
}

func TestGenerateTokenZeroLen(t *testing.T) {
	// Zero length should default to 32.
	tok := security.GenerateToken(0)
	if len(tok) == 0 {
		t.Fatal("expected non-empty for zero length")
	}
}

func TestGenerateTokenLong(t *testing.T) {
	tok := security.GenerateToken(64)
	if len(tok) != 64 {
		t.Errorf("expected 64, got %d", len(tok))
	}
}

// ---- HashPassword / CheckPassword -------------------------------------------

func TestHashPasswordNonEmpty(t *testing.T) {
	hash, err := security.HashPassword("secret123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash == "" {
		t.Fatal("empty hash")
	}
}

func TestCheckPasswordMatch(t *testing.T) {
	hash, err := security.HashPassword("secret123")
	if err != nil {
		t.Fatal(err)
	}
	if !security.CheckPassword("secret123", hash) {
		t.Fatal("CheckPassword: should match")
	}
}

func TestCheckPasswordMismatch(t *testing.T) {
	hash, _ := security.HashPassword("correct")
	if security.CheckPassword("wrong", hash) {
		t.Fatal("CheckPassword: should not match wrong password")
	}
}

func TestHashPasswordUnique(t *testing.T) {
	h1, _ := security.HashPassword("pass")
	h2, _ := security.HashPassword("pass")
	// bcrypt uses random salt — hashes should differ
	if h1 == h2 {
		t.Fatal("bcrypt hashes should be different (random salt)")
	}
}

func TestHashPasswordEmpty(t *testing.T) {
	hash, err := security.HashPassword("")
	if err != nil {
		t.Fatal(err)
	}
	if !security.CheckPassword("", hash) {
		t.Fatal("empty password should match its hash")
	}
}

// ---- ValidateRemoteURL ------------------------------------------------------

func TestValidateRemoteURLHTTPS(t *testing.T) {
	ok, reason := security.ValidateRemoteURL("https://github.com/foo/bar")
	if !ok {
		t.Fatalf("expected ok=true, got reason=%q", reason)
	}
}

func TestValidateRemoteURLHTTP(t *testing.T) {
	ok, _ := security.ValidateRemoteURL("http://example.com/path")
	if !ok {
		t.Fatal("http should be valid")
	}
}

func TestValidateRemoteURLLocalhost(t *testing.T) {
	ok, reason := security.ValidateRemoteURL("http://localhost/evil.py")
	if ok {
		t.Fatal("localhost should be blocked")
	}
	if !strings.Contains(reason, "localhost") {
		t.Errorf("reason %q should mention localhost", reason)
	}
}

func TestValidateRemoteURLLoopback(t *testing.T) {
	ok, _ := security.ValidateRemoteURL("http://127.0.0.1/evil")
	if ok {
		t.Fatal("loopback IP should be blocked")
	}
}

func TestValidateRemoteURLPrivateIP(t *testing.T) {
	ok, _ := security.ValidateRemoteURL("http://192.168.1.1/evil")
	if ok {
		t.Fatal("private IP should be blocked")
	}
}

func TestValidateRemoteURLFileScheme(t *testing.T) {
	ok, reason := security.ValidateRemoteURL("file:///etc/passwd")
	if ok {
		t.Fatal("file:// should be blocked")
	}
	if reason == "" {
		t.Fatal("expected non-empty reason")
	}
}

func TestValidateRemoteURLEmpty(t *testing.T) {
	ok, reason := security.ValidateRemoteURL("")
	if ok {
		t.Fatal("empty URL should be blocked")
	}
	if reason == "" {
		t.Fatal("expected non-empty reason")
	}
}

func TestValidateRemoteURLNoScheme(t *testing.T) {
	ok, _ := security.ValidateRemoteURL("not-a-url")
	if ok {
		t.Fatal("bare string should be blocked")
	}
}

func TestValidateRemoteURLLinkLocal(t *testing.T) {
	ok, _ := security.ValidateRemoteURL("http://169.254.169.254/metadata")
	if ok {
		t.Fatal("link-local should be blocked")
	}
}

// ---- IsSecureURL ------------------------------------------------------------

func TestIsSecureURLHTTPS(t *testing.T) {
	if !security.IsSecureURL("https://example.com/path") {
		t.Fatal("expected true for https")
	}
}

func TestIsSecureURLHTTP(t *testing.T) {
	if !security.IsSecureURL("http://example.com") {
		t.Fatal("expected true for http")
	}
}

func TestIsSecureURLLocalhost(t *testing.T) {
	if security.IsSecureURL("http://localhost") {
		t.Fatal("expected false for localhost")
	}
}

func TestIsSecureURLFTP(t *testing.T) {
	if security.IsSecureURL("ftp://example.com") {
		t.Fatal("expected false for ftp")
	}
}

// ---- GenerateSessionName ----------------------------------------------------

func TestGenerateSessionNameDeterministic(t *testing.T) {
	n1 := security.GenerateSessionName(12345, "+79001234567")
	n2 := security.GenerateSessionName(12345, "+79001234567")
	if n1 != n2 {
		t.Fatal("session name should be deterministic")
	}
}

func TestGenerateSessionNameDifferentAPI(t *testing.T) {
	n1 := security.GenerateSessionName(111, "+7900")
	n2 := security.GenerateSessionName(222, "+7900")
	if n1 == n2 {
		t.Fatal("different API IDs should give different names")
	}
}

func TestGenerateSessionNamePrefix(t *testing.T) {
	n := security.GenerateSessionName(1, "+1")
	if !strings.HasPrefix(n, "session_") {
		t.Errorf("expected prefix 'session_', got %q", n)
	}
}

// ---- LockFile / IsLocked ----------------------------------------------------

func TestLockFileNonExistent(t *testing.T) {
	// LockFile on non-existent path returns true (nothing to lock).
	ok := security.LockFile("/tmp/does_not_exist_mcub_test_xyz")
	if !ok {
		t.Fatal("expected true for non-existent file")
	}
}

// ---- VerifyChecksum ---------------------------------------------------------

func TestVerifyChecksumNoFile(t *testing.T) {
	// No checksum file — should return true (treat as OK).
	if !security.VerifyChecksum("/tmp/no_such_file_mcub") {
		t.Fatal("expected true when file does not exist")
	}
}
