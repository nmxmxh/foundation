package hermes

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

// buildSelectFixtureStore loads n tick records: price = i*1.5 (records where
// i%5 == 4 omit price entirely, so the price column carries real nulls),
// bucket = i%16, symbol alternates OVS/ALT.
func buildSelectFixtureStore(tb testing.TB, n int) *Store {
	tb.Helper()
	store, err := NewStore(ProjectionSpec{
		Name:          "ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"symbol", "bucket", "price"},
		MaxRecords:    100000,
	})
	if err != nil {
		tb.Fatalf("failed to create store: %v", err)
	}

	records := make([]database.DomainRecord, n)
	for i := range records {
		symbol := "OVS"
		if i%2 == 1 {
			symbol = "ALT"
		}
		fields := []database.RecordField{
			{Name: "symbol", Value: database.StringValue(symbol)},
			{Name: "bucket", Value: database.IntValue(int64(i % 16))},
		}
		if i%5 != 4 {
			fields = append(fields, database.RecordField{Name: "price", Value: database.FloatValue(float64(i) * 1.5)})
		}
		records[i] = database.DomainRecord{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       fmt.Sprintf("tick_%06d", i),
			Data:           database.RecordDataFromPairs(fields...),
		}
	}
	if _, err := store.BulkLoad(context.Background(), "ticks", records); err != nil {
		tb.Fatalf("failed to bulk load: %v", err)
	}
	return store
}

func fetchSelectFixtureBatch(tb testing.TB, store *Store, fields ...string) *RecordBatch {
	tb.Helper()
	batch, err := store.GetColumnarBatch(
		context.Background(), "ticks", Query{OrganizationID: "org_1"}, fields, Fence{},
	)
	if err != nil {
		tb.Fatalf("failed to get columnar batch: %v", err)
	}
	return batch
}

// bruteForceExpected recomputes the selection through the record batch row by
// row, giving the oracle the bitmap lane must agree with.
func bruteForceExpected(batch *RecordBatch, match func(row int) bool) map[int]bool {
	expected := map[int]bool{}
	for row := 0; row < batch.Rows; row++ {
		if match(row) {
			expected[row] = true
		}
	}
	return expected
}

func TestSelectionBitmapPredicatesAndMerges(t *testing.T) {
	store := buildSelectFixtureStore(t, 200)
	batch := fetchSelectFixtureBatch(t, store, "price", "bucket", "symbol")

	priceVec := batch.Columns[0].Data
	bucketValues := batch.Columns[1].Data.Int64Values()
	symbolVec, ok := batch.Columns[2].Data.(*StringVector)
	if !ok {
		t.Fatalf("symbol column is not a StringVector")
	}

	priceSel, err := batch.SelectFloat64("price", CompareGt, 150)
	if err != nil {
		t.Fatalf("SelectFloat64: %v", err)
	}
	bucketSel, err := batch.SelectInt64("bucket", CompareLe, 7)
	if err != nil {
		t.Fatalf("SelectInt64: %v", err)
	}
	symbolSel, err := batch.SelectString("symbol", CompareEq, "OVS")
	if err != nil {
		t.Fatalf("SelectString: %v", err)
	}

	priceValues := priceVec.Float64Values()
	expectPrice := bruteForceExpected(batch, func(row int) bool {
		return priceVec.IsValid(row) && priceValues[row] > 150
	})
	expectBucket := bruteForceExpected(batch, func(row int) bool {
		return bucketValues[row] <= 7
	})
	expectSymbol := bruteForceExpected(batch, func(row int) bool {
		return symbolVec.IsValid(row) && symbolVec.ValueAt(row) == "OVS"
	})

	assertSelection(t, "price>150", &priceSel, expectPrice)
	assertSelection(t, "bucket<=7", &bucketSel, expectBucket)
	assertSelection(t, "symbol==OVS", &symbolSel, expectSymbol)

	// AND merge.
	combined := clone(priceSel)
	if err := combined.And(&bucketSel); err != nil {
		t.Fatalf("And: %v", err)
	}
	expectAnd := map[int]bool{}
	for row := range expectPrice {
		if expectBucket[row] {
			expectAnd[row] = true
		}
	}
	assertSelection(t, "price AND bucket", &combined, expectAnd)

	// OR merge.
	union := clone(priceSel)
	if err := union.Or(&symbolSel); err != nil {
		t.Fatalf("Or: %v", err)
	}
	expectOr := map[int]bool{}
	for row := range expectPrice {
		expectOr[row] = true
	}
	for row := range expectSymbol {
		expectOr[row] = true
	}
	assertSelection(t, "price OR symbol", &union, expectOr)

	// AND NOT merge.
	remainder := clone(priceSel)
	if err := remainder.AndNot(&symbolSel); err != nil {
		t.Fatalf("AndNot: %v", err)
	}
	expectAndNot := map[int]bool{}
	for row := range expectPrice {
		if !expectSymbol[row] {
			expectAndNot[row] = true
		}
	}
	assertSelection(t, "price AND NOT symbol", &remainder, expectAndNot)

	// Masked reduction agrees with a manual sum over the AND selection.
	sum, err := batch.SumFloat64Selected("price", &combined)
	if err != nil {
		t.Fatalf("SumFloat64Selected: %v", err)
	}
	var expectSum float64
	for row := range expectAnd {
		expectSum += priceValues[row]
	}
	if sum != expectSum {
		t.Fatalf("selected sum = %v, expected %v", sum, expectSum)
	}
}

