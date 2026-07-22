package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/labspangaea/go-lib/cache"
)

func TestNop_GetAlwaysMisses(t *testing.T) {
	c := cache.Nop[string]()
	ctx := context.Background()

	v, ok, err := c.Get(ctx, "any-key")
	if err != nil {
		t.Fatalf("Nop.Get returned error: %v", err)
	}
	if ok {
		t.Fatal("Nop.Get should return false for ok")
	}
	if v != "" {
		t.Fatalf("Nop.Get should return zero value, got %q", v)
	}
}

func TestNop_SetAndDeleteAreNoOps(t *testing.T) {
	c := cache.Nop[int]()
	ctx := context.Background()

	if err := c.Set(ctx, "k", 42, time.Minute); err != nil {
		t.Fatalf("Nop.Set returned error: %v", err)
	}

	// Value should not be stored.
	_, ok, _ := c.Get(ctx, "k")
	if ok {
		t.Fatal("Nop should discard all writes")
	}

	if err := c.Delete(ctx, "k"); err != nil {
		t.Fatalf("Nop.Delete returned error: %v", err)
	}
}
