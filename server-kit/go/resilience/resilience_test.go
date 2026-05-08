package resilience

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/degradation"
)

func TestDefaultConfigSetsBoundedProductionDefaults(t *testing.T) {
	cfg := DefaultConfig("billing")

	if cfg.ServiceName != "billing" || cfg.Environment != "production" {
		t.Fatalf("unexpected service defaults: %+v", cfg)
	}
	if cfg.RetryMaxAttempts != 3 {
		t.Fatalf("RetryMaxAttempts = %d, want 3", cfg.RetryMaxAttempts)
	}
	if cfg.HealthCheckTimeout <= 0 || cfg.DegradationCheckInterval <= 0 {
		t.Fatalf("expected bounded health/degradation defaults: %+v", cfg)
	}
}

func TestRuntimeExecuteAndCacheHelpers(t *testing.T) {
	runtime, err := New(context.Background(), DefaultConfig("orders"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = runtime.Close(context.Background()) }()

	calls := 0
	if err := runtime.Execute(context.Background(), "unregistered", func() error {
		calls++
		return nil
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if runtime.IsDegraded("unregistered") {
		t.Fatalf("unregistered dependency should not be degraded")
	}

	if err := runtime.CacheSet(context.Background(), "tenant", "ovasabi", time.Minute); err != nil {
		t.Fatalf("CacheSet() error = %v", err)
	}
	var got string
	if err := runtime.CacheGet(context.Background(), "tenant", &got); err != nil {
		t.Fatalf("CacheGet() error = %v", err)
	}
	if got != "ovasabi" {
		t.Fatalf("cache value = %q", got)
	}

	computeCalls := 0
	computed, err := CacheGetOrSet(context.Background(), runtime, "computed", func() (string, error) {
		computeCalls++
		return "value", nil
	}, time.Minute)
	if err != nil {
		t.Fatalf("CacheGetOrSet() error = %v", err)
	}
	if computed != "value" || computeCalls != 1 {
		t.Fatalf("computed=%q calls=%d", computed, computeCalls)
	}
	computed, err = CacheGetOrSet(context.Background(), runtime, "computed", func() (string, error) {
		computeCalls++
		return "new", nil
	}, time.Minute)
	if err != nil {
		t.Fatalf("cached CacheGetOrSet() error = %v", err)
	}
	if computed != "value" || computeCalls != 1 {
		t.Fatalf("expected cached value, got %q calls=%d", computed, computeCalls)
	}

	spanCtx, end := runtime.StartSpan(context.Background(), "orders.test")
	end()
	if spanCtx == nil {
		t.Fatalf("expected span context")
	}
	if _, span := runtime.StartSpanWithOptions(context.Background(), "orders.test"); span != nil {
		t.Fatalf("expected nil span when tracing is disabled")
	}
	runtime.Start(context.Background())
}

func TestExecuteWithResultRetriesAndReturnsValue(t *testing.T) {
	runtime, err := New(context.Background(), DefaultConfig("orders"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = runtime.Close(context.Background()) }()

	attempts := 0
	got, err := ExecuteWithResult(runtime, context.Background(), "partner", func() (string, error) {
		attempts++
		if attempts == 1 {
			return "", errors.New("transient")
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("ExecuteWithResult() error = %v", err)
	}
	if got != "ok" || attempts != 2 {
		t.Fatalf("got %q after %d attempts, want ok after retry", got, attempts)
	}
}

func TestRegisterDependencyStatusAndHandlers(t *testing.T) {
	cfg := DefaultConfig("orders")
	cfg.DegradationCheckInterval = time.Hour
	runtime, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = runtime.Close(context.Background()) }()

	runtime.RegisterDependency(
		"database",
		func(context.Context) error { return nil },
		WithCritical(false),
		WithCheckInterval(time.Hour),
		WithFailureThreshold(2),
		WithFallbackBehavior(degradation.FallbackCache),
	)
	if runtime.GetCircuitBreaker("database") == nil {
		t.Fatalf("expected circuit breaker registration")
	}
	if runtime.GetSentinel("database") == nil {
		t.Fatalf("expected degradation sentinel")
	}

	for name, handler := range map[string]http.Handler{
		"health":    runtime.HealthHandler(),
		"liveness":  runtime.LivenessHandler(),
		"readiness": runtime.ReadinessHandler(),
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/"+name, nil))
		if recorder.Code < 200 || recorder.Code >= 300 {
			t.Fatalf("%s handler status = %d", name, recorder.Code)
		}
	}

	status := runtime.Status()
	if !status.Healthy {
		t.Fatalf("expected healthy status: %+v", status)
	}
	if _, ok := status.CircuitBreakers["database"]; !ok {
		t.Fatalf("expected circuit breaker status")
	}
}

func TestRuntimeCloseIsIdempotent(t *testing.T) {
	runtime, err := New(context.Background(), DefaultConfig("orders"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}
