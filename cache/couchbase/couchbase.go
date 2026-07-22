// Package couchbase provides a Couchbase KV-backed cache implementation using the gocb/v2 SDK.
package couchbase

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/couchbase/gocb/v2"
)

// Cache is a Couchbase KV-backed cache. Values are marshaled to/from JSON by default;
// supply WithMarshal and WithUnmarshal to override.
// The zero value is not usable; construct with New.
type Cache[V any] struct {
	col       *gocb.Collection
	prefix    string
	marshal   func(V) ([]byte, error)
	unmarshal func([]byte, *V) error
}

// New returns a Cache backed by col, configured by the supplied options.
// col must be an authenticated *gocb.Collection obtained from a connected Cluster.
func New[V any](col *gocb.Collection, opts ...Option[V]) *Cache[V] {
	c := &Cache[V]{
		col:       col,
		marshal:   func(v V) ([]byte, error) { return json.Marshal(v) },
		unmarshal: func(b []byte, v *V) error { return json.Unmarshal(b, v) },
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Get returns the value stored under key.
// Returns (zero, false, nil) on a miss or an expired document.
// Returns a non-nil error only on a Couchbase or deserialization failure.
func (c *Cache[V]) Get(ctx context.Context, key string) (v V, ok bool, err error) {
	opts := &gocb.GetOptions{Transcoder: gocb.NewRawBinaryTranscoder()}
	if dl, hasDeadline := ctx.Deadline(); hasDeadline {
		opts.Timeout = time.Until(dl)
	}

	result, err := c.col.Get(c.prefixedKey(key), opts)
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		return v, false, nil
	}
	if err != nil {
		return v, false, err
	}

	var raw []byte
	if err = result.Content(&raw); err != nil {
		return v, false, err
	}
	if err = c.unmarshal(raw, &v); err != nil {
		return v, false, err
	}
	return v, true, nil
}

// Set stores value under key with the given TTL. A ttl of 0 (cache.NoTTL) stores
// the entry without an expiry.
func (c *Cache[V]) Set(ctx context.Context, key string, value V, ttl time.Duration) error {
	data, err := c.marshal(value)
	if err != nil {
		return err
	}

	opts := &gocb.UpsertOptions{
		Expiry:     ttl,
		Transcoder: gocb.NewRawBinaryTranscoder(),
	}
	if dl, hasDeadline := ctx.Deadline(); hasDeadline {
		opts.Timeout = time.Until(dl)
	}

	_, err = c.col.Upsert(c.prefixedKey(key), data, opts)
	return err
}

// Delete removes the entry for key. Deleting a non-existent key is a no-op.
func (c *Cache[V]) Delete(ctx context.Context, key string) error {
	opts := &gocb.RemoveOptions{}
	if dl, hasDeadline := ctx.Deadline(); hasDeadline {
		opts.Timeout = time.Until(dl)
	}

	_, err := c.col.Remove(c.prefixedKey(key), opts)
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		return nil
	}
	return err
}

func (c *Cache[V]) prefixedKey(key string) string {
	if c.prefix == "" {
		return key
	}
	return c.prefix + ":" + key
}
