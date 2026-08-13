//go:build linux || darwin

package runtimehost

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

// Shared memory backs two different regions, and the distinction matters.
//
// The control buffer is BUFFER_TOTAL_BYTES and deliberately tiny so it stays
// resident in L1/L2. The arena is a separate, much larger mapping for bulk
// payloads. Both are file-backed mappings created here; only the size differs,
// which is why this takes a size rather than assuming the control buffer's.
//
// Linux and darwin share one implementation. They differ only in where a mapping
// should live: Linux has a tmpfs at /dev/shm, darwin has none and uses the
// system temp directory. An mmap of a regular file with MAP_SHARED is shared
// memory on both, so the mechanism is identical — only the path changes.
type sharedMemorySegment struct {
	file *os.File
	path string
	raw  []byte
}

func sharedMemorySupported(dir string) bool {
	segment, err := newSharedMemorySegment(dir, int(generated.BUFFER_TOTAL_BYTES))
	if err != nil {
		return false
	}
	_ = segment.Close()
	return true
}

// Both regions are mapped, and that is now the whole rule.
//
// It was not always. Two processes agree on a region either through mappings or
// through positional file I/O, and mixing the two is not safe: a MAP_SHARED
// writer against a pread reader drifts, and on darwin the symptom is that a
// freshly published descriptor reads back as FREE — a failure that looks like a
// protocol race and is not one. The arena used to sidestep this by not mapping
// at all, because the Rust kernel is `#![forbid(unsafe_code)]` and could not
// mmap. It can now, through a safe wrapper in `ovrt-core`, so both ends of both
// regions map and the mismatch has nowhere left to occur. The staging buffer,
// the per-exchange publish copy, and the msync that made them coherent are all
// gone with it.

// newSharedMemorySegment creates and maps a shared region of the given size.
func newSharedMemorySegment(dir string, size int) (*sharedMemorySegment, error) {
	if size <= 0 {
		return nil, fmt.Errorf("shared memory segment size must be positive, got %d", size)
	}
	baseDir := normalizeSharedMemoryDir(dir)
	if err := os.MkdirAll(baseDir, 0o700); err != nil && !os.IsExist(err) {
		return nil, fmt.Errorf("prepare shared memory directory %q: %w", baseDir, err)
	}
	file, err := os.CreateTemp(baseDir, "ovrt-runtime-*")
	if err != nil {
		return nil, fmt.Errorf("create shared memory segment: %w", err)
	}
	cleanup := func(cause error) (*sharedMemorySegment, error) {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, cause
	}
	if err := file.Truncate(int64(size)); err != nil {
		return cleanup(fmt.Errorf("size shared memory segment to %d bytes: %w", size, err))
	}
	raw, err := syscall.Mmap(
		int(file.Fd()),
		0,
		size,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_SHARED,
	)
	if err != nil {
		return cleanup(fmt.Errorf("map shared memory segment of %d bytes: %w", size, err))
	}
	return &sharedMemorySegment{
		file: file,
		path: file.Name(),
		raw:  raw,
	}, nil
}

// normalizeSharedMemoryDir picks where a mapping is created.
//
// An explicit directory always wins. Otherwise Linux prefers /dev/shm, which is
// tmpfs and never reaches a disk; darwin has no such mount and uses the system
// temp directory. The fallback also covers a Linux container built without
// /dev/shm, where insisting on it would disable the transport outright rather
// than degrade to a working one.
func normalizeSharedMemoryDir(dir string) string {
	if cleaned := strings.TrimSpace(dir); cleaned != "" {
		return filepath.Clean(cleaned)
	}
	if info, err := os.Stat("/dev/shm"); err == nil && info.IsDir() {
		return "/dev/shm"
	}
	return filepath.Clean(os.TempDir())
}

func (s *sharedMemorySegment) Close() error {
	if s == nil {
		return nil
	}
	var firstErr error
	if len(s.raw) > 0 {
		if err := syscall.Munmap(s.raw); err != nil {
			firstErr = err
		}
		s.raw = nil
	}
	if s.file != nil {
		if err := s.file.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
		s.file = nil
	}
	return firstErr
}

// Sync, WriteAt and ReadAt used to live here, to keep a mapped host and a
// positional kernel in step. Both ends map now, so a store is visible to the
// peer with no flush and no second copy, and the three of them had no callers
// left. `msync_unix.go` went with them.
