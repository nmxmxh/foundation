package hermes

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

// MirrorSweep is the one-place alternative to per-repository projection
// hooks: instead of every mutating command enqueueing a projection job, one
// bounded poller per process pulls each source table's changed rows
// (updated_at > cursor) and pushes them through the projected store — durable
// mirror, hot apply, and live fan-out in one batch write. Because it watches
// the tables rather than the call sites, it catches every writer: repositories,
// seeds, admin SQL, and services added later. The first pass from a zero
// cursor is a full sync (idempotent — unchanged rows are DISTINCT-FROM
// no-ops), so it doubles as startup reconciliation.
//
// Hard deletes are invisible to an updated_at sweep, so schemas announce them
// through a projection tombstone table (AFTER DELETE triggers) swept by a
// DeletedSince source — see AddDeleteSource. Upserts and deletes share the
// same loop, cursors, and error discipline.

// ChangedSince streams rows of one source that changed after cursor, in
// ascending updated_at order, calling visit with each materialized record and
// its updated_at. Implementations must bound their reads (LIMIT); the sweeper
// re-polls immediately while a source keeps yielding rows.
type ChangedSince func(ctx context.Context, cursor time.Time, visit func(rec database.DomainRecord, updatedAt time.Time) error) error

// DeletedSince streams record identities deleted after cursor, in ascending
// deleted_at order — the delete-side counterpart of ChangedSince. Hard deletes
// are invisible to an updated_at sweep, so schemas announce them through a
// tombstone table maintained by AFTER DELETE triggers; this source sweeps that
// table and the sweeper converges the projection via DeleteRecord (mirror
// removal + hot tombstone + live fan-out). Implementations must bound their
// reads (LIMIT).
type DeletedSince func(ctx context.Context, cursor time.Time, visit func(domain, collection, organizationID, recordID string, deletedAt time.Time) error) error

// MirrorSweepOptions configures the sweeper loop.
type MirrorSweepOptions struct {
	// Interval between idle polls (default 2s). A source that yielded rows is
	// re-polled immediately until it drains.
	Interval time.Duration
	// BatchSize bounds each UpsertRecords push (default 256).
	BatchSize int
}

// MirrorSweepStats exposes sweeper progress for health/debug surfaces.
type MirrorSweepStats struct {
	Swept  int64 // records pushed through the projected store
	Errors int64 // failed polls (retried on the next tick)
}

// MirrorSweeper polls registered sources and mirrors changed rows through a
// ProjectedRuntimeStore. One sweeper per process; sources are registered once
// at startup.
type MirrorSweeper struct {
	projected *ProjectedRuntimeStore
	sources   []mirrorSource
	opts      MirrorSweepOptions

	swept  atomic.Int64
	errors atomic.Int64
}

type mirrorSource struct {
	name    string
	changed ChangedSince
	deleted DeletedSince
	cursor  time.Time
}

// NewMirrorSweeper constructs a sweeper over the projected store.
func NewMirrorSweeper(projected *ProjectedRuntimeStore, opts MirrorSweepOptions) (*MirrorSweeper, error) {
	if projected == nil {
		return nil, errors.New("hermes mirror sweeper requires a projected store")
	}
	if opts.Interval <= 0 {
		opts.Interval = 2 * time.Second
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 256
	}
	return &MirrorSweeper{projected: projected, opts: opts}, nil
}

// AddSource registers one changed-rows source. Call before Run.
func (m *MirrorSweeper) AddSource(name string, changed ChangedSince) error {
	name = strings.TrimSpace(name)
	if name == "" || changed == nil {
		return errors.New("hermes mirror source requires a name and a ChangedSince")
	}
	m.sources = append(m.sources, mirrorSource{name: name, changed: changed})
	return nil
}

// AddDeleteSource registers one deleted-identities source (typically the
// projection tombstone table). Call before Run.
func (m *MirrorSweeper) AddDeleteSource(name string, deleted DeletedSince) error {
	name = strings.TrimSpace(name)
	if name == "" || deleted == nil {
		return errors.New("hermes mirror delete source requires a name and a DeletedSince")
	}
	m.sources = append(m.sources, mirrorSource{name: name, deleted: deleted})
	return nil
}

// Stats returns sweep progress counters.
func (m *MirrorSweeper) Stats() MirrorSweepStats {
	return MirrorSweepStats{Swept: m.swept.Load(), Errors: m.errors.Load()}
}

// SweepOnce polls every source once and pushes changed rows through the
// projected store. Returns the number of records swept. Per-source errors are
// counted and skipped (the cursor does not advance, so the next tick retries);
// only context cancellation aborts the pass.
func (m *MirrorSweeper) SweepOnce(ctx context.Context) (int, error) {
	total := 0
	for i := range m.sources {
		src := &m.sources[i]
		if err := ctxErr(ctx); err != nil {
			return total, err
		}
		n, next, err := m.sweepSource(ctx, src)
		total += n
		if err != nil {
			m.errors.Add(1)
			continue
		}
		src.cursor = next
	}
	m.swept.Add(int64(total))
	return total, nil
}

func (m *MirrorSweeper) sweepSource(ctx context.Context, src *mirrorSource) (int, time.Time, error) {
	if src.deleted != nil {
		return m.sweepDeletes(ctx, src)
	}
	batch := make([]database.DomainRecord, 0, m.opts.BatchSize)
	next := src.cursor
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if _, err := m.projected.UpsertRecords(ctx, batch); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}
	swept := 0
	err := src.changed(ctx, src.cursor, func(rec database.DomainRecord, updatedAt time.Time) error {
		batch = append(batch, rec)
		swept++
		if updatedAt.After(next) {
			next = updatedAt
		}
		if len(batch) >= m.opts.BatchSize {
			return flush()
		}
		return nil
	})
	if err != nil {
		return swept - len(batch), src.cursor, err
	}
	if err := flush(); err != nil {
		return swept - len(batch), src.cursor, err
	}
	return swept, next, nil
}

// sweepDeletes converges hard deletes: each tombstoned identity is removed
// through the projected store (mirror row + hot tombstone + live fan-out).
// DeleteRecord is idempotent, so replays after a partial pass are safe; the
// cursor advances only past identities that converged.
func (m *MirrorSweeper) sweepDeletes(ctx context.Context, src *mirrorSource) (int, time.Time, error) {
	next := src.cursor
	swept := 0
	err := src.deleted(ctx, src.cursor, func(domain, collection, organizationID, recordID string, deletedAt time.Time) error {
		if err := m.projected.DeleteRecord(ctx, domain, collection, organizationID, recordID); err != nil {
			return err
		}
		swept++
		if deletedAt.After(next) {
			next = deletedAt
		}
		return nil
	})
	if err != nil {
		return swept, src.cursor, err
	}
	return swept, next, nil
}

// Run sweeps until ctx ends: the first pass runs immediately (full sync from
// zero cursors), a productive pass re-polls without waiting so bursts drain
// at batch speed, and only an idle pass waits Interval. Source errors never
// stop the loop (counted; cursors hold so nothing is skipped) — only context
// cancellation returns.
func (m *MirrorSweeper) Run(ctx context.Context) error {
	if len(m.sources) == 0 {
		return errors.New("hermes mirror sweeper has no sources")
	}
	for {
		n, err := m.SweepOnce(ctx)
		if err != nil {
			return err // ctx cancellation only
		}
		if n > 0 {
			continue
		}
		idle := time.NewTimer(m.opts.Interval)
		select {
		case <-ctx.Done():
			idle.Stop()
			return ctx.Err()
		case <-idle.C:
		}
	}
}
