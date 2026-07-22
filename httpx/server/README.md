# httpx/server

Server-side `net/http` middleware for structured logging and OpenTelemetry tracing on incoming requests.

OTel span creation and W3C trace-context propagation are handled by the official
[`otelhttp`](https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp)
contrib package. This package adds only what `otelhttp` does not: request-ID generation,
context-logger stamping, structured request/response logging, and panic recovery.

---

## Installation

```go
import "github.com/labspangaea/go-lib/httpx/server"
```

---

## Middleware

| Middleware | What it does |
|---|---|
| `Chain` | Composes a stack — first argument is outermost |
| `Recover(log)` | Catches panics, logs stack trace, responds HTTP 500 |
| `RequestID()` | Reads or generates `X-Request-ID`, stores in context logger |
| `otelhttp.NewMiddleware` | Creates OTel span, extracts incoming W3C trace context *(external package)* |
| `Logging(log)` | Stamps `trace_id`/`span_id` on context logger, logs one summary line per request |

### Recommended order

```go
import (
    "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
    "github.com/labspangaea/go-lib/httpx/server"
)

http.Handle("/", server.Chain(mux,
    server.Recover(log),                    // outermost — catches panics from everything below
    server.RequestID(),                     // generate/read X-Request-ID before any logging
    otelhttp.NewMiddleware("my-service"),   // create span; must run before Logging
    server.Logging(log),                    // stamp trace IDs on logger, log request summary
))
```

**Why this order matters:**

- `Recover` is outermost so it catches panics from every middleware and handler below it.
- `RequestID` runs before `otelhttp` so the request ID is in the context logger when the span starts.
- `otelhttp` must run before `Logging` — `Logging` reads the active span from context to extract `trace_id` and `span_id`.

---

## How context flows through the stack

```
Incoming request
    → Recover       stores nothing; wraps everything in a deferred recover
    → RequestID     calls logger.WithRequestID(ctx, id)
                    context logger now has: request_id
    → otelhttp      creates span, puts it in context
    → Logging       calls logger.WithTraceContext(ctx, traceID, spanID)
                    context logger now has: request_id + trace_id + span_id
                    calls logger.WithLogger(ctx, reqLog.With(method, path))
                    context logger now has: request_id + trace_id + span_id + http.method + url.path
    → Handler       logger.FromContext(ctx) returns the fully enriched logger
```

Any `logger.FromContext(ctx)` call inside a handler produces log lines that automatically
carry all five fields — no manual wiring required.

---

## Middleware reference

### `Chain`

```go
server.Chain(h http.Handler, middleware ...func(http.Handler) http.Handler) http.Handler
```

First middleware in the list is the outermost. Equivalent to writing:

```go
Recover(RequestID(otelhttp.NewMiddleware(...)(Logging(h))))
```

### `RequestID`

Reads `X-Request-ID` from the request header. If absent, generates a random 16-character
hex string. Echoes the ID in the response header and stores it in the context logger via
`logger.WithRequestID`.

```go
id := r.Header.Get(server.HeaderRequestID) // read from context or header
```

### `Logging`

Logs one line per request after the handler returns. Fields logged:

| Field | Source |
|---|---|
| `http.method` | `r.Method` |
| `url.path` | `r.URL.Path` |
| `http.response.status_code` | captured from `ResponseWriter` |
| `duration_ms` | wall time from middleware entry to handler return |
| `trace_id`, `span_id` | extracted from the active OTel span |
| `request_id` | set by `RequestID` middleware |

Log level: `INFO` for status < 500, `ERROR` for status ≥ 500.

### `Recover`

Defers a `recover()` around the entire handler chain. On panic:
- Logs `error` (the panic value) and `stack` (full goroutine stack) at ERROR level
- Writes HTTP 500 to the client

Uses the `log` passed at construction — not the context logger — because `Recover` is
outermost and the context has not yet been enriched when the deferred function runs.

---

## Inside the handler

