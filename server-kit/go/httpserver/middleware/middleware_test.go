package middleware

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	kitlogger "github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scratch"
)

func TestRequestIDUsesProvidedOrGeneratedID(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if GetRequestID(r.Context()) == "" {
			t.Fatal("request id missing from context")
		}
		w.WriteHeader(http.StatusAccepted)
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "known")
	rr := httptest.NewRecorder()

	RequestID(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted || rr.Header().Get("X-Request-ID") != "known" {
		t.Fatalf("unexpected response: code=%d headers=%v", rr.Code, rr.Header())
	}
	if GetRequestID(context.Background()) != "" {
		t.Fatal("empty context should not have request id")
	}
}

func TestCORSOptionsShortCircuits(t *testing.T) {
	called := false
	handler := CORS(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, httptest.NewRequestWithContext(context.Background(), http.MethodOptions, "/", nil))

	if called || rr.Code != http.StatusNoContent {
		t.Fatalf("unexpected options behavior: called=%v code=%d", called, rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("cors origin header missing")
	}
}

func TestLoggerAndRecoverMiddleware(t *testing.T) {
	logger, err := kitlogger.New(kitlogger.Config{Output: io.Discard})
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	logged := Logger(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	rr := httptest.NewRecorder()
	logged.ServeHTTP(rr, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/items", nil))
	if rr.Code != http.StatusCreated {
		t.Fatalf("logger wrapped status = %d", rr.Code)
	}

	recovered := Recover(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rr = httptest.NewRecorder()
	recovered.ServeHTTP(rr, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("recover status = %d", rr.Code)
	}
}

func TestWithScratchArenaMiddleware(t *testing.T) {
	called := false
	handler := WithScratchArena(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		arena := scratch.FromContext(r.Context())
		if arena == nil {
			t.Fatal("expected scratch arena in context")
		}
		slice, err := scratch.AllocSlice[byte](arena, 128)
		if err != nil || len(slice) != 128 {
			t.Fatalf("failed to allocate from arena: err=%v len=%d", err, len(slice))
		}
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/test", nil)
	handler.ServeHTTP(rr, req)

	if !called || rr.Code != http.StatusOK {
		t.Fatalf("handler not called or bad code: called=%v code=%d", called, rr.Code)
	}

	// Test custom arena with negative/zero capacity fallback
	customHandler := WithCustomScratchArena(0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arena := scratch.FromContext(r.Context())
		if arena == nil || arena.Available() != DefaultScratchArenaSize {
			t.Fatalf("expected default size arena fallback, got %v", arena)
		}
		w.WriteHeader(http.StatusOK)
	}))
	rr2 := httptest.NewRecorder()
	customHandler.ServeHTTP(rr2, req)
	if rr2.Code != http.StatusOK {
		t.Fatalf("custom handler bad code: %d", rr2.Code)
	}
}
