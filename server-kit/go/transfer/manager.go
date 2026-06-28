package transfer

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
)

// DefaultMaxActive bounds the number of concurrently tracked transfers when a
// Manager is configured with a non-positive ceiling (CP-02: bounded state).
const DefaultMaxActive = 4096

// metaTransferID is the metadata key under which the transfer id is threaded
// onto bookend events so consumers can correlate the fact lane to the progress
// lane without parsing the payload.
const metaTransferID = "transfer_id"

// Manager registers trackers, enforces a bounded active set, and brackets each
// transfer with the durable, terminal-only bookend events on the fact lane:
//
//	<domain>:<action>[:vN]:requested   at Begin
//	<domain>:<action>[:vN]:success     at Complete
//	<domain>:<action>[:vN]:failed      at Fail / Abort
//
// The progress lane (Tracker fan-out) lives entirely off the event log.
type Manager struct {
	domain  string
	action  string
	version string

	bus       events.Bus // optional; nil disables bookend events.
	maxActive int
	now       func() time.Time

	mu       sync.RWMutex
	trackers map[string]*Tracker
}

// Config configures a Manager.
type Config struct {
	// Domain and Action form the event-type head, e.g. domain "media",
	// action "upload" -> "media:upload:...". Both are required.
	Domain string
	Action string
	// Version is an optional event-type version segment such as "v1".
	Version string
	// Bus, when set, receives bookend events. When nil, bookends are skipped
	// and the Manager is a pure in-memory progress registry.
	Bus events.Bus
	// MaxActive bounds concurrently tracked transfers. Defaults to
	// DefaultMaxActive when non-positive.
	MaxActive int
	// Now overrides the clock for deterministic tests. Defaults to time.Now (UTC).
	Now func() time.Time
}

// NewManager validates cfg and constructs a Manager.
func NewManager(cfg Config) (*Manager, error) {
	domain := strings.TrimSpace(cfg.Domain)
	if domain == "" {
		return nil, fmt.Errorf("transfer: manager domain is required")
	}
	action := strings.TrimSpace(cfg.Action)
	if action == "" {
		return nil, fmt.Errorf("transfer: manager action is required")
	}
	version := strings.TrimSpace(cfg.Version)
	max := cfg.MaxActive
	if max <= 0 {
		max = DefaultMaxActive
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	m := &Manager{
		domain:    domain,
		action:    action,
		version:   version,
		bus:       cfg.Bus,
		maxActive: max,
		now:       now,
		trackers:  make(map[string]*Tracker),
	}
	// Fail fast if the configured identity cannot form a legal event type.
	if err := events.ValidateEventType(m.eventType("requested")); err != nil {
		return nil, fmt.Errorf("transfer: invalid manager identity: %w", err)
	}
	return m, nil
}

// eventType assembles the bookend event type for a terminal state.
func (m *Manager) eventType(state string) string {
	head := m.domain + ":" + m.action
	if m.version != "" {
		head += ":" + m.version
	}
	return head + ":" + state
}

// BeginInput parameterizes a new transfer.
type BeginInput struct {
	// TransferID uniquely identifies the transfer. Required, ':'/whitespace-free.
	TransferID string
	// CorrelationID threads both lanes and every bookend event. Required.
	CorrelationID string
	// BytesTotal is the expected size, or 0 when not yet known.
	BytesTotal int64
}

// Begin registers a new Pending tracker, emits the `:requested` bookend, and
// returns the tracker. It rejects duplicates and enforces the active ceiling.
func (m *Manager) Begin(ctx context.Context, in BeginInput) (*Tracker, error) {
	id, err := validateIdentifier("id", in.TransferID)
	if err != nil {
		return nil, err
	}
	correlationID, err := validateIdentifier("correlation", in.CorrelationID)
	if err != nil {
		return nil, err
	}
	if in.BytesTotal < 0 {
		return nil, ErrNegativeBytes
	}

	m.mu.Lock()
	if _, exists := m.trackers[id]; exists {
		m.mu.Unlock()
		return nil, ErrDuplicate
	}
	if len(m.trackers) >= m.maxActive {
		m.mu.Unlock()
		return nil, ErrCapacity
	}
	tracker := newTracker(id, correlationID, in.BytesTotal, m.now)
	m.trackers[id] = tracker
	m.mu.Unlock()

	if err := m.publishBookend(ctx, "requested", tracker.Snapshot()); err != nil {
		// Roll back registration so a failed bookend does not leak a tracker.
		m.mu.Lock()
		delete(m.trackers, id)
		m.mu.Unlock()
		return nil, err
	}
	return tracker, nil
}

// Get returns the live tracker for id, if any.
func (m *Manager) Get(id string) (*Tracker, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tracker, ok := m.trackers[strings.TrimSpace(id)]
	return tracker, ok
}

// Active returns the number of currently tracked transfers.
func (m *Manager) Active() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.trackers)
}

