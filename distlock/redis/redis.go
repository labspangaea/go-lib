// Package redis provides a Redis-backed implementation of distlock.Locker using
// the go-redis/v9 driver.
//
// Acquire: atomic SET key token NX PX ttl_ms — succeeds only when the key does not exist.
// Unlock:  Lua script — delete key only when the stored value equals our token.
// Refresh: Lua script — PEXPIRE key only when the stored value equals our token.
//
// All three operations are atomic from Redis's perspective; no WATCH/MULTI needed.
package redis

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"github.com/labspangaea/go-lib/distlock"
)

// Lua scripts keep unlock and refresh atomic: they read and conditionally
// write in a single round-trip, preventing TOCTOU races between holders.

// unlockScript deletes the key only if the stored value equals ARGV[1] (our token).
// Returns 1 on success, 0 if the key is absent or owned by another holder.
const unlockScript = `
if redis.call("get", KEYS[1]) == ARGV[1] then
    return redis.call("del", KEYS[1])
else
    return 0
end`

// refreshScript resets the TTL only if the stored value equals ARGV[1].
// ARGV[2] is the new TTL in milliseconds.
// Returns 1 on success, 0 if not held.
const refreshScript = `
if redis.call("get", KEYS[1]) == ARGV[1] then
    return redis.call("pexpire", KEYS[1], ARGV[2])
else
    return 0
end`

// Locker creates Redis-backed Lock instances.
// Safe for concurrent use; the underlying Redis client is shared across Locks.
type Locker struct {
	client *goredis.Client
}

// NewLocker creates a Locker backed by client.
// The client lifecycle is owned by the caller — call client.Close() on shutdown.
func NewLocker(client *goredis.Client) *Locker {
	return &Locker{client: client}
}

// NewLock creates a Lock for key with the given TTL.
// The Lock is not yet acquired — call TryLock or Lock to acquire it.
// A fresh UUID token is generated per call, so two NewLock calls on the same
// key produce independent lock instances that cannot interfere with each other.
func (l *Locker) NewLock(key string, ttl time.Duration, opts ...distlock.LockOption) distlock.Lock {
	cfg := &distlock.LockConfig{
		RetryDelay:  100 * time.Millisecond,
		RetryJitter: 50 * time.Millisecond,
	}
	for _, o := range opts {
		o(cfg)
	}
	return &lock{
		client: l.client,
		key:    key,
		token:  uuid.New().String(),
		ttl:    ttl,
		cfg:    cfg,
	}
}

type lock struct {
	client *goredis.Client
	key    string
	token  string
	ttl    time.Duration
	cfg    *distlock.LockConfig
}

func (l *lock) Key() string   { return l.key }
func (l *lock) Token() string { return l.token }

// TryLock performs a single SET key token NX PX ttl_ms.
// Atomic: either sets the key (returns true) or does nothing (returns false).
func (l *lock) TryLock(ctx context.Context) (bool, error) {
	_, err := l.client.SetArgs(ctx, l.key, l.token, goredis.SetArgs{
		Mode: "NX",
		TTL:  l.ttl,
	}).Result()
	if errors.Is(err, goredis.Nil) {
		// Key already exists; NX condition not met.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("distlock redis: try lock %s: %w", l.key, err)
	}
	return true, nil
}

// Lock retries TryLock until it succeeds or ctx expires.
// Each attempt waits RetryDelay + [0, RetryJitter) before the next try.
// Jitter spreads retries from concurrent waiters, reducing Redis pressure.
func (l *lock) Lock(ctx context.Context) error {
	for {
		ok, err := l.TryLock(ctx)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}

		delay := l.cfg.RetryDelay
		if l.cfg.RetryJitter > 0 {
			delay += time.Duration(rand.Int63n(int64(l.cfg.RetryJitter)))
		}
		select {
		case <-ctx.Done():
			return distlock.ErrNotAcquired
		case <-time.After(delay):
		}
	}
}

// Unlock atomically releases the lock.
// Returns distlock.ErrNotHeld if this instance no longer owns the key.
func (l *lock) Unlock(ctx context.Context) error {
	result, err := l.client.Eval(ctx, unlockScript, []string{l.key}, l.token).Int64()
	if err != nil {
		return fmt.Errorf("distlock redis: unlock %s: %w", l.key, err)
	}
	if result == 0 {
		return distlock.ErrNotHeld
	}
	return nil
}

// Refresh resets the TTL to the original duration atomically.
// Returns distlock.ErrNotHeld if the lock has expired or been taken by another holder.
func (l *lock) Refresh(ctx context.Context) error {
	result, err := l.client.Eval(ctx, refreshScript, []string{l.key}, l.token, l.ttl.Milliseconds()).Int64()
	if err != nil {
		return fmt.Errorf("distlock redis: refresh %s: %w", l.key, err)
	}
	if result == 0 {
		return distlock.ErrNotHeld
	}
	return nil
}
