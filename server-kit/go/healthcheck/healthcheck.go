// Package healthcheck provides a reusable health check builder with dependency probes.
// It supports liveness and readiness checks with configurable timeouts.
//
// Usage:
//
//	hc := healthcheck.New(healthcheck.Config{
//	    ServiceName: "my-service",
//	})
//
//	hc.AddCheck("database", healthcheck.DatabaseCheck(db))
//	hc.AddCheck("redis", healthcheck.RedisCheck(redisClient))
//	hc.AddCheck("external-api", healthcheck.HTTPCheck("https://api.example.com/health"))
//
//	http.Handle("/health", hc.Handler())
//	http.Handle("/health/live", hc.LivenessHandler())
//	http.Handle("/health/ready", hc.ReadinessHandler())
package healthcheck

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"sync"
	"syscall"
	"time"
)

// Status represents the health status of a component.
type Status string

const (
	StatusHealthy   Status = "healthy"
	StatusUnhealthy Status = "unhealthy"
	StatusDegraded  Status = "degraded"
	StatusUnknown   Status = "unknown"
)

// CheckResult is the result of a health check.
type CheckResult struct {
	Status    Status         `json:"status"`
	Message   string         `json:"message,omitempty"`
	Duration  time.Duration  `json:"duration_ms"`
	Timestamp time.Time      `json:"timestamp"`
	Details   map[string]any `json:"details,omitempty"`
}

// CheckFunc is a function that performs a health check.
type CheckFunc func(ctx context.Context) CheckResult

// Check represents a named health check.
type Check struct {
	Name        string
	CheckFunc   CheckFunc
	Timeout     time.Duration
	Critical    bool // If true, failing this check makes the service unhealthy
	ForLiveness bool // If true, include in liveness checks
}

// Config holds configuration for the health checker.
type Config struct {
	// ServiceName is the name of the service.
	ServiceName string

	// ServiceVersion is the version of the service.
	ServiceVersion string

	// DefaultTimeout is the default timeout for checks. Default: 5s
	DefaultTimeout time.Duration

	// CacheDuration is how long to cache results. Default: 0 (no cache)
	CacheDuration time.Duration
}

// HealthChecker manages health checks.
type HealthChecker struct {
	config  Config
	checks  []Check
	mu      sync.RWMutex
	cache   map[string]cachedResult
	cacheMu sync.RWMutex
}

type cachedResult struct {
	result    CheckResult
	expiresAt time.Time
}

// HealthResponse is the response returned by health endpoints.
type HealthResponse struct {
	Status         Status                 `json:"status"`
	ServiceName    string                 `json:"service_name"`
	ServiceVersion string                 `json:"service_version,omitempty"`
	Timestamp      time.Time              `json:"timestamp"`
	Duration       time.Duration          `json:"duration_ms"`
	Checks         map[string]CheckResult `json:"checks,omitempty"`
}

// New creates a new health checker.
func New(cfg Config) *HealthChecker {
	if cfg.DefaultTimeout == 0 {
		cfg.DefaultTimeout = 5 * time.Second
	}

	return &HealthChecker{
		config: cfg,
		checks: make([]Check, 0),
		cache:  make(map[string]cachedResult),
	}
}

// AddCheck adds a health check.
func (hc *HealthChecker) AddCheck(name string, fn CheckFunc, opts ...CheckOption) {
	check := Check{
		Name:      name,
		CheckFunc: fn,
		Timeout:   hc.config.DefaultTimeout,
		Critical:  true,
	}

	for _, opt := range opts {
		opt(&check)
	}

	hc.mu.Lock()
	hc.checks = append(hc.checks, check)
	hc.mu.Unlock()
}

// CheckOption modifies a check's configuration.
type CheckOption func(*Check)

// WithTimeout sets the timeout for a check.
func WithTimeout(d time.Duration) CheckOption {
	return func(c *Check) {
		c.Timeout = d
	}
}

// WithCritical marks a check as critical.
func WithCritical(critical bool) CheckOption {
	return func(c *Check) {
		c.Critical = critical
	}
}

// WithLiveness includes the check in liveness probes.
func WithLiveness(liveness bool) CheckOption {
	return func(c *Check) {
		c.ForLiveness = liveness
	}
}

