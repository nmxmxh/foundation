package hermes

import (
	"context"
	"fmt"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

// Allocation and latency baselines for the hermes point-read lane, the hot
// path this optimization cycle targets (SEO facets, organization profiles).
// Run with -benchmem; compare against the CachedStateStore benchmarks in
// server-kit/go/database to decide which layer should serve a read.

const (
	benchDomain     = "bench"
	benchCollection = "facets"
)

func benchRecord(i int) database.DomainRecord {
	return database.DomainRecord{
		Domain:         benchDomain,
		Collection:     benchCollection,
		OrganizationID: fmt.Sprintf("org-%d", i%64),
		RecordID:       fmt.Sprintf("rec-%d", i),
		Data: database.RecordDataFromPairs(
			database.RecordField{Name: "title", Value: database.StringValue(fmt.Sprintf("facet-%d", i))},
			database.RecordField{Name: "kind", Value: database.StringValue("seo")},
			database.RecordField{Name: "payload", Value: database.RawValue([]byte(`{"depth":3,"n":9007199254740993}`))},
		),
	}
}

func newBenchProjectedStore(b *testing.B, records int) *ProjectedRuntimeStore {
	b.Helper()
	projected, err := WrapRuntimeStore(database.NewMemoryDB(), RuntimeStoreOptions{})
	if err != nil {
		b.Fatalf("wrap: %v", err)
	}
	ctx := context.Background()
	for i := range records {
		if _, err := projected.UpsertRecord(ctx, benchRecord(i)); err != nil {
			b.Fatalf("seed %d: %v", i, err)
		}
	}
	return projected
}

// BenchmarkHermesPointReadHotPartition measures repeated reads of one record
// already resident in a warm partition. This is the number to beat before
// adding another cache layer in front of hermes.
func BenchmarkHermesPointReadHotPartition(b *testing.B) {
	projected := newBenchProjectedStore(b, 1)
	ctx := context.Background()
	rec := benchRecord(0)
	if _, found, err := projected.GetRecord(ctx, rec.Domain, rec.Collection, rec.OrganizationID, rec.RecordID); err != nil || !found {
		b.Fatalf("warm: found=%v err=%v", found, err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, found, err := projected.GetRecord(ctx, rec.Domain, rec.Collection, rec.OrganizationID, rec.RecordID); err != nil || !found {
			b.Fatalf("read: found=%v err=%v", found, err)
		}
	}
}

// BenchmarkHermesPointReadRotatingKeys defeats per-key CPU caches across 64
// org scopes, approximating production fan-out per request.
func BenchmarkHermesPointReadRotatingKeys(b *testing.B) {
	const keys = 4096
	projected := newBenchProjectedStore(b, keys)
	ctx := context.Background()
	for i := range keys {
		rec := benchRecord(i)
		if _, found, err := projected.GetRecord(ctx, rec.Domain, rec.Collection, rec.OrganizationID, rec.RecordID); err != nil || !found {
			b.Fatalf("warm %d: found=%v err=%v", i, found, err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := benchRecord(i % keys)
		if _, found, err := projected.GetRecord(ctx, rec.Domain, rec.Collection, rec.OrganizationID, rec.RecordID); err != nil || !found {
			b.Fatalf("read: found=%v err=%v", found, err)
		}
	}
}

// BenchmarkHermesPointWrite measures the write path: partition apply plus
// durable mirror into the base store.
func BenchmarkHermesPointWrite(b *testing.B) {
	projected := newBenchProjectedStore(b, 1)
	ctx := context.Background()
	rec := benchRecord(0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mutated := rec
		mutated.Data = rec.Data.With("title", database.StringValue(fmt.Sprintf("facet-%d", i)))
		if _, err := projected.UpsertRecord(ctx, mutated); err != nil {
			b.Fatalf("write: %v", err)
		}
	}
}
