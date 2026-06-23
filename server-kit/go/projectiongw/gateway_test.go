package projectiongw

import (
	"strconv"
	"testing"
	"time"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	"google.golang.org/protobuf/proto"
)

func newTestGateway(t *testing.T) *Gateway {
	t.Helper()
	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name:          "signals",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"symbol"},
		MaxRecords:    16,
		MaxBytes:      1 << 20,
	})
	if err != nil {
		t.Fatalf("NewStore() err=%v", err)
	}
	gw, err := NewGateway(store, 0)
	if err != nil {
		t.Fatalf("NewGateway() err=%v", err)
	}
	return gw
}

func tickMutation(recordID string, version uint64, symbol string) *foundationpb.RecordMutation {
	return &foundationpb.RecordMutation{
		Operation:      foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT,
		Version:        version,
		Domain:         "signals",
		Collection:     "ticks",
		OrganizationId: "org_1",
		RecordId:       recordID,
		Fields: []*foundationpb.FieldValue{
			{Name: "symbol", Value: &foundationpb.ScalarValue{Kind: &foundationpb.ScalarValue_StringValue{StringValue: symbol}}},
		},
	}
}

func scope() *foundationpb.ProjectionScope {
	return &foundationpb.ProjectionScope{TenantId: "org_1", Domain: "signals", Collection: "ticks"}
}

func TestGatewaySnapshotAfterApply(t *testing.T) {
	gw := newTestGateway(t)
	ctx := t.Context()

	envelope, err := hermes.NewProjectionEnvelope([]*foundationpb.RecordMutation{tickMutation("tick_1", 1, "OVS")}, "corr-1")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() err=%v", err)
	}
	if _, err := gw.ApplyEnvelopes(ctx, "signals", []events.Envelope{envelope}); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}

	snapshot, err := gw.Snapshot(ctx, &foundationpb.ProjectionSnapshotRequest{Scope: scope()})
	if err != nil {
		t.Fatalf("Snapshot() err=%v", err)
	}
	muts := snapshot.GetBatch().GetMutations()
	if len(muts) != 1 || muts[0].GetRecordId() != "tick_1" {
		t.Fatalf("Snapshot mutations = %+v", muts)
	}
	if muts[0].GetOperation() != foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT {
		t.Fatalf("snapshot op = %v, want UPSERT", muts[0].GetOperation())
	}
	if snapshot.GetEpoch() == 0 {
		t.Fatalf("snapshot epoch should be non-zero after apply")
	}
}

func TestGatewayBroadcastsDeltaOnApply(t *testing.T) {
	gw := newTestGateway(t)
	ctx := t.Context()

	sub, err := gw.Subscribe(scope())
	if err != nil {
		t.Fatalf("Subscribe() err=%v", err)
	}
	defer sub.Cancel()

	envelope, err := hermes.NewProjectionEnvelope([]*foundationpb.RecordMutation{tickMutation("tick_1", 1, "OVS")}, "corr-1")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() err=%v", err)
	}
	if _, err := gw.ApplyEnvelopes(ctx, "signals", []events.Envelope{envelope}); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}

	select {
	case frame := <-sub.Frames:
		decoded, err := events.FromBinary(frame.Envelope)
		if err != nil {
			t.Fatalf("frame envelope decode err=%v", err)
		}
		var batch foundationpb.RecordMutationBatch
		if err := proto.Unmarshal(decoded.PayloadBytes, &batch); err != nil {
			t.Fatalf("batch unmarshal err=%v", err)
		}
		if len(batch.GetMutations()) != 1 || batch.GetMutations()[0].GetRecordId() != "tick_1" {
			t.Fatalf("delta mutations = %+v", batch.GetMutations())
		}
		if frame.Watermark != "1" {
			t.Fatalf("frame watermark = %q, want %q", frame.Watermark, "1")
		}
	case <-time.After(time.Second):
		t.Fatal("expected delta frame, got none")
	}
}

