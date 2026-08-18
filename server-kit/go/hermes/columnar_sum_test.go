package hermes

import (
	"math"
	"math/big"
	"testing"
)

// TestFloat64VectorSumMatchesScalarReference is the SIMD parity test. On the
// SIMD build (amd64 + GOEXPERIMENT=simd) it proves the vectorized reduction
// stays within floating-point tolerance of the scalar reference; on every other
// build it confirms the fallback is exactly the reference. Tolerance is required
// because lane-wise accumulation reorders non-associative float additions.
func TestFloat64VectorSumMatchesScalarReference(t *testing.T) {
	xs := make([]float64, 1000)
	for i := range xs {
		xs[i] = float64(i)*0.5 - 250.0
	}
	v := &Float64Vector{values: xs, validity: newValidityBitmap(len(xs))}

	got := v.Sum()
	want := sumFloat64sScalar(xs)
	if diff := math.Abs(got - want); diff > 1e-6*math.Abs(want)+1e-9 {
		t.Fatalf("Sum()=%v reference=%v diff=%v exceeds tolerance", got, want, diff)
	}

	if s := (&Float64Vector{}).Sum(); s != 0 {
		t.Fatalf("empty Sum()=%v want 0", s)
	}

	// Exercise every tail length around the 8-element SIMD stride and the
	// 16-element AVX2 threshold so the scalar tail and fallback are both covered.
	for _, n := range []int{1, 2, 3, 7, 8, 9, 15, 16, 17, 31, 33} {
		s := make([]float64, n)
		for i := range s {
			s[i] = float64(i+1) * 1.25
		}
		if diff := math.Abs(sumFloat64s(s) - sumFloat64sScalar(s)); diff > 1e-9*float64(n) {
			t.Fatalf("n=%d: sumFloat64s and scalar diverged by %v", n, diff)
		}
	}
}

// BenchmarkColumnarFloat64Sum measures the column reduction. Run the portable
// (scalar) lane with `go test -bench`; run the vectorized lane with
// `make bench-simd` (GOEXPERIMENT=simd) on an AVX2 amd64 host.
func BenchmarkColumnarFloat64Sum(b *testing.B) {
	xs := make([]float64, 10000)
	for i := range xs {
		xs[i] = float64(i) * 1.5
	}
	v := &Float64Vector{values: xs, validity: newValidityBitmap(len(xs))}
	b.ReportAllocs()
	var sink float64
	for b.Loop() {
		sink = v.Sum()
	}
	_ = sink
}

// --- exact-arithmetic oracle -------------------------------------------------
//
// The parity test above compares two approximations of the same non-associative
// operation: the 8-lane AVX2 reduction against the 4-accumulator scalar
// reference. Neither side is ground truth, so the tolerance it asserts bounds
// only the *disagreement* between two reorderings — not the error of either.
// The helpers below supply the missing anchor: the correctly-rounded true sum,
// computed in exact rational arithmetic, so both lanes can be measured against
// the value they are approximating. This mirrors the discipline used to certify
// numerically-searched results elsewhere (search in fast floating point,
// re-evaluate the claim in an exact regime); see the FP rules in
// docs/mathematical_practices.md §4.

// exactSumFloat64 returns the correctly-rounded true sum of xs.
//
// Every finite float64 is a dyadic rational, so big.Rat holds each addend and
// every partial sum with zero error; the single Float64() conversion at the end
// is the only rounding in the entire computation, and it is round-to-nearest-
// even. Inputs must be finite — big.Rat cannot represent NaN or ±Inf, which are
// covered by TestFloat64VectorSumNonFiniteParity instead.
func exactSumFloat64(t *testing.T, xs []float64) float64 {
	t.Helper()
	acc := new(big.Rat)
	term := new(big.Rat)
	for i, x := range xs {
		if term.SetFloat64(x) == nil {
			t.Fatalf("exactSumFloat64: xs[%d]=%v is not finite", i, x)
		}
		acc.Add(acc, term)
	}
	f, _ := acc.Float64()
	return f
}

// totalOrderBits maps a finite float64 onto uint64 so that numeric order becomes
// unsigned-integer order (the IEEE 754 totalOrder predicate for finite values).
// Subtracting two such keys counts representable values, which is what makes
// ulpDistance exact across sign changes and around zero.
func totalOrderBits(f float64) uint64 {
	b := math.Float64bits(f)
	if b>>63 == 1 {
		return ^b
	}
	return b | 1<<63
}

// ulpDistance counts the representable float64 values separating a and b. It is
// the scale-free way to state how wrong a reduction is: one ULP at 1e300 and one
// ULP at 1e-300 are wildly different absolute numbers but the same statement
// about the last mantissa bit. Both arguments must be finite.
func ulpDistance(a, b float64) uint64 {
	ak, bk := totalOrderBits(a), totalOrderBits(b)
	if ak > bk {
		return ak - bk
	}
	return bk - ak
}

