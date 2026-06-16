package bulk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	apperrors "github.com/nmxmxh/ovasabi_foundation/server-kit/go/errors"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/kernellane"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/objectstore"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

func newFSManager(t *testing.T) (*Manager, *objectstore.FSStore) {
	t.Helper()
	store, err := objectstore.NewFSStore(t.TempDir(), "bulk")
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	mgr, err := NewManager(Options{
		ObjectStore:      store,
		Cache:            redis.NewMemoryClient("test"),
		EventBus:         events.NewInMemoryBus(50),
		DefaultChunkSize: 4096,
		MaxChunkSize:     1 << 20,
		MaxParts:         8,
		Clock:            fixedNow,
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	return mgr, store
}

func writeTempPart(t *testing.T, payload []byte) *os.File {
	t.Helper()
	p := filepath.Join(t.TempDir(), "part.bin")
	if err := os.WriteFile(p, payload, 0o600); err != nil {
		t.Fatalf("write temp part: %v", err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatalf("open temp part: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func descFor(payload []byte) PartDescriptor {
	sum := sha256.Sum256(payload)
	return PartDescriptor{PartNumber: 0, Offset: 0, Size: int64(len(payload)), ExpectedRawSHA256: hex.EncodeToString(sum[:])}
}

// TestAcceptDescriptorFilePartZeroCopyLane drives the executable descriptor lane
// end to end through a filesystem object store: the part is hash-verified, moved
// (via copy_file_range where supported), and the stored bytes match the source.
func TestAcceptDescriptorFilePartZeroCopyLane(t *testing.T) {
	mgr, store := newFSManager(t)
	ctx := bulkContext("org_1", "corr_zc", "idem_zc")
	payload := bytes.Repeat([]byte("zero-copy-"), 500) // 5000 bytes

	plan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID:     "zc_1",
		TotalSize:      int64(len(payload)),
		ChunkSize:      int64(len(payload)),
		MaxMemory:      int64(len(payload)),
		Compression:    EncodingIdentity,
		IdempotencyKey: "idem_zc",
		Deadline:       fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Initiate() error = %v", err)
	}

	src := writeTempPart(t, payload)
	desc := descFor(payload)
	receipt, err := mgr.AcceptDescriptorFilePart(ctx, plan.TransferID, desc, src)
	if err != nil {
		t.Fatalf("AcceptDescriptorFilePart() error = %v", err)
	}

	if receipt.RawSize != int64(len(payload)) {
		t.Fatalf("raw size = %d want %d", receipt.RawSize, len(payload))
	}
	if receipt.RawSHA256 != desc.ExpectedRawSHA256 {
		t.Fatalf("digest = %q want %q", receipt.RawSHA256, desc.ExpectedRawSHA256)
	}
	if receipt.Encoding != EncodingIdentity {
		t.Fatalf("encoding = %q want identity", receipt.Encoding)
	}
	// The zero-copy flag must reflect the platform: true on Linux with
	// copy_file_range, false (streamed fallback) elsewhere.
	if receipt.ZeroCopy != kernellane.ZeroCopyFileSupported() {
		t.Fatalf("ZeroCopy=%v but kernellane support=%v", receipt.ZeroCopy, kernellane.ZeroCopyFileSupported())
	}

	// Read the stored part back from the filesystem object store to confirm the
	// kernel/fallback copy produced byte-identical content.
	reader, _, err := store.Open(ctx, receipt.ObjectKey)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	got, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatalf("read stored part: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("stored bytes differ from source")
	}

	// Re-accepting the same part is an idempotent replay, not a re-upload.
	src2 := writeTempPart(t, payload)
	replay, err := mgr.AcceptDescriptorFilePart(ctx, plan.TransferID, desc, src2)
	if err != nil {
		t.Fatalf("idempotent replay error = %v", err)
	}
	if !replay.IdempotentReplay || replay.RawSHA256 != desc.ExpectedRawSHA256 {
		t.Fatalf("expected idempotent replay, got %+v", replay)
	}
}

func TestAcceptDescriptorFilePartUnknownTransfer(t *testing.T) {
	mgr, _ := newFSManager(t)
	ctx := bulkContext("org_1", "corr_u", "idem_u")
	src := writeTempPart(t, []byte("data"))
	if _, err := mgr.AcceptDescriptorFilePart(ctx, "does-not-exist", descFor([]byte("data")), src); err == nil {
		t.Fatal("unknown transfer should error")
	}
}

// failingFileRangeStore is a FileRangeObjectStore whose zero-copy ingest fails,
// exercising the manager's error-handling and object cleanup on that lane.
type failingFileRangeStore struct {
	*objectstore.FSStore
}

func (f failingFileRangeStore) PutFileRange(context.Context, string, *os.File, int64, objectstore.PutOptions) (objectstore.Object, bool, error) {
	return objectstore.Object{}, false, errAppDependency
}

var errAppDependency = apperrors.New(apperrors.CodeDependency, "injected file-range failure")

// selectiveFailBus fails Publish only for a chosen event type, so Initiate can
// succeed while a later part-accept emit fails.
type selectiveFailBus struct{ failOn string }

func (b selectiveFailBus) Publish(_ context.Context, env events.Envelope) error {
	if env.EventType == b.failOn {
		return errAppDependency
	}
	return nil
}

func TestAcceptDescriptorFilePartSurfacesEventFailure(t *testing.T) {
	store, err := objectstore.NewFSStore(t.TempDir(), "bucket")
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	mgr, err := NewManager(Options{
		ObjectStore:      store,
		Cache:            redis.NewMemoryClient("test"),
		EventBus:         selectiveFailBus{failOn: "bulk:part:accept:v1:requested"},
		DefaultChunkSize: 4096,
		MaxChunkSize:     1 << 20,
		MaxParts:         8,
		Clock:            fixedNow,
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	ctx := bulkContext("org_1", "corr_ef", "idem_ef")
	payload := []byte("event-failure-bytes")
	plan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID: "ef_1", TotalSize: int64(len(payload)), ChunkSize: int64(len(payload)),
		MaxMemory: int64(len(payload)), Compression: EncodingIdentity, IdempotencyKey: "idem_ef",
		Deadline: fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Initiate() error = %v", err)
	}
	src := writeTempPart(t, payload)
	if _, err := mgr.AcceptDescriptorFilePart(ctx, plan.TransferID, descFor(payload), src); err == nil {
		t.Fatal("event publish failure should surface as an error")
	}
}

func TestAcceptDescriptorFilePartSurfacesStoreFailure(t *testing.T) {
	base, err := objectstore.NewFSStore(t.TempDir(), "bucket")
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	mgr, err := NewManager(Options{
		ObjectStore:      failingFileRangeStore{base},
		Cache:            redis.NewMemoryClient("test"),
		EventBus:         events.NewInMemoryBus(50),
		DefaultChunkSize: 4096,
		MaxChunkSize:     1 << 20,
		MaxParts:         8,
		Clock:            fixedNow,
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	ctx := bulkContext("org_1", "corr_sf", "idem_sf")
	payload := []byte("store-failure-bytes")
	plan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID: "sf_1", TotalSize: int64(len(payload)), ChunkSize: int64(len(payload)),
		MaxMemory: int64(len(payload)), Compression: EncodingIdentity, IdempotencyKey: "idem_sf",
		Deadline: fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Initiate() error = %v", err)
	}
	src := writeTempPart(t, payload)
	if _, err := mgr.AcceptDescriptorFilePart(ctx, plan.TransferID, descFor(payload), src); err == nil {
		t.Fatal("store failure should surface as an error")
	}
}

// TestAcceptDescriptorFilePartFallsBackToStreaming proves graceful degradation:
// an object store without FileRange support uses the streaming lane, never
// reports zero-copy, and still verifies integrity.
func TestAcceptDescriptorFilePartFallsBackToStreaming(t *testing.T) {
	mgr, _, _, _ := newTestManager(t) // in-memory store: not a FileRangeObjectStore
	ctx := bulkContext("org_1", "corr_fb", "idem_fb")
	payload := []byte("fbbytes") // within newTestManager's small chunk bounds

	plan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID:     "fb_1",
		TotalSize:      int64(len(payload)),
		ChunkSize:      int64(len(payload)),
		MaxMemory:      int64(len(payload)),
		Compression:    EncodingIdentity,
		IdempotencyKey: "idem_fb",
		Deadline:       fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Initiate() error = %v", err)
	}

	src := writeTempPart(t, payload)
	receipt, err := mgr.AcceptDescriptorFilePart(ctx, plan.TransferID, descFor(payload), src)
	if err != nil {
		t.Fatalf("AcceptDescriptorFilePart() error = %v", err)
	}
	if receipt.ZeroCopy {
		t.Fatal("in-memory store must not report zero-copy")
	}
	if receipt.RawSHA256 != descFor(payload).ExpectedRawSHA256 {
		t.Fatal("digest mismatch on fallback lane")
	}
}

// TestAcceptDescriptorFilePartCompressionUsesStreamingLane ensures a compressed
// transfer never takes the zero-copy lane (copy_file_range cannot compress).
func TestAcceptDescriptorFilePartCompressionUsesStreamingLane(t *testing.T) {
	mgr, _ := newFSManager(t)
	ctx := bulkContext("org_1", "corr_gz", "idem_gz")
	payload := bytes.Repeat([]byte("compressible "), 64)

	plan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID:     "gz_1",
		TotalSize:      int64(len(payload)),
		ChunkSize:      int64(len(payload)),
		MaxMemory:      int64(len(payload)),
		Compression:    EncodingGzip,
		IdempotencyKey: "idem_gz",
		Deadline:       fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Initiate() error = %v", err)
	}

	src := writeTempPart(t, payload)
	receipt, err := mgr.AcceptDescriptorFilePart(ctx, plan.TransferID, descFor(payload), src)
	if err != nil {
		t.Fatalf("AcceptDescriptorFilePart() error = %v", err)
	}
	if receipt.ZeroCopy {
		t.Fatal("compressed transfer must not use zero-copy lane")
	}
	if receipt.Encoding != EncodingGzip {
		t.Fatalf("encoding = %q want gzip", receipt.Encoding)
	}
}

func TestAcceptDescriptorFilePartValidatesInputs(t *testing.T) {
	mgr, _ := newFSManager(t)
	ctx := bulkContext("org_1", "corr_v", "idem_v")
	plan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID: "v_1", TotalSize: 4, ChunkSize: 4, MaxMemory: 4,
		Compression: EncodingIdentity, IdempotencyKey: "idem_v", Deadline: fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Initiate() error = %v", err)
	}

	if _, err := mgr.AcceptDescriptorFilePart(ctx, plan.TransferID, descFor([]byte("data")), nil); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("nil source error = %v want validation", err)
	}

	// Wrong declared digest must be rejected before any object is stored.
	src := writeTempPart(t, []byte("data"))
	bad := PartDescriptor{PartNumber: 0, Size: 4, ExpectedRawSHA256: shaHex("nope")}
	if _, err := mgr.AcceptDescriptorFilePart(ctx, plan.TransferID, bad, src); err == nil {
		t.Fatal("digest mismatch should error")
	}
}
