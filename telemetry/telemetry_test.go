package telemetry_test

import (
	"testing"

	"github.com/labspangaea/go-lib/telemetry"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// These tests verify option wiring without requiring a live OTLP collector.
// Integration tests against a real collector belong in a separate file.

func TestWithEndpoint(t *testing.T) {
	// Verify option doesn't panic.
	_ = telemetry.WithEndpoint("otel.example.com:4317")
}

func TestWithServiceInfo(t *testing.T) {
	_ = telemetry.WithServiceInfo("my-svc", "1.0.0", "instance-1")
}

func TestWithInsecure(t *testing.T) {
	_ = telemetry.WithInsecure()
}

func TestWithGzipCompression(t *testing.T) {
	_ = telemetry.WithGzipCompression()
}

func TestWithSampler(t *testing.T) {
	// Verify the new WithSampler option compiles and doesn't panic.
	_ = telemetry.WithSampler(sdktrace.NeverSample())
	_ = telemetry.WithSampler(sdktrace.AlwaysSample())
	_ = telemetry.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(0.1)))
}

func TestWithProtocol(t *testing.T) {
	_ = telemetry.WithProtocol(telemetry.ProtocolGRPC)
	_ = telemetry.WithProtocol(telemetry.ProtocolHTTPProtobuf)
	_ = telemetry.WithProtocol("not-a-real-protocol") // silently falls back to default
}

func TestWithHeaders(t *testing.T) {
	_ = telemetry.WithHeaders(map[string]string{"api-key": "secret"})
	_ = telemetry.WithHeaders(nil)
}
