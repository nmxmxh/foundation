package hermes

import (
	"context"
	"fmt"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"strconv"
	"testing"
	"time"
)

func TestGetColumnarBatch(t *testing.T) {
	store, err := NewStore(ProjectionSpec{
		Name:          "ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"symbol", "bucket", "price"},
	})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	ctx := context.Background()
	now := time.Now().UTC()

	// Seed 3 records
	records := []database.DomainRecord{
		{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       "tick_1",
			CreatedAt:      now.Add(-time.Minute),
			UpdatedAt:      now.Add(-time.Minute),
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "symbol", Value: database.StringValue("OVS")},
				database.RecordField{Name: "bucket", Value: database.IntValue(5)},
				database.RecordField{Name: "price", Value: database.FloatValue(12.34)},
			),
		},
		{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       "tick_2",
			CreatedAt:      now,
			UpdatedAt:      now,
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "symbol", Value: database.StringValue("AAPL")},
				database.RecordField{Name: "bucket", Value: database.IntValue(10)},
				database.RecordField{Name: "price", Value: database.FloatValue(150.50)},
			),
		},
		{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       "tick_3",
			CreatedAt:      now.Add(time.Minute),
			UpdatedAt:      now.Add(time.Minute),
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "symbol", Value: database.StringValue("MSFT")},
				database.RecordField{Name: "bucket", Value: database.IntValue(15)},
				database.RecordField{Name: "price", Value: database.FloatValue(310.25)},
			),
		},
	}

	if _, err := store.BulkLoad(ctx, "ticks", records); err != nil {
		t.Fatalf("failed to bulk load: %v", err)
	}

	// Fetch a Columnar Batch
	query := Query{
		OrganizationID: "org_1",
	}
	fields := []string{"record_id", "symbol", "bucket", "price", "created_at"}

	batch, err := store.GetColumnarBatch(ctx, "ticks", query, fields, Fence{})
	if err != nil {
		t.Fatalf("GetColumnarBatch failed: %v", err)
	}

	if batch.Rows != 3 {
		t.Errorf("expected 3 rows, got %d", batch.Rows)
	}

	if len(batch.Columns) != len(fields) {
		t.Fatalf("expected %d columns, got %d", len(fields), len(batch.Columns))
	}

	// Verify Record IDs
	recordIDCol := batch.Columns[0]
	if recordIDCol.Name != "record_id" {
		t.Errorf("expected column record_id, got %s", recordIDCol.Name)
	}
	recordIDVals := recordIDCol.Data.StringValues()
	if len(recordIDVals) != 3 || recordIDVals[0] != "tick_3" || recordIDVals[1] != "tick_2" || recordIDVals[2] != "tick_1" {
		t.Errorf("unexpected record_id values: %v", recordIDVals)
	}

	// Verify symbol values
	symbolCol := batch.Columns[1]
	symbolVals := symbolCol.Data.StringValues()
	if len(symbolVals) != 3 || symbolVals[0] != "MSFT" || symbolVals[1] != "AAPL" || symbolVals[2] != "OVS" {
		t.Errorf("unexpected symbol values: %v", symbolVals)
	}

	// Verify bucket values (Int64)
	bucketCol := batch.Columns[2]
	if bucketCol.Data.Type() != TypeInt64 {
		t.Errorf("expected bucket column to be TypeInt64, got %v", bucketCol.Data.Type())
	}
	bucketVals := bucketCol.Data.Int64Values()
	if len(bucketVals) != 3 || bucketVals[0] != 15 || bucketVals[1] != 10 || bucketVals[2] != 5 {
		t.Errorf("unexpected bucket values: %v", bucketVals)
	}

	// Verify price values (Float64)
	priceCol := batch.Columns[3]
	if priceCol.Data.Type() != TypeFloat64 {
		t.Errorf("expected price column to be TypeFloat64, got %v", priceCol.Data.Type())
	}
	priceVals := priceCol.Data.Float64Values()
	if len(priceVals) != 3 || priceVals[0] != 310.25 || priceVals[1] != 150.50 || priceVals[2] != 12.34 {
		t.Errorf("unexpected price values: %v", priceVals)
	}
}

