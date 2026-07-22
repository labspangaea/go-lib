package logger

// Field key constants — keep names stable; consumers import these directly.
const (
	// Service resource attributes (OTel semconv: resource.service.*).
	KeyServiceName       = "service.name"
	KeyServiceVersion    = "service.version"
	KeyServiceInstanceID = "service.instance.id"

	// Request / trace correlation (OTel semconv: trace.*, http.*, url.*).
	KeyRequestID      = "request_id"
	KeyTraceID        = "trace_id"
	KeySpanID         = "span_id"
	KeyHTTPMethod     = "http.method"
	KeyHTTPStatusCode = "http.response.status_code"
	KeyHTTPURL        = "url.full"
	KeyURLPath        = "url.path"

	// Optional body capture (server.Logging WithBody=true). Off by default
	// because bodies hold PII and inflate log volume; intended for dev/test.
	KeyRequestBody  = "http.request.body"
	KeyResponseBody = "http.response.body"

	// End-user (OTel semconv: enduser.*).
	KeyUserID = "enduser.id"

	// Error and diagnostic fields.
	KeyError      = "error"
	KeyStack      = "stack"
	KeyDurationMS = "duration_ms"
)
