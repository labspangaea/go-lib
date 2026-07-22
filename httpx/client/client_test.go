package client_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/labspangaea/go-lib/httpx/client"
)

func TestNewClient_DefaultTimeout(t *testing.T) {
	c := client.NewClient(slog.Default())
	if c.Timeout == 0 {
		t.Fatal("NewClient: Timeout is 0 (unbounded); want a non-zero default")
	}
}

func TestNewClient_DefaultTimeout_Is30s(t *testing.T) {
	c := client.NewClient(slog.Default())
	const want = 30 * time.Second
	if c.Timeout != want {
		t.Fatalf("NewClient: Timeout = %v; want %v", c.Timeout, want)
	}
}

func TestNewClient_CallerCanOverrideTimeout(t *testing.T) {
	c := client.NewClient(slog.Default())
	c.Timeout = 5 * time.Second
	if c.Timeout != 5*time.Second {
		t.Fatalf("expected caller override to 5s; got %v", c.Timeout)
	}
}

func TestNewClient_TransportIsSet(t *testing.T) {
	c := client.NewClient(slog.Default())
	if c.Transport == nil {
		t.Fatal("NewClient: Transport is nil; want a non-nil RoundTripper")
	}
}
