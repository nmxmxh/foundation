package hermes

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

func TestSnapshotChunkWriterRoundtrip(t *testing.T) {
	totalRecords := 250
	records := make([]database.DomainRecord, totalRecords)
	baseTime := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

	for i := range totalRecords {
		records[i] = database.DomainRecord{
			Domain:         "signals",
			Collection:     "telemetry",
			OrganizationID: "org_alpha",
			RecordID:       fmt.Sprintf("rec_%04d", i),
			CreatedAt:      baseTime.Add(time.Duration(i) * time.Second),
			UpdatedAt:      baseTime.Add(time.Duration(i) * time.Second),
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "sensor_id", Value: database.StringValue(fmt.Sprintf("sensor_%d", i%10))},
				database.RecordField{Name: "reading", Value: database.FloatValue(float64(i) * 1.25)},
				database.RecordField{Name: "active", Value: database.BoolValue(i%2 == 0)},
			),
			Vector: []float32{float32(i), float32(i * 2)},
		}
	}

	// Write in chunks of 50 records -> 5 chunks
	writer := NewSnapshotChunkWriter(50)
	payload, err := writer.Encode(records)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// 1. Verify manifest
	manifest, err := DecodeManifest(payload)
	if err != nil {
		t.Fatalf("DecodeManifest failed: %v", err)
	}
	if manifest.TotalRecords != uint32(totalRecords) {
		t.Fatalf("expected %d total records; got %d", totalRecords, manifest.TotalRecords)
	}
	if len(manifest.Chunks) != 5 {
		t.Fatalf("expected 5 chunks; got %d", len(manifest.Chunks))
	}
	for i, c := range manifest.Chunks {
		if c.Index != uint32(i) || c.RecordCount != 50 {
			t.Fatalf("unexpected chunk descriptor at %d: %+v", i, c)
		}
	}

	// 2. Decode through streamSnapshotRecords
	decoded := make([]database.DomainRecord, 0, totalRecords)
	err = streamSnapshotRecords(payload, func(rec database.DomainRecord) error {
		decoded = append(decoded, rec)
		return nil
	})
	if err != nil {
		t.Fatalf("streamSnapshotRecords failed: %v", err)
	}

	if len(decoded) != totalRecords {
		t.Fatalf("expected %d decoded records; got %d", totalRecords, len(decoded))
	}

	for i := range records {
		if decoded[i].RecordID != records[i].RecordID {
			t.Fatalf("record %d ID mismatch: %s vs %s", i, decoded[i].RecordID, records[i].RecordID)
		}
		if decoded[i].Domain != records[i].Domain || decoded[i].Collection != records[i].Collection {
			t.Fatalf("record %d scope mismatch", i)
		}
		if len(decoded[i].Vector) != len(records[i].Vector) {
			t.Fatalf("record %d vector length mismatch", i)
		}
	}
}

func TestSnapshotChunkCorruptionDetection(t *testing.T) {
	records := make([]database.DomainRecord, 30)
	for i := range records {
		records[i] = database.DomainRecord{
			Domain:         "signals",
			Collection:     "events",
			OrganizationID: "org_1",
			RecordID:       fmt.Sprintf("rec_%d", i),
		}
	}

	writer := NewSnapshotChunkWriter(10)
	payload, err := writer.Encode(records)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	manifest, err := DecodeManifest(payload)
	if err != nil {
		t.Fatalf("DecodeManifest failed: %v", err)
	}

	// Corrupt a byte in the first chunk's payload
	corrupted := append([]byte(nil), payload...)
	chunkOffset := manifest.Chunks[0].PayloadOffset + 10
	corrupted[chunkOffset] ^= 0xFF

	err = streamSnapshotRecords(corrupted, func(rec database.DomainRecord) error {
		return nil
	})
	if err == nil {
		t.Fatalf("expected checksum error on corrupted chunk payload; got nil")
	}
	if !errors.Is(err, ErrSnapshotCorrupt) {
		t.Fatalf("expected ErrSnapshotCorrupt; got %v", err)
	}
}

func TestSnapshotChunkEmpty(t *testing.T) {
	writer := NewSnapshotChunkWriter(100)
	payload, err := writer.Encode(nil)
	if err != nil {
		t.Fatalf("Encode nil failed: %v", err)
	}

	count := 0
	err = streamSnapshotRecords(payload, func(rec database.DomainRecord) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("streamSnapshotRecords failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 records; got %d", count)
	}
}

func TestSnapshotChunkWarmIntegration(t *testing.T) {
	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	spec := ProjectionSpec{
		Name:          "telemetry",
		Domain:        "ops",
		Collection:    "metrics",
		IndexedFields: []string{"sensor"},
	}
	if err := store.Register(spec); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	records := make([]database.DomainRecord, 150)
	for i := range records {
		records[i] = database.DomainRecord{
			Domain:         "ops",
			Collection:     "metrics",
			OrganizationID: "org_1",
			RecordID:       fmt.Sprintf("m_%d", i),
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "sensor", Value: database.StringValue("temp_1")},
			),
		}
	}

	writer := NewSnapshotChunkWriter(50)
	payload, err := writer.Encode(records)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	sum := sha256.Sum256(payload)
	desc := SnapshotDescriptor{
		Projection:     "telemetry",
		Domain:         "ops",
		Collection:     "metrics",
		OrganizationID: "org_1",
		Epoch:          1,
		Watermark:      100,
		Records:        int64(len(records)),
		Bytes:          int64(len(payload)),
		Checksum:       fmt.Sprintf("%x", sum),
	}

	snapStore := NewMemorySnapshotStore()
	ctx := context.Background()
	if err := snapStore.Save(ctx, desc, payload); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	warmedDesc, ok, err := store.WarmFromSnapshot(ctx, "telemetry", snapStore)
	if err != nil || !ok {
		t.Fatalf("WarmFromSnapshot failed: ok=%v, err=%v", ok, err)
	}
	if warmedDesc.Records != 150 {
		t.Fatalf("expected 150 records; got %d", warmedDesc.Records)
	}

	count, err := store.Count(ctx, "telemetry", Query{OrganizationID: "org_1"}, Fence{})
	if err != nil || count != 150 {
		t.Fatalf("expected count 150; got %d, err=%v", count, err)
	}
}