func assertSelection(t *testing.T, label string, sel *SelectionBitmap, expected map[int]bool) {
	t.Helper()
	if sel.Count() != len(expected) {
		t.Fatalf("%s: Count = %d, expected %d", label, sel.Count(), len(expected))
	}
	seen := map[int]bool{}
	sel.ForEachSelected(func(row int) bool {
		seen[row] = true
		return true
	})
	if len(seen) != len(expected) {
		t.Fatalf("%s: iterated %d rows, expected %d", label, len(seen), len(expected))
	}
	for row := range expected {
		if !seen[row] {
			t.Fatalf("%s: expected row %d selected", label, row)
		}
		if !sel.IsSelected(row) {
			t.Fatalf("%s: IsSelected(%d) = false", label, row)
		}
	}
}

func clone(sel SelectionBitmap) SelectionBitmap {
	words := make([]uint64, len(sel.words))
	copy(words, sel.words)
	return SelectionBitmap{words: words, n: sel.n}
}

func TestSelectionBitmapNullsNeverMatch(t *testing.T) {
	store := buildSelectFixtureStore(t, 50)
	batch := fetchSelectFixtureBatch(t, store, "price")
	priceVec := batch.Columns[0].Data
	if priceVec.NullCount() == 0 {
		t.Fatal("fixture must contain null prices")
	}

	// The null cells hold 0 in the value buffer; Ne 999999 and Le 999999 would
	// both match a raw zero, so the validity mask is what excludes them.
	for _, op := range []CompareOp{CompareNe, CompareLe} {
		sel, err := batch.SelectFloat64("price", op, 999999)
		if err != nil {
			t.Fatalf("SelectFloat64(%s): %v", op, err)
		}
		sel.ForEachSelected(func(row int) bool {
			if !priceVec.IsValid(row) {
				t.Fatalf("op %s selected null row %d", op, row)
			}
			return true
		})
		if sel.Count() != batch.Rows-priceVec.NullCount() {
			t.Fatalf("op %s selected %d rows, expected %d valid rows", op, sel.Count(), batch.Rows-priceVec.NullCount())
		}
	}
}

func TestSelectionBitmapCompareOps(t *testing.T) {
	store := buildSelectFixtureStore(t, 64)
	batch := fetchSelectFixtureBatch(t, store, "bucket")
	values := batch.Columns[0].Data.Int64Values()

	cases := []struct {
		op    CompareOp
		match func(int64) bool
	}{
		{CompareEq, func(v int64) bool { return v == 7 }},
		{CompareNe, func(v int64) bool { return v != 7 }},
		{CompareLt, func(v int64) bool { return v < 7 }},
		{CompareLe, func(v int64) bool { return v <= 7 }},
		{CompareGt, func(v int64) bool { return v > 7 }},
		{CompareGe, func(v int64) bool { return v >= 7 }},
	}
	for _, tc := range cases {
		sel, err := batch.SelectInt64("bucket", tc.op, 7)
		if err != nil {
			t.Fatalf("SelectInt64(%s): %v", tc.op, err)
		}
		expected := 0
		for _, v := range values {
			if tc.match(v) {
				expected++
			}
		}
		if sel.Count() != expected {
			t.Fatalf("op %s: Count = %d, expected %d", tc.op, sel.Count(), expected)
		}
	}

	if CompareOp(99).String() != "unknown" {
		t.Fatalf("unexpected CompareOp string: %s", CompareOp(99))
	}
	for _, op := range []CompareOp{CompareEq, CompareNe, CompareLt, CompareLe, CompareGt, CompareGe} {
		if op.String() == "unknown" {
			t.Fatalf("op %d must have a name", op)
		}
	}
	if compareMatches(CompareOp(99), int64(1), int64(1)) {
		t.Fatal("unknown op must never match")
	}
}