// summationErrorBound returns the classical forward error bound for a
// floating-point summation whose longest dependent add chain is k terms:
//
//	|fl(Σx) − Σx| ≤ γ_k · Σ|x_i|,   γ_k = k·u / (1 − k·u),   u = 2⁻⁵³
//
// (Higham, Accuracy and Stability of Numerical Algorithms, 2nd ed. §4.2 — the
// source mathematical_practices.md §4 already cites.) The bound scales with the
// sum of absolute values, not with the result: that is precisely why a
// correctly implemented reduction can still be 100% relatively wrong on an
// ill-conditioned input, and why FP-1's relative tolerance cannot be the only
// check. k comes from the lane's shape, never from trial fitting.
func summationErrorBound(xs []float64, k int) float64 {
	const u = 1.0 / (1 << 53) // double unit roundoff under round-to-nearest
	var absSum float64
	for _, x := range xs {
		absSum += math.Abs(x)
	}
	ku := float64(k) * u
	if ku >= 1 {
		return math.Inf(1)
	}
	return ku / (1 - ku) * absSum
}

// laneChainLength upper-bounds the longest dependent add chain over both
// shipping lanes for an n-element input: the scalar reference runs 4
// accumulators (⌈n/4⌉ adds each, 3 combining adds) and the AVX2 lane runs 8
// (⌈n/8⌉ each, 7 combining adds plus a scalar tail below the 8-element stride).
// n/4+16 covers whichever one this build compiled.
func laneChainLength(n int) int { return n/4 + 16 }

// ulpUnbounded marks a case whose conditioning makes any fixed ULP ceiling
// meaningless; only the mathematical bound is asserted for it.
const ulpUnbounded = math.MaxUint64

// TestFloat64VectorSumMatchesExactOracle measures both float lanes against the
// exact sum rather than against each other. Every case asserts the Higham bound
// (mathematically guaranteed for a correct reduction, so it can never be tuned
// away) and, where the input is well-conditioned enough for the claim to mean
// something, a ULP ceiling that holds on both the scalar and AVX2 lanes.
func TestFloat64VectorSumMatchesExactOracle(t *testing.T) {
	// ramp is the input the older parity test uses. Every element is a multiple
	// of 0.5 and every partial sum stays well inside 2⁵³, so all partial sums are
	// exactly representable in any accumulation order — the case is incapable of
	// producing reordering error, which is exactly why the cases after it exist.
	ramp := make([]float64, 1000)
	for i := range ramp {
		ramp[i] = float64(i)*0.5 - 250.0
	}

	// cancellation is the textbook ill-conditioned sum: Σ|x| is 1e16 times the
	// true total, so the small terms live below the ULP of the running partial
	// sums and survive only by luck of ordering.
	cancellation := make([]float64, 0, 999)
	for range 333 {
		cancellation = append(cancellation, 1e16, 1.0, -1e16)
	}

	// mixedMagnitude sweeps 17 decades with alternating signs. The true total
	// lands near 1e-5 while Σ|x| reaches ~1e10, so this is a second — and much
	// less obvious — ill-conditioned case: nothing about the input looks
	// pathological, yet no ULP claim about the result is meaningful.
	mixedMagnitude := make([]float64, 1024)
	for i := range mixedMagnitude {
		v := math.Pow(10, float64(i%17)-8)
		if i%2 == 1 {
			v = -v
		}
		mixedMagnitude[i] = v
	}

	// wellConditioned is the case that isolates reordering error on its own:
	// all terms positive and within one octave, so Σ|x| = Σx and no cancellation
	// can occur, but the partial sums are not exactly representable — unlike the
	// ramp above, this input can actually observe a change in accumulation order.
	// The multiplier is a deterministic mantissa spreader, not a random seed.
	wellConditioned := make([]float64, 4096)
	for i := range wellConditioned {
		wellConditioned[i] = 1 + float64((i*2654435761)%1000)/1000
	}

	// subnormals exercise FP-4's smallest-magnitude regime. Sums of small
	// integer multiples of the smallest subnormal stay exactly representable.
	subnormals := make([]float64, 64)
	for i := range subnormals {
		subnormals[i] = math.SmallestNonzeroFloat64 * float64(i%3+1)
	}

	// bigPlusSmall is where multi-accumulator reduction beats a single running
	// sum: 1e17 has a ULP of 16, so a strict left-to-right sum absorbs every
	// following 1.0 into rounding, while independent lanes let the small terms
	// accumulate among themselves before meeting the large one.
	bigPlusSmall := make([]float64, 0, 4097)
	bigPlusSmall = append(bigPlusSmall, 1e17)
	for range 4096 {
		bigPlusSmall = append(bigPlusSmall, 1.0)
	}

	cases := []struct {
		name   string
		xs     []float64
		maxULP uint64
	}{
		// ramp and subnormals are exactly representable in any accumulation
		// order, so 0 is a guarantee rather than an observation.
		{"ramp-exactly-representable", ramp, 0},
		{"subnormals", subnormals, 0},
		// Both shipping lanes land on the correctly-rounded result here; the
		// ceiling carries a little margin for a future lane with a different
		// accumulator count.
		{"well-conditioned-positive", wellConditioned, 2},
		// 64 ULP is derived, not fitted: the accumulator holding 1e17 (ULP 16)
		// absorbs its share of the 4096 ones and loses every one of them, so a
		// k-accumulator lane errs by 4096/k. The 4-accumulator reference gives
		// 1024 absolute = 64 ULP; the 8-lane AVX2 path gives exactly half. A
		// regression to fewer accumulators fails this.
		{"big-plus-small", bigPlusSmall, 64},
		// No ULP claim is meaningful on these two: see the comments above.
		{"catastrophic-cancellation", cancellation, ulpUnbounded},
		{"mixed-magnitude-cancellation", mixedMagnitude, ulpUnbounded},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exact := exactSumFloat64(t, tc.xs)
			bound := summationErrorBound(tc.xs, laneChainLength(len(tc.xs)))

			lane := sumFloat64s(tc.xs)
			ref := sumFloat64sScalar(tc.xs)

			// Sum() must route to the same lane as the free function.
			v := &Float64Vector{values: tc.xs, validity: newValidityBitmap(len(tc.xs))}
			if got := v.Sum(); got != lane {
				t.Fatalf("Sum()=%v does not match sumFloat64s()=%v", got, lane)
			}

			for _, got := range []struct {
				what  string
				value float64
			}{{"lane", lane}, {"scalar reference", ref}} {
				if err := math.Abs(got.value - exact); err > bound {
					t.Errorf("%s sum=%v exact=%v error=%v exceeds Higham bound %v",
						got.what, got.value, exact, err, bound)
				}
				ulps := ulpDistance(got.value, exact)
				if tc.maxULP != ulpUnbounded && ulps > tc.maxULP {
					t.Errorf("%s sum=%v exact=%v differs by %d ULP, want <= %d",
						got.what, got.value, exact, ulps, tc.maxULP)
				}
				t.Logf("%-17s = %-24v exact = %-24v %d ULP", got.what, got.value, exact, ulps)
			}
		})
	}
}

