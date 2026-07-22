package logger_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/labspangaea/go-lib/logger"
)

func TestNop(t *testing.T) {
	l := logger.Nop()
	if l == nil {
		t.Fatal("Nop should never return nil")
	}
	// Should be a singleton.
	if logger.Nop() != l {
		t.Fatal("Nop should return the same instance every time")
	}
}

func TestIsNop(t *testing.T) {
	if !logger.IsNop(logger.Nop()) {
		t.Fatal("IsNop(Nop()) should be true")
	}

	real := logger.New()
	if logger.IsNop(real) {
		t.Fatal("IsNop should be false for a real logger")
	}
}

func TestFromContext_Empty(t *testing.T) {
	l := logger.FromContext(context.Background())
	if !logger.IsNop(l) {
		t.Fatal("FromContext on empty context should return Nop")
	}
}

func TestWithLogger_RoundTrip(t *testing.T) {
	real := logger.New()
	ctx := logger.WithLogger(context.Background(), real)
	got := logger.FromContext(ctx)

	if got != real {
		t.Fatal("FromContext should return the logger stored by WithLogger")
	}
}

func TestWithRequestID(t *testing.T) {
	base := logger.New(logger.WithDevelopment())
	ctx := logger.WithLogger(context.Background(), base)
	ctx = logger.WithRequestID(ctx, "req-123")

	l := logger.FromContext(ctx)
	if l == nil {
		t.Fatal("logger should not be nil after WithRequestID")
	}
	if logger.IsNop(l) {
		t.Fatal("logger should not be Nop after WithRequestID")
	}
	// We can't easily inspect slog.Logger attrs, but we verify it doesn't panic
	// and is a different instance from base (due to .With).
	if l == base {
		t.Fatal("WithRequestID should create a new logger via With")
	}
}

func TestWithTraceContext(t *testing.T) {
	base := logger.New(logger.WithDevelopment())
	ctx := logger.WithLogger(context.Background(), base)
	ctx = logger.WithTraceContext(ctx, "abc123trace", "def456span")

	l := logger.FromContext(ctx)
	if logger.IsNop(l) {
		t.Fatal("logger should not be Nop after WithTraceContext")
	}
	if l == base {
		t.Fatal("WithTraceContext should create a new logger via With")
	}
}

func TestNew_Development(t *testing.T) {
	l := logger.New(logger.WithDevelopment())
	if l == nil {
		t.Fatal("New with development mode should return a non-nil logger")
	}
	if logger.IsNop(l) {
		t.Fatal("New should not return Nop")
	}
}

func TestNew_WithLevel(t *testing.T) {
	l := logger.New(logger.WithLevel(slog.LevelError))
	if l == nil {
		t.Fatal("New with custom level should return a non-nil logger")
	}
}

func TestNew_WithServiceInfo(t *testing.T) {
	l := logger.New(logger.WithServiceInfo("my-svc", "1.0.0", "instance-1"))
	if l == nil {
		t.Fatal("New with service info should return a non-nil logger")
	}
}

func TestNew_WithHandler(t *testing.T) {
	extra := slog.DiscardHandler
	l := logger.New(logger.WithHandler(extra))
	if l == nil {
		t.Fatal("New with extra handler should return a non-nil logger")
	}
}

func TestNew_WithSource(t *testing.T) {
	l := logger.New(logger.WithSource())
	if l == nil {
		t.Fatal("New with source should return a non-nil logger")
	}
}

func TestNew_WithOTelBridge_NilProvider(t *testing.T) {
	// Nil provider is a no-op — should not panic and should still return a usable logger.
	l := logger.New(
		logger.WithServiceInfo("svc", "1.0.0", "i-1"),
		logger.WithOTelBridge(nil, slog.LevelError),
	)
	if l == nil {
		t.Fatal("New with nil bridge provider should return a non-nil logger")
	}
	if logger.IsNop(l) {
		t.Fatal("New with nil bridge should not return Nop")
	}
	// Smoke-test: emit log records below and at the threshold without panic.
	l.Info("info message")
	l.Error("error message")
}
