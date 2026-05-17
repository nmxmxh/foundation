// Package resilience provides a unified bootstrap for all resilience patterns.
// It integrates circuit breakers, retry policies, health checks, caching,
// degradation handling, and distributed tracing into a single cohesive runtime.
package resilience

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/cache"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/circuitbreaker"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/degradation"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/healthcheck"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/retry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/tracing"
	"go.opentelemetry.io/otel/trace"
)

// Config holds all resilience configuration.
type Config struct {
	// Service identification
	ServiceName    string
	ServiceVersion string
	Environment    string

	// Tracing configuration
	TracingEnabled    bool
	TracingEndpoint   string
	TracingSampleRate float64

	// Cache configuration
	CacheBackend    string // "memory" or "redis"
	CacheDefaultTTL time.Duration
	CachePrefix     string
	RedisURL        string

	// Health check configuration
	HealthCheckInterval time.Duration
	HealthCheckTimeout  time.Duration

	// Circuit breaker defaults
	CircuitBreakerFailureThreshold uint32
	CircuitBreakerSuccessThreshold uint32
	CircuitBreakerTimeout          time.Duration

	// Retry defaults
	RetryMaxAttempts  int
	RetryInitialDelay time.Duration
	RetryMaxDelay     time.Duration

	// Degradation check interval
	DegradationCheckInterval time.Duration
}

// DefaultConfig returns sensible defaults for production use.
func DefaultConfig(serviceName string) Config {
	return Config{
		ServiceName:                    serviceName,
		ServiceVersion:                 "1.0.0",
		Environment:                    "production",
		TracingEnabled:                 false,
		TracingEndpoint:                "",
		TracingSampleRate:              0.1,
		CacheBackend:                   "memory",
		CacheDefaultTTL:                5 * time.Minute,
		CachePrefix:                    serviceName + ":",
		HealthCheckInterval:            10 * time.Second,
		HealthCheckTimeout:             5 * time.Second,
		CircuitBreakerFailureThreshold: 5,
		CircuitBreakerSuccessThreshold: 2,
		CircuitBreakerTimeout:          30 * time.Second,
		RetryMaxAttempts:               3,
		RetryInitialDelay:              100 * time.Millisecond,
		RetryMaxDelay:                  5 * time.Second,
		DegradationCheckInterval:       10 * time.Second,
	}
}

// Runtime holds all initialized resilience components.
type Runtime struct {
	config Config
	mu     sync.RWMutex

	// Core components
	HealthChecker      *healthcheck.HealthChecker
	circuitBreakers    map[string]*circuitbreaker.CircuitBreaker
	DegradationManager *degradation.Manager
	Cache              *cache.Cache
	TracingProvider    *tracing.Provider

	// Retry policies
	DefaultRetry  *retry.Policy
	HTTPRetry     *retry.Policy
	DatabaseRetry *retry.Policy

	// Shutdown tracking
	shutdownCh chan struct{}
	closed     bool
}

// New creates a new resilience runtime with the given configuration.
func New(ctx context.Context, cfg Config) (*Runtime, error) {
	r := &Runtime{
		config:          cfg,
		shutdownCh:      make(chan struct{}),
		circuitBreakers: make(map[string]*circuitbreaker.CircuitBreaker),
	}

	// Initialize health checker
	r.HealthChecker = healthcheck.New(healthcheck.Config{
		ServiceName:    cfg.ServiceName,
		DefaultTimeout: cfg.HealthCheckTimeout,
	})

	// Initialize degradation manager
	r.DegradationManager = degradation.NewManager()

	// Initialize cache
	var cacheBackend cache.Backend
	if cfg.CacheBackend == "memory" {
		cacheBackend = cache.NewMemoryBackend()
	} else {
		// For Redis backend, caller should set it up separately
		cacheBackend = cache.NewMemoryBackend()
	}
	r.Cache = cache.New(cache.Config{
		Backend:    cacheBackend,
		DefaultTTL: cfg.CacheDefaultTTL,
		Prefix:     cfg.CachePrefix,
	})

	// Initialize retry policies
	r.DefaultRetry = retry.NewPolicy(retry.Config{
		MaxAttempts:  cfg.RetryMaxAttempts,
		InitialDelay: cfg.RetryInitialDelay,
		MaxDelay:     cfg.RetryMaxDelay,
		Multiplier:   2.0,
		Jitter:       0.1,
	})
	r.HTTPRetry = retry.HTTPRetry()
	r.DatabaseRetry = retry.DatabaseRetry()

	// Initialize tracing if enabled
	if cfg.TracingEnabled && cfg.TracingEndpoint != "" {
		tp, err := tracing.NewProvider(tracing.Config{
			ServiceName: cfg.ServiceName,
			Endpoint:    cfg.TracingEndpoint,
			SampleRate:  cfg.TracingSampleRate,
			Environment: cfg.Environment,
		})
		if err != nil {
			// Log warning but don't fail - tracing is optional
			fmt.Printf("[WARN] Failed to initialize tracing: %v\n", err)
		} else {
			r.TracingProvider = tp
		}
	}

	return r, nil
}

