//go:build !linux

package kernellane

import "os"

// Non-Linux builds have exactly one clone lane: the portable userspace copy.
// Reporting (\"\", false, nil) keeps the visible contract identical to Linux —
// CloneFile falls back and reports CloneLaneUserspace — so callers never need a
// platform branch.
func kernelCloneFile(_, _ *os.File) (string, bool, error) {
	return "", false, nil
}
