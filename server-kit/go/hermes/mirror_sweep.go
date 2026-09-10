package hermes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
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
	// Signalled counts targeted sweeps Run made because a source was flagged
	// by Notify, as opposed to the reconcile passes it makes on the timer.
	Signalled int64
	// Coalesced counts signals that landed on an already-flagged source and so
	// cost no sweep of their own. Against Signalled it is the burst ratio: the
	// higher it runs, the more the flag is earning.
	Coalesced int64
	// Unroutable counts signals naming a source nobody registered. Any value
	// but zero is a wiring bug — that scope is converging on the timer alone.
	Unroutable int64
}

// MirrorSweeper polls registered sources and mirrors changed rows through a
// ProjectedRuntimeStore. One sweeper per process; sources are registered once
// at startup.
type MirrorSweeper struct {
	projected *ProjectedRuntimeStore
	opts      MirrorSweepOptions

	// sourcesMu guards the registry. Registration is a startup step, but a
	// change signal can arrive the moment the first bus subscription is live,
	// which is not reliably after the last AddSource — and an unsynchronized
	// append racing a lookup is a data race whether or not it ever misreads.
	sourcesMu sync.RWMutex
	sources   []*mirrorSource
	byName    map[string]*mirrorSource

	// wake carries "at least one source is dirty" to Run. Capacity one on
	// purpose: the news is a boolean, so a second sender has nothing to add and
	// must never block the writer that sent it.
	wake chan struct{}

	swept      atomic.Int64
	errors     atomic.Int64
	signalled  atomic.Int64
	coalesced  atomic.Int64
	unroutable atomic.Int64
}

type mirrorSource struct {
	name           string
	changed        ChangedSince
	deleted        DeletedSince
	deletedRecords DeletedRecordsSince

	// mu guards cursor for the whole of one sweep of this source, so a
	// signal-driven SweepSource and a periodic SweepOnce cannot interleave on
	// the same cursor. Per source rather than per sweeper: two scopes have no
	// reason to wait for each other.
	mu     sync.Mutex
	cursor time.Time

	// dirty means "somebody said this source moved and nothing has swept it
	// since". It is a flag rather than a queue because that is what makes a
	// burst cheap: a thousand signals between two sweeps set the same bit, and
	// the one sweep that follows reads everything past the cursor anyway.
	dirty atomic.Bool
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
	return &MirrorSweeper{
		projected: projected,
		opts:      opts,
		byName:    make(map[string]*mirrorSource, 8),
		wake:      make(chan struct{}, 1),
	}, nil
}

// AddSource registers one changed-rows source. Call before Run.
func (m *MirrorSweeper) AddSource(name string, changed ChangedSince) error {
	name = strings.TrimSpace(name)
	if name == "" || changed == nil {
		return errors.New("hermes mirror source requires a name and a ChangedSince")
	}
	return m.register(&mirrorSource{name: name, changed: changed})
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
	return m.register(&mirrorSource{name: name, deleted: deleted})
}

// AddDeleteRecordSource registers a delete source that carries each deletion's
// audience fields, so the delta can be addressed to the same subscribers the
// record was. Call before Run.
func (m *MirrorSweeper) AddDeleteRecordSource(name string, deleted DeletedRecordsSince) error {
	name = strings.TrimSpace(name)
	if name == "" || deleted == nil {
		return errors.New("hermes mirror delete source requires a name and a DeletedRecordsSince")
	}
	return m.register(&mirrorSource{name: name, deletedRecords: deleted})
}

// register adds one source under its name. Names are the address a change
// signal is delivered to, so two sources cannot share one: the second would be
// unreachable, and a signal naming it would converge the first instead — which
// looks exactly like convergence working.
func (m *MirrorSweeper) register(src *mirrorSource) error {
	m.sourcesMu.Lock()
	defer m.sourcesMu.Unlock()
	if _, exists := m.byName[src.name]; exists {
		return fmt.Errorf("hermes mirror sweeper already has a source named %q", src.name)
	}
	m.byName[src.name] = src
	m.sources = append(m.sources, src)
	return nil
}

