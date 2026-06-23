package hermes

import (
	"fmt"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

// TestIndexCompactionPreservesQueryCorrectness drives the indexed delta chain past
// its compaction threshold (maxIndexDeltaDepth) with a mix of upserts and deletes,
// then asserts the indexed query still returns exactly the live set (TE-04 many,
// TE-18 bounded compaction). Compaction is a performance optimization; this proves
// it does not change the visible query result.
func TestIndexCompactionPreservesQueryCorrectness(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name: "signals", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"symbol"}, MaxRecords: 8192, MaxBytes: 64 << 20,
	})
	ctx := t.Context()

	const total = maxIndexDeltaDepth + 64 // exceed the delta depth so compaction fires
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

// TestEstimateValueBytes covers the value-size estimator used for projection byte
// budgeting across each supported value shape, including the recursive map case
// and the typed RecordValue (raw bytes preferred over text).
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

	// Recursive map: keys + nested value estimates + per-entry overhead.
	m := map[string]any{"k": "vv"} // 1 (key) + 2 (value) + 16 overhead
	if got := estimateValueBytes(m); got != 1+2+16 {
		t.Fatalf("estimateValueBytes(map) = %d, want 19", got)
	}
}