func TestSelectionBitmapTailWordHygiene(t *testing.T) {
	for _, n := range []int{1, 63, 64, 65} {
		sel := NewSelectionBitmap(n)
		sel.Not() // select everything; tail bits must stay clear
		if sel.Count() != n {
			t.Fatalf("n=%d: Not selected %d rows", n, sel.Count())
		}
		max := -1
		sel.ForEachSelected(func(row int) bool {
			max = row
			return true
		})
		if max != n-1 {
			t.Fatalf("n=%d: highest selected row = %d", n, max)
		}
		if sel.IsSelected(n) || sel.IsSelected(-1) {
			t.Fatalf("n=%d: out-of-range rows selected", n)
		}
		sel.Not()
		if sel.Count() != 0 {
			t.Fatalf("n=%d: double Not left %d selected", n, sel.Count())
		}
	}

	empty := NewSelectionBitmap(-1)
	if empty.Len() != 0 || empty.Count() != 0 {
		t.Fatalf("negative-length bitmap must normalize to empty")
	}
	empty.Not()
	if empty.Count() != 0 {
		t.Fatal("Not on empty bitmap must stay empty")
	}
}

func TestSelectionBitmapErrors(t *testing.T) {
	store := buildSelectFixtureStore(t, 32)
	batch := fetchSelectFixtureBatch(t, store, "price", "symbol")

	if _, err := batch.SelectFloat64("missing", CompareEq, 1); err == nil {
		t.Fatal("unknown column must error")
	}
	if _, err := batch.SelectFloat64("symbol", CompareEq, 1); err == nil {
		t.Fatal("float predicate over string column must error")
	}
	if _, err := batch.SelectInt64("price", CompareEq, 1); err == nil {
		t.Fatal("int predicate over float column must error")
	}
	if _, err := batch.SelectString("price", CompareEq, "x"); err == nil {
		t.Fatal("string predicate over float column must error")
	}
	if _, err := batch.SumFloat64Selected("missing", &SelectionBitmap{}); err == nil {
		t.Fatal("sum over unknown column must error")
	}
	if _, err := batch.SumFloat64Selected("symbol", &SelectionBitmap{}); err == nil {
		t.Fatal("sum over string column must error")
	}
	short := NewSelectionBitmap(batch.Rows - 1)
	if _, err := batch.SumFloat64Selected("price", &short); err == nil {
		t.Fatal("sum with mismatched selection length must error")
	}

	a := NewSelectionBitmap(10)
	c := NewSelectionBitmap(11)
	if err := a.And(&c); err == nil {
		t.Fatal("And with mismatched lengths must error")
	}
	if err := a.Or(&c); err == nil {
		t.Fatal("Or with mismatched lengths must error")
	}
	if err := a.AndNot(&c); err == nil {
		t.Fatal("AndNot with mismatched lengths must error")
	}
}

func TestForEachSelectedEarlyStop(t *testing.T) {
	sel := NewSelectionBitmap(130)
	sel.Not()
	visited := 0
	sel.ForEachSelected(func(int) bool {
		visited++
		return visited < 70 // stop mid-second-word
	})
	if visited != 70 {
		t.Fatalf("visited %d rows, expected early stop at 70", visited)
	}
}

// ---------------------------------------------------------------------------
// Benchmarks: the Ambit-style claim under test is that two predicates plus a
// bulk AND merge plus a masked reduction beat the equivalent per-record
// pointer-chase filter, and that the bitmap lane's bytes touched are
// deterministic and small.
// ---------------------------------------------------------------------------

