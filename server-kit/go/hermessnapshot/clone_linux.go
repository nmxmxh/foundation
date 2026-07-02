//go:build linux

package hermessnapshot

// Linux zero-copy clone lanes, fastest first:
//
//  1. reflink (FICLONE ioctl): a copy-on-write clone — O(1) metadata, zero
//     data blocks moved. Requires filesystem support (XFS/Btrfs; overlayfs
//     and ext4 refuse it, which is a tested fallback path, not an error).
//  2. copy_file_range: the kernel moves bytes inside the page cache without
//     round-tripping them through a userspace buffer.
//  3. userspace io.Copy: the portable fallback shared with non-Linux builds.
//
// Every lane produces byte-identical results (the caller re-verifies the
// artifact checksum at read time), so lane selection is purely a cost
// decision — FallbackRefinement in one function.

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
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

	if err := unix.IoctlFileClone(int(dstFile.Fd()), int(srcFile.Fd())); err == nil {
		return "reflink", nil
	} else if !laneUnsupported(err) {
		return "", fmt.Errorf("reflink clone: %w", err)
	}

	if err := copyFileRangeAll(dstFile, srcFile); err == nil {
		return "copy_file_range", nil
	} else if !laneUnsupported(err) {
		return "", fmt.Errorf("copy_file_range clone: %w", err)
	}

	return userspaceCopy(dstFile, srcFile)
}

// copyFileRangeAll drains the source through copy_file_range with a bounded
// loop (CP-02): every iteration must make progress or the lane is abandoned.
func copyFileRangeAll(dst, src *os.File) error {
	info, err := src.Stat()
	if err != nil {
		return err
	}
	// Rewind both descriptors: a failed reflink attempt does not move offsets,
	// but be explicit rather than clever.
	if _, err := src.Seek(0, 0); err != nil {
		return err
	}
	if _, err := dst.Seek(0, 0); err != nil {
		return err
	}
	remaining := info.Size()
	for remaining > 0 {
		n, err := unix.CopyFileRange(int(src.Fd()), nil, int(dst.Fd()), nil, int(min64(remaining, 1<<30)), 0)
		if err != nil {
			return err
		}
		if n <= 0 {
			return errors.New("copy_file_range made no progress")
		}
		remaining -= int64(n)
	}
	return dst.Truncate(info.Size())
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// laneUnsupported reports the errno classes that mean "this lane does not
// exist here" rather than "the copy failed": wrong filesystem, cross-device,
// old kernel, or unsupported file type.
func laneUnsupported(err error) bool {
	return errors.Is(err, unix.ENOTSUP) ||
		errors.Is(err, unix.EOPNOTSUPP) ||
		errors.Is(err, unix.EINVAL) ||
		errors.Is(err, unix.EXDEV) ||
		errors.Is(err, unix.ENOSYS) ||
		errors.Is(err, unix.EBADF) ||
		errors.Is(err, unix.ETXTBSY)
}
