// Package distlock defines a minimal distributed-lock abstraction.
//
// # Design rationale
//
// A distributed lock must solve three problems:
//  1. Mutual exclusion — only one holder at a time.
//  2. Deadlock freedom — a crashed holder's lock eventually expires (TTL).
//  3. Fault isolation — releasing someone else's lock must be impossible.
//
// The interface uses a per-Lock unique token (set by the backend at NewLock time)
// to satisfy (3): unlock and refresh operations are atomic check-and-act — they
// only succeed when the stored value equals this instance's token.
//
// # Fencing tokens
//
// A process can be paused (GC, paging) longer than the lock TTL, then resume and
// believe it still holds the lock. The only safe defence is a fencing token: the
// caller passes Token() to every storage write; the storage layer rejects writes
// from stale tokens. Lock.Token() exposes the value for this purpose.
//
// # Single-node vs. Redlock
//
// The Redis backend uses single-node Redis. Redlock (multi-node quorum) does not
// provide stronger guarantees when process pauses are possible — fencing tokens
// are the correct solution regardless. Use etcd or ZooKeeper if you need a
// consensus-based lock without relying on fencing tokens.
//
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=distlock.go -destination=mocks/mock_distlock.go -package=mocks
package distlock

import (
	"context"
	"errors"
	"time"
)

// Sentinel errors returned by Lock operations.
var (
	// ErrNotHeld is returned by Unlock or Refresh when this instance no longer
	// holds the lock — it either expired or was taken by another holder.
	ErrNotHeld = errors.New("distlock: lock not held by this instance")

	// ErrNotAcquired is returned by Lock when ctx expires before the lock
	// could be acquired.
	ErrNotAcquired = errors.New("distlock: context expired before lock was acquired")
)

// Lock represents a single named distributed lock.
// A Lock value is NOT safe for concurrent use — acquire, use, and release within
// one goroutine. Create a new Lock per operation via Locker.NewLock.
type Lock interface {
	// TryLock makes one non-blocking attempt to acquire the lock.
	// Returns (true, nil) on success, (false, nil) when another holder owns
	// the lock, and a non-nil error on I/O failure.
	TryLock(ctx context.Context) (bool, error)

	// Lock blocks until the lock is acquired or ctx expires/is cancelled.
	// Retries with configurable delay and jitter. Returns ErrNotAcquired if ctx
	// expires before the lock becomes available.
	Lock(ctx context.Context) error

	// Unlock releases the lock atomically — it only succeeds when the stored
	// token matches this instance's token. Returns ErrNotHeld if the lock has
	// expired or been taken by another holder.
	Unlock(ctx context.Context) error

	// Refresh resets the lock TTL to its original duration. Call periodically
	// from a background goroutine when the critical section may outlast the TTL.
	// Returns ErrNotHeld if the lock has expired.
	Refresh(ctx context.Context) error

	// Token returns the unique value stored inside the lock. Pass it to
	// downstream storage writes as a fencing token to detect stale operations.
	Token() string

	// Key returns the lock's resource name.
	Key() string
}

// Locker creates distributed Lock instances for named resources.
// Implementations must be safe for concurrent use.
type Locker interface {
	NewLock(key string, ttl time.Duration, opts ...LockOption) Lock
}

// LockConfig holds options applied per Lock at NewLock time.
type LockConfig struct {
	// RetryDelay is the base delay between retry attempts in blocking Lock calls.
	// Default: 100ms.
	RetryDelay time.Duration

	// RetryJitter is the maximum random duration added to RetryDelay each attempt.
	// Jitter prevents thundering-herd when many callers wait on the same lock.
	// Default: 50ms.
	RetryJitter time.Duration
}

// LockOption configures a Lock at construction time.
type LockOption func(*LockConfig)

// WithRetryDelay sets the base delay between retry attempts for blocking Lock.
// Default: 100ms.
func WithRetryDelay(d time.Duration) LockOption {
	return func(c *LockConfig) { c.RetryDelay = d }
}

// WithRetryJitter sets the maximum random jitter added to each retry delay.
// Setting jitter to 0 disables randomisation. Default: 50ms.
func WithRetryJitter(d time.Duration) LockOption {
	return func(c *LockConfig) { c.RetryJitter = d }
}

// WithLock is a convenience wrapper: acquire l, run fn, release l.
//
// Unlock uses context.WithoutCancel so it succeeds even when ctx is already
// cancelled (e.g. request timeout fired inside fn).
//
// Return value priority:
//   - If fn returns nil and Unlock returns an error → return unlock error.
//     This matters: ErrNotHeld means the lock expired during fn, so another
//     holder may have entered the critical section — the caller should know.
//   - If fn returns an error → return fn error (unlock is best-effort).
func WithLock(ctx context.Context, l Lock, fn func(ctx context.Context) error) error {
	if err := l.Lock(ctx); err != nil {
		return err
	}
	fnErr := fn(ctx)
	if unlockErr := l.Unlock(context.WithoutCancel(ctx)); unlockErr != nil && fnErr == nil {
		return unlockErr
	}
	return fnErr
}
