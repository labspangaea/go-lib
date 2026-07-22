# logger

Structured, context-aware logging for Go services, built on the standard library [`log/slog`](https://pkg.go.dev/log/slog).

**Zero external dependencies.** Outputs JSON in production, human-readable text in development. Field keys follow [OpenTelemetry Semantic Conventions v1.26](https://opentelemetry.io/docs/specs/semconv/) so log records align with your traces out of the box.

---

## Why `log/slog` instead of a custom interface or zap?

| Concern | Old approach | This package |
|---|---|---|
| Dependency | `go.uber.org/zap` | stdlib only (`log/slog`, Go ≥ 1.21) |
| Interface coupling | Custom `Logger` interface leaked `zap.Field` into every caller | `*slog.Logger` is the standard — no custom type to maintain |
| OTel integration | Manual field helpers returning `zap.Field` | Bridge via `go.opentelemetry.io/contrib/bridges/otelslog` — one line, no code changes |
| Dynamic log level | Not supported | Pass a `*slog.LevelVar` to `WithLevel` |
| Testing | Required a mock or stub | `logger.Nop()` discards all output; or swap in any `slog.Handler` |

`log/slog` is now the idiomatic choice in the Go ecosystem. Accepting it as the standard means you get free interoperability with any library that also uses `slog`, and you never need to write adapter code.

---

## Installation

```go
import "github.com/labspangaea/go-lib/logger"
```

No additional dependencies required — `log/slog` ships with Go 1.21+.

---

## Quick start

```go
log := logger.New(
    logger.WithServiceInfo("my-service", "v1.2.3", "instance-1"),
)

log.Info("server started", slog.String("addr", ":8080"))
log.Warn("cache miss", slog.String(logger.KeyRequestID, reqID))
log.Error("db query failed", slog.Any(logger.KeyError, err))
```

**Production output** (JSON, stdout):
```json
{"timestamp":"2026-04-23T08:00:00.000Z","level":"info","message":"server started","service.name":"my-service","service.version":"v1.2.3","service.instance.id":"instance-1","addr":":8080"}
```

**Development output** (text, stderr):
```
2026-04-23T08:00:00.000Z INFO  server started service.name=my-service addr=:8080
```

---

## Constructor options

```go
log := logger.New(
    logger.WithServiceInfo("api-gateway", "v2.0.0", "pod-abc123"),
    logger.WithLevel(slog.LevelDebug),
    logger.WithSource(),
)
```

| Option | Default | Description |
|---|---|---|
| `WithServiceInfo(name, version, id)` | — | Attaches `service.name`, `service.version`, `service.instance.id` to every entry |
| `WithLevel(slog.Leveler)` | `slog.LevelInfo` | Minimum level to emit. Accepts `slog.Level` or `*slog.LevelVar` for runtime changes |
| `WithDevelopment()` | — | Text encoding to stderr + source file/line on every entry |
| `WithSource()` | — | Adds source file and line number (JSON output) |
| `WithHandler(slog.Handler)` | — | Fan out every record to an additional `slog.Handler` alongside the primary stdout/stderr output |
| `WithOTelBridge(lp, minLevel)` | bridge off; min `LevelError` | Convenience: stream records at `>= minLevel` to OTLP via the supplied `LoggerProvider` (typically returned by `telemetry.Setup`). Lower-severity records stay stdout-only |

### Dynamic log level at runtime

```go
var lvl slog.LevelVar // defaults to Info

log := logger.New(
    logger.WithServiceInfo("worker", "v1.0.0", "i-001"),
    logger.WithLevel(&lvl),
)

// Later — no restart needed, takes effect on next log call
lvl.Set(slog.LevelDebug)
```

This is why `WithLevel` accepts `slog.Leveler` (the interface) rather than `slog.Level` (the value): a `*slog.LevelVar` satisfies `slog.Leveler` and lets you change the level at runtime, which is essential for diagnosing production issues without redeployment.

---

## Context propagation

Attach a logger to a request context once — retrieve it anywhere in the call chain without threading it through every function argument.

### Middleware (attach at request entry point)

```go
func LoggingMiddleware(log *slog.Logger) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            ctx := logger.WithLogger(r.Context(), log)
            ctx  = logger.WithRequestID(ctx, r.Header.Get("X-Request-ID"))
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

### Handler / service layer (retrieve anywhere downstream)

```go
func (s *OrderService) Create(ctx context.Context, order Order) error {
    log := logger.FromContext(ctx) // never nil — returns Nop() if not set

    log.Info("creating order", slog.String("order_id", order.ID))

    if err := s.repo.Insert(ctx, order); err != nil {
        log.Error("insert failed", slog.Any(logger.KeyError, err))
        return err
    }
    return nil
}
```

### OTel trace correlation

If you are using OpenTelemetry tracing, stamp the current trace and span IDs onto the logger once per span:

```go
span := trace.SpanFromContext(ctx)
sc   := span.SpanContext()

ctx = logger.WithTraceContext(ctx, sc.TraceID().String(), sc.SpanID().String())
```

Every log line emitted inside that context will carry `trace_id` and `span_id`, which log aggregators (Loki, Cloud Logging, Elastic) use to correlate logs with traces.

---

## OTel log export (optional)

To export log records to an OpenTelemetry pipeline without changing any logging call sites:

```go
import "go.opentelemetry.io/contrib/bridges/otelslog"

// provider is your configured sdklog.LoggerProvider
log := otelslog.NewLogger("my-service", otelslog.WithLoggerProvider(provider))
```

`otelslog.NewLogger` returns a `*slog.Logger` — the rest of your code is unchanged.

---

## Field key constants

`fields.go` exports the OTel semantic convention key strings so all services use consistent field names:

```go
slog.String(logger.KeyRequestID, id)       // "request_id"
slog.String(logger.KeyTraceID, traceID)    // "trace_id"
slog.String(logger.KeySpanID, spanID)      // "span_id"
slog.String(logger.KeyHTTPMethod, "GET")   // "http.method"
slog.Int(logger.KeyHTTPStatusCode, 200)    // "http.response.status_code"
slog.Any(logger.KeyError, err)             // "error"
slog.String(logger.KeyUserID, uid)         // "enduser.id"
```

Using these constants instead of raw strings prevents typos and makes log queries across services reliable.

---

## Testing

Use `logger.Nop()` when logging output is irrelevant to the test:

```go
func TestOrderService_Create(t *testing.T) {
    ctx := logger.WithLogger(context.Background(), logger.Nop())
    svc := NewOrderService(repo, ...)
    err := svc.Create(ctx, testOrder)
    // ...
}
```

`Nop()` returns a shared, pre-allocated logger that discards all output — no allocations per call, no noise in test output.

To assert log output in tests, inject a custom `slog.Handler`:

```go
var buf bytes.Buffer
log := slog.New(slog.NewJSONHandler(&buf, nil))
ctx := logger.WithLogger(context.Background(), log)

svc.Create(ctx, order)

// assert buf contains expected JSON fields
```

---

## Design decisions

**No custom `Logger` interface** — The previous version defined a `Logger` interface with `zap.Field` parameters. This created false decoupling: callers still had to import `go.uber.org/zap` to build field values, so the interface provided no real abstraction boundary. `*slog.Logger` is the actual standard — using it directly is simpler and more honest.

**`Handler().WithAttrs()` instead of `With(...any)`** — Base attributes set via `WithServiceInfo` are attached using `handler.WithAttrs([]slog.Attr)`. This is more efficient than `logger.With(...any)` because it bypasses the reflection-based argument parsing that `With` uses when it receives `any` values, and it enforces `[]slog.Attr` at compile time.

**`slog.DiscardHandler` for `Nop()`** — Rather than constructing a handler that writes to `io.Discard`, `slog.DiscardHandler` (added in Go 1.24) is the purpose-built zero-cost discard sink. The `nop` logger is a package-level variable, so `Nop()` and `FromContext` misses never allocate.

**`contextKey` is a private struct type** — Using `type contextKey struct{}` instead of a plain `string` or `int` as the context key prevents collisions with any other package that might store a value under the same string key.
