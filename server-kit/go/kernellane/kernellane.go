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
//
// These primitives never expose OS-specific types in their public API, and a
// failed accelerator is never fatal — it degrades to the portable path.
package kernellane

import (
	"errors"
	"io"
	"os"
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
