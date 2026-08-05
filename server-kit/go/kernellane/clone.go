package kernellane

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// Clone lane names. A clone reports which mechanism actually moved the bytes,
// because "we have a fast path" is not evidence that the fast path ran.
const (
	// CloneLaneReflink is a copy-on-write clone (FICLONE): O(1) metadata, zero
	// data blocks moved. Requires filesystem support — XFS and Btrfs provide
	// it; ext4 and overlayfs refuse it, which is a fallback, not an error.
	CloneLaneReflink = "reflink"
	// CloneLaneCopyFileRange moves bytes inside the kernel page cache without
	// round-tripping them through a userspace buffer.
	CloneLaneCopyFileRange = "copy_file_range"
	// CloneLaneUserspace is the portable fallback available on every platform.
	CloneLaneUserspace = "userspace"
)

// CloneFile copies srcPath to dstPath using the fastest lane this host and
// filesystem actually support, and reports which lane ran.
//
// The lanes form a refinement ladder — reflink, then copy_file_range, then a
// portable userspace copy. Every lane produces byte-identical output, so lane
// selection is purely a cost decision and a failed accelerator is never fatal.
// This is the same shape as CopyFile and the MPTCP dialer: a real kernel path,
// a capability probe, and a behaviour-preserving fallback, with no OS-specific
// type in the public API.
func CloneFile(dstPath, srcPath string) (lane string, err error) {
	srcFile, err := os.Open(srcPath) // #nosec G304 -- caller-owned store paths.
	if err != nil {
		return "", fmt.Errorf("open clone source: %w", err)
	}
	defer func() { _ = srcFile.Close() }()

	dstFile, err := os.OpenFile(dstPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) // #nosec G304
	if err != nil {
		return "", fmt.Errorf("create clone target: %w", err)
	}
	defer func() { _ = dstFile.Close() }()

	lane, ok, kerr := kernelCloneFile(dstFile, srcFile)
	if kerr != nil {
		return "", kerr
	}
	if ok {
		return lane, nil
	}
	return userspaceClone(dstFile, srcFile)
}

// userspaceClone is the portable lane: a plain buffered stream through
// userspace. It is the semantics baseline every kernel lane must match.
func userspaceClone(dst, src *os.File) (string, error) {
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	if _, err := dst.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	if _, err := io.Copy(dst, src); err != nil {
		return "", fmt.Errorf("userspace clone copy: %w", err)
	}
	return CloneLaneUserspace, nil
}

var cloneProbe struct {
	once sync.Once
	lane string
}

// BestCloneLane reports, with a cached one-shot probe, the fastest clone lane
// that actually works on the host's temp filesystem. Callers use it for
// capability reporting and benchmark annotation; CloneFile selects its own lane
// per call, because the answer can differ per filesystem.
func BestCloneLane() string {
	cloneProbe.once.Do(func() { cloneProbe.lane = probeCloneLane() })
	return cloneProbe.lane
}

func probeCloneLane() string { return probeCloneLaneIn("") }

// probeCloneLaneIn runs the probe inside base ("" selects the OS temp dir).
// The seam exists so the probe's own failure paths are testable: kernellane
// promises that a failed accelerator degrades rather than errors, and that
// promise has to hold for the capability probe too — a probe that cannot fail
// safely is not a fallback.
func probeCloneLaneIn(base string) string {
	dir, err := os.MkdirTemp(base, "kl-clone-probe-*")
	if err != nil {
		return CloneLaneUserspace
	}
	defer func() { _ = os.RemoveAll(dir) }()

	src := dir + "/src"
	if err := os.WriteFile(src, []byte("kernellane-clone-probe"), 0o600); err != nil {
		return CloneLaneUserspace
	}
	lane, err := CloneFile(dir+"/dst", src)
	if err != nil {
		return CloneLaneUserspace
	}
	return lane
}
