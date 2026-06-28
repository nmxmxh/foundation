package transfer

import (
	"sync"
	"time"
)

// Subscriber receives coalesced progress updates for a single transfer.
// Subscribers must not block; the tracker invokes them synchronously while
// holding no lock, but a slow subscriber still stalls the producing goroutine.
type Subscriber func(Update)

// Tracker owns the live, in-memory state of one transfer and fans coalesced
// updates out to subscribers. It is safe for concurrent use.
//
// Byte progress is strictly monotonic: Advance accepts an absolute offset and
// ignores any value at or below the current high-water mark. This makes
// resumable/retried writes (which re-send a known offset) naturally idempotent
// on the progress lane.
type Tracker struct {
	id            string
	correlationID string

	mu         sync.RWMutex
	phase      Phase
	bytesDone  int64
	bytesTotal int64
	checksum   string
	reason     string
	seq        uint64
	updatedAt  time.Time

	subs   map[int]Subscriber
	nextID int

	now func() time.Time
}

// NewTracker constructs a standalone Pending tracker for callers who want a
// progress lane without manager-emitted bookend events (for example a download
// or export tracked purely client-side). A negative total is treated as
// unknown (0). Identifiers are validated to stay compatible with the metadata
// token grammar.
func NewTracker(id, correlationID string, total int64) (*Tracker, error) {
	validID, err := validateIdentifier("id", id)
	if err != nil {
		return nil, err
	}
	validCorrelation, err := validateIdentifier("correlation", correlationID)
	if err != nil {
		return nil, err
	}
	return newTracker(validID, validCorrelation, total, func() time.Time { return time.Now().UTC() }), nil
}

// newTracker constructs a Pending tracker. Identifiers are assumed validated by
// the caller (the Manager). now must be non-nil.
func newTracker(id, correlationID string, total int64, now func() time.Time) *Tracker {
	if total < 0 {
		total = 0
	}
	return &Tracker{
		id:            id,
		correlationID: correlationID,
		phase:         PhasePending,
		bytesTotal:    total,
		subs:          map[int]Subscriber{},
		now:           now,
		updatedAt:     now(),
	}
}

// ID returns the transfer id.
func (t *Tracker) ID() string { return t.id }

// CorrelationID returns the correlation id threading both lanes.
func (t *Tracker) CorrelationID() string { return t.correlationID }

// Snapshot returns the current state as an immutable Update.
func (t *Tracker) Snapshot() Update {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.snapshotLocked()
}

func (t *Tracker) snapshotLocked() Update {
	return Update{
		TransferID:    t.id,
		CorrelationID: t.correlationID,
		Phase:         t.phase,
		BytesDone:     t.bytesDone,
		BytesTotal:    t.bytesTotal,
		Checksum:      t.checksum,
		Reason:        t.reason,
		Seq:           t.seq,
		Timestamp:     t.updatedAt,
	}
}

// IsTerminal reports whether the transfer has settled.
func (t *Tracker) IsTerminal() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return IsTerminal(t.phase)
}

// SetTotal declares (or revises upward) the expected byte total. A non-positive
// value, or one below the bytes already observed, is ignored. Returns the
// resulting snapshot and whether anything changed.
func (t *Tracker) SetTotal(total int64) (Update, bool) {
	t.mu.Lock()
	if IsTerminal(t.phase) || total <= 0 || total == t.bytesTotal || total < t.bytesDone {
		snap := t.snapshotLocked()
		t.mu.Unlock()
		return snap, false
	}
	t.bytesTotal = total
	snap := t.advanceSeqLocked()
	subs := t.copySubsLocked()
	t.mu.Unlock()
	emit(subs, snap)
	return snap, true
}

