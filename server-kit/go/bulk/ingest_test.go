package bulk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	runtimeconfig "github.com/nmxmxh/ovasabi_foundation/config-contracts/go/runtimeconfig"
	apperrors "github.com/nmxmxh/ovasabi_foundation/server-kit/go/errors"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/kernellane"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/objectstore"
)

// ingestChunk is large enough that each part passes the reclaimer's 64 KiB punch interval.
const ingestChunk = 64 * 1024

func newIngestManager(t *testing.T, objects ObjectStore, state StateStore) *Manager {
	t.Helper()
	mgr, err := NewManager(Options{
		ObjectStore: objects, StateStore: state, DefaultChunkSize: ingestChunk,
		MaxChunkSize: ingestChunk, MaxParts: 8, Clock: fixedNow,
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	return mgr
}

func ingestSource(t *testing.T, parts int) (*os.File, []byte) {
	t.Helper()
	payload := make([]byte, ingestChunk*parts)
	for i := range payload {
		payload[i] = byte(i%251) + 1 // never zero, so a freed block is visible
	}
	path := filepath.Join(t.TempDir(), "source.bin")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f, payload
}

func initiateIngest(t *testing.T, mgr *Manager, ctx context.Context, id string, size int) TransferPlan {
	t.Helper()
	plan, err := mgr.Initiate(ctx, InitiateRequest{TransferID: id, TotalSize: int64(size), ChunkSize: ingestChunk, MaxMemory: ingestChunk})
	if err != nil {
		t.Fatalf("Initiate() error = %v", err)
	}
	return plan
}

func readAll(t *testing.T, mgr *Manager, ctx context.Context, id string, size int) []byte {
	t.Helper()
	reader, _, err := mgr.OpenRange(ctx, id, 0, int64(size))
	if err != nil {
		t.Fatalf("OpenRange() error = %v", err)
	}
	defer func() { _ = reader.Close() }()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read range: %v", err)
	}
	return got
}

func TestIngestFileWithoutReclaimKeepsSource(t *testing.T) {
	objects := objectstore.New(runtimeconfig.ObjectStorageConfig{Endpoint: "memory://ingest", Bucket: "bulk"})
	mgr := newIngestManager(t, objects, NewMemoryStateStore())
	ctx := bulkContext("org_1", "corr_ingest_plain", "")
	src, payload := ingestSource(t, 3)
	plan := initiateIngest(t, mgr, ctx, "ingest_plain", len(payload))

	result, err := mgr.IngestFile(ctx, plan.TransferID, src, IngestFileOptions{})
	if err != nil {
		t.Fatalf("IngestFile() error = %v", err)
	}
	if len(result.Receipts) != 3 || result.ReclaimedOffset != 0 || result.Punched {
		t.Fatalf("result = %+v, want 3 receipts and no reclaim", result)
	}
	if _, err := mgr.Complete(ctx, plan.TransferID, CompleteRequest{ExpectedRootSHA256: manifestRoot(result.Receipts)}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if got := readAll(t, mgr, ctx, plan.TransferID, len(payload)); !bytes.Equal(got, payload) {
		t.Fatal("stored transfer does not match the source")
	}
	onDisk := make([]byte, len(payload))
	if _, err := src.ReadAt(onDisk, 0); err != nil || !bytes.Equal(onDisk, payload) {
		t.Fatalf("source changed without ReclaimSource: err=%v", err)
	}
}

func TestIngestFileReclaimsSourceAfterDurableParts(t *testing.T) {
	objects, err := objectstore.NewFSStore(t.TempDir(), "bulk")
	if err != nil {
		t.Fatalf("NewFSStore: %v", err)
	}
	mgr := newIngestManager(t, objects, NewMemoryStateStore())
	ctx := bulkContext("org_1", "corr_ingest_reclaim", "")
	src, payload := ingestSource(t, 4)
	plan := initiateIngest(t, mgr, ctx, "ingest_reclaim", len(payload))
	ckpt := filepath.Join(t.TempDir(), "reclaim.json")

	result, err := mgr.IngestFile(ctx, plan.TransferID, src, IngestFileOptions{ReclaimSource: true, CheckpointPath: ckpt})
	if err != nil {
		t.Fatalf("IngestFile() error = %v", err)
	}
	if result.ReclaimedOffset != int64(len(payload)) {
		t.Fatalf("ReclaimedOffset = %d, want %d", result.ReclaimedOffset, len(payload))
	}
	if want := kernellane.HolePunchSupportedFile(src); result.Punched != want {
		t.Fatalf("Punched = %v, want %v from the filesystem probe", result.Punched, want)
	}
	if result.Punched {
		block := make([]byte, kernellane.BlockSize)
		if _, err := src.ReadAt(block, 0); err != nil || !bytes.Equal(block, make([]byte, kernellane.BlockSize)) {
			t.Fatalf("source block 0 still holds data after reclaim: err=%v", err)
		}
	}
	wm, err := kernellane.LoadCheckpoint(ckpt)
	if err != nil || wm.SourcePunchedOffset != int64(len(payload)) {
		t.Fatalf("checkpoint = (%+v, %v), want punched offset %d", wm, err, len(payload))
	}
	if _, err := mgr.Complete(ctx, plan.TransferID, CompleteRequest{ExpectedRootSHA256: manifestRoot(result.Receipts)}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if got := readAll(t, mgr, ctx, plan.TransferID, len(payload)); !bytes.Equal(got, payload) {
		t.Fatal("object store copy does not match the original source")
	}
}

// A restart after a crash skips parts that have receipts and never reads freed bytes.
func TestIngestFileResumesWithoutRereadingFreedParts(t *testing.T) {
	objects, err := objectstore.NewFSStore(t.TempDir(), "bulk")
	if err != nil {
		t.Fatalf("NewFSStore: %v", err)
	}
	state := NewMemoryStateStore()
	mgr := newIngestManager(t, objects, state)
	ctx := bulkContext("org_1", "corr_ingest_resume", "")
	src, payload := ingestSource(t, 3)
	plan := initiateIngest(t, mgr, ctx, "ingest_resume", len(payload))
	ckpt := filepath.Join(t.TempDir(), "reclaim.json")
	opts := IngestFileOptions{ReclaimSource: true, CheckpointPath: ckpt}

	first, err := mgr.IngestFile(ctx, plan.TransferID, src, opts)
	if err != nil {
		t.Fatalf("first IngestFile() error = %v", err)
	}
	// Overwrite the freed prefix: a resume that read it would store these bytes.
	if _, err := src.WriteAt(bytes.Repeat([]byte{0xEE}, ingestChunk), 0); err != nil {
		t.Fatalf("overwrite source: %v", err)
	}
	restarted := newIngestManager(t, objects, state)
	second, err := restarted.IngestFile(ctx, plan.TransferID, src, opts)
	if err != nil {
		t.Fatalf("resumed IngestFile() error = %v", err)
	}
	if len(second.Receipts) != len(first.Receipts) || second.Receipts[0].RawSHA256 != first.Receipts[0].RawSHA256 {
		t.Fatal("resume re-read a part that already had a receipt")
	}
	if _, err := restarted.Complete(ctx, plan.TransferID, CompleteRequest{ExpectedRootSHA256: manifestRoot(second.Receipts)}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if got := readAll(t, restarted, ctx, plan.TransferID, len(payload)); !bytes.Equal(got, payload) {
		t.Fatal("stored transfer changed after resume")
	}
}

// Regression: when a receipt is lost for a freed part, ingest must stop instead of storing zeros.
func TestIngestFileRefusesFreedPartWithoutReceipt(t *testing.T) {
	objects, err := objectstore.NewFSStore(t.TempDir(), "bulk")
	if err != nil {
		t.Fatalf("NewFSStore: %v", err)
	}
	mgr := newIngestManager(t, objects, NewMemoryStateStore())
	ctx := bulkContext("org_1", "corr_ingest_lost", "")
	src, payload := ingestSource(t, 2)
	plan := initiateIngest(t, mgr, ctx, "ingest_lost", len(payload))
	ckpt := filepath.Join(t.TempDir(), "reclaim.json")
	opts := IngestFileOptions{ReclaimSource: true, CheckpointPath: ckpt}
	if _, err := mgr.IngestFile(ctx, plan.TransferID, src, opts); err != nil {
		t.Fatalf("IngestFile() error = %v", err)
	}

	lost := NewMemoryStateStore() // the plan survived, the receipts did not
	stored, err := mgr.state.LoadPlan(ctx, plan.OrganizationID, plan.TransferID)
	if err != nil {
		t.Fatalf("LoadPlan: %v", err)
	}
	if err := lost.SavePlan(ctx, stored); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}
	restarted := newIngestManager(t, objects, lost)
	_, err = restarted.IngestFile(ctx, plan.TransferID, src, opts)
	if !apperrors.Is(err, apperrors.CodeInvalidState) {
		t.Fatalf("IngestFile with lost receipts = %v, want invalid state", err)
	}
}

func TestIngestFileRefusals(t *testing.T) {
	memory := objectstore.New(runtimeconfig.ObjectStorageConfig{Endpoint: "memory://ingest-refuse", Bucket: "bulk"})
	mgr := newIngestManager(t, memory, NewMemoryStateStore())
	ctx := bulkContext("org_1", "corr_ingest_refuse", "")
	src, payload := ingestSource(t, 1)
	plan := initiateIngest(t, mgr, ctx, "ingest_refuse", len(payload))
	ckpt := filepath.Join(t.TempDir(), "reclaim.json")

	cases := []struct {
		name string
		src  *os.File
		opts IngestFileOptions
		code apperrors.Code
	}{
		{"nil source", nil, IngestFileOptions{}, apperrors.CodeValidation},
		{"reclaim without checkpoint", src, IngestFileOptions{ReclaimSource: true}, apperrors.CodeValidation},
		{"reclaim on a store that is not durable", src, IngestFileOptions{ReclaimSource: true, CheckpointPath: ckpt}, apperrors.CodeInvalidState},
	}
	for _, tc := range cases {
		if _, err := mgr.IngestFile(ctx, plan.TransferID, tc.src, tc.opts); !apperrors.Is(err, tc.code) {
			t.Fatalf("%s: err = %v, want %s", tc.name, err, tc.code)
		}
	}
	onDisk := make([]byte, len(payload))
	if _, err := src.ReadAt(onDisk, 0); err != nil || !bytes.Equal(onDisk, payload) {
		t.Fatalf("a refused reclaim changed the source: err=%v", err)
	}
	short, _ := ingestSource(t, 2)
	if _, err := mgr.IngestFile(ctx, plan.TransferID, short, IngestFileOptions{}); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("size mismatch err = %v, want validation", err)
	}
	if _, err := mgr.IngestFile(ctx, "missing", src, IngestFileOptions{}); err == nil {
		t.Fatal("IngestFile of an unknown transfer returned nil")
	}
	closed, _ := ingestSource(t, 1)
	_ = closed.Close()
	if _, err := mgr.IngestFile(ctx, plan.TransferID, closed, IngestFileOptions{}); err == nil {
		t.Fatal("IngestFile of a closed file returned nil")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := mgr.IngestFile(cancelled, plan.TransferID, src, IngestFileOptions{}); err == nil {
		t.Fatal("IngestFile with a cancelled context returned nil")
	}
}

func TestIngestFileRejectsForeignCheckpoints(t *testing.T) {
	objects, err := objectstore.NewFSStore(t.TempDir(), "bulk")
	if err != nil {
		t.Fatalf("NewFSStore: %v", err)
	}
	mgr := newIngestManager(t, objects, NewMemoryStateStore())
	ctx := bulkContext("org_1", "corr_ingest_ckpt", "")
	src, payload := ingestSource(t, 1)
	plan := initiateIngest(t, mgr, ctx, "ingest_ckpt", len(payload))
	dir := t.TempDir()
	write := func(name string, wm kernellane.ReclaimWatermark) string {
		data, err := json.Marshal(wm)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("write checkpoint: %v", err)
		}
		return path
	}
	cases := map[string]struct {
		path string
		code apperrors.Code
	}{
		"aborted":    {write("aborted.json", kernellane.ReclaimWatermark{SourceTotalSize: int64(len(payload)), Aborted: true}), apperrors.CodeInvalidState},
		"other size": {write("size.json", kernellane.ReclaimWatermark{SourceTotalSize: 1}), apperrors.CodeConflict},
		"corrupt":    {filepath.Join(dir, "corrupt.json"), apperrors.CodeDependency},
	}
	if err := os.WriteFile(cases["corrupt"].path, []byte("{"), 0o600); err != nil {
		t.Fatalf("write corrupt checkpoint: %v", err)
	}
	for name, tc := range cases {
		opts := IngestFileOptions{ReclaimSource: true, CheckpointPath: tc.path}
		if _, err := mgr.IngestFile(ctx, plan.TransferID, src, opts); !apperrors.Is(err, tc.code) {
			t.Fatalf("%s checkpoint: err = %v, want %s", name, err, tc.code)
		}
	}
}

func TestIngestFileEmitsBookends(t *testing.T) {
	bus := events.NewInMemoryBus(50)
	mgr, err := NewManager(Options{
		ObjectStore: objectstore.New(runtimeconfig.ObjectStorageConfig{Endpoint: "memory://ingest-events", Bucket: "bulk"}),
		EventBus:    bus, DefaultChunkSize: ingestChunk, MaxChunkSize: ingestChunk, MaxParts: 8, Clock: fixedNow,
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	ctx := bulkContext("org_1", "corr_ingest_events", "")
	src, payload := ingestSource(t, 1)
	plan := initiateIngest(t, mgr, ctx, "ingest_events", len(payload))
	if _, err := mgr.IngestFile(ctx, plan.TransferID, src, IngestFileOptions{}); err != nil {
		t.Fatalf("IngestFile() error = %v", err)
	}
	seen := map[string]bool{}
	for _, env := range bus.Recent(50) {
		seen[env.EventType] = true
	}
	for _, want := range []string{"bulk:file:ingest:v1:requested", "bulk:file:ingest:v1:success"} {
		if !seen[want] {
			t.Fatalf("event %q was not emitted; saw %v", want, seen)
		}
	}
}

// syncFailStore is durable in type but fails to sync, like a disk that returns EIO.
type syncFailStore struct {
	*objectstore.FSStore
}

func (s syncFailStore) SyncObject(context.Context, string) error {
	return errors.New("disk sync failed")
}

func TestIngestFileReclaimFailuresFreeNothing(t *testing.T) {
	ctx := bulkContext("org_1", "corr_ingest_fail", "")
	newFS := func() *objectstore.FSStore {
		fs, err := objectstore.NewFSStore(t.TempDir(), "bulk")
		if err != nil {
			t.Fatalf("NewFSStore: %v", err)
		}
		return fs
	}
	assertIntact := func(name string, src *os.File, payload []byte) {
		t.Helper()
		got := make([]byte, len(payload))
		if _, err := src.ReadAt(got, 0); err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("%s: source changed after a failed reclaim: err=%v", name, err)
		}
	}

	mgr := newIngestManager(t, syncFailStore{newFS()}, NewMemoryStateStore())
	src, payload := ingestSource(t, 1)
	plan := initiateIngest(t, mgr, ctx, "ingest_sync_fail", len(payload))
	opts := IngestFileOptions{ReclaimSource: true, CheckpointPath: filepath.Join(t.TempDir(), "c.json")}
	if _, err := mgr.IngestFile(ctx, plan.TransferID, src, opts); !apperrors.Is(err, apperrors.CodeDependency) {
		t.Fatalf("sync failure err = %v, want dependency", err)
	}
	assertIntact("sync failure", src, payload)

	mgr = newIngestManager(t, newFS(), NewMemoryStateStore())
	src, payload = ingestSource(t, 1)
	plan = initiateIngest(t, mgr, ctx, "ingest_ckpt_fail", len(payload))
	opts = IngestFileOptions{ReclaimSource: true, CheckpointPath: filepath.Join(t.TempDir(), "missing", "c.json")}
	if _, err := mgr.IngestFile(ctx, plan.TransferID, src, opts); !apperrors.Is(err, apperrors.CodeDependency) {
		t.Fatalf("checkpoint failure err = %v, want dependency", err)
	}
	assertIntact("checkpoint failure", src, payload)
}

func TestIngestFileRejectsConflictingReceipt(t *testing.T) {
	state := NewMemoryStateStore()
	mgr := newIngestManager(t, objectstore.New(runtimeconfig.ObjectStorageConfig{Endpoint: "memory://ingest-conflict", Bucket: "bulk"}), state)
	ctx := bulkContext("org_1", "corr_ingest_conflict", "")
	src, payload := ingestSource(t, 1)
	plan := initiateIngest(t, mgr, ctx, "ingest_conflict", len(payload))
	if err := state.SaveReceipt(ctx, PartReceipt{OrganizationID: plan.OrganizationID, TransferID: plan.TransferID, PartNumber: 0, RawSize: 1}); err != nil {
		t.Fatalf("SaveReceipt: %v", err)
	}
	if _, err := mgr.IngestFile(ctx, plan.TransferID, src, IngestFileOptions{}); !apperrors.Is(err, apperrors.CodeConflict) {
		t.Fatalf("conflicting receipt err = %v, want conflict", err)
	}
}

func TestIngestFileReportsUnreadableSource(t *testing.T) {
	mgr := newIngestManager(t, objectstore.New(runtimeconfig.ObjectStorageConfig{Endpoint: "memory://ingest-unreadable", Bucket: "bulk"}), NewMemoryStateStore())
	ctx := bulkContext("org_1", "corr_ingest_unreadable", "")
	_, payload := ingestSource(t, 1)
	path := filepath.Join(t.TempDir(), "write-only.bin")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	writeOnly, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open write-only: %v", err)
	}
	defer func() { _ = writeOnly.Close() }()
	plan := initiateIngest(t, mgr, ctx, "ingest_unreadable", len(payload))
	if _, err := mgr.IngestFile(ctx, plan.TransferID, writeOnly, IngestFileOptions{}); err == nil {
		t.Fatal("IngestFile of an unreadable source returned nil")
	}
}
