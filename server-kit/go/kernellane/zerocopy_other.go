//go:build !linux

package kernellane

import "os"

// kernelCopyFile has no kernel zero-copy primitive outside Linux, so it always
// signals the portable fallback. Keeping ordinary (non-Linux) builds free of
// any syscall dependency mirrors the SIMD scalar-fallback discipline.
func kernelCopyFile(_, _ *os.File, _ int64) (int64, bool, error) {
	return 0, false, errZeroCopyUnsupported
}
