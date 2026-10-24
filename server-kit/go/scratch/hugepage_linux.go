//go:build linux

package scratch

import (
	"golang.org/x/sys/unix"
)

// HugePageSize is the standard 2 MiB huge page size on x86_64 and ARM64.
const HugePageSize = 2 * 1024 * 1024

// AdviseHugePage requests that the Linux kernel back the memory buffer with 2 MiB huge pages.
// It applies only to buffers that are at least 2 MiB in length.
func AdviseHugePage(raw []byte) error {
	if len(raw) < HugePageSize {
		return nil
	}
	return unix.Madvise(raw, unix.MADV_HUGEPAGE)
}