func TestGatewayDoesNotBroadcastDuplicates(t *testing.T) {
	gw := newTestGateway(t)
	ctx := t.Context()

	sub, err := gw.Subscribe(scope())
	if err != nil {
		t.Fatalf("Subscribe() err=%v", err)
	}
	defer sub.Cancel()

	mutation := tickMutation("tick_1", 1, "OVS")
	mutation.SourceId = "tick_1@1"
	envelope, err := hermes.NewProjectionEnvelope([]*foundationpb.RecordMutation{mutation}, "corr-1")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() err=%v", err)
	}

	// First apply is accepted and broadcast.
	if _, err := gw.ApplyEnvelopes(ctx, "signals", []events.Envelope{envelope}); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}
	select {
	case <-sub.Frames:
	case <-time.After(time.Second):
		t.Fatal("expected first delta")
	}

	// Re-applying the identical envelope is a duplicate; hermes is the source of
	// truth, so no delta must be broadcast.
	result, err := gw.ApplyEnvelopes(ctx, "signals", []events.Envelope{envelope})
	if err != nil {
		t.Fatalf("ApplyEnvelopes() duplicate err=%v", err)
	}
	if result.Applied != 0 || result.Duplicates != 1 {
		t.Fatalf("expected duplicate apply, got applied=%d duplicates=%d", result.Applied, result.Duplicates)
	}
	select {
	case <-sub.Frames:
		t.Fatal("duplicate apply must not broadcast a delta")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestGatewayBroadcastsInProcessApply(t *testing.T) {
	// The in-process projected runtime store writes via store.Apply (not
	// envelopes). The store observer must still surface a delta, so the live loop
	// works under a pure in-memory server with no Redis projector.
	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name: "signals", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"symbol"}, MaxRecords: 16, MaxBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("NewStore() err=%v", err)
	}
	gw, err := NewGateway(store, 0)
	if err != nil {
		t.Fatalf("NewGateway() err=%v", err)
	}
	defer gw.Close()

	sub, err := gw.Subscribe(scope())
	if err != nil {
		t.Fatalf("Subscribe() err=%v", err)
	}
	defer sub.Cancel()

	if _, err := store.Apply(t.Context(), "signals", hermes.Event{
		Operation: hermes.OperationUpsert,
		SourceID:  "tick_1@1",
		Version:   1,
		Record: database.DomainRecord{
			Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: "tick_1",
			Data: database.RecordData{{Name: "symbol", Value: database.StringValue("OVS")}},
		},
	}); err != nil {
		t.Fatalf("store.Apply() err=%v", err)
	}

	select {
	case frame := <-sub.Frames:
		decoded, err := events.FromBinary(frame.Envelope)
		if err != nil {
			t.Fatalf("decode err=%v", err)
		}
		var batch foundationpb.RecordMutationBatch
		if err := proto.Unmarshal(decoded.PayloadBytes, &batch); err != nil {
			t.Fatalf("unmarshal err=%v", err)
		}
		if len(batch.GetMutations()) != 1 || batch.GetMutations()[0].GetRecordId() != "tick_1" {
			t.Fatalf("delta = %+v", batch.GetMutations())
		}
	case <-time.After(time.Second):
		t.Fatal("expected in-process apply to broadcast a delta")
	}
}

