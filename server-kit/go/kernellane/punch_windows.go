//go:build windows

package kernellane

import "os"

// kernelPunchHole on Windows provides a non-fatal fallback. Windows requires
// setting the sparse attribute (FSCTL_SET_SPARSE) before zeroing data ranges
// (FSCTL_SET_ZERO_DATA). Returning (false, nil) ensures graceful degradation
// without failing callers.
func kernelPunchHole(_ *os.File, _, _ int64) (bool, error) {
	return false, nil
}
