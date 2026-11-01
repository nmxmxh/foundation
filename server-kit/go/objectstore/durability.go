package objectstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

// ErrNotDurable reports a store that cannot keep an object across a crash.
var ErrNotDurable = errors.New("objectstore: store does not keep objects across a crash")

// SyncObject makes a stored object durable.
//
// An S3 PUT is durable when it returns success, so S3 returns nil.
// The memory store returns ErrNotDurable. Callers that destroy their only
// other copy, such as bulk source reclamation, must stop on that error.
func (s *Store) SyncObject(_ context.Context, key string) error {
	if s == nil {
		return fmt.Errorf("object store is required")
	}
	if normalizeKey(key) == "" {
		return fmt.Errorf("object key is required")
	}
	if s.memory {
		return ErrNotDurable
	}
	return nil
}

// SyncObject flushes the object file and its directory entry to stable storage.
// PutStream does not sync, so a caller that needs durability calls this after it.
func (s *FSStore) SyncObject(_ context.Context, key string) error {
	if s == nil {
		return fmt.Errorf("object store is required")
	}
	p, err := s.objectPath(key)
	if err != nil {
		return err
	}
	f, err := os.Open(p) // #nosec G304 -- p is confined to the store root by objectPath.
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	return syncParentDir(filepath.Dir(p))
}

// syncParentDir makes a new directory entry durable.
// Windows cannot sync a directory handle, and EINVAL means the filesystem does not support it.
func syncParentDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir) // #nosec G304 -- dir is inside the store root.
	if err != nil {
		return err
	}
	syncErr := d.Sync()
	closeErr := d.Close()
	if syncErr != nil && !errors.Is(syncErr, syscall.EINVAL) {
		return syncErr
	}
	return closeErr
}
