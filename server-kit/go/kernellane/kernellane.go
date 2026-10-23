// Package kernellane turns the bulk transfer "candidate" accelerators into real,
// capability-gated transport primitives with portable fallbacks. It follows the
// same discipline as the Go SIMD lane: a real path when the OS supports it, a
// runtime capability probe, and an automatic, behaviour-preserving fallback
// when it does not.
//
//   - Multipath TCP: stdlib (net.Dialer/ListenConfig.SetMultipathTCP). Real on
//     Linux with MPTCP enabled; transparently falls back to ordinary TCP
//     elsewhere. No build tags, no external dependency.
//   - Kernel file zero-copy: copy_file_range(2) on Linux (build-tagged), with a
//     portable io.Copy fallback on every other platform.
//   - Kernel hole punching: fallocate(2) on Linux and fcntl(2) F_PUNCHHOLE on
//     Darwin APFS, with safe fallback on unsupported filesystems.
//
// These primitives never expose OS-specific types in their public API, and a
// failed accelerator is never fatal — it degrades to the portable path.
package kernellane

import (
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
)

// errZeroCopyUnsupported signals that the kernel zero-copy path is unavailable
// and the caller should use the portable fallback. It is an internal sentinel,
// never returned from the public API.
var errZeroCopyUnsupported = errors.New("kernellane: kernel zero-copy unavailable on this platform")

// CopyFile copies size bytes from src to dst. On Linux with a supporting
// filesystem it uses the copy_file_range syscall (kernel zero-copy, no transfer
// through user space); on every other platform, or when the kernel/filesystem
// rejects the call, it falls back to a portable buffered copy. The zeroCopy
// return reports whether the kernel path actually executed.
func CopyFile(dst, src *os.File, size int64) (written int64, zeroCopy bool, err error) {
	if size < 0 {
		return 0, false, errors.New("kernellane: negative copy size")
	}
	if size == 0 {
		return 0, false, nil
	}
	n, ok, kerr := kernelCopyFile(dst, src, size)
	if kerr == nil {
		return n, ok, nil
	}
	if !errors.Is(kerr, errZeroCopyUnsupported) {
		return n, false, kerr
	}
	// Portable fallback: bytes are copied through user space but the result is
	// identical, which the parity test enforces.
	w, cerr := io.Copy(dst, io.LimitReader(src, size))
	return w, false, cerr
}

var zeroCopyProbe struct {
	once sync.Once
	ok   bool
}

// ZeroCopyFileSupported reports, with a cached one-shot probe, whether kernel
// file zero-copy actually works on this host. The probe performs a tiny
// copy_file_range between two temp files; any failure reports false.
func ZeroCopyFileSupported() bool {
	zeroCopyProbe.once.Do(func() { zeroCopyProbe.ok = probeZeroCopy() })
	return zeroCopyProbe.ok
}

func probeZeroCopy() bool {
	src, err := os.CreateTemp("", "kl-zc-src-*")
	if err != nil {
		return false
	}
	defer func() { _ = os.Remove(src.Name()); _ = src.Close() }()
	dst, err := os.CreateTemp("", "kl-zc-dst-*")
	if err != nil {
		return false
	}
	defer func() { _ = os.Remove(dst.Name()); _ = dst.Close() }()

	payload := []byte("kernellane-zero-copy-probe")
	if _, err := src.Write(payload); err != nil {
		return false
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return false
	}
	n, ok, err := kernelCopyFile(dst, src, int64(len(payload)))
	return err == nil && ok && n == int64(len(payload))
}

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
	defer func() {
		_ = os.Remove(f.Name())
		_ = f.Close()
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
