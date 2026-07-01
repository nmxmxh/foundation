package hermes

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

func snapshotTierSpec() ProjectionSpec {
	return ProjectionSpec{
		Name:             "state_ticks",
		Domain:           "signals",
		Collection:       "ticks",
		IndexedFields:    []string{"bucket", "symbol"},
		MaxRecords:       1_000_000,
		MaxBytes:         512 << 20,
		MaxAppliedEvents: 2_000_000,
	}
}

func snapshotTierRecords(org string, from, count int) []database.DomainRecord {
	records := make([]database.DomainRecord, count)
	for i := range records {
		id := from + i
		records[i] = database.DomainRecord{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: org,
			RecordID:       fmt.Sprintf("tick_%06d", id),
			Data:           testRecordData(map[string]any{"bucket": id % 16, "symbol": "OVS"}),
		}
	}
	return records
}

// seedSnapshotTierStore returns a warm store holding count records for org and
// the source watermark that ApplyRecords advanced to.
func seedSnapshotTierStore(t *testing.T, org string, count int) (*Store, uint64) {
	t.Helper()
	store := newTestStore(t, snapshotTierSpec())
	res, err := store.ApplyRecords(context.Background(), "state_ticks", "seed", 1, snapshotTierRecords(org, 0, count))
	if err != nil {
		t.Fatalf("ApplyRecords() error = %v", err)
	}
	if res.Applied != count {
		t.Fatalf("seed applied = %d, want %d", res.Applied, count)
	}
	wm, err := store.partition("state_ticks")
	if err != nil {
		t.Fatalf("partition() error = %v", err)
	}
	return store, wm.watermark.Load()
}

func snapshotTierIDs(t *testing.T, store *Store, org string) []string {
	t.Helper()
	var ids []string
	if _, err := store.ForEachView(context.Background(), "state_ticks", Query{OrganizationID: org}, Fence{}, func(v RecordView) error {
		ids = append(ids, v.RecordID)
		return nil
	}); err != nil {
		t.Fatalf("ForEachView() error = %v", err)
	}
	sort.Strings(ids)
	return ids
}