func TestGatewayScopeIsolation(t *testing.T) {
	gw := newTestGateway(t)

	// Subscriber on a different tenant must not receive org_1 deltas.
	other, err := gw.Subscribe(&foundationpb.ProjectionScope{TenantId: "org_2", Domain: "signals", Collection: "ticks"})
	if err != nil {
		t.Fatalf("Subscribe() err=%v", err)
	}
	defer other.Cancel()

	envelope, err := hermes.NewProjectionEnvelope([]*foundationpb.RecordMutation{tickMutation("tick_1", 1, "OVS")}, "corr-1")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() err=%v", err)
	}
	if _, err := gw.ApplyEnvelopes(t.Context(), "signals", []events.Envelope{envelope}); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}

	select {
	case <-other.Frames:
		t.Fatal("cross-tenant subscriber must not receive deltas")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestGatewaySnapshotPaginates(t *testing.T) {
	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name: "signals", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"symbol"}, MaxRecords: 64, MaxBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("NewStore() err=%v", err)
	}
	gw, err := NewGateway(store, 0, WithResolver(func(s *foundationpb.ProjectionScope) (string, hermes.Query, error) {
		return "signals", hermes.QueryWithFilters(s.GetTenantId(), 10), nil
	}))
	if err != nil {
		t.Fatalf("NewGateway() err=%v", err)
	}
	defer gw.Close()

	ctx := t.Context()
	for i := 1; i <= 25; i++ {
		if _, err := store.Apply(ctx, "signals", hermes.Event{
			Operation: hermes.OperationUpsert, SourceID: fmtTick(i), Version: uint64(i),
			Record: database.DomainRecord{
				Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: fmtTick(i),
				Data: database.RecordData{{Name: "symbol", Value: database.StringValue("OVS")}},
			},
		}); err != nil {
			t.Fatalf("Apply() err=%v", err)
		}
	}

	// Page through via the gateway cursor and assert full, non-overlapping coverage.
	seen := map[string]struct{}{}
	cursor := ""
	for pages := 0; ; pages++ {
		snap, err := gw.Snapshot(ctx, &foundationpb.ProjectionSnapshotRequest{Scope: scope(), Cursor: cursor})
		if err != nil {
			t.Fatalf("Snapshot() err=%v", err)
		}
		for _, m := range snap.GetBatch().GetMutations() {
			if _, dup := seen[m.GetRecordId()]; dup {
				t.Fatalf("record %s on two pages", m.GetRecordId())
			}
			seen[m.GetRecordId()] = struct{}{}
		}
		if !snap.GetHasMore() {
			break
		}
		cursor = snap.GetNextCursor()
		if pages > 25 {
			t.Fatal("pagination did not terminate")
		}
	}
	if len(seen) != 25 {
		t.Fatalf("paginated coverage = %d, want 25", len(seen))
	}
}

func TestGatewaySnapshotIsAlwaysBounded(t *testing.T) {
	// A client cannot trigger an unbounded scan: an over-large (or zero) limit is
	// clamped to the gateway's max, keeping the read O(limit) — BoundedWork.
	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name: "signals", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"symbol"}, MaxRecords: 64, MaxBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("NewStore() err=%v", err)
	}
	gw, err := NewGateway(store, 0, WithResolver(func(scope *foundationpb.ProjectionScope) (string, hermes.Query, error) {
		// Deliberately unbounded resolver (Limit 0) to prove the gateway clamps it.
		return "signals", hermes.QueryWithFilters(scope.GetTenantId(), 0), nil
	}))
	if err != nil {
		t.Fatalf("NewGateway() err=%v", err)
	}
	defer gw.Close()

	ctx := t.Context()
	for i := range 40 {
		if _, err := store.Apply(ctx, "signals", hermes.Event{
			Operation: hermes.OperationUpsert, SourceID: fmtTick(i), Version: uint64(i + 1),
			Record: database.DomainRecord{
				Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: fmtTick(i),
				Data: database.RecordData{{Name: "symbol", Value: database.StringValue("OVS")}},
			},
		}); err != nil {
			t.Fatalf("Apply() err=%v", err)
		}
	}

	// Client requests a huge limit; the gateway clamps to DefaultSnapshotLimit.
	snap, err := gw.Snapshot(ctx, &foundationpb.ProjectionSnapshotRequest{Scope: scope(), Limit: 1 << 20})
	if err != nil {
		t.Fatalf("Snapshot() err=%v", err)
	}
	if got := len(snap.GetBatch().GetMutations()); got > DefaultSnapshotLimit {
		t.Fatalf("snapshot returned %d mutations, exceeds bound %d", got, DefaultSnapshotLimit)
	}
}

func fmtTick(i int) string { return "tick_" + strconv.Itoa(i) }

func TestGatewayRejectsInvalidScope(t *testing.T) {
	gw := newTestGateway(t)
	if _, err := gw.Subscribe(&foundationpb.ProjectionScope{TenantId: "org_1", Domain: "signals"}); err == nil {
		t.Fatal("expected ErrScopeInvalid for missing collection")
	}
}