// AddScopeSource registers a changed-rows source under the canonical name for
// its (domain, collection) scope, so a change signal about a record written to
// that scope can find it without a second naming convention to keep in
// agreement. Prefer it over AddSource: a source registered under one name and
// woken under another converges nothing while every part looks correct alone.
func (m *MirrorSweeper) AddScopeSource(domain, collection string, changed ChangedSince) error {
	return m.AddSource(ScopeSourceName(domain, collection), changed)
}

// AddScopeDeleteSource registers a tombstone source under the canonical delete
// name for its scope. See AddDeleteSource for what an identity-only delete
// cannot do under a per-record audience policy.
func (m *MirrorSweeper) AddScopeDeleteSource(domain, collection string, deleted DeletedSince) error {
	return m.AddDeleteSource(ScopeDeleteSourceName(domain, collection), deleted)
}

// AddScopeDeleteRecordSource registers an addressed tombstone source under the
// canonical delete name for its scope.
func (m *MirrorSweeper) AddScopeDeleteRecordSource(domain, collection string, deleted DeletedRecordsSince) error {
	return m.AddDeleteRecordSource(ScopeDeleteSourceName(domain, collection), deleted)
}

// hasSource reports whether a source is registered under this name.
func (m *MirrorSweeper) hasSource(name string) bool {
	_, ok := m.lookup(name)
	return ok
}

// lookup resolves one source by name.
func (m *MirrorSweeper) lookup(name string) (*mirrorSource, bool) {
	m.sourcesMu.RLock()
	defer m.sourcesMu.RUnlock()
	src, ok := m.byName[name]
	return src, ok
}

// snapshot returns the registered sources for one pass.
func (m *MirrorSweeper) snapshot() []*mirrorSource {
	m.sourcesMu.RLock()
	defer m.sourcesMu.RUnlock()
	return m.sources[:len(m.sources):len(m.sources)]
}

// Notify marks one source as changed and returns immediately, without touching
// the database. Run converges it on its next turn — right away if it is idle.
//
// This is the intake a change signal should use, not SweepSource. A signal
// arrives on somebody else's goroutine — typically the one goroutine a bus
// dispatches every subscription from — and sweeping inline there puts a
// database round trip and a projection apply in front of every other event the
// process was about to handle. Notify is a flag and a non-blocking send.
//
// It also makes a burst cost one sweep instead of one per message: signals that
// arrive while a sweep is running collapse into the single sweep that follows
// it. Both properties matter more as the fleet grows, because every replica
// receives every announcement.
//
// An unregistered name is a wiring mistake — a source woken under a name nobody
// registered converges nothing while every part looks correct alone — so it
// returns an error rather than dropping the signal quietly.
func (m *MirrorSweeper) Notify(name string) error {
	src, ok := m.lookup(strings.TrimSpace(name))
	if !ok {
		m.unroutable.Add(1)
		return fmt.Errorf("hermes mirror sweeper has no source %q", name)
	}
	if src.dirty.Swap(true) {
		// Already flagged and not yet swept: this signal joined the one ahead
		// of it, which is the whole point.
		m.coalesced.Add(1)
		return nil
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}
	return nil
}

// Stats returns sweep progress counters.
func (m *MirrorSweeper) Stats() MirrorSweepStats {
	return MirrorSweepStats{
		Swept:      m.swept.Load(),
		Errors:     m.errors.Load(),
		Signalled:  m.signalled.Load(),
		Coalesced:  m.coalesced.Load(),
		Unroutable: m.unroutable.Load(),
	}
}

// SweepOnce polls every source once and pushes changed rows through the
// projected store. Returns the number of records swept. Per-source errors are
// counted and skipped (the cursor does not advance, so the next tick retries);
// only context cancellation aborts the pass.
func (m *MirrorSweeper) SweepOnce(ctx context.Context) (int, error) {
	total := 0
	for _, src := range m.snapshot() {
		if err := ctxErr(ctx); err != nil {
			return total, err
		}
		n, err := m.sweepOne(ctx, src)
		total += n
		if err != nil {
			m.errors.Add(1)
		}
	}
	return total, nil
}

