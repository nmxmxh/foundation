package whatsappx

import (
	"context"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/adapter"
)

// Adapter wraps the WhatsApp client into an adapter.Provider.
type Adapter struct {
	client *Client
	name   string
}

// NewAdapter creates a managed WhatsApp adapter.
func NewAdapter(name string, client *Client) *Adapter {
	if name == "" {
		name = "whatsapp"
	}
	return &Adapter{
		client: client,
		name:   name,
	}
}

func (a *Adapter) Name() string       { return a.name }
func (a *Adapter) Kind() adapter.Kind { return adapter.KindMessaging }

func (a *Adapter) Probe(_ context.Context) (adapter.Health, error) {
	if a.client == nil {
		return adapter.HealthNotServing, nil
	}
	return adapter.HealthServing, nil
}

func (a *Adapter) Status() adapter.Status {
	health := adapter.HealthServing
	if a.client == nil {
		health = adapter.HealthNotServing
	}
	return adapter.Status{
		Name:       a.name,
		Kind:       adapter.KindMessaging,
		Health:     health,
		LastProbed: time.Now(),
		UpdatedAt:  time.Now(),
	}
}

func (a *Adapter) Close() error {
	return nil
}

// Client returns the underlying WhatsApp Client.
func (a *Adapter) Client() *Client {
	return a.client
}
