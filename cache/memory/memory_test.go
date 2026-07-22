package memory_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/labspangaea/go-lib/cache/memory"
)

var ctx = context.Background()

func TestGetMiss(t *testing.T) {
	c := memory.New[string]()
	v, ok, err := c.Get(ctx, "missing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected miss")
	}
	if v != "" {
		t.Fatalf("expected zero value, got %q", v)
	}
}

func TestSetAndGet(t *testing.T) {
	c := memory.New[int]()
	if err := c.Set(ctx, "answer", 42, 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	v, ok, err := c.Get(ctx, "answer")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("expected hit")
	}
	if v != 42 {
		t.Fatalf("expected 42, got %d", v)
	}
}

func TestSetOverwrite(t *testing.T) {
	c := memory.New[string]()
	_ = c.Set(ctx, "k", "v1", 0)
	_ = c.Set(ctx, "k", "v2", 0)

	v, ok, _ := c.Get(ctx, "k")
	if !ok || v != "v2" {
		t.Fatalf("expected v2, got %q (ok=%v)", v, ok)
	}
	if c.Len() != 1 {
		t.Fatalf("overwrite should not increase Len, got %d", c.Len())
	}
}

func TestDelete(t *testing.T) {
	c := memory.New[string]()
	_ = c.Set(ctx, "k", "v", 0)
	_ = c.Delete(ctx, "k")

	_, ok, _ := c.Get(ctx, "k")
	if ok {
		t.Fatal("expected miss after delete")
	}
}

func TestDeleteMissIsNoOp(t *testing.T) {
	c := memory.New[string]()
	if err := c.Delete(ctx, "nonexistent"); err != nil {
		t.Fatalf("Delete nonexistent should not error: %v", err)
	}
}

func TestTTLExpiry(t *testing.T) {
	c := memory.New[string]()
	_ = c.Set(ctx, "short", "value", 50*time.Millisecond)

	// Immediately should hit.
	_, ok, _ := c.Get(ctx, "short")
	if !ok {
		t.Fatal("expected hit before expiry")
	}

	// Wait for expiry.
	time.Sleep(80 * time.Millisecond)

	_, ok, _ = c.Get(ctx, "short")
	if ok {
		t.Fatal("expected miss after TTL expiry")
	}
}

func TestNoTTLNeverExpires(t *testing.T) {
	c := memory.New[string]()
	_ = c.Set(ctx, "forever", "value", 0) // NoTTL

	// Sanity check — should always be available.
	v, ok, _ := c.Get(ctx, "forever")
	if !ok || v != "value" {
		t.Fatal("NoTTL entry should never expire")
	}
}

func TestLRUEviction(t *testing.T) {
	c := memory.New[string](memory.WithCapacity[string](2))
	_ = c.Set(ctx, "a", "1", 0)
	_ = c.Set(ctx, "b", "2", 0)
	_ = c.Set(ctx, "c", "3", 0) // should evict "a" (LRU)

	_, ok, _ := c.Get(ctx, "a")
	if ok {
		t.Fatal("expected 'a' to be evicted")
	}

	v, ok, _ := c.Get(ctx, "b")
	if !ok || v != "2" {
		t.Fatal("'b' should still be present")
	}

	v, ok, _ = c.Get(ctx, "c")
	if !ok || v != "3" {
		t.Fatal("'c' should still be present")
	}
}

func TestLRUEvictionRespectsAccessOrder(t *testing.T) {
	c := memory.New[string](memory.WithCapacity[string](2))
	_ = c.Set(ctx, "a", "1", 0)
	_ = c.Set(ctx, "b", "2", 0)

	// Access "a" so "b" becomes LRU.
	_, _, _ = c.Get(ctx, "a")

	_ = c.Set(ctx, "c", "3", 0) // should evict "b" (LRU)

	_, ok, _ := c.Get(ctx, "b")
	if ok {
		t.Fatal("expected 'b' to be evicted after 'a' was accessed")
	}

	_, ok, _ = c.Get(ctx, "a")
	if !ok {
		t.Fatal("'a' should survive — it was recently accessed")
	}
}

func TestExpiredItemsEvictedBeforeLRU(t *testing.T) {
	c := memory.New[string](memory.WithCapacity[string](2))
	_ = c.Set(ctx, "expired", "old", 50*time.Millisecond)
	_ = c.Set(ctx, "fresh", "new", 0)

	time.Sleep(80 * time.Millisecond)

	// This Set should evict "expired" first (it's expired), NOT "fresh".
	_ = c.Set(ctx, "third", "3", 0)

	_, ok, _ := c.Get(ctx, "fresh")
	if !ok {
		t.Fatal("'fresh' should survive — expired item should be evicted first")
	}
}

func TestLen(t *testing.T) {
	c := memory.New[int]()
	if c.Len() != 0 {
		t.Fatalf("empty cache should have Len 0, got %d", c.Len())
	}

	_ = c.Set(ctx, "a", 1, 0)
	_ = c.Set(ctx, "b", 2, 0)
	if c.Len() != 2 {
		t.Fatalf("expected Len 2, got %d", c.Len())
	}

	_ = c.Delete(ctx, "a")
	if c.Len() != 1 {
		t.Fatalf("expected Len 1 after delete, got %d", c.Len())
	}
}

func TestConcurrentAccess(t *testing.T) {
	c := memory.New[int](memory.WithCapacity[int](100))
	const goroutines = 50
	const ops = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := range goroutines {
		go func(id int) {
			defer wg.Done()
			for i := range ops {
				key := "key-" + string(rune('A'+id%26)) + "-" + string(rune('0'+i%10))
				_ = c.Set(ctx, key, i, time.Millisecond*10)
				_, _, _ = c.Get(ctx, key)
				_ = c.Delete(ctx, key)
			}
		}(g)
	}

	wg.Wait()
	// No race detector panic = pass.
}
