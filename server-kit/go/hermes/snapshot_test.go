package hermes

import (
	"strconv"
	"testing"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

// TestScalarProtoRoundTrip is the contract drift guard between the write-side
// mapping (scalarFromProto, contract.go) and the read-side mapping
// (scalarToProto, snapshot.go). A scalar that crosses into hermes via a
// RecordMutation field must come back out as the same proto scalar in a
// snapshot, otherwise the projection and the wire have drifted.
func TestScalarProtoRoundTrip(t *testing.T) {
	cases := map[string]*foundationpb.ScalarValue{
		"string": {Kind: &foundationpb.ScalarValue_StringValue{StringValue: "OVS"}},
		"int":    {Kind: &foundationpb.ScalarValue_Int64Value{Int64Value: -42}},
		"uint":   {Kind: &foundationpb.ScalarValue_Uint64Value{Uint64Value: 42}},
		"double": {Kind: &foundationpb.ScalarValue_DoubleValue{DoubleValue: 3.5}},
		"bool":   {Kind: &foundationpb.ScalarValue_BoolValue{BoolValue: true}},
		"bytes":  {Kind: &foundationpb.ScalarValue_BytesValue{BytesValue: []byte("xy")}},
	}
	for name, scalar := range cases {
		t.Run(name, func(t *testing.T) {
			value, err := scalarFromProto(scalar)
			if err != nil {
				t.Fatalf("scalarFromProto() err=%v", err)
			}
			recordValue, ok := database.RecordValueFromAny(value)
			if !ok {
				t.Fatalf("RecordValueFromAny(%v) not ok", value)
			}
			out, ok := scalarToProto(recordValue)
			if !ok {
				t.Fatalf("scalarToProto() not ok")
			}
			if out.String() != scalar.String() {
				t.Fatalf("round trip drift: got %v want %v", out, scalar)
			}
		})
	}
}

func TestSnapshotProjectionRoundTrip(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:          "signals",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"symbol"},
		MaxRecords:    16,
		MaxBytes:      1 << 20,
	})
	applyTestRecord(t, store, "signals", "org_1", "tick_1", 1, map[string]any{"symbol": "OVS"})

	snapshot, err := store.SnapshotProjection(t.Context(), "signals", Query{OrganizationID: "org_1"}, Fence{}, 0)
	if err != nil {
		t.Fatalf("SnapshotProjection() err=%v", err)
	}
	if len(snapshot.Mutations) != 1 {
		t.Fatalf("snapshot mutations = %d, want 1", len(snapshot.Mutations))
	}
	mutation := snapshot.Mutations[0]
	if mutation.GetRecordId() != "tick_1" || mutation.GetOrganizationId() != "org_1" {
		t.Fatalf("snapshot mutation identity = %+v", mutation)
	}
	if mutation.GetOperation() != foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT {
		t.Fatalf("snapshot op = %v, want UPSERT", mutation.GetOperation())
	}
	if snapshot.Epoch == 0 {
		t.Fatalf("snapshot epoch should be non-zero")
	}
}

func TestSnapshotPageCursor(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name: "signals", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"symbol"}, MaxRecords: 64, MaxBytes: 1 << 20,
	})
	const total = 25
	for i := 1; i <= total; i++ {
		applyTestRecord(t, store, "signals", "org_1", fmtRecord(i), uint64(i), map[string]any{"symbol": "OVS"})
	}

	// Page through the whole scope in bounded pages of 10 (scope > limit).
	query := QueryWithFilters("org_1", 10)
	seen := map[string]uint64{}
	var lastVersion uint64 = 1 << 62 // pages must be strictly version-descending
	cursor := uint64(0)
	pages := 0
	for {
		page, err := store.SnapshotPage(t.Context(), "signals", query, Fence{}, 0, cursor)
		if err != nil {
			t.Fatalf("SnapshotPage() err=%v", err)
		}
		pages++
		for _, m := range page.Mutations {
			if m.GetVersion() >= lastVersion {
				t.Fatalf("page not version-descending: %d after %d", m.GetVersion(), lastVersion)
			}
			lastVersion = m.GetVersion()
			if _, dup := seen[m.GetRecordId()]; dup {
				t.Fatalf("record %s returned on more than one page", m.GetRecordId())
			}
			seen[m.GetRecordId()] = m.GetVersion()
		}
		if !page.HasMore {
			break
		}
		if page.NextCursor == 0 {
			t.Fatalf("HasMore but NextCursor is zero")
		}
		cursor = page.NextCursor
		if pages > total {
			t.Fatalf("pagination did not terminate")
		}
	}
	if len(seen) != total {
		t.Fatalf("paginated coverage = %d records, want %d", len(seen), total)
	}
	if pages != 3 { // 25 records / 10 per page = 3 pages (10,10,5)
		t.Fatalf("expected 3 pages, got %d", pages)
	}
}

func fmtRecord(i int) string { return "tick_" + strconv.Itoa(i) }

func TestSnapshotProjectionIncremental(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name: "signals", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"symbol"}, MaxRecords: 16, MaxBytes: 1 << 20,
	})
	applyTestRecord(t, store, "signals", "org_1", "tick_1", 1, map[string]any{"symbol": "A"})
	applyTestRecord(t, store, "signals", "org_1", "tick_2", 2, map[string]any{"symbol": "B"})
	applyTestRecord(t, store, "signals", "org_1", "tick_3", 3, map[string]any{"symbol": "C"})

	// Full snapshot returns all three.
	full, err := store.SnapshotProjection(t.Context(), "signals", Query{OrganizationID: "org_1"}, Fence{}, 0)
	if err != nil || len(full.Mutations) != 3 {
		t.Fatalf("full snapshot len=%d err=%v", len(full.Mutations), err)
	}

	// Incremental snapshot since version 1 returns only the records that advanced
	// past it (versions 2 and 3), not the whole collection.
	incremental, err := store.SnapshotProjection(t.Context(), "signals", Query{OrganizationID: "org_1"}, Fence{}, 1)
	if err != nil {
		t.Fatalf("incremental snapshot err=%v", err)
	}
	if len(incremental.Mutations) != 2 {
		t.Fatalf("incremental snapshot len=%d, want 2", len(incremental.Mutations))
	}
	for _, m := range incremental.Mutations {
		if m.GetVersion() <= 1 {
			t.Fatalf("incremental snapshot leaked stale record version %d", m.GetVersion())
		}
	}
}
