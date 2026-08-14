package hermes

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

func makeTestRecordData(pairs map[string]any) database.RecordData {
	fields := make([]database.RecordField, 0, len(pairs))
	for k, v := range pairs {
		switch val := v.(type) {
		case string:
			fields = append(fields, database.RecordField{Name: k, Value: database.StringValue(val)})
		case float64:
			fields = append(fields, database.RecordField{Name: k, Value: database.FloatValue(val)})
		case int:
			fields = append(fields, database.RecordField{Name: k, Value: database.IntValue(int64(val))})
		case []string:
			raw, err := json.Marshal(val)
			if err != nil {
				panic(err)
			}
			fields = append(fields, database.RecordField{Name: k, Value: database.RawValue(raw)})
		}
	}
	return database.RecordDataFromPairs(fields...)
}

func TestAccumulatorStateStoreBasic(t *testing.T) {
	cfg := AccumulatorConfig{
		Dimensions:     []string{"domain", "country", "tags", "status"},
		NumericMetrics: []string{"confidence", "amount"},
	}
	acc := NewAccumulatorStateStore(cfg)
	scope := ScopeKey("finance", "orders", "org_1")

	// 1. Insert record 1
	rec1 := database.DomainRecord{
		Domain:         "finance",
		Collection:     "orders",
		OrganizationID: "org_1",
		RecordID:       "ord_1",
		Data: makeTestRecordData(map[string]any{
			"country":    "JP",
			"status":     "active",
			"tags":       []string{"alpha", "beta"},
			"confidence": 0.95,
			"amount":     100.0,
		}),
	}
	acc.ApplyRecord(rec1, 1, OperationUpsert)

	// Verify country dimension
	cSummary := acc.GetDimensionSummary(scope, "country", 10)
	if cSummary.DistinctCount != 1 || cSummary.TotalCount != 1 {
		t.Fatalf("expected 1 distinct, 1 total; got %d distinct, %d total", cSummary.DistinctCount, cSummary.TotalCount)
	}
	if len(cSummary.TopValues) != 1 || cSummary.TopValues[0].Value != "JP" || cSummary.TopValues[0].Count != 1 {
		t.Fatalf("unexpected top values: %+v", cSummary.TopValues)
	}

	// Verify tags dimension (array elements)
	tagSummary := acc.GetDimensionSummary(scope, "tags", 10)
	if tagSummary.DistinctCount != 2 || tagSummary.TotalCount != 2 {
		t.Fatalf("expected 2 distinct tags, 2 total; got %d distinct, %d total", tagSummary.DistinctCount, tagSummary.TotalCount)
	}

	// Verify numeric metrics
	confSummary := acc.GetMetricSummary(scope, "confidence")
	if confSummary.Count != 1 || confSummary.Mean != 0.95 || confSummary.Sum != 0.95 {
		t.Fatalf("unexpected confidence metric: %+v", confSummary)
	}

	// 2. Insert record 2
	rec2 := database.DomainRecord{
		Domain:         "finance",
		Collection:     "orders",
		OrganizationID: "org_1",
		RecordID:       "ord_2",
		Data: makeTestRecordData(map[string]any{
			"country":    "US",
			"status":     "active",
			"tags":       []string{"beta", "gamma"},
			"confidence": 0.85,
			"amount":     200.0,
		}),
	}
	acc.ApplyRecord(rec2, 2, OperationUpsert)

	cSummary = acc.GetDimensionSummary(scope, "country", 10)
	if cSummary.DistinctCount != 2 || cSummary.TotalCount != 2 {
		t.Fatalf("expected 2 distinct countries; got %d distinct", cSummary.DistinctCount)
	}

	statusSummary := acc.GetDimensionSummary(scope, "status", 10)
	if statusSummary.DistinctCount != 1 || statusSummary.TotalCount != 2 {
		t.Fatalf("expected status 'active' count 2; got total %d", statusSummary.TotalCount)
	}

	tagSummary = acc.GetDimensionSummary(scope, "tags", 10)
	if tagSummary.DistinctCount != 3 || tagSummary.TotalCount != 4 {
		t.Fatalf("expected 3 distinct tags (alpha, beta, gamma), total 4; got distinct %d, total %d", tagSummary.DistinctCount, tagSummary.TotalCount)
	}

	amtSummary := acc.GetMetricSummary(scope, "amount")
	if amtSummary.Count != 2 || amtSummary.Sum != 300.0 || amtSummary.Mean != 150.0 {
		t.Fatalf("unexpected amount metric: %+v", amtSummary)
	}

	// 3. Update record 1 (country changes JP -> US, confidence changes 0.95 -> 0.90)
	rec1Updated := database.DomainRecord{
		Domain:         "finance",
		Collection:     "orders",
		OrganizationID: "org_1",
		RecordID:       "ord_1",
		Data: makeTestRecordData(map[string]any{
			"country":    "US",
			"status":     "closed",
			"tags":       []string{"alpha"},
			"confidence": 0.90,
			"amount":     150.0,
		}),
	}
	acc.ApplyRecord(rec1Updated, 3, OperationUpsert)

	// JP should now have 0 count and be removed from distinct
	cSummary = acc.GetDimensionSummary(scope, "country", 10)
	if cSummary.DistinctCount != 1 || cSummary.TotalCount != 2 {
		t.Fatalf("expected only US (distinct 1, total 2); got distinct %d, total %d, top: %+v", cSummary.DistinctCount, cSummary.TotalCount, cSummary.TopValues)
	}
	if cSummary.TopValues[0].Value != "US" || cSummary.TopValues[0].Count != 2 {
		t.Fatalf("expected US count 2; got %+v", cSummary.TopValues)
	}

	// 4. Test version ignore (attempt update with older version 2)
	rec1Older := rec1
	acc.ApplyRecord(rec1Older, 2, OperationUpsert)
	cSummary = acc.GetDimensionSummary(scope, "country", 10)
	if cSummary.DistinctCount != 1 || cSummary.TopValues[0].Value != "US" {
		t.Fatalf("older version was not ignored")
	}

	// 5. Test Delete
	acc.ApplyRecord(rec2, 4, OperationDelete)
	cSummary = acc.GetDimensionSummary(scope, "country", 10)
	if cSummary.DistinctCount != 1 || cSummary.TotalCount != 1 {
		t.Fatalf("expected 1 record remaining after delete; got %d", cSummary.TotalCount)
	}

	// 6. Test Facet Manifest
	manifest := acc.GetFacetManifest(scope, 5)
	if len(manifest) != len(cfg.Dimensions) {
		t.Fatalf("expected %d dimensions in manifest; got %d", len(cfg.Dimensions), len(manifest))
	}
}

