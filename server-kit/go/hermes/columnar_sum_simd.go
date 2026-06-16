//go:build amd64 && goexperiment.simd

package hermes

import "simd/archsimd"

// sumFloat64s is the vectorized reduction used only on amd64 builds compiled
// with GOEXPERIMENT=simd. It falls back to the scalar reference when AVX2 is
// absent or the input is too small to amortize vector setup. archsimd vector
// types are confined to this file and never escape into a public API.
//
// Two independent 4-wide accumulators give the CPU instruction-level
// parallelism across the dependent add chain; the tail below the 8-element
// stride is summed scalar.
func sumFloat64s(xs []float64) float64 {
	if !archsimd.X86.AVX2() || len(xs) < 16 {
		return sumFloat64sScalar(xs)
	}
	acc0 := archsimd.BroadcastFloat64x4(0)
	acc1 := archsimd.BroadcastFloat64x4(0)
	i := 0
	for ; i+8 <= len(xs); i += 8 {
		acc0 = acc0.Add(archsimd.LoadFloat64x4Slice(xs[i : i+4]))
		acc1 = acc1.Add(archsimd.LoadFloat64x4Slice(xs[i+4 : i+8]))
	}
	var lo, hi [4]float64
	acc0.Store(&lo)
	acc1.Store(&hi)
	sum := lo[0] + lo[1] + lo[2] + lo[3] + hi[0] + hi[1] + hi[2] + hi[3]
	for ; i < len(xs); i++ {
		sum += xs[i]
	}
	return sum
}