// RegisterDependency registers a dependency for health checking and degradation monitoring.
func (r *Runtime) RegisterDependency(name string, checker func(context.Context) error, opts ...DependencyOption) {
	cfg := &dependencyConfig{
		critical:         true,
		checkInterval:    r.config.DegradationCheckInterval,
		failureThreshold: 3,
		fallbackBehavior: degradation.FallbackFailOpen,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// Add health check - wrap the error-returning function into CheckFunc
	checkFunc := healthcheck.CustomCheck(name, checker)
	r.HealthChecker.AddCheck(name, checkFunc, healthcheck.WithCritical(cfg.critical))

	// Create circuit breaker for this dependency
	r.mu.Lock()
	r.circuitBreakers[name] = circuitbreaker.New(name, circuitbreaker.Config{
		FailureThreshold: r.config.CircuitBreakerFailureThreshold,
		SuccessThreshold: r.config.CircuitBreakerSuccessThreshold,
		Timeout:          r.config.CircuitBreakerTimeout,
	})
	r.mu.Unlock()

	// Add degradation monitoring
	r.DegradationManager.Register(name, degradation.Config{
		HealthCheck:      checker,
		CheckInterval:    cfg.checkInterval,
		FailureThreshold: cfg.failureThreshold,
		FallbackBehavior: cfg.fallbackBehavior,
	})
}

// dependencyConfig holds configuration for a dependency.
type dependencyConfig struct {
	critical         bool
	checkInterval    time.Duration
	failureThreshold int
	fallbackBehavior degradation.FallbackBehavior
}

// DependencyOption configures a dependency registration.
type DependencyOption func(*dependencyConfig)

// WithCritical marks the dependency as critical (affects readiness).
func WithCritical(critical bool) DependencyOption {
	return func(c *dependencyConfig) {
		c.critical = critical
	}
}

// WithCheckInterval sets the health check interval for the dependency.
func WithCheckInterval(interval time.Duration) DependencyOption {
	return func(c *dependencyConfig) {
		c.checkInterval = interval
	}
}

// WithFailureThreshold sets the failure threshold before degradation.
func WithFailureThreshold(threshold int) DependencyOption {
	return func(c *dependencyConfig) {
		c.failureThreshold = threshold
	}
}

// WithFallbackBehavior sets the fallback behavior when degraded.
func WithFallbackBehavior(behavior degradation.FallbackBehavior) DependencyOption {
	return func(c *dependencyConfig) {
		c.fallbackBehavior = behavior
	}
}

// GetCircuitBreaker returns the circuit breaker for a dependency.
func (r *Runtime) GetCircuitBreaker(name string) *circuitbreaker.CircuitBreaker {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.circuitBreakers[name]
}

// Execute runs an operation with circuit breaker and retry protection.
func (r *Runtime) Execute(ctx context.Context, name string, fn func() error) error {
	cb := r.GetCircuitBreaker(name)
	if cb == nil {
		// No circuit breaker registered, execute directly with retry
		return r.DefaultRetry.Do(ctx, fn)
	}

	_, err := cb.Execute(ctx, func() (any, error) {
		return nil, r.DefaultRetry.Do(ctx, fn)
	})
	return err
}

// ExecuteWithResult runs an operation that returns a result with protection.
// This is a standalone function because Go doesn't support generic methods.
func ExecuteWithResult[T any](r *Runtime, ctx context.Context, name string, fn func() (T, error)) (T, error) {
	var zero T

	// Wrap the typed function to return interface{}
	wrappedFn := func() (any, error) {
		return fn()
	}

	cb := r.GetCircuitBreaker(name)
	var result any
	var err error

	if cb == nil {
		// No circuit breaker registered, execute directly with retry
		result, err = r.DefaultRetry.DoWithResult(ctx, wrappedFn)
	} else {
		result, err = cb.Execute(ctx, func() (any, error) {
			return r.DefaultRetry.DoWithResult(ctx, wrappedFn)
		})
	}

	if err != nil {
		return zero, err
	}
	if result == nil {
		return zero, nil
	}
	return result.(T), nil
}

// IsDegraded returns true if the named dependency is in degraded state.
func (r *Runtime) IsDegraded(name string) bool {
	if r.DegradationManager.GetStatus(name) == nil {
		return false
	}
	sentinel := r.DegradationManager.Sentinel(name)
	if sentinel == nil {
		return false
	}
	return !sentinel.Guard()
}

// GetSentinel returns the degradation sentinel for a dependency.
func (r *Runtime) GetSentinel(name string) *degradation.Sentinel {
	return r.DegradationManager.Sentinel(name)
}

// HealthHandler returns an HTTP handler for health checks.
func (r *Runtime) HealthHandler() http.Handler {
	return r.HealthChecker.Handler()
}

// LivenessHandler returns an HTTP handler for liveness probes.
func (r *Runtime) LivenessHandler() http.Handler {
	return r.HealthChecker.LivenessHandler()
}

// ReadinessHandler returns an HTTP handler for readiness probes.
func (r *Runtime) ReadinessHandler() http.Handler {
	return r.HealthChecker.ReadinessHandler()
}

// StartSpan starts a new tracing span if tracing is enabled.
// Returns the new context and an end function that should be called when done.
func (r *Runtime) StartSpan(ctx context.Context, name string) (context.Context, func()) {
	if r.TracingProvider == nil {
		return ctx, func() {}
	}
	newCtx, span := r.TracingProvider.Start(ctx, name)
	return newCtx, func() { span.End() }
}

// StartSpanWithOptions starts a new tracing span with options.
// Returns (ctx, nil) if tracing is disabled - callers should check for nil span.
func (r *Runtime) StartSpanWithOptions(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if r.TracingProvider == nil {
		return ctx, nil
	}
	return r.TracingProvider.Start(ctx, name, opts...)
}

// CacheGet retrieves a value from cache.
func (r *Runtime) CacheGet(ctx context.Context, key string, dest any) error {
	return r.Cache.Get(ctx, key, dest)
}

// CacheSet stores a value in cache with the given TTL.
func (r *Runtime) CacheSet(ctx context.Context, key string, value any, ttl time.Duration) error {
	return r.Cache.Set(ctx, key, value, ttl)
}

// CacheGetOrSet gets from cache or computes and stores the value.
func CacheGetOrSet[T any](ctx context.Context, r *Runtime, key string, compute func() (T, error), ttl time.Duration) (T, error) {
	return cache.GetOrSet(ctx, r.Cache, key, compute, ttl)
}

// Start begins background monitoring goroutines.
// Note: The degradation manager auto-starts health checks when Register is called.
func (r *Runtime) Start(ctx context.Context) {
	// Nothing to start - healthcheck runs on-demand, degradation manager
	// starts its health check goroutines when dependencies are registered.
}

// Close shuts down all resilience components gracefully.
func (r *Runtime) Close(ctx context.Context) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	close(r.shutdownCh)
	r.mu.Unlock()

	// Stop degradation manager (stops health check goroutines)
	r.DegradationManager.Stop()

	// Shutdown tracing
	if r.TracingProvider != nil {
		if err := r.TracingProvider.Shutdown(ctx); err != nil {
			return fmt.Errorf("tracing shutdown: %w", err)
		}
	}

	return nil
}

