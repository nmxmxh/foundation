//go:build !linux && !darwin && !windows

package kernellane

import "os"

// kernelPunchHole returns (false, nil) on operating systems where sparse
// hole punching is not natively supported, preserving graceful degradation.
func kernelPunchHole(_ *os.File, _, _ int64) (bool, error) {
	return false, nil
}
