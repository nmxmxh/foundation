package hermes

import (
	"context"
	"fmt"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

// benchDeleteRecords builds n records in one scope, already materialized in
// both the mirror and the hot partition, ready to be deleted.
func benchDeleteRecords(b *testing.B, n int) []database.DomainRecord {
	b.Helper()
	records := make([]database.DomainRecord, 0, n)
	for index := range n {
		records = append(records, database.DomainRecord{
			Domain: "marketplace", Collection: "orders", OrganizationID: "org_1",
			RecordID: fmt.Sprintf("order_%d", index),
			Data:     testRecordData(map[string]any{"customer_id": "cust_alice"}),
		})
	}
	return records
}

func newDeleteBenchStore(b *testing.B, capacity int) *ProjectedRuntimeStore {
	b.Helper()
	store, err := WrapRuntimeStore(database.NewMemoryDB(), RuntimeStoreOptions{
		MaxRecordsPerScope: capacity + 1,
		MaxBytesPerScope:   1 << 30,
	})
	if err != nil {
		b.Fatalf("WrapRuntimeStore() err=%v", err)
	}
	return store
}

// BenchmarkDeleteRecordsPerRecord is the baseline: one DeleteRecordWithFields
// per row, which is what the mirror sweeper's delete lanes do today. Each call
// takes the partition lock, runs a full apply cycle, publishes indexes and
// notifies observers — so a sweep of N deletions pays N of everything, and the
// projection gateway encodes N fan-out frames instead of one.
func BenchmarkDeleteRecordsPerRecord(b *testing.B) {
	ctx := context.Background()
	for _, n := range []int{1, 16, 256} {
		b.Run(fmt.Sprintf("records=%d", n), func(b *testing.B) {
			store := newDeleteBenchStore(b, n)
			records := benchDeleteRecords(b, n)
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				if _, err := store.UpsertRecords(ctx, records); err != nil {
					b.Fatalf("UpsertRecords() err=%v", err)
				}
				b.StartTimer()
				for _, rec := range records {
					if err := store.DeleteRecordWithFields(ctx, rec); err != nil {
						b.Fatalf("DeleteRecordWithFields() err=%v", err)
					}
				}
			}
		})
	}
}

// BenchmarkDeleteRecordsBatched is the same N deletions through DeleteRecords:
// one base delete per row still (no base store batches yet), but ONE hot apply
// and one fan-out frame per scope. Compare against
// BenchmarkDeleteRecordsPerRecord at the same cardinality — the per-record
// projection cost is what should collapse toward a per-batch boundary cost.
func BenchmarkDeleteRecordsBatched(b *testing.B) {
	ctx := context.Background()
	for _, n := range []int{1, 16, 256} {
		b.Run(fmt.Sprintf("records=%d", n), func(b *testing.B) {
			store := newDeleteBenchStore(b, n)
			records := benchDeleteRecords(b, n)
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				if _, err := store.UpsertRecords(ctx, records); err != nil {
					b.Fatalf("UpsertRecords() err=%v", err)
				}
				b.StartTimer()
				if err := store.DeleteRecords(ctx, records); err != nil {
					b.Fatalf("DeleteRecords() err=%v", err)
				}
			}
		})
	}
}

// BenchmarkDeleteRecordsFanOut isolates what the projection gateway pays: with
// a subscriber attached, the per-record lane encodes one frame per deletion and
// the batched lane encodes one for the whole batch.
func BenchmarkDeleteRecordsFanOut(b *testing.B) {
	ctx := context.Background()
	const n = 256
	for _, mode := range []string{"per_record", "batched"} {
		b.Run(mode, func(b *testing.B) {
			store := newDeleteBenchStore(b, n)
			records := benchDeleteRecords(b, n)
			frames := 0
			cancel := store.Store().Observe(func(string, []AppliedMutation) { frames++ })
			defer cancel()
			b.ReportAllocs()
			sweep := deleteSweepFunc(b, store, records, mode)
			for b.Loop() {
				b.StopTimer()
				if _, err := store.UpsertRecords(ctx, records); err != nil {
					b.Fatalf("UpsertRecords() err=%v", err)
				}
				frames = 0
				b.StartTimer()
				sweep(ctx)
			}
			b.ReportMetric(float64(frames), "frames/sweep")
		})
	}
}

// deleteSweepFunc returns the delete lane under test, so the benchmark body
// stays a timing loop rather than a branch.
func deleteSweepFunc(b *testing.B, store *ProjectedRuntimeStore, records []database.DomainRecord, mode string) func(context.Context) {
	b.Helper()
	if mode == "per_record" {
		return func(ctx context.Context) {
			for _, rec := range records {
				if err := store.DeleteRecordWithFields(ctx, rec); err != nil {
					b.Fatalf("DeleteRecordWithFields() err=%v", err)
				}
			}
		}
	}
	return func(ctx context.Context) {
		if err := store.DeleteRecords(ctx, records); err != nil {
			b.Fatalf("DeleteRecords() err=%v", err)
		}
	}
}
