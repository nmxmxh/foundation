//go:build !linux

package runtimehost

import "fmt"

type sharedMemorySegment struct {
	path string
	raw  []byte
}

func sharedMemorySupported(_ string) bool {
	return false
}

func newSharedMemorySegment(_ string) (*sharedMemorySegment, error) {
	return nil, fmt.Errorf("shared memory transport is only supported on linux")
}

func (s *sharedMemorySegment) Close() error {
	return nil
}
