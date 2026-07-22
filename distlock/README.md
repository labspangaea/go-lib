# distlock

Generic distributed-lock abstraction for Go services, with a concrete Redis backend.

All backends satisfy the same `distlock.Locker` and `distlock.Lock` interfaces. Swapping from Redis to etcd (or any future backend) is a wiring change — no service-layer code changes.

---

## Package layout

```
distlock/         ← Lock, Locker interfaces; sentinel errors; WithLock helper
distlock/redis/   ← Redis backend (go-redis/v9, single-node)
distlock/mocks/   ← MockLocker, MockLock (generated — do not edit)
```

---

## SOLID design

| Principle | Applied here |
|---|---|
| **SRP** | `Lock` manages one lock instance; `Locker` creates locks — two responsibilities, two types |
| **OCP** | New backends (etcd, Postgres advisory locks, ZooKeeper) = new sub-packages, no interface changes |
| **LSP** | Any `Locker` substitutes another — service layer unchanged |
| **ISP** | Services that only need non-blocking acquire depend on `TryLock` contract; services that need blocking depend on `Lock` |
| **DIP** | Service layers depend on `distlock.Locker`, not `*redis.Locker` |

---

## Interfaces

```go
// Locker creates named Lock instances. Safe for concurrent use.
type Locker interface {
    NewLock(key string, ttl time.Duration, opts ...LockOption) Lock
}

// Lock is a single distributed lock. NOT safe for concurrent use —
// create one per goroutine/operation via Locker.NewLock.
type Lock interface {
    TryLock(ctx context.Context) (bool, error)  // single non-blocking attempt
    Lock(ctx context.Context) error              // blocking with retry
    Unlock(ctx context.Context) error            // atomic release
    Refresh(ctx context.Context) error           // extend TTL
    Token() string                               // fencing token
    Key() string                                 // resource name
}
```

### Sentinel errors

| Error | When |
|---|---|
| `distlock.ErrNotHeld` | `Unlock` or `Refresh` called when this instance no longer owns the lock |
| `distlock.ErrNotAcquired` | `Lock` ctx expired before the lock became available |

---

## Redis backend (`distlock/redis`)

```go
import "github.com/labspangaea/go-lib/distlock/redis"
```

### Construction

```go
client := goredis.NewClient(&goredis.Options{Addr: "redis:6379"})

locker := redis.NewLocker(client)
```

The client lifecycle is owned by the caller. `Locker` and all `Lock` instances it creates share the same client.

### TryLock — non-blocking

```go
mu := locker.NewLock("payments:order-42", 30*time.Second)

ok, err := mu.TryLock(ctx)
if err != nil {
    return err // I/O error
}
if !ok {
    return errors.New("order already being processed")
}
defer mu.Unlock(ctx)

// critical section
```

### Lock — blocking with retry

```go
mu := locker.NewLock("reports:monthly",
    5*time.Minute,
    distlock.WithRetryDelay(200*time.Millisecond),
    distlock.WithRetryJitter(100*time.Millisecond),
)

ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

if err := mu.Lock(ctx); err != nil {
    // distlock.ErrNotAcquired — nobody released within 30s
    return err
}
defer mu.Unlock(context.Background())

// critical section
```

**Retry behaviour:** each attempt waits `RetryDelay + rand[0, RetryJitter)` before retrying. Jitter prevents thundering-herd when many goroutines compete for the same lock.

### WithLock — try-finally convenience

```go
mu := locker.NewLock("inventory:sku-9", 10*time.Second)

err := distlock.WithLock(ctx, mu, func(ctx context.Context) error {
    return deductStock(ctx, skuID, qty)
})
```

`WithLock` acquires the lock, calls `fn`, then releases — even if `fn` returns an error or the context is cancelled. If `fn` succeeds but `Unlock` returns `ErrNotHeld` (the lock expired mid-operation), `WithLock` surfaces that error so the caller knows another holder may have entered the critical section.

### Refresh — long-running critical sections

When the critical section may outlast the lock TTL, refresh the TTL from a background goroutine:

