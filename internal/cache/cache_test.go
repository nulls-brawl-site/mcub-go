package cache_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nulls-brawl-site/mcub-go/internal/cache"
)

func TestSetGet(t *testing.T) {
	c := cache.New(100, time.Minute)
	c.Set("key1", "value1")
	v, ok := c.Get("key1")
	if !ok || v != "value1" {
		t.Fatalf("expected value1, got %v (ok=%v)", v, ok)
	}
}

func TestGetMissing(t *testing.T) {
	c := cache.New(100, time.Minute)
	_, ok := c.Get("nonexistent")
	if ok {
		t.Fatal("expected miss for nonexistent key")
	}
}

func TestExpiry(t *testing.T) {
	c := cache.New(100, 50*time.Millisecond)
	c.Set("key", "val")
	time.Sleep(100 * time.Millisecond)
	_, ok := c.Get("key")
	if ok {
		t.Fatal("expected key to expire")
	}
}

func TestGetOrSet(t *testing.T) {
	c := cache.New(100, time.Minute)
	calls := 0
	fn := func() interface{} {
		calls++
		return "computed"
	}

	v1 := c.GetOrSet("k", fn)
	v2 := c.GetOrSet("k", fn)

	if v1 != "computed" || v2 != "computed" {
		t.Fatalf("unexpected values: %v, %v", v1, v2)
	}
	if calls != 1 {
		t.Fatalf("expected fn called once, got %d", calls)
	}
}

func TestDelete(t *testing.T) {
	c := cache.New(100, time.Minute)
	c.Set("key", "val")
	c.Delete("key")
	_, ok := c.Get("key")
	if ok {
		t.Fatal("key should have been deleted")
	}
	// Delete non-existent key should not panic
	c.Delete("nonexistent")
}

func TestClear(t *testing.T) {
	c := cache.New(100, time.Minute)
	for i := 0; i < 10; i++ {
		c.Set(fmt.Sprintf("key%d", i), i)
	}
	c.Clear()
	if s := c.Size(); s != 0 {
		t.Fatalf("expected size 0 after clear, got %d", s)
	}
}

func TestMaxSize(t *testing.T) {
	c := cache.New(3, time.Minute)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	c.Set("d", 4) // should evict LRU ("a")
	if s := c.Size(); s > 3 {
		t.Fatalf("size %d exceeds max 3", s)
	}
}

func TestLRUEviction(t *testing.T) {
	c := cache.New(2, time.Minute)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Get("a")    // access "a" → "b" becomes LRU
	c.Set("c", 3) // should evict "b"
	_, ok := c.Get("b")
	if ok {
		t.Fatal("b should have been evicted by LRU")
	}
	v, ok := c.Get("a")
	if !ok || v != 1 {
		t.Fatalf("a should still be present, got %v ok=%v", v, ok)
	}
}

func TestCustomTTL(t *testing.T) {
	c := cache.New(100, 10*time.Second) // default 10s
	// Override with short TTL
	c.Set("short", "v", 60*time.Millisecond)
	c.Set("long", "v2") // uses default TTL
	time.Sleep(100 * time.Millisecond)
	_, ok1 := c.Get("short")
	_, ok2 := c.Get("long")
	if ok1 {
		t.Fatal("short-lived key should have expired")
	}
	if !ok2 {
		t.Fatal("long-lived key should still be present")
	}
}

func TestOverwrite(t *testing.T) {
	c := cache.New(100, time.Minute)
	c.Set("key", "v1")
	c.Set("key", "v2")
	v, ok := c.Get("key")
	if !ok || v != "v2" {
		t.Fatalf("expected v2, got %v", v)
	}
}

func TestKeys(t *testing.T) {
	c := cache.New(100, time.Minute)
	c.Set("x", 1)
	c.Set("y", 2)
	keys := c.Keys()
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
}

func TestSize(t *testing.T) {
	c := cache.New(100, time.Minute)
	if c.Size() != 0 {
		t.Fatal("expected empty cache")
	}
	c.Set("k", "v")
	if c.Size() != 1 {
		t.Fatal("expected size 1")
	}
}

func TestAutoCleanup(t *testing.T) {
	c := cache.New(100, 50*time.Millisecond)
	c.Set("k", "v")
	stop := c.StartAutoCleanup(30 * time.Millisecond)
	defer stop()
	time.Sleep(120 * time.Millisecond)
	if c.Size() != 0 {
		t.Fatal("expected auto-cleanup to remove expired entries")
	}
}

func TestConcurrentAccess(t *testing.T) {
	c := cache.New(50, time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("key%d", i)
			c.Set(key, i)
			c.Get(key)
		}(i)
	}
	wg.Wait()
}
