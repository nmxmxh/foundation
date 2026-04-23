package security

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

func TestRedisRateLimiter(t *testing.T) {
	client := redis.NewMemoryClient("")
	limiter := NewRedisRateLimiter(client, 2, time.Second)

	handler := limiter.Limit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Request 1: Allow
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr1.Code)
	}

	// Request 2: Allow
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr2.Code)
	}

	// Request 3: Rate Limited
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	rr3 := httptest.NewRecorder()
	handler.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusTooManyRequests {
		t.Errorf("expected status 429, got %d", rr3.Code)
	}

	// Wait for window to pass
	time.Sleep(1100 * time.Millisecond)

	// Request 4: Allow again
	req4 := httptest.NewRequest(http.MethodGet, "/", nil)
	rr4 := httptest.NewRecorder()
	handler.ServeHTTP(rr4, req4)
	if rr4.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr4.Code)
	}
}

func TestRedisRateLimiter_Fallback(t *testing.T) {
	// Limiter with nil client should allow all
	limiter := NewRedisRateLimiter(nil, 1, time.Second)
	handler := limiter.Limit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for i := 0; i < 5; i++ {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected status 200 on fallback, got %d", rr.Code)
		}
	}
}

func TestRedisRateLimiter_Allow(t *testing.T) {
	client := redis.NewMemoryClient("")
	limiter := NewRedisRateLimiter(client, 1, time.Second)

	if !limiter.Allow(context.Background(), "test-key") {
		t.Error("expected first allow to be true")
	}
	if limiter.Allow(context.Background(), "test-key") {
		t.Error("expected second allow to be false")
	}

	time.Sleep(1100 * time.Millisecond)
	if !limiter.Allow(context.Background(), "test-key") {
		t.Error("expected allow after window to be true")
	}
}