// SweepSource polls one registered source by name, synchronously, and returns
// how many records it converged. It is the targeted counterpart of SweepOnce,
// for a caller that already knows which scope changed and should not pay to
// poll every other source to act on it.
//
// It blocks for a database round trip and a projection apply, and every caller
// gets its own pass. A change signal arriving on a shared goroutine wants
// Notify instead, which costs a flag and coalesces bursts; use SweepSource when
// the count matters to the caller — a test, an admin endpoint, a one-shot
// converge before serving a read.
//
// Safe to call concurrently with SweepOnce and with itself: a source sweeps one
// at a time, and a second caller for the same source waits rather than racing
// its cursor.
func (m *MirrorSweeper) SweepSource(ctx context.Context, name string) (int, error) {
	src, ok := m.lookup(name)
	if !ok {
		m.unroutable.Add(1)
		return 0, fmt.Errorf("hermes mirror sweeper has no source %q", name)
	}
	n, err := m.sweepOne(ctx, src)
	if err != nil {
		m.errors.Add(1)
	}
	return n, err
}

// SourceNames lists the registered sources, in registration order.
func (m *MirrorSweeper) SourceNames() []string {
	m.sourcesMu.RLock()
	defer m.sourcesMu.RUnlock()
	names := make([]string, 0, len(m.sources))
	for _, src := range m.sources {
		names = append(names, src.name)
	}
	return names
}

// sweepOne runs one source under its own lock and advances its cursor only on
// success, so a partial pass re-reads rather than skipping.
func (m *MirrorSweeper) sweepOne(ctx context.Context, src *mirrorSource) (int, error) {
	// Clear before reading, never after: a signal that lands mid-sweep is about
	// a write this pass may not see, so it has to survive as a flag for the
	// next one. Clearing afterward would swallow it.
	src.dirty.Store(false)
	src.mu.Lock()
	defer src.mu.Unlock()
	n, next, err := m.sweepSource(ctx, src)
	m.swept.Add(int64(n))
	if err != nil {
		return n, err
	}
	src.cursor = next
	return n, nil
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
		if _, err := m.projected.upsertRecordsConverging(ctx, batch); err != nil {
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
	if err := d.sweeper.projected.deleteRecordsConverging(ctx, d.batch); err != nil {
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
// at batch speed, and an idle pass waits for Interval or for a signal,
// whichever comes first. Source errors never stop the loop (counted; cursors
// hold so nothing is skipped) — only context cancellation returns.
//
// With Notify wired, Interval stops being the latency of a change and becomes
// the reconcile interval: the backstop for writers the application never saw —
// a migration, an admin UPDATE, a replica whose announcement was dropped.
// Signals carry the rest, so the interval can be loosened to whatever staleness
// the slowest such writer justifies rather than tightened toward zero.
func (m *MirrorSweeper) Run(ctx context.Context) error {
	if len(m.snapshot()) == 0 {
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
		if err := m.waitForWork(ctx); err != nil {
			return err
		}
	}
}

// waitForWork blocks until the reconcile interval elapses, and converges
// signalled sources as they arrive without restarting that interval. A stream
// of signals therefore never becomes a stream of full passes: it converges the
// scopes that moved and leaves the reconcile clock alone.
func (m *MirrorSweeper) waitForWork(ctx context.Context) error {
	idle := time.NewTimer(m.opts.Interval)
	defer idle.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-idle.C:
			return nil
		case <-m.wake:
			if err := m.sweepSignalled(ctx); err != nil {
				return err // ctx cancellation only
			}
		}
	}
}

// sweepSignalled converges every source currently flagged by Notify. A source
// whose sweep failed is re-flagged rather than retried here: the cursor did not
// advance, so nothing is lost, and retrying in place would spin against a
// database that is already unhappy. The next signal or the reconcile pass picks
// it up.
func (m *MirrorSweeper) sweepSignalled(ctx context.Context) error {
	for _, src := range m.snapshot() {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		if !src.dirty.Load() {
			continue
		}
		m.signalled.Add(1)
		if _, err := m.sweepOne(ctx, src); err != nil {
			m.errors.Add(1)
			src.dirty.Store(true)
		}
	}
	return nil
}
