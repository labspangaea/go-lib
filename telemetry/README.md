# telemetry

Bootstraps the OpenTelemetry Go SDK for distributed tracing and structured log export via OTLP. Works with any OTLP-compatible backend — New Relic, Datadog, Elastic APM, Grafana, TrueWatch, Jaeger, or a self-hosted OpenTelemetry Collector. Both OTLP/gRPC (default) and OTLP/HTTP+protobuf are supported via `WithProtocol`.

---

## What this package does

`Setup` initialises two OTel providers in a single call:

| Provider | Exporter | Signal |
|---|---|---|
| `sdktrace.TracerProvider` | `otlptracegrpc` or `otlptracehttp` | Spans → OTLP backend |
| `sdklog.LoggerProvider` | `otlploggrpc` or `otlploghttp` | Log records → OTLP backend |

Both providers are registered as OTel globals and share the same resource attributes (service name, version, instance ID). A single `ShutdownFunc` flushes and stops both on process exit.

---

## Why two separate packages?

`logger` has **zero external dependencies** — it works with stdlib `log/slog` alone. `telemetry` owns the OTel SDK imports so services that do not need distributed tracing or OTLP export can import `logger` without pulling in gRPC, protobuf, and the OTel SDK.

---

## Installation

```go
import "github.com/labspangaea/go-lib/telemetry"
```

---

## Quick start

```go
func main() {
    ctx := context.Background()

    tracer, lp, shutdown, err := telemetry.Setup(ctx,
        telemetry.WithEndpoint("otel-collector.internal:4317"),
        telemetry.WithServiceInfo("my-service", "v1.0.0", os.Getenv("POD_NAME")),
        telemetry.WithInsecure(), // remove in production with TLS
    )
    if err != nil {
        log.Fatal(err)
    }
    defer shutdown(ctx)

    ctx, span := tracer.Start(ctx, "handle-request")
    defer span.End()
}
```

---

## Options

| Option | Default | Description |
|---|---|---|
| `WithEndpoint(addr)` | `localhost:4317` | OTLP endpoint of the collector or APM agent — `host:port` |
| `WithProtocol(p)` | `"grpc"` | OTLP wire protocol: `"grpc"` or `"http/protobuf"` |
| `WithServiceInfo(name, version, id)` | — | `service.name`, `service.version`, `service.instance.id` on every span and log record |
| `WithHeaders(map)` | — | Per-request headers — used for SaaS auth (e.g. New Relic `api-key`) |
| `WithInsecure()` | TLS enabled | Disables TLS — use for a local collector or non-production |
| `WithGzipCompression()` | no compression | Enables gzip on the OTLP exporter |
| `WithSampler(s)` | `AlwaysSample` | Override the trace sampler (e.g. `ParentBased(TraceIDRatioBased(0.1))`) |

---

## Shutdown

`ShutdownFunc` flushes all buffered spans and log records, then stops both exporters. Always call it before the process exits — pass a context with a deadline so it does not block indefinitely:

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
defer shutdown(ctx)
```

---

## Connecting `telemetry` and `logger`

The `logger` package accepts any `slog.Handler` via `WithHandler`. Use `otelslog.NewHandler` from `go.opentelemetry.io/contrib/bridges/otelslog` to create a bridge that routes every `slog` record through the OTel log pipeline.

```go
import (
    "go.opentelemetry.io/contrib/bridges/otelslog"

    "github.com/labspangaea/go-lib/logger"
    "github.com/labspangaea/go-lib/telemetry"
)

func main() {
    ctx := context.Background()

    // 1. Bootstrap OTel — get back the log provider.
    tracer, lp, shutdown, err := telemetry.Setup(ctx,
        telemetry.WithEndpoint("otel-collector.internal:4317"),
        telemetry.WithServiceInfo("api-gateway", "v2.0.0", os.Getenv("POD_NAME")),
        telemetry.WithInsecure(),
    )
    if err != nil {
        log.Fatal(err)
    }
    defer shutdown(ctx)

    // 2. Build the slog bridge from the log provider.
    bridge := otelslog.NewHandler("api-gateway", otelslog.WithLoggerProvider(lp))

    // 3. Create the logger — logs go to stdout (JSON) AND the OTLP backend simultaneously.
    log := logger.New(
        logger.WithServiceInfo("api-gateway", "v2.0.0", os.Getenv("POD_NAME")),
        logger.WithHandler(bridge),
    )

    // 4. Correlate logs with the active span.
    ctx, span := tracer.Start(ctx, "handle-request")
    defer span.End()

    sc  := span.SpanContext()
    ctx  = logger.WithTraceContext(ctx, sc.TraceID().String(), sc.SpanID().String())

    logger.FromContext(ctx).Info("request handled", slog.String("route", "/orders"))
}
```

Every log line emitted inside the span will carry `trace_id` and `span_id` as structured fields, so any APM backend or log aggregator can correlate them with the corresponding trace automatically.

---

## Backend compatibility

This package sends standard OTLP — no vendor SDK required. Point `WithEndpoint` at the collector or agent for your platform.

| Backend | Endpoint | Protocol | Auth |
|---|---|---|---|
| OpenTelemetry Collector (local) | `localhost:4317` | gRPC, insecure | — |
| New Relic (US) | `otlp.nr-data.net:4317` | gRPC, TLS | `WithHeaders({"api-key": "<license>"})` |
| New Relic (EU) | `otlp.eu01.nr-data.net:4317` | gRPC, TLS | `WithHeaders({"api-key": "<license>"})` |
| Datadog Agent | `<DD_AGENT_HOST>:4317` | gRPC, insecure | — |
| Elastic APM | `<apm-server-host>:8200` | http/protobuf, TLS | `WithHeaders({"Authorization": "Bearer <token>"})` |
| Grafana Cloud | `otlp-gateway-prod-us-central-0.grafana.net:443` | gRPC, TLS | `WithHeaders({"Authorization": "Basic <b64>"})` |
| TrueWatch / DataKit | `<datakit-host>:4317` | gRPC, insecure | — (DataKit holds the tenant token) |
| Jaeger (all-in-one) | `localhost:4317` | gRPC, insecure | — |

### Direct-to-SaaS example (New Relic)

```go
tracer, lp, shutdown, err := telemetry.Setup(ctx,
    telemetry.WithEndpoint("otlp.nr-data.net:4317"),
    telemetry.WithProtocol(telemetry.ProtocolGRPC),
    telemetry.WithServiceInfo("api-gateway", "v2.0.0", os.Getenv("POD_NAME")),
    telemetry.WithHeaders(map[string]string{"api-key": os.Getenv("NEW_RELIC_LICENSE_KEY")}),
)
```
