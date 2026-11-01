//go:build linux || darwin

package kernellane

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// kernelHoleAtStart asks the filesystem with SEEK_DATA whether block 0 is a hole.
// The kernel extent map is the answer, so a file that starts with zero bytes is not a hole.
// supported is false when the filesystem cannot answer. The file offset is restored.
func kernelHoleAtStart(f *os.File, size int64) (hole, supported bool, err error) {
	current, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return false, false, err
	}
	dataOffset, seekErr := unix.Seek(int(f.Fd()), 0, unix.SEEK_DATA)
	if _, err := f.Seek(current, io.SeekStart); err != nil {
		return false, false, err
	}
	switch {
	case seekErr == nil:
		return dataOffset >= min(size, BlockSize), true, nil
	case errors.Is(seekErr, unix.ENXIO):
		// No data after offset 0: the whole file is a hole.
		return true, true, nil
	case errors.Is(seekErr, unix.EINVAL), errors.Is(seekErr, unix.ENOTSUP), errors.Is(seekErr, unix.EOPNOTSUPP):
		return false, false, nil
	default:
		return false, false, seekErr
	}
}
