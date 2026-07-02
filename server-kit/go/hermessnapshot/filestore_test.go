package hermessnapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
)

func newTestFileStore(t testing.TB) *FileStore {
	t.Helper()
	store, err := NewFileStore(filepath.Join(t.TempDir(), "snaps"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return store
}

func fileChecksum(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func fileDesc(projection string, epoch, watermark uint64, payload []byte) hermes.SnapshotDescriptor {
	return hermes.SnapshotDescriptor{
		Projection: projection,
		Domain:     "signals",
		Collection: "ticks",
		Epoch:      epoch,
		Watermark:  watermark,
		Records:    1,
		Bytes:      int64(len(payload)),
		Checksum:   fileChecksum(payload),
	}
}

func TestFileStoreSaveLatestRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newTestFileStore(t)
	payload := []byte("artifact-payload")
	desc := fileDesc("hp:signals:ticks:org_1", 3, 42, payload)

	if err := store.Save(ctx, desc, payload); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, body, ok, err := store.Latest(ctx, desc.Projection)
	if err != nil || !ok {
		t.Fatalf("Latest ok=%v err=%v", ok, err)
	}
	if got != desc || !bytes.Equal(body, payload) {
		t.Fatalf("Latest mismatch: desc=%+v payload=%q", got, body)
	}

	// Missing projection is ok=false, not an error.
	if _, _, ok, err := store.Latest(ctx, "hp:missing"); ok || err != nil {
		t.Fatalf("missing projection ok=%v err=%v", ok, err)
	}
	// Empty projection on save must error.
	if err := store.Save(ctx, hermes.SnapshotDescriptor{}, payload); err == nil {
		t.Fatal("empty projection must error")
	}
}

func TestFileStoreNewestWins(t *testing.T) {
	ctx := context.Background()
	store := newTestFileStore(t)
	newer := []byte("newer")
	older := []byte("older")
	newerDesc := fileDesc("hp:p", 2, 10, newer)
	olderDesc := fileDesc("hp:p", 1, 99, older)

	if err := store.Save(ctx, newerDesc, newer); err != nil {
		t.Fatalf("Save newer: %v", err)
	}
	if err := store.Save(ctx, olderDesc, older); err != nil {
		t.Fatalf("Save older: %v", err)
	}
	got, body, ok, err := store.Latest(ctx, "hp:p")
	if err != nil || !ok || got.Epoch != 2 || !bytes.Equal(body, newer) {
		t.Fatalf("newest-wins violated: desc=%+v payload=%q ok=%v err=%v", got, body, ok, err)
	}
}

func TestFileStoreCorruptArtifactDetected(t *testing.T) {
	ctx := context.Background()
	store := newTestFileStore(t)
	payload := []byte("original")
	desc := fileDesc("hp:p", 1, 1, payload)
	if err := store.Save(ctx, desc, payload); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Tamper with the artifact behind the pointer's back.
	if err := os.WriteFile(store.artifactPath(desc), []byte("tampered"), 0o600); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if _, _, _, err := store.Latest(ctx, "hp:p"); !errors.Is(err, hermes.ErrSnapshotCorrupt) {
		t.Fatalf("Latest err = %v, want ErrSnapshotCorrupt", err)
	}
	// Traversal-shaped pointer keys are rejected.
	pointer := []byte(`{"descriptor":{"Projection":"hp:p"},"artifact_key":"../../etc/passwd"}`)
	if err := os.WriteFile(store.latestPath("hp:p"), pointer, 0o600); err != nil {
		t.Fatalf("write pointer: %v", err)
	}
	if _, _, _, err := store.Latest(ctx, "hp:p"); err == nil {
		t.Fatal("traversal pointer must error")
	}
}

func TestFileStorePromoteLatestLanes(t *testing.T) {
	ctx := context.Background()
	src := newTestFileStore(t)
	dst := newTestFileStore(t)
	payload := bytes.Repeat([]byte("zero-copy-artifact-"), 64<<10) // ~1.2 MB
	desc := fileDesc("hp:signals:ticks:org_1", 5, 500, payload)
	if err := src.Save(ctx, desc, payload); err != nil {
		t.Fatalf("Save: %v", err)
	}

	lane, promoted, err := src.PromoteLatest(ctx, dst, desc.Projection)
	if err != nil {
		t.Fatalf("PromoteLatest: %v", err)
	}
	if promoted != desc {
		t.Fatalf("promoted descriptor mismatch: %+v", promoted)
	}
	switch runtime.GOOS {
	case "linux":
		if lane != "reflink" && lane != "copy_file_range" && lane != "userspace" {
			t.Fatalf("unexpected linux lane %q", lane)
		}
	default:
		if lane != "userspace" {
			t.Fatalf("non-linux lane = %q, want userspace", lane)
		}
	}
	t.Logf("promote lane on %s: %s", runtime.GOOS, lane)

	// The promoted artifact must be byte-identical and checksum-valid.
	got, body, ok, err := dst.Latest(ctx, desc.Projection)
	if err != nil || !ok || got != desc || !bytes.Equal(body, payload) {
		t.Fatalf("promoted Latest mismatch ok=%v err=%v", ok, err)
	}

	// Re-promotion is newest-wins skipped.
	lane, _, err = src.PromoteLatest(ctx, dst, desc.Projection)
	if err != nil || lane != "skipped" {
		t.Fatalf("re-promotion lane=%q err=%v, want skipped", lane, err)
	}

	// Promoting a missing projection errors; nil destination errors.
	if _, _, err := src.PromoteLatest(ctx, dst, "hp:absent"); err == nil {
		t.Fatal("promoting missing snapshot must error")
	}
	if _, _, err := src.PromoteLatest(ctx, nil, desc.Projection); err == nil {
		t.Fatal("nil destination must error")
	}
}

func TestFileStoreOpenArtifactStreams(t *testing.T) {
	ctx := context.Background()
	store := newTestFileStore(t)
	payload := bytes.Repeat([]byte("stream"), 4096)
	desc := fileDesc("hp:p", 1, 7, payload)
	if err := store.Save(ctx, desc, payload); err != nil {
		t.Fatalf("Save: %v", err)
	}
	file, got, ok, err := store.OpenArtifact(ctx, "hp:p")
	if err != nil || !ok {
		t.Fatalf("OpenArtifact ok=%v err=%v", ok, err)
	}
	defer closeQuiet(file)
	if got != desc {
		t.Fatalf("OpenArtifact descriptor mismatch: %+v", got)
	}
	streamed, err := io.ReadAll(file)
	if err != nil || !bytes.Equal(streamed, payload) {
		t.Fatalf("streamed artifact mismatch err=%v", err)
	}
	if _, _, ok, err := store.OpenArtifact(ctx, "hp:absent"); ok || err != nil {
		t.Fatalf("absent OpenArtifact ok=%v err=%v", ok, err)
	}
}

// TestFileStoreWarmsHermesPartition proves the integration contract: a
// projection exported through the snapshot tier, saved to a FileStore,
// promoted to a second FileStore through the zero-copy lane, then warming a
// cold Hermes partition — with the checksum re-verified by WarmFromSnapshot.
func TestFileStoreWarmsHermesPartition(t *testing.T) {
	ctx := context.Background()
	spec := hermes.ProjectionSpec{
		Name:          "hp",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"bucket"},
		MaxRecords:    1024,
	}
	source, err := hermes.NewStore(spec)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	records := make([]database.DomainRecord, 100)
	for i := range records {
		records[i] = database.DomainRecord{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       fmt.Sprintf("rec_%03d", i),
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "bucket", Value: database.IntValue(int64(i % 4))},
			),
		}
	}
	if _, err := source.BulkLoad(ctx, "hp", records); err != nil {
		t.Fatalf("BulkLoad: %v", err)
	}

	origin := newTestFileStore(t)
	if _, err := source.SaveSnapshot(ctx, "hp", hermes.Query{OrganizationID: "org_1"}, origin); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	replica := newTestFileStore(t)
	lane, _, err := origin.PromoteLatest(ctx, replica, "hp")
	if err != nil {
		t.Fatalf("PromoteLatest: %v", err)
	}
	t.Logf("promotion lane: %s", lane)

	cold, err := hermes.NewStore(spec)
	if err != nil {
		t.Fatalf("NewStore(cold): %v", err)
	}
	desc, ok, err := cold.WarmFromSnapshot(ctx, "hp", replica)
	if err != nil || !ok {
		t.Fatalf("WarmFromSnapshot ok=%v err=%v", ok, err)
	}
	if desc.Records != int64(len(records)) {
		t.Fatalf("warmed %d records, want %d", desc.Records, len(records))
	}
	count, err := cold.Count(ctx, "hp", hermes.Query{OrganizationID: "org_1"}, hermes.Fence{})
	if err != nil || count != int64(len(records)) {
		t.Fatalf("warmed count=%d err=%v", count, err)
	}
}

