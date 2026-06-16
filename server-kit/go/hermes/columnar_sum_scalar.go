//go:build !(amd64 && goexperiment.simd)

package hermes

// sumFloat64s uses the scalar reference on every build without the amd64 SIMD
// lane. This keeps ordinary (portable) builds free of any GOEXPERIMENT
// dependency — the SIMD path is opt-in via `make bench-simd`.
func sumFloat64s(xs []float64) float64 {
	return sumFloat64sScalar(xs)
}
