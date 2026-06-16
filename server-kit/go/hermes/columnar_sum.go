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
// reference. Because floating-point addition is not associative, the SIMD
// lane's lane-wise accumulation can differ from the strict left-to-right scalar
// sum by a few ULPs — acceptable for the analytical/telemetry lane this serves,
// and bounded by the parity test. Null entries are summed as their zero value;
// null-aware (validity-masked) reduction is a separate future lane.
func (v *Float64Vector) Sum() float64 {
	return sumFloat64s(v.values)
}

// sumFloat64sScalar is the portable reference reduction and the fallback used by
// every non-SIMD build. The SIMD lane must stay within floating-point tolerance
// of this result; see TestFloat64VectorSumMatchesScalarReference.
func sumFloat64sScalar(xs []float64) float64 {
	var s float64
	for _, x := range xs {
		s += x
	}
	return s
}
