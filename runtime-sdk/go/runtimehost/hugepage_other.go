//go:build !linux

package runtimehost

// HugePageThresholdBytes defines the minimum mapping size eligible for 2 MiB huge page advice.
const HugePageThresholdBytes = 2 * 1024 * 1024

// adviseHugePage is a no-op on platforms without MADV_HUGEPAGE support.
func adviseHugePage(raw []byte) error {
	return nil
}
