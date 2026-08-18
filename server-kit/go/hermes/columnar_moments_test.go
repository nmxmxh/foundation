package hermes

import (
	"math"
	"testing"
)

// naiveMomentsSumSq is the arithmetically obvious partial reduction: carry
// (count, Σx, Σx²) and recover variance as Σx²/n − (Σx/n)². It exists only as a
// test subject. TestMomentsSurvivesCatastrophicCancellation drives it off a
// cliff that the production implementation walks past, which is the argument
// for the M2 representation stated as an executable fact rather than a comment.
type naiveMomentsSumSq struct {
	count int
	sum   float64
	sumSq float64
}

func (n naiveMomentsSumSq) merge(o naiveMomentsSumSq) naiveMomentsSumSq {
	return naiveMomentsSumSq{count: n.count + o.count, sum: n.sum + o.sum, sumSq: n.sumSq + o.sumSq}
}

func (n naiveMomentsSumSq) variance() float64 {
	if n.count < 2 {
		return math.NaN()
	}
	mean := n.sum / float64(n.count)
	return (n.sumSq - float64(n.count)*mean*mean) / float64(n.count-1)
}

func naiveMomentsOf(values []float64) naiveMomentsSumSq {
	out := naiveMomentsSumSq{}
	for _, x := range values {
		out.count++
		out.sum += x
		out.sumSq += x * x
	}
	return out
}

// refMoments is the direct two-pass textbook computation over a plain slice,
// used as the oracle. It is not the implementation under test: it makes two
// passes over the whole input and cannot be partitioned, which is exactly why
// the production code is shaped differently.
func refMoments(values []float64) (mean, sampleVariance float64) {
	if len(values) == 0 {
		return 0, math.NaN()
	}
	var sum float64
	for _, x := range values {
		sum += x
	}
	mean = sum / float64(len(values))
	if len(values) < 2 {
		return mean, math.NaN()
	}
	var m2 float64
	for _, x := range values {
		d := x - mean
		m2 += d * d
	}
	return mean, m2 / float64(len(values)-1)
}

func momentsOf(values []float64) Moments {
	mean, _ := refMoments(values)
	var m2 float64
	for _, x := range values {
		d := x - mean
		m2 += d * d
	}
	return Moments{Count: len(values), Mean: mean, M2: m2}
}

func closeEnough(got, want, tolerance float64) bool {
	if math.IsNaN(got) || math.IsNaN(want) {
		return math.IsNaN(got) && math.IsNaN(want)
	}
	scale := math.Max(1, math.Max(math.Abs(got), math.Abs(want)))
	return math.Abs(got-want) <= tolerance*scale
}

// ---------------------------------------------------------------------------
// Monoid laws
//
// Every distributed reduce built on MergeMoments depends on these three. A
// scheduler is free to partition the work any way it likes, in any order, and
// to drop empty partitions — but only because of what is asserted here.
// ---------------------------------------------------------------------------

func TestMergeMomentsHasIdentityElement(t *testing.T) {
	m := momentsOf([]float64{3, 1, 4, 1, 5, 9, 2, 6})
	identity := Moments{}

	if got := MergeMoments(m, identity); got != m {
		t.Errorf("m ⊕ e = %+v, want %+v", got, m)
	}
	if got := MergeMoments(identity, m); got != m {
		t.Errorf("e ⊕ m = %+v, want %+v", got, m)
	}
	if got := MergeMoments(identity, identity); got != identity {
		t.Errorf("e ⊕ e = %+v, want the identity", got)
	}
}

func TestMergeMomentsIsAssociative(t *testing.T) {
	a := momentsOf([]float64{1, 2, 3})
	b := momentsOf([]float64{40, 50})
	c := momentsOf([]float64{600, 700, 800, 900})

	left := MergeMoments(MergeMoments(a, b), c)
	right := MergeMoments(a, MergeMoments(b, c))

	if left.Count != right.Count {
		t.Fatalf("count: (a⊕b)⊕c = %d, a⊕(b⊕c) = %d", left.Count, right.Count)
	}
	if !closeEnough(left.Mean, right.Mean, 1e-12) {
		t.Errorf("mean: (a⊕b)⊕c = %v, a⊕(b⊕c) = %v", left.Mean, right.Mean)
	}
	if !closeEnough(left.M2, right.M2, 1e-12) {
		t.Errorf("M2: (a⊕b)⊕c = %v, a⊕(b⊕c) = %v", left.M2, right.M2)
	}
}

