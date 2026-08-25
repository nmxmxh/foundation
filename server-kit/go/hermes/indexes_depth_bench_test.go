package hermes

import (
	"context"
	"fmt"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

// Cost of a delta chain, measured on both sides of the tradeoff the depth bound
// is supposed to balance.
//
// The write side is well understood: compaction flattens the chain and walks
// every live key, so it costs O(N) and runs once every maxIndexDeltaDepth
// publishes. The read side is the half that was never measured. forEachKey has
// a fast path that iterates the live set directly, but it only applies when the
// snapshot is flat — one delta layer with a non-empty base is enough to force
// every scan onto the reconciling path, which allocates a seen map the size of
// the whole index on each call.
//
// If that is real, the depth bound is not trading reads against writes at all:
// it is leaving reads on the expensive path for the entire window between
// compactions, and lowering the bound would help reads rather than hurt them.

func benchChain(keys, depth int) *indexSnapshot {
	flat := make(map[string]struct{}, keys)
	order := make([]recordOrderEntry, 0, keys)
	for i := range keys {
		key := fmt.Sprintf("key_%08d", i)
		flat[key] = struct{}{}
		order = append(order, recordOrderEntry{key: key, version: uint64(i + 1)})
	}
	base := &indexSnapshot{adds: flat, order: order, size: keys}
	if depth == 0 {
		return base
	}
	// Layers carry one fresh key each, which is the shape a steady trickle of
	// upserts produces: the base holds the bulk, the chain holds the recent.
	current := base
	for level := 1; level <= depth; level++ {
		key := fmt.Sprintf("delta_%08d", level)
		current = &indexSnapshot{
			base:  current,
			adds:  map[string]struct{}{key: {}},
			order: []recordOrderEntry{{key: key, version: uint64(keys + level)}},
			size:  current.size + 1,
			depth: level,
		}
	}
	return current
}

// BenchmarkIndexScanByChainDepth is the read side. Depth 0 is the flat shape
// compaction produces; every other row is the shape the index spends the rest
// of its life in.
func BenchmarkIndexScanByChainDepth(b *testing.B) {
	const keys = 100_000
	for _, depth := range []int{0, 1, 8, 64, 512} {
		b.Run(fmt.Sprintf("depth%d", depth), func(b *testing.B) {
			snapshot := benchChain(keys, depth)
			b.ReportAllocs()
			var seen int
			for b.Loop() {
				count := 0
				snapshot.forEachKey(func(string) bool {
					count++
					return true
				})
				seen = count
			}
			if seen < keys {
				b.Fatalf("scanned %d keys, want at least %d", seen, keys)
			}
		})
	}
}

// BenchmarkIndexCompactByKeyCount is the write side: one full flatten, which is
// what the bound buys by running it only once per maxIndexDeltaDepth publishes.
func BenchmarkIndexCompactByKeyCount(b *testing.B) {
	for _, keys := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("%dkeys", keys), func(b *testing.B) {
			// One layer past the bound so compactIndexSnapshot does the work.
			snapshot := benchChain(keys, maxIndexDeltaDepth+1)
			b.ReportAllocs()
			var got int
			for b.Loop() {
				flat := compactIndexSnapshot(snapshot)
				got = flat.len()
			}
			if got == 0 {
				b.Fatal("compaction produced an empty index")
			}
		})
	}
}

// BenchmarkHermesCountAfterApplies measures a read in the shape a live
// projection is actually in, through the public API.
//
// Every other read benchmark in this package seeds with BulkLoad and reads
// immediately, so the index is flat and the scan takes the fast path. A
// projection is flat only in the instant after a compaction; for the 511
// publishes that follow it carries a delta chain, and that is the shape a
// serving read meets. Seeding then applying before the read is what makes the
// difference visible from outside the package.
func BenchmarkHermesCountAfterApplies(b *testing.B) {
	const records = 20_000
	// 600 crosses maxIndexDeltaDepth, so that row is the only one that reaches
	// the post-compaction shape: a flat terminal carrying the live set with a
	// short chain above it. The lower rows never compact and therefore only
	// ever measure the post-bulk-load shape, where the bulk still sits in a
	// layer above emptyIndex.
	for _, applies := range []int{0, 1, 64, 600} {
		b.Run(fmt.Sprintf("applies%d", applies), func(b *testing.B) {
			store := benchmarkStore(b, records)
			ctx := context.Background()

			for i := range applies {
				if _, err := store.Apply(ctx, "bench_ticks", Event{
					Operation: OperationUpsert,
					SourceID:  fmt.Sprintf("depth_%d", i),
					Version:   uint64(i + 1),
					Record: database.DomainRecord{
						Domain:         "signals",
						Collection:     "ticks",
						OrganizationID: "org_1",
						RecordID:       fmt.Sprintf("tick_%06d", i),
						Data:           testRecordData(map[string]any{"bucket": 7, "symbol": "OVS"}),
					},
				}); err != nil {
					b.Fatalf("Apply: %v", err)
				}
			}

			query := QueryFromRecordQuery("org_1", testRecordQuery(0, map[string]any{"bucket": 7}))
			b.ReportAllocs()
			for b.Loop() {
				count, err := store.Count(ctx, "bench_ticks", query, Fence{})
				if err != nil || count == 0 {
					b.Fatalf("Count() count=%d err=%v", count, err)
				}
			}
		})
	}
}
