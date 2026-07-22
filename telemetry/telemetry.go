// Package telemetry bootstraps the OpenTelemetry SDK for distributed tracing
// and structured log export via OTLP. It works with any OTLP-compatible
// backend: New Relic, Datadog, Elastic APM, Grafana, TrueWatch, or a local
// OpenTelemetry Collector.
//
// Both OTLP/gRPC (default) and OTLP/HTTP+protobuf are supported via WithProtocol.
package telemetry

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// ShutdownFunc flushes buffered spans and log records, then stops all exporters.
// Always call it — typically via defer — before the process exits.
type ShutdownFunc func(ctx context.Context) error

// Setup initialises TracerProvider and LoggerProvider, registers both as OTel
// globals, and returns:
//   - a Tracer scoped to the configured service name
//   - a LoggerProvider to pass to otelslog.NewHandler for log bridging
//   - a ShutdownFunc to flush and stop on process exit
//
// The providers export via OTLP to the endpoint configured with WithEndpoint
// (default: localhost:4317) using the protocol selected by WithProtocol
// (default: grpc).
func Setup(ctx context.Context, opts ...Option) (trace.Tracer, otellog.LoggerProvider, ShutdownFunc, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(cfg)
	}

	res, err := buildResource(ctx, cfg)
	if err != nil {
		return nil, nil, nil, err
	}

	tp, err := buildTracerProvider(ctx, cfg, res)
	if err != nil {
		return nil, nil, nil, err
	}
	otel.SetTracerProvider(tp)

	lp, err := buildLoggerProvider(ctx, cfg, res)
	if err != nil {
		_ = tp.Shutdown(ctx)
		return nil, nil, nil, err
	}
	global.SetLoggerProvider(lp)

	shutdown := func(ctx context.Context) error {
		return errors.Join(tp.Shutdown(ctx), lp.Shutdown(ctx))
	}

	return tp.Tracer(cfg.serviceName), lp, shutdown, nil
}

func buildResource(ctx context.Context, cfg *config) (*resource.Resource, error) {
	r, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.serviceName),
			semconv.ServiceVersion(cfg.serviceVersion),
			semconv.ServiceInstanceID(cfg.serviceInstanceID),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: build resource: %w", err)
	}
	return r, nil
}

func buildTracerProvider(ctx context.Context, cfg *config, res *resource.Resource) (*sdktrace.TracerProvider, error) {
	var exp sdktrace.SpanExporter
	var err error
	switch cfg.protocol {
	case ProtocolHTTPProtobuf:
		exp, err = otlptracehttp.New(ctx, cfg.traceHTTPOpts()...)
	default:
		exp, err = otlptracegrpc.New(ctx, cfg.traceGRPCOpts()...)
	}
	if err != nil {
		return nil, fmt.Errorf("telemetry: trace exporter: %w", err)
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sdktrace.NewBatchSpanProcessor(exp)),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(cfg.sampler),
	), nil
}

func buildLoggerProvider(ctx context.Context, cfg *config, res *resource.Resource) (*sdklog.LoggerProvider, error) {
	var exp sdklog.Exporter
	var err error
	switch cfg.protocol {
	case ProtocolHTTPProtobuf:
		exp, err = otlploghttp.New(ctx, cfg.logHTTPOpts()...)
	default:
		exp, err = otlploggrpc.New(ctx, cfg.logGRPCOpts()...)
	}
	if err != nil {
		return nil, fmt.Errorf("telemetry: log exporter: %w", err)
	}
	return sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
		sdklog.WithResource(res),
	), nil
}
