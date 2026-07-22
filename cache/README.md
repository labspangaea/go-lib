# cache

Generic, interface-driven cache abstractions for Go services, with concrete in-memory (LRU + TTL) and Redis backends.

---

## Package layout

```
cache/            ← interfaces + Nop (zero external dependencies)
cache/memory/     ← in-process LRU cache with per-entry TTL (stdlib only)
cache/redis/      ← Redis-backed cache with JSON serialization (go-redis/v9)
cache/couchbase/  ← Couchbase KV-backed cache with JSON serialization (gocb/v2)
```

Import only what you need. A service that defines its own in-memory backend only imports `cache` for the interface. A service that uses Redis adds `cache/redis`. Tests that want a discard sink import nothing extra — `cache.Nop` is in the root package.

---

## Interfaces

```go
type Getter[V any] interface {
    Get(ctx context.Context, key string) (V, bool, error)
}

type Setter[V any] interface {
    Set(ctx context.Context, key string, value V, ttl time.Duration) error
}

type Deleter interface {
    Delete(ctx context.Context, key string) error
}

type Cache[V any] interface {
    Getter[V]
    Setter[V]
    Deleter
}
```

**Accept the narrowest interface at every call site.** A function that only reads from the cache should declare `cache.Getter[V]`, not `cache.Cache[V]`. This follows the Interface Segregation Principle and makes the dependency explicit — callers that receive a `Getter` cannot accidentally write to the cache.

### Get contract

- Returns `(value, true, nil)` on a hit.
- Returns `(zero, false, nil)` on a miss or an expired entry — a miss is **never** an error.
- Returns a non-nil error only for I/O or serialization failures.

### Set contract

- `ttl == 0` (`cache.NoTTL`) means the entry never expires.
- Updating an existing key replaces the value and resets the TTL.

### Delete contract

- Deleting a non-existent key is a no-op, not an error.

---

## In-memory cache (`cache/memory`)

Goroutine-safe LRU with optional per-entry TTL. No external dependencies.

```go
import "github.com/labspangaea/go-lib/cache/memory"

c := memory.New[*User](
    memory.WithCapacity(1000), // evict LRU when full; 0 = unlimited
)

// Write
_ = c.Set(ctx, "user:42", user, 5*time.Minute)

// Read
u, ok, err := c.Get(ctx, "user:42")

// Delete
_ = c.Delete(ctx, "user:42")
```

**Eviction strategy:**
When the cache reaches capacity on a `Set`, expired entries are swept first. If the cache is still full after the sweep, the least recently used entry is removed. `Get` also evicts an entry lazily if it finds it expired. There is no background goroutine, so no `Close` is needed.

---

## Redis cache (`cache/redis`)

Redis-backed cache with JSON serialization by default.

```go
import (
    goredis "github.com/redis/go-redis/v9"
    "github.com/labspangaea/go-lib/cache/redis"
)

client := goredis.NewClient(&goredis.Options{Addr: "redis:6379"})

c := redis.New[*User](client,
    redis.WithKeyPrefix("users:prod"),   // stored as "users:prod:<key>"
)

// Write
_ = c.Set(ctx, "42", user, 10*time.Minute)

// Read
u, ok, err := c.Get(ctx, "42")

// Delete
_ = c.Delete(ctx, "42")
```

### Custom serialization

Swap JSON for msgpack, protobuf, or anything else:

```go
c := redis.New[*User](client,
    redis.WithMarshal(func(u *User) ([]byte, error) {
        return proto.Marshal(u)
    }),
    redis.WithUnmarshal(func(b []byte, u **User) error {
        *u = new(User)
        return proto.Unmarshal(b, *u)
    }),
)
```

---

## Couchbase KV cache (`cache/couchbase`)

Couchbase KV-backed cache with JSON serialization by default. Uses the `gocb/v2` SDK's `RawBinaryTranscoder` so serialization is fully controlled by this package — the Couchbase transcoder never re-encodes values.

```go
import (
    "github.com/couchbase/gocb/v2"
    "github.com/labspangaea/go-lib/cache/couchbase"
)

cluster, _ := gocb.Connect("couchbase://localhost", gocb.ClusterOptions{
    Username: "Administrator",
    Password: "password",
})
col := cluster.Bucket("my-bucket").DefaultCollection()

c := couchbase.New[*User](col,
    couchbase.WithKeyPrefix[*User]("users:prod"), // stored as "users:prod:<key>"
)

// Write
_ = c.Set(ctx, "42", user, 10*time.Minute)

// Read
u, ok, err := c.Get(ctx, "42")

// Delete
_ = c.Delete(ctx, "42")
```

