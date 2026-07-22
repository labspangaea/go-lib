package server_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labspangaea/go-lib/httpx/server"
	"github.com/labspangaea/go-lib/logger"
)

func TestChain_OrderIsOuterFirst(t *testing.T) {
	var order []string

	mw := func(label string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, label+"-in")
				next.ServeHTTP(w, r)
				order = append(order, label+"-out")
			})
		}
	}

	handler := server.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "handler")
		}),
		mw("A"),
		mw("B"),
	)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	expected := []string{"A-in", "B-in", "handler", "B-out", "A-out"}
	if len(order) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, order)
	}
	for i := range expected {
		if order[i] != expected[i] {
			t.Fatalf("position %d: expected %q, got %q (full: %v)", i, expected[i], order[i], order)
		}
	}
}

func TestRequestID_GeneratesWhenMissing(t *testing.T) {
	var capturedID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The request ID should be in the response header.
		capturedID = w.Header().Get(server.HeaderRequestID)
	})

	handler := server.Chain(inner, server.RequestID())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if capturedID == "" {
		t.Fatal("RequestID should generate an ID when header is absent")
	}
	if len(capturedID) != 16 { // 8 bytes = 16 hex chars
		t.Fatalf("expected 16-char hex ID, got %q (len=%d)", capturedID, len(capturedID))
	}
}

func TestRequestID_PreservesExisting(t *testing.T) {
	var capturedID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = w.Header().Get(server.HeaderRequestID)
	})

	handler := server.Chain(inner, server.RequestID())
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(server.HeaderRequestID, "my-custom-id")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if capturedID != "my-custom-id" {
		t.Fatalf("expected preserved ID %q, got %q", "my-custom-id", capturedID)
	}
}

func TestLogging_UsesInjectedLoggerWhenContextEmpty(t *testing.T) {
	// When no logger is in context, the Logging middleware should fall back
	// to the injected `log` parameter (not Nop).
	injectedLog := logger.New(logger.WithDevelopment())
	var usedLogger *slog.Logger

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		usedLogger = logger.FromContext(r.Context())
	})

	handler := server.Chain(inner, server.Logging(injectedLog))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/test", nil))

	if logger.IsNop(usedLogger) {
		t.Fatal("Logging middleware should use the injected logger, not Nop, when context is empty")
	}
}

func TestRecover_CatchesPanic(t *testing.T) {
	log := logger.New(logger.WithDevelopment())

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	handler := server.Chain(inner, server.Recover(log))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 after panic, got %d", rec.Code)
	}
}

func TestResponseWriter_CapturesStatus(t *testing.T) {
	log := logger.New(logger.WithDevelopment())

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	})

	handler := server.Chain(inner, server.Logging(log))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/items", nil))

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}
}

func TestResponseWriter_DefaultsTo200(t *testing.T) {
	log := logger.New(logger.WithDevelopment())

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	handler := server.Chain(inner, server.Logging(log))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}