func BenchmarkHermesGetColumnarBatch(b *testing.B) {
	// Create a store with 10k records
	store, err := NewStore(ProjectionSpec{
		Name:          "ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"symbol", "bucket", "price"},
		MaxRecords:    100000,
	})
	if err != nil {
		b.Fatalf("failed to create store: %v", err)
	}

	ctx := context.Background()
	records := make([]database.DomainRecord, 10000)
	for i := range records {
		records[i] = database.DomainRecord{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       fmt.Sprintf("tick_%06d", i),
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "symbol", Value: database.StringValue("OVS")},
				database.RecordField{Name: "bucket", Value: database.IntValue(int64(i % 16))},
				database.RecordField{Name: "price", Value: database.FloatValue(float64(i) * 1.5)},
			),
		}
	}

	if _, err := store.BulkLoad(ctx, "ticks", records); err != nil {
		b.Fatalf("failed to bulk load: %v", err)
	}

	query := Query{
		OrganizationID: "org_1",
	}
	fields := []string{"record_id", "symbol", "bucket", "price"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		batch, err := store.GetColumnarBatch(ctx, "ticks", query, fields, Fence{})
		if err != nil || batch.Rows != 10000 {
			b.Fatalf("GetColumnarBatch failed: %v", err)
		}
	}
}

func BenchmarkHermesListRecordsComparison(b *testing.B) {
	store, err := NewStore(ProjectionSpec{
		Name:          "ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"symbol", "bucket", "price"},
		MaxRecords:    100000,
	})
	if err != nil {
		b.Fatalf("failed to create store: %v", err)
	}

	ctx := context.Background()
	records := make([]database.DomainRecord, 10000)
	for i := range records {
		records[i] = database.DomainRecord{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       fmt.Sprintf("tick_%06d", i),
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "symbol", Value: database.StringValue("OVS")},
				database.RecordField{Name: "bucket", Value: database.IntValue(int64(i % 16))},
				database.RecordField{Name: "price", Value: database.FloatValue(float64(i) * 1.5)},
			),
		}
	}

	if _, err := store.BulkLoad(ctx, "ticks", records); err != nil {
		b.Fatalf("failed to bulk load: %v", err)
	}

	query := Query{
		OrganizationID: "org_1",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		list, err := store.ListRecords(ctx, "ticks", query, Fence{})
		if err != nil || len(list) != 10000 {
			b.Fatalf("ListRecords failed: %v", err)
		}
	}
}

// BenchmarkHermesColumnarSumPrice proves the sequential-scan advantage of the
// offset+bytes layout: iterating Float64Values() is a single contiguous slice
// scan with no pointer chasing. Compare against BenchmarkHermesListRecordsSumPrice
// which chases one pointer per record into RecordData.
func BenchmarkHermesColumnarSumPrice(b *testing.B) {
	store, err := NewStore(ProjectionSpec{
		Name:          "ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"symbol", "bucket", "price"},
		MaxRecords:    100000,
	})
	if err != nil {
		b.Fatalf("failed to create store: %v", err)
	}

	ctx := context.Background()
	records := make([]database.DomainRecord, 10000)
	for i := range records {
		records[i] = database.DomainRecord{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       fmt.Sprintf("tick_%06d", i),
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "symbol", Value: database.StringValue("OVS")},
				database.RecordField{Name: "bucket", Value: database.IntValue(int64(i % 16))},
				database.RecordField{Name: "price", Value: database.FloatValue(float64(i) * 1.5)},
			),
		}
	}
	if _, err := store.BulkLoad(ctx, "ticks", records); err != nil {
		b.Fatalf("failed to bulk load: %v", err)
	}

	query := Query{OrganizationID: "org_1"}
	fields := []string{"price"}

	b.ReportAllocs()
	b.ResetTimer()
	var sink float64
	for i := 0; i < b.N; i++ {
		batch, err := store.GetColumnarBatch(ctx, "ticks", query, fields, Fence{})
		if err != nil {
			b.Fatal(err)
		}
		vals := batch.Columns[0].Data.Float64Values()
		sum := 0.0
		for _, v := range vals {
			sum += v
		}
		sink = sum
	}
	_ = sink
}

