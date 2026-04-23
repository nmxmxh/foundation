// Package degradation provides formalized degradation modes for external dependencies.
// It allows services to gracefully handle dependency failures with fallback behaviors.
//
// Usage:
//
//	dm := degradation.NewManager()
//	dm.Register("redis", degradation.Config{
//	    HealthCheck: func(ctx) error { return redis.Ping(ctx) },
//	    CheckInterval: 10 * time.Second,
//	    RecoveryThreshold: 3,
//	})
//
//	if dm.IsHealthy("redis") {
//	    // use redis
//	} else {
//	    // fallback behavior
//	}
package degradation

import (
	"context"
	"sync"
	"time"
)

// Mode represents the operational mode of a dependency.
type Mode string

const (
	// ModeNormal indicates the dependency is fully operational.
	ModeNormal Mode = "normal"
	// ModeDegraded indicates the dependency is partially available.
	ModeDegraded Mode = "degraded"
	// ModeUnavailable indicates the dependency is completely unavailable.
	ModeUnavailable Mode = "unavailable"
	// ModeRecovering indicates the dependency is recovering from failure.
	ModeRecovering Mode = "recovering"
)

// Config holds configuration for a dependency.
type Config struct {
	// HealthCheck is called to verify dependency health.
	HealthCheck func(ctx context.Context) error

	// CheckInterval is how often to check health. Default: 10s
	CheckInterval time.Duration

	// FailureThreshold is how many failures before marking unavailable. Default: 3
	FailureThreshold int

	// RecoveryThreshold is how many successes needed to recover. Default: 2
	RecoveryThreshold int

	// Timeout for health checks. Default: 5s
	Timeout time.Duration

	// OnModeChange is called when the mode changes.
	OnModeChange func(name string, oldMode, newMode Mode)

	// FallbackBehavior defines what to do when unavailable.
	FallbackBehavior FallbackBehavior
}

// FallbackBehavior defines fallback strategies.
type FallbackBehavior string

const (
	// FallbackFailOpen allows requests to proceed without the dependency.
	FallbackFailOpen FallbackBehavior = "fail_open"
	// FallbackFailClosed rejects requests when dependency is unavailable.
	FallbackFailClosed FallbackBehavior = "fail_closed"
	// FallbackCache uses cached data when available.
	FallbackCache FallbackBehavior = "cache"
	// FallbackDefault uses default values.
	FallbackDefault FallbackBehavior = "default"
)

// Dependency tracks the state of an external dependency.
type Dependency struct {
	Name              string
	Config            Config
	Mode              Mode
	ConsecutiveFails  int
	ConsecutiveSuccess int
	LastCheck         time.Time
	LastError         error
	mu                sync.RWMutex
}

// Manager manages multiple dependencies and their degradation states.
type Manager struct {
	dependencies map[string]*Dependency
	mu           sync.RWMutex
	stopCh       chan struct{}
	wg           sync.WaitGroup
}

// NewManager creates a new degradation manager.
func NewManager() *Manager {
	return &Manager{
		dependencies: make(map[string]*Dependency),
		stopCh:       make(chan struct{}),
	}
}

// Register registers a dependency with the manager.
func (m *Manager) Register(name string, cfg Config) {
	if cfg.CheckInterval == 0 {
		cfg.CheckInterval = 10 * time.Second
	}
	if cfg.FailureThreshold == 0 {
		cfg.FailureThreshold = 3
	}
	if cfg.RecoveryThreshold == 0 {
		cfg.RecoveryThreshold = 2
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.FallbackBehavior == "" {
		cfg.FallbackBehavior = FallbackFailClosed
	}

	dep := &Dependency{
		Name:   name,
		Config: cfg,
		Mode:   ModeNormal,
	}

	m.mu.Lock()
	m.dependencies[name] = dep
	m.mu.Unlock()

	// Start health check goroutine
	m.wg.Add(1)
	go m.runHealthChecks(dep)
}

// Unregister removes a dependency from the manager.
func (m *Manager) Unregister(name string) {
	m.mu.Lock()
	delete(m.dependencies, name)
	m.mu.Unlock()
}

// runHealthChecks periodically checks dependency health.
func (m *Manager) runHealthChecks(dep *Dependency) {
	defer m.wg.Done()

	ticker := time.NewTicker(dep.Config.CheckInterval)
	defer ticker.Stop()

	// Initial check
	m.checkHealth(dep)

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.checkHealth(dep)
		}
	}
}

// checkHealth performs a single health check.
func (m *Manager) checkHealth(dep *Dependency) {
	ctx, cancel := context.WithTimeout(context.Background(), dep.Config.Timeout)
	defer cancel()

	err := dep.Config.HealthCheck(ctx)

	dep.mu.Lock()
	defer dep.mu.Unlock()

	dep.LastCheck = time.Now()
	dep.LastError = err
	oldMode := dep.Mode

	if err != nil {
		dep.ConsecutiveFails++
		dep.ConsecutiveSuccess = 0

		if dep.ConsecutiveFails >= dep.Config.FailureThreshold {
			dep.Mode = ModeUnavailable
		} else if dep.Mode == ModeNormal {
			dep.Mode = ModeDegraded
		}
	} else {
		dep.ConsecutiveSuccess++
		dep.ConsecutiveFails = 0

		if dep.Mode == ModeUnavailable {
			dep.Mode = ModeRecovering
		}

		if dep.ConsecutiveSuccess >= dep.Config.RecoveryThreshold {
			dep.Mode = ModeNormal
		}
	}

	if oldMode != dep.Mode && dep.Config.OnModeChange != nil {
		go dep.Config.OnModeChange(dep.Name, oldMode, dep.Mode)
	}
}