```go
mu := locker.NewLock("etl:nightly", 30*time.Second)
if err := mu.Lock(ctx); err != nil {
    return err
}

// Refresh goroutine: extend TTL every 20s while holding the lock
refreshCtx, refreshCancel := context.WithCancel(ctx)
go func() {
    ticker := time.NewTicker(20 * time.Second)
    defer ticker.Stop()
    for {
        select {
        case <-refreshCtx.Done():
            return
        case <-ticker.C:
            if err := mu.Refresh(context.Background()); err != nil {
                // distlock.ErrNotHeld — lock expired; abort the operation
                refreshCancel()
                return
            }
        }
    }
}()

err := runETL(refreshCtx)
refreshCancel()
_ = mu.Unlock(context.Background())
```

---

## Fencing tokens

A process can be paused longer than the lock TTL (GC, OS scheduling, paging), resume, and still believe it holds the lock. The only safe defence is a **fencing token**: pass `mu.Token()` to every storage write; the storage layer rejects writes from stale tokens.

```go
mu := locker.NewLock("orders:42", 10*time.Second)
if err := mu.Lock(ctx); err != nil {
    return err
}
defer mu.Unlock(ctx)

// Pass the token as an optimistic-concurrency guard to the DB write.
// The query fails if the token in the lock_tokens table has moved on.
_, err = db.ExecContext(ctx,
    `UPDATE orders SET status = $1 WHERE id = $2 AND lock_token = $3`,
    "processing", orderID, mu.Token(),
)
```

Without fencing tokens, a slow process can corrupt data after its lock expired — `Unlock` returning `ErrNotHeld` is the signal that this may have happened.

---

## Redis implementation details

| Operation | Redis command | Atomic? |
|---|---|---|
| **TryLock** | `SET key token NX PX ttl_ms` | Yes — set-if-not-exists in one command |
| **Unlock** | Lua: `GET` + `DEL` if token matches | Yes — Lua executes atomically |
| **Refresh** | Lua: `GET` + `PEXPIRE` if token matches | Yes — Lua executes atomically |

The unique token (UUID v4) ensures only the holder that set the key can release or refresh it. Without this, a slow holder could accidentally delete a lock acquired by a different process after the slow holder's TTL expired.

---

## Single-node vs. Redlock

This backend uses single-node Redis. The Redlock algorithm (quorum across ≥3 nodes) was proposed for high-availability but has known issues under process pauses regardless of quorum size. **Fencing tokens are the correct solution.** If you need a lock that survives Redis restarts, use etcd or ZooKeeper as a future backend — the interface is the same.

---

## Wiring in a service

```go
// service layer — depends on the interface (DIP)
type OrderService struct {
    locker distlock.Locker
}

func (s *OrderService) ProcessOrder(ctx context.Context, id string) error {
    mu := s.locker.NewLock("orders:"+id, 30*time.Second)
    return distlock.WithLock(ctx, mu, func(ctx context.Context) error {
        return s.process(ctx, id)
    })
}

// main.go — wire Redis (no change in OrderService)
client := goredis.NewClient(&goredis.Options{Addr: "redis:6379"})
locker := redistlock.NewLocker(client)
svc := NewOrderService(locker)
```

---

## Graceful shutdown

The locker holds no background goroutines. Shutdown order:

```go
// 1. Stop accepting new requests (HTTP drain)
srv.Shutdown(drainCtx)

// 2. Active handlers finish — any in-flight WithLock calls complete + unlock
wg.Wait()

// 3. Close Redis client (all lock operations already done)
client.Close()
```

---

## Testing with mocks

```go
import (
    "github.com/labspangaea/go-lib/distlock/mocks"
    "go.uber.org/mock/gomock"
)

func TestOrderService_ProcessOrder(t *testing.T) {
    ctrl := gomock.NewController(t)

    mockLock := mocks.NewMockLock(ctrl)
    mockLock.EXPECT().Lock(gomock.Any()).Return(nil)
    mockLock.EXPECT().Unlock(gomock.Any()).Return(nil)

    mockLocker := mocks.NewMockLocker(ctrl)
    mockLocker.EXPECT().
        NewLock("orders:42", 30*time.Second).
        Return(mockLock)

    svc := NewOrderService(mockLocker)
    err := svc.ProcessOrder(context.Background(), "42")
    require.NoError(t, err)
}
```

Regenerate mocks after changing the interface:

```bash
go generate ./distlock/...
```
