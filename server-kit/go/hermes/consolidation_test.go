package hermes

import (
	"context"
	"errors"
	"fmt"
	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	redispkg "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/worker"
	"testing"
	"time"
)

// TestApplyBatchSkipsEventLevelRejections pins the batch-tolerance contract:
// an event-level rejection (invalid scope, capacity) poisons only the
// offending event. The rest of the batch still applies, accepted mutations
// still fan out to observers, and the rejection surfaces as a joined error so
// single-event Apply callers keep their pinned ErrInvalidEvent /
// ErrProjectionLimit contract. Before this, one bad event aborted the batch
// and suppressed fan-out of everything applied before it.
func TestApplyBatchSkipsEventLevelRejections(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:       "tolerant_ticks",
		Domain:     "signals",
		Collection: "ticks",
		MaxRecords: 2,
		MaxBytes:   1 << 20,
	})
	ctx := t.Context()

	var accepted []AppliedMutation
	cancel := store.Observe(func(_ string, mutations []AppliedMutation) {
		accepted = append(accepted, mutations...)
	})
	defer cancel()

	result, err := store.ApplyBatch(ctx, "tolerant_ticks", []Event{
		// invalid scope: rejected, must not halt the batch
		{Operation: OperationUpsert, SourceID: "bad_scope", Version: 1,
			Record: database.DomainRecord{Domain: "other", Collection: "ticks", OrganizationID: "org_1", RecordID: "tick_bad"}},
		// two valid events fill the partition
		{Operation: OperationUpsert, SourceID: "ok_1", Version: 2, Record: testRecord("signals", "ticks", "org_1", "tick_1", nil)},
		{Operation: OperationUpsert, SourceID: "ok_2", Version: 3, Record: testRecord("signals", "ticks", "org_1", "tick_2", nil)},
		// over capacity: rejected, must not halt the batch either
		{Operation: OperationUpsert, SourceID: "over", Version: 4, Record: testRecord("signals", "ticks", "org_1", "tick_3", nil)},
	})
	if !errors.Is(err, ErrInvalidEvent) || !errors.Is(err, ErrProjectionLimit) {
		t.Fatalf("err = %v, want joined ErrInvalidEvent + ErrProjectionLimit", err)
	}
	if result.Applied != 2 || result.Ignored != 2 {
		t.Fatalf("result = %+v, want Applied=2 Ignored=2", result)
	}
	if len(accepted) != 2 {
		t.Fatalf("observer saw %d mutations, want 2 (accepted events must fan out despite batch error)", len(accepted))
	}
	count, err := store.Count(ctx, "tolerant_ticks", Query{OrganizationID: "org_1"}, Fence{})
	if err != nil || count != 2 {
		t.Fatalf("Count() = %d err=%v, want 2", count, err)
	}
}

// TestTailerQuarantinesPoisonMessage proves a message whose decode fails is
// quarantined (acked, dropped, counted) instead of halting the tail loop:
// healthy messages in the same batch still apply, and the poison message is
// not redelivered on the next poll. Before this, one poison message failed
// PollOnce before any apply, nothing was acked, and Run redelivered it
// forever.
func TestTailerQuarantinesPoisonMessage(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:       "quarantine_ticks",
		Domain:     "signals",
		Collection: "ticks",
		MaxRecords: 10,
		MaxBytes:   1 << 20,
	})
	client := redispkg.NewMemoryClient("test")
	ctx := t.Context()
	for i := range 2 {
		if _, err := client.XAdd(ctx, "hermes:poison", redispkg.Values{
			redispkg.Field("organization_id", "org_1"),
			redispkg.Field("record_id", fmt.Sprintf("tick_%d", i)),
			redispkg.Field("version", i+1),
		}); err != nil {
			t.Fatalf("XAdd() error = %v", err)
		}
	}
	source, err := NewRedisStreamSource(client, "hermes:poison", "hermes", "node_1")
	if err != nil {
		t.Fatalf("NewRedisStreamSource() error = %v", err)
	}
	decode := func(_ context.Context, message SourceMessage) ([]Event, error) {
		rawID, _ := message.Values.Get("record_id")
		recordID, _ := rawID.(string)
		if recordID == "tick_0" {
			return nil, errors.New("poison payload")
		}
		rawVersion, _ := message.Values.Get("version")
		return []Event{{
			Operation: OperationUpsert,
			Version:   uint64(intFromAny(rawVersion)),
			Record:    testRecord("signals", "ticks", "org_1", recordID, nil),
		}}, nil
	}
	tailer, err := NewTailer(store, "quarantine_ticks", source, decode, TailerOptions{MaxBatch: 8})
	if err != nil {
		t.Fatalf("NewTailer() error = %v", err)
	}

	result, err := tailer.PollOnce(ctx)
	if err != nil {
		t.Fatalf("PollOnce() error = %v (poison must not fail the poll)", err)
	}
	if result.Read != 2 || result.Quarantined != 1 || result.Decoded != 1 || result.Acked != 2 || result.Apply.Applied != 1 {
		t.Fatalf("PollOnce() result = %+v, want Read=2 Quarantined=1 Decoded=1 Acked=2 Applied=1", result)
	}
	// The poison message was acked: nothing redelivers.
	result, err = tailer.PollOnce(ctx)
	if err != nil || result.Read != 0 {
		t.Fatalf("second PollOnce() result=%+v err=%v, want no redelivery", result, err)
	}
}

