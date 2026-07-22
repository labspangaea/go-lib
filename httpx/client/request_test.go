package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/labspangaea/go-lib/httpx/client"
)

// doerFunc adapts a plain function to the Doer interface.
// Use it in tests instead of starting a real HTTP server.
type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

// jsonResponse builds a minimal *http.Response with a JSON body.
func jsonResponse(status int, v any) *http.Response {
	b, _ := json.Marshal(v)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(b))),
	}
}

// rawResponse builds a *http.Response with a plain string body.
func rawResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// emptyResponse builds a *http.Response with no body (e.g. 204).
func emptyResponse(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

// --- Get ---

type user struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func TestGet_Success(t *testing.T) {
	want := user{ID: 1, Name: "alice"}
	d := doerFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		return jsonResponse(http.StatusOK, want), nil
	})

	got, err := client.Get[user](context.Background(), d, "http://example.com/users/1", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Body != want {
		t.Errorf("body = %+v, want %+v", got.Body, want)
	}
	if got.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", got.StatusCode)
	}
}

func TestGet_QueryParams(t *testing.T) {
	d := doerFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("page") != "2" {
			t.Errorf("query param page = %q, want 2", r.URL.Query().Get("page"))
		}
		return jsonResponse(http.StatusOK, user{}), nil
	})

	q := url.Values{"page": {"2"}}
	_, err := client.Get[user](context.Background(), d, "http://example.com/users", q, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGet_CustomHeaders(t *testing.T) {
	d := doerFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-Tenant") != "acme" {
			t.Errorf("X-Tenant = %q, want acme", r.Header.Get("X-Tenant"))
		}
		return jsonResponse(http.StatusOK, user{}), nil
	})

	_, err := client.Get[user](context.Background(), d, "http://example.com/users/1", nil, map[string]string{"X-Tenant": "acme"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGet_404_ReturnsRequestError(t *testing.T) {
	errBody := map[string]any{"message": "not found"}
	d := doerFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusNotFound, errBody), nil
	})

	_, err := client.Get[user](context.Background(), d, "http://example.com/users/99", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var re *client.RequestError
	if !errors.As(err, &re) {
		t.Fatalf("error type = %T, want *client.RequestError", err)
	}
	if re.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", re.StatusCode)
	}
	if re.Body["message"] != "not found" {
		t.Errorf("Body[message] = %v, want not found", re.Body["message"])
	}
}

func TestGet_TransportError(t *testing.T) {
	want := errors.New("connection refused")
	d := doerFunc(func(*http.Request) (*http.Response, error) {
		return nil, want
	})

	_, err := client.Get[user](context.Background(), d, "http://localhost:0/users", nil, nil)
	if !errors.Is(err, want) {
		t.Errorf("error = %v, want %v", err, want)
	}
}

func TestGet_ContextPropagated(t *testing.T) {
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "sentinel")

	d := doerFunc(func(r *http.Request) (*http.Response, error) {
		if r.Context().Value(ctxKey{}) != "sentinel" {
			t.Error("context not propagated to request")
		}
		return jsonResponse(http.StatusOK, user{}), nil
	})

	_, err := client.Get[user](ctx, d, "http://example.com/users/1", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Post ---

type createReq struct {
	Name string `json:"name"`
}

func TestPost_Success(t *testing.T) {
	want := user{ID: 2, Name: "bob"}
	d := doerFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		var body createReq
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		if body.Name != "bob" {
			t.Errorf("body.Name = %q, want bob", body.Name)
		}
		return jsonResponse(http.StatusCreated, want), nil
	})

	got, err := client.Post[createReq, user](context.Background(), d, "http://example.com/users", createReq{Name: "bob"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Body != want {
		t.Errorf("body = %+v, want %+v", got.Body, want)
	}
}

func TestPost_500_ReturnsRequestError(t *testing.T) {
	d := doerFunc(func(*http.Request) (*http.Response, error) {
		return rawResponse(http.StatusInternalServerError, `{"error":"internal"}`), nil
	})

	_, err := client.Post[createReq, user](context.Background(), d, "http://example.com/users", createReq{}, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var re *client.RequestError
	if !errors.As(err, &re) {
		t.Fatalf("error type = %T, want *client.RequestError", err)
	}
	if re.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", re.StatusCode)
	}
}

// --- Put ---

func TestPut_Success(t *testing.T) {
	d := doerFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		return jsonResponse(http.StatusOK, user{ID: 1, Name: "updated"}), nil
	})

	got, err := client.Put[createReq, user](context.Background(), d, "http://example.com/users/1", createReq{Name: "updated"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Body.Name != "updated" {
		t.Errorf("body.Name = %q, want updated", got.Body.Name)
	}
}

// --- Delete ---

func TestDelete_Success(t *testing.T) {
	d := doerFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		return emptyResponse(http.StatusNoContent), nil
	})

	got, err := client.Delete[user](context.Background(), d, "http://example.com/users/1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", got.StatusCode)
	}
}

// --- 204 No Content ---

func TestSend_204_NoBodyDecode(t *testing.T) {
	d := doerFunc(func(*http.Request) (*http.Response, error) {
		return emptyResponse(http.StatusNoContent), nil
	})

	// If the body were decoded on 204, json.Decoder would return io.EOF → error.
	// A nil error here proves the body decode was skipped.
	got, err := client.Get[user](context.Background(), d, "http://example.com/ping", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error on 204: %v", err)
	}
	if got.Body != (user{}) {
		t.Errorf("body = %+v, want zero value", got.Body)
	}
}

// --- RequestError ---

func TestRequestError_Error(t *testing.T) {
	re := &client.RequestError{
		StatusCode: 404,
		Method:     http.MethodGet,
		URL:        "http://example.com/users/99",
		Duration:   10 * time.Millisecond,
	}
	want := "GET http://example.com/users/99: status 404"
	if re.Error() != want {
		t.Errorf("Error() = %q, want %q", re.Error(), want)
	}
}

func TestRequestError_NonJSONBody(t *testing.T) {
	d := doerFunc(func(*http.Request) (*http.Response, error) {
		return rawResponse(http.StatusBadGateway, "bad gateway"), nil
	})

	_, err := client.Get[user](context.Background(), d, "http://example.com/users/1", nil, nil)

	var re *client.RequestError
	if !errors.As(err, &re) {
		t.Fatalf("error type = %T, want *client.RequestError", err)
	}
	if re.Body["message"] != "bad gateway" {
		t.Errorf("Body[message] = %v, want bad gateway", re.Body["message"])
	}
}

func TestRequestError_EmptyBody(t *testing.T) {
	d := doerFunc(func(*http.Request) (*http.Response, error) {
		return emptyResponse(http.StatusNotFound), nil
	})

	_, err := client.Get[user](context.Background(), d, "http://example.com/users/1", nil, nil)

	var re *client.RequestError
	if !errors.As(err, &re) {
		t.Fatalf("error type = %T, want *client.RequestError", err)
	}
	if re.Body != nil {
		t.Errorf("Body = %v, want nil for empty response", re.Body)
	}
}

// --- Duration ---

func TestResponse_DurationRecorded(t *testing.T) {
	d := doerFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, user{ID: 1}), nil
	})

	got, err := client.Get[user](context.Background(), d, "http://example.com/users/1", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Duration < 0 {
		t.Errorf("Duration = %v, want >= 0", got.Duration)
	}
}
