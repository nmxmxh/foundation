package hermessnapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	runtimeconfig "github.com/nmxmxh/ovasabi_foundation/config-contracts/go/runtimeconfig"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/objectstore"
)

func memoryObjectStore(t *testing.T) *objectstore.Store {
	t.Helper()
	return objectstore.New(runtimeconfig.ObjectStorageConfig{
		Endpoint: "memory://hermes-snapshots",
		Bucket:   "snapshots-test",
	})
}

func spec() hermes.ProjectionSpec {
	return hermes.ProjectionSpec{
		Name:             "state_ticks",
		Domain:           "signals",
		Collection:       "ticks",
		IndexedFields:    []string{"bucket"},
		MaxRecords:       1_000_000,
		MaxBytes:         512 << 20,
		MaxAppliedEvents: 2_000_000,
	}
}

func records(org string, count int) []database.DomainRecord {
	out := make([]database.DomainRecord, count)
	for i := range out {
		out[i] = database.DomainRecord{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: org,
			RecordID:       fmt.Sprintf("tick_%06d", i),
			Data: database.RecordData{
				{Name: "bucket", Value: mustValue(i % 8)},
			},
		}
	}
	return out
}

func mustValue(v any) database.RecordValue {
	value, ok := database.RecordValueFromAny(v)
	if !ok {
		panic("unsupported value")
	}
	return value
}

func warmIDs(t *testing.T, store *hermes.Store, org string) []string {
	t.Helper()
	var ids []string
	if _, err := store.ForEachView(context.Background(), "state_ticks", hermes.Query{OrganizationID: org}, hermes.Fence{}, func(v hermes.RecordView) error {
		ids = append(ids, v.RecordID)
		return nil
	}); err != nil {
		t.Fatalf("ForEachView() error = %v", err)
	}
	sort.Strings(ids)
	return ids
}

// TestObjectStoreRoundTrip: save a snapshot through the objectstore adapter, then
// warm a cold hermes store from it, end to end.
func TestObjectStoreRoundTrip(t *testing.T) {
	const org = "org_1"
	ctx := context.Background()
	adapter, err := New(memoryObjectStore(t), "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	source, err := hermes.NewStore(spec())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, err := source.ApplyRecords(ctx, "state_ticks", "seed", 1, records(org, 400)); err != nil {
		t.Fatalf("ApplyRecords() error = %v", err)
	}
	desc, err := source.SaveSnapshot(ctx, "state_ticks", hermes.Query{OrganizationID: org}, adapter)
	if err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}
	if desc.Records != 400 {
		t.Fatalf("descriptor records = %d, want 400", desc.Records)
	}

	cold, err := hermes.NewStore(spec())
	if err != nil {
		t.Fatalf("NewStore(cold) error = %v", err)
	}
	warmDesc, ok, err := cold.WarmFromSnapshot(ctx, "state_ticks", adapter)
	if err != nil || !ok {
		t.Fatalf("WarmFromSnapshot() ok=%v err=%v", ok, err)
	}
	if warmDesc.Checksum != desc.Checksum {
		t.Fatalf("warm checksum = %q, want %q", warmDesc.Checksum, desc.Checksum)
	}
	if got := warmIDs(t, cold, org); len(got) != 400 {
		t.Fatalf("warmed %d records, want 400", len(got))
	}
}

// TestObjectStoreLatestEmpty: no snapshot yet ⇒ ok=false, no error.
func TestObjectStoreLatestEmpty(t *testing.T) {
	adapter, err := New(memoryObjectStore(t), "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	desc, payload, ok, err := adapter.Latest(context.Background(), "state_ticks:missing")
	if err != nil || ok || payload != nil || desc.Records != 0 {
		t.Fatalf("Latest(empty) = {%+v, ok=%v, err=%v}, want empty/ok=false", desc, ok, err)
	}
}

// TestObjectStoreNewestWins: an older descriptor does not overwrite a newer
// committed snapshot.
func TestObjectStoreNewestWins(t *testing.T) {
	ctx := context.Background()
	adapter, err := New(memoryObjectStore(t), "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	newer := hermes.SnapshotDescriptor{Projection: "p", Epoch: 6, Watermark: 10, Checksum: checksum([]byte("new"))}
	older := hermes.SnapshotDescriptor{Projection: "p", Epoch: 5, Watermark: 999, Checksum: checksum([]byte("old"))}
	if err := adapter.Save(ctx, newer, []byte("new")); err != nil {
		t.Fatalf("Save(newer) error = %v", err)
	}
	if err := adapter.Save(ctx, older, []byte("old")); err != nil {
		t.Fatalf("Save(older) error = %v", err)
	}
	desc, payload, ok, err := adapter.Latest(ctx, "p")
	if err != nil || !ok {
		t.Fatalf("Latest() ok=%v err=%v", ok, err)
	}
	if desc.Epoch != 6 || string(payload) != "new" {
		t.Fatalf("Latest kept epoch=%d payload=%q, want newest (6,\"new\")", desc.Epoch, payload)
	}
}

// TestObjectStoreCorruptArtifact: a LATEST pointer whose artifact bytes do not
// match the recorded checksum is reported as corrupt (so warm falls back).
func TestObjectStoreCorruptArtifact(t *testing.T) {
	ctx := context.Background()
	obj := memoryObjectStore(t)
	adapter, err := New(obj, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	desc := hermes.SnapshotDescriptor{Projection: "p", Epoch: 1, Watermark: 1, Checksum: checksum([]byte("original"))}
	artifactKey := adapter.artifactKey(desc)
	// Write tampered artifact bytes, then a pointer that trusts the original checksum.
	if _, err := obj.PutBytes(ctx, artifactKey, []byte("tampered"), objectstore.PutOptions{}); err != nil {
		t.Fatalf("PutBytes(artifact) error = %v", err)
	}
	pointer, err := json.Marshal(latestPointer{Descriptor: desc, ArtifactKey: artifactKey})
	if err != nil {
		t.Fatalf("json.Marshal(pointer) error = %v", err)
	}
	if _, err := obj.PutBytes(ctx, adapter.latestKey("p"), pointer, objectstore.PutOptions{}); err != nil {
		t.Fatalf("PutBytes(pointer) error = %v", err)
	}
	if _, _, ok, err := adapter.Latest(ctx, "p"); ok || err != hermes.ErrSnapshotCorrupt {
		t.Fatalf("Latest() ok=%v err=%v, want ErrSnapshotCorrupt", ok, err)
	}
}

func checksum(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
