package hermes

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

func BenchmarkHermesConcurrentReadWrite(b *testing.B) {
	// Initialize projection store
	store, err := NewStore(ProjectionSpec{
		Name:          "bench_contention",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"symbol"},
		MaxRecords:    100000,
		MaxBytes:      128 << 20,
	})
	if err != nil {
		b.Fatalf("NewStore() error = %v", err)
	}

	ctx := context.Background()

	// Seed records
	seedSize := 10000
	records := make([]database.DomainRecord, seedSize)
	for i := range records {
		records[i] = database.DomainRecord{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       fmt.Sprintf("tick_%06d", i),
			Data:           testRecordData(map[string]any{"symbol": "OVS"}),
		}
	}
	if _, err := store.BulkLoad(ctx, "bench_contention", records); err != nil {
		b.Fatalf("BulkLoad() error = %v", err)
	}

	// Channel to signal writers to stop
	stopCh := make(chan struct{})
	var wg sync.WaitGroup

	// Spawn background writers
	numWriters := 4
	for w := range numWriters {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			i := 0
			for {
				select {
				case <-stopCh:
					return
				default:
					event := Event{
						Operation: OperationUpsert,
						SourceID:  fmt.Sprintf("writer_%d_event_%d", writerID, i),
						Version:   uint64(i + 1),
						Record: database.DomainRecord{
							Domain:         "signals",
							Collection:     "ticks",
							OrganizationID: "org_1",
							RecordID:       fmt.Sprintf("tick_%06d", i%seedSize),
							Data:           testRecordData(map[string]any{"symbol": "OVS"}),
						},
					}
					_, _ = store.Apply(ctx, "bench_contention", event)
					i++
					time.Sleep(10 * time.Microsecond)
				}
			}
		}(w)
	}

	query := Query{OrganizationID: "org_1"}

	b.ReportAllocs()
	b.ResetTimer()

	// Run read benchmarks under concurrent write pressure
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			recordID := fmt.Sprintf("tick_%06d", i%seedSize)
			_, _, err := store.GetRecord(ctx, "bench_contention", query, recordID, Fence{})
			if err != nil {
				b.Errorf("GetRecord() error = %v", err)
			}
			i++
		}
	})

	b.StopTimer()
	close(stopCh)
	wg.Wait()
}
