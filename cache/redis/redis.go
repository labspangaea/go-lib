// Package redis provides a Redis-backed cache implementation with JSON serialization.
package redis

import (
	"context"
	"encoding/json"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Cache is a Redis-backed cache. Values are marshaled to/from JSON by default;
// supply WithMarshal and WithUnmarshal to override.
// The zero value is not usable; construct with New.
type Cache[V any] struct {
	client    *goredis.Client
	prefix    string
	marshal   func(V) ([]byte, error)
	unmarshal func([]byte, *V) error
}

// New returns a Cache backed by client, configured by the supplied options.
func New[V any](client *goredis.Client, opts ...Option[V]) *Cache[V] {
	c := &Cache[V]{
		client:    client,
		marshal:   func(v V) ([]byte, error) { return json.Marshal(v) },
		unmarshal: func(b []byte, v *V) error { return json.Unmarshal(b, v) },
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Get returns the value stored under key. Returns (zero, false, nil) on a
// cache miss. Returns a non-nil error only on a Redis or deserialization failure.
func (c *Cache[V]) Get(ctx context.Context, key string) (v V, ok bool, err error) {
	data, err := c.client.Get(ctx, c.prefixedKey(key)).Bytes()
	if err == goredis.Nil {
		return v, false, nil
	}
	if err != nil {
		return v, false, err
	}
	if err = c.unmarshal(data, &v); err != nil {
		return v, false, err
	}
	return v, true, nil
}

// Set stores value under key with the given TTL. A ttl of 0 (cache.NoTTL)
// stores the entry without an expiry.
func (c *Cache[V]) Set(ctx context.Context, key string, value V, ttl time.Duration) error {
	data, err := c.marshal(value)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, c.prefixedKey(key), data, ttl).Err()
}

// Delete removes the entry for key. Deleting a non-existent key is a no-op.
func (c *Cache[V]) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, c.prefixedKey(key)).Err()
}

// MGet fetches multiple keys in one Redis MGET command (single round trip).
// Missing keys have a nil pointer in the returned map — never an error.
// Implements cache.BatchGetter[V].
func (c *Cache[V]) MGet(ctx context.Context, keys []string) (map[string]V, error) {
	prefixed := make([]string, len(keys))
	for i, k := range keys {
		prefixed[i] = c.prefixedKey(k)
	}

	vals, err := c.client.MGet(ctx, prefixed...).Result()
	if err != nil {
		return nil, err
	}

	out := make(map[string]V, len(keys))
	for i, raw := range vals {
		if raw == nil {
			continue // cache miss — key absent from map
		}
		b, ok := raw.(string)
		if !ok {
			continue
		}
		var v V
		if err = c.unmarshal([]byte(b), &v); err != nil {
			continue // skip undecodable entry; treat as miss
		}
		out[keys[i]] = v // return under original (un-prefixed) key
	}
	return out, nil
}

// MSet writes all entries using a single pipelined SET with per-key TTL.
// Every entry receives the same ttl; TTL jitter is applied by the caller before MSet.
// Implements cache.BatchSetter[V].
func (c *Cache[V]) MSet(ctx context.Context, entries map[string]V, ttl time.Duration) error {
	_, err := c.client.Pipelined(ctx, func(pipe goredis.Pipeliner) error {
		for key, val := range entries {
			b, marshalErr := c.marshal(val)
			if marshalErr != nil {
				return marshalErr
			}
			pipe.Set(ctx, c.prefixedKey(key), b, ttl)
		}
		return nil
	})
	return err
}

func (c *Cache[V]) prefixedKey(key string) string {
	if c.prefix == "" {
		return key
	}
	return c.prefix + ":" + key
}
