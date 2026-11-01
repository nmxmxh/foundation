//go:build !linux && !darwin

package kernellane

import "os"

// kernelHoleAtStart reports that this platform cannot query extents.
// InspectPunchedPrefix then uses the zero-byte check.
func kernelHoleAtStart(_ *os.File, _ int64) (hole, supported bool, err error) {
	return false, false, nil
}
