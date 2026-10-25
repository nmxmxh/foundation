package security

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMapletRateLimiter_AllowAndBlock(t *testing.T) {
	limiter := NewMapletRateLimiter(3, 50*time.Millisecond, 100)

	// First 3 requests allowed
	for i := 1; i <= 3; i++ {
		if !limiter.Allow("client_1") {
			t.Fatalf("request %d should be allowed", i)
		}
	}

	// 4th request blocked
	if limiter.Allow("client_1") {
		t.Fatalf("request 4 should be blocked")
	}

	// Different client allowed
	if !limiter.Allow("client_2") {
		t.Fatalf("client_2 should be allowed")
	}

	// Memory bytes check
	if limiter.MemoryBytes() == 0 {
		t.Fatalf("expected non-zero memory bytes")
	}
	if limiter.WindowRemaining() == 0 {
		t.Fatalf("expected non-zero window remaining")
	}

	// Wait for window expiration
	time.Sleep(60 * time.Millisecond)

	// After window reset, client_1 allowed again
	if !limiter.Allow("client_1") {
		t.Fatalf("client_1 should be allowed after window reset")
	}
}

func TestMapletRateLimiter_DefaultOptions(t *testing.T) {
	limiter := NewMapletRateLimiter(0, 0, 0)
	if limiter.limit != 100 {
		t.Fatalf("expected default limit 100, got %d", limiter.limit)
	}
	if limiter.window != time.Minute {
		t.Fatalf("expected default window 1m, got %v", limiter.window)
	}
}

func TestMapletRateLimiter_HTTPMiddleware(t *testing.T) {
	limiter := NewMapletRateLimiter(2, time.Second, 100)

	handlerCalled := 0
	handler := limiter.Limit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled++
		w.WriteHeader(http.StatusOK)
	}))

	// Request 1: allowed
	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req1.RemoteAddr = "192.168.1.1:1234"
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec1.Code)
	}

	// Request 2: allowed
	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req2.RemoteAddr = "192.168.1.1:1234"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec2.Code)
	}

	// Request 3: blocked (429)
	req3 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req3.RemoteAddr = "192.168.1.1:1234"
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests, got %d", rec3.Code)
	}

	if handlerCalled != 2 {
		t.Fatalf("expected handler called 2 times, got %d", handlerCalled)
	}

	// Test WindowRemaining when window expired
	limiterExpired := NewMapletRateLimiter(5, 5*time.Millisecond, 10)
	time.Sleep(10 * time.Millisecond)
	if rem := limiterExpired.WindowRemaining(); rem != 0 {
		t.Fatalf("expected 0 remaining for expired window, got %v", rem)
	}

	// Uncovered Context Helpers on empty context
	ctx := req1.Context()
	if id := GetSessionIDFromContext(ctx); id != "" {
		t.Fatalf("expected empty session id")
	}
	if exp := GetAccessExpiresAtFromContext(ctx); exp != "" {
		t.Fatalf("expected empty access expires")
	}
	if exp := GetRefreshExpiresAtFromContext(ctx); exp != "" {
		t.Fatalf("expected empty refresh expires")
	}
	if org := GetOrganizationIDFromContext(ctx); org != "" {
		t.Fatalf("expected empty org id")
	}

	// Legacy RateLimiter default options coverage
	legacyLimiter := NewRateLimiter(0, 0)
	if legacyLimiter.limit != 200 || legacyLimiter.window != time.Minute {
		t.Fatalf("unexpected defaults on legacy limiter")
	}
}

func BenchmarkMapletRateLimiter_Allow(b *testing.B) {
	limiter := NewMapletRateLimiter(200, time.Hour, 10000)
	ips := make([]string, 1000)
	for i := range 1000 {
		ips[i] = "10.0.1.12"
	}

	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		_ = limiter.Allow(ips[i%1000])
	}
}

func BenchmarkLegacyRateLimiter_Allow(b *testing.B) {
	limiter := NewRateLimiter(200, time.Hour)
	ips := make([]string, 1000)
	for i := range 1000 {
		ips[i] = "10.0.1.12"
	}

	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		_ = limiter.Allow(ips[i%1000])
	}
}