### Context and timeouts

Couchbase KV operations use the context deadline as the operation timeout when present:

```go
ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
defer cancel()

u, ok, err := c.Get(ctx, "42") // aborts after 200 ms
```

If no deadline is set, the Couchbase SDK applies its default KV timeout (2.5 s).

### Custom serialization

```go
c := couchbase.New[*User](col,
    couchbase.WithMarshal(func(u *User) ([]byte, error) {
        return proto.Marshal(u)
    }),
    couchbase.WithUnmarshal(func(b []byte, u **User) error {
        *u = new(User)
        return proto.Unmarshal(b, *u)
    }),
)
```

---

## Nop (tests and disabled caching)

```go
var c cache.Cache[*User] = cache.Nop[*User]()
```

`Nop` discards all writes and always returns a miss. Use it in unit tests to satisfy a `cache.Cache` dependency without any caching side effects.

---

## Swapping backends without changing business logic

Because all backends satisfy the same `cache.Cache[V]` interface, the choice of backend is a wiring decision, not a code change:

```go
// Development — fast, no external dependency
var c cache.Cache[*Order] = memory.New[*Order](memory.WithCapacity(500))

// Production — shared across replicas (Redis)
var c cache.Cache[*Order] = redis.New[*Order](redisClient,
    redis.WithKeyPrefix("orders:prod"),
)

// Production — shared across replicas (Couchbase)
var c cache.Cache[*Order] = couchbase.New[*Order](col,
    couchbase.WithKeyPrefix[*Order]("orders:prod"),
)

// Tests — no side effects
var c cache.Cache[*Order] = cache.Nop[*Order]()

// Service layer is unchanged regardless of which backend is wired in
func NewOrderService(c cache.Cache[*Order]) *OrderService { ... }
```

---

## Cache-aside pattern (`cache.Aside`)

`Aside` implements the lazy-loading pattern in one call: try cache → on miss, fetch from source → write back. Cache write failures are logged but never propagated — the caller always gets their data.

```go
import "github.com/labspangaea/go-lib/cache"

func (s *UserService) GetUser(ctx context.Context, id int64) (*User, error) {
    key := fmt.Sprintf("users:%d", id)  // caller owns key construction
    return cache.Aside(ctx, s.cache, key, 5*time.Minute,
        func(ctx context.Context) (*User, error) {
            return s.db.FindUser(ctx, id)  // called only on a cache miss
        },
    )
}
```

**Contract:**
- Returns the cached value immediately on a hit — `fn` is never called.
- Calls `fn` on a miss. If `fn` returns `(nil, nil)` (resource does not exist), nothing is cached and `(nil, nil)` is returned.
- If `fn` returns an error, the error is returned and nothing is cached.
- Cache write failures are logged at `WARN` level using the context logger but do not fail the call.

**Key construction is the caller's responsibility.** `Aside` does not read environment variables or apply any prefix. Name keys explicitly:

```go
// Good: caller controls the namespace
key := fmt.Sprintf("orders:prod:%d", orderID)

// Avoid: burying env-var reads inside the helper
key = os.Getenv("APP_NAME") + prefixKey + fmt.Sprint(id)  // hidden dependency, untestable
```

**TTL is a plain `time.Duration`.** If the TTL depends on the fetched data (e.g., an expiry timestamp in the response), compute it before calling:

```go
token, err := cache.Aside(ctx, s.cache, key, cache.NoTTL,
    func(ctx context.Context) (*Token, error) {
        return s.auth.FetchToken(ctx)
    },
)
// then re-cache with the correct TTL derived from token.ExpiresAt
```

---

## Design notes

**Generics over `any`** — `Cache[V any]` eliminates type assertions at every call site. Each cache instance is bound to one value type, which is the right model: a user cache and an order cache are different objects with different eviction policies and TTLs.

**Miss is not an error** — Distinguishing miss from error with a `bool` return (like a map lookup) means callers write straightforward `if !ok { fetch from DB }` logic rather than checking against a sentinel error value.

**Narrow interfaces at call sites** — Declare `cache.Getter[V]` in functions that only read. This documents intent, makes testing simpler (any struct with a `Get` method qualifies), and prevents callers from accidentally writing or deleting through a reference they should only be reading.