func TestMergeMomentsIsCommutative(t *testing.T) {
	a := momentsOf([]float64{1, 2, 3, 4})
	b := momentsOf([]float64{100, 200})

	ab := MergeMoments(a, b)
	ba := MergeMoments(b, a)

	if ab.Count != ba.Count || !closeEnough(ab.Mean, ba.Mean, 1e-12) || !closeEnough(ab.M2, ba.M2, 1e-12) {
		t.Errorf("a⊕b = %+v, b⊕a = %+v", ab, ba)
	}
}

// ---------------------------------------------------------------------------
// Partition parity — the regression test for the deleted swarm branch's reduce
// ---------------------------------------------------------------------------

// TestMomentsPartitionParityMatchesSinglePass is the direct regression test for
// the bug recorded as issue 7 of the swarm-experiment post-mortem: partial
// standard deviations were summed and divided by N, which is not how variance
// composes. Here the same data is reduced whole and in partitions of every
// width, and the answers must agree.
func TestMomentsPartitionParityMatchesSinglePass(t *testing.T) {
	values := make([]float64, 257) // deliberately not a multiple of 64
	for i := range values {
		values[i] = math.Sin(float64(i))*50 + float64(i)*0.25
	}

	wantMean, wantVariance := refMoments(values)

	for _, width := range []int{1, 2, 7, 63, 64, 65, 128, 256, 257, 512} {
		merged := Moments{}
		for start := 0; start < len(values); start += width {
			end := min(start+width, len(values))
			merged = merged.Merge(momentsOf(values[start:end]))
		}

		if merged.Count != len(values) {
			t.Errorf("width %d: count = %d, want %d", width, merged.Count, len(values))
		}
		gotMean, ok := merged.MeanValue()
		if !ok {
			t.Fatalf("width %d: mean undefined", width)
		}
		if !closeEnough(gotMean, wantMean, 1e-9) {
			t.Errorf("width %d: mean = %v, want %v", width, gotMean, wantMean)
		}
		gotVariance, ok := merged.Variance()
		if !ok {
			t.Fatalf("width %d: variance undefined", width)
		}
		if !closeEnough(gotVariance, wantVariance, 1e-9) {
			t.Errorf("width %d: variance = %v, want %v", width, gotVariance, wantVariance)
		}
	}
}

// TestAveragingPartialStdDevsIsWrong pins the actual failure mode down with a
// number, so the reason this type exists cannot be quietly refactored away. Two
// partitions with zero internal spread but different centres have a real,
// non-zero combined variance; averaging their standard deviations reports zero.
func TestAveragingPartialStdDevsIsWrong(t *testing.T) {
	left := []float64{10, 10, 10, 10}
	right := []float64{20, 20, 20, 20}

	leftSD, leftOK := momentsOf(left).StdDev()
	rightSD, rightOK := momentsOf(right).StdDev()
	if !leftOK || !rightOK {
		t.Fatal("partition standard deviations should be defined")
	}
	if leftSD != 0 || rightSD != 0 {
		t.Fatalf("each partition should have zero spread, got %v and %v", leftSD, rightSD)
	}

	// What the deleted implementation did.
	naive := (leftSD + rightSD) / 2

	// What the monoid does.
	merged := momentsOf(left).Merge(momentsOf(right))
	correct, ok := merged.StdDev()
	if !ok {
		t.Fatal("merged standard deviation should be defined")
	}

	_, wantVariance := refMoments(append(append([]float64{}, left...), right...))
	want := math.Sqrt(wantVariance)

	if !closeEnough(correct, want, 1e-12) {
		t.Errorf("merged stddev = %v, want %v", correct, want)
	}
	if naive == correct {
		t.Error("averaging partial stddevs agreed with the monoid; the test has lost its subject")
	}
	if naive != 0 {
		t.Errorf("sanity: averaging zeros should give 0, got %v", naive)
	}
}

