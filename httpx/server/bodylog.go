package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// maxLoggedBodyBytes caps how much of any single body the logger keeps in
// memory and writes to the log line. JSON bodies past this point are stored
// truncated with a trailing "...(truncated, N bytes total)" marker; the
// actual response sent to the client is unaffected.
const maxLoggedBodyBytes = 64 * 1024

// LoggingOption configures Logging behavior. Applied left-to-right; later
// options override earlier ones.
type LoggingOption func(*loggingConfig)

type loggingConfig struct {
	captureBody bool
}

// WithBody enables capture of request and response bodies in the per-request
// log line as `request_body` and `response_body` fields. Only application/json
// content types are captured; binary, multipart, and form bodies are skipped
// to keep the implementation simple. Bodies larger than 64 KiB are truncated
// with a marker so logs don't unbounded-grow.
//
// Off by default. Enable in dev/test only — bodies frequently hold PII and
// inflate log volume by orders of magnitude.
func WithBody(enabled bool) LoggingOption {
	return func(c *loggingConfig) { c.captureBody = enabled }
}

// boundedBuffer captures up to capCap bytes and counts the rest. The original
// total length is preserved so the log line can flag truncation honestly.
type boundedBuffer struct {
	buf   bytes.Buffer
	total int
	cap   int
}

func newBoundedBuffer(cap int) *boundedBuffer { return &boundedBuffer{cap: cap} }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.total += len(p)
	if remaining := b.cap - b.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.buf.Write(p)
	}
	// Always return the original write length — the wrapped ResponseWriter
	// is the one that actually sends bytes; the buffer is a side channel.
	return len(p), nil
}

// truncated returns true when the writer received more bytes than it stored.
func (b *boundedBuffer) truncated() bool { return b.total > b.buf.Len() }

// snapshot returns the captured bytes with a truncation marker appended when
// the original body exceeded the cap. The marker is intentionally outside the
// JSON envelope so anyone copy-pasting the field gets the readable form, and
// log filters can still json-decode the prefix if they trim it.
func (b *boundedBuffer) snapshot() []byte {
	if !b.truncated() {
		return b.buf.Bytes()
	}
	out := make([]byte, 0, b.buf.Len()+64)
	out = append(out, b.buf.Bytes()...)
	out = append(out, []byte("...(truncated, ")...)
	out = append(out, []byte(itoa(b.total))...)
	out = append(out, []byte(" bytes total)")...)
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// isJSONContentType reports whether ct is a JSON-family media type. Handles
// the common parameter forms (`application/json; charset=utf-8`,
// `application/vnd.api+json`).
func isJSONContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return ct == "application/json" || strings.HasSuffix(ct, "+json")
}

// readAndRestore drains r.Body up to maxLoggedBodyBytes for the log line and
// reattaches a fresh ReadCloser containing the full original payload so the
// downstream handler still sees the same request. Bodies above the cap are
// fully read into memory (so the handler still gets everything) but only the
// prefix is retained for logging.
func readAndRestore(r *http.Request) []byte {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	full, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		// Best-effort: if the body read failed there's nothing to log; restore
		// an empty body and let the handler surface the underlying error.
		r.Body = io.NopCloser(bytes.NewReader(nil))
		return nil
	}
	r.Body = io.NopCloser(bytes.NewReader(full))
	if len(full) <= maxLoggedBodyBytes {
		return full
	}
	// Truncate for the log line; keep the marker shape consistent with
	// boundedBuffer so request and response truncation read the same way.
	out := make([]byte, 0, maxLoggedBodyBytes+64)
	out = append(out, full[:maxLoggedBodyBytes]...)
	out = append(out, []byte("...(truncated, ")...)
	out = append(out, []byte(itoa(len(full)))...)
	out = append(out, []byte(" bytes total)")...)
	return out
}

// bodyAttr returns a slog.Attr that inlines body as raw JSON when the bytes
// parse as valid JSON, otherwise as a quoted string. This keeps log
// aggregators (Loki, Cloud Logging) able to query nested fields when the
// payload is well-formed without breaking the log line when it isn't.
func bodyAttr(key string, body []byte) slog.Attr {
	if json.Valid(body) {
		return slog.Any(key, json.RawMessage(body))
	}
	return slog.String(key, string(body))
}
