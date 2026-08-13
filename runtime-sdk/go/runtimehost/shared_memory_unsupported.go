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

func (s *sharedMemorySegment) Close() error {
	return nil
}
