package hermes

import (
	"context"
	"fmt"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

func TestBitmapIndexMultiAttribute(t *testing.T) {
	registry := NewBitmapIndexRegistry()
	scope := recordScope{domain: "weather", collection: "stations", organizationID: "org_alpha"}
	spec := ProjectionSpec{
		Domain:        "weather",
		Collection:    "stations",
		IndexedFields: []string{"country", "domain", "status"},
	}

	// 1. Add 4 records
	// rec1: country=JP, domain=climate, status=active
	rec1 := database.DomainRecord{
		Domain: "weather", Collection: "stations", OrganizationID: "org_alpha", RecordID: "rec_1",
		Data: database.RecordDataFromPairs(
			database.RecordField{Name: "country", Value: database.StringValue("JP")},
			database.RecordField{Name: "domain", Value: database.StringValue("climate")},
			database.RecordField{Name: "status", Value: database.StringValue("active")},
		),
	}
	// rec2: country=JP, domain=marine, status=active
	rec2 := database.DomainRecord{
		Domain: "weather", Collection: "stations", OrganizationID: "org_alpha", RecordID: "rec_2",
		Data: database.RecordDataFromPairs(
			database.RecordField{Name: "country", Value: database.StringValue("JP")},
			database.RecordField{Name: "domain", Value: database.StringValue("marine")},
			database.RecordField{Name: "status", Value: database.StringValue("active")},
		),
	}
	// rec3: country=US, domain=climate, status=active
	rec3 := database.DomainRecord{
		Domain: "weather", Collection: "stations", OrganizationID: "org_alpha", RecordID: "rec_3",
		Data: database.RecordDataFromPairs(
			database.RecordField{Name: "country", Value: database.StringValue("US")},
			database.RecordField{Name: "domain", Value: database.StringValue("climate")},
			database.RecordField{Name: "status", Value: database.StringValue("active")},
		),
	}
	// rec4: country=JP, domain=climate, status=inactive
	rec4 := database.DomainRecord{
		Domain: "weather", Collection: "stations", OrganizationID: "org_alpha", RecordID: "rec_4",
		Data: database.RecordDataFromPairs(
			database.RecordField{Name: "country", Value: database.StringValue("JP")},
			database.RecordField{Name: "domain", Value: database.StringValue("climate")},
			database.RecordField{Name: "status", Value: database.StringValue("inactive")},
		),
	}

	registry.Add(scope, "rec_1", rec1, spec)
	registry.Add(scope, "rec_2", rec2, spec)
	registry.Add(scope, "rec_3", rec3, spec)
	registry.Add(scope, "rec_4", rec4, spec)

	// Query 1: country=JP AND domain=climate -> should match rec_1 and rec_4
	filters1 := []QueryFilter{
		{Field: "country", Kind: 's', Value: "JP"},
		{Field: "domain", Kind: 's', Value: "climate"},
	}
	keys1, ok := registry.QueryCompoundFilters(scope, filters1)
	if !ok {
		t.Fatalf("expected bitmap query to cover filters")
	}
	if len(keys1) != 2 || keys1[0] != "rec_1" || keys1[1] != "rec_4" {
		t.Fatalf("expected [rec_1, rec_4]; got %+v", keys1)
	}

	// Query 2: country=JP AND domain=climate AND status=active -> should match rec_1 only
	filters2 := []QueryFilter{
		{Field: "country", Kind: 's', Value: "JP"},
		{Field: "domain", Kind: 's', Value: "climate"},
		{Field: "status", Kind: 's', Value: "active"},
	}
	keys2, ok := registry.QueryCompoundFilters(scope, filters2)
	if !ok {
		t.Fatalf("expected bitmap query to cover filters")
	}
	if len(keys2) != 1 || keys2[0] != "rec_1" {
		t.Fatalf("expected [rec_1]; got %+v", keys2)
	}

	// Query 3: country=UK -> zero matches
	filters3 := []QueryFilter{
		{Field: "country", Kind: 's', Value: "UK"},
	}
	keys3, ok := registry.QueryCompoundFilters(scope, filters3)
	if !ok || len(keys3) != 0 {
		t.Fatalf("expected zero keys for UK; got ok=%v, keys=%+v", ok, keys3)
	}

	// 2. Remove rec_1 and verify slot recycling on new insert
	registry.Remove(scope, "rec_1", rec1, spec)
	keysAfterRemove, _ := registry.QueryCompoundFilters(scope, filters1)
	if len(keysAfterRemove) != 1 || keysAfterRemove[0] != "rec_4" {
		t.Fatalf("expected only rec_4 after rec_1 removal; got %+v", keysAfterRemove)
	}

	// Add rec_5 into recycled slot
	rec5 := database.DomainRecord{
		Domain: "weather", Collection: "stations", OrganizationID: "org_alpha", RecordID: "rec_5",
		Data: database.RecordDataFromPairs(
			database.RecordField{Name: "country", Value: database.StringValue("JP")},
			database.RecordField{Name: "domain", Value: database.StringValue("climate")},
			database.RecordField{Name: "status", Value: database.StringValue("active")},
		),
	}
	registry.Add(scope, "rec_5", rec5, spec)
	keysAfterRecycle, _ := registry.QueryCompoundFilters(scope, filters2)
	if len(keysAfterRecycle) != 1 || keysAfterRecycle[0] != "rec_5" {
		t.Fatalf("expected rec_5 in recycled slot; got %+v", keysAfterRecycle)
	}
}

func TestBitmapIndexStoreIntegration(t *testing.T) {
	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	spec := ProjectionSpec{
		Name:          "signals",
		Domain:        "geo",
		Collection:    "sensors",
		IndexedFields: []string{"country", "domain", "status"},
	}
	if err := store.Register(spec); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	records := []database.DomainRecord{
		{
			Domain: "geo", Collection: "sensors", OrganizationID: "org_1", RecordID: "s1",
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "country", Value: database.StringValue("JP")},
				database.RecordField{Name: "domain", Value: database.StringValue("climate")},
				database.RecordField{Name: "status", Value: database.StringValue("active")},
			),
		},
		{
			Domain: "geo", Collection: "sensors", OrganizationID: "org_1", RecordID: "s2",
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "country", Value: database.StringValue("JP")},
				database.RecordField{Name: "domain", Value: database.StringValue("marine")},
				database.RecordField{Name: "status", Value: database.StringValue("active")},
			),
		},
		{
			Domain: "geo", Collection: "sensors", OrganizationID: "org_1", RecordID: "s3",
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "country", Value: database.StringValue("JP")},
				database.RecordField{Name: "domain", Value: database.StringValue("climate")},
				database.RecordField{Name: "status", Value: database.StringValue("active")},
			),
		},
	}

	ctx := context.Background()
	_, err = store.ApplyRecords(ctx, "signals", "test", 1, records)
	if err != nil {
		t.Fatalf("ApplyRecords failed: %v", err)
	}

	query := Query{
		OrganizationID: "org_1",
		Plan: QueryPlan{
			filters: []QueryFilter{
				{Field: "country", Kind: 's', Value: "JP"},
				{Field: "domain", Kind: 's', Value: "climate"},
			},
			count: 2,
		},
	}

	count, err := store.Count(ctx, "signals", query, Fence{})
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected count 2; got %d", count)
	}

	batch, err := store.GetColumnarBatch(ctx, "signals", query, []string{"country", "domain"}, Fence{})
	if err != nil {
		t.Fatalf("GetColumnarBatch failed: %v", err)
	}
	if batch.Rows != 2 {
		t.Fatalf("expected 2 batch rows; got %d", batch.Rows)
	}
}