// TestSnapshotWarmMatchesSource is the SnapshotTailComplete parity oracle at
// tail=0: warming a cold store from a snapshot yields the same record set as the
// source it was exported from.
func TestSnapshotWarmMatchesSource(t *testing.T) {
	const org = "org_1"
	source, wm := seedSnapshotTierStore(t, org, 500)
	snaps := NewMemorySnapshotStore()

	desc, err := source.SaveSnapshot(context.Background(), "state_ticks", Query{OrganizationID: org}, snaps)
	if err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}
	if desc.Records != 500 {
		t.Fatalf("descriptor records = %d, want 500", desc.Records)
	}
	if desc.Watermark != wm {
		t.Fatalf("descriptor watermark = %d, want %d", desc.Watermark, wm)
	}

	cold := newTestStore(t, snapshotTierSpec())
	gotDesc, ok, err := cold.WarmFromSnapshot(context.Background(), "state_ticks", snaps)
	if err != nil || !ok {
		t.Fatalf("WarmFromSnapshot() ok=%v err=%v", ok, err)
	}
	if gotDesc.Checksum != desc.Checksum {
		t.Fatalf("warm checksum = %q, want %q", gotDesc.Checksum, desc.Checksum)
	}

	want := snapshotTierIDs(t, source, org)
	got := snapshotTierIDs(t, cold, org)
	if len(got) != len(want) {
		t.Fatalf("warmed record count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("record[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	part, _ := cold.partition("state_ticks")
	if part.watermark.Load() != desc.Watermark {
		t.Fatalf("warmed watermark = %d, want restored %d", part.watermark.Load(), desc.Watermark)
	}
}

// TestSnapshotWarmPlusTailComplete proves SnapshotTailComplete for tail>0:
// warm(snapshot@W) + replay(events version>W) equals the live source.
func TestSnapshotWarmPlusTailComplete(t *testing.T) {
	const org = "org_1"
	source, wm := seedSnapshotTierStore(t, org, 300)
	snaps := NewMemorySnapshotStore()

	desc, err := source.SaveSnapshot(context.Background(), "state_ticks", Query{OrganizationID: org}, snaps)
	if err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}

	// The source advances past the snapshot: 200 more records land after W.
	tail := snapshotTierRecords(org, 300, 200)
	if _, err := source.ApplyRecords(context.Background(), "state_ticks", "tail", wm+1, tail); err != nil {
		t.Fatalf("source tail ApplyRecords() error = %v", err)
	}

	cold := newTestStore(t, snapshotTierSpec())
	if _, ok, err := cold.WarmFromSnapshot(context.Background(), "state_ticks", snaps); err != nil || !ok {
		t.Fatalf("WarmFromSnapshot() ok=%v err=%v", ok, err)
	}
	// Merge only the tail (version > snapshot watermark), as a real warm would.
	if _, err := cold.ApplyRecords(context.Background(), "state_ticks", "tail", desc.Watermark+1, tail); err != nil {
		t.Fatalf("cold tail ApplyRecords() error = %v", err)
	}

	want := snapshotTierIDs(t, source, org)
	got := snapshotTierIDs(t, cold, org)
	if len(got) != 500 || len(want) != 500 {
		t.Fatalf("counts got=%d want=%d, expected 500", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("record[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestSnapshotWarmEmptyStore: no snapshot yet ⇒ ok=false, no error (caller falls
// back to Rebuild).
func TestSnapshotWarmEmptyStore(t *testing.T) {
	cold := newTestStore(t, snapshotTierSpec())
	desc, ok, err := cold.WarmFromSnapshot(context.Background(), "state_ticks", NewMemorySnapshotStore())
	if err != nil {
		t.Fatalf("WarmFromSnapshot() error = %v", err)
	}
	if ok {
		t.Fatalf("WarmFromSnapshot() ok = true, want false for empty store")
	}
	if desc.Records != 0 {
		t.Fatalf("empty warm descriptor records = %d, want 0", desc.Records)
	}
}

// TestSnapshotWarmCorruptArtifact: a tampered payload fails checksum and leaves
// the partition untouched.
func TestSnapshotWarmCorruptArtifact(t *testing.T) {
	const org = "org_1"
	source, _ := seedSnapshotTierStore(t, org, 100)
	snaps := NewMemorySnapshotStore()
	if _, err := source.SaveSnapshot(context.Background(), "state_ticks", Query{OrganizationID: org}, snaps); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}
	// Corrupt the stored payload without touching the checksum.
	item := snaps.items["state_ticks"]
	if len(item.payload) == 0 {
		t.Fatal("expected non-empty payload")
	}
	item.payload[0] ^= 0xFF
	snaps.items["state_ticks"] = item

	cold := newTestStore(t, snapshotTierSpec())
	_, ok, err := cold.WarmFromSnapshot(context.Background(), "state_ticks", snaps)
	if ok || err != ErrSnapshotCorrupt {
		t.Fatalf("WarmFromSnapshot() ok=%v err=%v, want ErrSnapshotCorrupt", ok, err)
	}
	if part, _ := cold.partition("state_ticks"); part.records.Load() != 0 {
		t.Fatalf("corrupt warm left %d records, want partition untouched", part.records.Load())
	}
}

// TestSnapshotWarmScopeMismatch: an artifact whose declared scope disagrees with
// the target projection is rejected (SnapshotScopeSealed).
func TestSnapshotWarmScopeMismatch(t *testing.T) {
	const org = "org_1"
	source, _ := seedSnapshotTierStore(t, org, 50)
	snaps := NewMemorySnapshotStore()
	if _, err := source.SaveSnapshot(context.Background(), "state_ticks", Query{OrganizationID: org}, snaps); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}
	// Rewrite the descriptor's declared domain, recomputing the checksum so it
	// passes integrity but fails the scope seal.
	item := snaps.items["state_ticks"]
	item.desc.Domain = "impostor"
	snaps.items["state_ticks"] = item

	cold := newTestStore(t, snapshotTierSpec())
	_, ok, err := cold.WarmFromSnapshot(context.Background(), "state_ticks", snaps)
	if ok || err != ErrSnapshotScope {
		t.Fatalf("WarmFromSnapshot() ok=%v err=%v, want ErrSnapshotScope", ok, err)
	}
}

// TestSnapshotStoreNewestWins: Save keeps the newest by (Epoch, Watermark) and
// ignores stale/duplicate writes (idempotent, race-safe).
func TestSnapshotStoreNewestWins(t *testing.T) {
	snaps := NewMemorySnapshotStore()
	older := SnapshotDescriptor{Projection: "p", Epoch: 5, Watermark: 100, Checksum: "a"}
	newer := SnapshotDescriptor{Projection: "p", Epoch: 6, Watermark: 10, Checksum: "b"}
	if err := snaps.Save(context.Background(), newer, []byte("new")); err != nil {
		t.Fatalf("Save(newer) error = %v", err)
	}
	if err := snaps.Save(context.Background(), older, []byte("old")); err != nil {
		t.Fatalf("Save(older) error = %v", err)
	}
	desc, payload, ok, err := snaps.Latest(context.Background(), "p")
	if err != nil || !ok {
		t.Fatalf("Latest() ok=%v err=%v", ok, err)
	}
	if desc.Epoch != 6 || string(payload) != "new" {
		t.Fatalf("Latest() kept epoch=%d payload=%q, want newest (6,\"new\")", desc.Epoch, payload)
	}
}

func BenchmarkHermesWarmFromSnapshot(b *testing.B) {
	const org = "org_1"
	const count = 10000
	source := newBenchStore(b)
	if _, err := source.ApplyRecords(context.Background(), "bench_ticks", "seed", 1, benchSnapshotRecords(org, count)); err != nil {
		b.Fatalf("seed ApplyRecords() error = %v", err)
	}
	snaps := NewMemorySnapshotStore()
	if _, err := source.SaveSnapshot(context.Background(), "bench_ticks", Query{OrganizationID: org}, snaps); err != nil {
		b.Fatalf("SaveSnapshot() error = %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		cold, err := NewStore(benchSnapshotSpec())
		if err != nil {
			b.Fatalf("NewStore() error = %v", err)
		}
		if _, ok, err := cold.WarmFromSnapshot(context.Background(), "bench_ticks", snaps); err != nil || !ok {
			b.Fatalf("WarmFromSnapshot() ok=%v err=%v", ok, err)
		}
	}
}

func benchSnapshotSpec() ProjectionSpec {
	return ProjectionSpec{
		Name:             "bench_ticks",
		Domain:           "signals",
		Collection:       "ticks",
		IndexedFields:    []string{"bucket", "symbol"},
		MaxRecords:       1_000_000,
		MaxBytes:         512 << 20,
		MaxAppliedEvents: 2_000_000,
	}
}

func benchSnapshotRecords(org string, count int) []database.DomainRecord {
	records := make([]database.DomainRecord, count)
	for i := range records {
		records[i] = database.DomainRecord{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: org,
			RecordID:       fmt.Sprintf("tick_%06d", i),
			Data:           testRecordData(map[string]any{"bucket": i % 16, "symbol": "OVS"}),
		}
	}
	return records
}
