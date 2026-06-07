// Package cache provides a thread-safe in-memory TTL cache with LRU eviction.
// Ported from core/lib/time/cache.py (TTLCache).
package cache

import (
	"sync"
	"time"
)

// entry holds a cached value along with its expiry time.
type entry struct {
	value     interface{}
	expiresAt time.Time
}

// TTLCache is a thread-safe in-memory key/value cache where every entry has
// a time-to-live. When the cache is full the least-recently-used entry is
// evicted (LRU), mirroring the OrderedDict behaviour in the Python original.
type TTLCache struct {
	mu         sync.RWMutex
	entries    map[string]*entry
	order      []string // insertion/access order – back = most-recently-used
	maxSize    int
	defaultTTL time.Duration
}

// New creates a TTLCache with the given maximum size and default TTL.
// maxSize <= 0 means unlimited.
func New(maxSize int, defaultTTL time.Duration) *TTLCache {
	cap := maxSize
	if cap <= 0 {
		cap = 64 // initial map allocation when unlimited
	}
	return &TTLCache{
		entries:    make(map[string]*entry, cap),
		order:      make([]string, 0, cap),
		maxSize:    maxSize,
		defaultTTL: defaultTTL,
	}
}

// moveToEnd marks key as the most-recently-used item.
// Must be called with mu held (write or suitable lock).
func (c *TTLCache) moveToEnd(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	c.order = append(c.order, key)
}

// evictLRU removes the least-recently-used entry.
// Must be called with write lock held.
func (c *TTLCache) evictLRU() {
	for len(c.order) > 0 {
		lru := c.order[0]
		c.order = c.order[1:]
		if _, ok := c.entries[lru]; ok {
			delete(c.entries, lru)
			return
		}
	}
}

// cleanupExpiredLocked removes all expired entries.
// Must be called with write lock held.
func (c *TTLCache) cleanupExpiredLocked() {
	now := time.Now()
	newOrder := c.order[:0]
	for _, k := range c.order {
		if e, ok := c.entries[k]; ok && e.expiresAt.After(now) {
			newOrder = append(newOrder, k)
		} else {
			delete(c.entries, k)
		}
	}
	c.order = newOrder
}

// Set stores value under key. An optional ttl argument overrides the default
// TTL for this entry (use 0 or omit to use the cache default).
func (c *TTLCache) Set(key string, value interface{}, ttl ...time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	d := c.defaultTTL
	if len(ttl) > 0 && ttl[0] > 0 {
		d = ttl[0]
	}

	if _, exists := c.entries[key]; !exists {
		// New entry: ensure capacity.
		if c.maxSize > 0 && len(c.entries) >= c.maxSize {
			c.cleanupExpiredLocked()
			if len(c.entries) >= c.maxSize {
				c.evictLRU()
			}
		}
	}

	c.entries[key] = &entry{
		value:     value,
		expiresAt: time.Now().Add(d),
	}
	c.moveToEnd(key)
}

// Get returns the value stored under key and whether it was found and
// non-expired. Expired entries are deleted on access.
func (c *TTLCache) Get(key string) (interface{}, bool) {
	// Fast-path: read lock.
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()

	if !ok {
		return nil, false
	}
	if time.Now().After(e.expiresAt) {
		// Upgrade to write lock for deletion.
		c.mu.Lock()
		if e2, ok2 := c.entries[key]; ok2 && time.Now().After(e2.expiresAt) {
			delete(c.entries, key)
		}
		c.mu.Unlock()
		return nil, false
	}

	// Mark as recently used.
	c.mu.Lock()
	c.moveToEnd(key)
	c.mu.Unlock()

	return e.value, true
}

// GetOrSet returns the cached value for key if it exists and is not expired.
// Otherwise it calls fn to compute the value, stores it, and returns it.
// An optional ttl overrides the default TTL for the computed entry.
func (c *TTLCache) GetOrSet(key string, fn func() interface{}, ttl ...time.Duration) interface{} {
	if v, ok := c.Get(key); ok {
		return v
	}
	v := fn()
	c.Set(key, v, ttl...)
	return v
}

// Delete removes key from the cache. No-op if the key is absent.
func (c *TTLCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
	// Remove from order slice.
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
}

// Clear removes all entries from the cache.
func (c *TTLCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*entry, 64)
	c.order = c.order[:0]
}

// Size returns the number of non-expired entries currently in the cache.
func (c *TTLCache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cleanupExpiredLocked()
	return len(c.entries)
}

// Keys returns the keys of all non-expired entries.
func (c *TTLCache) Keys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cleanupExpiredLocked()
	keys := make([]string, 0, len(c.entries))
	for k := range c.entries {
		keys = append(keys, k)
	}
	return keys
}

// Cleanup removes expired entries. Useful to call periodically.
func (c *TTLCache) Cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cleanupExpiredLocked()
}

// StartAutoCleanup launches a background goroutine that calls Cleanup every
// interval. It returns a stop function that cancels the goroutine.
func (c *TTLCache) StartAutoCleanup(interval time.Duration) (stop func()) {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				c.Cleanup()
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}
