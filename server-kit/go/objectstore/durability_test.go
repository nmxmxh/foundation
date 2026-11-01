package objectstore

import (
	"context"
	"errors"
	"strings"
	"testing"

	runtimeconfig "github.com/nmxmxh/ovasabi_foundation/config-contracts/go/runtimeconfig"
)

func TestSyncObjectReportsDurability(t *testing.T) {
	ctx := context.Background()
	memory := New(runtimeconfig.ObjectStorageConfig{Endpoint: "memory://sync", Bucket: "b"})
	if err := memory.SyncObject(ctx, "k"); !errors.Is(err, ErrNotDurable) {
		t.Fatalf("memory SyncObject = %v, want ErrNotDurable", err)
	}
	if err := memory.SyncObject(ctx, " "); err == nil {
		t.Fatal("memory SyncObject accepted an empty key")
	}
	remote := New(runtimeconfig.ObjectStorageConfig{Endpoint: "https://s3.example.test", Bucket: "b"})
	if err := remote.SyncObject(ctx, "k"); err != nil {
		t.Fatalf("s3 SyncObject = %v, want nil", err)
	}
	var nilStore *Store
	if err := nilStore.SyncObject(ctx, "k"); err == nil {
		t.Fatal("nil Store SyncObject returned nil")
	}

	fs, err := NewFSStore(t.TempDir(), "b")
	if err != nil {
		t.Fatalf("NewFSStore: %v", err)
	}
	if _, err := fs.PutStream(ctx, "parts/p0", strings.NewReader("payload"), 7, PutOptions{}); err != nil {
		t.Fatalf("PutStream: %v", err)
	}
	if err := fs.SyncObject(ctx, "parts/p0"); err != nil {
		t.Fatalf("fs SyncObject: %v", err)
	}
	if err := fs.SyncObject(ctx, "parts/missing"); err == nil {
		t.Fatal("fs SyncObject of a missing object returned nil")
	}
	if err := fs.SyncObject(ctx, "../escape"); err == nil {
		t.Fatal("fs SyncObject accepted a key outside the root")
	}
	var nilFS *FSStore
	if err := nilFS.SyncObject(ctx, "k"); err == nil {
		t.Fatal("nil FSStore SyncObject returned nil")
	}
	if err := syncParentDir(t.TempDir() + "/missing"); err == nil {
		t.Fatal("syncParentDir of a missing directory returned nil")
	}
}