// Status returns the overall resilience status.
func (r *Runtime) Status() Status {
	health := r.HealthChecker.RunChecks(context.Background(), false)

	cbStats := make(map[string]CircuitBreakerStatus)
	r.mu.RLock()
	for name, cb := range r.circuitBreakers {
		stats := cb.Stats()
		cbStats[name] = CircuitBreakerStatus{
			State:       stats.State,
			Failures:    int64(stats.Failures),
			Successes:   int64(stats.Successes),
			LastFailure: stats.LastFailureTime,
		}
	}
	r.mu.RUnlock()

	degraded := make(map[string]bool)
	for name := range r.DegradationManager.AllStatus() {
		degraded[name] = r.IsDegraded(name)
	}

	return Status{
		Healthy:         health.Status == healthcheck.StatusHealthy,
		HealthDetails:   health,
		CircuitBreakers: cbStats,
		Degraded:        degraded,
	}
}

// Status represents the overall resilience status.
type Status struct {
	Healthy         bool                            `json:"healthy"`
	HealthDetails   healthcheck.HealthResponse      `json:"health_details"`
	CircuitBreakers map[string]CircuitBreakerStatus `json:"circuit_breakers"`
	Degraded        map[string]bool                 `json:"degraded"`
}

// CircuitBreakerStatus represents a circuit breaker's status.
type CircuitBreakerStatus struct {
	State       string    `json:"state"`
	Failures    int64     `json:"failures"`
	Successes   int64     `json:"successes"`
	LastFailure time.Time `json:"last_failure"`
}
