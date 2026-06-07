package permissions_test

import (
	"testing"
	"time"

	"github.com/nulls-brawl-site/mcub-go/internal/permissions"
)

func TestAllowIsAllowed(t *testing.T) {
	m := permissions.New()
	m.Allow(123, "token1", 60)

	if !m.IsAllowed(123, "token1") {
		t.Fatal("user 123 should be allowed for token1")
	}
	if m.IsAllowed(456, "token1") {
		t.Fatal("user 456 should not be allowed for token1")
	}
}

func TestIsAllowedMissingToken(t *testing.T) {
	m := permissions.New()
	if m.IsAllowed(1, "notoken") {
		t.Fatal("should not be allowed for missing token")
	}
}

func TestExpiry(t *testing.T) {
	m := permissions.New()
	// ttlSecs=0 means already expired
	m.Allow(1, "tok", 0)
	// Give a tiny sleep to ensure time passes
	time.Sleep(5 * time.Millisecond)
	if m.IsAllowed(1, "tok") {
		t.Fatal("permission should have expired (ttl=0)")
	}
}

func TestExpiryShortTTL(t *testing.T) {
	m := permissions.New()
	// 1 second TTL, but we'll sleep past it
	// Using a very short trick: allow with negative-equivalent by manipulating
	// We just verify that a live permission works and then sleeps expire it.
	m.Allow(42, "shortlived", 0) // 0 seconds = expires immediately
	time.Sleep(10 * time.Millisecond)
	if m.IsAllowed(42, "shortlived") {
		t.Fatal("0-second permission should be expired")
	}
}

func TestAllowAll(t *testing.T) {
	m := permissions.New()
	m.AllowAll("shared_token")

	// Any user should be allowed
	if !m.IsAllowed(1, "shared_token") {
		t.Fatal("user 1 should be allowed via AllowAll")
	}
	if !m.IsAllowed(999, "shared_token") {
		t.Fatal("user 999 should be allowed via AllowAll")
	}
	if !m.IsAllAllowed("shared_token") {
		t.Fatal("IsAllAllowed should return true")
	}
}

func TestAllowAllFalseForNormalToken(t *testing.T) {
	m := permissions.New()
	m.Allow(1, "specific", 60)
	if m.IsAllAllowed("specific") {
		t.Fatal("specific token should not be all-allowed")
	}
}

func TestRevoke(t *testing.T) {
	m := permissions.New()
	m.Allow(1, "tok", 60)
	m.Revoke("tok")
	if m.IsAllowed(1, "tok") {
		t.Fatal("permission should be revoked")
	}
}

func TestRevokeNonExistent(t *testing.T) {
	m := permissions.New()
	// Should not panic
	m.Revoke("doesnotexist")
}

func TestCleanup(t *testing.T) {
	m := permissions.New()
	m.Allow(1, "expired_tok", 0) // 0 seconds TTL
	time.Sleep(5 * time.Millisecond)
	// Add a live permission
	m.Allow(2, "live_tok", 60)

	m.Cleanup()

	// expired should be gone
	if m.IsAllowed(1, "expired_tok") {
		t.Fatal("expired permission should be cleaned up")
	}
	// live should remain
	if !m.IsAllowed(2, "live_tok") {
		t.Fatal("live permission should survive cleanup")
	}
}

func TestMultipleGrants(t *testing.T) {
	m := permissions.New()
	// Multiple users allowed for same token
	m.Allow(1, "multi", 60)
	m.Allow(2, "multi", 60)

	if !m.IsAllowed(1, "multi") {
		t.Fatal("user 1 should be allowed")
	}
	if !m.IsAllowed(2, "multi") {
		t.Fatal("user 2 should be allowed")
	}
	if m.IsAllowed(3, "multi") {
		t.Fatal("user 3 should not be allowed")
	}
}

func TestAllowOverwriteExtends(t *testing.T) {
	m := permissions.New()
	m.Allow(1, "tok", 60)
	m.Allow(1, "tok", 120) // second grant stacks
	if !m.IsAllowed(1, "tok") {
		t.Fatal("should still be allowed after second grant")
	}
}
