//go:build linux

package runtimehost

import (
	"golang.org/x/sys/unix"
)

// HugePageThresholdBytes defines the minimum mapping size eligible for 2 MiB huge page advice.
const HugePageThresholdBytes = 2 * 1024 * 1024

// adviseHugePage advises the Linux kernel to back the shared mapping with transparent huge pages.
func adviseHugePage(raw []byte) error {
	if len(raw) < HugePageThresholdBytes {
		return nil
	}
	return unix.Madvise(raw, unix.MADV_HUGEPAGE)
}
