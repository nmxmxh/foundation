package hermessnapshot

// Error-path coverage for the FileStore: cancelled contexts, unwritable
// roots, malformed pointers, and comparison edges. These paths are the
// controlled-error half of the CP-04 contract.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/objectstore"
)

// memoryObjectStoreTB adapts the store_test helper shape for testing.TB users.
func memoryObjectStoreTB(t *testing.T) *objectstore.Store {
	t.Helper()
	return memoryObjectStore(t)
}

func objectstoreputopts() objectstore.PutOptions { return objectstore.PutOptions{} }

func TestFileStoreContextCancellation(t *testing.T) {
	store := newTestFileStore(t)
	payload := []byte("payload")
	desc := fileDesc("hp:p", 1, 1, payload)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	if err := store.Save(cancelled, desc, payload); !errors.Is(err, context.Canceled) {
		t.Fatalf("Save err = %v, want context.Canceled", err)
	}
	if _, _, _, err := store.Latest(cancelled, "hp:p"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Latest err = %v, want context.Canceled", err)
	}
	if _, _, _, err := store.OpenArtifact(cancelled, "hp:p"); !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenArtifact err = %v, want context.Canceled", err)
	}
	if _, _, err := store.PromoteLatest(cancelled, newTestFileStore(t), "hp:p"); !errors.Is(err, context.Canceled) {
		t.Fatalf("PromoteLatest err = %v, want context.Canceled", err)
	}
}

func TestNewFileStoreValidation(t *testing.T) {
	if _, err := NewFileStore("   "); err == nil {
		t.Fatal("blank root must error")
	}
	// A root whose parent is an existing *file* cannot be created.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	if _, err := NewFileStore(filepath.Join(blocker, "root")); err == nil {
		t.Fatal("root under a file must error")
	}
}