```go
func orderHandler(w http.ResponseWriter, r *http.Request) {
    // Logger is pre-loaded with request_id, trace_id, span_id, method, path.
    log := logger.FromContext(r.Context())

    log.Info("processing order", slog.String("order_id", id))

    // Start a child span for a downstream operation.
    ctx, span := tracer.Start(r.Context(), "db.query")
    defer span.End()

    // ...
}
```

---

## Framework integrations

All four frameworks implement `http.Handler`, so `server.Chain` wraps them directly —
one pattern, four frameworks. The middleware stack is identical in every case.

### Chi ★ recommended

Chi's middleware interface is `func(http.Handler) http.Handler` — the exact same shape
as `server.Chain`. Zero adapter code.

```go
import "github.com/go-chi/chi/v5"

r := chi.NewRouter()
r.Get("/orders/{id}", func(w http.ResponseWriter, r *http.Request) {
    log := logger.FromContext(r.Context()) // fully enriched logger
    id  := chi.URLParam(r, "id")
    // ...
})

http.ListenAndServe(":8080", server.Chain(r,
    server.Recover(log),
    server.RequestID(),
    otelhttp.NewMiddleware("my-service"),
    server.Logging(log),
))
```

### Gorilla Mux

```go
import "github.com/gorilla/mux"

r := mux.NewRouter()
r.HandleFunc("/orders/{id}", func(w http.ResponseWriter, r *http.Request) {
    log  := logger.FromContext(r.Context())
    vars := mux.Vars(r) // {"id": "42"}
    // ...
}).Methods("GET")

http.ListenAndServe(":8080", server.Chain(r,
    server.Recover(log),
    server.RequestID(),
    otelhttp.NewMiddleware("my-service"),
    server.Logging(log),
))
```

### Gin

Use `gin.New()`, not `gin.Default()` — `gin.Default()` adds Gin's own logger and
recovery middleware which duplicate what our stack already provides.

```go
import "github.com/gin-gonic/gin"

router := gin.New()
router.GET("/orders/:id", func(c *gin.Context) {
    // Access the enriched logger via c.Request.Context(), not c directly.
    log := logger.FromContext(c.Request.Context())
    id  := c.Param("id")
    // ...
})

http.ListenAndServe(":8080", server.Chain(router,
    server.Recover(log),
    server.RequestID(),
    otelhttp.NewMiddleware("my-service"),
    server.Logging(log),
))
```

### Echo

```go
import "github.com/labstack/echo/v4"

e := echo.New()
// Do not call e.Use() with Echo's built-in Logger/Recover — our stack replaces them.
e.GET("/orders/:id", func(c echo.Context) error {
    // Access the enriched logger via c.Request().Context().
    log := logger.FromContext(c.Request().Context())
    id  := c.Param("id")
    return c.JSON(http.StatusOK, result)
})

http.ListenAndServe(":8080", server.Chain(e,
    server.Recover(log),
    server.RequestID(),
    otelhttp.NewMiddleware("my-service"),
    server.Logging(log),
))
```

---

## Framework comparison

### Performance (router throughput, single core)

| Framework | Router algorithm | Approx. req/s |
|---|---|---|
| **Gin** | Radix tree (httprouter fork) | ~150–170K |
| **Echo** | Radix tree | ~120–140K |
| **stdlib `net/http`** | Map + trie | ~150K |
| **Chi** | Radix tree | ~90K |
| **Gorilla Mux** | Sequential regex match | ~20–40K |

> Numbers are routing-only microbenchmarks. In a real service the DB query dominates; router throughput is rarely the bottleneck.

### Pros & cons

