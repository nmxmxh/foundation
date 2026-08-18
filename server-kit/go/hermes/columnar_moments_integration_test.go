package hermes

import (
	"context"
	"math"
	"testing"
)

// Reachability tests for the moment monoid through the public consumption path.
//
// The unit tests in columnar_moments_test.go build vectors with an unexported
// constructor. That proves the arithmetic and proves nothing about delivery: a
// generated application never constructs a vector, it calls
// GetColumnarBatchWhere and reads Column.Data. server-kit is vendored into those
// applications and its ownership row says app services consume it "through
// public APIs", so the public API is the product and an unreachable primitive is
// an undelivered one.
//
// These tests therefore start where an application starts.

// momentsOfColumn is the shape an application's aggregation code takes: query a
// slice of the projection, take the column, reduce it.
//
// The all-null check before the type assertion is not defensive padding, it is
// required, and it is written on the Vector interface rather than on batch.Rows
// on purpose. buildFieldVector derives a column's type from the first *present
// scalar value*, so the untyped fallback fires in two distinct situations:
// a predicate that matched no rows, and a batch of rows in which this particular
// field is absent from every one of them. Only the second is invisible to a
// `batch.Rows == 0` check, and both must reduce to the identity.
//
// This is the idiom documented on MomentsValid, exercised here so it stays true.
func momentsOfColumn(tb testing.TB, store *Store, field string, predicates []ColumnPredicate) Moments {
	tb.Helper()
	batch, err := store.GetColumnarBatchWhere(
		context.Background(), "ticks",
		Query{OrganizationID: "org_1"},
		[]string{field}, predicates, Fence{},
	)
	if err != nil {
		tb.Fatalf("GetColumnarBatchWhere: %v", err)
	}
	if len(batch.Columns) == 0 {
		return Moments{}
	}

	column := batch.Columns[0].Data
	if column.Len() == column.NullCount() {
		return Moments{}
	}
	vector, ok := column.(*Float64Vector)
	if !ok {
		tb.Fatalf("column %q is %T over %d rows with %d present, not a Float64Vector",
			field, column, batch.Rows, column.Len()-column.NullCount())
	}
	return vector.MomentsValid()
}

// TestMomentsAreReachableFromAColumnarQuery is the delivery test: a moment
// triple can be obtained from the public query API, and it agrees with the
// arithmetic computed directly over the same values.
func TestMomentsAreReachableFromAColumnarQuery(t *testing.T) {
	store := buildSelectFixtureStore(t, 400)

	batch, err := store.GetColumnarBatchWhere(
		context.Background(), "ticks",
		Query{OrganizationID: "org_1"},
		[]string{"price"}, nil, Fence{},
	)
	if err != nil {
		t.Fatalf("GetColumnarBatchWhere: %v", err)
	}
	vector, ok := batch.Columns[0].Data.(*Float64Vector)
	if !ok {
		t.Fatalf("price column is %T", batch.Columns[0].Data)
	}

	got := vector.MomentsValid()

	// Count is *contributing* rows, not batch rows. The projection carries rows
	// whose price field is absent, and a null is not a value — so this is the
	// null algebra arriving intact through the query path, which is the part a
	// vector-level unit test cannot show.
	present := vector.Len() - vector.NullCount()
	if got.Count != present {
		t.Fatalf("moments counted %d rows, %d of %d batch rows are present",
			got.Count, present, batch.Rows)
	}
	if vector.NullCount() == 0 {
		t.Log("fixture has no null prices; this run does not exercise null handling")
	}

	// The oracle sees only the present values, matching what the reduction is
	// defined over.
	presentValues := make([]float64, 0, present)
	for i, value := range vector.Float64Values() {
		if vector.IsValid(i) {
			presentValues = append(presentValues, value)
		}
	}
	wantMean, wantVariance := refMoments(presentValues)
	gotMean, ok := got.MeanValue()
	if !ok {
		t.Fatal("mean undefined over a populated column")
	}
	if !closeEnough(gotMean, wantMean, 1e-9) {
		t.Errorf("mean = %v, want %v", gotMean, wantMean)
	}
	gotVariance, ok := got.Variance()
	if !ok {
		t.Fatal("variance undefined over a populated column")
	}
	if !closeEnough(gotVariance, wantVariance, 1e-9) {
		t.Errorf("variance = %v, want %v", gotVariance, wantVariance)
	}
}