// TestUpsertRecordsBatchGroupsScopesAndStaysCoherent pins the batch write
// path: UpsertRecords lands every record in the base store and the hot
// partitions (grouped per scope), fans accepted mutations out to observers,
// and allocates versions from the same counter as single-record upserts so a
// later UpsertRecord still wins LWW over the batch. MemoryDB has no batch
// capability, so this also covers the per-record fallback lane.
func TestUpsertRecordsBatchGroupsScopesAndStaysCoherent(t *testing.T) {
	base := database.NewMemoryDB()
	store, err := WrapRuntimeStore(base, RuntimeStoreOptions{MaxRecordsPerScope: 16, MaxBytesPerScope: 1 << 20})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	ctx := t.Context()

	var accepted []AppliedMutation
	cancel := store.Store().Observe(func(_ string, mutations []AppliedMutation) {
		accepted = append(accepted, mutations...)
	})
	defer cancel()

	saved, err := store.UpsertRecords(ctx, []database.DomainRecord{
		{Domain: "menu", Collection: "dishes", OrganizationID: "org_1", RecordID: "dish_1",
			Data: testRecordData(map[string]any{"state": "published"})},
		{Domain: "menu", Collection: "dishes", OrganizationID: "org_1", RecordID: "dish_2",
			Data: testRecordData(map[string]any{"state": "published"})},
		{Domain: "billing", Collection: "subscriptions", OrganizationID: "org_1", RecordID: "sub_1",
			Data: testRecordData(map[string]any{"status": "active"})},
	})
	if err != nil || len(saved) != 3 {
		t.Fatalf("UpsertRecords() len=%d err=%v", len(saved), err)
	}
	if len(accepted) != 3 {
		t.Fatalf("observer saw %d mutations, want 3 (batch applies must fan out)", len(accepted))
	}
	for _, check := range []struct {
		domain, collection string
		want               int64
	}{{"menu", "dishes", 2}, {"billing", "subscriptions", 1}} {
		name := store.ProjectionName(check.domain, check.collection, "org_1")
		count, err := store.Store().Count(ctx, name, Query{OrganizationID: "org_1"}, Fence{})
		if err != nil || count != check.want {
			t.Fatalf("hot count %s/%s = %d err=%v, want %d", check.domain, check.collection, count, err, check.want)
		}
	}
	if _, found, err := base.GetRecord(ctx, "billing", "subscriptions", "org_1", "sub_1"); err != nil || !found {
		t.Fatalf("base record missing after batch: found=%v err=%v", found, err)
	}

	// A later single-record upsert must win LWW over the batch (shared counter).
	if _, err := store.UpsertRecord(ctx, database.DomainRecord{
		Domain: "menu", Collection: "dishes", OrganizationID: "org_1", RecordID: "dish_1",
		Data: testRecordData(map[string]any{"state": "sold_out"}),
	}); err != nil {
		t.Fatalf("follow-up UpsertRecord() error = %v", err)
	}
	name := store.ProjectionName("menu", "dishes", "org_1")
	rec, found, err := store.Store().GetRecord(ctx, name, Query{OrganizationID: "org_1"}, "dish_1", Fence{})
	if err != nil || !found || !recordDataStringEquals(rec.Data, "state", "sold_out") {
		t.Fatalf("post-batch LWW record = %+v found=%v err=%v, want state=sold_out", rec, found, err)
	}
}