// RunChecks runs all health checks and returns the overall health.
func (hc *HealthChecker) RunChecks(ctx context.Context, livenessOnly bool) HealthResponse {
	start := time.Now()
	response := HealthResponse{
		Status:         StatusHealthy,
		ServiceName:    hc.config.ServiceName,
		ServiceVersion: hc.config.ServiceVersion,
		Timestamp:      start,
		Checks:         make(map[string]CheckResult),
	}

	hc.mu.RLock()
	checks := make([]Check, len(hc.checks))
	copy(checks, hc.checks)
	hc.mu.RUnlock()

	// Run checks concurrently
	var wg sync.WaitGroup
	var mu sync.Mutex
	results := make(map[string]CheckResult)

	for _, check := range checks {
		if livenessOnly && !check.ForLiveness {
			continue
		}

		wg.Add(1)
		go func(c Check) {
			defer wg.Done()

			// Check cache
			if hc.config.CacheDuration > 0 {
				hc.cacheMu.RLock()
				cached, ok := hc.cache[c.Name]
				hc.cacheMu.RUnlock()
				if ok && time.Now().Before(cached.expiresAt) {
					mu.Lock()
					results[c.Name] = cached.result
					mu.Unlock()
					return
				}
			}

			// Run check with timeout
			checkCtx, cancel := context.WithTimeout(ctx, c.Timeout)
			defer cancel()

			result := c.CheckFunc(checkCtx)

			// Update cache
			if hc.config.CacheDuration > 0 {
				hc.cacheMu.Lock()
				hc.cache[c.Name] = cachedResult{
					result:    result,
					expiresAt: time.Now().Add(hc.config.CacheDuration),
				}
				hc.cacheMu.Unlock()
			}

			mu.Lock()
			results[c.Name] = result
			mu.Unlock()
		}(check)
	}

	wg.Wait()

	// Determine overall status
	response.Checks = results
	for _, check := range checks {
		if livenessOnly && !check.ForLiveness {
			continue
		}

		result, ok := results[check.Name]
		if !ok {
			continue
		}

		if result.Status == StatusUnhealthy && check.Critical {
			response.Status = StatusUnhealthy
		} else if result.Status == StatusDegraded && response.Status == StatusHealthy {
			response.Status = StatusDegraded
		}
	}

	response.Duration = time.Since(start)
	return response
}

// Handler returns an HTTP handler for the combined health check.
func (hc *HealthChecker) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := hc.RunChecks(r.Context(), false)
		hc.writeResponse(w, response)
	})
}

// LivenessHandler returns an HTTP handler for liveness checks.
func (hc *HealthChecker) LivenessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := hc.RunChecks(r.Context(), true)
		hc.writeResponse(w, response)
	})
}

// ReadinessHandler returns an HTTP handler for readiness checks.
func (hc *HealthChecker) ReadinessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := hc.RunChecks(r.Context(), false)
		hc.writeResponse(w, response)
	})
}

func (hc *HealthChecker) writeResponse(w http.ResponseWriter, response HealthResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	status := http.StatusOK
	if response.Status == StatusUnhealthy {
		status = http.StatusServiceUnavailable
	}

	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		_ = err
	}
}

// Pre-built check functions

// PingCheck returns a simple check that always succeeds.
func PingCheck() CheckFunc {
	return func(ctx context.Context) CheckResult {
		return CheckResult{
			Status:    StatusHealthy,
			Message:   "pong",
			Timestamp: time.Now(),
		}
	}
}

// DatabaseCheck returns a check for SQL database connectivity.
func DatabaseCheck(db *sql.DB) CheckFunc {
	return func(ctx context.Context) CheckResult {
		start := time.Now()
		result := CheckResult{
			Timestamp: start,
		}

		if err := db.PingContext(ctx); err != nil {
			result.Status = StatusUnhealthy
			result.Message = err.Error()
		} else {
			result.Status = StatusHealthy
			result.Message = "database is reachable"

			// Get stats
			stats := db.Stats()
			result.Details = map[string]any{
				"open_connections": stats.OpenConnections,
				"in_use":           stats.InUse,
				"idle":             stats.Idle,
			}
		}

		result.Duration = time.Since(start)
		return result
	}
}

// Pinger interface for types that have a Ping method.
type Pinger interface {
	Ping(ctx context.Context) error
}

