package logger

import (
	"context"
	"log/slog"
)

type contextKey struct{}

// WithLogger returns a new context carrying l.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, contextKey{}, l)
}

// FromContext retrieves the *slog.Logger stored in ctx.
// Returns Nop() when no logger is present so callers are never handed nil.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(contextKey{}).(*slog.Logger); ok {
		return l
	}
	return Nop()
}

// WithRequestID derives a new context whose logger carries request_id.
func WithRequestID(ctx context.Context, id string) context.Context {
	l := FromContext(ctx).With(slog.String(KeyRequestID, id))
	return WithLogger(ctx, l)
}

// WithTraceContext derives a new context carrying both trace_id and span_id.
// Call this when integrating with an OpenTelemetry trace span.
func WithTraceContext(ctx context.Context, traceID, spanID string) context.Context {
	l := FromContext(ctx).With(
		slog.String(KeyTraceID, traceID),
		slog.String(KeySpanID, spanID),
	)
	return WithLogger(ctx, l)
}
