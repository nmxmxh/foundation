//go:build linux

package kernellane

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// kernelCloneFile tries the Linux kernel clone lanes fastest-first. It reports
// (lane, true, nil) when a kernel lane moved the bytes, (\"\", false, nil) when
// no kernel lane applies and the caller should fall back, and a non-nil error
// only for genuine failures — never for "this filesystem does not support it".
func kernelCloneFile(dst, src *os.File) (string, bool, error) {
	if err := unix.IoctlFileClone(int(dst.Fd()), int(src.Fd())); err == nil {
		return CloneLaneReflink, true, nil
	} else if !laneUnsupported(err) {
		return "", false, err
	}

	if err := copyFileRangeAll(dst, src); err == nil {
		return CloneLaneCopyFileRange, true, nil
	} else if !laneUnsupported(err) {
		return "", false, err
	}

	return "", false, nil
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

// laneUnsupported reports the errno classes that mean "this lane does not exist
// here" rather than "the copy failed": wrong filesystem, cross-device, old
// kernel, or unsupported file type.
func laneUnsupported(err error) bool {
	return errors.Is(err, unix.ENOTSUP) ||
		errors.Is(err, unix.EOPNOTSUPP) ||
		errors.Is(err, unix.EINVAL) ||
		errors.Is(err, unix.EXDEV) ||
		errors.Is(err, unix.ENOSYS) ||
		errors.Is(err, unix.EBADF) ||
		errors.Is(err, unix.ETXTBSY)
}
