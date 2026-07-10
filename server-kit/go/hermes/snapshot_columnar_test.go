package hermes

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

// columnarParityRecords is a deliberately awkward fixture: multiple orgs,
// sparse fields (no record carries every field), every RecordValue kind, a
// record with empty Data, vectors on some records only, and non-zero
// timestamps — everything the columnar layout must round-trip exactly.
func columnarParityRecords() []database.DomainRecord {
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	return []database.DomainRecord{
		{
			Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: "tick_1",
			Data: database.RecordData{
				{Name: "symbol", Value: database.StringValue("OVS")},
				{Name: "price", Value: database.FloatValue(42.5)},
				{Name: "bucket", Value: database.IntValue(-7)},
			},
			Vector:    []float32{1.5, -2.25},
			CreatedAt: base, UpdatedAt: base.Add(time.Minute),
		},
		{
			Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: "tick_2",
			Data: database.RecordData{
				{Name: "symbol", Value: database.StringValue("")},
				{Name: "open", Value: database.BoolValue(true)},
				{Name: "count", Value: database.UintValue(9000000000000000000)},
			},
			CreatedAt: base.Add(time.Hour), UpdatedAt: base.Add(2 * time.Hour),
		},
		{
			Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: "tick_3",
			Data:      database.RecordData{},
			CreatedAt: base, UpdatedAt: base,
		},
	}
}

// TestColumnarSnapshotRoundTripParity is the refinement proof for the HCS1
// artifact: warming a cold store from the columnar artifact must produce
// exactly the state warming from the legacy row-proto artifact produces, and
// the shadow comparator must read both formats identically. Corrupt columnar
// payloads must fail with ErrSnapshotCorrupt, never panic.
func TestColumnarSnapshotRoundTripParity(t *testing.T) {
	ctx := t.Context()
	spec := ProjectionSpec{
		Name: "colsnap_ticks", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"symbol"}, MaxRecords: 64, MaxBytes: 1 << 20,
	}
	source := newTestStore(t, spec)
	if _, err := source.ApplyRecords(ctx, "colsnap_ticks", "seed", 1, columnarParityRecords()); err != nil {
		t.Fatalf("seed ApplyRecords() error = %v", err)
	}

	// Two artifacts of the same partition, one per format.
	protoDesc, protoPayload, err := source.ExportSnapshot(ctx, "colsnap_ticks", Query{OrganizationID: "org_1"})
	if err != nil {
		t.Fatalf("ExportSnapshot() error = %v", err)
	}
	colDesc, colPayload, err := source.ExportSnapshotColumnar(ctx, "colsnap_ticks", Query{OrganizationID: "org_1"})
	if err != nil {
		t.Fatalf("ExportSnapshotColumnar() error = %v", err)
	}
	if !isColumnarSnapshot(colPayload) || isColumnarSnapshot(protoPayload) {
		t.Fatal("format sniffing misidentifies artifacts")
	}
	if protoDesc.Records != colDesc.Records || protoDesc.Watermark != colDesc.Watermark {
		t.Fatalf("descriptor drift: proto=%+v columnar=%+v", protoDesc, colDesc)
	}

	// Refinement: both formats stream identical record sets.
	collect := func(payload []byte) map[string]database.DomainRecord {
		out := map[string]database.DomainRecord{}
		if err := streamSnapshotRecords(payload, func(rec database.DomainRecord) error {
			out[rec.RecordID] = rec
			return nil
		}); err != nil {
			t.Fatalf("streamSnapshotRecords() error = %v", err)
		}
		return out
	}
	protoRecords := collect(protoPayload)
	colRecords := collect(colPayload)
	if len(protoRecords) != len(colRecords) || len(colRecords) != 3 {
		t.Fatalf("record counts: proto=%d columnar=%d, want 3", len(protoRecords), len(colRecords))
	}
	for key, want := range protoRecords {
		got, ok := colRecords[key]
		if !ok {
			t.Fatalf("columnar artifact missing %s", key)
		}
		wantJSON, _ := want.Data.MarshalJSON()
		gotJSON, _ := got.Data.MarshalJSON()
		if string(wantJSON) != string(gotJSON) {
			t.Fatalf("%s data drift:\nproto:    %s\ncolumnar: %s", key, wantJSON, gotJSON)
		}
		if !want.CreatedAt.Equal(got.CreatedAt) || !want.UpdatedAt.Equal(got.UpdatedAt) {
			t.Fatalf("%s timestamp drift: %v/%v vs %v/%v", key, want.CreatedAt, want.UpdatedAt, got.CreatedAt, got.UpdatedAt)
		}
		if len(want.Vector) != len(got.Vector) {
			t.Fatalf("%s vector drift: %v vs %v", key, want.Vector, got.Vector)
		}
		for i := range want.Vector {
			if want.Vector[i] != got.Vector[i] {
				t.Fatalf("%s vector[%d] drift: %v vs %v", key, i, want.Vector[i], got.Vector[i])
			}
		}
	}

	// Cold warm from the columnar artifact serves the same reads.
	snaps := NewMemorySnapshotStore()
	if err := snaps.Save(ctx, colDesc, colPayload); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	cold := newTestStore(t, spec)
	if _, ok, err := cold.WarmFromSnapshot(ctx, "colsnap_ticks", snaps); err != nil || !ok {
		t.Fatalf("WarmFromSnapshot(columnar) ok=%v err=%v", ok, err)
	}
	count, err := cold.Count(ctx, "colsnap_ticks", Query{OrganizationID: "org_1"}, Fence{})
	if err != nil || count != 3 {
		t.Fatalf("cold Count() = %d err=%v, want 3", count, err)
	}
	rec, found, err := cold.GetRecord(ctx, "colsnap_ticks", Query{OrganizationID: "org_1"}, "tick_1", Fence{})
	if err != nil || !found || !recordDataStringEquals(rec.Data, "symbol", "OVS") {
		t.Fatalf("cold GetRecord() = %+v found=%v err=%v", rec, found, err)
	}

	// The shadow comparator reads the columnar artifact and matches the source.
	report, ok, err := source.ShadowCompareSnapshot(ctx, "colsnap_ticks", Query{OrganizationID: "org_1"}, snaps)
	if err != nil || !ok || !report.Match() {
		t.Fatalf("ShadowCompareSnapshot(columnar) report=%+v ok=%v err=%v, want clean match", report, ok, err)
	}

	// Corruption: valid magic + truncated body errors, never panics.
	truncated := colPayload[:len(colPayload)/2]
	if err := streamSnapshotRecords(truncated, func(database.DomainRecord) error { return nil }); !errors.Is(err, ErrSnapshotCorrupt) {
		t.Fatalf("truncated columnar err = %v, want ErrSnapshotCorrupt", err)
	}
}