// TestApplyRecordsSkipsEventLevelRejections extends the batch-tolerance
// contract to the trusted-records lane (the batch-upsert hot path): a capacity
// rejection mid-batch poisons only the overflowing record — the records before
// it stay applied and the error surfaces joined at the end.
func TestApplyRecordsSkipsEventLevelRejections(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:       "tolerant_records",
		Domain:     "signals",
		Collection: "ticks",
		MaxRecords: 1,
		MaxBytes:   1 << 20,
	})
	ctx := t.Context()
	result, err := store.ApplyRecords(ctx, "tolerant_records", "state", 1, []database.DomainRecord{
		testRecord("signals", "ticks", "org_1", "tick_1", nil),
		testRecord("signals", "ticks", "org_1", "tick_2", nil), // over capacity
	})
	if !errors.Is(err, ErrProjectionLimit) {
		t.Fatalf("err = %v, want joined ErrProjectionLimit", err)
	}
	if result.Applied != 1 || result.Ignored != 1 {
		t.Fatalf("result = %+v, want Applied=1 Ignored=1 (overflow must not poison the batch)", result)
	}
	count, err := store.Count(ctx, "tolerant_records", Query{OrganizationID: "org_1"}, Fence{})
	if err != nil || count != 1 {
		t.Fatalf("Count() = %d err=%v, want 1", count, err)
	}
}

// TestSnapshotShadowEvidenceCycle drives the shadow-mode snapshot rollout
// across three simulated process generations sharing one durable base and one
// snapshot store:
//
//	gen 1: cold warm, no artifact yet → rebuild serves, artifact saved.
//	gen 2: cold warm, artifact matches the rebuild → clean-match evidence.
//	gen 3: base mutated out-of-band since the artifact → mismatch evidence,
//	       artifact refreshed to the new truth.
//
// Throughout, the served warm path is the source rebuild — the shadow lane
// only accumulates the evidence counters that gate ever preferring snapshots.
func TestSnapshotShadowEvidenceCycle(t *testing.T) {
	base := database.NewMemoryDB()
	snaps := NewMemorySnapshotStore()
	ctx := t.Context()
	opts := RuntimeStoreOptions{MaxRecordsPerScope: 8, MaxBytesPerScope: 1 << 20, SnapshotStore: snaps}

	seed := func(id string) {
		t.Helper()
		if _, err := base.UpsertRecord(ctx, database.DomainRecord{
			Domain: "menu", Collection: "dishes", OrganizationID: "org_1", RecordID: id,
			Data: testRecordData(map[string]any{"state": "published"}),
		}); err != nil {
			t.Fatalf("seed %s error = %v", id, err)
		}
	}
	warmGen := func(label string) RuntimeStats {
		t.Helper()
		gen, err := WrapRuntimeStore(base, opts)
		if err != nil {
			t.Fatalf("%s WrapRuntimeStore() error = %v", label, err)
		}
		if err := gen.WarmScope(ctx, "menu", "dishes", "org_1"); err != nil {
			t.Fatalf("%s WarmScope() error = %v", label, err)
		}
		return gen.HermesRuntimeStats()
	}

	seed("dish_1")
	seed("dish_2")

	// Gen 1: no artifact yet — neither match nor mismatch, one save.
	stats := warmGen("gen1")
	if stats.SnapshotShadowMatches != 0 || stats.SnapshotShadowMismatches != 0 || stats.SnapshotShadowErrors != 0 {
		t.Fatalf("gen1 stats = %+v, want no shadow outcomes before an artifact exists", stats)
	}
	if stats.SnapshotSaves != 1 {
		t.Fatalf("gen1 saves = %d, want 1 (rebuild must produce the first artifact)", stats.SnapshotSaves)
	}

	// Gen 2: artifact reproduces the rebuild exactly — clean match.
	stats = warmGen("gen2")
	if stats.SnapshotShadowMatches != 1 || stats.SnapshotShadowMismatches != 0 {
		t.Fatalf("gen2 stats = %+v, want one clean shadow match", stats)
	}

	// Base mutates after the artifact: gen 3 must record the divergence.
	seed("dish_3")
	stats = warmGen("gen3")
	if stats.SnapshotShadowMismatches != 1 || stats.SnapshotShadowErrors != 0 {
		t.Fatalf("gen3 stats = %+v, want one shadow mismatch (artifact stale-behind)", stats)
	}

	// Gen 4: gen 3 refreshed the artifact to current truth — clean again.
	stats = warmGen("gen4")
	if stats.SnapshotShadowMatches != 1 || stats.SnapshotShadowMismatches != 0 {
		t.Fatalf("gen4 stats = %+v, want the refreshed artifact to match", stats)
	}
}