// TestFloat64VectorSumNonFiniteParity pins the FP-4 obligations for the raw
// reduction: NaN propagates, a single-signed infinity saturates, and mixed
// infinities produce NaN. The exact oracle cannot express these values, so the
// assertion here is IEEE classification — and, just as importantly, that both
// lanes classify identically. Inputs are sized past the 16-element AVX2
// threshold so the vector path actually engages on a SIMD build.
func TestFloat64VectorSumNonFiniteParity(t *testing.T) {
	finite := func(n int) []float64 {
		xs := make([]float64, n)
		for i := range xs {
			xs[i] = float64(i+1) * 0.25
		}
		return xs
	}

	cases := []struct {
		name  string
		build func() []float64
		want  func(float64) bool
		desc  string
	}{
		{
			name:  "nan-propagates",
			build: func() []float64 { xs := finite(64); xs[37] = math.NaN(); return xs },
			want:  math.IsNaN,
			desc:  "NaN",
		},
		{
			name:  "positive-infinity-saturates",
			build: func() []float64 { xs := finite(64); xs[9] = math.Inf(1); return xs },
			want:  func(f float64) bool { return math.IsInf(f, 1) },
			desc:  "+Inf",
		},
		{
			name:  "negative-infinity-saturates",
			build: func() []float64 { xs := finite(64); xs[9] = math.Inf(-1); return xs },
			want:  func(f float64) bool { return math.IsInf(f, -1) },
			desc:  "-Inf",
		},
		{
			name: "mixed-infinities-are-nan",
			build: func() []float64 {
				xs := finite(64)
				xs[3] = math.Inf(1)
				xs[40] = math.Inf(-1)
				return xs
			},
			want: math.IsNaN,
			desc: "NaN",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			xs := tc.build()
			lane := sumFloat64s(xs)
			ref := sumFloat64sScalar(xs)
			if !tc.want(lane) {
				t.Errorf("sumFloat64s=%v want %s", lane, tc.desc)
			}
			if !tc.want(ref) {
				t.Errorf("sumFloat64sScalar=%v want %s", ref, tc.desc)
			}
			// Bit-equality is the right check here: these classes have no
			// rounding to disagree about, so the lanes must agree exactly
			// (NaN compared by classification, since NaN != NaN).
			if math.IsNaN(lane) != math.IsNaN(ref) || (!math.IsNaN(lane) && lane != ref) {
				t.Errorf("lane/reference disagree on non-finite input: %v vs %v", lane, ref)
			}
		})
	}
}
