//go:build !linux && !darwin

package runtimehost

import "fmt"

// Shared memory is a POSIX mmap of a file with MAP_SHARED. Platforms without it
// fall back to the stdio transport, which is correct but copies the control
// buffer per exchange and cannot carry an arena.
type sharedMemorySegment struct {
	path string
	raw  []byte
}

func sharedMemorySupported(_ string) bool {
	return false
}

func newSharedMemorySegment(_ string, _ int) (*sharedMemorySegment, error) {
	return nil, fmt.Errorf("shared memory transport requires linux or darwin")
}

// newSharedMemoryFile mirrors the unix constructor's signature so process_pool
// compiles on platforms without shared memory. It can never succeed here because
// newSharedMemorySegment already refuses.
func newSharedMemoryFile(_ string, _ int) (*sharedMemorySegment, error) {
	return nil, fmt.Errorf("shared memory transport requires linux or darwin")
}

func (s *sharedMemorySegment) Close() error {
	return nil
}

func (s *sharedMemorySegment) Sync() error {
	return fmt.Errorf("shared memory transport requires linux or darwin")
}

func (s *sharedMemorySegment) WriteAt(_ []byte, _ int64) error {
	return fmt.Errorf("shared memory transport requires linux or darwin")
}

func (s *sharedMemorySegment) ReadAt(_ []byte, _ int64) error {
	return fmt.Errorf("shared memory transport requires linux or darwin")
}
