package hermes

import (
	"testing"
	"time"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

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

	// fieldsToProto skips fields whose scalar mapping fails and returns nil for
	// empty data.
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
	intVec.validity.set(0) // index 1 stays null
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