// Complete settles the transfer successfully: it drives the tracker to Ready
// (via Staged/Processing as needed), emits the `:success` bookend, and removes
// the tracker from the active set.
func (m *Manager) Complete(ctx context.Context, id, checksum string) error {
	return m.settle(ctx, id, PhaseReady, "success",
		WithChecksum(strings.TrimSpace(checksum)))
}

// Fail settles the transfer as Failed and emits the `:failed` bookend.
func (m *Manager) Fail(ctx context.Context, id, reason string) error {
	return m.settle(ctx, id, PhaseFailed, "failed", WithReason(strings.TrimSpace(reason)))
}

// Abort settles the transfer as Aborted (caller/deadline cancellation). It maps
// to the `:failed` bookend on the fact lane since the operation did not succeed.
func (m *Manager) Abort(ctx context.Context, id, reason string) error {
	return m.settle(ctx, id, PhaseAborted, "failed", WithReason(strings.TrimSpace(reason)))
}

// settle drives the tracker to a terminal phase, emits the bookend, and reaps
// the tracker. Reaping happens regardless of bookend outcome so a transient bus
// failure cannot pin memory; the bookend error is still surfaced.
func (m *Manager) settle(ctx context.Context, id string, phase Phase, state string, opts ...TransitionOption) error {
	tracker, ok := m.Get(id)
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	snap, err := m.driveTerminal(tracker, phase, opts...)
	if err != nil {
		return err
	}

	m.mu.Lock()
	delete(m.trackers, tracker.ID())
	m.mu.Unlock()

	return m.publishBookend(ctx, state, snap)
}

// driveTerminal walks the forward pipeline as needed to legally reach a terminal
// phase, applying the terminal options on the final hop.
func (m *Manager) driveTerminal(tracker *Tracker, target Phase, opts ...TransitionOption) (Update, error) {
	// Failed/Aborted are reachable from any non-terminal phase directly.
	if target == PhaseFailed || target == PhaseAborted {
		return tracker.Transition(target, opts...)
	}
	// Ready requires walking Staged -> Processing -> Ready as required.
	// Bounded by the fixed pipeline length (CP-02).
	pipeline := []Phase{PhaseUploading, PhaseStaged, PhaseProcessing, PhaseReady}
	var snap Update
	for _, next := range pipeline {
		cur := tracker.Snapshot().Phase
		if !needsHop(cur, next, target) {
			continue
		}
		var hopOpts []TransitionOption
		if next == target {
			hopOpts = opts
		}
		s, err := tracker.Transition(next, hopOpts...)
		if err != nil {
			return s, err
		}
		snap = s
		if next == target {
			break
		}
	}
	if snap.Phase != target {
		return tracker.Snapshot(), ErrInvalidTransition
	}
	return snap, nil
}

// needsHop reports whether, sitting at cur, we still must step to `next` to
// reach `target` along the forward pipeline.
func needsHop(cur, next, target Phase) bool {
	return phaseRank(cur) < phaseRank(next) && phaseRank(next) <= phaseRank(target)
}

// phaseRank orders the forward pipeline for hop computation. Terminal failure
// phases are not ranked here (they bypass the pipeline).
func phaseRank(p Phase) int {
	switch p {
	case PhasePending:
		return 0
	case PhaseUploading:
		return 1
	case PhaseStaged:
		return 2
	case PhaseProcessing:
		return 3
	case PhaseReady:
		return 4
	default:
		return -1
	}
}

// publishBookend builds and publishes a terminal-only fact-lane event carrying
// the transfer id, correlation id, and byte snapshot. A nil bus is a no-op.
func (m *Manager) publishBookend(ctx context.Context, state string, snap Update) error {
	if m.bus == nil {
		return nil
	}
	md := metadata.New()
	md.EnsureCorrelation(snap.CorrelationID)
	meta := md.ToObject()
	meta[metaTransferID] = extension.String(snap.TransferID)

	payload := extension.Object{
		"transfer_id": extension.String(snap.TransferID),
		"phase":       extension.String(string(snap.Phase)),
		"bytes_done":  extension.Int(snap.BytesDone),
		"bytes_total": extension.Int(snap.BytesTotal),
	}
	if snap.Checksum != "" {
		payload["checksum"] = extension.String(snap.Checksum)
	}
	if snap.Reason != "" {
		payload["reason"] = extension.String(snap.Reason)
	}

	env := events.Envelope{
		EventType:     m.eventType(state),
		CorrelationID: snap.CorrelationID,
		Payload:       payload,
		Metadata:      meta,
		Timestamp:     m.now(),
	}
	if err := m.bus.Publish(ctx, env); err != nil {
		return fmt.Errorf("transfer: publish %s bookend: %w", state, err)
	}
	return nil
}