func BenchmarkBitmapIndexQuery(b *testing.B) {
	registry := NewBitmapIndexRegistry()
	scope := recordScope{domain: "test", collection: "items", organizationID: "org_1"}
	spec := ProjectionSpec{
		Domain:        "test",
		Collection:    "items",
		IndexedFields: []string{"country", "domain", "status"},
	}

	for i := range 10000 {
		rec := database.DomainRecord{
			Domain: "test", Collection: "items", OrganizationID: "org_1", RecordID: fmt.Sprintf("rec_%d", i),
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "country", Value: database.StringValue(fmt.Sprintf("C_%d", i%5))},
				database.RecordField{Name: "domain", Value: database.StringValue(fmt.Sprintf("D_%d", i%10))},
				database.RecordField{Name: "status", Value: database.StringValue("active")},
			),
		}
		registry.Add(scope, rec.RecordID, rec, spec)
	}

	filters := []QueryFilter{
		{Field: "country", Kind: 's', Value: "C_1"},
		{Field: "domain", Kind: 's', Value: "D_1"},
		{Field: "status", Kind: 's', Value: "active"},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		keys, ok := registry.QueryCompoundFilters(scope, filters)
		if !ok || len(keys) == 0 {
			b.Fatalf("unexpected benchmark result")
		}
	}
}
