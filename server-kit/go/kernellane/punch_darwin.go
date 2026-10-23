//go:build darwin

package kernellane

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// fPunchhole is Darwin's F_PUNCHHOLE command constant (99) from sys/fcntl.h.
const fPunchhole = 99

// fpunchhole corresponds to Darwin's C struct fpunchhole:
//
//	typedef struct fpunchhole {
//	    unsigned int fp_flags; /* unused */
//	    unsigned int reserved; /* round to 8-byte boundary */
//	    off_t fp_offset;
//	    off_t fp_length;
//	} fpunchhole_t;
type fpunchhole struct {
	flags    uint32
	reserved uint32
	offset   int64
	length   int64
}

// kernelPunchHole deallocates physical blocks on Darwin APFS using fcntl(F_PUNCHHOLE).
// It returns (true, nil) on success, or (false, nil) if the filesystem (e.g. HFS+,
// exFAT, FAT32) or OS does not support sparse hole punching.
func kernelPunchHole(f *os.File, offset, length int64) (bool, error) {
	hole := fpunchhole{
		offset: offset,
		length: length,
	}
	_, _, errno := unix.Syscall(
		unix.SYS_FCNTL,
		f.Fd(),
		uintptr(fPunchhole),
		uintptr(unsafe.Pointer(&hole)),
	)
	if errno == 0 {
		return true, nil
	}
	if isUnsupportedHolePunch(errno) {
		return false, nil
	}
	return false, errno
}

func isUnsupportedHolePunch(err error) bool {
	return errors.Is(err, unix.ENOTSUP) ||
		errors.Is(err, unix.EOPNOTSUPP) ||
		errors.Is(err, unix.EINVAL) ||
		errors.Is(err, unix.ENOSYS) ||
		errors.Is(err, unix.EPERM)
}
