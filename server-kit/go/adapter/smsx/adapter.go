package smsx

import (
	"context"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/adapter"
)

// Adapter wraps an SMS Sender into an adapter.Provider.
type Adapter struct {
	sender Sender
	name   string
}

// NewAdapter creates a managed SMS adapter.
func NewAdapter(name string, sender Sender) *Adapter {
	if name == "" {
		name = "sms"
	}
	return &Adapter{
		sender: sender,
		name:   name,
	}
}

func (a *Adapter) Name() string       { return a.name }
func (a *Adapter) Kind() adapter.Kind { return adapter.KindSMS }

func (a *Adapter) Probe(ctx context.Context) (adapter.Health, error) {
	if a.sender == nil {
		return adapter.HealthNotServing, nil
	}
	return a.sender.Probe(ctx)
}

func (a *Adapter) Status() adapter.Status {
	health := adapter.HealthServing
	if a.sender == nil {
		health = adapter.HealthNotServing
	}
	return adapter.Status{
		Name:       a.name,
		Kind:       adapter.KindSMS,
		Health:     health,
		LastProbed: time.Now(),
		UpdatedAt:  time.Now(),
	}
}

func (a *Adapter) Close() error {
	if a.sender != nil {
		return a.sender.Close()
	}
	return nil
}

// Sender returns the underlying SMS Sender.
func (a *Adapter) Sender() Sender {
	return a.sender
}
