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

// DeletedRecordsSince is DeletedSince for a scope whose deletions have to be
// ADDRESSED as well as announced. It streams a DomainRecord rather than four
// strings, so the source can attach the fields the record's audience is derived
// from (see projectiongw.AudiencePolicy): with them the delete reaches exactly
// the subscribers the record itself reached, and without them a per-record
// scope's deletion is dropped rather than broadcast, converging only on the
// reader's next snapshot.
//
// Attach the audience fields and nothing else. A tombstone that carries a copy
// of the row keeps deleted data alive in a second place.
type DeletedRecordsSince func(ctx context.Context, cursor time.Time, visit func(rec database.DomainRecord, deletedAt time.Time) error) error

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
	name           string
	changed        ChangedSince
	deleted        DeletedSince
	deletedRecords DeletedRecordsSince
	cursor         time.Time
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
//
// A scope under a per-record audience policy should use AddDeleteRecordSource
// instead: an identity-only delete names no audience, so it is dropped rather
// than delivered.
func (m *MirrorSweeper) AddDeleteSource(name string, deleted DeletedSince) error {
	name = strings.TrimSpace(name)
	if name == "" || deleted == nil {
		return errors.New("hermes mirror delete source requires a name and a DeletedSince")
	}
	m.sources = append(m.sources, mirrorSource{name: name, deleted: deleted})
	return nil
}

// AddDeleteRecordSource registers a delete source that carries each deletion's
// audience fields, so the delta can be addressed to the same subscribers the
// record was. Call before Run.
func (m *MirrorSweeper) AddDeleteRecordSource(name string, deleted DeletedRecordsSince) error {
	name = strings.TrimSpace(name)
	if name == "" || deleted == nil {
		return errors.New("hermes mirror delete source requires a name and a DeletedRecordsSince")
	}
	m.sources = append(m.sources, mirrorSource{name: name, deletedRecords: deleted})
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
	if src.deletedRecords != nil {
		return m.sweepDeleteRecords(ctx, src)
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

// deleteBatcher accumulates deletions and flushes them through the projected
// store in BatchSize groups, so a delete sweep costs one apply and one fan-out
// frame per scope per batch instead of one of each per row. It is the delete
// counterpart of the changed lane's flush loop, and both delete sources share
// it — the only difference between them is what they can put in rec.Data.
type deleteBatcher struct {
	sweeper *MirrorSweeper
	batch   []database.DomainRecord
	next    time.Time
	swept   int
}

func newDeleteBatcher(m *MirrorSweeper, cursor time.Time) *deleteBatcher {
	return &deleteBatcher{
		sweeper: m,
		batch:   make([]database.DomainRecord, 0, m.opts.BatchSize),
		next:    cursor,
	}
}

// add queues one deletion, flushing when the batch is full.
func (d *deleteBatcher) add(ctx context.Context, rec database.DomainRecord, deletedAt time.Time) error {
	d.batch = append(d.batch, rec)
	d.swept++
	if deletedAt.After(d.next) {
		d.next = deletedAt
	}
	if len(d.batch) >= d.sweeper.opts.BatchSize {
		return d.flush(ctx)
	}
	return nil
}

func (d *deleteBatcher) flush(ctx context.Context) error {
	if len(d.batch) == 0 {
		return nil
	}
	if err := d.sweeper.projected.DeleteRecords(ctx, d.batch); err != nil {
		return err
	}
	d.batch = d.batch[:0]
	return nil
}

// result reports what converged. On error the queued-but-unflushed tail is not
// counted and the cursor does not advance, matching the changed lane: a partial
// pass re-reads rather than skipping.
func (d *deleteBatcher) result(cursor time.Time, err error) (int, time.Time, error) {
	if err != nil {
		return d.swept - len(d.batch), cursor, err
	}
	return d.swept, d.next, nil
}

// sweepDeletes converges hard deletes: each tombstoned identity is removed
// through the projected store (mirror row + hot tombstone + live fan-out) in
// BatchSize groups. Deletion is idempotent, so replays after a partial pass are
// safe; the cursor advances only past identities that converged.
//
// An identity-only source cannot name an audience, so under a per-record
// audience policy these deletions are withheld by the gateway — see
// AddDeleteRecordSource.
func (m *MirrorSweeper) sweepDeletes(ctx context.Context, src *mirrorSource) (int, time.Time, error) {
	batcher := newDeleteBatcher(m, src.cursor)
	err := src.deleted(ctx, src.cursor, func(domain, collection, organizationID, recordID string, deletedAt time.Time) error {
		return batcher.add(ctx, database.DomainRecord{
			Domain:         domain,
			Collection:     collection,
			OrganizationID: organizationID,
			RecordID:       recordID,
		}, deletedAt)
	})
	if err == nil {
		err = batcher.flush(ctx)
	}
	return batcher.result(src.cursor, err)
}

// sweepDeleteRecords is sweepDeletes for a source that carries each deletion's
// audience fields, so the projected delete is addressed rather than anonymous.
func (m *MirrorSweeper) sweepDeleteRecords(ctx context.Context, src *mirrorSource) (int, time.Time, error) {
	batcher := newDeleteBatcher(m, src.cursor)
	err := src.deletedRecords(ctx, src.cursor, func(rec database.DomainRecord, deletedAt time.Time) error {
		return batcher.add(ctx, rec, deletedAt)
	})
	if err == nil {
		err = batcher.flush(ctx)
	}
	return batcher.result(src.cursor, err)
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
