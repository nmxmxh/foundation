package hermes

import (
	"testing"
)

// TestGetColumnarBatchBuildsTypedColumns covers the columnar read path end to end
// (TE-11 columnar parity): reserved fields map to fixed record attributes and data
// fields infer their column type from the first valid scalar, while an unknown
// field yields an empty string column. The batch is the public contract; the
// oracle is the per-column vector type and representative values.
func TestGetColumnarBatchBuildsTypedColumns(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name: "signals", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"symbol"}, MaxRecords: 16, MaxBytes: 1 << 20,
	})
	ctx := t.Context()

	applyTestRecord(t, store, "signals", "org_1", "tick_1", 1,
		map[string]any{"symbol": "OVS", "qty": int64(10), "price": 3.5, "active": true})
	applyTestRecord(t, store, "signals", "org_1", "tick_2", 2,
		map[string]any{"symbol": "ABC", "qty": int64(20), "price": 9.0, "active": false})

	fields := []string{
		"_record", "record_id", "organization_id", "created_at", "updated_at", "version",
		"symbol", "qty", "price", "active", "missing_field",
	}
	batch, err := store.GetColumnarBatch(ctx, "signals", Query{OrganizationID: "org_1"}, fields, Fence{})
	if err != nil {
		t.Fatalf("GetColumnarBatch() err=%v", err)
	}
	if batch.Rows != 2 {
		t.Fatalf("rows = %d, want 2", batch.Rows)
	}
	if len(batch.Columns) != len(fields) {
		t.Fatalf("columns = %d, want %d", len(batch.Columns), len(fields))
	}

	col := map[string]Vector{}
	for _, c := range batch.Columns {
		col[c.Name] = c.Data
	}

	// Reserved field column types.
	assertColumnType(t, col, "_record", TypeBinary)
	assertColumnType(t, col, "record_id", TypeString)
	assertColumnType(t, col, "organization_id", TypeString)
	assertColumnType(t, col, "created_at", TypeTimestamp)
	assertColumnType(t, col, "updated_at", TypeTimestamp)
	assertColumnType(t, col, "version", TypeInt64)

	// Data field types inferred from the first valid scalar.
	assertColumnType(t, col, "symbol", TypeString) // 's'
	assertColumnType(t, col, "qty", TypeInt64)     // 'i'
	assertColumnType(t, col, "price", TypeFloat64) // 'f'
	assertColumnType(t, col, "active", TypeInt64)  // 'b' -> 0/1 int column
	assertColumnType(t, col, "missing_field", TypeString)

	// Rows are sorted version-descending (equal timestamps): tick_2 then tick_1.
	rid := col["record_id"]
	if sv, ok := rid.(*StringVector); !ok || sv.ValueAt(0) != "tick_2" || sv.ValueAt(1) != "tick_1" {
		t.Fatalf("record_id order = %v, want [tick_2 tick_1]", rid.StringValues())
	}
	if v := col["version"].Int64Values(); v[0] != 2 || v[1] != 1 {
		t.Fatalf("version column = %v, want [2 1]", v)
	}
	if q := col["qty"].Int64Values(); q[0] != 20 || q[1] != 10 {
		t.Fatalf("qty column = %v, want [20 10]", q)
	}
	if p := col["price"].Float64Values(); p[0] != 9.0 || p[1] != 3.5 {
		t.Fatalf("price column = %v, want [9 3.5]", p)
	}
	// The unknown field is an all-null string column.
	if mv := col["missing_field"]; mv.NullCount() != 2 {
		t.Fatalf("missing_field null count = %d, want 2", mv.NullCount())
	}
}

func assertColumnType(t *testing.T, col map[string]Vector, name string, want DataType) {
	t.Helper()
	v, ok := col[name]
	if !ok {
		t.Fatalf("column %q missing", name)
	}
	if v.Type() != want {
		t.Fatalf("column %q type = %v, want %v", name, v.Type(), want)
	}
}