// BenchmarkHermesColumnarBitmapFilterSum: fetch batch, build two predicate
// bitmaps (one float column scan + one int column scan), AND-merge them
// word-at-a-time, then sum prices over surviving rows only.
func BenchmarkHermesColumnarBitmapFilterSum(b *testing.B) {
	store := buildSelectFixtureStore(b, 10000)
	ctx := context.Background()
	query := Query{OrganizationID: "org_1"}
	fields := []string{"price", "bucket"}

	// Deterministic bytes-touched budget for one iteration of the bitmap lane:
	// two full column scans + three bitmaps' words + selected value reads.
	// (The ListRecords baseline has no deterministic equivalent: its per-record
	// pointer graph is the cost the columnar layout removes.)
	batch, err := store.GetColumnarBatch(ctx, "ticks", query, fields, Fence{})
	if err != nil {
		b.Fatal(err)
	}
	priceSel, err := batch.SelectFloat64("price", CompareGt, 7500)
	if err != nil {
		b.Fatal(err)
	}
	bucketSel, err := batch.SelectInt64("bucket", CompareLe, 7)
	if err != nil {
		b.Fatal(err)
	}
	merged := clone(priceSel)
	if err := merged.And(&bucketSel); err != nil {
		b.Fatal(err)
	}
	wordBytes := int64(len(priceSel.words)+len(bucketSel.words)+len(merged.words)) * 8
	bytesTouched := int64(batch.Rows)*8*2 + wordBytes + int64(merged.Count())*8

	b.ReportAllocs()
	b.ResetTimer()
	var sink float64
	for i := 0; i < b.N; i++ {
		batch, err := store.GetColumnarBatch(ctx, "ticks", query, fields, Fence{})
		if err != nil {
			b.Fatal(err)
		}
		priceSel, err := batch.SelectFloat64("price", CompareGt, 7500)
		if err != nil {
			b.Fatal(err)
		}
		bucketSel, err := batch.SelectInt64("bucket", CompareLe, 7)
		if err != nil {
			b.Fatal(err)
		}
		if err := priceSel.And(&bucketSel); err != nil {
			b.Fatal(err)
		}
		sum, err := batch.SumFloat64Selected("price", &priceSel)
		if err != nil {
			b.Fatal(err)
		}
		sink = sum
	}
	b.ReportMetric(float64(bytesTouched), "bytes_touched/op")
	_ = sink
}

// BenchmarkHermesListRecordsFilterSum is the equivalent record-path filter:
// list copied records, then per record chase into RecordData for both fields,
// parse, and apply the same two predicates.
func BenchmarkHermesListRecordsFilterSum(b *testing.B) {
	store := buildSelectFixtureStore(b, 10000)
	ctx := context.Background()
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
			priceVal, ok := r.Data.Get("price")
			if !ok {
				continue
			}
			_, priceIdx, ok := priceVal.ScalarIndex()
			if !ok {
				continue
			}
			price, err2 := strconv.ParseFloat(priceIdx, 64)
			if err2 != nil || price <= 7500 {
				continue
			}
			bucketVal, ok := r.Data.Get("bucket")
			if !ok {
				continue
			}
			_, bucketIdx, ok := bucketVal.ScalarIndex()
			if !ok {
				continue
			}
			bucket, err2 := strconv.ParseInt(bucketIdx, 10, 64)
			if err2 != nil || bucket > 7 {
				continue
			}
			sum += price
		}
		sink = sum
	}
	_ = sink
}

// BenchmarkHermesSelectionBitmapMerge10K isolates the merge itself: one AND
// plus one POPCNT count over 10K-row bitmaps — the word-at-a-time work that
// replaces per-row boolean logic.
func BenchmarkHermesSelectionBitmapMerge10K(b *testing.B) {
	left := NewSelectionBitmap(10000)
	right := NewSelectionBitmap(10000)
	for i := 0; i < 10000; i += 2 {
		left.words[i>>6] |= 1 << uint(i&63)
	}
	for i := 0; i < 10000; i += 3 {
		right.words[i>>6] |= 1 << uint(i&63)
	}

	b.ReportAllocs()
	b.ResetTimer()
	var sink int
	for i := 0; i < b.N; i++ {
		merged := clone(left)
		if err := merged.And(&right); err != nil {
			b.Fatal(err)
		}
		sink = merged.Count()
	}
	b.ReportMetric(float64(len(left.words)*8*3), "bytes_touched/op")
	_ = sink
}

// BenchmarkHermesColumnarBitmapFilterCachedBatch isolates the new lane itself:
// the batch is fetched once (the dashboard/analytics reuse pattern) and each
// iteration pays only predicate scans + AND merge + masked sum. This is the
// per-filter cost once a projection batch is resident.
func BenchmarkHermesColumnarBitmapFilterCachedBatch(b *testing.B) {
	store := buildSelectFixtureStore(b, 10000)
	batch := fetchSelectFixtureBatch(b, store, "price", "bucket")

	b.ReportAllocs()
	b.ResetTimer()
	var sink float64
	for i := 0; i < b.N; i++ {
		priceSel, err := batch.SelectFloat64("price", CompareGt, 7500)
		if err != nil {
			b.Fatal(err)
		}
		bucketSel, err := batch.SelectInt64("bucket", CompareLe, 7)
		if err != nil {
			b.Fatal(err)
		}
		if err := priceSel.And(&bucketSel); err != nil {
			b.Fatal(err)
		}
		sum, err := batch.SumFloat64Selected("price", &priceSel)
		if err != nil {
			b.Fatal(err)
		}
		sink = sum
	}
	_ = sink
}
