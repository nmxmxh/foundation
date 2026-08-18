package hermes

// Float64 column reduction. This is the first Foundation Go SIMD lane: a
// bounded, benchmark-gated reduction over a contiguous structure-of-arrays
// buffer — exactly the shape the Go SIMD posture sanctions (AGENTS.md,
// optimization_points #51/#55). The public surface (Float64Vector.Sum) is
// portable and never exposes archsimd vector types; the vectorized path lives
// behind `amd64 && goexperiment.simd` build tags with this scalar reference as
// the always-present fallback.

// Sum returns the arithmetic sum of the column's contiguous value buffer.
//
// On an amd64 build compiled with GOEXPERIMENT=simd and a CPU reporting AVX2,
// this uses a vectorized reduction; every other build uses the scalar
// reference. Because floating-point addition is not associative, the two lanes
// accumulate in different orders and can differ by a few ULPs. Neither is a
// left-to-right sum: the reference below runs four accumulators and the SIMD
// lane eight, which makes both *more* accurate than a naive sequential sum, and
// the 8-lane path the more accurate of the two. Both are bounded against the
// exact sum by TestFloat64VectorSumMatchesExactOracle; measured error is
// tabulated in docs/foundation_benchmarks.md.
//
// Null entries are summed as their zero value, and for a sum specifically that
// is not a shortcut: 0 is the additive identity, so nulls provably cannot
// perturb the total. The same reasoning does not carry to product, min, or max,
// whose identities are 1 and ±∞ — use SumValid/MinValid/MaxValid for those, and
// whenever the count of contributing rows matters (an all-null column sums to 0
// here, indistinguishable from a column of real zeros). See
// columnar_reduce.go and docs/columnar_null_algebra.md.
func (v *Float64Vector) Sum() float64 {
	return sumFloat64s(v.values)
}

// sumFloat64sScalar is the portable reference reduction and the fallback used by
// every non-SIMD build. The SIMD lane must stay within floating-point tolerance
// of this result; see TestFloat64VectorSumMatchesScalarReference.
func sumFloat64sScalar(xs []float64) float64 {
	var s0, s1, s2, s3 float64
	i := 0
	for ; i+4 <= len(xs); i += 4 {
		s0 += xs[i]
		s1 += xs[i+1]
		s2 += xs[i+2]
		s3 += xs[i+3]
	}
	sum := (s0 + s1) + (s2 + s3)
	for ; i < len(xs); i++ {
		sum += xs[i]
	}
	return sum
}
