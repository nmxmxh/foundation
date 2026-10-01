package pushx

import (
	"context"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/adapter"
)

// Adapter wraps the push delivery runner into an adapter.Provider.
type Adapter struct {
	runner *Runner
	name   string
}

// NewAdapter creates a managed push adapter.
func NewAdapter(name string, runner *Runner) *Adapter {
	if name == "" {
		name = "push"
	}
	return &Adapter{
		runner: runner,
		name:   name,
	}
}

func (a *Adapter) Name() string       { return a.name }
func (a *Adapter) Kind() adapter.Kind { return adapter.KindPush }

func (a *Adapter) Probe(_ context.Context) (adapter.Health, error) {
	if a.runner == nil {
		return adapter.HealthNotServing, nil
	}
	return adapter.HealthServing, nil
}

func (a *Adapter) Status() adapter.Status {
	health := adapter.HealthServing
	if a.runner == nil {
		health = adapter.HealthNotServing
	}
	return adapter.Status{
		Name:       a.name,
		Kind:       adapter.KindPush,
		Health:     health,
		LastProbed: time.Now(),
		UpdatedAt:  time.Now(),
	}
}

func (a *Adapter) Close() error {
	return nil
}

// Runner returns the underlying outbox runner.
func (a *Adapter) Runner() *Runner {
	return a.runner
}
