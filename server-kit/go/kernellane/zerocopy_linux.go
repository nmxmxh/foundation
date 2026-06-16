//go:build linux

package kernellane

import (
	"os"

	"golang.org/x/sys/unix"
)

// kernelCopyFile uses copy_file_range(2) to move bytes between two files inside
// the kernel. When the syscall or filesystem does not support it (older kernel,
// cross-device, special files) it reports errZeroCopyUnsupported so CopyFile can
// fall back without surfacing an error.
func kernelCopyFile(dst, src *os.File, size int64) (int64, bool, error) {
	var total int64
	remaining := size
	for remaining > 0 {
		n, err := unix.CopyFileRange(int(src.Fd()), nil, int(dst.Fd()), nil, int(remaining), 0)
		switch err {
		case nil:
		case unix.ENOSYS, unix.EXDEV, unix.EINVAL, unix.EOPNOTSUPP, unix.EPERM, unix.EBADF:
			if total == 0 {
				return 0, false, errZeroCopyUnsupported
			}
			return total, false, err
		default:
			return total, false, err
		}
		if n == 0 {
			break // source exhausted before size; report what we copied.
		}
		total += int64(n)
		remaining -= int64(n)
	}
	return total, true, nil
}
