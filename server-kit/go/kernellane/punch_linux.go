//go:build linux

package kernellane

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// kernelPunchHole deallocates physical blocks on Linux filesystems (ext4, XFS,
// btrfs, tmpfs) using fallocate(FALLOC_FL_PUNCH_HOLE | FALLOC_FL_KEEP_SIZE).
// It returns (true, nil) on success, or (false, nil) if the filesystem does
// not support sparse hole punching.
func kernelPunchHole(f *os.File, offset, length int64) (bool, error) {
	err := unix.Fallocate(int(f.Fd()), unix.FALLOC_FL_PUNCH_HOLE|unix.FALLOC_FL_KEEP_SIZE, offset, length)
	if err == nil {
		return true, nil
	}
	if isUnsupportedHolePunch(err) {
		return false, nil
	}
	return false, err
}

func isUnsupportedHolePunch(err error) bool {
	return errors.Is(err, unix.ENOTSUP) ||
		errors.Is(err, unix.EOPNOTSUPP) ||
		errors.Is(err, unix.EINVAL) ||
		errors.Is(err, unix.ENOSYS) ||
		errors.Is(err, unix.EPERM)
}
