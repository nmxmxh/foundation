package hermes

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

func TestGetColumnarBatch(t *testing.T) {
	store, err := NewStore(ProjectionSpec{
		Name:          "ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"symbol", "bucket", "price"},
	})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	ctx := context.Background()
	now := time.Now().UTC()

	// Seed 3 records
	records := []database.DomainRecord{
		{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       "tick_1",
			CreatedAt:      now.Add(-time.Minute),
			UpdatedAt:      now.Add(-time.Minute),
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "symbol", Value: database.StringValue("OVS")},
				database.RecordField{Name: "bucket", Value: database.IntValue(5)},
				database.RecordField{Name: "price", Value: database.FloatValue(12.34)},
			),
		},
		{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       "tick_2",
			CreatedAt:      now,
			UpdatedAt:      now,
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "symbol", Value: database.StringValue("AAPL")},
				database.RecordField{Name: "bucket", Value: database.IntValue(10)},
				database.RecordField{Name: "price", Value: database.FloatValue(150.50)},
			),
		},
		{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       "tick_3",
			CreatedAt:      now.Add(time.Minute),
			UpdatedAt:      now.Add(time.Minute),
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "symbol", Value: database.StringValue("MSFT")},
				database.RecordField{Name: "bucket", Value: database.IntValue(15)},
				database.RecordField{Name: "price", Value: database.FloatValue(310.25)},
			),
		},
	}

	if _, err := store.BulkLoad(ctx, "ticks", records); err != nil {
		t.Fatalf("failed to bulk load: %v", err)
	}

	// Fetch a Columnar Batch
	query := Query{
		OrganizationID: "org_1",
	}
	fields := []string{"record_id", "symbol", "bucket", "price", "created_at"}

	batch, err := store.GetColumnarBatch(ctx, "ticks", query, fields, Fence{})
	if err != nil {
		t.Fatalf("GetColumnarBatch failed: %v", err)
	}

	if batch.Rows != 3 {
		t.Errorf("expected 3 rows, got %d", batch.Rows)
	}

	if len(batch.Columns) != len(fields) {
		t.Fatalf("expected %d columns, got %d", len(fields), len(batch.Columns))
	}

	// Verify Record IDs
	recordIDCol := batch.Columns[0]
	if recordIDCol.Name != "record_id" {
		t.Errorf("expected column record_id, got %s", recordIDCol.Name)
	}
	recordIDVals := recordIDCol.Data.StringValues()
	if len(recordIDVals) != 3 || recordIDVals[0] != "tick_3" || recordIDVals[1] != "tick_2" || recordIDVals[2] != "tick_1" {
		t.Errorf("unexpected record_id values: %v", recordIDVals)
	}

	// Verify symbol values
	symbolCol := batch.Columns[1]
	symbolVals := symbolCol.Data.StringValues()
	if len(symbolVals) != 3 || symbolVals[0] != "MSFT" || symbolVals[1] != "AAPL" || symbolVals[2] != "OVS" {
		t.Errorf("unexpected symbol values: %v", symbolVals)
	}

	// Verify bucket values (Int64)
	bucketCol := batch.Columns[2]
	if bucketCol.Data.Type() != TypeInt64 {
		t.Errorf("expected bucket column to be TypeInt64, got %v", bucketCol.Data.Type())
	}
	bucketVals := bucketCol.Data.Int64Values()
	if len(bucketVals) != 3 || bucketVals[0] != 15 || bucketVals[1] != 10 || bucketVals[2] != 5 {
		t.Errorf("unexpected bucket values: %v", bucketVals)
	}

	// Verify price values (Float64)
	priceCol := batch.Columns[3]
	if priceCol.Data.Type() != TypeFloat64 {
		t.Errorf("expected price column to be TypeFloat64, got %v", priceCol.Data.Type())
	}
	priceVals := priceCol.Data.Float64Values()
	if len(priceVals) != 3 || priceVals[0] != 310.25 || priceVals[1] != 150.50 || priceVals[2] != 12.34 {
		t.Errorf("unexpected price values: %v", priceVals)
	}
}

func BenchmarkHermesGetColumnarBatch(b *testing.B) {
	// Create a store with 10k records
	store, err := NewStore(ProjectionSpec{
		Name:          "ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"symbol", "bucket", "price"},
		MaxRecords:    100000,
	})
	if err != nil {
		b.Fatalf("failed to create store: %v", err)
	}

	ctx := context.Background()
	records := make([]database.DomainRecord, 10000)
	for i := range records {
		records[i] = database.DomainRecord{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       fmt.Sprintf("tick_%06d", i),
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "symbol", Value: database.StringValue("OVS")},
				database.RecordField{Name: "bucket", Value: database.IntValue(int64(i % 16))},
				database.RecordField{Name: "price", Value: database.FloatValue(float64(i) * 1.5)},
			),
		}
	}

	if _, err := store.BulkLoad(ctx, "ticks", records); err != nil {
		b.Fatalf("failed to bulk load: %v", err)
	}

	query := Query{
		OrganizationID: "org_1",
	}
	fields := []string{"record_id", "symbol", "bucket", "price"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		batch, err := store.GetColumnarBatch(ctx, "ticks", query, fields, Fence{})
		if err != nil || batch.Rows != 10000 {
			b.Fatalf("GetColumnarBatch failed: %v", err)
		}
	}
}

func BenchmarkHermesListRecordsComparison(b *testing.B) {
	store, err := NewStore(ProjectionSpec{
		Name:          "ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"symbol", "bucket", "price"},
		MaxRecords:    100000,
	})
	if err != nil {
		b.Fatalf("failed to create store: %v", err)
	}

	ctx := context.Background()
	records := make([]database.DomainRecord, 10000)
	for i := range records {
		records[i] = database.DomainRecord{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: "org_1",
			RecordID:       fmt.Sprintf("tick_%06d", i),
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "symbol", Value: database.StringValue("OVS")},
				database.RecordField{Name: "bucket", Value: database.IntValue(int64(i % 16))},
				database.RecordField{Name: "price", Value: database.FloatValue(float64(i) * 1.5)},
			),
		}
	}

	if _, err := store.BulkLoad(ctx, "ticks", records); err != nil {
		b.Fatalf("failed to bulk load: %v", err)
	}

	query := Query{
		OrganizationID: "org_1",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		list, err := store.ListRecords(ctx, "ticks", query, Fence{})
		if err != nil || len(list) != 10000 {
			b.Fatalf("ListRecords failed: %v", err)
		}
	}
}

