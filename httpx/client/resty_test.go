package client_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/labspangaea/go-lib/httpx/client"
)

func TestNewResty_InheritsClientDefaults(t *testing.T) {
	r := client.NewResty(slog.Default())

	hc := r.GetClient()
	if hc == nil {
		t.Fatal("NewResty: underlying *http.Client is nil")
	}
	if hc.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v; want the 30s default NewClient applies", hc.Timeout)
	}
	if hc.Transport == nil {
		t.Fatal("Transport is nil; resty must ride the instrumented transport")
	}
}

// The whole point of NewResty is that it reuses NewClient's transport stack
// instead of duplicating the OTel + logging middleware. If trace context stops
// reaching the wire, that reuse has silently broken.
func TestNewResty_PropagatesTraceContext(t *testing.T) {
	otel.SetTracerProvider(sdktrace.NewTracerProvider())
	otel.SetTextMapPropagator(propagation.TraceContext{})

	var gotTraceparent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceparent = r.Header.Get("traceparent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	ctx, span := otel.Tracer("test").Start(context.Background(), "caller")
	defer span.End()

	var out struct {
		OK bool `json:"ok"`
	}
	resp, err := client.NewResty(slog.Default()).R().
		SetContext(ctx).
		SetResult(&out).
		Get(srv.URL)
	if err != nil {
		t.Fatalf("resty request: %v", err)
	}
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d; want 200", resp.StatusCode())
	}
	if !out.OK {
		t.Error("response body did not decode into the result struct")
	}
	if gotTraceparent == "" {
		t.Fatal("no traceparent header reached the server; resty is not using the instrumented transport")
	}
	if !strings.HasPrefix(gotTraceparent, "00-") {
		t.Errorf("traceparent = %q; want a W3C version-00 header", gotTraceparent)
	}
}

// Outbound calls must log through the context logger so they carry the same
// trace_id / span_id as the surrounding server span.
func TestNewResty_LogsOutboundRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if _, err := client.NewResty(log).R().Get(srv.URL); err != nil {
		t.Fatalf("resty request: %v", err)
	}

	if buf.Len() == 0 {
		t.Fatal("no log record emitted for the outbound request")
	}
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(bytes.Split(buf.Bytes(), []byte("\n"))[0]), &rec); err != nil {
		t.Fatalf("log record is not valid JSON: %v", err)
	}
	if rec["msg"] != "http client" {
		t.Errorf("log msg = %v; want \"http client\"", rec["msg"])
	}
}

func TestNewRestyWithClient_UsesGivenClient(t *testing.T) {
	hc := &http.Client{
		Timeout:   5 * time.Second,
		Transport: client.NewTransport(http.DefaultTransport, slog.Default()),
	}

	if got := client.NewRestyWithClient(hc).GetClient(); got != hc {
		t.Error("NewRestyWithClient did not reuse the supplied *http.Client")
	}
}