| Framework | Pros | Cons |
|---|---|---|
| **Chi** | Native `func(http.Handler) http.Handler` middleware — zero adapter; URL params in `r.Context()`; stdlib-idiomatic; no framework types in handler signatures | Fewer built-in features than Gin/Echo |
| **Gin** | Fastest router; large ecosystem; built-in JSON binding, validation, rendering | Own middleware type (`gin.HandlerFunc`); must use `c.Request.Context()` not `r.Context()`; `gin.Default()` adds duplicate middleware |
| **Echo** | Fast; clean API; built-in binder and validator | Own middleware type; `c.Request().Context()` accessor; slightly more boilerplate |
| **Gorilla Mux** | Mature; regex routes; host/method/scheme matching; reverse routing | Slowest router by far; briefly archived in 2022 |

### Recommendation

**Chi** for new services in this library's ecosystem. Its middleware shape is identical
to `server.Chain` — no glue code, no type conversions, no surprises. URL params, logger,
and trace context all live in the same `r.Context()`.

**Gin** or **Echo** are reasonable if the team already uses them or needs their built-in
binding/validation. Avoid `gin.Default()` and Echo's built-in logger/recovery when using
this middleware stack.

**Gorilla Mux** only if regex-based routing or reverse URL generation is a hard requirement.

---

## Why `net/http` and not `fasthttp`

`fasthttp` can handle ~1–2M req/s versus `net/http`'s ~150–300K, but that raw number
almost never matters. Here is why this library is built on `net/http`:

### 1. `context.Context` is non-negotiable for this library

The entire logger and telemetry stack is built on `context.Context`:
`logger.WithRequestID`, `logger.WithTraceContext`, `logger.FromContext`,
`trace.SpanFromContext`. `fasthttp` does not use `context.Context` on the request
hot-path — it uses `RequestCtx.SetUserValue` instead. Bridging the two requires a
separate adapter on every handler, destroying the "zero wiring" goal.

### 2. OpenTelemetry has no official `fasthttp` support

`go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` is the official,
spec-compliant OTel package for HTTP. There is no equivalent for `fasthttp`.
`otelfiber` exists for Fiber specifically, but it is a community package, not an
OTel project, and covers only Fiber's handler model.

### 3. `fasthttp` pools objects — you cannot hold references safely

`fasthttp` reuses `*RequestCtx` across requests via `sync.Pool`. You must copy any
value you want to keep before the handler returns. This makes storing context values,
passing requests to goroutines, or streaming responses significantly more dangerous
and error-prone.

### 4. No HTTP/2 or HTTP/3

`fasthttp` is HTTP/1.1 only. `net/http` supports HTTP/2 out of the box and HTTP/3
via `quic-go`. Services that need multiplexing, server push, or modern protocol
support cannot use `fasthttp`.

### 5. The performance gap rarely matters in practice

A typical service spends its time here:

```
DB query          5–50 ms   ████████████████████
External HTTP     2–20 ms   ████████
JSON marshal      50–200 µs ▌
net/http overhead 1–5 µs    ·   ← fasthttp saves this
```

`net/http` at 300K req/s is already more than most services ever sustain.
The fasthttp gain is real in proxy/gateway workloads that do nothing but
forward bytes — it is invisible next to a single database round-trip.

### When fasthttp IS the right choice

| Scenario | net/http | fasthttp |
|---|---|---|
| REST API / microservice | ✅ | overkill |
| gRPC service | ✅ (required) | ❌ incompatible |
| OTel tracing | ✅ official support | ⚠️ community only |
| High-throughput reverse proxy | ✅ sufficient | ✅ justified |
| Edge gateway, millions conn/s | ⚠️ may bottleneck | ✅ designed for this |
| HTTP/2 or HTTP/3 | ✅ | ❌ |

Use `fasthttp` when profiling proves `net/http` is the specific bottleneck **and** you
are willing to give up `context.Context`, OTel, HTTP/2, and the entire `net/http`
middleware ecosystem.

---

## Graceful shutdown

Graceful shutdown means: **stop accepting new requests, drain in-flight ones, then close downstream resources**. Terminating abruptly (SIGKILL or `server.Close()`) cuts open connections mid-response and leaves external resources (database transactions, pubsub acks) in an undefined state.

