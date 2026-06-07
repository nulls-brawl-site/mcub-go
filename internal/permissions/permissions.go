// Package permissions manages temporary callback permissions for inline buttons.
// Ported from core/lib/base/permissions.py (CallbackPermissionManager).
package permissions

import (
	"sync"
	"time"
)

// sentinel token used to mark "allow all" access.
const allowAllSentinel = "__allow_all__"

// CallbackPermission records a single user's access right to a callback token.
type CallbackPermission struct {
	UserID    int64
	Token     string
	ExpiresAt time.Time
}

// CallbackPermissionManager manages which users are allowed to trigger which
// callback tokens. Permissions are time-bounded and may be granted per-user
// or universally (allow-all).
//
// The design mirrors the Python CallbackPermissionManager but is token-centric
// instead of user-centric, to align with Telegram's inline-button model where
// a message is sent to a user and only that user (or anyone) should be able to
// click it.
type CallbackPermissionManager struct {
	mu    sync.RWMutex
	perms map[string][]CallbackPermission // token -> grants
}

// New creates an empty CallbackPermissionManager.
func New() *CallbackPermissionManager {
	return &CallbackPermissionManager{
		perms: make(map[string][]CallbackPermission),
	}
}

// Allow grants userID permission to trigger token for ttlSecs seconds.
// Multiple calls for the same (userID, token) pair append a new grant
// (the most recent unexpired one wins during IsAllowed checks).
func (m *CallbackPermissionManager) Allow(userID int64, token string, ttlSecs int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	perm := CallbackPermission{
		UserID:    userID,
		Token:     token,
		ExpiresAt: time.Now().Add(time.Duration(ttlSecs) * time.Second),
	}
	m.perms[token] = append(m.perms[token], perm)
}

// IsAllowed returns true if userID holds a valid (non-expired) permission for
// token, or if the token has been marked allow-all.
func (m *CallbackPermissionManager) IsAllowed(userID int64, token string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	for _, p := range m.perms[token] {
		if p.ExpiresAt.Before(now) {
			continue
		}
		if p.UserID == userID || p.UserID == 0 { // 0 = allow-all sentinel
			return true
		}
	}
	return false
}

// Revoke removes all permissions (for any user) associated with token.
func (m *CallbackPermissionManager) Revoke(token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.perms, token)
}

// Cleanup removes all expired permissions across every token.
// Call periodically to avoid unbounded memory growth.
func (m *CallbackPermissionManager) Cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for token, grants := range m.perms {
		live := grants[:0]
		for _, p := range grants {
			if p.ExpiresAt.After(now) {
				live = append(live, p)
			}
		}
		if len(live) == 0 {
			delete(m.perms, token)
		} else {
			m.perms[token] = live
		}
	}
}

// AllowAll marks token as accessible by any user for ttlSecs seconds.
// Internally stored as a permission with UserID == 0 (the allow-all sentinel).
func (m *CallbackPermissionManager) AllowAll(token string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Long-lived sentinel: 24 hours — callers should Revoke when done.
	perm := CallbackPermission{
		UserID:    0, // allow-all
		Token:     token,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	// Prepend so IsAllowed finds it quickly.
	m.perms[token] = append([]CallbackPermission{perm}, m.perms[token]...)
	_ = allowAllSentinel // keep sentinel const referenced
}

// IsAllAllowed returns true if the token has an active allow-all grant
// (i.e. it was registered via AllowAll and has not expired or been revoked).
func (m *CallbackPermissionManager) IsAllAllowed(token string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	for _, p := range m.perms[token] {
		if p.UserID == 0 && p.ExpiresAt.After(now) {
			return true
		}
	}
	return false
}