// BenchmarkFileStorePromoteLatest measures one artifact promotion (clone +
// pointer publish) through whatever lane the platform offers; the reported
// lane label appears in the log line once per run. On non-Linux this is the
// userspace baseline; on Linux container/VM filesystems it exercises the
// kernel lane but the numbers carry the storage-virtualization asterisk
// recorded in the ledger.
func BenchmarkFileStorePromoteLatest(b *testing.B) {
	ctx := context.Background()
	src := newTestFileStore(b)
	payload := bytes.Repeat([]byte("A"), 8<<20) // 8 MB artifact
	desc := fileDesc("hp:bench", 1, 1, payload)
	if err := src.Save(ctx, desc, payload); err != nil {
		b.Fatalf("Save: %v", err)
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))

	lane := ""
	for b.Loop() {
		b.StopTimer()
		dst, err := NewFileStore(filepath.Join(b.TempDir(), "dst"))
		if err != nil {
			b.Fatalf("NewFileStore: %v", err)
		}
		b.StartTimer()
		lane, _, err = src.PromoteLatest(ctx, dst, "hp:bench")
		if err != nil {
			b.Fatalf("PromoteLatest: %v", err)
		}
	}
	b.StopTimer()
	b.Logf("clone lane: %s (%s)", lane, runtime.GOOS)
}