// TestEnvelopeTailerQuarantinesPoisonEnvelope proves the envelope fallback
// path survives poison: a message with a missing envelope field and one with
// undecodable envelope bytes are quarantined (acked, dropped, counted) while
// the healthy envelope in the same batch still applies, and nothing
// redelivers. Before this, RedisStreamEnvelopeSource failed the whole read on
// the first bad message, so poison redelivered forever and Run halted.
func TestEnvelopeTailerQuarantinesPoisonEnvelope(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:       "quarantine_stream",
		Domain:     "signals",
		Collection: "ticks",
		MaxRecords: 10,
		MaxBytes:   1 << 20,
	})
	ctx := t.Context()
	client := redispkg.NewMemoryClient("test")
	// poison 1: envelope field missing entirely
	if _, err := client.XAdd(ctx, "hermes:qstream", redispkg.Values{redispkg.Field("other", "x")}); err != nil {
		t.Fatalf("XAdd() error = %v", err)
	}
	// poison 2: envelope field holds garbage bytes
	if _, err := client.XAdd(ctx, "hermes:qstream", redispkg.Values{redispkg.Field("envelope", []byte("not an envelope"))}); err != nil {
		t.Fatalf("XAdd() error = %v", err)
	}
	// healthy canonical projection envelope
	envelope, err := NewProjectionEnvelope([]*foundationpb.RecordMutation{
		MutationFromRecord(testRecord("signals", "ticks", "org_1", "tick_ok", nil), OperationUpsert, 3),
	}, "corr_q")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() error = %v", err)
	}
	raw, err := envelope.ToBinary()
	if err != nil {
		t.Fatalf("ToBinary() error = %v", err)
	}
	if _, err := client.XAdd(ctx, "hermes:qstream", redispkg.Values{redispkg.Field("envelope", raw)}); err != nil {
		t.Fatalf("XAdd() error = %v", err)
	}

	source, err := NewRedisStreamEnvelopeSource(client, "hermes:qstream", "hermes", "node_1", "")
	if err != nil {
		t.Fatalf("NewRedisStreamEnvelopeSource() error = %v", err)
	}
	tailer, err := NewEnvelopeTailer(store, "quarantine_stream", source, TailerOptions{MaxBatch: 8})
	if err != nil {
		t.Fatalf("NewEnvelopeTailer() error = %v", err)
	}

	result, err := tailer.PollOnce(ctx)
	if err != nil {
		t.Fatalf("PollOnce() error = %v (poison must not fail the poll)", err)
	}
	if result.Read != 3 || result.Quarantined != 2 || result.Decoded != 1 || result.Acked != 3 || result.Apply.Applied != 1 {
		t.Fatalf("PollOnce() result = %+v, want Read=3 Quarantined=2 Decoded=1 Acked=3 Applied=1", result)
	}
	// Poison messages were acked: nothing redelivers.
	result, err = tailer.PollOnce(ctx)
	if err != nil || result.Read != 0 {
		t.Fatalf("second PollOnce() result=%+v err=%v, want no redelivery", result, err)
	}
}

