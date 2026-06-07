// Package loader – inline_manager.go
//
// InlineManager is the Go port of MCUB's Python InlineManager (inline.py).
// It manages temporary inline callback handlers with TTL-based expiry and
// per-user access control.
package loader

import (
	"sync"
	"time"
)

// InlineCallbackEntry stores a single callback handler registration.
type InlineCallbackEntry struct {
	// Handler is the function to call when the callback is triggered.
	// The concrete type depends on the caller; InlineManager treats it as opaque.
	Handler interface{}

	// ExpiresAt is the wall-clock time after which this entry is considered
	// expired and will be removed by Cleanup.
	ExpiresAt time.Time

	// AllowAll, when true, permits any user to trigger the callback.
	AllowAll bool

	// AllowedUsers is the explicit list of user IDs permitted to trigger the
	// callback when AllowAll is false.
	AllowedUsers []int64
}

// IsExpired reports whether the entry has passed its expiry time.
func (e *InlineCallbackEntry) IsExpired() bool {
	return time.Now().After(e.ExpiresAt)
}

// InlineManager manages temporary inline callback handlers keyed by a UUID
// string. It is safe for concurrent use.
type InlineManager struct {
	mu        sync.RWMutex
	callbacks map[string]*InlineCallbackEntry
}

// NewInlineManager creates an empty InlineManager.
func NewInlineManager() *InlineManager {
	return &InlineManager{
		callbacks: make(map[string]*InlineCallbackEntry),
	}
}

// Register stores a callback handler under the given key.
// If a handler with the same key already exists it is replaced.
func (m *InlineManager) Register(key string, entry *InlineCallbackEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callbacks[key] = entry
}

// Get returns the handler entry for key if it exists and has not expired.
// A second boolean return value indicates whether the entry was found.
// Expired entries are NOT automatically removed here; call Cleanup explicitly.
func (m *InlineManager) Get(key string) (*InlineCallbackEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.callbacks[key]
	if !ok {
		return nil, false
	}
	if entry.IsExpired() {
		return nil, false
	}
	return entry, true
}

// Delete removes the handler for key. It is a no-op if key does not exist.
func (m *InlineManager) Delete(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.callbacks, key)
}

// Cleanup scans all entries and removes any that have expired.
// It returns the number of entries removed.
func (m *InlineManager) Cleanup() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	removed := 0
	for key, entry := range m.callbacks {
		if now.After(entry.ExpiresAt) {
			delete(m.callbacks, key)
			removed++
		}
	}
	return removed
}

// IsAllowed checks whether userID is permitted to trigger the callback for key.
//
//   - Returns false if the key does not exist or has expired.
//   - Returns true if AllowAll is set on the entry.
//   - Returns true if userID appears in AllowedUsers.
//   - Returns false otherwise.
func (m *InlineManager) IsAllowed(key string, userID int64) bool {
	entry, ok := m.Get(key)
	if !ok {
		return false
	}
	if entry.AllowAll {
		return true
	}
	for _, id := range entry.AllowedUsers {
		if id == userID {
			return true
		}
	}
	return false
}

// Len returns the number of currently stored entries (including expired ones
// that haven't been cleaned up yet).
func (m *InlineManager) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.callbacks)
}
