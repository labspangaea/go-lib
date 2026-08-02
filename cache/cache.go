// Package cache defines a minimal, generic cache abstraction and a no-op implementation.
// Concrete backends (in-memory, Redis) live in sub-packages so callers import only what they need.
//
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=cache.go -destination=mocks/mock_cache.go -package=mocks
package cache

import (
	"context"
	"time"
)

// NoTTL passed to Set means the entry never expires.
const NoTTL time.Duration = 0

// Getter retrieves a single value by key.
// The second return is false on a cache miss; errors are reserved for I/O or
// serialization failures — a miss is never an error.
type Getter[V any] interface {
	Get(ctx context.Context, key string) (V, bool, error)
}

// Setter writes a value with an optional TTL. Use NoTTL for no expiry.
type Setter[V any] interface {
	Set(ctx context.Context, key string, value V, ttl time.Duration) error
}

// Deleter removes an entry. Deleting a non-existent key is a no-op, not an error.
type Deleter interface {
	Delete(ctx context.Context, key string) error
}

// Cache is the full read-write interface. Callers that only read should depend
// on Getter; callers that only write should depend on Setter. This follows the
// Interface Segregation Principle — accept the narrowest interface you need.
type Cache[V any] interface {
	Getter[V]
	Setter[V]
	Deleter
}

// BatchGetter reads multiple keys in a single round trip.
// A nil value in the returned map means the key was not found (cache miss).
// Implemented by backends that support batch reads (e.g. Redis MGET via pipeline).
type BatchGetter[V any] interface {
	MGet(ctx context.Context, keys []string) (map[string]V, error)
}

// BatchSetter writes multiple key-value pairs in a single round trip.
// Implemented by backends that support pipelined writes (e.g. Redis pipeline SET).
type BatchSetter[V any] interface {
	MSet(ctx context.Context, entries map[string]V, ttl time.Duration) error
}

// nop discards all writes and always reports a miss.
type nop[V any] struct{}

// Nop returns a Cache that silently discards all writes and always returns a
// cache miss (zero, false, nil). Callers cannot distinguish a Nop miss from a
// real backend miss — this is by design: treat Nop as "caching is disabled."
// Use as a safe default in tests or when caching is disabled.
func Nop[V any]() Cache[V] { return nop[V]{} }

func (nop[V]) Get(_ context.Context, _ string) (v V, _ bool, _ error)      { return }
func (nop[V]) Set(_ context.Context, _ string, _ V, _ time.Duration) error { return nil }
func (nop[V]) Delete(_ context.Context, _ string) error                    { return nil }
