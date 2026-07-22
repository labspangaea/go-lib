package distlock_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/labspangaea/go-lib/distlock"
	"github.com/labspangaea/go-lib/distlock/mocks"
)

// --- WithLock tests ---

func TestWithLock_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	ml := mocks.NewMockLock(ctrl)

	ml.EXPECT().Lock(gomock.Any()).Return(nil)
	ml.EXPECT().Unlock(gomock.Any()).Return(nil)

	called := false
	err := distlock.WithLock(context.Background(), ml, func(ctx context.Context) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("fn was not called")
	}
}

func TestWithLock_FnError_ReturnsItAndUnlocks(t *testing.T) {
	ctrl := gomock.NewController(t)
	ml := mocks.NewMockLock(ctrl)

	fnErr := errors.New("business logic failed")
	ml.EXPECT().Lock(gomock.Any()).Return(nil)
	ml.EXPECT().Unlock(gomock.Any()).Return(nil)

	err := distlock.WithLock(context.Background(), ml, func(ctx context.Context) error {
		return fnErr
	})
	if !errors.Is(err, fnErr) {
		t.Errorf("expected fn error, got %v", err)
	}
}

func TestWithLock_UnlockError_WhenFnSucceeds(t *testing.T) {
	ctrl := gomock.NewController(t)
	ml := mocks.NewMockLock(ctrl)

	ml.EXPECT().Lock(gomock.Any()).Return(nil)
	ml.EXPECT().Unlock(gomock.Any()).Return(distlock.ErrNotHeld)

	err := distlock.WithLock(context.Background(), ml, func(ctx context.Context) error {
		return nil
	})
	if !errors.Is(err, distlock.ErrNotHeld) {
		t.Errorf("expected ErrNotHeld, got %v", err)
	}
}

func TestWithLock_FnError_TakesPriorityOverUnlockError(t *testing.T) {
	ctrl := gomock.NewController(t)
	ml := mocks.NewMockLock(ctrl)

	fnErr := errors.New("fn failed")
	ml.EXPECT().Lock(gomock.Any()).Return(nil)
	ml.EXPECT().Unlock(gomock.Any()).Return(distlock.ErrNotHeld)

	err := distlock.WithLock(context.Background(), ml, func(ctx context.Context) error {
		return fnErr
	})
	if !errors.Is(err, fnErr) {
		t.Errorf("expected fn error to take priority, got %v", err)
	}
}

func TestWithLock_LockFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	ml := mocks.NewMockLock(ctrl)

	ml.EXPECT().Lock(gomock.Any()).Return(distlock.ErrNotAcquired)

	err := distlock.WithLock(context.Background(), ml, func(ctx context.Context) error {
		t.Fatal("fn should not be called when Lock fails")
		return nil
	})
	if !errors.Is(err, distlock.ErrNotAcquired) {
		t.Errorf("expected ErrNotAcquired, got %v", err)
	}
}

// --- Sentinel errors ---

func TestErrNotHeld_ErrorsIs(t *testing.T) {
	wrapped := fmt.Errorf("operation failed: %w", distlock.ErrNotHeld)
	if !errors.Is(wrapped, distlock.ErrNotHeld) {
		t.Error("errors.Is should match wrapped ErrNotHeld")
	}
}

func TestErrNotAcquired_ErrorsIs(t *testing.T) {
	wrapped := fmt.Errorf("timeout: %w", distlock.ErrNotAcquired)
	if !errors.Is(wrapped, distlock.ErrNotAcquired) {
		t.Error("errors.Is should match wrapped ErrNotAcquired")
	}
}

func TestErrNotHeld_Message(t *testing.T) {
	if distlock.ErrNotHeld.Error() != "distlock: lock not held by this instance" {
		t.Errorf("unexpected message: %q", distlock.ErrNotHeld.Error())
	}
}

func TestErrNotAcquired_Message(t *testing.T) {
	if distlock.ErrNotAcquired.Error() != "distlock: context expired before lock was acquired" {
		t.Errorf("unexpected message: %q", distlock.ErrNotAcquired.Error())
	}
}

// --- Option constructors ---

func TestWithRetryDelay(t *testing.T) {
	cfg := &distlock.LockConfig{}
	distlock.WithRetryDelay(200 * time.Millisecond)(cfg)
	if cfg.RetryDelay != 200*time.Millisecond {
		t.Errorf("RetryDelay = %v, want 200ms", cfg.RetryDelay)
	}
}

func TestWithRetryJitter(t *testing.T) {
	cfg := &distlock.LockConfig{}
	distlock.WithRetryJitter(75 * time.Millisecond)(cfg)
	if cfg.RetryJitter != 75*time.Millisecond {
		t.Errorf("RetryJitter = %v, want 75ms", cfg.RetryJitter)
	}
}
