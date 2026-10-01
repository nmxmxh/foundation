package adapter

import (
	"context"
	"time"
)

// Kind identifies the adapter category.
type Kind string

const (
	KindPush      Kind = "push"
	KindOAuth     Kind = "oauth"
	KindMessaging Kind = "messaging"
	KindEmail     Kind = "email"
	KindSMS       Kind = "sms"
)

// Health represents the operational condition of an adapter.
type Health int32

const (
	HealthUnknown Health = iota
	HealthServing
	HealthDegraded
	HealthNotServing
)

// String returns the name of the health state.
func (h Health) String() string {
	switch h {
	case HealthServing:
		return "serving"
	case HealthDegraded:
		return "degraded"
	case HealthNotServing:
		return "not_serving"
	default:
		return "unknown"
	}
}

// OK returns true when health is serving or degraded.
func (h Health) OK() bool {
	return h == HealthServing || h == HealthDegraded
}

// Status captures a point-in-time snapshot of adapter condition.
type Status struct {
	Name        string         `json:"name"`
	Kind        Kind           `json:"kind"`
	Health      Health         `json:"health"`
	LastProbed  time.Time      `json:"last_probed,omitzero"`
	LastLatency time.Duration  `json:"last_latency_ns"`
	LastError   string         `json:"last_error,omitempty"`
	UpdatedAt   time.Time      `json:"updated_at,omitzero"`
	Details     map[string]any `json:"details,omitempty"`
}

// Provider represents the universal contract every adapter subpackage implements.
type Provider interface {
	// Name returns the unique identifier of the adapter.
	Name() string

	// Kind returns the adapter category.
	Kind() Kind

	// Probe checks adapter health within the provided deadline.
	Probe(ctx context.Context) (Health, error)

	// Status returns the latest health snapshot.
	Status() Status

	// Close releases any resources held by the adapter.
	Close() error
}