// BenchmarkHermesListRecordsSumPrice is the equivalent scan over a []DomainRecord
// slice: each price access chases a pointer into RecordData, defeating the CPU
// prefetcher. This is the baseline BenchmarkHermesColumnarSumPrice beats.
func BenchmarkHermesListRecordsSumPrice(b *testing.B) {
	store, err := NewStore(ProjectionSpec{
		Name:          "ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"symbol", "bucket", "price"},
		MaxRecords:    100000,
	})
	if err != nil {
		b.Fatalf("failed to create store: %v", err)
	}

	ctx := context.Background()
	records := make([]database.DomainRecord, 10000)
	for i := range records {
		records[i] = database.DomainRecord{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       fmt.Sprintf("tick_%06d", i),
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "symbol", Value: database.StringValue("OVS")},
				database.RecordField{Name: "bucket", Value: database.IntValue(int64(i % 16))},
				database.RecordField{Name: "price", Value: database.FloatValue(float64(i) * 1.5)},
			),
		}
	}
	if _, err := store.BulkLoad(ctx, "ticks", records); err != nil {
		b.Fatalf("failed to bulk load: %v", err)
	}

	query := Query{OrganizationID: "org_1"}

	b.ReportAllocs()
	b.ResetTimer()
	var sink float64
	for i := 0; i < b.N; i++ {
		list, err := store.ListRecords(ctx, "ticks", query, Fence{})
		if err != nil {
			b.Fatal(err)
		}
		sum := 0.0
		for _, r := range list {
			if val, ok := r.Data.Get("price"); ok {
				if _, idxVal, ok := val.ScalarIndex(); ok {
					if f, err2 := strconv.ParseFloat(idxVal, 64); err2 == nil {
						sum += f
					}
				}
			}
		}
		sink = sum
	}
	_ = sink
}

// BenchmarkHermesColumnarStringValueAt measures (*StringVector).ValueAt on a
// transient hot scan: escape analysis elides the copy, so no per-element
// allocation, sequential buffer scan.
func BenchmarkHermesColumnarStringValueAt(b *testing.B) {
	store, err := NewStore(ProjectionSpec{
		Name:          "ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"symbol", "bucket", "price"},
		MaxRecords:    100000,
	})
	if err != nil {
		b.Fatalf("failed to create store: %v", err)
	}

	ctx := context.Background()
	records := make([]database.DomainRecord, 10000)
	for i := range records {
		records[i] = database.DomainRecord{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       fmt.Sprintf("tick_%06d", i),
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "symbol", Value: database.StringValue("OVS")},
				database.RecordField{Name: "bucket", Value: database.IntValue(int64(i % 16))},
				database.RecordField{Name: "price", Value: database.FloatValue(float64(i) * 1.5)},
			),
		}
	}
	if _, err := store.BulkLoad(ctx, "ticks", records); err != nil {
		b.Fatalf("failed to bulk load: %v", err)
	}

	query := Query{OrganizationID: "org_1"}
	fields := []string{"record_id"}

	b.ReportAllocs()
	b.ResetTimer()
	var sink int
	for i := 0; i < b.N; i++ {
		batch, err := store.GetColumnarBatch(ctx, "ticks", query, fields, Fence{})
		if err != nil {
			b.Fatal(err)
		}
		sv, ok := batch.Columns[0].Data.(*StringVector)
		if !ok {
			b.Fatal("expected *StringVector")
		}
		n := sv.Len()
		// ValueAt: transient use (len only); escape analysis elides the copy.
		for j := range n {
			sink += len(sv.ValueAt(j))
		}
	}
	_ = sink
}

