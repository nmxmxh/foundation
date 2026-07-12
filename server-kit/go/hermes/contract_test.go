package hermes

import (
	"errors"
	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	redispkg "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
	"testing"
	"time"
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

// TestWatermarkRoundTrip exercises the resume-token codec used by the projection
// transport. FormatWatermark/ParseWatermark are inverse functions, and malformed
// or empty tokens must resolve to zero (full snapshot / resume from beginning).
func TestWatermarkRoundTrip(t *testing.T) {
	if got := FormatWatermark(0); got != "" {
		t.Fatalf("FormatWatermark(0) = %q, want empty", got)
	}
	if got := FormatWatermark(42); got != "42" {
		t.Fatalf("FormatWatermark(42) = %q, want 42", got)
	}
	if got := ParseWatermark(""); got != 0 {
		t.Fatalf("ParseWatermark(\"\") = %d, want 0", got)
	}
	if got := ParseWatermark("not-a-number"); got != 0 {
		t.Fatalf("ParseWatermark(malformed) = %d, want 0", got)
	}
	if got := ParseWatermark(FormatWatermark(987654321)); got != 987654321 {
		t.Fatalf("ParseWatermark(FormatWatermark(987654321)) = %d", got)
	}
}

// TestMutationFromView projects a borrowed RecordView into an owned mutation and
// asserts the projection copies scalars, vector, and timestamps so the result
// outlives the ForEachView callback that produced the view.
func TestMutationFromView(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	view := RecordView{
		Domain:         "signals",
		Collection:     "ticks",
		OrganizationID: "org_1",
		RecordID:       "tick_1",
		Version:        7,
		Data: database.RecordData{
			{Name: "symbol", Value: database.StringValue("OVS")},
			{Name: "price", Value: database.FloatValue(3.5)},
		},
		Vector:    []float32{0.1, 0.2},
		CreatedAt: now,
		UpdatedAt: now,
	}

	mutation := MutationFromView(view, OperationPatch)
	if mutation.GetRecordId() != "tick_1" || mutation.GetOrganizationId() != "org_1" {
		t.Fatalf("mutation identity = %+v", mutation)
	}
	if mutation.GetVersion() != 7 {
		t.Fatalf("mutation version = %d, want 7", mutation.GetVersion())
	}
	if mutation.GetOperation() != foundationpb.ProjectionOperation_PROJECTION_OPERATION_PATCH {
		t.Fatalf("mutation op = %v, want PATCH", mutation.GetOperation())
	}
	if len(mutation.GetFields()) != 2 {
		t.Fatalf("mutation fields = %d, want 2", len(mutation.GetFields()))
	}
	if len(mutation.GetVector()) != 2 {
		t.Fatalf("mutation vector = %d, want 2", len(mutation.GetVector()))
	}
	if mutation.GetCreatedAt() == nil || mutation.GetUpdatedAt() == nil {
		t.Fatalf("mutation timestamps should be set")
	}
}

// TestOperationToProto pins the Operation -> proto enum mapping, including the
// default (upsert) branch for unknown operations.
func TestOperationToProto(t *testing.T) {
	cases := map[Operation]foundationpb.ProjectionOperation{
		OperationUpsert:      foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT,
		OperationPatch:       foundationpb.ProjectionOperation_PROJECTION_OPERATION_PATCH,
		OperationDelete:      foundationpb.ProjectionOperation_PROJECTION_OPERATION_DELETE,
		Operation("unknown"): foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT,
	}
	for op, want := range cases {
		if got := operationToProto(op); got != want {
			t.Fatalf("operationToProto(%q) = %v, want %v", op, got, want)
		}
	}
}

// TestScalarToProtoRejectsMalformed covers the parse-failure branches: a numeric
// kind whose Text is not parseable must report ok=false, and a null/unknown kind
// carries no scalar payload.
func TestScalarToProtoRejectsMalformed(t *testing.T) {
	bad := []database.RecordValue{
		{Kind: database.RecordValueInt, Text: "not-int"},
		{Kind: database.RecordValueUint, Text: "-1"},
		{Kind: database.RecordValueFloat, Text: "not-float"},
		{Kind: database.RecordValueNull},
	}
	for _, value := range bad {
		if _, ok := scalarToProto(value); ok {
			t.Fatalf("scalarToProto(%+v) ok=true, want false", value)
		}
	}

	if got := fieldsToProto(nil); got != nil {
		t.Fatalf("fieldsToProto(nil) = %v, want nil", got)
	}
	mixed := database.RecordData{
		{Name: "ok", Value: database.StringValue("v")},
		{Name: "bad", Value: database.RecordValue{Kind: database.RecordValueInt, Text: "x"}},
	}
	if got := fieldsToProto(mixed); len(got) != 1 {
		t.Fatalf("fieldsToProto(mixed) = %d fields, want 1", len(got))
	}
}

// TestVectorAccessors exercises the columnar Vector accessor surface for each
// concrete vector type. These getters back the read-side columnar batch contract
// and must report type, length, null counts, validity, and the type-appropriate
// value slice (nil for mismatched accessors).
func TestVectorAccessors(t *testing.T) {
	intVec := newInt64Vector(2)
	intVec.values[0], intVec.values[1] = 10, 20
	intVec.validity.set(0)
	assertVector(t, intVec, TypeInt64, 2, 1)
	if got := intVec.Int64Values(); len(got) != 2 || got[0] != 10 {
		t.Fatalf("Int64Values = %v", got)
	}
	if intVec.Float64Values() != nil || intVec.StringValues() != nil || intVec.BytesValues() != nil {
		t.Fatalf("Int64Vector mismatched accessors should be nil")
	}

	floatVec := newFloat64Vector(1)
	floatVec.values[0] = 1.5
	floatVec.validity.set(0)
	assertVector(t, floatVec, TypeFloat64, 1, 0)
	if got := floatVec.Float64Values(); len(got) != 1 || got[0] != 1.5 {
		t.Fatalf("Float64Values = %v", got)
	}
	if floatVec.Int64Values() != nil || floatVec.StringValues() != nil || floatVec.BytesValues() != nil {
		t.Fatalf("Float64Vector mismatched accessors should be nil")
	}

	strVec, err := newStringVectorFromSlice([]string{"a", "bc"}, []bool{true, true})
	if err != nil {
		t.Fatalf("newStringVectorFromSlice err=%v", err)
	}
	assertVector(t, strVec, TypeString, 2, 0)
	if got := strVec.StringValues(); len(got) != 2 || got[1] != "bc" {
		t.Fatalf("StringValues = %v", got)
	}
	if strVec.ValueAt(0) != "a" {
		t.Fatalf("ValueAt(0) = %q", strVec.ValueAt(0))
	}
	if len(strVec.Offsets()) != 3 || len(strVec.Bytes()) != 3 {
		t.Fatalf("offsets=%v bytes=%v", strVec.Offsets(), strVec.Bytes())
	}
	if strVec.Int64Values() != nil || strVec.Float64Values() != nil || strVec.BytesValues() != nil {
		t.Fatalf("StringVector mismatched accessors should be nil")
	}

	tsVec := newTimestampVector(1)
	tsVec.values[0] = time.Unix(0, 1234)
	tsVec.validity.set(0)
	assertVector(t, tsVec, TypeTimestamp, 1, 0)
	if got := tsVec.Int64Values(); len(got) != 1 || got[0] != 1234 {
		t.Fatalf("Timestamp Int64Values = %v", got)
	}
	if tsVec.Float64Values() != nil || tsVec.StringValues() != nil || tsVec.BytesValues() != nil {
		t.Fatalf("TimestampVector mismatched accessors should be nil")
	}

	recVec := &DomainRecordVector{
		values:   []database.DomainRecord{{RecordID: "r1"}},
		validity: newValidityBitmap(1),
	}
	recVec.validity.set(0)
	assertVector(t, recVec, TypeBinary, 1, 0)
	if got := recVec.RecordValues(); len(got) != 1 || got[0].RecordID != "r1" {
		t.Fatalf("RecordValues = %v", got)
	}
	if recVec.Int64Values() != nil || recVec.Float64Values() != nil ||
		recVec.StringValues() != nil || recVec.BytesValues() != nil {
		t.Fatalf("DomainRecordVector mismatched accessors should be nil")
	}
}

func assertVector(t *testing.T, v Vector, wantType DataType, wantLen, wantNulls int) {
	t.Helper()
	if v.Type() != wantType {
		t.Fatalf("Type() = %v, want %v", v.Type(), wantType)
	}
	if v.Len() != wantLen {
		t.Fatalf("Len() = %d, want %d", v.Len(), wantLen)
	}
	if v.NullCount() != wantNulls {
		t.Fatalf("NullCount() = %d, want %d", v.NullCount(), wantNulls)
	}
	if !v.IsValid(0) {
		t.Fatalf("IsValid(0) = false, want true")
	}
}