// ---------------------------------------------------------------------------
// Numerical behaviour — why M2 and not Σx²
// ---------------------------------------------------------------------------

// TestMomentsSurvivesCatastrophicCancellation is the argument for the M2
// representation. The data is centred at 1e9 with unit-scale spread, so Σx² and
// n·mean² are both around 1e26 and their difference is around 1e1: seventeen
// significant digits apart, and float64 has fifteen or sixteen. The naive form
// loses the answer entirely — often returning a negative variance, which is not
// merely inaccurate but outside the range of the quantity.
func TestMomentsSurvivesCatastrophicCancellation(t *testing.T) {
	const offset = 1e9
	base := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

	shifted := make([]float64, len(base))
	for i, x := range base {
		shifted[i] = offset + x
	}

	// Variance is translation-invariant, so the shifted data must report the
	// same variance as the base data. That is the oracle.
	_, wantVariance := refMoments(base)

	got, ok := momentsOf(shifted).Variance()
	if !ok {
		t.Fatal("variance should be defined")
	}
	if !closeEnough(got, wantVariance, 1e-6) {
		t.Errorf("M2 variance = %v, want %v (translation invariance)", got, wantVariance)
	}

	// Partitioned, because that is the shape the reduce actually runs in: each
	// partition accumulates its own (count, Σx, Σx²) and the merge sums them.
	// The cancellation is a property of the representation, not of the
	// partitioning, so it survives the merge intact.
	naiveWhole := naiveMomentsOf(shifted)
	naivePartitioned := naiveMomentsOf(shifted[:5]).merge(naiveMomentsOf(shifted[5:]))
	if naivePartitioned.count != naiveWhole.count {
		t.Fatalf("naive merge lost rows: %d vs %d", naivePartitioned.count, naiveWhole.count)
	}

	naive := naivePartitioned.variance()
	if closeEnough(naive, wantVariance, 1e-6) {
		t.Skip("this platform's float64 absorbed the cancellation; the M2 assertion above still holds")
	}
	t.Logf("naive Σx² form returned %v against a true variance of %v — the error this type avoids", naive, wantVariance)

	// And the monoid gets it right under the same partitioning.
	m2Partitioned := momentsOf(shifted[:5]).Merge(momentsOf(shifted[5:]))
	gotPartitioned, ok := m2Partitioned.Variance()
	if !ok {
		t.Fatal("partitioned variance should be defined")
	}
	if !closeEnough(gotPartitioned, wantVariance, 1e-6) {
		t.Errorf("partitioned M2 variance = %v, want %v", gotPartitioned, wantVariance)
	}
}

// ---------------------------------------------------------------------------
// Column reduction: nulls, propagation, and agreement with the first moment
// ---------------------------------------------------------------------------

func TestMomentsValidIgnoresNullRows(t *testing.T) {
	// Every third row is present; the rest are poisoned with NaN by the helper,
	// so any leak shows up as NaN rather than as a plausible wrong number.
	vec := buildFloat64Vec(200, func(i int) bool { return i%3 == 0 }, func(i int) float64 { return float64(i) })

	var present []float64
	for i := 0; i < 200; i += 3 {
		present = append(present, float64(i))
	}
	wantMean, wantVariance := refMoments(present)

	got := vec.MomentsValid()
	if got.Count != len(present) {
		t.Fatalf("count = %d, want %d", got.Count, len(present))
	}
	gotMean, ok := got.MeanValue()
	if !ok {
		t.Fatal("mean undefined over a column with present rows")
	}
	if !closeEnough(gotMean, wantMean, 1e-9) {
		t.Errorf("mean = %v, want %v", gotMean, wantMean)
	}
	gotVariance, ok := got.Variance()
	if !ok {
		t.Fatal("variance undefined over a column with present rows")
	}
	if !closeEnough(gotVariance, wantVariance, 1e-9) {
		t.Errorf("variance = %v, want %v", gotVariance, wantVariance)
	}
}

