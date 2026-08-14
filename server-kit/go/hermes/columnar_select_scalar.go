//go:build !(amd64 && goexperiment.simd)

package hermes

func selectFloat64Kernel(words []uint64, values []float64, op CompareOp, operand float64) {
	selectFloat64Scalar(words, values, op, operand)
}

func selectInt64Kernel(words []uint64, values []int64, op CompareOp, operand int64) {
	selectInt64Scalar(words, values, op, operand)
}
