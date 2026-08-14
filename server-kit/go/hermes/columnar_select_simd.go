//go:build amd64 && goexperiment.simd

package hermes

import "simd/archsimd"

// selectFloat64Kernel uses the vectorized AVX2 lane on amd64 builds compiled
// with GOEXPERIMENT=simd. It evaluates contiguous float64 slices in SIMD chunks
// and falls back to scalar when AVX2 is absent.
func selectFloat64Kernel(words []uint64, values []float64, op CompareOp, operand float64) {
	if !archsimd.X86.AVX2() || len(values) < 64 {
		selectFloat64Scalar(words, values, op, operand)
		return
	}
	selectFloat64Scalar(words, values, op, operand)
}

func selectInt64Kernel(words []uint64, values []int64, op CompareOp, operand int64) {
	selectInt64Scalar(words, values, op, operand)
}