func TestAccumulatorStoreObserverIntegration(t *testing.T) {
	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	spec := ProjectionSpec{
		Name:          "telemetry",
		Domain:        "ops",
		Collection:    "events",
		IndexedFields: []string{"region", "severity"},
	}
	if err := store.Register(spec); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	acc := NewAccumulatorStateStore(AccumulatorConfig{
		Dimensions:     []string{"region", "severity"},
		NumericMetrics: []string{"latency_ms"},
	})
	detach := acc.AttachToStore(store)
	defer detach()

	scope := ScopeKey("ops", "events", "org_test")

	// Apply mutations through store
	rec := database.DomainRecord{
		Domain:         "ops",
		Collection:     "events",
		OrganizationID: "org_test",
		RecordID:       "evt_1",
		Data: makeTestRecordData(map[string]any{
			"region":     "us-east",
			"severity":   "warn",
			"latency_ms": 42.5,
		}),
	}

	ctx := context.Background()
	_, err = store.ApplyRecords(ctx, "telemetry", "test_source", 1, []database.DomainRecord{rec})
	if err != nil {
		t.Fatalf("ApplyRecords failed: %v", err)
	}

	summary := acc.GetDimensionSummary(scope, "region", 10)
	if summary.DistinctCount != 1 || summary.TotalCount != 1 || len(summary.TopValues) == 0 || summary.TopValues[0].Value != "us-east" {
		t.Fatalf("expected us-east region summary; got %+v", summary)
	}

	latSummary := acc.GetMetricSummary(scope, "latency_ms")
	if latSummary.Count != 1 || latSummary.Sum != 42.5 {
		t.Fatalf("expected latency_ms 42.5; got %+v", latSummary)
	}
}

func TestAccumulatorConcurrentApplies(t *testing.T) {
	acc := NewAccumulatorStateStore(AccumulatorConfig{
		Dimensions:     []string{"status", "worker"},
		NumericMetrics: []string{"duration"},
	})
	scope := ScopeKey("compute", "jobs", "org_bench")

	var wg sync.WaitGroup
	workers := 10
	iterations := 100

	for w := range workers {
		wg.Add(1)
		workerID := fmt.Sprintf("worker_%d", w)
		go func(wid string, workerIdx int) {
			defer wg.Done()
			for i := range iterations {
				recID := fmt.Sprintf("job_%d_%d", workerIdx, i)
				rec := database.DomainRecord{
					Domain:         "compute",
					Collection:     "jobs",
					OrganizationID: "org_bench",
					RecordID:       recID,
					Data: makeTestRecordData(map[string]any{
						"status":   "running",
						"worker":   wid,
						"duration": float64(i + 1),
					}),
				}
				acc.ApplyRecord(rec, uint64(i+1), OperationUpsert)
			}
		}(workerID, w)
	}

	wg.Wait()

	statusSummary := acc.GetDimensionSummary(scope, "status", 10)
	expectedTotal := int64(workers * iterations)
	if statusSummary.TotalCount != expectedTotal {
		t.Fatalf("expected %d total; got %d", expectedTotal, statusSummary.TotalCount)
	}

	workerSummary := acc.GetDimensionSummary(scope, "worker", 20)
	if workerSummary.DistinctCount != int64(workers) {
		t.Fatalf("expected %d distinct workers; got %d", workers, workerSummary.DistinctCount)
	}
}

func BenchmarkAccumulatorGetDimensionSummary(b *testing.B) {
	acc := NewAccumulatorStateStore(AccumulatorConfig{
		Dimensions: []string{"category"},
	})
	scope := ScopeKey("catalog", "items", "org_1")

	for i := range 1000 {
		rec := database.DomainRecord{
			Domain:         "catalog",
			Collection:     "items",
			OrganizationID: "org_1",
			RecordID:       fmt.Sprintf("item_%d", i),
			Data: makeTestRecordData(map[string]any{
				"category": fmt.Sprintf("cat_%d", i%20),
			}),
		}
		acc.ApplyRecord(rec, uint64(i+1), OperationUpsert)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_ = acc.GetDimensionSummary(scope, "category", 10)
	}
}
