package hermes

import (
	"math"
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
