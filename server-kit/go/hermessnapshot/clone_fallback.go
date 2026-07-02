//go:build !linux

package hermessnapshot

// Non-Linux builds have exactly one clone lane: the portable userspace copy.
// The Linux kernel lanes (reflink, copy_file_range) are build-tag gated in
// clone_linux.go; this file keeps every other platform on the same visible
// contract with the same lane-reporting shape.

import (
	"fmt"
	"os"
)

func cloneFile(dst, src string) (string, error) {
	srcFile, err := os.Open(src) // #nosec G304 -- paths derived from store roots.
	if err != nil {
		return "", fmt.Errorf("open clone source: %w", err)
	}
	defer closeQuiet(srcFile)
	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) // #nosec G304
	if err != nil {
		return "", fmt.Errorf("create clone target: %w", err)
	}
	defer closeQuiet(dstFile)
	return userspaceCopy(dstFile, srcFile)
}
