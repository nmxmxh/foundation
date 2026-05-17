package circuitbreaker

import (
	"maps"
	"sync"
)

// Registry manages multiple circuit breakers for different services.
type Registry struct {
	mu       sync.RWMutex
	breakers map[string]*CircuitBreaker
	config   Config // default config for new breakers
}

// NewRegistry creates a new circuit breaker registry with default configuration.
func NewRegistry(defaultConfig Config) *Registry {
	return &Registry{
		breakers: make(map[string]*CircuitBreaker),
		config:   defaultConfig,
	}
}

// Get returns a circuit breaker by name, creating one if it doesn't exist.
func (r *Registry) Get(name string) *CircuitBreaker {
	r.mu.RLock()
	cb, ok := r.breakers[name]
	r.mu.RUnlock()
	if ok {
		return cb
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	if cb, ok := r.breakers[name]; ok {
		return cb
	}

	cb = New(name, r.config)
	r.breakers[name] = cb
	return cb
}

// GetWithConfig returns a circuit breaker with custom configuration.
func (r *Registry) GetWithConfig(name string, cfg Config) *CircuitBreaker {
	r.mu.Lock()
	defer r.mu.Unlock()

	if cb, ok := r.breakers[name]; ok {
		return cb
	}

	cb := New(name, cfg)
	r.breakers[name] = cb
	return cb
}

// Remove removes a circuit breaker from the registry.
func (r *Registry) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.breakers, name)
}

// Reset resets all circuit breakers to closed state.
func (r *Registry) Reset() {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, cb := range r.breakers {
		cb.Reset()
	}
}

// All returns all circuit breakers in the registry.
func (r *Registry) All() map[string]*CircuitBreaker {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]*CircuitBreaker, len(r.breakers))
	maps.Copy(result, r.breakers)
	return result
}

// AllStats returns stats for all circuit breakers.
func (r *Registry) AllStats() map[string]Stats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]Stats, len(r.breakers))
	for k, v := range r.breakers {
		result[k] = v.Stats()
	}
	return result
}

// Global registry instance
var globalRegistry *Registry
var globalOnce sync.Once

// Global returns the global circuit breaker registry.
func Global() *Registry {
	globalOnce.Do(func() {
		globalRegistry = NewRegistry(DefaultConfig())
	})
	return globalRegistry
}

// SetGlobalConfig sets the default configuration for the global registry.
// Must be called before any circuit breakers are created.
func SetGlobalConfig(cfg Config) {
	globalOnce.Do(func() {
		globalRegistry = NewRegistry(cfg)
	})
}