// PingerCheck returns a check for types implementing Pinger (like Redis clients).
func PingerCheck(p Pinger, name string) CheckFunc {
	return func(ctx context.Context) CheckResult {
		start := time.Now()
		result := CheckResult{
			Timestamp: start,
		}

		if err := p.Ping(ctx); err != nil {
			result.Status = StatusUnhealthy
			result.Message = fmt.Sprintf("%s: %s", name, err.Error())
		} else {
			result.Status = StatusHealthy
			result.Message = fmt.Sprintf("%s is reachable", name)
		}

		result.Duration = time.Since(start)
		return result
	}
}

// HTTPCheck returns a check for HTTP endpoint availability.
func HTTPCheck(url string) CheckFunc {
	return func(ctx context.Context) CheckResult {
		start := time.Now()
		result := CheckResult{
			Timestamp: start,
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			result.Status = StatusUnhealthy
			result.Message = err.Error()
			result.Duration = time.Since(start)
			return result
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			result.Status = StatusUnhealthy
			result.Message = err.Error()
		} else {
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				result.Status = StatusHealthy
				result.Message = fmt.Sprintf("HTTP %d", resp.StatusCode)
			} else {
				result.Status = StatusUnhealthy
				result.Message = fmt.Sprintf("HTTP %d", resp.StatusCode)
			}
			result.Details = map[string]any{
				"status_code": resp.StatusCode,
			}
		}

		result.Duration = time.Since(start)
		return result
	}
}

// TCPCheck returns a check for TCP connectivity.
func TCPCheck(address string) CheckFunc {
	return func(ctx context.Context) CheckResult {
		start := time.Now()
		result := CheckResult{
			Timestamp: start,
		}

		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", address)
		if err != nil {
			result.Status = StatusUnhealthy
			result.Message = err.Error()
		} else {
			_ = conn.Close()
			result.Status = StatusHealthy
			result.Message = fmt.Sprintf("TCP connection to %s successful", address)
		}

		result.Duration = time.Since(start)
		return result
	}
}

// DiskSpaceCheck returns a check for available disk space.
func DiskSpaceCheck(path string, minFreeBytes uint64) CheckFunc {
	return func(ctx context.Context) CheckResult {
		start := time.Now()
		result := CheckResult{
			Timestamp: start,
		}

		var stat syscall.Statfs_t
		if err := syscall.Statfs(path, &stat); err != nil {
			result.Status = StatusUnhealthy
			result.Message = err.Error()
			result.Duration = time.Since(start)
			return result
		}

		freeBytes := stat.Bavail * uint64(stat.Bsize)
		totalBytes := stat.Blocks * uint64(stat.Bsize)

		result.Details = map[string]any{
			"path":        path,
			"free_bytes":  freeBytes,
			"total_bytes": totalBytes,
			"free_pct":    float64(freeBytes) / float64(totalBytes) * 100,
		}

		if freeBytes < minFreeBytes {
			result.Status = StatusUnhealthy
			result.Message = fmt.Sprintf("disk space low: %d bytes free", freeBytes)
		} else {
			result.Status = StatusHealthy
			result.Message = fmt.Sprintf("disk space OK: %d bytes free", freeBytes)
		}

		result.Duration = time.Since(start)
		return result
	}
}

// MemoryCheck returns a check for memory usage.
func MemoryCheck(maxUsedPct float64) CheckFunc {
	return func(ctx context.Context) CheckResult {
		start := time.Now()
		result := CheckResult{
			Timestamp: start,
		}

		var m runtime.MemStats
		runtime.ReadMemStats(&m)

		result.Details = map[string]any{
			"alloc_mb":       m.Alloc / 1024 / 1024,
			"total_alloc_mb": m.TotalAlloc / 1024 / 1024,
			"sys_mb":         m.Sys / 1024 / 1024,
			"num_gc":         m.NumGC,
		}

		result.Status = StatusHealthy
		result.Message = fmt.Sprintf("memory: %d MB allocated", m.Alloc/1024/1024)
		result.Duration = time.Since(start)
		return result
	}
}

// CustomCheck returns a check that runs a custom function.
func CustomCheck(name string, fn func(ctx context.Context) error) CheckFunc {
	return func(ctx context.Context) CheckResult {
		start := time.Now()
		result := CheckResult{
			Timestamp: start,
		}

		if err := fn(ctx); err != nil {
			result.Status = StatusUnhealthy
			result.Message = err.Error()
		} else {
			result.Status = StatusHealthy
			result.Message = fmt.Sprintf("%s OK", name)
		}

		result.Duration = time.Since(start)
		return result
	}
}