// TestShardedMomentsMergeToTheWholeProjection is the reason the type exists,
// exercised the way an application would hit it: reduce two disjoint slices of
// a projection separately, merge the triples, and get the same answer as one
// reduction over both.
//
// This is the shape that the deleted swarm branch got wrong — partial spreads
// combined across partitions — and it is the shape a real deployment reaches
// for, because merging two triples is cheaper than shipping two sets of rows.
func TestShardedMomentsMergeToTheWholeProjection(t *testing.T) {
	store := buildSelectFixtureStore(t, 500)

	// Two disjoint shards with deliberately different centres, because equal
	// centres would let a wrong merge coincide with a right one.
	const split = 150.0
	lower := momentsOfColumn(t, store, "price", []ColumnPredicate{
		PredicateFloat64("price", CompareLt, split),
	})
	upper := momentsOfColumn(t, store, "price", []ColumnPredicate{
		PredicateFloat64("price", CompareGe, split),
	})
	whole := momentsOfColumn(t, store, "price", nil)

	if lower.Count == 0 || upper.Count == 0 {
		t.Fatalf("shards must both be populated: lower=%d upper=%d", lower.Count, upper.Count)
	}
	if lower.Count+upper.Count != whole.Count {
		t.Fatalf("shards cover %d rows, whole has %d", lower.Count+upper.Count, whole.Count)
	}

	lowerMean, _ := lower.MeanValue()
	upperMean, _ := upper.MeanValue()
	if closeEnough(lowerMean, upperMean, 1e-6) {
		t.Fatalf("shard centres coincide (%v, %v); the test cannot distinguish a correct merge",
			lowerMean, upperMean)
	}

	merged := lower.Merge(upper)

	mergedMean, ok := merged.MeanValue()
	if !ok {
		t.Fatal("merged mean undefined")
	}
	wholeMean, ok := whole.MeanValue()
	if !ok {
		t.Fatal("whole mean undefined")
	}
	if !closeEnough(mergedMean, wholeMean, 1e-9) {
		t.Errorf("merged mean = %v, whole = %v", mergedMean, wholeMean)
	}

	mergedVariance, ok := merged.Variance()
	if !ok {
		t.Fatal("merged variance undefined")
	}
	wholeVariance, ok := whole.Variance()
	if !ok {
		t.Fatal("whole variance undefined")
	}
	if !closeEnough(mergedVariance, wholeVariance, 1e-9) {
		t.Errorf("merged variance = %v, whole = %v", mergedVariance, wholeVariance)
	}

	// And the failure this replaces, priced: averaging the shards' standard
	// deviations is what the deleted implementation did.
	lowerSD, _ := lower.StdDev()
	upperSD, _ := upper.StdDev()
	naive := (lowerSD + upperSD) / 2
	correct := math.Sqrt(wholeVariance)
	if closeEnough(naive, correct, 1e-6) {
		t.Fatal("averaging shard stddevs happened to agree; the fixture no longer exercises the bug")
	}
	t.Logf("averaged shard stddevs = %v, true stddev = %v (%.1f%% error)",
		naive, correct, math.Abs(naive-correct)/correct*100)
}

// TestMomentsOverAnEmptyShardIsTheIdentity covers the case a sharded reducer
// hits constantly and must not special-case: a predicate that matches nothing
// contributes the identity, so an empty shard can be merged in blind.
func TestMomentsOverAnEmptyShardIsTheIdentity(t *testing.T) {
	store := buildSelectFixtureStore(t, 100)

	empty := momentsOfColumn(t, store, "price", []ColumnPredicate{
		PredicateFloat64("price", CompareGt, 1e12),
	})
	if !empty.IsNull() {
		t.Fatalf("an empty shard reduced to %+v, want the identity", empty)
	}

	// A field no record carries is the other route to the untyped fallback, and
	// the one a batch.Rows check would miss: the batch is full of rows, and the
	// column is all-null.
	absent := momentsOfColumn(t, store, "no_such_field", nil)
	if !absent.IsNull() {
		t.Fatalf("an absent field reduced to %+v, want the identity", absent)
	}

	populated := momentsOfColumn(t, store, "price", nil)
	merged := populated.Merge(empty)

	if merged.Count != populated.Count {
		t.Fatalf("merging an empty shard changed the count: %d -> %d", populated.Count, merged.Count)
	}
	populatedVariance, ok := populated.Variance()
	if !ok {
		t.Fatal("variance undefined over a populated projection")
	}
	mergedVariance, ok := merged.Variance()
	if !ok {
		t.Fatal("variance undefined after merging an empty shard")
	}
	if !closeEnough(mergedVariance, populatedVariance, 1e-12) {
		t.Errorf("merging an empty shard moved the variance: %v -> %v",
			populatedVariance, mergedVariance)
	}
}
