package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/labspangaea/go-lib/logger"
)

// RequestID extracts the X-Request-ID request header, or generates a random
// 16-character hex ID when the header is absent. The ID is echoed in the
// X-Request-ID response header and stored in the context logger via
// logger.WithRequestID so every downstream log line carries it automatically.
func RequestID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(HeaderRequestID)
			if id == "" {
				id = newID()
			}
			w.Header().Set(HeaderRequestID, id)
			ctx := logger.WithRequestID(r.Context(), id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Logging stamps the active OTel span's trace_id and span_id onto the context
// logger (so any log call inside the handler automatically carries them), then
// logs a single request-summary line after the handler returns.
//
// Place this after RequestID and otelhttp.NewMiddleware in the chain so both
// the request_id (response header) and the span are already populated when
// Logging runs.
//
// Log levels: INFO for status < 500, ERROR for status ≥ 500.
//
// Pass WithBody(true) to additionally include `request_body` and
// `response_body` fields when the content type is JSON. Off by default.
func Logging(log *slog.Logger, opts ...LoggingOption) func(http.Handler) http.Handler {
	var cfg loggingConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ctx := r.Context()

			// Always derive from the middleware's log parameter rather than
			// FromContext(ctx). RequestID upstream stores a logger derived from
			// Nop() (the bare http.Request context has no logger), and IsNop is
			// a singleton-pointer check that can't detect a discard-handler logger
			// after .With() derivation — using FromContext silently discards every
			// per-request log line. Pulling request_id from the response header
			// (set by RequestID earlier in the chain) avoids the trap entirely.
			base := log
			if reqID := w.Header().Get(HeaderRequestID); reqID != "" {
				base = base.With(slog.String(logger.KeyRequestID, reqID))
			}

			// If otelhttp created a span, stamp its IDs onto the context logger
			// so any log call from handlers can call logger.FromContext(ctx) and
			// automatically include trace_id + span_id without extra wiring.
			if sc := trace.SpanFromContext(ctx).SpanContext(); sc.IsValid() {
				ctx = logger.WithTraceContext(ctx, sc.TraceID().String(), sc.SpanID().String())
			}

			reqLog := base.With(
				slog.String(logger.KeyHTTPMethod, r.Method),
				slog.String(logger.KeyURLPath, r.URL.Path),
			)
			ctx = logger.WithLogger(ctx, reqLog)

			// Capture request body up front so the handler still sees the
			// full payload (readAndRestore reattaches a fresh ReadCloser).
			// Skip for non-JSON to avoid logging form/multipart/binary bytes.
			var reqBody []byte
			if cfg.captureBody && isJSONContentType(r.Header.Get("Content-Type")) {
				reqBody = readAndRestore(r)
			}

			rw := wrapWriter(w)
			if cfg.captureBody {
				rw.bodyCap = newBoundedBuffer(maxLoggedBodyBytes)
			}
			next.ServeHTTP(rw, r.WithContext(ctx))

			status := rw.statusCode()
			args := []any{
				slog.Int(logger.KeyHTTPStatusCode, status),
				slog.Int64(logger.KeyDurationMS, time.Since(start).Milliseconds()),
			}
			if cfg.captureBody {
				if len(reqBody) > 0 {
					args = append(args, bodyAttr(logger.KeyRequestBody, reqBody))
				}
				if rw.bodyCap != nil && isJSONContentType(rw.Header().Get("Content-Type")) {
					if snap := rw.bodyCap.snapshot(); len(snap) > 0 {
						args = append(args, bodyAttr(logger.KeyResponseBody, snap))
					}
				}
			}
			if status >= 500 {
				reqLog.Error(msgServer, args...)
			} else {
				reqLog.Info(msgServer, args...)
			}
		})
	}
}

// Recover catches panics anywhere in the handler chain, logs the stack trace,
// and responds with HTTP 500. Place this as the outermost middleware so it
// catches panics from every middleware below it.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if p := recover(); p != nil {
					log.Error(msgPanic,
						slog.Any(logger.KeyError, fmt.Errorf("%v", p)),
						slog.String(logger.KeyStack, string(debug.Stack())),
					)
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
