package hermes

import (
	"context"
	"errors"
	"sync"
	"time"
)

// SnapshotWriter decides when a projection is due for a fresh durable snapshot
// and writes it off the hot path. It is the piece that makes snapshotDeltaThreshold
// concrete: it bounds how many un-materialized applies a later WarmFromSnapshot
// must replay as a tail, by re-snapshotting once the source watermark has moved
// far enough (or enough time has passed).
//
// A snapshot is a cache of a prefix of the event stream, so the writer is
// best-effort: a transient save failure is not fatal, and calling MaybeSnapshot
// more often than needed is cheap (a projection with no new applies is skipped).
type SnapshotWriter struct {
	store *Store
	snaps SnapshotStore
	opts  SnapshotWriterOptions

	mu       sync.Mutex
	lastWM   map[string]uint64
	lastTime map[string]time.Time
}

// SnapshotWriterOptions governs the re-snapshot triggers. At least one of
// DeltaThreshold or Interval must be set.
type SnapshotWriterOptions struct {
	// DeltaThreshold re-snapshots once the source watermark has advanced by at
	// least this many versions since the last snapshot. It is the tail-length
	// bound: a warm off the resulting artifact replays at most this many events.
	// Zero disables the delta trigger.
	DeltaThreshold uint64
	// Interval re-snapshots when at least this long has elapsed since the last
	// snapshot for a projection. Zero disables the time trigger.
	Interval time.Duration
	// Now is injectable for tests; defaults to time.Now.
	Now func() time.Time
}

// SnapshotTarget names a projection and the query scope to snapshot.
type SnapshotTarget struct {
	Projection string
	Query      Query
}

// NewSnapshotWriter validates options and returns a writer.
func NewSnapshotWriter(store *Store, snaps SnapshotStore, opts SnapshotWriterOptions) (*SnapshotWriter, error) {
	if store == nil {
		return nil, errors.New("hermes snapshot writer requires a store")
	}
	if snaps == nil {
		return nil, errors.New("hermes snapshot writer requires a snapshot store")
	}
	if opts.DeltaThreshold == 0 && opts.Interval <= 0 {
		return nil, errors.New("hermes snapshot writer requires a delta threshold or interval")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &SnapshotWriter{
		store:    store,
		snaps:    snaps,
		opts:     opts,
		lastWM:   map[string]uint64{},
		lastTime: map[string]time.Time{},
	}, nil
}

// MaybeSnapshot snapshots the projection iff it is due, returning the descriptor
// and true when it wrote one. Safe to call frequently: a projection with no new
// applies since its last snapshot, or an empty one, is skipped.
func (w *SnapshotWriter) MaybeSnapshot(ctx context.Context, projection string, query Query) (SnapshotDescriptor, bool, error) {
	stats, err := w.store.Stats(projection)
	if err != nil {
		return SnapshotDescriptor{}, false, err
	}
	now := w.opts.Now()
	w.mu.Lock()
	due := w.dueLocked(projection, stats.SourceWatermark, now)
	w.mu.Unlock()
	if !due {
		return SnapshotDescriptor{}, false, nil
	}
	desc, err := w.store.SaveSnapshot(ctx, projection, query, w.snaps)
	if err != nil {
		return SnapshotDescriptor{}, false, err
	}
	w.mu.Lock()
	// Advance recorded state forward only, so concurrent writers stay monotone.
	if desc.Watermark >= w.lastWM[projection] {
		w.lastWM[projection] = desc.Watermark
		w.lastTime[projection] = now
	}
	w.mu.Unlock()
	return desc, true, nil
}

// dueLocked reports whether a projection is due at its current watermark. The
// caller holds w.mu.
func (w *SnapshotWriter) dueLocked(projection string, watermark uint64, now time.Time) bool {
	if watermark == 0 {
		return false // nothing to persist
	}
	lastWM, seen := w.lastWM[projection]
	if !seen {
		// No baseline yet. A delta writer takes its first snapshot once the
		// scope holds at least DeltaThreshold applies (bounding the first
		// warm's tail); an interval-only writer takes it on first observation.
		if w.opts.DeltaThreshold > 0 {
			return watermark >= w.opts.DeltaThreshold
		}
		return w.opts.Interval > 0
	}
	if watermark <= lastWM {
		return false // no new applies since last snapshot
	}
	if w.opts.DeltaThreshold > 0 && watermark-lastWM >= w.opts.DeltaThreshold {
		return true
	}
	if w.opts.Interval > 0 && now.Sub(w.lastTime[projection]) >= w.opts.Interval {
		return true
	}
	return false
}

// Run ticks every tick and calls MaybeSnapshot for each target until ctx is
// done. It is bounded: one pass per tick, no unbounded fan-out, and a per-target
// error is skipped (the writer is best-effort) rather than killing the loop.
func (w *SnapshotWriter) Run(ctx context.Context, tick time.Duration, targets ...SnapshotTarget) error {
	if tick <= 0 {
		return errors.New("hermes snapshot writer tick must be positive")
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			for _, target := range targets {
				if err := ctxErr(ctx); err != nil {
					return err
				}
				// Best-effort: a transient per-projection failure must not
				// stop the loop; the next tick retries.
				_, _, _ = w.MaybeSnapshot(ctx, target.Projection, target.Query)
			}
		}
	}
}
