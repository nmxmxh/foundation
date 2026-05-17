package middleware

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
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
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
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
