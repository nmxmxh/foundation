package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newFSStore(t *testing.T) *FSStore {
	t.Helper()
	s, err := NewFSStore(t.TempDir(), "bucket")
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	return s
}

func TestNewFSStoreRequiresRoot(t *testing.T) {
	if _, err := NewFSStore("  ", "b"); err == nil {
		t.Fatal("empty root should error")
	}
	s := newFSStore(t)
	if s.Describe()["driver"] != "filesystem" {
		t.Fatalf("describe = %v", s.Describe())
	}
}

func TestNewFSStoreRejectsUnwritableRoot(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := filepath.Join(t.TempDir(), "readonly")
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(root, 0o550); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o750) })
	// Regression: a pre-existing root the process cannot write to (e.g. a
	// root-owned volume mounted over the path) must fail at construction, not
	// on the first Put.
	if _, err := NewFSStore(root, "b"); err == nil {
		t.Fatal("unwritable root should error at construction")
	} else if !strings.Contains(err.Error(), "not writable") {
		t.Fatalf("error should name the writability failure, got: %v", err)
	}
}

func TestFSStorePutStreamReadBackAndDelete(t *testing.T) {
	s := newFSStore(t)
	ctx := context.Background()
	payload := []byte("filesystem-object-store")

	obj, err := s.PutStream(ctx, "a/b/obj.bin", bytes.NewReader(payload), int64(len(payload)), PutOptions{ContentType: "text/plain"})
	if err != nil {
		t.Fatalf("PutStream() error = %v", err)
	}
	if obj.Size != int64(len(payload)) || obj.ContentType != "text/plain" {
		t.Fatalf("object = %+v", obj)
	}

	r, _, err := s.Open(ctx, "a/b/obj.bin")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	got, _ := io.ReadAll(r)
	_ = r.Close()
	if !bytes.Equal(got, payload) {
		t.Fatalf("read back %q want %q", got, payload)
	}

	if err := s.Delete(ctx, "a/b/obj.bin"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := s.Delete(ctx, "a/b/obj.bin"); err != nil {
		t.Fatalf("Delete() of missing object should be nil, got %v", err)
	}
	if _, _, err := s.Open(ctx, "a/b/obj.bin"); err == nil {
		t.Fatal("Open after delete should error")
	}
}

func TestFSStorePutStreamSizeMismatch(t *testing.T) {
	s := newFSStore(t)
	if _, err := s.PutStream(context.Background(), "k", bytes.NewReader([]byte("abc")), 99, PutOptions{}); err == nil {
		t.Fatal("size mismatch should error")
	}
	if _, err := s.PutStream(context.Background(), "k", nil, 0, PutOptions{}); err == nil {
		t.Fatal("nil reader should error")
	}
	if _, err := s.PutStream(context.Background(), "", bytes.NewReader(nil), -1, PutOptions{}); err == nil {
		t.Fatal("empty key should error")
	}
}

func TestFSStorePutFileRangeMatchesSource(t *testing.T) {
	s := newFSStore(t)
	ctx := context.Background()
	payload := bytes.Repeat([]byte("range-copy "), 400)

	srcPath := filepath.Join(t.TempDir(), "src.bin")
	if err := os.WriteFile(srcPath, payload, 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	src, err := os.Open(srcPath)
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	defer func() { _ = src.Close() }()

	obj, zeroCopy, err := s.PutFileRange(ctx, "parts/0", src, int64(len(payload)), PutOptions{})
	if err != nil {
		t.Fatalf("PutFileRange() error = %v", err)
	}
	if obj.Size != int64(len(payload)) {
		t.Fatalf("object size = %d want %d", obj.Size, len(payload))
	}
	_ = zeroCopy // platform-dependent; correctness is the invariant here.

	r, _, err := s.Open(ctx, "parts/0")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	got, _ := io.ReadAll(r)
	_ = r.Close()
	if sha256.Sum256(got) != sha256.Sum256(payload) {
		t.Fatal("PutFileRange produced different bytes than the source")
	}

	if _, _, err := s.PutFileRange(ctx, "parts/0", nil, 0, PutOptions{}); err == nil {
		t.Fatal("nil source should error")
	}
}

func TestFSStoreGetRange(t *testing.T) {
	s := newFSStore(t)
	ctx := context.Background()
	payload := []byte("0123456789")
	if _, err := s.PutStream(ctx, "k", bytes.NewReader(payload), int64(len(payload)), PutOptions{}); err != nil {
		t.Fatalf("PutStream() error = %v", err)
	}

	r, _, err := s.GetRange(ctx, "k", 3, 4)
	if err != nil {
		t.Fatalf("GetRange() error = %v", err)
	}
	got, _ := io.ReadAll(r)
	_ = r.Close()
	if string(got) != "3456" {
		t.Fatalf("range = %q want 3456", got)
	}

	for _, bad := range []struct {
		off, length int64
	}{{-1, 4}, {0, 0}, {0, -1}} {
		if _, _, err := s.GetRange(ctx, "k", bad.off, bad.length); err == nil {
			t.Fatalf("GetRange(%d,%d) should error", bad.off, bad.length)
		}
	}
	if _, _, err := s.GetRange(ctx, "k", 100, 4); err == nil {
		t.Fatal("offset beyond size should error")
	}
}

func TestFSStoreRejectsPathTraversal(t *testing.T) {
	s := newFSStore(t)
	if _, err := s.objectPath("../escape"); err == nil {
		t.Fatal("path traversal key should be rejected")
	}
	if _, err := s.objectPath("a/../../escape"); err == nil {
		t.Fatal("nested traversal key should be rejected")
	}
	if _, err := s.objectPath(""); err == nil {
		t.Fatal("empty key should be rejected")
	}
}

func TestFSStoreNilReceiverGuards(t *testing.T) {
	var s *FSStore
	ctx := context.Background()
	if _, err := s.PutStream(ctx, "k", bytes.NewReader(nil), 0, PutOptions{}); err == nil {
		t.Fatal("nil store PutStream should error")
	}
	if _, _, err := s.PutFileRange(ctx, "k", nil, 0, PutOptions{}); err == nil {
		t.Fatal("nil store PutFileRange should error")
	}
	if _, _, err := s.Open(ctx, "k"); err == nil {
		t.Fatal("nil store Open should error")
	}
	if _, _, err := s.GetRange(ctx, "k", 0, 1); err == nil {
		t.Fatal("nil store GetRange should error")
	}
	if err := s.Delete(ctx, "k"); err == nil {
		t.Fatal("nil store Delete should error")
	}
	if len(s.Describe()) != 0 {
		t.Fatal("nil store Describe should be empty")
	}
}

func TestFSStoreOpenAndGetRangeMissing(t *testing.T) {
	s := newFSStore(t)
	ctx := context.Background()
	if _, _, err := s.Open(ctx, "missing"); err == nil {
		t.Fatal("Open of missing object should error")
	}
	if _, _, err := s.GetRange(ctx, "missing", 0, 1); err == nil {
		t.Fatal("GetRange of missing object should error")
	}
	if strings.TrimSpace(s.root) == "" {
		t.Fatal("store root should be set")
	}
}