// Advance moves the byte high-water mark to the absolute offset bytesDone,
// transitioning Pending -> Uploading on first progress. Offsets at or below the
// current mark are ignored (monotonic, resumable-safe). A negative offset is a
// programming error and is rejected. Advancing a terminal transfer is rejected.
func (t *Tracker) Advance(bytesDone int64) (Update, error) {
	if bytesDone < 0 {
		return Update{}, ErrNegativeBytes
	}
	t.mu.Lock()
	if IsTerminal(t.phase) {
		snap := t.snapshotLocked()
		t.mu.Unlock()
		return snap, ErrTerminal
	}
	if bytesDone <= t.bytesDone && t.phase != PhasePending {
		// No forward progress and already moving: coalesce to a no-op.
		snap := t.snapshotLocked()
		t.mu.Unlock()
		return snap, nil
	}
	if bytesDone > t.bytesDone {
		t.bytesDone = bytesDone
	}
	if t.bytesTotal > 0 && t.bytesDone > t.bytesTotal {
		t.bytesTotal = t.bytesDone
	}
	if t.phase == PhasePending {
		t.phase = PhaseUploading
	}
	snap := t.advanceSeqLocked()
	subs := t.copySubsLocked()
	t.mu.Unlock()
	emit(subs, snap)
	return snap, nil
}

// TransitionOption customizes a phase transition.
type TransitionOption func(*transitionConfig)

type transitionConfig struct {
	reason   string
	checksum string
}

// WithReason records a human-readable reason, used for Failed/Aborted.
func WithReason(reason string) TransitionOption {
	return func(c *transitionConfig) { c.reason = reason }
}

// WithChecksum records the settled object's checksum, used for Ready/Staged.
func WithChecksum(checksum string) TransitionOption {
	return func(c *transitionConfig) { c.checksum = checksum }
}

// Transition moves the transfer to phase `to` if the state machine permits it.
// On a successful move to a phase that implies completion (Ready), the byte
// count is snapped up to the known total so Fraction reports 1.
func (t *Tracker) Transition(to Phase, opts ...TransitionOption) (Update, error) {
	var cfg transitionConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	t.mu.Lock()
	if IsTerminal(t.phase) {
		snap := t.snapshotLocked()
		t.mu.Unlock()
		return snap, ErrTerminal
	}
	if !CanTransition(t.phase, to) {
		snap := t.snapshotLocked()
		t.mu.Unlock()
		return snap, ErrInvalidTransition
	}
	t.phase = to
	if cfg.reason != "" {
		t.reason = cfg.reason
	}
	if cfg.checksum != "" {
		t.checksum = cfg.checksum
	}
	if to == PhaseReady && t.bytesTotal > 0 {
		t.bytesDone = t.bytesTotal
	}
	snap := t.advanceSeqLocked()
	subs := t.copySubsLocked()
	t.mu.Unlock()
	emit(subs, snap)
	return snap, nil
}

// Subscribe registers fn for future updates and immediately delivers the
// current snapshot so a late subscriber is never blind. It returns an
// unsubscribe function that is safe to call multiple times.
func (t *Tracker) Subscribe(fn Subscriber) (cancel func()) {
	if fn == nil {
		return func() {}
	}
	t.mu.Lock()
	id := t.nextID
	t.nextID++
	t.subs[id] = fn
	snap := t.snapshotLocked()
	t.mu.Unlock()

	fn(snap)

	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			delete(t.subs, id)
			t.mu.Unlock()
		})
	}
}

// advanceSeqLocked bumps the monotonic sequence and timestamp, returning the
// fresh snapshot. Caller must hold the write lock.
func (t *Tracker) advanceSeqLocked() Update {
	t.seq++
	t.updatedAt = t.now()
	return t.snapshotLocked()
}

// copySubsLocked snapshots subscribers so emission happens without the lock.
func (t *Tracker) copySubsLocked() []Subscriber {
	if len(t.subs) == 0 {
		return nil
	}
	out := make([]Subscriber, 0, len(t.subs))
	for _, fn := range t.subs {
		out = append(out, fn)
	}
	return out
}

func emit(subs []Subscriber, u Update) {
	for _, fn := range subs {
		fn(u)
	}
}
