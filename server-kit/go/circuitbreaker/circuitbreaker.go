// Package circuitbreaker provides a foundation-level circuit breaker for external service calls.
// It implements the circuit breaker pattern with configurable thresholds, timeouts, and state transitions.
//
// Usage:
//
//	cb := circuitbreaker.New("payment-gateway", circuitbreaker.Config{
//	    FailureThreshold: 5,
//	    SuccessThreshold: 2,
//	    Timeout:          30 * time.Second,
//	    HalfOpenMaxCalls: 3,
//	})
//
//	result, err := cb.Execute(ctx, func() (interface{}, error) {
//	    return paymentClient.Charge(amount)
//	})
package circuitbreaker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// State represents the current state of the circuit breaker.
type State int32

const (
	// StateClosed allows requests to pass through normally.
	StateClosed State = iota
	// StateOpen blocks all requests immediately.
	StateOpen
	// StateHalfOpen allows limited requests to test if the service has recovered.
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

var (
	// ErrCircuitOpen is returned when the circuit breaker is in the open state.
	ErrCircuitOpen = errors.New("circuit breaker is open")
	// ErrTooManyRequests is returned when too many requests are made in half-open state.
	ErrTooManyRequests = errors.New("too many requests in half-open state")
)

// Config holds the configuration for a circuit breaker.
type Config struct {
	// FailureThreshold is the number of consecutive failures before opening the circuit.
	// Default: 5
	FailureThreshold uint32

	// SuccessThreshold is the number of consecutive successes in half-open state
	// required to close the circuit. Default: 2
	SuccessThreshold uint32

	// Timeout is the duration the circuit stays open before transitioning to half-open.
	// Default: 30 seconds
	Timeout time.Duration

	// HalfOpenMaxCalls is the maximum number of calls allowed in half-open state.
	// Default: 3
	HalfOpenMaxCalls uint32

	// OnStateChange is called when the circuit breaker state changes.
	OnStateChange func(name string, from, to State)

	// IsFailure determines if an error should be counted as a failure.
	// Default: all non-nil errors are failures.
	IsFailure func(err error) bool

	// Clock is used for time operations (for testing).
	Clock Clock
}

// Clock interface for time operations.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		FailureThreshold: 5,
		SuccessThreshold: 2,
		Timeout:          30 * time.Second,
		HalfOpenMaxCalls: 3,
		IsFailure:        func(err error) bool { return err != nil },
		Clock:            realClock{},
	}
}

// CircuitBreaker implements the circuit breaker pattern.
type CircuitBreaker struct {
	name   string
	config Config

	mu              sync.Mutex
	state           int32 // atomic State
	failures        uint32
	successes       uint32
	halfOpenCalls   uint32
	lastFailureTime time.Time
	lastStateChange time.Time
}

// New creates a new circuit breaker with the given name and configuration.
func New(name string, cfg Config) *CircuitBreaker {
	if cfg.FailureThreshold == 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.SuccessThreshold == 0 {
		cfg.SuccessThreshold = 2
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.HalfOpenMaxCalls == 0 {
		cfg.HalfOpenMaxCalls = 3
	}
	if cfg.IsFailure == nil {
		cfg.IsFailure = func(err error) bool { return err != nil }
	}
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}

	return &CircuitBreaker{
		name:            name,
		config:          cfg,
		state:           int32(StateClosed),
		lastStateChange: cfg.Clock.Now(),
	}
}

// Name returns the circuit breaker's name.
func (cb *CircuitBreaker) Name() string {
	return cb.name
}

// State returns the current state of the circuit breaker.
func (cb *CircuitBreaker) State() State {
	return State(atomic.LoadInt32(&cb.state))
}

// Counts returns current failure and success counts.
func (cb *CircuitBreaker) Counts() (failures, successes uint32) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.failures, cb.successes
}

// Execute runs the given function if the circuit breaker allows it.
// Returns ErrCircuitOpen if the circuit is open, or ErrTooManyRequests
// if too many requests are being made in half-open state.
func (cb *CircuitBreaker) Execute(ctx context.Context, fn func() (interface{}, error)) (interface{}, error) {
	if err := cb.beforeRequest(); err != nil {
		return nil, err
	}

	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}

	result, err := fn()
	cb.afterRequest(err)
	return result, err
}

