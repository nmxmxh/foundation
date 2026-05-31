package hermes

import (
	"context"
	"fmt"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

func BenchmarkHermesGetRecordCopied(b *testing.B) {
	store := benchmarkStore(b, 10000)
	ctx := context.Background()
	query := Query{OrganizationID: "org_1"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, ok, err := store.GetRecord(ctx, "bench_ticks", query, "tick_000123", Fence{})
		if err != nil || !ok {
			b.Fatalf("GetRecord() ok=%v err=%v", ok, err)
		}
	}
}

func BenchmarkHermesForEachViewLimit50(b *testing.B) {
	store := benchmarkStore(b, 10000)
	ctx := context.Background()
	query := Query{
		OrganizationID: "org_1",
		Filters:        map[string]any{"bucket": 7},
		Limit:          50,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		seen, err := store.ForEachView(ctx, "bench_ticks", query, Fence{}, func(view RecordView) error {
			if view.RecordID == "" {
				b.Fatal("empty view")
			}
			return nil
		})
		if err != nil || seen != 50 {
			b.Fatalf("ForEachView() seen=%d err=%v", seen, err)
		}
	}
}

func BenchmarkHermesCountIndexed(b *testing.B) {
	store := benchmarkStore(b, 10000)
	ctx := context.Background()
	query := Query{
		OrganizationID: "org_1",
		Filters:        map[string]any{"bucket": 7},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		count, err := store.Count(ctx, "bench_ticks", query, Fence{})
		if err != nil || count == 0 {
			b.Fatalf("Count() count=%d err=%v", count, err)
		}
	}
}

func BenchmarkHermesListRecordsCopiedLimit50(b *testing.B) {
	store := benchmarkStore(b, 10000)
	ctx := context.Background()
	query := Query{
		OrganizationID: "org_1",
		Filters:        map[string]any{"bucket": 7},
		Limit:          50,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		items, err := store.ListRecords(ctx, "bench_ticks", query, Fence{})
		if err != nil || len(items) != 50 {
			b.Fatalf("ListRecords() len=%d err=%v", len(items), err)
		}
	}
}

func BenchmarkHermesApplyEventUpsert(b *testing.B) {
	store := newBenchStore(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := store.Apply(ctx, "bench_ticks", Event{
			Operation: OperationUpsert,
			SourceID:  fmt.Sprintf("bench_%d", i),
			Version:   uint64(i + 1),
			Record: database.DomainRecord{
				Domain:         "signals",
				Collection:     "ticks",
				OrganizationID: "org_1",
				RecordID:       fmt.Sprintf("tick_%06d", i%100000),
				Data:           map[string]any{"bucket": i % 16, "symbol": "OVS"},
			},
		})
		if err != nil {
			b.Fatalf("Apply() error = %v", err)
		}
	}
}

func BenchmarkHermesApplyBatch64(b *testing.B) {
	store := newBenchStore(b)
	ctx := context.Background()
	events := make([]Event, 64)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range events {
			id := i*len(events) + j
			events[j] = Event{
				Operation: OperationUpsert,
				SourceID:  fmt.Sprintf("batch_%d", id),
				Version:   uint64(id + 1),
				Record: database.DomainRecord{
					Domain:         "signals",
					Collection:     "ticks",
					OrganizationID: "org_1",
					RecordID:       fmt.Sprintf("tick_%06d", id%100000),
					Data:           map[string]any{"bucket": id % 16, "symbol": "OVS"},
				},
			}
		}
		if _, err := store.ApplyBatch(ctx, "bench_ticks", events); err != nil {
			b.Fatalf("ApplyBatch() error = %v", err)
		}
	}
}

