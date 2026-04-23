//go:build linux

package runtimehost

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

type sharedMemorySegment struct {
	file *os.File
	path string
	raw  []byte
}

func sharedMemorySupported(dir string) bool {
	segment, err := newSharedMemorySegment(dir)
	if err != nil {
		return false
	}
	_ = segment.Close()
	return true
}

func newSharedMemorySegment(dir string) (*sharedMemorySegment, error) {
	baseDir := normalizeSharedMemoryDir(dir)
	file, err := os.CreateTemp(baseDir, "ovrt-runtime-*")
	if err != nil {
		return nil, fmt.Errorf("create shared memory segment: %w", err)
	}
	cleanup := func(cause error) (*sharedMemorySegment, error) {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, cause
	}
	if err := file.Truncate(int64(generated.BUFFER_TOTAL_BYTES)); err != nil {
		return cleanup(fmt.Errorf("size shared memory segment: %w", err))
	}
	raw, err := syscall.Mmap(
		int(file.Fd()),
		0,
		int(generated.BUFFER_TOTAL_BYTES),
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_SHARED,
	)
	if err != nil {
		return cleanup(fmt.Errorf("map shared memory segment: %w", err))
	}
	return &sharedMemorySegment{
		file: file,
		path: file.Name(),
		raw:  raw,
	}, nil
}

func normalizeSharedMemoryDir(dir string) string {
	cleaned := strings.TrimSpace(dir)
	if cleaned == "" {
		cleaned = "/dev/shm"
	}
	return filepath.Clean(cleaned)
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
