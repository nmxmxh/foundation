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

// newSharedMemoryFile creates a shared region without mapping it.
//
// Two processes agree on a region either through mappings or through file I/O,
// and mixing the two is not safe. A kernel that forbids unsafe code cannot mmap,
// so it reads and writes its arena with pread/pwrite; if this side also holds a
// MAP_SHARED mapping of the same file, the two views drift once the kernel
// writes — on darwin the host's subsequent stagings become invisible to the
// kernel, which reports a freshly published descriptor as FREE. The failure
// looks like a protocol race and is neither, so the arena simply does not map:
// both ends use positional file I/O and the file system keeps them coherent.
func newSharedMemoryFile(dir string, size int) (*sharedMemorySegment, error) {
	segment, err := newSharedMemorySegment(dir, size)
	if err != nil {
		return nil, err
	}
	if len(segment.raw) > 0 {
		if err := syscall.Munmap(segment.raw); err != nil {
			_ = segment.Close()
			return nil, fmt.Errorf("unmap arena segment: %w", err)
		}
		segment.raw = nil
	}
	return segment, nil
}

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

// Sync flushes mapped writes so a reader using ordinary file I/O sees them.
//
// The host writes the arena through an mmap; a kernel reads it with positional
// reads (`pread`). POSIX guarantees those views are coherent for MAP_SHARED, but
// only after the mapping's dirty pages are written back — without an explicit
// msync the reader can observe a partially updated region, which surfaces as a
// descriptor that is addressable but still reads FREE. That failure is
// intermittent and load-dependent, which is the worst kind: it looks like a race
// in the protocol rather than a missing flush.
func (s *sharedMemorySegment) Sync() error {
	if s == nil || len(s.raw) == 0 {
		return nil
	}
	return unix_msync(s.raw)
}

// WriteAt publishes staged bytes through the file.
//
// The counterpart to ReadAt: the arena stages into an ordinary buffer and
// publishes with one positional write, so the kernel's pread sees exactly what
// was staged. See newSharedMemoryFile for why this is not an mmap.
func (s *sharedMemorySegment) WriteAt(src []byte, offset int64) error {
	if s == nil || s.file == nil {
		return fmt.Errorf("shared memory segment is not open")
	}
	if len(src) == 0 {
		return nil
	}
	_, err := s.file.WriteAt(src, offset)
	return err
}

// ReadAt reads through the file rather than the mapping.
//
// Needed because the two ends of an exchange use different APIs: the host stages
// through its mmap, while a kernel forbidden from unsafe code reads and writes
// with positional file I/O. A result the kernel wrote with pwrite is not visible
// through the host's existing mapping — the mapped pages are older and, worse, a
// subsequent msync of that mapping would write them back over the kernel's
// result. Reading the result through the file avoids both.
func (s *sharedMemorySegment) ReadAt(dst []byte, offset int64) error {
	if s == nil || s.file == nil {
		return fmt.Errorf("shared memory segment is not open")
	}
	_, err := s.file.ReadAt(dst, offset)
	return err
}
