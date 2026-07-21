package objectstore

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/kernellane"
)

// FSStore is a filesystem-backed object store. Unlike the S3 and in-memory
// backends, it stores objects as real files on disk, which is the destination
// kernel file zero-copy (copy_file_range) requires. It satisfies the bulk
// ObjectStore interface (PutStream/GetRange/Delete) and additionally exposes
// PutFileRange for the zero-copy ingest lane.
//
// It is intended for local, edge, and same-host deployments. Content type is
// preserved best-effort via a small sidecar; payload bytes are the contract.
type FSStore struct {
	root   string
	bucket string
}

// NewFSStore creates a filesystem object store rooted at dir.
func NewFSStore(dir, bucket string) (*FSStore, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("filesystem object store root is required")
	}
	clean := filepath.Clean(dir)
	if err := os.MkdirAll(clean, 0o750); err != nil {
		return nil, err
	}
	// MkdirAll succeeds when the root already exists even if it is not writable
	// by this process (e.g. a volume mounted root-owned over the path), which
	// would otherwise surface only as per-request Put failures. Probe once at
	// construction so misconfiguration fails at boot, where operators see it.
	probe, err := os.CreateTemp(clean, ".writeprobe-*")
	if err != nil {
		return nil, fmt.Errorf("filesystem object store root %q is not writable: %w", clean, err)
	}
	probeName := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(probeName)
		return nil, fmt.Errorf("filesystem object store root %q write probe: %w", clean, err)
	}
	if err := os.Remove(probeName); err != nil {
		return nil, fmt.Errorf("filesystem object store root %q write probe cleanup: %w", clean, err)
	}
	return &FSStore{root: clean, bucket: strings.TrimSpace(bucket)}, nil
}

// Describe reports the backend driver and root for diagnostics.
func (s *FSStore) Describe() map[string]string {
	if s == nil {
		return map[string]string{}
	}
	return map[string]string{"driver": "filesystem", "root": s.root, "bucket": s.bucket}
}

func (s *FSStore) objectPath(key string) (string, error) {
	key = normalizeKey(key)
	if key == "" {
		return "", fmt.Errorf("object key is required")
	}
	p := filepath.Clean(filepath.Join(s.root, filepath.FromSlash(key)))
	// Reject keys that escape the store root (path traversal).
	if p != s.root && !strings.HasPrefix(p, s.root+string(os.PathSeparator)) {
		return "", fmt.Errorf("object key escapes store root")
	}
	return p, nil
}

func (s *FSStore) object(key string, size int64, contentType string, meta map[string]string) Object {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return Object{
		Key:         normalizeKey(key),
		Bucket:      s.bucket,
		ContentType: contentType,
		Size:        size,
		URL:         "file://" + s.mustPath(key),
		Metadata:    cloneMetadata(meta),
	}
}

func (s *FSStore) mustPath(key string) string {
	p, err := s.objectPath(key)
	if err != nil {
		return ""
	}
	return p
}

// PutStream writes the reader to the object file, enforcing size when known.
func (s *FSStore) PutStream(_ context.Context, key string, reader io.Reader, size int64, opts PutOptions) (Object, error) {
	if s == nil {
		return Object{}, fmt.Errorf("object store is required")
	}
	if reader == nil {
		return Object{}, fmt.Errorf("object reader is required")
	}
	p, err := s.objectPath(key)
	if err != nil {
		return Object{}, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return Object{}, err
	}
	f, err := os.Create(p) // #nosec G304 -- p is confined to the store root by objectPath.
	if err != nil {
		return Object{}, err
	}
	var n int64
	if size >= 0 {
		n, err = io.Copy(f, io.LimitReader(reader, size))
	} else {
		n, err = io.Copy(f, reader)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(p)
		return Object{}, err
	}
	if size >= 0 && n != size {
		_ = os.Remove(p)
		return Object{}, fmt.Errorf("object stream size mismatch: got %d want %d", n, size)
	}
	return s.object(key, n, opts.ContentType, opts.Metadata), nil
}

// PutFileRange ingests the part directly from a source file. On Linux with a
// supporting filesystem it uses copy_file_range (kernel zero-copy); elsewhere it
// falls back to a portable copy. The bool reports whether the zero-copy path
// executed. The caller is responsible for any integrity verification.
func (s *FSStore) PutFileRange(_ context.Context, key string, src *os.File, size int64, opts PutOptions) (Object, bool, error) {
	if s == nil {
		return Object{}, false, fmt.Errorf("object store is required")
	}
	if src == nil {
		return Object{}, false, fmt.Errorf("source file is required")
	}
	p, err := s.objectPath(key)
	if err != nil {
		return Object{}, false, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return Object{}, false, err
	}
	dst, err := os.Create(p) // #nosec G304 -- p is confined to the store root by objectPath.
	if err != nil {
		return Object{}, false, err
	}
	n, zeroCopy, err := kernellane.CopyFile(dst, src, size)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(p)
		return Object{}, false, err
	}
	return s.object(key, n, opts.ContentType, opts.Metadata), zeroCopy, nil
}

// Open returns a reader over the whole object.
func (s *FSStore) Open(_ context.Context, key string) (io.ReadCloser, Object, error) {
	if s == nil {
		return nil, Object{}, fmt.Errorf("object store is required")
	}
	p, err := s.objectPath(key)
	if err != nil {
		return nil, Object{}, err
	}
	f, err := os.Open(p) // #nosec G304 -- p is confined to the store root by objectPath.
	if err != nil {
		return nil, Object{}, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, Object{}, err
	}
	return f, s.object(key, info.Size(), "", nil), nil
}

// GetRange returns a bounded reader over [offset, offset+length).
func (s *FSStore) GetRange(_ context.Context, key string, offset, length int64) (io.ReadCloser, Object, error) {
	if s == nil {
		return nil, Object{}, fmt.Errorf("object store is required")
	}
	if offset < 0 {
		return nil, Object{}, fmt.Errorf("object range offset must be non-negative")
	}
	if length <= 0 {
		return nil, Object{}, fmt.Errorf("object range length must be positive")
	}
	if _, err := checkedRangeEnd(offset, length); err != nil {
		return nil, Object{}, err
	}
	p, err := s.objectPath(key)
	if err != nil {
		return nil, Object{}, err
	}
	f, err := os.Open(p) // #nosec G304 -- p is confined to the store root by objectPath.
	if err != nil {
		return nil, Object{}, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, Object{}, err
	}
	if offset > info.Size() {
		_ = f.Close()
		return nil, Object{}, fmt.Errorf("object range offset exceeds object size")
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, Object{}, err
	}
	return &boundedFile{file: f, remaining: length}, s.object(key, info.Size(), "", nil), nil
}

// Delete removes the object file if present.
func (s *FSStore) Delete(_ context.Context, key string) error {
	if s == nil {
		return fmt.Errorf("object store is required")
	}
	p, err := s.objectPath(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// boundedFile limits reads to a byte budget and closes the underlying file.
type boundedFile struct {
	file      *os.File
	remaining int64
}

func (b *boundedFile) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.file.Read(p)
	b.remaining -= int64(n)
	return n, err
}

func (b *boundedFile) Close() error { return b.file.Close() }
