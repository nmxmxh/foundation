package hermes

import (
	"errors"
	"testing"
	"time"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	redispkg "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

func TestStoreApplyProjectionEnvelopeContract(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:          "projection_contract",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"bucket", "symbol"},
		MaxRecords:    10,
		MaxBytes:      1 << 20,
	})
	envelope, err := NewProjectionEnvelope([]*foundationpb.RecordMutation{
		projectionMutation("tick_1", 7, "corr_projection"),
	}, "corr_projection")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() error = %v", err)
	}
	raw, err := envelope.ToBinary()
	if err != nil {
		t.Fatalf("ToBinary() error = %v", err)
	}

	result, err := store.ApplyEnvelopeBytes(t.Context(), "projection_contract", raw)
	if err != nil || result.Applied != 1 {
		t.Fatalf("ApplyEnvelopeBytes() result=%+v err=%v", result, err)
	}
	count, err := store.Count(t.Context(), "projection_contract", QueryFromRecordQuery("org_1", testRecordQuery(0, map[string]any{"bucket": int64(7)})), Fence{})
	if err != nil || count != 1 {
		t.Fatalf("Count() = %d err=%v", count, err)
	}
}

func TestStoreApplyProjectionEnvelopePatchPreservesExistingFields(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:          "projection_patch_contract",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"status", "bucket"},
		MaxRecords:    10,
		MaxBytes:      1 << 20,
	})
	ctx := t.Context()
	applyTestRecord(t, store, "projection_patch_contract", "org_1", "tick_patch", 10, map[string]any{
		"status": "active",
		"bucket": 1,
		"title":  "original",
	})
	patch := projectionMutation("tick_patch", 11, "corr_patch")
	patch.Operation = foundationpb.ProjectionOperation_PROJECTION_OPERATION_PATCH
	patch.SourceId = "projection:patch:tick_patch"
	patch.Fields = []*foundationpb.FieldValue{
		{
			Name: "status",
			Value: &foundationpb.ScalarValue{
				Kind: &foundationpb.ScalarValue_StringValue{StringValue: "archived"},
			},
		},
		{
			Name: "bucket",
			Value: &foundationpb.ScalarValue{
				Kind: &foundationpb.ScalarValue_Int64Value{Int64Value: 2},
			},
		},
	}
	envelope, err := NewProjectionEnvelope([]*foundationpb.RecordMutation{patch}, "corr_patch")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() error = %v", err)
	}
	result, err := store.ApplyEnvelope(ctx, "projection_patch_contract", envelope)
	if err != nil || result.Applied != 1 {
		t.Fatalf("ApplyEnvelope() patch result=%+v err=%v", result, err)
	}
	rec, ok, err := store.GetRecord(ctx, "projection_patch_contract", Query{OrganizationID: "org_1"}, "tick_patch", Fence{MinEpoch: result.Epoch})
	if err != nil || !ok {
		t.Fatalf("GetRecord() after patch ok=%v err=%v", ok, err)
	}
	if !recordDataStringEquals(rec.Data, "title", "original") ||
		!recordDataStringEquals(rec.Data, "status", "archived") ||
		!recordDataIntEquals(rec.Data, "bucket", 2) {
		t.Fatalf("protobuf patch did not merge correctly: %+v", rec.Data)
	}
}

func TestEnvelopeTailerPollOnceAppliesFoundationEnvelopeFromRedisStream(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:          "projection_stream",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"symbol"},
		MaxRecords:    10,
		MaxBytes:      1 << 20,
	})
	envelope, err := NewProjectionEnvelope([]*foundationpb.RecordMutation{
		projectionMutation("tick_2", 8, "corr_stream"),
	}, "corr_stream")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() error = %v", err)
	}
	raw, err := envelope.ToBinary()
	if err != nil {
		t.Fatalf("ToBinary() error = %v", err)
	}
	client := redispkg.NewMemoryClient("test")
	if _, err := client.XAdd(t.Context(), "hermes:projection", redispkg.Values{redispkg.Field("envelope", raw)}); err != nil {
		t.Fatalf("XAdd() error = %v", err)
	}
	source, err := NewRedisStreamEnvelopeSource(client, "hermes:projection", "hermes", "node_1", "")
	if err != nil {
		t.Fatalf("NewRedisStreamEnvelopeSource() error = %v", err)
	}
	tailer, err := NewEnvelopeTailer(store, "projection_stream", source, TailerOptions{MaxBatch: 8})
	if err != nil {
		t.Fatalf("NewEnvelopeTailer() error = %v", err)
	}

	result, err := tailer.PollOnce(t.Context())
	if err != nil || result.Read != 1 || result.Decoded != 1 || result.Acked != 1 || result.Apply.Applied != 1 {
		t.Fatalf("PollOnce() result=%+v err=%v", result, err)
	}
	count, err := store.Count(t.Context(), "projection_stream", QueryFromRecordQuery("org_1", testRecordQuery(0, map[string]any{"symbol": "OVS"})), Fence{})
	if err != nil || count != 1 {
		t.Fatalf("Count() = %d err=%v", count, err)
	}
}