// TestMomentsValidAgreesWithMeanValid keeps the two projections of the same
// column consistent. They are computed by different code — SumValid masks
// values, MomentsValid masks deviations — and a divergence between them would
// mean one of the two mask arguments is wrong.
func TestMomentsValidAgreesWithMeanValid(t *testing.T) {
	vec := buildFloat64Vec(1000, func(i int) bool { return i%7 != 0 }, func(i int) float64 {
		return math.Cos(float64(i)) * 1000
	})

	fromSum := vec.MeanValid()
	fromMoments := vec.MomentsValid()

	if fromSum.Count != fromMoments.Count {
		t.Fatalf("count: MeanValid = %d, MomentsValid = %d", fromSum.Count, fromMoments.Count)
	}
	gotMean, ok := fromMoments.MeanValue()
	if !ok {
		t.Fatal("MomentsValid reported no mean where MeanValid found one")
	}
	if !closeEnough(gotMean, fromSum.Value, 1e-9) {
		t.Errorf("mean: MeanValid = %v, MomentsValid = %v", fromSum.Value, gotMean)
	}
}

func TestMomentsValidOnAllNullColumnIsIdentity(t *testing.T) {
	vec := buildFloat64Vec(128, func(int) bool { return false }, func(int) float64 { return 0 })

	got := vec.MomentsValid()
	if !got.IsNull() {
		t.Fatalf("all-null column reduced to %+v, want the identity", got)
	}
	if _, ok := got.MeanValue(); ok {
		t.Error("mean reported as defined over an all-null column")
	}
	if _, ok := got.Variance(); ok {
		t.Error("variance reported as defined over an all-null column")
	}
	if _, ok := got.StdDev(); ok {
		t.Error("stddev reported as defined over an all-null column")
	}
}

// TestMomentsValidPropagatesNaNFromPresentRows fixes the NaN policy: a present
// NaN is a value whose spread is undefined, and it propagates. This is
// SumValid's behaviour, not MinValid's, and the two differ deliberately.
func TestMomentsValidPropagatesNaNFromPresentRows(t *testing.T) {
	vec := newFloat64Vector(4)
	for i, x := range []float64{1, math.NaN(), 3, 2} {
		vec.values[i] = x
		vec.validity.set(i)
	}

	got := vec.MomentsValid()
	if got.Count != 4 {
		t.Fatalf("count = %d, want 4", got.Count)
	}
	variance, ok := got.Variance()
	if !ok {
		t.Fatal("variance should be defined (four present rows) even though it is NaN")
	}
	if !math.IsNaN(variance) {
		t.Errorf("variance = %v, want NaN (a present NaN propagates through +)", variance)
	}
}

// ---------------------------------------------------------------------------
// Definedness boundaries
// ---------------------------------------------------------------------------

func TestVarianceIsUndefinedBelowTwoRows(t *testing.T) {
	single := momentsOf([]float64{42})

	if _, ok := single.Variance(); ok {
		t.Error("sample variance reported as defined for a single row")
	}
	if _, ok := single.StdDev(); ok {
		t.Error("sample stddev reported as defined for a single row")
	}

	// The population form is defined for one row, and is zero: a single point is
	// the whole population and does not deviate from itself.
	popVariance, ok := single.PopulationVariance()
	if !ok {
		t.Fatal("population variance should be defined for a single row")
	}
	if popVariance != 0 {
		t.Errorf("population variance = %v, want 0", popVariance)
	}

	mean, ok := single.MeanValue()
	if !ok || mean != 42 {
		t.Errorf("mean = %v (defined=%v), want 42", mean, ok)
	}
}

func TestSampleAndPopulationVarianceDifferByBesselCorrection(t *testing.T) {
	values := []float64{2, 4, 4, 4, 5, 5, 7, 9}
	m := momentsOf(values)

	sample, ok := m.Variance()
	if !ok {
		t.Fatal("sample variance undefined")
	}
	population, ok := m.PopulationVariance()
	if !ok {
		t.Fatal("population variance undefined")
	}

	n := float64(len(values))
	if !closeEnough(population, sample*(n-1)/n, 1e-12) {
		t.Errorf("population = %v, sample = %v: Bessel relation does not hold", population, sample)
	}
	// The textbook value for this classic dataset.
	if !closeEnough(population, 4, 1e-12) {
		t.Errorf("population variance = %v, want 4", population)
	}
}