func BenchmarkHermesApplyRecords64(b *testing.B) {
	store := newBenchStore(b)
	ctx := context.Background()
	records := make([]database.DomainRecord, 64)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range records {
			id := i*len(records) + j
			records[j] = database.DomainRecord{
				Domain:         "signals",
				Collection:     "ticks",
				OrganizationID: "org_1",
				RecordID:       fmt.Sprintf("tick_%06d", id%100000),
				Data:           map[string]any{"bucket": id % 16, "symbol": "OVS"},
			}
		}
		if _, err := store.ApplyRecords(ctx, "bench_ticks", "records", uint64(i*len(records)+1), records); err != nil {
			b.Fatalf("ApplyRecords() error = %v", err)
		}
	}
}

func BenchmarkHermesBulkLoad512(b *testing.B) {
	store := newBenchStore(b)
	ctx := context.Background()
	records := make([]database.DomainRecord, 512)
	for i := range records {
		records[i] = database.DomainRecord{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       fmt.Sprintf("tick_%06d", i),
			Data:           map[string]any{"bucket": i % 16, "symbol": "OVS"},
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.BulkLoad(ctx, "bench_ticks", records); err != nil {
			b.Fatalf("BulkLoad() error = %v", err)
		}
	}
}

func BenchmarkHermesApplyRecordPayloads64(b *testing.B) {
	store := newBenchStore(b)
	ctx := context.Background()
	payloads := make([]RecordPayload, 64)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range payloads {
			id := i*len(payloads) + j
			payloads[j] = RecordPayload{
				SourceID:      fmt.Sprintf("payload_%d", id),
				Version:       uint64(id + 1),
				EventType:     "signals:ticks:success",
				SchemaVersion: "capnp.signals.ticks.v1",
				Payload:       []byte{byte(id % 16)},
			}
		}
		if _, err := store.ApplyRecordPayloads(ctx, "bench_ticks", payloads, benchPayloadDecoder); err != nil {
			b.Fatalf("ApplyRecordPayloads() error = %v", err)
		}
	}
}

func BenchmarkHermesApplyRecordPayloadEvents64(b *testing.B) {
	store := newBenchStore(b)
	ctx := context.Background()
	payloads := make([]RecordPayload, 64)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range payloads {
			id := i*len(payloads) + j
			payloads[j] = RecordPayload{
				SourceID:      fmt.Sprintf("payload_event_%d", id),
				Version:       uint64(id + 1),
				EventType:     "signals:ticks:success",
				SchemaVersion: "capnp.signals.ticks.v1",
				Payload:       []byte{byte(id % 16)},
			}
		}
		if _, err := store.ApplyRecordPayloadEvents(ctx, "bench_ticks", payloads, benchPayloadEventDecoder); err != nil {
			b.Fatalf("ApplyRecordPayloadEvents() error = %v", err)
		}
	}
}

func BenchmarkHermesProjectedRuntimeStoreHotGet(b *testing.B) {
	store := benchmarkProjectedRuntimeStore(b, 10000)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, ok, err := store.GetRecord(ctx, "signals", "ticks", "org_1", "tick_000123")
		if err != nil || !ok {
			b.Fatalf("GetRecord() ok=%v err=%v", ok, err)
		}
	}
}