// ExecuteWithFallback runs the given function, falling back to the fallback function
// if the circuit is open or the primary function fails.
func (cb *CircuitBreaker) ExecuteWithFallback(
	ctx context.Context,
	fn func() (interface{}, error),
	fallback func(error) (interface{}, error),
) (interface{}, error) {
	result, err := cb.Execute(ctx, fn)
	if err != nil {
		return fallback(err)
	}
	return result, nil
}

// beforeRequest checks if a request is allowed and updates state if necessary.
func (cb *CircuitBreaker) beforeRequest() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := cb.config.Clock.Now()
	state := State(atomic.LoadInt32(&cb.state))

	switch state {
	case StateClosed:
		return nil

	case StateOpen:
		// Check if timeout has passed
		if now.Sub(cb.lastFailureTime) >= cb.config.Timeout {
			cb.transitionTo(StateHalfOpen)
			cb.halfOpenCalls = 1
			return nil
		}
		return ErrCircuitOpen

	case StateHalfOpen:
		if cb.halfOpenCalls >= cb.config.HalfOpenMaxCalls {
			return ErrTooManyRequests
		}
		cb.halfOpenCalls++
		return nil
	}

	return nil
}

// afterRequest updates the circuit breaker state based on the request result.
func (cb *CircuitBreaker) afterRequest(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	isFailure := cb.config.IsFailure(err)
	state := State(atomic.LoadInt32(&cb.state))

	switch state {
	case StateClosed:
		if isFailure {
			cb.failures++
			cb.successes = 0
			cb.lastFailureTime = cb.config.Clock.Now()
			if cb.failures >= cb.config.FailureThreshold {
				cb.transitionTo(StateOpen)
			}
		} else {
			cb.failures = 0
			cb.successes++
		}

	case StateHalfOpen:
		if isFailure {
			cb.failures++
			cb.successes = 0
			cb.lastFailureTime = cb.config.Clock.Now()
			cb.transitionTo(StateOpen)
		} else {
			cb.successes++
			cb.failures = 0
			if cb.successes >= cb.config.SuccessThreshold {
				cb.transitionTo(StateClosed)
			}
		}
	}
}

// transitionTo changes the circuit breaker state.
func (cb *CircuitBreaker) transitionTo(newState State) {
	oldState := State(atomic.LoadInt32(&cb.state))
	if oldState == newState {
		return
	}

	atomic.StoreInt32(&cb.state, int32(newState))
	cb.lastStateChange = cb.config.Clock.Now()

	// Reset counters on state change
	cb.failures = 0
	cb.successes = 0
	cb.halfOpenCalls = 0

	if cb.config.OnStateChange != nil {
		// Call outside of lock to prevent deadlocks
		go cb.config.OnStateChange(cb.name, oldState, newState)
	}
}

// Reset manually resets the circuit breaker to the closed state.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	oldState := State(atomic.LoadInt32(&cb.state))
	atomic.StoreInt32(&cb.state, int32(StateClosed))
	cb.failures = 0
	cb.successes = 0
	cb.halfOpenCalls = 0
	cb.lastStateChange = cb.config.Clock.Now()

	if cb.config.OnStateChange != nil && oldState != StateClosed {
		go cb.config.OnStateChange(cb.name, oldState, StateClosed)
	}
}

// Stats returns statistics about the circuit breaker.
type Stats struct {
	Name            string      `json:"name"`
	State           string      `json:"state"`
	Failures        uint32      `json:"failures"`
	Successes       uint32      `json:"successes"`
	LastStateChange time.Time   `json:"last_state_change"`
	LastFailureTime time.Time   `json:"last_failure_time"`
	Config          ConfigStats `json:"config"`
}

type ConfigStats struct {
	FailureThreshold uint32        `json:"failure_threshold"`
	SuccessThreshold uint32        `json:"success_threshold"`
	Timeout          time.Duration `json:"timeout"`
	HalfOpenMaxCalls uint32        `json:"half_open_max_calls"`
}

// Stats returns current statistics for the circuit breaker.
func (cb *CircuitBreaker) Stats() Stats {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	return Stats{
		Name:            cb.name,
		State:           State(atomic.LoadInt32(&cb.state)).String(),
		Failures:        cb.failures,
		Successes:       cb.successes,
		LastStateChange: cb.lastStateChange,
		LastFailureTime: cb.lastFailureTime,
		Config: ConfigStats{
			FailureThreshold: cb.config.FailureThreshold,
			SuccessThreshold: cb.config.SuccessThreshold,
			Timeout:          cb.config.Timeout,
			HalfOpenMaxCalls: cb.config.HalfOpenMaxCalls,
		},
	}
}
