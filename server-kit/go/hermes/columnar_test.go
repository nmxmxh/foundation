package hermes

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
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

	for b.Loop() {
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

	for b.Loop() {
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

	var sink float64
	for b.Loop() {
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

	var sink float64
	for b.Loop() {
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

	var sink int
	for b.Loop() {
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

	var sink int
	for b.Loop() {
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

func BenchmarkHermesColumnarAssembly(b *testing.B) {
	for _, rows := range []int{128, 10000} {
		store := buildSelectFixtureStore(b, rows)
		for _, mode := range []string{"full", "limit50", "predicate_limit50"} {
			b.Run(fmt.Sprintf("rows%d/%s", rows, mode), func(b *testing.B) {
				query := Query{OrganizationID: "org_1"}
				if mode != "full" {
					query.Limit = 50
				}
				fields := []string{"price", "bucket"}
				predicates := []ColumnPredicate{PredicateFloat64("price", CompareGe, float64(rows)*1.125)}
				b.ReportAllocs()
				for b.Loop() {
					var batch *RecordBatch
					var err error
					if mode == "predicate_limit50" {
						batch, err = store.GetColumnarBatchWhere(context.Background(), "ticks", query, fields, predicates, Fence{})
					} else {
						batch, err = store.GetColumnarBatch(context.Background(), "ticks", query, fields, Fence{})
					}
					if err != nil || batch.Rows == 0 {
						b.Fatalf("assembly failed: %v", err)
					}
				}
			})
		}
	}
}

func TestColumnarLimitUsesCanonicalOrder(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{Name: "ticks", Domain: "signals", Collection: "ticks", IndexedFields: []string{"group", "side"}})
	base := time.Unix(1700000000, 0).UTC()
	records := make([]database.DomainRecord, 8)
	for i := range records {
		records[i] = database.DomainRecord{
			Domain: "signals", Collection: "ticks", OrganizationID: "org_1",
			RecordID: fmt.Sprintf("tick_%d", i), UpdatedAt: base.Add(-time.Duration(i) * time.Minute),
			Data: database.RecordDataFromPairs(database.RecordField{Name: "group", Value: database.StringValue("same")},
				database.RecordField{Name: "side", Value: database.StringValue("same")}),
		}
	}
	if _, err := store.BulkLoad(t.Context(), "ticks", records); err != nil {
		t.Fatal(err)
	}
	full, err := store.GetColumnarBatch(t.Context(), "ticks", Query{OrganizationID: "org_1"}, []string{"record_id"}, Fence{})
	if err != nil {
		t.Fatal(err)
	}
	want := full.Columns[0].Data.StringValues()[:3]
	group, _ := NewQueryFilter("group", "same")
	side, _ := NewQueryFilter("side", "same")
	for _, query := range []Query{QueryWithFilters("org_1", 3), QueryWithFilters("org_1", 3, group), QueryWithFilters("org_1", 3, group, side)} {
		limited, err := store.GetColumnarBatch(t.Context(), "ticks", query, []string{"record_id"}, Fence{})
		if err != nil {
			t.Fatal(err)
		}
		if got := limited.Columns[0].Data.StringValues(); !slices.Equal(got, want) {
			t.Fatalf("limited order = %v, want %v", got, want)
		}
	}
}

func BenchmarkHermesColumnarUnorderedLimit50(b *testing.B) {
	for _, rows := range []int{128, 10000} {
		b.Run(fmt.Sprintf("rows%d", rows), func(b *testing.B) {
			store := buildSelectFixtureStore(b, rows)
			_, err := store.Apply(b.Context(), "ticks", Event{
				Operation: OperationPatch, SourceID: "out-of-order", Version: uint64(rows + 1),
				Record: database.DomainRecord{Domain: "signals", Collection: "ticks", OrganizationID: "org_1",
					RecordID: "tick_000000", UpdatedAt: time.Unix(1, 0)},
			})
			if err != nil {
				b.Fatal(err)
			}
			part, err := store.partition("ticks")
			if err != nil || !part.activeRegistry().columnarUnordered.Load() {
				b.Fatal("fixture must invalidate the publication order proof")
			}
			query := Query{OrganizationID: "org_1", Limit: 50}
			fields := []string{"price", "bucket"}
			b.ReportAllocs()
			for b.Loop() {
				batch, err := store.GetColumnarBatch(b.Context(), "ticks", query, fields, Fence{})
				if err != nil || batch.Rows != 50 {
					b.Fatalf("bounded selection failed: %v", err)
				}
			}
		})
	}
}

func TestNewQueryFilterValidation(t *testing.T) {
	if _, ok := NewQueryFilter("symbol", "OVS"); !ok {
		t.Fatal("string filter should be accepted")
	}
	if _, ok := NewQueryFilter("  ", "OVS"); ok {
		t.Fatal("blank field must be rejected")
	}
	if _, ok := NewQueryFilter("x", []string{"not", "scalar"}); ok {
		t.Fatal("non-scalar value must be rejected")
	}
}

func TestQueryFilterValueRoundTrip(t *testing.T) {
	cases := map[string]any{
		"string": "OVS",
		"int":    int64(-42),
		"uint":   uint64(42),
		"bool":   true,
		"float":  3.5,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			filter, ok := NewQueryFilter("field", in)
			if !ok {
				t.Fatalf("NewQueryFilter(%v) not ok", in)
			}
			got := queryFilterValue(filter)
			want, ok := database.RecordValueFromAny(in)
			if !ok {
				t.Fatalf("RecordValueFromAny(%v) not ok", in)
			}
			gk, gv, _ := got.ScalarIndex()
			wk, wv, _ := want.ScalarIndex()
			if gk != wk || gv != wv {
				t.Fatalf("round trip drift: got (%c,%q) want (%c,%q)", gk, gv, wk, wv)
			}
		})
	}

	for _, kind := range []byte{'i', 'u', 'f'} {
		v := queryFilterValue(QueryFilter{Field: "f", Kind: kind, Value: "not-a-number"})
		if v.Kind != database.RecordValueString {
			t.Fatalf("malformed %c value = kind %v, want string fallback", kind, v.Kind)
		}
	}
}

func TestQueryWithFiltersPlanShape(t *testing.T) {
	sym, _ := NewQueryFilter("symbol", "OVS")
	region, _ := NewQueryFilter("region", "us")
	blank := QueryFilter{Field: "  ", Kind: 's', Value: "x"}

	if q := QueryWithFilters("org_1", 10); q.Plan.count != 0 {
		t.Fatalf("zero filters -> count %d, want 0", q.Plan.count)
	}
	if q := QueryWithFilters("org_1", 10, sym); q.Plan.count != 1 || q.Plan.first.Field != "symbol" {
		t.Fatalf("one filter plan = %+v", q.Plan)
	}

	q := QueryWithFilters("org_1", 10, sym, region, blank)
	if q.Plan.count != 2 {
		t.Fatalf("two valid filters (one blank dropped) -> count %d, want 2", q.Plan.count)
	}
	if q.Plan.filters[0].Field != "region" || q.Plan.filters[1].Field != "symbol" {
		t.Fatalf("filters not sorted by field: %+v", q.Plan.filters)
	}

	rf := q.Plan.RecordFilters()
	if len(rf) != 2 {
		t.Fatalf("RecordFilters len = %d, want 2", len(rf))
	}
}

func TestQueryFromRecordQuery(t *testing.T) {

	single := database.RecordQuery{Limit: 5, Filters: []database.RecordFilter{
		{Field: "symbol", Value: database.StringValue("OVS")},
	}}
	if q := QueryFromRecordQuery("org_1", single); q.Plan.count != 1 || q.Limit != 5 {
		t.Fatalf("single = %+v", q)
	}

	blank := database.RecordQuery{Limit: 1, Filters: []database.RecordFilter{
		{Field: "  ", Value: database.StringValue("x")},
	}}
	if q := QueryFromRecordQuery("org_1", blank); q.Plan.count != 0 {
		t.Fatalf("blank single -> count %d, want 0", q.Plan.count)
	}

	many := database.RecordQuery{Limit: 9, Filters: []database.RecordFilter{
		{Field: "symbol", Value: database.StringValue("OVS")},
		{Field: "", Value: database.StringValue("skip")},
		{Field: "region", Value: database.StringValue("us")},
	}}
	if q := QueryFromRecordQuery("org_1", many); q.Plan.count != 2 {
		t.Fatalf("many -> count %d, want 2", q.Plan.count)
	}
}

func TestRecordMatchesPlannedFilters(t *testing.T) {
	spec := driftSpec()
	rec := testRecord("signals", "ticks", "org_1", "tick_1", map[string]any{"symbol": "OVS"})

	if !recordMatches(rec, spec, Query{OrganizationID: "org_1"}) {
		t.Fatal("record should match its own tenant with no filters")
	}

	if recordMatches(rec, spec, Query{OrganizationID: "org_other"}) {
		t.Fatal("record must not match a different tenant")
	}

	match := QueryWithFilters("org_1", 0, mustFilter(t, "symbol", "OVS"))
	if !recordMatches(rec, spec, match) {
		t.Fatal("record should match an equal planned filter")
	}

	noMatch := QueryWithFilters("org_1", 0, mustFilter(t, "symbol", "NOPE"))
	if recordMatches(rec, spec, noMatch) {
		t.Fatal("record must not match a differing planned filter")
	}
}

// TestFastDecimalFloatMatchesStrconvExactly pins the fast path to strconv's
// answer, bit for bit. strconv.ParseFloat returns the correctly rounded value;
// the fast path reaches it through one exact division, and this test is what
// proves the two agree rather than merely being close. A column built from a
// different value than a row read would compare unequal.
func TestFastDecimalFloatMatchesStrconvExactly(t *testing.T) {
	fixed := []string{
		"", " ", ".", "-", "+", "-.", "+.", "0", "-0", "+0", "0.0", "-0.0",
		"1", "-1", "1.", "-1.", ".5", "-.5", "+.5", "1.5", "3.14159",
		"1e5", "1E5", "1e-5", "1.5e3", "0x1p-2", "Inf", "-Inf", "NaN", "nan",
		"Infinity", "0x1.8p1", "00001", "00001.000", "1_000", "1,5", "1.2.3",
		" 1.5", "1.5 ", "1.5\n", "١٢٣", "123456789012345", "1234567890123456",
		"12345678901234567890", "0.000000000000001", "1e", "e5", "--1", "1-",
		"9007199254740993", "-9007199254740993", "1.7976931348623157e308",
		"4.9406564584124654e-324", "18446744073709551616", "0.30000000000000004",
	}
	for s := 1; s <= 40; s++ {
		fixed = append(fixed, strconv.Itoa(s), "-"+strconv.Itoa(s), strconv.Itoa(s)+".5")
	}

	t.Run("handles the cases it claims", func(t *testing.T) {
		for _, s := range fixed {
			want, wantErr := strconv.ParseFloat(s, 64)
			got, err := parseColumnarFloat(s)
			if (err != nil) != (wantErr != nil) {
				t.Fatalf("parseColumnarFloat(%q) error = %v, strconv error = %v", s, err, wantErr)
			}
			if wantErr != nil {
				continue
			}
			if math.Float64bits(got) != math.Float64bits(want) {
				t.Fatalf("parseColumnarFloat(%q) = %v (%#x), strconv = %v (%#x)",
					s, got, math.Float64bits(got), want, math.Float64bits(want))
			}
		}
	})

	t.Run("agrees bit for bit on generated decimals", func(t *testing.T) {
		// Exercise carries, trailing zeros, and boundary digit counts, which is
		// where a hand-rolled mantissa/scale split would drift from correct
		// rounding if it were not exact.
		for digits := 1; digits <= 17; digits++ {
			for _, lead := range []string{"1", "9", "1234567890", "999999999999999", "100000000000000"} {
				if len(lead) < digits {
					continue
				}
				mantissa := lead[:digits]
				for _, scale := range []int{0, 1, 2, 5, 9, digits, digits - 1} {
					if scale < 0 {
						continue
					}
					var b strings.Builder
					if digits > scale {
						b.WriteString(mantissa[:digits-scale])
						b.WriteByte('.')
						b.WriteString(mantissa[digits-scale:])
					} else {
						b.WriteString("0.")
						b.WriteString(strings.Repeat("0", scale-digits))
						b.WriteString(mantissa)
					}
					for _, candidate := range []string{b.String(), "-" + b.String()} {
						want, wantErr := strconv.ParseFloat(candidate, 64)
						if wantErr != nil {
							continue
						}
						got, err := parseColumnarFloat(candidate)
						if err != nil {
							t.Fatalf("parseColumnarFloat(%q) unexpected error %v", candidate, err)
						}
						if math.Float64bits(got) != math.Float64bits(want) {
							t.Fatalf("parseColumnarFloat(%q) = %v (%#x), strconv = %v (%#x)",
								candidate, got, math.Float64bits(got), want, math.Float64bits(want))
						}
					}
				}
			}
		}
	})

	t.Run("claims the common shapes it is there to speed up", func(t *testing.T) {
		// Without this the two subtests above could both pass while
		// fastDecimalFloat deferred every input and the fast path was dead code.
		for _, s := range []string{"0", "-0.0", "1", "1.5", "-2.25", "3.14159", "0.1", "12345.6789", "-0.0009765625"} {
			if _, ok := fastDecimalFloat(s); !ok {
				t.Fatalf("fastDecimalFloat declined %q, want the fast path", s)
			}
		}
	})

	t.Run("deferrs shapes it must not claim", func(t *testing.T) {
		for _, s := range []string{"1e5", "0x1p-2", "Inf", "NaN", "1_000", " 1.5", "1.5 ", "1e-300", "1.7976931348623157e308"} {
			if _, ok := fastDecimalFloat(s); ok {
				t.Fatalf("fastDecimalFloat claimed %q, want deferral to strconv", s)
			}
		}
	})
}

// TestColumnarFloatColumnIsBitIdenticalToStrconv guards the caller: a float
// column built through the fast parser must equal one built through strconv,
// element for element.
func TestColumnarFloatColumnIsBitIdenticalToStrconv(t *testing.T) {
	values := []string{"0", "-0.0", "1", "1.5", "-2.25", "3.14159", "0.1", "1e-300", "1.7976931348623157e308", "1_000"}
	entries := make([]*recordEntry, 0, len(values))
	for i, text := range values {
		entries = append(entries, &recordEntry{
			version: uint64(i),
			record: database.DomainRecord{
				Domain: "signals", Collection: "ticks", OrganizationID: "org_1",
				RecordID: "rec_" + strconv.Itoa(i),
				Data:     database.RecordData{{Name: "price", Value: database.RecordValue{Kind: database.RecordValueFloat, Text: text}}},
			},
		})
	}

	vec, err := buildDataFieldVector("price", entries, len(entries))
	if err != nil {
		t.Fatalf("buildDataFieldVector: %v", err)
	}
	fv, ok := vec.(*Float64Vector)
	if !ok {
		t.Fatalf("vector type = %T, want *Float64Vector", vec)
	}
	for i, text := range values {
		want, wantErr := strconv.ParseFloat(text, 64)
		if wantErr != nil {
			if fv.validity.get(i) {
				t.Fatalf("row %d (%q) marked valid, want invalid", i, text)
			}
			continue
		}
		if !fv.validity.get(i) {
			t.Fatalf("row %d (%q) marked invalid, want valid", i, text)
		}
		if math.Float64bits(fv.values[i]) != math.Float64bits(want) {
			t.Fatalf("row %d (%q) = %v (%#x), want %v (%#x)",
				i, text, fv.values[i], math.Float64bits(fv.values[i]), want, math.Float64bits(want))
		}
	}
}

// TestBitmapGrowPreservesShapeUnderIncrementalRaise guards the invariant the
// geometric growth relies on. Consumers and the shape assertions all key off
// len(words), never cap, and the word kernels reslice src to len(dst), so the
// required invariant is len(words) == (n+63)/64 after every raise.
//
// It exercises the pattern that motivated the change: one bit raised at a time
// into a fresh bitmap, which is what the inverted index does while a scope
// fills. Under exact sizing that re-copied the whole slice per record.
func TestBitmapGrowPreservesShapeUnderIncrementalRaise(t *testing.T) {
	for _, final := range []int{1, 63, 64, 65, 127, 128, 129, 1000, 4096, 5000} {
		b := newBitmap(0)
		for i := range final {
			b.set(i)
			wantWords := (b.n + 63) / 64
			if len(b.words) != wantWords {
				t.Fatalf("final=%d after set(%d): len(words) = %d, want %d", final, i, len(b.words), wantWords)
			}
			if b.n < i+1 {
				t.Fatalf("final=%d after set(%d): n = %d, want at least %d", final, i, b.n, i+1)
			}
		}
		// Every raised bit reads back, and nothing outside the range reads set.
		for i := range final {
			if !b.get(i) {
				t.Fatalf("final=%d: bit %d did not survive grow", final, i)
			}
		}
		for i := final; i < final+130; i++ {
			if b.get(i) {
				t.Fatalf("final=%d: bit %d past the end read set", final, i)
			}
		}
	}
}

// TestBitmapGrowSpareCapacityIsInvisible pins that reserving capacity does not
// change what a clone, a count, or a merge observes. A clone must not inherit
// the spare capacity, or the next grow would expose stale headroom.
func TestBitmapGrowSpareCapacityIsInvisible(t *testing.T) {
	b := newBitmap(0)
	for i := range 5000 {
		b.set(i)
	}
	clone := b.clone()
	if len(clone.words) != len(b.words) {
		t.Fatalf("clone len(words) = %d, want %d", len(clone.words), len(b.words))
	}
	if clone.count() != b.count() {
		t.Fatalf("clone count = %d, want %d", clone.count(), b.count())
	}
	if clone.count() != 5000 {
		t.Fatalf("count = %d, want 5000", clone.count())
	}
	for i := range 5000 {
		if !clone.get(i) {
			t.Fatalf("clone lost bit %d", i)
		}
	}

	// A clone has no spare capacity, so growing it must still allocate cleanly.
	clone.set(9000)
	if len(clone.words) != (clone.n+63)/64 {
		t.Fatalf("grown clone len(words) = %d, want %d", len(clone.words), (clone.n+63)/64)
	}
	if clone.count() != 5001 {
		t.Fatalf("grown clone count = %d, want 5001 (5000 plus the newly set bit)", clone.count())
	}
	// Growing a clone past its headroom must not resurrect stale words.
	other := b.clone()
	other.set(9000)
	other.grow(9001)
	if other.count() != 5001 {
		t.Fatalf("count after grow on a clone = %d, want 5001", other.count())
	}
}
