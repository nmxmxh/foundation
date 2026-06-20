package connector

import (
	"context"
	"sort"
	"sync"
)

// Manager owns a set of connectors keyed by name and drives their lifecycle as
// a unit. It is the natural integration point for startup.Runtime: register the
// connectors an app needs, Start them with the runtime context, and AddCloser
// the manager's Close.
type Manager struct {
	mu         sync.RWMutex
	connectors map[string]*Connector
	started    bool
	ctx        context.Context
}

// NewManager returns an empty Manager.
func NewManager() *Manager {
	return &Manager{connectors: map[string]*Connector{}}
}

// Add constructs a connector from cfg and registers it. If the manager is
// already started, the new connector's supervisor is started immediately.
func (m *Manager) Add(cfg Config) (*Connector, error) {
	c, err := New(cfg)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if existing, ok := m.connectors[cfg.Name]; ok {
		m.mu.Unlock()
		_ = c.Close()
		return existing, nil
	}
	m.connectors[cfg.Name] = c
	started, ctx := m.started, m.ctx
	m.mu.Unlock()
	if started {
		c.Start(ctx)
	}
	return c, nil
}

// Get returns a connector by name.
func (m *Manager) Get(name string) (*Connector, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.connectors[name]
	return c, ok
}

// Start launches every registered connector's supervisor and remembers ctx so
// connectors added later also start.
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	m.started = true
	m.ctx = ctx
	conns := make([]*Connector, 0, len(m.connectors))
	for _, c := range m.connectors {
		conns = append(conns, c)
	}
	m.mu.Unlock()
	for _, c := range conns {
		c.Start(ctx)
	}
}

// Statuses returns a snapshot of every connector's status, sorted by name.
func (m *Manager) Statuses() []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Status, 0, len(m.connectors))
	for _, c := range m.connectors {
		out = append(out, c.Status())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Healthy reports whether every connector is currently serving or degraded
// (i.e. none is hard-down). Useful for a coarse readiness gate.
func (m *Manager) Healthy() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, c := range m.connectors {
		if !c.Status().Health.OK() {
			return false
		}
	}
	return true
}

// Close stops and releases every connector. It satisfies startup.CloseFunc.
func (m *Manager) Close() error {
	m.mu.Lock()
	conns := make([]*Connector, 0, len(m.connectors))
	for _, c := range m.connectors {
		conns = append(conns, c)
	}
	m.connectors = map[string]*Connector{}
	m.mu.Unlock()
	var firstErr error
	for _, c := range conns {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
