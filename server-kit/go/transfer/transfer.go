// Package transfer provides a long-lived, correlation-anchored primitive for
// operations whose durable truth is "started / finished" but whose user
// experience needs the continuous in-between: uploads, downloads, exports,
// media transcodes, and other streamed work.
//
// A transfer rides two lanes:
//
//   - The fact lane (package events): durable, replayable, terminal-only
//     bookend events `<domain>:<action>[:vN]:requested|success|failed`.
//   - The progress lane (this package): ephemeral, best-effort, coalesced,
//     last-write-wins Update fan-out that is never written to the event log.
//
// The two lanes are tied together by a single CorrelationID (and a TransferID).
// This gives progress event-grade capability (correlation, tracing, RBAC
// metadata, subscription) without inheriting event semantics (durability,
// replay, idempotency) that progress ticks must not have.
package transfer

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Phase is a point in a transfer lifecycle.
//
// The non-terminal phases form a forward-only pipeline:
//
//	Pending -> Uploading -> Staged -> Processing -> Ready
//
// Any non-terminal phase may also move to Failed or Aborted. Ready, Failed,
// and Aborted are terminal.
type Phase string

const (
	// PhasePending is the initial phase before any bytes move.
	PhasePending Phase = "pending"
	// PhaseUploading means bytes are actively being received or sent.
	PhaseUploading Phase = "uploading"
	// PhaseStaged means all bytes have landed but post-processing has not begun.
	PhaseStaged Phase = "staged"
	// PhaseProcessing means the staged object is being transformed/validated.
	PhaseProcessing Phase = "processing"
	// PhaseReady is the terminal success phase.
	PhaseReady Phase = "ready"
	// PhaseFailed is the terminal failure phase.
	PhaseFailed Phase = "failed"
	// PhaseAborted is the terminal phase for caller- or deadline-cancelled work.
	PhaseAborted Phase = "aborted"
)

// Sentinel errors. Callers may match these with errors.Is.
var (
	// ErrInvalidTransition is returned when a phase change violates the state machine.
	ErrInvalidTransition = errors.New("transfer: invalid phase transition")
	// ErrTerminal is returned when mutating a transfer that has already settled.
	ErrTerminal = errors.New("transfer: already in a terminal phase")
	// ErrEmptyID is returned when a transfer id is missing.
	ErrEmptyID = errors.New("transfer: id is required")
	// ErrEmptyCorrelation is returned when a correlation id is missing.
	ErrEmptyCorrelation = errors.New("transfer: correlation id is required")
	// ErrNegativeBytes is returned when a byte count is negative.
	ErrNegativeBytes = errors.New("transfer: byte counts must be non-negative")
	// ErrNotFound is returned by the manager when a transfer id is unknown.
	ErrNotFound = errors.New("transfer: not found")
	// ErrCapacity is returned when the manager has reached its active ceiling.
	ErrCapacity = errors.New("transfer: active transfer capacity reached")
	// ErrDuplicate is returned when beginning a transfer whose id is already live.
	ErrDuplicate = errors.New("transfer: id already active")
)

// terminalPhases is the closed set of settled phases.
var terminalPhases = map[Phase]struct{}{
	PhaseReady:   {},
	PhaseFailed:  {},
	PhaseAborted: {},
}

// forwardEdges is the legal forward (non-terminal) pipeline.
var forwardEdges = map[Phase]Phase{
	PhasePending:    PhaseUploading,
	PhaseUploading:  PhaseStaged,
	PhaseStaged:     PhaseProcessing,
	PhaseProcessing: PhaseReady,
}

// IsTerminal reports whether p is a settled phase.
func IsTerminal(p Phase) bool {
	_, ok := terminalPhases[p]
	return ok
}

// IsValidPhase reports whether p is a known phase.
func IsValidPhase(p Phase) bool {
	switch p {
	case PhasePending, PhaseUploading, PhaseStaged, PhaseProcessing,
		PhaseReady, PhaseFailed, PhaseAborted:
		return true
	default:
		return false
	}
}

// CanTransition reports whether moving from -> to is legal.
//
// Rules:
//   - from must be a known, non-terminal phase.
//   - Failed and Aborted are reachable from any non-terminal phase.
//   - Otherwise only the single forward edge for `from` is allowed.
//   - Self-transitions are not transitions and are rejected.
func CanTransition(from, to Phase) bool {
	if !IsValidPhase(from) || !IsValidPhase(to) {
		return false
	}
	if IsTerminal(from) || from == to {
		return false
	}
	if to == PhaseFailed || to == PhaseAborted {
		return true
	}
	next, ok := forwardEdges[from]
	return ok && next == to
}

// Update is an immutable snapshot of a transfer at a point in time. It is the
// payload of the progress lane. Updates are safe to drop and to coalesce: only
// the highest Seq for a given TransferID is authoritative.
type Update struct {
	TransferID    string    `json:"transfer_id"`
	CorrelationID string    `json:"correlation_id"`
	Phase         Phase     `json:"phase"`
	BytesDone     int64     `json:"bytes_done"`
	BytesTotal    int64     `json:"bytes_total"` // 0 means unknown / not yet declared.
	Checksum      string    `json:"checksum,omitempty"`
	Reason        string    `json:"reason,omitempty"` // populated for failed/aborted.
	Seq           uint64    `json:"seq"`
	Timestamp     time.Time `json:"timestamp"`
}

// Fraction returns completion in the closed interval [0,1]. It returns 0 when
// the total is unknown, and 1 once the transfer has settled successfully.
func (u Update) Fraction() float64 {
	if u.Phase == PhaseReady {
		return 1
	}
	if u.BytesTotal <= 0 {
		return 0
	}
	if u.BytesDone >= u.BytesTotal {
		return 1
	}
	return float64(u.BytesDone) / float64(u.BytesTotal)
}

// Terminal reports whether the snapshot is in a settled phase.
func (u Update) Terminal() bool { return IsTerminal(u.Phase) }

// validateIdentifier trims and enforces a non-empty token without colons so it
// stays compatible with the event-type and metadata token grammars.
func validateIdentifier(kind, value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		switch kind {
		case "id":
			return "", ErrEmptyID
		case "correlation":
			return "", ErrEmptyCorrelation
		default:
			return "", fmt.Errorf("transfer: %s is required", kind)
		}
	}
	if strings.ContainsAny(v, ": \t\n") {
		return "", fmt.Errorf("transfer: %s %q must not contain whitespace or ':'", kind, v)
	}
	return v, nil
}