// BenchmarkHermesWarmFromSnapshotFormats compares cold-warm cost per artifact
// format on the same 10K-record partition. artifact_bytes reports payload
// size; the columnar row exists to prove the decode-bound insight from the
// 2026-07-01 ledger entry (proto decode dominated warm allocations).
func BenchmarkHermesWarmFromSnapshotFormats(b *testing.B) {
	const org = "org_1"
	const count = 10000
	ctx := context.Background()
	source := newBenchStore(b)
	if _, err := source.ApplyRecords(ctx, "bench_ticks", "seed", 1, benchSnapshotRecords(org, count)); err != nil {
		b.Fatalf("seed ApplyRecords() error = %v", err)
	}

	protoDesc, protoPayload, err := source.ExportSnapshot(ctx, "bench_ticks", Query{OrganizationID: org})
	if err != nil {
		b.Fatalf("ExportSnapshot() error = %v", err)
	}
	colDesc, colPayload, err := source.ExportSnapshotColumnar(ctx, "bench_ticks", Query{OrganizationID: org})
	if err != nil {
		b.Fatalf("ExportSnapshotColumnar() error = %v", err)
	}

	for _, lane := range []struct {
		name    string
		desc    SnapshotDescriptor
		payload []byte
	}{
		{"proto_rows", protoDesc, protoPayload},
		{"columnar", colDesc, colPayload},
	} {
		b.Run(fmt.Sprintf("format=%s", lane.name), func(b *testing.B) {
			snaps := NewMemorySnapshotStore()
			if err := snaps.Save(ctx, lane.desc, lane.payload); err != nil {
				b.Fatalf("Save() error = %v", err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				cold, err := NewStore(benchSnapshotSpec())
				if err != nil {
					b.Fatalf("NewStore() error = %v", err)
				}
				if _, ok, err := cold.WarmFromSnapshot(ctx, "bench_ticks", snaps); err != nil || !ok {
					b.Fatalf("WarmFromSnapshot() ok=%v err=%v", ok, err)
				}
			}
			b.ReportMetric(float64(len(lane.payload)), "artifact_bytes")
		})
	}
}
