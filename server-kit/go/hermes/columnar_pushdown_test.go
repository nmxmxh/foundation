package hermes

import (
	"context"
	"testing"
)

func fetchPushdownBatch(tb testing.TB, store *Store, fields []string, predicates []ColumnPredicate, limit int) *RecordBatch {
	tb.Helper()
	batch, err := store.GetColumnarBatchWhere(
		context.Background(), "ticks",
		Query{OrganizationID: "org_1", Limit: limit},
		fields, predicates, Fence{},
	)
	if err != nil {
		tb.Fatalf("GetColumnarBatchWhere: %v", err)
	}
	return batch
}

// TestGetColumnarBatchWherePushdownParity: pushdown must select exactly the
// rows a post-hoc SelectionBitmap over the full batch selects, and the summed
// measure must agree. The bitmap lane is the oracle.
func TestGetColumnarBatchWherePushdownParity(t *testing.T) {
	store := buildSelectFixtureStore(t, 500)

	full := fetchSelectFixtureBatch(t, store, "price", "bucket", "record_id")
	priceSel, err := full.SelectFloat64("price", CompareGt, 150)
	if err != nil {
		t.Fatalf("SelectFloat64: %v", err)
	}
	bucketSel, err := full.SelectInt64("bucket", CompareLe, 7)
	if err != nil {
		t.Fatalf("SelectInt64: %v", err)
	}
	if err := priceSel.And(&bucketSel); err != nil {
		t.Fatalf("And: %v", err)
	}
	idVec, ok := full.Columns[2].Data.(*StringVector)
	if !ok {
		t.Fatal("record_id column is not a StringVector")
	}
	expectedIDs := map[string]bool{}
	var expectedSum float64
	prices := full.Columns[0].Data.Float64Values()
	priceSel.ForEachSelected(func(row int) bool {
		expectedIDs[idVec.ValueAt(row)] = true
		expectedSum += prices[row]
		return true
	})

	pushed := fetchPushdownBatch(t, store, []string{"price", "record_id"}, []ColumnPredicate{
		PredicateFloat64("price", CompareGt, 150),
		PredicateInt64("bucket", CompareLe, 7),
	}, 0)

	if pushed.Rows != len(expectedIDs) {
		t.Fatalf("pushdown rows = %d, bitmap oracle selected %d", pushed.Rows, len(expectedIDs))
	}
	pushedIDs, ok := pushed.Columns[1].Data.(*StringVector)
	if !ok {
		t.Fatal("pushdown record_id column is not a StringVector")
	}
	var pushedSum float64
	for i, v := range pushed.Columns[0].Data.Float64Values() {
		if !expectedIDs[pushedIDs.ValueAt(i)] {
			t.Fatalf("pushdown selected unexpected record %q", pushedIDs.ValueAt(i))
		}
		pushedSum += v
	}
	if pushedSum != expectedSum {
		t.Fatalf("pushdown sum = %v, oracle sum = %v", pushedSum, expectedSum)
	}
}

// TestGetColumnarBatchWhereLimitAfterFilter: the limit must apply to filtered
// rows (WHERE then LIMIT), and every returned row must satisfy the predicate.
func TestGetColumnarBatchWhereLimitAfterFilter(t *testing.T) {
	store := buildSelectFixtureStore(t, 400)

	batch := fetchPushdownBatch(t, store, []string{"price"}, []ColumnPredicate{
		PredicateFloat64("price", CompareGt, 300),
	}, 25)
	if batch.Rows != 25 {
		t.Fatalf("limited pushdown rows = %d, expected 25", batch.Rows)
	}
	vec := batch.Columns[0].Data
	for i, v := range vec.Float64Values() {
		if !vec.IsValid(i) || v <= 300 {
			t.Fatalf("row %d violates predicate: valid=%v value=%v", i, vec.IsValid(i), v)
		}
	}
}

