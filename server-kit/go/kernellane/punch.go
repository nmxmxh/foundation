package kernellane

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"
)

// BlockSize is the standard 4 KiB filesystem allocation unit used for extent alignment.
const BlockSize int64 = 4096

// BlockMask is the bitmask used to round down byte offsets to BlockSize.
const BlockMask int64 = ^(BlockSize - 1)

// ErrInvalidRange reports an offset or length that is negative or overflows.
var ErrInvalidRange = errors.New("kernellane: invalid punch hole range")

// PunchHole deallocates physical storage backing the range [offset, offset+length)
// in f without changing the logical file size.
//
// The return value punched reports whether physical blocks were deallocated by
// the kernel. On filesystems or operating systems that do not support sparse
// allocation, it degrades to (false, nil). Callers never need a platform branch.
// An error is returned only for genuine I/O or descriptor failures.
func PunchHole(f *os.File, offset, length int64) (punched bool, err error) {
	if f == nil {
		return false, errors.New("kernellane: nil file descriptor")
	}
	if offset < 0 || length <= 0 || offset > math.MaxInt64-length {
		return false, ErrInvalidRange
	}
	return kernelPunchHole(f, offset, length)
}

// PunchHolePrefix deallocates the prefix range [0, upToOffset) in f. It rounds
// down to the nearest BlockSize (4 KiB) boundary to preserve filesystem extents.
// If upToOffset is less than BlockSize, it returns (false, nil).
func PunchHolePrefix(f *os.File, upToOffset int64) (punched bool, err error) {
	alignedLen := upToOffset & BlockMask
	if alignedLen <= 0 {
		return false, nil
	}
	return PunchHole(f, 0, alignedLen)
}

var holePunchProbe struct {
	once sync.Once
	ok   bool
}

// HolePunchSupported reports whether the host temporary filesystem supports
// hole punching. The probe result is cached after the first execution.
func HolePunchSupported() bool {
	holePunchProbe.once.Do(func() { holePunchProbe.ok = probeHolePunchInDir("") })
	return holePunchProbe.ok
}

// HolePunchSupportedFile reports whether the filesystem hosting the given file
// supports hole punching.
func HolePunchSupportedFile(f *os.File) bool {
	if f == nil {
		return false
	}
	name := f.Name()
	if name == "" {
		return HolePunchSupported()
	}
	return HolePunchSupportedInDir(filepath.Dir(name))
}

// HolePunchSupportedInDir probes whether the directory filesystem supports hole
// punching. It creates a temporary file and exercises a single block punch.
func HolePunchSupportedInDir(dir string) bool {
	return probeHolePunchInDir(dir)
}

func probeHolePunchInDir(dir string) bool {
	f, err := os.CreateTemp(dir, "kl-hp-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	defer func() {
		_ = f.Close()
		_ = os.Remove(name)
	}()

	payload := make([]byte, BlockSize*2)
	for i := range payload {
		payload[i] = 0x55
	}
	if _, err := f.Write(payload); err != nil {
		return false
	}
	if err := f.Sync(); err != nil {
		return false
	}

	punched, err := kernelPunchHole(f, 0, BlockSize)
	return err == nil && punched
}
