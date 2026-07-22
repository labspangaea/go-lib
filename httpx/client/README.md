# httpx/client

HTTP client with automatic OTel trace-context propagation and structured logging
of outbound requests.

---

## Installation

```go
import "github.com/labspangaea/go-lib/httpx/client"
```

---

## Quick start

```go
c := client.NewClient(log)

resp, err := c.Do(req.WithContext(ctx)) // ctx carries the active server span
```

Every outbound call automatically:
- Injects `traceparent` / `tracestate` headers so the downstream service joins the same trace
- Creates a client span in the current trace
- Logs method, URL, status, and duration using the context logger

---

## Transport layers

`NewTransport` stacks two wrappers around the base transport:

```
Your code
    → loggingTransport   log request + response, record span errors
    → otelhttp.Transport  inject W3C trace-context headers, create client span
    → base               actual TCP connection (http.DefaultTransport or custom)
```

`otelhttp.Transport` runs first (inner) so by the time `loggingTransport` executes,
the outgoing headers already carry `traceparent` and the client span exists in context.

---

## API

### `NewClient`

```go
func NewClient(log *slog.Logger) *http.Client
```

Returns an `*http.Client` backed by `http.DefaultTransport`, wrapped with OTel propagation
and structured logging. Adjust `Timeout` and other fields on the returned value:

```go
c := client.NewClient(log)
c.Timeout = 10 * time.Second
```

### `NewTransport`

```go
func NewTransport(base http.RoundTripper, log *slog.Logger) http.RoundTripper
```

Wraps an existing transport — useful when you need custom TLS, a proxy, or a connection
pool alongside OTel and logging:

```go
base := &http.Transport{
    TLSHandshakeTimeout: 5 * time.Second,
    MaxIdleConnsPerHost: 20,
}
c := &http.Client{
    Timeout:   15 * time.Second,
    Transport: client.NewTransport(base, log),
}
```

`base` must not be nil — pass `http.DefaultTransport` for standard behaviour.

---

## Log output

One line per outbound call, using the **context logger** (inherits `trace_id`, `span_id`,
and `request_id` from the surrounding server span). Falls back to the `log` passed to
`NewClient`/`NewTransport` when no logger is in the context.

| Field | Value |
|---|---|
| `http.method` | request method |
| `url.full` | full request URL |
| `http.response.status_code` | response status |
| `duration_ms` | round-trip time in milliseconds |
| `error` | transport-level error (connection refused, timeout, etc.) |

Log level: `INFO` for 2xx/3xx, `WARN` for 4xx, `ERROR` for 5xx or transport failure.

---

## Typed request helpers

`request.go` provides generic `Get`, `Post`, `Put`, and `Delete` helpers on top of any `Doer`
(anything with a `Do(*http.Request) (*http.Response, error)` method — `*http.Client` satisfies
this automatically).

### Types

```go
// Doer is the one method the helpers need. *http.Client satisfies it.
// In tests, use a stub instead of a real server.
type Doer interface {
    Do(*http.Request) (*http.Response, error)
}

// Response carries a decoded body alongside HTTP metadata.
type Response[T any] struct {
    StatusCode int
    Header     http.Header
    Body       T
    Duration   time.Duration
}

// RequestError is returned for non-2xx responses.
// Use errors.As to inspect StatusCode, Method, URL, and the parsed error body.
type RequestError struct {
    StatusCode int
    Method     string
    URL        string
    Body       map[string]any // best-effort JSON parse of the error body
    Duration   time.Duration
}
```

### Functions

```go
func Get[ResT any](ctx, client, url, queries, headers) (*Response[ResT], error)
func Post[ReqT, ResT any](ctx, client, url, payload, headers) (*Response[ResT], error)
func Put[ReqT, ResT any](ctx, client, url, payload, headers)  (*Response[ResT], error)
func Delete[ResT any](ctx, client, url, headers)              (*Response[ResT], error)
```

### Usage

Wire up a client once at startup and pass it through your service:

```go
httpClient := client.NewClient(log) // OTel + logging transport
httpClient.Timeout = 10 * time.Second
```

**GET with query params and custom headers:**

```go
type UserResponse struct {
    ID   int    `json:"id"`
    Name string `json:"name"`
}

resp, err := client.Get[UserResponse](
    ctx,
    httpClient,
    "https://api.example.com/users/42",
    url.Values{"expand": {"roles"}},          // nil = no query params
    map[string]string{"X-Tenant": "acme"},    // nil = no extra headers
)
if err != nil {
    return err
}
fmt.Println(resp.Body.Name, resp.Duration)
```

**POST with a typed request and response:**

```go
type CreateUserReq struct {
    Name  string `json:"name"`
    Email string `json:"email"`
}

resp, err := client.Post[CreateUserReq, UserResponse](
    ctx,
    httpClient,
    "https://api.example.com/users",
    CreateUserReq{Name: "alice", Email: "alice@example.com"},
    nil,
)
```

`Content-Type: application/json` is set automatically on body-bearing methods.

**Handling non-2xx errors:**

```go
resp, err := client.Get[UserResponse](ctx, httpClient, url, nil, nil)
if err != nil {
    var re *client.RequestError
    if errors.As(err, &re) {
        // re.StatusCode, re.Method, re.URL, re.Body (parsed JSON), re.Duration
        if re.StatusCode == http.StatusNotFound {
            return ErrUserNotFound
        }
    }
    return err
}
```

**204 No Content** — body is skipped and `Response.Body` holds the zero value of `ResT`.

### Testing with a stub Doer

Because the helpers accept `Doer` (not `*http.Client`), tests need no real server:

```go
type doerFunc func(*http.Request) (*http.Response, error)
func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

stub := doerFunc(func(r *http.Request) (*http.Response, error) {
    body, _ := json.Marshal(UserResponse{ID: 1, Name: "alice"})
    return &http.Response{
        StatusCode: 200,
        Header:     http.Header{},
        Body:       io.NopCloser(bytes.NewReader(body)),
    }, nil
})

resp, err := client.Get[UserResponse](context.Background(), stub, "http://fake/users/1", nil, nil)
```

No `httptest.Server`, no network, no flakiness.

---

## Trace propagation

When the outbound call is made inside a server handler (i.e. the context already has an
active span from `otelhttp.NewMiddleware`), the client span becomes a **child** of the
server span. The downstream service receives the `traceparent` header and can continue
the same trace, producing a connected waterfall in your APM dashboard.

```
[server span: GET /orders]
    └── [client span: POST payment-service/charge]   ← created by NewTransport
            └── [server span in payment-service]     ← reads traceparent header
```

No extra wiring is needed — pass the request context and propagation happens automatically:

```go
func (s *OrderService) Create(ctx context.Context) error {
    req, _ := http.NewRequestWithContext(ctx, http.MethodPost, paymentURL, body)
    resp, err := s.httpClient.Do(req) // traceparent injected automatically
    // ...
}
```
