// Package client provides an net/http client with OTel trace-context propagation
// and structured logging of outbound requests.
package client

import (
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"

	"github.com/labspangaea/go-lib/logger"
)

const msgClient = "http client"

// defaultClientTimeout is the timeout applied to every client created by
// NewClient. Callers that need a different value may override client.Timeout
// after construction; callers that explicitly need no timeout may set it to 0.
const defaultClientTimeout = 30 * time.Second

// loggingTransport wraps a RoundTripper to log outbound requests using the
// context logger. It is the inner layer; otelhttp.Transport is the outer layer
// so that trace context is already injected into headers before we log.
type loggingTransport struct {
	base http.RoundTripper
	log  *slog.Logger
}

func (t *loggingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	start := time.Now()

	// Prefer the context logger (carries trace_id + span_id when the caller is
	// inside a server span); fall back to the transport's base logger when the
	// caller did not attach one to the context.
	l := logger.FromContext(r.Context())
	if logger.IsNop(l) {
		l = t.log
	}

	resp, err := t.base.RoundTrip(r)
	elapsed := time.Since(start)

	if err != nil {
		// Record the error on the active span so it surfaces in the trace.
		if span := trace.SpanFromContext(r.Context()); span.IsRecording() {
			span.RecordError(err)
		}
		l.Error(msgClient,
			slog.String(logger.KeyHTTPMethod, r.Method),
			slog.String(logger.KeyHTTPURL, r.URL.String()),
			slog.Any(logger.KeyError, err),
			slog.Int64(logger.KeyDurationMS, elapsed.Milliseconds()),
		)
		return nil, err
	}

	args := []any{
		slog.String(logger.KeyHTTPMethod, r.Method),
		slog.String(logger.KeyHTTPURL, r.URL.String()),
		slog.Int(logger.KeyHTTPStatusCode, resp.StatusCode),
		slog.Int64(logger.KeyDurationMS, elapsed.Milliseconds()),
	}
	switch {
	case resp.StatusCode >= 500:
		l.Error(msgClient, args...)
	case resp.StatusCode >= 400:
		l.Warn(msgClient, args...)
	default:
		l.Info(msgClient, args...)
	}

	return resp, nil
}

// NewTransport returns an http.RoundTripper that:
//  1. Propagates W3C trace context into outgoing request headers and creates a
//     client span (via otelhttp.NewTransport).
//  2. Logs each request/response using the context logger so outbound calls
//     carry the same trace_id and span_id as the surrounding server span.
//
// base must not be nil; pass http.DefaultTransport for the standard behaviour.
func NewTransport(base http.RoundTripper, log *slog.Logger) http.RoundTripper {
	// otelhttp.NewTransport handles span creation and header injection.
	// The logging layer wraps it so it sees the already-propagated context.
	return &loggingTransport{
		base: otelhttp.NewTransport(base),
		log:  log,
	}
}

// NewClient returns an *http.Client pre-configured with NewTransport backed by
// http.DefaultTransport. The returned client propagates OTel trace context and
// logs every outbound request. The default timeout is 30 s; callers can
// override it by setting client.Timeout after construction.
func NewClient(log *slog.Logger) *http.Client {
	return &http.Client{
		Transport: NewTransport(http.DefaultTransport, log),
		Timeout:   defaultClientTimeout,
	}
}