### How `http.Server.Shutdown` works

```
SIGTERM received
    │
    ├── server.Shutdown(ctx) called
    │       closes the listener immediately — no new connections accepted
    │       waits for all active handlers to return (or ctx to expire)
    │
    └── in-flight handlers: still running
            their request contexts are NOT cancelled by Shutdown
            they complete normally, write their responses, and return
```

`server.Shutdown(ctx)` returns `nil` when all handlers have returned, or `ctx.Err()` if the drain deadline expires first (connections are then forcibly closed). `server.Close()` is the abrupt variant — it closes immediately without draining.

---

## Case 1 — API service

An API service accepts HTTP traffic, may publish messages, and has no long-running background consumers.

### Shutdown sequence

```
SIGTERM
  → srv.Shutdown(ctx)   stop listener; drain in-flight HTTP handlers
  → pub.Close()         flush any buffered publishes
  → shutdownTelemetry   flush spans and metrics
```

### Example

```go
package main

import (
    "context"
    "log/slog"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

    "github.com/labspangaea/go-lib/httpx/server"
    "github.com/labspangaea/go-lib/logger"
    "github.com/labspangaea/go-lib/pubsub/kafka"
    "github.com/labspangaea/go-lib/telemetry"
)

func main() {
    log := logger.New(logger.Config{Level: slog.LevelInfo})

    shutdownTelemetry, _ := telemetry.Setup(context.Background(), telemetry.Config{
        ServiceName: "order-api",
    })

    pub := kafka.NewPublisher([]string{"kafka:9092"})

    mux := http.NewServeMux()
    mux.HandleFunc("/orders", ordersHandler(pub))

    srv := &http.Server{
        Addr: ":8080",
        Handler: server.Chain(mux,
            server.Recover(log),
            server.RequestID(),
            otelhttp.NewMiddleware("order-api"),
            server.Logging(log),
        ),
        ReadTimeout:  15 * time.Second,
        WriteTimeout: 30 * time.Second,
        IdleTimeout:  60 * time.Second,
    }

    go func() {
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Error("server error", slog.Any("error", err))
            os.Exit(1)
        }
    }()
    log.Info("api started", slog.String("addr", ":8080"))

    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
    <-quit
    log.Info("shutdown signal received")

    // 1. Stop accepting; drain active HTTP handlers (30 s budget)
    drainCtx, drainCancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer drainCancel()
    if err := srv.Shutdown(drainCtx); err != nil {
        log.Error("http drain timeout", slog.Any("error", err))
    }

    // 2. Flush any in-flight publishes
    _ = pub.Close()

    // 3. Flush spans and metrics
    telemCtx, telemCancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer telemCancel()
    shutdownTelemetry(telemCtx)

    log.Info("shutdown complete")
}
```

### `ReadTimeout` / `WriteTimeout` and shutdown

`Shutdown` honours `WriteTimeout` — handlers that have already started writing are given `WriteTimeout` to finish. Set it long enough for your slowest streaming response. `ReadTimeout` guards the initial request read and does not affect shutdown.

### Liveness vs readiness probes

If Kubernetes probes share the same port, they stop responding as soon as `Shutdown` is called. Run probes on a separate server and close it *after* the main server drains:

```go
probeSrv := &http.Server{Addr: ":9090", Handler: probesMux}
go probeSrv.ListenAndServe()

// ... SIGTERM received ...

srv.Shutdown(drainCtx)  // 1. drain main traffic
probeSrv.Close()        // 2. close probes after traffic stops
```

---

## Case 2 — Consumer service

A consumer service runs one or more `Subscribe` loops and has no HTTP listener. Its only concern is finishing the current message before the process exits.

### Shutdown sequence

```
SIGTERM
  → appCancel()         cancel the context → Subscribe fetch loops exit
  → wg.Wait()           wait for each Subscribe to return (current message finishes + ack/commit)
  → con.Close()         release broker resources
  → shutdownTelemetry   flush spans and metrics
```

