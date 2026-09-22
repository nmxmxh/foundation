package hermes

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

func BenchmarkHermesColumnarAssembly(b *testing.B) {
	for _, rows := range []int{128, 10000} {
		store := buildSelectFixtureStore(b, rows)
		for _, mode := range []string{"full", "limit50", "predicate_limit50"} {
			b.Run(fmt.Sprintf("rows%d/%s", rows, mode), func(b *testing.B) {
				query := Query{OrganizationID: "org_1"}
				if mode != "full" {
					query.Limit = 50
				}
				fields := []string{"price", "bucket"}
				predicates := []ColumnPredicate{PredicateFloat64("price", CompareGe, float64(rows)*1.125)}
				b.ReportAllocs()
				for b.Loop() {
					var batch *RecordBatch
					var err error
					if mode == "predicate_limit50" {
						batch, err = store.GetColumnarBatchWhere(context.Background(), "ticks", query, fields, predicates, Fence{})
					} else {
						batch, err = store.GetColumnarBatch(context.Background(), "ticks", query, fields, Fence{})
					}
					if err != nil || batch.Rows == 0 {
						b.Fatalf("assembly failed: %v", err)
					}
				}
			})
		}
	}
}

func TestColumnarLimitUsesCanonicalOrder(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{Name: "ticks", Domain: "signals", Collection: "ticks", IndexedFields: []string{"group", "side"}})
	base := time.Unix(1700000000, 0).UTC()
	records := make([]database.DomainRecord, 8)
	for i := range records {
		records[i] = database.DomainRecord{
			Domain: "signals", Collection: "ticks", OrganizationID: "org_1",
			RecordID: fmt.Sprintf("tick_%d", i), UpdatedAt: base.Add(-time.Duration(i) * time.Minute),
			Data: database.RecordDataFromPairs(database.RecordField{Name: "group", Value: database.StringValue("same")},
				database.RecordField{Name: "side", Value: database.StringValue("same")}),
		}
	}
	if _, err := store.BulkLoad(t.Context(), "ticks", records); err != nil {
		t.Fatal(err)
	}
	full, err := store.GetColumnarBatch(t.Context(), "ticks", Query{OrganizationID: "org_1"}, []string{"record_id"}, Fence{})
	if err != nil {
		t.Fatal(err)
	}
	want := full.Columns[0].Data.StringValues()[:3]
	group, _ := NewQueryFilter("group", "same")
	side, _ := NewQueryFilter("side", "same")
	for _, query := range []Query{QueryWithFilters("org_1", 3), QueryWithFilters("org_1", 3, group), QueryWithFilters("org_1", 3, group, side)} {
		limited, err := store.GetColumnarBatch(t.Context(), "ticks", query, []string{"record_id"}, Fence{})
		if err != nil {
			t.Fatal(err)
		}
		if got := limited.Columns[0].Data.StringValues(); !slices.Equal(got, want) {
			t.Fatalf("limited order = %v, want %v", got, want)
		}
	}
}

func BenchmarkHermesColumnarUnorderedLimit50(b *testing.B) {
	for _, rows := range []int{128, 10000} {
		b.Run(fmt.Sprintf("rows%d", rows), func(b *testing.B) {
			store := buildSelectFixtureStore(b, rows)
			_, err := store.Apply(b.Context(), "ticks", Event{
				Operation: OperationPatch, SourceID: "out-of-order", Version: uint64(rows + 1),
				Record: database.DomainRecord{Domain: "signals", Collection: "ticks", OrganizationID: "org_1",
					RecordID: "tick_000000", UpdatedAt: time.Unix(1, 0)},
			})
			if err != nil {
				b.Fatal(err)
			}
			part, err := store.partition("ticks")
			if err != nil || !part.activeRegistry().columnarUnordered.Load() {
				b.Fatal("fixture must invalidate the publication order proof")
			}
			query := Query{OrganizationID: "org_1", Limit: 50}
			fields := []string{"price", "bucket"}
			b.ReportAllocs()
			for b.Loop() {
				batch, err := store.GetColumnarBatch(b.Context(), "ticks", query, fields, Fence{})
				if err != nil || batch.Rows != 50 {
					b.Fatalf("bounded selection failed: %v", err)
				}
			}
		})
	}
}
