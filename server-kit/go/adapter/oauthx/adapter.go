package oauthx

import (
	"context"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/adapter"
)

// Adapter wraps an IdentityProvider into an adapter.Provider for lifecycle management.
type Adapter struct {
	provider IdentityProvider
	name     string
}

// NewAdapter creates a managed OAuth adapter from an identity provider.
func NewAdapter(name string, provider IdentityProvider) *Adapter {
	if name == "" {
		name = "google-oauth"
	}
	return &Adapter{
		provider: provider,
		name:     name,
	}
}

func (a *Adapter) Name() string       { return a.name }
func (a *Adapter) Kind() adapter.Kind { return adapter.KindOAuth }

func (a *Adapter) Probe(_ context.Context) (adapter.Health, error) {
	if a.provider == nil {
		return adapter.HealthNotServing, nil
	}
	return adapter.HealthServing, nil
}

func (a *Adapter) Status() adapter.Status {
	health := adapter.HealthServing
	if a.provider == nil {
		health = adapter.HealthNotServing
	}
	return adapter.Status{
		Name:       a.name,
		Kind:       adapter.KindOAuth,
		Health:     health,
		LastProbed: time.Now(),
		UpdatedAt:  time.Now(),
	}
}

func (a *Adapter) Close() error {
	return nil
}

// Provider returns the underlying IdentityProvider.
func (a *Adapter) Provider() IdentityProvider {
	return a.provider
}