### Example (Kafka)

```go
package main

import (
    "context"
    "log/slog"
    "os"
    "os/signal"
    "sync"
    "syscall"
    "time"

    "github.com/labspangaea/go-lib/logger"
    "github.com/labspangaea/go-lib/pubsub"
    "github.com/labspangaea/go-lib/pubsub/kafka"
    "github.com/labspangaea/go-lib/telemetry"
)

func main() {
    log := logger.New(logger.Config{Level: slog.LevelInfo})

    shutdownTelemetry, _ := telemetry.Setup(context.Background(), telemetry.Config{
        ServiceName: "invoice-consumer",
    })

    con := kafka.NewConsumer([]string{"kafka:9092"})

    appCtx, appCancel := context.WithCancel(context.Background())
    var wg sync.WaitGroup

    topics := []string{"orders.created", "orders.cancelled"}
    for _, topic := range topics {
        wg.Add(1)
        go func(t string) {
            defer wg.Done()
            if err := con.Subscribe(appCtx, t, "invoice-consumer", handleMessage); err != nil {
                log.Error("subscribe error", slog.String("topic", t), slog.Any("error", err))
            }
        }(topic)
    }
    log.Info("consumer started", slog.Any("topics", topics))

    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
    <-quit
    log.Info("shutdown signal received — draining")

    // 1. Stop all fetch loops; in-flight handlers complete before Subscribe returns
    appCancel()
    wg.Wait()

    // 2. Release broker resources
    _ = con.Close()

    // 3. Flush spans and metrics
    telemCtx, telemCancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer telemCancel()
    shutdownTelemetry(telemCtx)

    log.Info("shutdown complete")
}

func handleMessage(ctx context.Context, msg pubsub.Message) error {
    // ctx is context.WithoutCancel — DB/HTTP calls here are NOT cancelled on shutdown
    return processInvoice(ctx, msg.Payload)
}
```

### Example (RabbitMQ)

```go
package main

import (
    "context"
    "log/slog"
    "os"
    "os/signal"
    "sync"
    "syscall"
    "time"

    amqp091 "github.com/rabbitmq/amqp091-go"

    "github.com/labspangaea/go-lib/logger"
    "github.com/labspangaea/go-lib/pubsub"
    "github.com/labspangaea/go-lib/pubsub/rabbitmq"
    "github.com/labspangaea/go-lib/telemetry"
)

func main() {
    log := logger.New(logger.Config{Level: slog.LevelInfo})

    shutdownTelemetry, _ := telemetry.Setup(context.Background(), telemetry.Config{
        ServiceName: "invoice-consumer",
    })

    conn, err := amqp091.Dial("amqp://guest:guest@rabbitmq:5672/")
    if err != nil {
        log.Error("rabbitmq dial", slog.Any("error", err))
        os.Exit(1)
    }

    con := rabbitmq.NewConsumer(conn, "orders")

    appCtx, appCancel := context.WithCancel(context.Background())
    var wg sync.WaitGroup

    wg.Add(1)
    go func() {
        defer wg.Done()
        if err := con.Subscribe(appCtx, "orders.created", "invoice-consumer", handleMessage); err != nil {
            log.Error("subscribe error", slog.Any("error", err))
        }
    }()
    log.Info("consumer started")

    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
    <-quit
    log.Info("shutdown signal received — draining")

    // 1. Stop fetch loop; in-flight handler finishes + ack before Subscribe returns
    appCancel()
    wg.Wait()

    // 2. Close connection LAST — all channels must be done first
    _ = conn.Close()

    // 3. Flush spans and metrics
    telemCtx, telemCancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer telemCancel()
    shutdownTelemetry(telemCtx)

    log.Info("shutdown complete")
}

func handleMessage(ctx context.Context, msg pubsub.Message) error {
    return processInvoice(ctx, msg.Payload)
}
```
