// Package server provides net/http middleware for structured logging and
// OpenTelemetry tracing on incoming requests.
//
// OTel span creation and W3C trace-context propagation are delegated to the
// official otelhttp contrib package. This package adds only the pieces otelhttp
// does not provide: request-ID generation, context-logger stamping, structured
// request/response logging, and panic recovery.
//
// Recommended middleware order:
//
//	import "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
//
//	http.Handle("/", server.Chain(mux,
//	    server.Recover(log),                    // outermost: catches panics from all below
//	    server.RequestID(),                     // extract/generate X-Request-ID
//	    otelhttp.NewMiddleware("my-service"),   // OTel span + W3C propagation
//	    server.Logging(log),                    // stamp trace IDs on logger, log summary
//	))
package server

import "net/http"

const (
	// HeaderRequestID is the canonical HTTP header for request tracing.
	// Set by RequestID on both the inbound read and the outbound echo.
	HeaderRequestID = "X-Request-ID"

	msgServer = "http"
	msgPanic  = "panic recovered"
)

// Chain composes middleware around h. The first middleware in the list is the
// outermost — it executes first on the way in and last on the way out.
func Chain(h http.Handler, middleware ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		h = middleware[i](h)
	}
	return h
}

// responseWriter wraps http.ResponseWriter to capture the status code written
// by the handler (the standard interface does not expose it after the fact)
// and, when bodyCap is non-nil, a truncated copy of the response body for
// the body-logging path.
type responseWriter struct {
	http.ResponseWriter
	status  int
	wrote   bool
	bodyCap *boundedBuffer // nil = body capture disabled (zero overhead)
}

// Unwrap returns the underlying ResponseWriter so type assertions to
// http.Flusher, http.Hijacker, or http.Pusher pass through the wrapper.
func (rw *responseWriter) Unwrap() http.ResponseWriter { return rw.ResponseWriter }

func (rw *responseWriter) WriteHeader(code int) {
	if rw.wrote {
		return
	}
	rw.status = code
	rw.wrote = true
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wrote {
		rw.status = http.StatusOK
		rw.wrote = true
	}
	if rw.bodyCap != nil {
		rw.bodyCap.Write(b)
	}
	return rw.ResponseWriter.Write(b)
}

func (rw *responseWriter) statusCode() int {
	if rw.status == 0 {
		return http.StatusOK
	}
	return rw.status
}

// wrapWriter returns a *responseWriter for w. If w is already a *responseWriter
// it is returned as-is to prevent double-wrapping when middlewares nest.
func wrapWriter(w http.ResponseWriter) *responseWriter {
	if rw, ok := w.(*responseWriter); ok {
		return rw
	}
	return &responseWriter{ResponseWriter: w}
}
