package redis_test

import (
	"testing"

	"github.com/labspangaea/go-lib/cache/redis"
)

// These tests validate option wiring and key-prefix logic without a live Redis.
// Integration tests against a real Redis should be in a separate _integration_test.go
// file behind a build tag.

func TestWithKeyPrefix(t *testing.T) {
	// We can't call Get/Set without a real Redis client, but we can verify
	// that New does not panic and options are accepted.
	// A full integration test would use testcontainers or a mock.
	c := redis.New[string](nil, redis.WithKeyPrefix[string]("myapp:prod"))
	if c == nil {
		t.Fatal("New should return a non-nil Cache")
	}
}

func TestCustomMarshal(t *testing.T) {
	called := false
	marshal := func(v string) ([]byte, error) {
		called = true
		return []byte(v), nil
	}
	c := redis.New[string](nil,
		redis.WithMarshal[string](marshal),
		redis.WithUnmarshal[string](func(b []byte, v *string) error {
			*v = string(b)
			return nil
		}),
	)
	if c == nil {
		t.Fatal("New should return a non-nil Cache")
	}
	// Can't exercise marshal without a real client, but verify wiring didn't panic.
	_ = called
}