func TestRedisStreamEnvelopeSourceReplaysPendingBeforeNewMessages(t *testing.T) {
	envelope, err := NewProjectionEnvelope([]*foundationpb.RecordMutation{
		projectionMutation("tick_pending", 1, "corr_pending"),
	}, "corr_pending")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() error = %v", err)
	}
	raw, err := envelope.ToBinary()
	if err != nil {
		t.Fatalf("ToBinary() error = %v", err)
	}
	client := redispkg.NewMemoryClient("test")
	firstID, err := client.XAdd(t.Context(), "hermes:pending", redispkg.Values{redispkg.Field("envelope", raw)})
	if err != nil {
		t.Fatalf("XAdd(first) error = %v", err)
	}
	if _, err := client.XAdd(t.Context(), "hermes:pending", redispkg.Values{redispkg.Field("envelope", raw)}); err != nil {
		t.Fatalf("XAdd(second) error = %v", err)
	}
	source, err := NewRedisStreamEnvelopeSource(client, "hermes:pending", "hermes", "node_1", "")
	if err != nil {
		t.Fatalf("NewRedisStreamEnvelopeSource() error = %v", err)
	}
	first, err := source.ReadEnvelopes(t.Context(), 1)
	if err != nil || len(first) != 1 || first[0].AckID != firstID {
		t.Fatalf("first ReadEnvelopes() = %+v err=%v, want first", first, err)
	}
	pending, err := source.ReadEnvelopes(t.Context(), 1)
	if err != nil || len(pending) != 1 || pending[0].AckID != firstID {
		t.Fatalf("pending ReadEnvelopes() = %+v err=%v, want first again", pending, err)
	}
	if err := source.Ack(t.Context(), firstID); err != nil {
		t.Fatalf("Ack() error = %v", err)
	}
	next, err := source.ReadEnvelopes(t.Context(), 1)
	if err != nil || len(next) != 1 || next[0].AckID == firstID {
		t.Fatalf("next ReadEnvelopes() = %+v err=%v, want second", next, err)
	}
}

func TestProjectionEnvelopeRejectsJSONPayload(t *testing.T) {
	_, err := EventsFromEnvelope(events.Envelope{
		EventType:       ProjectionEnvelopeEventType,
		Payload:         extension.Object{"record_id": extension.String("tick_1")},
		PayloadEncoding: events.PayloadEncodingJSON,
		Metadata:        extension.Object{"correlation_id": extension.String("corr_json")},
		CorrelationID:   "corr_json",
		SchemaVersion:   events.EnvelopeSchemaVersion,
		Timestamp:       testTime(),
	})
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("EventsFromEnvelope() err=%v, want ErrInvalidEvent", err)
	}
}

func TestProjectionEnvelopeRejectsDuplicateFields(t *testing.T) {
	mutation := projectionMutation("tick_3", 9, "corr_duplicate")
	mutation.Fields = append(mutation.Fields, &foundationpb.FieldValue{
		Name: "bucket",
		Value: &foundationpb.ScalarValue{
			Kind: &foundationpb.ScalarValue_Int64Value{Int64Value: 9},
		},
	})
	envelope, err := NewProjectionEnvelope([]*foundationpb.RecordMutation{mutation}, "corr_duplicate")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() error = %v", err)
	}
	_, err = EventsFromEnvelope(envelope)
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("EventsFromEnvelope() err=%v, want ErrInvalidEvent", err)
	}
}

func projectionMutation(recordID string, version uint64, correlationID string) *foundationpb.RecordMutation {
	return &foundationpb.RecordMutation{
		Operation:            foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT,
		SourceId:             "projection:" + recordID,
		Version:              version,
		Domain:               "signals",
		Collection:           "ticks",
		OrganizationId:       "org_1",
		RecordId:             recordID,
		Payload:              []byte{0x01, 0x02, 0x03},
		PayloadEncoding:      foundationpb.PayloadEncoding_PAYLOAD_ENCODING_CAPNP,
		PayloadSchemaVersion: "capnp.signals.ticks.v1",
		CorrelationId:        correlationID,
		Fields: []*foundationpb.FieldValue{
			{
				Name: "bucket",
				Value: &foundationpb.ScalarValue{
					Kind: &foundationpb.ScalarValue_Int64Value{Int64Value: int64(version)},
				},
			},
			{
				Name: "symbol",
				Value: &foundationpb.ScalarValue{
					Kind: &foundationpb.ScalarValue_StringValue{StringValue: "OVS"},
				},
			},
		},
	}
}

func testTime() time.Time {
	return time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
}
