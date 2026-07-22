package cache

import (
	"context"
	"log/slog"
	"time"

	"github.com/labspangaea/go-lib/logger"
)

// Aside implements the cache-aside (lazy-loading) pattern:
//
//  1. Try the cache. On a hit, return immediately.
//  2. On a miss, call fn to fetch from the source of truth.
//  3. Write the result back to the cache (best-effort — a write failure is
//     logged but does not fail the call; the caller still gets their data).
//
// key must be a stable, unique string for this value — the caller is
// responsible for key construction and namespacing.
//
// ttl is passed directly to Cache.Set; use cache.NoTTL for no expiry.
//
// fn must return (nil, nil) when the resource does not exist.
// Aside treats a nil result as "nothing to cache" and returns (nil, nil).
func Aside[V any](
	ctx context.Context,
	c   Cache[V],
	key string,
	ttl time.Duration,
	fn  func(context.Context) (*V, error),
) (*V, error) {
	if v, ok, err := c.Get(ctx, key); err != nil {
		logger.FromContext(ctx).Warn("cache read failed",
			slog.String("key", key),
			slog.Any(logger.KeyError, err),
		)
	} else if ok {
		return &v, nil
	}

	v, err := fn(ctx)
	if err != nil || v == nil {
		return v, err
	}

	if err := c.Set(ctx, key, *v, ttl); err != nil {
		logger.FromContext(ctx).Warn("cache write failed",
			slog.String("key", key),
			slog.Any(logger.KeyError, err),
		)
	}
	return v, nil
}
