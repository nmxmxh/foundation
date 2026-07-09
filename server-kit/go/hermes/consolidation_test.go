package hermes

import (
	"context"
	"errors"
	"fmt"
	"testing"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	redispkg "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/worker"
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
