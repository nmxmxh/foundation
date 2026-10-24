//go:build !linux

package scratch

// HugePageSize is the standard 2 MiB huge page size on x86_64 and ARM64.
const HugePageSize = 2 * 1024 * 1024

// AdviseHugePage is a transparent no-op on non-Linux platforms.
func AdviseHugePage(raw []byte) error {
	return nil
}