func TestGetColumnarBatchWhereReservedFields(t *testing.T) {
	store := buildSelectFixtureStore(t, 64)

	byID := fetchPushdownBatch(t, store, []string{"record_id"}, []ColumnPredicate{
		PredicateString("record_id", CompareLt, "tick_000010"),
	}, 0)
	if byID.Rows != 10 {
		t.Fatalf("record_id predicate rows = %d, expected 10", byID.Rows)
	}

	byOrg := fetchPushdownBatch(t, store, []string{"record_id"}, []ColumnPredicate{
		PredicateString("organization_id", CompareEq, "org_1"),
	}, 0)
	if byOrg.Rows != 64 {
		t.Fatalf("organization_id eq rows = %d, expected 64", byOrg.Rows)
	}

	byVersion := fetchPushdownBatch(t, store, []string{"version"}, []ColumnPredicate{
		PredicateInt64("version", CompareGe, 1),
	}, 0)
	if byVersion.Rows != 64 {
		t.Fatalf("version ge 1 rows = %d, expected 64", byVersion.Rows)
	}

	// Kind mismatches on reserved fields never match.
	if got := fetchPushdownBatch(t, store, []string{"record_id"}, []ColumnPredicate{
		PredicateInt64("record_id", CompareGe, 0),
	}, 0); got.Rows != 0 {
		t.Fatalf("int predicate on record_id matched %d rows", got.Rows)
	}
	if got := fetchPushdownBatch(t, store, []string{"record_id"}, []ColumnPredicate{
		PredicateString("version", CompareEq, "1"),
	}, 0); got.Rows != 0 {
		t.Fatalf("string predicate on version matched %d rows", got.Rows)
	}
}

func TestGetColumnarBatchWhereNullAndKindSemantics(t *testing.T) {
	store := buildSelectFixtureStore(t, 100)

	// A price predicate never matches records whose price field is absent:
	// fixture omits price for i%5==4, so 20 of 100 rows are null.
	matched := fetchPushdownBatch(t, store, []string{"price"}, []ColumnPredicate{
		PredicateFloat64("price", CompareGe, 0),
	}, 0)
	if matched.Rows != 80 {
		t.Fatalf("null-price rows leaked through: rows = %d, expected 80", matched.Rows)
	}

	// Kind mismatch against a data field: float predicate over string column.
	if got := fetchPushdownBatch(t, store, []string{"symbol"}, []ColumnPredicate{
		PredicateFloat64("symbol", CompareEq, 1),
	}, 0); got.Rows != 0 {
		t.Fatalf("float predicate on string field matched %d rows", got.Rows)
	}
	// Missing field matches nothing.
	if got := fetchPushdownBatch(t, store, []string{"price"}, []ColumnPredicate{
		PredicateString("missing_field", CompareEq, "x"),
	}, 0); got.Rows != 0 {
		t.Fatalf("missing field matched %d rows", got.Rows)
	}
	// Empty predicate slice behaves like the plain batch path.
	all := fetchPushdownBatch(t, store, []string{"price"}, nil, 0)
	if all.Rows != 100 {
		t.Fatalf("empty predicates rows = %d, expected 100", all.Rows)
	}
}

func TestGetColumnarBatchWhereErrors(t *testing.T) {
	store := buildSelectFixtureStore(t, 8)
	ctx := context.Background()

	if _, err := store.GetColumnarBatchWhere(ctx, "ticks", Query{}, []string{"price"}, nil, Fence{}); err == nil {
		t.Fatal("missing organization must error")
	}
	if _, err := store.GetColumnarBatchWhere(ctx, "missing", Query{OrganizationID: "org_1"}, []string{"price"}, nil, Fence{}); err == nil {
		t.Fatal("unknown projection must error")
	}
}

// BenchmarkHermesColumnarPushdownFilterSum: the cold path with predicates
// applied during construction. Compare against
// BenchmarkHermesColumnarBitmapFilterSum (filter after full construction) and
// BenchmarkHermesListRecordsFilterSum (record path): surviving rows (~44%)
// are the only rows sorted and materialized.
func BenchmarkHermesColumnarPushdownFilterSum(b *testing.B) {
	store := buildSelectFixtureStore(b, 10000)
	ctx := context.Background()
	query := Query{OrganizationID: "org_1"}
	fields := []string{"price"}
	predicates := []ColumnPredicate{
		PredicateFloat64("price", CompareGt, 7500),
		PredicateInt64("bucket", CompareLe, 7),
	}

	b.ReportAllocs()
	b.ResetTimer()
	var sink float64
	for i := 0; i < b.N; i++ {
		batch, err := store.GetColumnarBatchWhere(ctx, "ticks", query, fields, predicates, Fence{})
		if err != nil {
			b.Fatal(err)
		}
		sum := 0.0
		for _, v := range batch.Columns[0].Data.Float64Values() {
			sum += v
		}
		sink = sum
	}
	_ = sink
}