func TestFileStorePointerDecodeError(t *testing.T) {
	ctx := context.Background()
	store := newTestFileStore(t)
	if err := os.MkdirAll(store.scopeDir("hp:p"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(store.latestPath("hp:p"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write pointer: %v", err)
	}
	if _, _, _, err := store.Latest(ctx, "hp:p"); err == nil {
		t.Fatal("malformed pointer must error")
	}
	if _, _, _, err := store.OpenArtifact(ctx, "hp:p"); err == nil {
		t.Fatal("malformed pointer must error on OpenArtifact")
	}
	if _, _, err := store.PromoteLatest(ctx, newTestFileStore(t), "hp:p"); err == nil {
		t.Fatal("malformed pointer must error on PromoteLatest")
	}
}

func TestFileStoreMissingArtifactBehindPointer(t *testing.T) {
	ctx := context.Background()
	store := newTestFileStore(t)
	payload := []byte("payload")
	desc := fileDesc("hp:p", 1, 1, payload)
	if err := store.Save(ctx, desc, payload); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.Remove(store.artifactPath(desc)); err != nil {
		t.Fatalf("remove artifact: %v", err)
	}
	if _, _, _, err := store.Latest(ctx, "hp:p"); err == nil {
		t.Fatal("missing artifact must error")
	}
	if _, _, _, err := store.OpenArtifact(ctx, "hp:p"); err == nil {
		t.Fatal("missing artifact must error on OpenArtifact")
	}
	if _, _, err := store.PromoteLatest(ctx, newTestFileStore(t), "hp:p"); err == nil {
		t.Fatal("missing artifact must error on PromoteLatest")
	}
}

func TestFileStoreSaveIdempotentAndPromoteUpgrades(t *testing.T) {
	ctx := context.Background()
	store := newTestFileStore(t)
	payload := []byte("payload")
	desc := fileDesc("hp:p", 2, 5, payload)
	if err := store.Save(ctx, desc, payload); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Idempotent re-save of the identical snapshot is a no-op success.
	if err := store.Save(ctx, desc, payload); err != nil {
		t.Fatalf("re-Save: %v", err)
	}

	// Promotion overwrites an older destination snapshot.
	dst := newTestFileStore(t)
	older := []byte("older")
	if err := dst.Save(ctx, fileDesc("hp:p", 1, 9, older), older); err != nil {
		t.Fatalf("Save older on dst: %v", err)
	}
	lane, promoted, err := store.PromoteLatest(ctx, dst, "hp:p")
	if err != nil || lane == "skipped" {
		t.Fatalf("promotion over older lane=%q err=%v", lane, err)
	}
	if promoted.Epoch != 2 {
		t.Fatalf("promoted epoch = %d, want 2", promoted.Epoch)
	}
	got, body, ok, err := dst.Latest(ctx, "hp:p")
	if err != nil || !ok || got.Epoch != 2 || !bytes.Equal(body, payload) {
		t.Fatalf("post-promotion Latest desc=%+v ok=%v err=%v", got, ok, err)
	}
}

func TestStrictlyNewerThanEdges(t *testing.T) {
	base := hermes.SnapshotDescriptor{Epoch: 2, Watermark: 10}
	cases := []struct {
		candidate hermes.SnapshotDescriptor
		want      bool
	}{
		{hermes.SnapshotDescriptor{Epoch: 3, Watermark: 0}, true},
		{hermes.SnapshotDescriptor{Epoch: 1, Watermark: 99}, false},
		{hermes.SnapshotDescriptor{Epoch: 2, Watermark: 11}, true},
		{hermes.SnapshotDescriptor{Epoch: 2, Watermark: 10}, false},
		{hermes.SnapshotDescriptor{Epoch: 2, Watermark: 9}, false},
	}
	for _, tc := range cases {
		if got := strictlyNewerThan(tc.candidate, base); got != tc.want {
			t.Fatalf("strictlyNewerThan(%+v) = %v, want %v", tc.candidate, got, tc.want)
		}
	}
}

func TestWriteFileAtomicRenameFailure(t *testing.T) {
	dir := t.TempDir()
	// Renaming onto a path whose parent is a file fails after the temp write,
	// exercising the cleanup branch.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	target := filepath.Join(blocker, "child")
	if err := writeFileAtomic(target, []byte("payload")); err == nil {
		t.Fatal("rename into file-parent must error")
	}
}

func TestCloneFileSourceErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := cloneFile(filepath.Join(dir, "dst"), filepath.Join(dir, "absent")); err == nil {
		t.Fatal("cloning a missing source must error")
	}
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("data"), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if _, err := cloneFile(filepath.Join(dir, "missing-parent", "dst"), src); err == nil {
		t.Fatal("cloning into a missing directory must error")
	}
}

// --- objectstore Store error-path coverage (same package, shared helpers) ---

func TestObjectStoreSaveErrorPaths(t *testing.T) {
	obj := memoryObjectStoreTB(t)
	adapter, err := New(obj, "custom/prefix")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	payload := []byte("payload")
	desc := fileDesc("hp:p", 1, 1, payload)

	// Empty projection rejected.
	if err := adapter.Save(context.Background(), hermes.SnapshotDescriptor{}, payload); err == nil {
		t.Fatal("empty projection must error")
	}
	// The memory objectstore backend does not observe context cancellation,
	// so drive an ordinary save through the custom prefix instead: it covers
	// Save's happy path against a non-default prefix plus the Latest read-back.
	if err := adapter.Save(context.Background(), desc, payload); err != nil {
		t.Fatalf("Save with custom prefix: %v", err)
	}
	if _, _, ok, err := adapter.Latest(context.Background(), "hp:p"); !ok || err != nil {
		t.Fatalf("Latest with custom prefix ok=%v err=%v", ok, err)
	}
	// Nil object store rejected at construction.
	if _, err := New(nil, ""); err == nil {
		t.Fatal("nil object store must error")
	}
}

func TestObjectStoreLatestErrorPaths(t *testing.T) {
	ctx := context.Background()
	obj := memoryObjectStoreTB(t)
	adapter, err := New(obj, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Malformed pointer JSON.
	if _, err := obj.PutBytes(ctx, adapter.latestKey("hp:p"), []byte("{not json"), objectstoreputopts()); err != nil {
		t.Fatalf("PutBytes: %v", err)
	}
	if _, _, _, err := adapter.Latest(ctx, "hp:p"); err == nil {
		t.Fatal("malformed pointer must error")
	}
	// Pointer referencing a missing artifact.
	pointer := []byte(`{"descriptor":{"Projection":"hp:q","Checksum":"00"},"artifact_key":"hermes/snapshots/hp/q/9-9.ovsnap"}`)
	if _, err := obj.PutBytes(ctx, adapter.latestKey("hp:q"), pointer, objectstoreputopts()); err != nil {
		t.Fatalf("PutBytes pointer: %v", err)
	}
	if _, _, _, err := adapter.Latest(ctx, "hp:q"); err == nil {
		t.Fatal("missing artifact must error")
	}
}

func TestObjectStoreNewerThanEdges(t *testing.T) {
	base := hermes.SnapshotDescriptor{Epoch: 2, Watermark: 10}
	if !newerThan(hermes.SnapshotDescriptor{Epoch: 3}, base) {
		t.Fatal("higher epoch must be newer")
	}
	if newerThan(hermes.SnapshotDescriptor{Epoch: 1, Watermark: 99}, base) {
		t.Fatal("lower epoch must not be newer")
	}
	if !newerThan(hermes.SnapshotDescriptor{Epoch: 2, Watermark: 10}, base) {
		t.Fatal("equal snapshot is re-saveable (newerThan is inclusive)")
	}
}

func TestUserspaceCopySeekErrors(t *testing.T) {
	dir := t.TempDir()
	openFile := func(name string) *os.File {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		f, err := os.OpenFile(path, os.O_RDWR, 0o600)
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		return f
	}
	src := openFile("src")
	dst := openFile("dst")
	closeQuiet(src)
	if _, err := userspaceCopy(dst, src); err == nil {
		t.Fatal("closed source must fail seek")
	}
	src2 := openFile("src2")
	defer closeQuiet(src2)
	closeQuiet(dst)
	if _, err := userspaceCopy(dst, src2); err == nil {
		t.Fatal("closed destination must fail seek")
	}
}

func TestWriteFileAtomicRenameOntoDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "occupied")
	if err := os.MkdirAll(filepath.Join(target, "child"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeFileAtomic(target, []byte("payload")); err == nil {
		t.Fatal("rename onto non-empty directory must error")
	}
}
