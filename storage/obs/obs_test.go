package obs_test

import (
	"context"
	"testing"
	"time"

	"github.com/labspangaea/go-lib/storage/obs"
)

// newTestClient creates a Client with a fake endpoint.
// It will fail at SDK construction if the endpoint is malformed; the tests here
// only exercise pre-flight context logic, which does not make network calls.
func newTestClient(t *testing.T) *obs.Client {
	t.Helper()
	c, err := obs.New("ak", "sk", "https://obs.example.com")
	if err != nil {
		t.Fatalf("obs.New: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

// cancelledCtx returns a context that is already cancelled.
func cancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// expiredCtx returns a context whose deadline has already passed.
func expiredCtx() context.Context {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	cancel()
	return ctx
}

func TestPut_CancelledContext(t *testing.T) {
	c := newTestClient(t)
	err := c.Put(cancelledCtx(), "bucket", "key", nil)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestPut_ExpiredDeadline(t *testing.T) {
	c := newTestClient(t)
	err := c.Put(expiredCtx(), "bucket", "key", nil)
	if err == nil {
		t.Fatal("expected error from expired deadline, got nil")
	}
	if err != context.DeadlineExceeded {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestGet_CancelledContext(t *testing.T) {
	c := newTestClient(t)
	_, _, err := c.Get(cancelledCtx(), "bucket", "key")
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestGet_ExpiredDeadline(t *testing.T) {
	c := newTestClient(t)
	_, _, err := c.Get(expiredCtx(), "bucket", "key")
	if err != context.DeadlineExceeded {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestDelete_CancelledContext(t *testing.T) {
	c := newTestClient(t)
	err := c.Delete(cancelledCtx(), "bucket", "key")
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestDelete_ExpiredDeadline(t *testing.T) {
	c := newTestClient(t)
	err := c.Delete(expiredCtx(), "bucket", "key")
	if err != context.DeadlineExceeded {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestStat_CancelledContext(t *testing.T) {
	c := newTestClient(t)
	_, err := c.Stat(cancelledCtx(), "bucket", "key")
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestStat_ExpiredDeadline(t *testing.T) {
	c := newTestClient(t)
	_, err := c.Stat(expiredCtx(), "bucket", "key")
	if err != context.DeadlineExceeded {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestPresignGet_CancelledContext(t *testing.T) {
	c := newTestClient(t)
	_, err := c.PresignGet(cancelledCtx(), "bucket", "key", time.Hour)
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestPresignGet_ExpiredDeadline(t *testing.T) {
	c := newTestClient(t)
	_, err := c.PresignGet(expiredCtx(), "bucket", "key", time.Hour)
	if err != context.DeadlineExceeded {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestPresignPut_CancelledContext(t *testing.T) {
	c := newTestClient(t)
	_, err := c.PresignPut(cancelledCtx(), "bucket", "key", time.Hour)
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestPresignPut_ExpiredDeadline(t *testing.T) {
	c := newTestClient(t)
	_, err := c.PresignPut(expiredCtx(), "bucket", "key", time.Hour)
	if err != context.DeadlineExceeded {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestWithDefaultTimeout_Applied(t *testing.T) {
	// Verify New does not error when WithDefaultTimeout is used.
	c, err := obs.New("ak", "sk", "https://obs.example.com", obs.WithDefaultTimeout(10))
	if err != nil {
		t.Fatalf("obs.New with WithDefaultTimeout: %v", err)
	}
	c.Close()
}

func TestWithDefaultTimeout_Zero_Ignored(t *testing.T) {
	// Zero value should be ignored (use default).
	c, err := obs.New("ak", "sk", "https://obs.example.com", obs.WithDefaultTimeout(0))
	if err != nil {
		t.Fatalf("obs.New with WithDefaultTimeout(0): %v", err)
	}
	c.Close()
}
