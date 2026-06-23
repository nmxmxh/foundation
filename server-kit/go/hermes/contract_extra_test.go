package hermes

import (
	"testing"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
)

func mutation(recordID string, version uint64, symbol string) *foundationpb.RecordMutation {
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

// TestApplyEnvelopesRoundTrip covers the protobuf projection-envelope apply path
// (TE-11 envelope parity, TE-10 lifecycle): a RecordMutationBatch wrapped in a
// terminal envelope is decoded and applied, and the records become queryable.
func TestApplyEnvelopesRoundTrip(t *testing.T) {
	store := newTestStore(t, driftSpec())
	ctx := t.Context()

	env, err := NewProjectionEnvelope([]*foundationpb.RecordMutation{
		mutation("tick_1", 1, "OVS"), mutation("tick_2", 2, "ABC"),
	}, "corr_env")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() err=%v", err)
	}
	if _, err := store.ApplyEnvelopes(ctx, "signals", []events.Envelope{env}); err != nil {
		t.Fatalf("ApplyEnvelopes() err=%v", err)
	}

	count, err := store.Count(ctx, "signals", Query{OrganizationID: "org_1"}, Fence{})
	if err != nil || count != 2 {
		t.Fatalf("Count() = %d err=%v, want 2", count, err)
	}
}

// TestApplyEnvelopesObserved covers the gateway apply seam: each accepted mutation
// decoded from the envelope reaches the observer, while an empty envelope set is a
// no-op that still returns the current epoch.
func TestApplyEnvelopesObserved(t *testing.T) {
	store := newTestStore(t, driftSpec())
	ctx := t.Context()

	env, err := NewProjectionEnvelope([]*foundationpb.RecordMutation{mutation("tick_1", 1, "OVS")}, "corr_obs")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() err=%v", err)
	}
	var observed []AppliedMutation
	if _, err := store.ApplyEnvelopesObserved(ctx, "signals", []events.Envelope{env}, func(m AppliedMutation) {
		observed = append(observed, m)
	}); err != nil {
		t.Fatalf("ApplyEnvelopesObserved() err=%v", err)
	}
	if len(observed) != 1 || observed[0].Record.RecordID != "tick_1" {
		t.Fatalf("observed = %+v, want 1 for tick_1", observed)
	}

	// Empty envelope set: no-op that still reports the epoch.
	res, err := store.ApplyEnvelopesObserved(ctx, "signals", nil, func(AppliedMutation) {})
	if err != nil {
		t.Fatalf("ApplyEnvelopesObserved(empty) err=%v", err)
	}
	if res.Epoch == 0 {
		t.Fatal("empty apply should still report a non-zero epoch")
	}
}

// TestApplyEnvelopesRejectsNonProtobuf covers the envelope guard: a non-protobuf
// payload encoding is rejected (the projection lane is protobuf-only), so a
// malformed envelope never mutates state.
func TestApplyEnvelopesRejectsNonProtobuf(t *testing.T) {
	store := newTestStore(t, driftSpec())
	bad := events.Envelope{
		EventType:       ProjectionEnvelopeEventType,
		PayloadEncoding: "json",
		CorrelationID:   "corr_bad",
		SchemaVersion:   events.EnvelopeSchemaVersion,
	}
	if _, err := store.ApplyEnvelopes(t.Context(), "signals", []events.Envelope{bad}); err == nil {
		t.Fatal("non-protobuf projection envelope should be rejected")
	}
}
