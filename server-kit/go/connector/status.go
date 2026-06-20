package connector

import "time"

// Health is the tri-state-plus health model for a remote endpoint. It mirrors
// the gRPC health-checking vocabulary (SERVING / NOT_SERVING / UNKNOWN) with an
// added DEGRADED state for "reachable but breaching SLO".
type Health int32

const (
	// HealthUnknown means the remote has not yet been probed, or its status
	// could not be determined.
	HealthUnknown Health = iota
	// HealthServing means the remote is reachable and within SLO.
	HealthServing
	// HealthDegraded means the remote is reachable but breaching SLO (high
	// latency, elevated error rate, partial availability).
	HealthDegraded
	// HealthNotServing means the remote is unreachable or failing.
	HealthNotServing
)

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

// OK reports whether the health state permits new traffic.
func (h Health) OK() bool { return h == HealthServing || h == HealthDegraded }

// Phase is the lifecycle phase of the supervisor (intelligent thread). It is
// distinct from Health: Health describes the remote, Phase describes what the
// supervisor is currently doing about it.
type Phase int32

const (
	// PhaseInit is the state before the supervisor has started.
	PhaseInit Phase = iota
	// PhaseProbing means the supervisor is actively checking the remote.
	PhaseProbing
	// PhaseConnected means the connector is healthy and serving traffic.
	PhaseConnected
	// PhaseDegraded means the connector is serving but the remote is degraded.
	PhaseDegraded
	// PhaseRecovering means a heal action is in flight (backoff, reconnect,
	// transport failover, half-open probe).
	PhaseRecovering
	// PhaseDown means the connector has given up routing to the current
	// transport and the breaker is open.
	PhaseDown
	// PhaseClosed means the supervisor has stopped.
	PhaseClosed
)

func (p Phase) String() string {
	switch p {
	case PhaseProbing:
		return "probing"
	case PhaseConnected:
		return "connected"
	case PhaseDegraded:
		return "degraded"
	case PhaseRecovering:
		return "recovering"
	case PhaseDown:
		return "down"
	case PhaseClosed:
		return "closed"
	default:
		return "init"
	}
}

// Status is an immutable snapshot of a connector's knowledge at a point in time.
// It is the unit the supervisor emits and the manager aggregates.
type Status struct {
	Name            string        `json:"name"`
	Transport       string        `json:"transport"`
	Endpoint        string        `json:"endpoint"`
	Health          Health        `json:"health"`
	Phase           Phase         `json:"phase"`
	Breaker         string        `json:"breaker"`
	Capabilities    Capabilities  `json:"capabilities"`
	Watermark       string        `json:"watermark,omitempty"`
	ConsecFailures  int           `json:"consec_failures"`
	ConsecSuccesses int           `json:"consec_successes"`
	LastLatency     time.Duration `json:"last_latency_ns"`
	LastError       string        `json:"last_error,omitempty"`
	LastProbedAt    time.Time     `json:"last_probed_at,omitzero"`
	LastChangedAt   time.Time     `json:"last_changed_at,omitzero"`
	UpdatedAt       time.Time     `json:"updated_at,omitzero"`
}