// BenchmarkHermesColumnarStringValuesSlice is the allocating baseline:
// StringValues() materializes a []string, one allocation for the slice
// plus each string header. Compare against BenchmarkHermesColumnarStringValueAt.
func BenchmarkHermesColumnarStringValuesSlice(b *testing.B) {
	store, err := NewStore(ProjectionSpec{
		Name:          "ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"symbol", "bucket", "price"},
		MaxRecords:    100000,
	})
	if err != nil {
		b.Fatalf("failed to create store: %v", err)
	}

	ctx := context.Background()
	records := make([]database.DomainRecord, 10000)
	for i := range records {
		records[i] = database.DomainRecord{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       fmt.Sprintf("tick_%06d", i),
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "symbol", Value: database.StringValue("OVS")},
				database.RecordField{Name: "bucket", Value: database.IntValue(int64(i % 16))},
				database.RecordField{Name: "price", Value: database.FloatValue(float64(i) * 1.5)},
			),
		}
	}
	if _, err := store.BulkLoad(ctx, "ticks", records); err != nil {
		b.Fatalf("failed to bulk load: %v", err)
	}

	query := Query{OrganizationID: "org_1"}
	fields := []string{"record_id"}

	b.ReportAllocs()
	b.ResetTimer()
	var sink int
	for i := 0; i < b.N; i++ {
		batch, err := store.GetColumnarBatch(ctx, "ticks", query, fields, Fence{})
		if err != nil {
			b.Fatal(err)
		}
		// StringValues() materialises the []string — this is the allocating path.
		vals := batch.Columns[0].Data.StringValues()
		for _, s := range vals {
			sink += len(s)
		}
	}
	_ = sink
}

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

	assertColumnType(t, col, "_record", TypeBinary)
	assertColumnType(t, col, "record_id", TypeString)
	assertColumnType(t, col, "organization_id", TypeString)
	assertColumnType(t, col, "created_at", TypeTimestamp)
	assertColumnType(t, col, "updated_at", TypeTimestamp)
	assertColumnType(t, col, "version", TypeInt64)

	assertColumnType(t, col, "symbol", TypeString)
	assertColumnType(t, col, "qty", TypeInt64)
	assertColumnType(t, col, "price", TypeFloat64)
	assertColumnType(t, col, "active", TypeInt64)
	assertColumnType(t, col, "missing_field", TypeString)

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

func TestGetColumnarBatch_StrictParsingErrors(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name: "signals", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"symbol"}, MaxRecords: 16, MaxBytes: 1 << 20,
	})
	ctx := t.Context()

	rec1 := database.DomainRecord{
		Domain:         "signals",
		Collection:     "ticks",
		OrganizationID: "org_1",
		RecordID:       "tick_1",
		Data: database.RecordData{
			{Name: "qty", Value: database.RecordValue{Kind: database.RecordValueInt, Text: "not-an-int"}},
		},
	}
	_, err := store.Apply(ctx, "signals", Event{
		Operation: OperationUpsert,
		SourceID:  "src_1",
		Version:   1,
		Record:    rec1,
	})
	if err != nil {
		t.Fatalf("Apply() err = %v", err)
	}

	_, err = store.GetColumnarBatch(ctx, "signals", Query{OrganizationID: "org_1"}, []string{"qty"}, Fence{})
	if err == nil {
		t.Fatal("expected error parsing malformed integer, got nil")
	}

	store2 := newTestStore(t, ProjectionSpec{
		Name: "signals2", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"symbol"}, MaxRecords: 16, MaxBytes: 1 << 20,
	})
	rec2 := database.DomainRecord{
		Domain:         "signals",
		Collection:     "ticks",
		OrganizationID: "org_1",
		RecordID:       "tick_2",
		Data: database.RecordData{
			{Name: "price", Value: database.RecordValue{Kind: database.RecordValueFloat, Text: "not-a-float"}},
		},
	}
	_, err = store2.Apply(ctx, "signals2", Event{
		Operation: OperationUpsert,
		SourceID:  "src_2",
		Version:   1,
		Record:    rec2,
	})
	if err != nil {
		t.Fatalf("Apply() err = %v", err)
	}

	_, err = store2.GetColumnarBatch(ctx, "signals2", Query{OrganizationID: "org_1"}, []string{"price"}, Fence{})
	if err == nil {
		t.Fatal("expected error parsing malformed float, got nil")
	}
}