func BenchmarkHermesProjectedRuntimeStoreWarmCount(b *testing.B) {
	store := benchmarkProjectedRuntimeStore(b, 10000)
	ctx := context.Background()
	filters := map[string]any{"bucket": 7}
	if _, err := store.CountRecords(ctx, "signals", "ticks", "org_1", filters); err != nil {
		b.Fatalf("warm CountRecords() error = %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		count, err := store.CountRecords(ctx, "signals", "ticks", "org_1", filters)
		if err != nil || count == 0 {
			b.Fatalf("CountRecords() count=%d err=%v", count, err)
		}
	}
}

func BenchmarkHermesDriftCheckMerkle(b *testing.B) {
	ctx := context.Background()
	source := database.NewMemoryDB()
	for i := range 10000 {
		_, err := source.UpsertRecord(ctx, database.DomainRecord{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       fmt.Sprintf("tick_%06d", i),
			Data:           map[string]any{"bucket": i % 16, "symbol": "OVS"},
		})
		if err != nil {
			b.Fatalf("source UpsertRecord() error = %v", err)
		}
	}
	store := newBenchStore(b)
	if _, err := store.Rebuild(ctx, "bench_ticks", source, Query{OrganizationID: "org_1"}); err != nil {
		b.Fatalf("Rebuild() error = %v", err)
	}
	opts := DriftOptions{MaxRecords: 10000, SampleSize: 64}
	query := Query{OrganizationID: "org_1"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		report, err := store.CheckDrift(ctx, "bench_ticks", source, query, opts)
		if err != nil || !report.OK() {
			b.Fatalf("CheckDrift() ok=%v err=%v", report.OK(), err)
		}
	}
}

func benchmarkStore(b *testing.B, records int) *Store {
	b.Helper()
	store := newBenchStore(b)
	events := make([]Event, records)
	for i := range events {
		events[i] = Event{
			Operation: OperationUpsert,
			SourceID:  fmt.Sprintf("seed_%d", i),
			Version:   uint64(i + 1),
			Record: database.DomainRecord{
				Domain:         "signals",
				Collection:     "ticks",
				OrganizationID: "org_1",
				RecordID:       fmt.Sprintf("tick_%06d", i),
				Data:           map[string]any{"bucket": i % 16, "symbol": "OVS"},
			},
		}
	}
	if _, err := store.ApplyBatch(context.Background(), "bench_ticks", events); err != nil {
		b.Fatalf("seed ApplyBatch() error = %v", err)
	}
	return store
}

func benchmarkProjectedRuntimeStore(b *testing.B, records int) *ProjectedRuntimeStore {
	b.Helper()
	base := database.NewMemoryDB()
	store, err := WrapRuntimeStore(base, RuntimeStoreOptions{
		IndexedFields:      []string{"bucket", "symbol"},
		MaxRecordsPerScope: records + 1,
		MaxBytesPerScope:   512 << 20,
	})
	if err != nil {
		b.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	ctx := context.Background()
	for i := range records {
		_, err = store.UpsertRecord(ctx, database.DomainRecord{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       fmt.Sprintf("tick_%06d", i),
			Data:           map[string]any{"bucket": i % 16, "symbol": "OVS"},
		})
		if err != nil {
			b.Fatalf("seed UpsertRecord() error = %v", err)
		}
	}
	if _, _, err := store.GetRecord(ctx, "signals", "ticks", "org_1", "tick_000123"); err != nil {
		b.Fatalf("warm GetRecord() error = %v", err)
	}
	return store
}

func benchPayloadDecoder(_ context.Context, payload RecordPayload) (database.DomainRecord, error) {
	return database.DomainRecord{
		Domain:         "signals",
		Collection:     "ticks",
		OrganizationID: "org_1",
		RecordID:       payload.SourceID,
		Data:           map[string]any{"bucket": int(payload.Payload[0]), "symbol": "OVS"},
	}, nil
}

func benchPayloadEventDecoder(ctx context.Context, payloads []RecordPayload, events []Event) ([]Event, error) {
	for _, payload := range payloads {
		if err := ctxErr(ctx); err != nil {
			return nil, err
		}
		events = append(events, Event{
			Operation: OperationUpsert,
			SourceID:  payload.SourceID,
			Version:   payload.Version,
			Record: database.DomainRecord{
				Domain:         "signals",
				Collection:     "ticks",
				OrganizationID: "org_1",
				RecordID:       payload.SourceID,
				Data:           map[string]any{"bucket": int(payload.Payload[0]), "symbol": "OVS"},
			},
		})
	}
	return events, nil
}

func newBenchStore(b *testing.B) *Store {
	b.Helper()
	store, err := NewStore(ProjectionSpec{
		Name:             "bench_ticks",
		Domain:           "signals",
		Collection:       "ticks",
		IndexedFields:    []string{"bucket", "symbol"},
		MaxRecords:       1_000_000,
		MaxBytes:         512 << 20,
		MaxAppliedEvents: 2_000_000,
	})
	if err != nil {
		b.Fatalf("NewStore() error = %v", err)
	}
	return store
}
