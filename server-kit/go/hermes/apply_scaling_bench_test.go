package hermes

import (
	"context"
	"fmt"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

// Apply cost as a function of projection size (TE-18 item 6).
//
// BenchmarkHermesApplyEventUpsert seeds nothing and lets the harness decide how
// many events to apply, so its reported per-op cost is a function of -benchtime
// rather than of the projection. Reading it as a per-event constant is a trap:
// the same benchmark reports ~4.2 KB/op at a thousand iterations and ~92 KB/op
// at two hundred thousand, and neither number is "the" cost of applying an
// event.
//
// This benchmark fixes the projection size first and then measures steady-state
// apply against it, so the size series says something about the data structure
// instead of about the harness. What it shows is that per-event apply cost grows
// with the number of live keys: the index keeps a chain of delta snapshots and
// flattens it whenever the chain passes maxIndexDeltaDepth, and that flattening
// walks every live key. With the depth bound a constant, the amortized cost per
// event is proportional to the index size rather than to the size of the change.
//
// The bound exists to keep read walks shallow, so it is not simply wrong — the
// two costs are in genuine tension and resolving it means tiering the index
// rather than tuning a number. This benchmark is here so the tradeoff stays
// measured, and so a change that alters it is visible rather than discovered.
func BenchmarkHermesApplyAtProjectionSize(b *testing.B) {
	sizes := []int{1_000, 10_000, 100_000}
	for _, size := range sizes {
		b.Run(fmt.Sprintf("%dkeys", size), func(b *testing.B) {
			store := newBenchStore(b)
			ctx := context.Background()

			seed := make([]database.DomainRecord, size)
			for i := range seed {
				seed[i] = database.DomainRecord{
					Domain:         "signals",
					Collection:     "ticks",
					OrganizationID: "org_1",
					RecordID:       fmt.Sprintf("tick_%08d", i),
					Data:           testRecordData(map[string]any{"bucket": i % 16, "symbol": "OVS"}),
				}
			}
			if _, err := store.BulkLoad(ctx, "bench_ticks", seed); err != nil {
				b.Fatalf("BulkLoad: %v", err)
			}

			b.ReportAllocs()
			b.ResetTimer()

			// Updates in place against a projection already at `size`, so the
			// live key count stays fixed for the whole run and the measured
			// cost is attributable to that size alone.
			version := uint64(1)
			applied := 0
			for i := 0; b.Loop(); i++ {
				version++
				result, err := store.Apply(ctx, "bench_ticks", Event{
					Operation: OperationUpsert,
					SourceID:  fmt.Sprintf("scale_%d_%d", size, i),
					Version:   version,
					Record: database.DomainRecord{
						Domain:         "signals",
						Collection:     "ticks",
						OrganizationID: "org_1",
						RecordID:       fmt.Sprintf("tick_%08d", i%size),
						Data:           testRecordData(map[string]any{"bucket": i % 16, "symbol": "OVS"}),
					},
				})
				if err != nil {
					b.Fatalf("Apply: %v", err)
				}
				applied += result.Applied
			}
			b.StopTimer()

			if applied == 0 {
				b.Fatal("every apply was ignored; the benchmark measured rejection, not work")
			}
			// Reported so a regression that changes the shape of the curve is
			// attributable rather than mysterious.
			b.ReportMetric(float64(size), "keys")
		})
	}
}