// TestRecordWorkerProcessorUpsertsThroughProjectedStore pins the canonical
// projection path: the processor writes through ProjectedRuntimeStore, so one
// job buys the durable record-store row (cold-start rebuildable) AND the hot
// partition apply — unlike WorkerProcessor, which applies to the in-memory
// store only and leaves nothing to rebuild from after a restart.
func TestRecordWorkerProcessorUpsertsThroughProjectedStore(t *testing.T) {
	base := database.NewMemoryDB()
	projected, err := WrapRuntimeStore(base, RuntimeStoreOptions{MaxRecordsPerScope: 8, MaxBytesPerScope: 1 << 20})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	ctx := t.Context()

	processor, err := NewRecordWorkerProcessor(projected, "menu_projection", "hotplane", func(_ context.Context, job worker.Job) ([]database.DomainRecord, error) {
		return []database.DomainRecord{{
			Domain:         "menu",
			Collection:     "dishes",
			OrganizationID: "org_1",
			RecordID:       job.IdempotencyKey,
			Data:           testRecordData(map[string]any{"state": "published"}),
		}}, nil
	})
	if err != nil {
		t.Fatalf("NewRecordWorkerProcessor() error = %v", err)
	}
	if processor.Kind() != "menu_projection" || processor.Queue() != "hotplane" || processor.MaxAttempts() != 3 {
		t.Fatalf("processor identity = %s/%s/%d", processor.Kind(), processor.Queue(), processor.MaxAttempts())
	}

	if err := processor.Handle(ctx, worker.Job{IdempotencyKey: "dish_1"}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	// Durable side: the base store holds the record (rebuildable after restart).
	if _, found, err := base.GetRecord(ctx, "menu", "dishes", "org_1", "dish_1"); err != nil || !found {
		t.Fatalf("base GetRecord() found=%v err=%v, want durable record", found, err)
	}
	// Hot side: the partition the gateway reads serves it without the base.
	projection := projected.ProjectionName("menu", "dishes", "org_1")
	count, err := projected.Store().Count(ctx, projection, Query{OrganizationID: "org_1"}, Fence{})
	if err != nil || count != 1 {
		t.Fatalf("hot Count() = %d err=%v, want 1", count, err)
	}
	// Idempotent replay (River retry) stays a single record.
	if err := processor.Handle(ctx, worker.Job{IdempotencyKey: "dish_1"}); err != nil {
		t.Fatalf("replay Handle() error = %v", err)
	}
	if count, _ = projected.Store().Count(ctx, projection, Query{OrganizationID: "org_1"}, Fence{}); count != 1 {
		t.Fatalf("count after replay = %d, want 1", count)
	}
}

// TestScopeBackfillMakesWarmSelfSufficient proves the self-backfilling warm:
// with an empty record mirror and a configured ScopeBackfill enumerator, a
// WarmScope pulls the scope's rows from the authoritative source through the
// batch lane, so eagerly configured warm scopes work on first boot with no
// separate backfill step. A non-empty mirror never re-triggers the backfill.
func TestScopeBackfillMakesWarmSelfSufficient(t *testing.T) {
	base := database.NewMemoryDB()
	ctx := t.Context()
	backfills := 0
	store, err := WrapRuntimeStore(base, RuntimeStoreOptions{
		MaxRecordsPerScope: 16,
		MaxBytesPerScope:   1 << 20,
		ScopeBackfill: func(_ context.Context, domain, collection, organizationID string, visit database.RecordVisitor) error {
			backfills++
			// Simulates the app-side enumerator reading its normalized tables.
			for i := range 3 {
				if err := visit(database.DomainRecord{
					Domain: domain, Collection: collection, OrganizationID: organizationID,
					RecordID: fmt.Sprintf("dish_%d", i),
					Data:     testRecordData(map[string]any{"state": "published"}),
				}); err != nil {
					return err
				}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}

	// Mirror empty + backfiller configured: warm self-backfills.
	if err := store.WarmScope(ctx, "menu", "dishes", "org_1"); err != nil {
		t.Fatalf("WarmScope() error = %v", err)
	}
	if backfills != 1 {
		t.Fatalf("backfills = %d, want 1", backfills)
	}
	// Rows landed durably in the mirror AND in the hot partition.
	if _, found, err := base.GetRecord(ctx, "menu", "dishes", "org_1", "dish_0"); err != nil || !found {
		t.Fatalf("mirror after backfill: found=%v err=%v", found, err)
	}
	name := store.ProjectionName("menu", "dishes", "org_1")
	if count, _ := store.Store().Count(ctx, name, Query{OrganizationID: "org_1"}, Fence{}); count != 3 {
		t.Fatalf("hot count = %d, want 3", count)
	}

	// A fresh process generation over the now-populated mirror must NOT
	// re-trigger the backfill: the mirror is the rebuild source from here on.
	gen2, err := WrapRuntimeStore(base, RuntimeStoreOptions{
		MaxRecordsPerScope: 16, MaxBytesPerScope: 1 << 20,
		ScopeBackfill: func(context.Context, string, string, string, database.RecordVisitor) error {
			backfills++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("gen2 WrapRuntimeStore() error = %v", err)
	}
	if err := gen2.WarmScope(ctx, "menu", "dishes", "org_1"); err != nil {
		t.Fatalf("gen2 WarmScope() error = %v", err)
	}
	if backfills != 1 {
		t.Fatalf("backfills after gen2 = %d, want 1 (non-empty mirror must not re-backfill)", backfills)
	}
	name2 := gen2.ProjectionName("menu", "dishes", "org_1")
	if count, _ := gen2.Store().Count(ctx, name2, Query{OrganizationID: "org_1"}, Fence{}); count != 3 {
		t.Fatalf("gen2 hot count = %d, want 3", count)
	}
}

// TestMirrorSweeperPushesChangedRows pins the one-place projection seam: a
// sweep pulls rows changed after the cursor from each source, pushes them
// through the projected store (durable mirror + hot partition + fan-out), and
// advances the cursor so unchanged rows are never re-read. A failing source is
// counted and retried without advancing; other sources keep sweeping.
func TestMirrorSweeperPushesChangedRows(t *testing.T) {
	base := database.NewMemoryDB()
	store, err := WrapRuntimeStore(base, RuntimeStoreOptions{MaxRecordsPerScope: 16, MaxBytesPerScope: 1 << 20})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	ctx := t.Context()

	// A fake source table: rows with updated_at, served incrementally.
	type row struct {
		id string
		at time.Time
	}
	t0 := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	rows := []row{{"dish_1", t0}, {"dish_2", t0.Add(time.Second)}}
	polls := 0
	source := func(_ context.Context, cursor time.Time, visit func(database.DomainRecord, time.Time) error) error {
		polls++
		for _, r := range rows {
			if !r.at.After(cursor) {
				continue
			}
			if err := visit(database.DomainRecord{
				Domain: "menu", Collection: "dishes", OrganizationID: "org_1", RecordID: r.id,
				Data: testRecordData(map[string]any{"state": "published"}),
			}, r.at); err != nil {
				return err
			}
		}
		return nil
	}
	failing := func(context.Context, time.Time, func(database.DomainRecord, time.Time) error) error {
		return errors.New("source down")
	}

	sweeper, err := NewMirrorSweeper(store, MirrorSweepOptions{BatchSize: 1})
	if err != nil {
		t.Fatalf("NewMirrorSweeper() error = %v", err)
	}
	if err := sweeper.AddSource("menu_dishes", source); err != nil {
		t.Fatalf("AddSource() error = %v", err)
	}
	if err := sweeper.AddSource("broken", failing); err != nil {
		t.Fatalf("AddSource(broken) error = %v", err)
	}

	// First sweep from zero cursor = full sync.
	n, err := sweeper.SweepOnce(ctx)
	if err != nil || n != 2 {
		t.Fatalf("SweepOnce() = %d err=%v, want 2", n, err)
	}
	if _, found, err := base.GetRecord(ctx, "menu", "dishes", "org_1", "dish_2"); err != nil || !found {
		t.Fatalf("mirror after sweep: found=%v err=%v", found, err)
	}
	name := store.ProjectionName("menu", "dishes", "org_1")
	if count, _ := store.Store().Count(ctx, name, Query{OrganizationID: "org_1"}, Fence{}); count != 2 {
		t.Fatalf("hot count = %d, want 2", count)
	}

	// Second sweep: cursor advanced, nothing re-read.
	if n, _ := sweeper.SweepOnce(ctx); n != 0 {
		t.Fatalf("idle sweep swept %d, want 0", n)
	}

	// A change after the cursor is picked up.
	rows = append(rows, row{"dish_3", t0.Add(2 * time.Second)})
	if n, _ := sweeper.SweepOnce(ctx); n != 1 {
		t.Fatalf("incremental sweep swept %d, want 1", n)
	}

	stats := sweeper.Stats()
	if stats.Swept != 3 || stats.Errors != 3 {
		t.Fatalf("stats = %+v, want Swept=3 Errors=3 (one failing source per pass)", stats)
	}
}

// TestMirrorSweeperConvergesHardDeletes pins the delete lane: identities
// announced by a tombstone source are removed from the mirror and the hot
// partition (with live fan-out via the store observer), the cursor advances,
// and replays are safe because DeleteRecord is idempotent.
func TestMirrorSweeperConvergesHardDeletes(t *testing.T) {
	base := database.NewMemoryDB()
	store, err := WrapRuntimeStore(base, RuntimeStoreOptions{MaxRecordsPerScope: 16, MaxBytesPerScope: 1 << 20})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	ctx := t.Context()
	for _, id := range []string{"dish_1", "dish_2"} {
		if _, err := store.UpsertRecord(ctx, database.DomainRecord{
			Domain: "menu", Collection: "dishes", OrganizationID: "org_1", RecordID: id,
			Data: testRecordData(map[string]any{"state": "published"}),
		}); err != nil {
			t.Fatalf("seed %s error = %v", id, err)
		}
	}

	t0 := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	type tomb struct {
		id string
		at time.Time
	}
	tombs := []tomb{}
	source := func(_ context.Context, cursor time.Time, visit func(string, string, string, string, time.Time) error) error {
		for _, tb := range tombs {
			if !tb.at.After(cursor) {
				continue
			}
			if err := visit("menu", "dishes", "org_1", tb.id, tb.at); err != nil {
				return err
			}
		}
		return nil
	}
	sweeper, err := NewMirrorSweeper(store, MirrorSweepOptions{})
	if err != nil {
		t.Fatalf("NewMirrorSweeper() error = %v", err)
	}
	if err := sweeper.AddDeleteSource("tombstones", source); err != nil {
		t.Fatalf("AddDeleteSource() error = %v", err)
	}

	// Hard delete announced by tombstone: swept away from mirror + hot.
	tombs = append(tombs, tomb{"dish_1", t0})
	if n, err := sweeper.SweepOnce(ctx); err != nil || n != 1 {
		t.Fatalf("SweepOnce() = %d err=%v, want 1", n, err)
	}
	if _, found, _ := base.GetRecord(ctx, "menu", "dishes", "org_1", "dish_1"); found {
		t.Fatal("mirror row survived delete sweep")
	}
	name := store.ProjectionName("menu", "dishes", "org_1")
	if count, _ := store.Store().Count(ctx, name, Query{OrganizationID: "org_1"}, Fence{}); count != 1 {
		t.Fatalf("hot count = %d, want 1 (dish_2 only)", count)
	}

	// Cursor advanced: the same tombstone is not re-swept.
	if n, _ := sweeper.SweepOnce(ctx); n != 0 {
		t.Fatalf("idle delete sweep swept %d, want 0", n)
	}
}

func TestNewQueryFilterValidation(t *testing.T) {
	if _, ok := NewQueryFilter("symbol", "OVS"); !ok {
		t.Fatal("string filter should be accepted")
	}
	if _, ok := NewQueryFilter("  ", "OVS"); ok {
		t.Fatal("blank field must be rejected")
	}
	if _, ok := NewQueryFilter("x", []string{"not", "scalar"}); ok {
		t.Fatal("non-scalar value must be rejected")
	}
}

func TestNormalizeSpecBoundsRangeIndexes(t *testing.T) {
	spec, err := normalizeSpec(ProjectionSpec{
		Name: "range_bounds", Domain: "signals", Collection: "ticks",
		RangeIndexedFields: []string{"price", "bucket", "price", " ", "ordinal"},
		MaxRangeIndexes:    2,
	})
	if err != nil {
		t.Fatalf("normalizeSpec: %v", err)
	}
	if len(spec.RangeIndexedFields) != 2 || spec.RangeIndexedFields[0] != "price" || spec.RangeIndexedFields[1] != "bucket" {
		t.Fatalf("bounded range fields = %v, want [price bucket]", spec.RangeIndexedFields)
	}
}

func TestQueryFilterValueRoundTrip(t *testing.T) {
	cases := map[string]any{
		"string": "OVS",
		"int":    int64(-42),
		"uint":   uint64(42),
		"bool":   true,
		"float":  3.5,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			filter, ok := NewQueryFilter("field", in)
			if !ok {
				t.Fatalf("NewQueryFilter(%v) not ok", in)
			}
			got := queryFilterValue(filter)
			want, ok := database.RecordValueFromAny(in)
			if !ok {
				t.Fatalf("RecordValueFromAny(%v) not ok", in)
			}
			gk, gv, _ := got.ScalarIndex()
			wk, wv, _ := want.ScalarIndex()
			if gk != wk || gv != wv {
				t.Fatalf("round trip drift: got (%c,%q) want (%c,%q)", gk, gv, wk, wv)
			}
		})
	}

	for _, kind := range []byte{'i', 'u', 'f'} {
		v := queryFilterValue(QueryFilter{Field: "f", Kind: kind, Value: "not-a-number"})
		if v.Kind != database.RecordValueString {
			t.Fatalf("malformed %c value = kind %v, want string fallback", kind, v.Kind)
		}
	}
}

func TestQueryWithFiltersPlanShape(t *testing.T) {
	sym, _ := NewQueryFilter("symbol", "OVS")
	region, _ := NewQueryFilter("region", "us")
	blank := QueryFilter{Field: "  ", Kind: 's', Value: "x"}

	if q := QueryWithFilters("org_1", 10); q.Plan.count != 0 {
		t.Fatalf("zero filters -> count %d, want 0", q.Plan.count)
	}
	if q := QueryWithFilters("org_1", 10, sym); q.Plan.count != 1 || q.Plan.first.Field != "symbol" {
		t.Fatalf("one filter plan = %+v", q.Plan)
	}

	q := QueryWithFilters("org_1", 10, sym, region, blank)
	if q.Plan.count != 2 {
		t.Fatalf("two valid filters (one blank dropped) -> count %d, want 2", q.Plan.count)
	}
	if q.Plan.filters[0].Field != "region" || q.Plan.filters[1].Field != "symbol" {
		t.Fatalf("filters not sorted by field: %+v", q.Plan.filters)
	}

	rf := q.Plan.RecordFilters()
	if len(rf) != 2 {
		t.Fatalf("RecordFilters len = %d, want 2", len(rf))
	}
}

func TestQueryFromRecordQuery(t *testing.T) {

	single := database.RecordQuery{Limit: 5, Filters: []database.RecordFilter{
		{Field: "symbol", Value: database.StringValue("OVS")},
	}}
	if q := QueryFromRecordQuery("org_1", single); q.Plan.count != 1 || q.Limit != 5 {
		t.Fatalf("single = %+v", q)
	}

	blank := database.RecordQuery{Limit: 1, Filters: []database.RecordFilter{
		{Field: "  ", Value: database.StringValue("x")},
	}}
	if q := QueryFromRecordQuery("org_1", blank); q.Plan.count != 0 {
		t.Fatalf("blank single -> count %d, want 0", q.Plan.count)
	}

	many := database.RecordQuery{Limit: 9, Filters: []database.RecordFilter{
		{Field: "symbol", Value: database.StringValue("OVS")},
		{Field: "", Value: database.StringValue("skip")},
		{Field: "region", Value: database.StringValue("us")},
	}}
	if q := QueryFromRecordQuery("org_1", many); q.Plan.count != 2 {
		t.Fatalf("many -> count %d, want 2", q.Plan.count)
	}
}

func TestRecordMatchesPlannedFilters(t *testing.T) {
	spec := driftSpec()
	rec := testRecord("signals", "ticks", "org_1", "tick_1", map[string]any{"symbol": "OVS"})

	if !recordMatches(rec, spec, Query{OrganizationID: "org_1"}) {
		t.Fatal("record should match its own tenant with no filters")
	}

	if recordMatches(rec, spec, Query{OrganizationID: "org_other"}) {
		t.Fatal("record must not match a different tenant")
	}

	match := QueryWithFilters("org_1", 0, mustFilter(t, "symbol", "OVS"))
	if !recordMatches(rec, spec, match) {
		t.Fatal("record should match an equal planned filter")
	}

	noMatch := QueryWithFilters("org_1", 0, mustFilter(t, "symbol", "NOPE"))
	if recordMatches(rec, spec, noMatch) {
		t.Fatal("record must not match a differing planned filter")
	}
}

func mustFilter(t *testing.T, field string, value any) QueryFilter {
	t.Helper()
	f, ok := NewQueryFilter(field, value)
	if !ok {
		t.Fatalf("NewQueryFilter(%q,%v) not ok", field, value)
	}
	return f
}

func TestCountIndexedDoesNotAllocate(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name: "count_alloc", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"bucket"}, MaxRecords: 1024, MaxBytes: 8 << 20,
	})
	records := make([]database.DomainRecord, 256)
	for i := range records {
		records[i] = testRecord("signals", "ticks", "org_1", fmt.Sprintf("tick_%03d", i), map[string]any{"bucket": i % 8})
	}
	if _, err := store.BulkLoad(t.Context(), "count_alloc", records); err != nil {
		t.Fatalf("BulkLoad: %v", err)
	}
	query := QueryWithFilters("org_1", 0, mustFilter(t, "bucket", 7))
	if got := testing.AllocsPerRun(100, func() {
		count, countErr := store.Count(context.Background(), "count_alloc", query, Fence{})
		if countErr != nil || count != 32 {
			t.Fatalf("Count = %d, %v; want 32, nil", count, countErr)
		}
	}); got != 0 {
		t.Fatalf("Count allocations = %g, want 0", got)
	}
}

func TestIndexCompactionPreservesQueryCorrectness(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name: "signals", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"symbol"}, MaxRecords: 8192, MaxBytes: 64 << 20,
	})
	ctx := t.Context()

	const total = maxIndexDeltaDepth + 64
	const deleted = 50
	for i := range total {
		if _, err := store.Apply(ctx, "signals", Event{
			Operation: OperationUpsert,
			SourceID:  fmt.Sprintf("src_up_%d", i),
			Version:   uint64(i + 1),
			Record:    testRecord("signals", "ticks", "org_1", fmt.Sprintf("tick_%d", i), map[string]any{"symbol": "OVS"}),
		}); err != nil {
			t.Fatalf("upsert %d err=%v", i, err)
		}
	}
	for i := range deleted {
		if _, err := store.Apply(ctx, "signals", Event{
			Operation: OperationDelete,
			SourceID:  fmt.Sprintf("src_del_%d", i),
			Version:   uint64(total + i + 1),
			Record:    testRecord("signals", "ticks", "org_1", fmt.Sprintf("tick_%d", i), nil),
		}); err != nil {
			t.Fatalf("delete %d err=%v", i, err)
		}
	}

	count, err := store.Count(ctx, "signals",
		QueryWithFilters("org_1", 0, mustFilter(t, "symbol", "OVS")), Fence{})
	if err != nil {
		t.Fatalf("Count() err=%v", err)
	}
	if want := int64(total - deleted); count != want {
		t.Fatalf("indexed count after compaction = %d, want %d", count, want)
	}
}

func TestEstimateValueBytes(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  int
	}{
		{"nil", nil, 0},
		{"string", "abcd", 4},
		{"bytes", []byte("xyz"), 3},
		{"float32 slice", []float32{1, 2, 3}, 12},
		{"float64 slice", []float64{1, 2}, 16},
		{"string slice", []string{"ab", "c"}, 3},
		{"bool", true, 1},
		{"int", int64(7), 8},
		{"record value text", database.StringValue("hello"), 5},
		{"record value raw", database.RawValue([]byte(`{"a":1}`)), len(`{"a":1}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := estimateValueBytes(tc.value); got != tc.want {
				t.Fatalf("estimateValueBytes(%v) = %d, want %d", tc.value, got, tc.want)
			}
		})
	}

	m := map[string]any{"k": "vv"}
	if got := estimateValueBytes(m); got != 1+2+16 {
		t.Fatalf("estimateValueBytes(map) = %d, want 19", got)
	}
}
