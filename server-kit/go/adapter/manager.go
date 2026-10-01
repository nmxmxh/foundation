package adapter

import (
	"context"
	"errors"
	"sort"
	"sync"
)

var (
	// ErrDuplicateAdapter indicates an adapter with the same name exists.
	ErrDuplicateAdapter = errors.New("adapter: duplicate adapter name")
	// ErrNilAdapter indicates the provided adapter is nil.
	ErrNilAdapter = errors.New("adapter: provider cannot be nil")
)

// Manager coordinates lifecycle and health monitoring for registered adapters.
type Manager struct {
	mu       sync.RWMutex
	adapters map[string]Provider
	order    []string
}

// NewManager creates an initialized adapter manager.
func NewManager() *Manager {
	return &Manager{
		adapters: make(map[string]Provider),
		order:    make([]string, 0),
	}
}

// Register adds a provider to the manager.
func (m *Manager) Register(p Provider) error {
	if p == nil {
		return ErrNilAdapter
	}
	name := p.Name()
	if name == "" {
		return errors.New("adapter: provider name cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.adapters[name]; exists {
		return ErrDuplicateAdapter
	}
	m.adapters[name] = p
	m.order = append(m.order, name)
	return nil
}

// Get retrieves a registered provider by name.
func (m *Manager) Get(name string) (Provider, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.adapters[name]
	return p, ok
}

// List returns all registered providers in registration order.
func (m *Manager) List() []Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Provider, 0, len(m.order))
	for _, name := range m.order {
		if p, ok := m.adapters[name]; ok {
			out = append(out, p)
		}
	}
	return out
}

// Statuses returns a sorted slice of health snapshots.
func (m *Manager) Statuses() []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Status, 0, len(m.adapters))
	for _, p := range m.adapters {
		out = append(out, p.Status())
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

// Healthy returns true when all registered adapters are serving or degraded.
func (m *Manager) Healthy() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, p := range m.adapters {
		if !p.Status().Health.OK() {
			return false
		}
	}
	return true
}

// ProbeAll runs health checks concurrently across all adapters with bounded execution.
func (m *Manager) ProbeAll(ctx context.Context) map[string]Health {
	m.mu.RLock()
	providers := make([]Provider, 0, len(m.adapters))
	for _, p := range m.adapters {
		providers = append(providers, p)
	}
	m.mu.RUnlock()

	results := make(map[string]Health, len(providers))
	if len(providers) == 0 {
		return results
	}

	var (
		resMu sync.Mutex
		wg    sync.WaitGroup
	)

	for _, p := range providers {
		wg.Add(1)
		go func(prov Provider) {
			defer wg.Done()
			h, _ := prov.Probe(ctx)
			resMu.Lock()
			results[prov.Name()] = h
			resMu.Unlock()
		}(p)
	}

	wg.Wait()
	return results
}

// Close terminates all registered adapters in reverse registration order.
func (m *Manager) Close() error {
	m.mu.Lock()
	names := make([]string, len(m.order))
	copy(names, m.order)
	adapters := m.adapters
	m.adapters = make(map[string]Provider)
	m.order = make([]string, 0)
	m.mu.Unlock()

	var firstErr error
	for i := len(names) - 1; i >= 0; i-- {
		name := names[i]
		if p, ok := adapters[name]; ok {
			if err := p.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
