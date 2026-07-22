//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=request.go -destination=mocks/mock_doer.go -package=mocks

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Doer is the single method of *http.Client that helpers need.
// Accepting this instead of *http.Client keeps tests simple — any stub works.
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

// RequestError is returned for non-2xx responses. Use errors.As to inspect
// StatusCode, Method, URL, and the parsed error body.
type RequestError struct {
	StatusCode int
	Method     string
	URL        string
	Body       map[string]any
	Duration   time.Duration
}

func (e *RequestError) Error() string {
	return fmt.Sprintf("%s %s: status %d", e.Method, e.URL, e.StatusCode)
}

// Get sends a GET request and decodes a successful JSON response into ResT.
// queries and headers may be nil.
func Get[ResT any](ctx context.Context, client Doer, rawURL string, queries url.Values, headers map[string]string) (*Response[ResT], error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if len(queries) > 0 {
		req.URL.RawQuery = queries.Encode()
	}
	return send[ResT](ctx, client, req, headers)
}

// Post marshals payload as JSON, sends a POST request, and decodes a
// successful JSON response into ResT.
func Post[ReqT any, ResT any](ctx context.Context, client Doer, rawURL string, payload ReqT, headers map[string]string) (*Response[ResT], error) {
	req, err := newJSONRequest(ctx, http.MethodPost, rawURL, payload)
	if err != nil {
		return nil, err
	}
	return send[ResT](ctx, client, req, headers)
}

// Put marshals payload as JSON, sends a PUT request, and decodes a successful
// JSON response into ResT.
func Put[ReqT any, ResT any](ctx context.Context, client Doer, rawURL string, payload ReqT, headers map[string]string) (*Response[ResT], error) {
	req, err := newJSONRequest(ctx, http.MethodPut, rawURL, payload)
	if err != nil {
		return nil, err
	}
	return send[ResT](ctx, client, req, headers)
}

// Delete sends a DELETE request and decodes a successful JSON response into ResT.
func Delete[ResT any](ctx context.Context, client Doer, rawURL string, headers map[string]string) (*Response[ResT], error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, rawURL, nil)
	if err != nil {
		return nil, err
	}
	return send[ResT](ctx, client, req, headers)
}

func newJSONRequest(ctx context.Context, method, rawURL string, payload any) (*http.Request, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func send[ResT any](_ context.Context, client Doer, req *http.Request, headers map[string]string) (*Response[ResT], error) {
	setHeaders(req, headers)

	start := time.Now()
	resp, err := client.Do(req)
	dur := time.Since(start)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, decodeError(resp, req, dur)
	}

	var body ResT
	if resp.StatusCode != http.StatusNoContent {
		if err = json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return nil, err
		}
	}

	return &Response[ResT]{
		StatusCode: resp.StatusCode,
		Header:     resp.Header,
		Body:       body,
		Duration:   dur,
	}, nil
}

func setHeaders(req *http.Request, headers map[string]string) {
	for k, v := range headers {
		req.Header.Set(k, v)
	}
}

func decodeError(resp *http.Response, req *http.Request, dur time.Duration) *RequestError {
	re := &RequestError{
		StatusCode: resp.StatusCode,
		Method:     req.Method,
		URL:        req.URL.String(),
		Duration:   dur,
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil || len(b) == 0 {
		return re
	}
	body := map[string]any{}
	if json.Unmarshal(b, &body) == nil {
		re.Body = body
	} else {
		re.Body = map[string]any{"message": string(b)}
	}
	return re
}
