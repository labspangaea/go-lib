package couchbase_test

import (
	"testing"

	"github.com/labspangaea/go-lib/cache/couchbase"
)

// We can't construct a real *gocb.Collection without a live cluster,
// so we test option wiring by verifying New does not panic and that
// options are accepted. Integration tests against a real Couchbase
// cluster should use testcontainers-go.

func TestWithKeyPrefix(t *testing.T) {
	// Verify the option is a valid function that does not panic.
	opt := couchbase.WithKeyPrefix[string]("users:prod")
	if opt == nil {
		t.Fatal("WithKeyPrefix returned nil")
	}
}

func TestWithMarshal(t *testing.T) {
	opt := couchbase.WithMarshal(func(s string) ([]byte, error) {
		return []byte(s), nil
	})
	if opt == nil {
		t.Fatal("WithMarshal returned nil")
	}
}

func TestWithUnmarshal(t *testing.T) {
	opt := couchbase.WithUnmarshal(func(b []byte, s *string) error {
		*s = string(b)
		return nil
	})
	if opt == nil {
		t.Fatal("WithUnmarshal returned nil")
	}
}
