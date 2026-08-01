package client

import (
	"log/slog"
	"net/http"

	"github.com/go-resty/resty/v2"
)

// NewResty returns a *resty.Client for callers who prefer resty's fluent request
// API over net/http.
//
// It is built on the client NewClient returns, so it inherits the same instrumented
// transport stack — W3C trace-context propagation, client spans, and structured
// request/response logging through the context logger — rather than duplicating any
// of it. Anything true of NewClient is true here, including the 30 s default timeout.
//
// Pass the request context so outbound calls join the surrounding server span:
//
//	r := client.NewResty(log)
//	resp, err := r.R().
//	    SetContext(ctx).
//	    SetResult(&out).
//	    Get("https://api.example.com/v1/orders")
//
// The two surfaces interoperate. The generic helpers in request.go
// (Get/Post/Put/Delete) take a Doer, which *http.Client already satisfies — so use
// NewClient for those and NewResty where resty's builder earns its keep (retries,
// multipart, per-request middleware). resty.Client.GetClient() returns the
// underlying *http.Client if you need to cross back over.
func NewResty(log *slog.Logger) *resty.Client {
	return resty.NewWithClient(NewClient(log))
}

// NewRestyWithClient wraps an existing *http.Client that the caller has already
// configured — custom TLS, a proxy, a tuned connection pool.
//
// The client must carry an instrumented transport or the resulting resty client
// loses tracing and logging. Build one with NewTransport:
//
//	base := &http.Transport{MaxIdleConnsPerHost: 20}
//	hc := &http.Client{
//	    Timeout:   15 * time.Second,
//	    Transport: client.NewTransport(base, log),
//	}
//	r := client.NewRestyWithClient(hc)
func NewRestyWithClient(hc *http.Client) *resty.Client {
	return resty.NewWithClient(hc)
}