// Stop stops all health checks.
func (m *Manager) Stop() {
	close(m.stopCh)
	m.wg.Wait()
}

// IsHealthy returns true if the dependency is in normal mode.
func (m *Manager) IsHealthy(name string) bool {
	return m.GetMode(name) == ModeNormal
}

// IsAvailable returns true if the dependency is usable (normal or degraded).
func (m *Manager) IsAvailable(name string) bool {
	mode := m.GetMode(name)
	return mode == ModeNormal || mode == ModeDegraded || mode == ModeRecovering
}

// GetMode returns the current mode of a dependency.
func (m *Manager) GetMode(name string) Mode {
	m.mu.RLock()
	dep, ok := m.dependencies[name]
	m.mu.RUnlock()

	if !ok {
		return ModeUnavailable
	}

	dep.mu.RLock()
	defer dep.mu.RUnlock()
	return dep.Mode
}

// GetStatus returns detailed status for a dependency.
func (m *Manager) GetStatus(name string) *DependencyStatus {
	m.mu.RLock()
	dep, ok := m.dependencies[name]
	m.mu.RUnlock()

	if !ok {
		return nil
	}

	dep.mu.RLock()
	defer dep.mu.RUnlock()

	status := &DependencyStatus{
		Name:               dep.Name,
		Mode:               dep.Mode,
		LastCheck:          dep.LastCheck,
		ConsecutiveFails:   dep.ConsecutiveFails,
		ConsecutiveSuccess: dep.ConsecutiveSuccess,
		FallbackBehavior:   dep.Config.FallbackBehavior,
	}

	if dep.LastError != nil {
		status.LastError = dep.LastError.Error()
	}

	return status
}

// DependencyStatus is the detailed status of a dependency.
type DependencyStatus struct {
	Name               string           `json:"name"`
	Mode               Mode             `json:"mode"`
	LastCheck          time.Time        `json:"last_check"`
	LastError          string           `json:"last_error,omitempty"`
	ConsecutiveFails   int              `json:"consecutive_fails"`
	ConsecutiveSuccess int              `json:"consecutive_success"`
	FallbackBehavior   FallbackBehavior `json:"fallback_behavior"`
}

// AllStatus returns status for all dependencies.
func (m *Manager) AllStatus() map[string]*DependencyStatus {
	m.mu.RLock()
	names := make([]string, 0, len(m.dependencies))
	for name := range m.dependencies {
		names = append(names, name)
	}
	m.mu.RUnlock()

	result := make(map[string]*DependencyStatus, len(names))
	for _, name := range names {
		result[name] = m.GetStatus(name)
	}
	return result
}

// Execute executes a function with fallback handling.
func (m *Manager) Execute(ctx context.Context, name string, fn func() error, fallback func() error) error {
	if m.IsAvailable(name) {
		err := fn()
		if err == nil {
			return nil
		}

		// Record failure for next health check evaluation
		m.mu.RLock()
		dep, ok := m.dependencies[name]
		m.mu.RUnlock()

		if ok {
			dep.mu.Lock()
			dep.ConsecutiveFails++
			dep.ConsecutiveSuccess = 0
			dep.LastError = err
			dep.mu.Unlock()
		}

		// Check fallback behavior
		behavior := m.getFallbackBehavior(name)
		if behavior == FallbackFailClosed {
			return err
		}
	}

	// Run fallback
	if fallback != nil {
		return fallback()
	}

	return ErrDependencyUnavailable
}

func (m *Manager) getFallbackBehavior(name string) FallbackBehavior {
	m.mu.RLock()
	dep, ok := m.dependencies[name]
	m.mu.RUnlock()

	if !ok {
		return FallbackFailClosed
	}

	return dep.Config.FallbackBehavior
}

// ErrDependencyUnavailable is returned when a dependency is unavailable.
var ErrDependencyUnavailable = dependencyError("dependency unavailable")

type dependencyError string

func (e dependencyError) Error() string { return string(e) }

// WithFallback is a helper that wraps a function with fallback logic.
func WithFallback[T any](m *Manager, name string, fn func() (T, error), fallback func() (T, error)) func() (T, error) {
	return func() (T, error) {
		if m.IsAvailable(name) {
			result, err := fn()
			if err == nil {
				return result, nil
			}
		}

		if fallback != nil {
			return fallback()
		}

		var zero T
		return zero, ErrDependencyUnavailable
	}
}

// Sentinel provides a simple check-and-use pattern.
type Sentinel struct {
	manager *Manager
	name    string
}

// NewSentinel creates a sentinel for a specific dependency.
func (m *Manager) Sentinel(name string) *Sentinel {
	return &Sentinel{manager: m, name: name}
}

// Guard returns true if the dependency is available.
func (s *Sentinel) Guard() bool {
	return s.manager.IsAvailable(s.name)
}

// Healthy returns true if the dependency is fully healthy.
func (s *Sentinel) Healthy() bool {
	return s.manager.IsHealthy(s.name)
}

// Mode returns the current mode.
func (s *Sentinel) Mode() Mode {
	return s.manager.GetMode(s.name)
}
