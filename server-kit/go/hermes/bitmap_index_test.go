package hermes

import (
	"context"
	"fmt"
	"strconv"
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

// TestBitmapIndexReleasesValuesNoRecordCarries pins the reclamation rule. A
// bitmap per value ever seen is invisible to MaxRecords and MaxBytes, so the
// working set has to track live volume rather than lifetime volume.
func TestBitmapIndexReleasesValuesNoRecordCarries(t *testing.T) {
	spec := ProjectionSpec{
		Name: "reclaim", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"country"},
		MaxRecords:    1024, MaxBytes: 8 << 20,
	}
	sc := newScopeBitmapIndex()

	mk := func(i int) database.DomainRecord {
		return database.DomainRecord{
			Domain: "signals", Collection: "ticks", OrganizationID: "org_1",
			RecordID: "r" + strconv.Itoa(i),
			Data:     database.RecordData{{Name: "country", Value: database.StringValue("C_" + strconv.Itoa(i))}},
		}
	}

	for i := range 20 {
		sc.AddRecordLocked("r"+strconv.Itoa(i), mk(i), spec)
	}
	sc.mu.RLock()
	afterAdd := len(sc.inverted)
	sc.mu.RUnlock()
	if afterAdd != 20 {
		t.Fatalf("indexed %d values, want 20", afterAdd)
	}

	for i := range 20 {
		sc.RemoveRecordLocked("r"+strconv.Itoa(i), mk(i), spec)
	}
	sc.mu.RLock()
	afterRemove := len(sc.inverted)
	slotLen := len(sc.slotToKey)
	sc.mu.RUnlock()
	if afterRemove != 0 {
		t.Fatalf("after removing every record the index still holds %d values, want 0", afterRemove)
	}
	if slotLen != 20 {
		t.Fatalf("slot table length = %d, want 20 (slots are recycled, not shrunk)", slotLen)
	}
}

// TestBitmapIndexSaturatesAndDefers covers the cap. Past the bound the index
// stops taking new values and reports itself unsaturated, so a query naming an
// unindexed value falls back to the ordinary candidate index instead of
// concluding there are no matches.
func TestBitmapIndexSaturatesAndDefers(t *testing.T) {
	spec := ProjectionSpec{
		Name: "saturate", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"country"},
		MaxRecords:    1 << 20, MaxBytes: 64 << 20,
	}
	sc := newScopeBitmapIndex()
	// The cap resolves from the byte budget and the live slot count, so drive it
	// through a slot count a real scope would reach.
	const slots = 10000
	valueCap := bitmapValueCapFor(spec.MaxBytes, slots)
	for i := range valueCap + 10 {
		rec := database.DomainRecord{
			Domain: "signals", Collection: "ticks", OrganizationID: "org_1",
			RecordID: "r" + strconv.Itoa(i),
			Data:     database.RecordData{{Name: "country", Value: database.StringValue("C_" + strconv.Itoa(i))}},
		}
		sc.AddRecordLocked("r"+strconv.Itoa(i), rec, spec)
	}

	sc.mu.RLock()
	held := len(sc.inverted)
	saturated := sc.saturated
	sc.mu.RUnlock()
	if held > valueCap {
		t.Fatalf("index holds %d values, want at most %d", held, valueCap)
	}
	if !saturated {
		t.Fatal("expected the scope to report saturation after passing the bound")
	}

	// A value past the cap was never indexed, so the answer must defer.
	if _, covered := sc.IntersectFilters([]QueryFilter{
		{Field: "country", Kind: 's', Value: "C_999999"},
	}); covered {
		t.Fatal("saturated scope claimed coverage of an unindexed value")
	}

	// A value that was indexed must still answer.
	keys, covered := sc.IntersectFilters([]QueryFilter{
		{Field: "country", Kind: 's', Value: "C_5"},
	})
	if !covered {
		t.Fatal("saturated scope must still answer for an indexed value")
	}
	if len(keys) != 1 || keys[0] != "r5" {
		t.Fatalf("keys = %v, want [r5]", keys)
	}
}

// TestBitmapIndexUnindexedValueIsZeroWhenNotSaturated is the other half: below
// the bound a missing bitmap really does mean no records carry the value, so
// the scope must report authoritative coverage rather than deferring.
func TestBitmapIndexUnindexedValueIsZeroWhenNotSaturated(t *testing.T) {
	spec := ProjectionSpec{
		Name: "authoritative", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"country"},
		MaxRecords:    1024, MaxBytes: 8 << 20,
	}
	sc := newScopeBitmapIndex()
	rec := database.DomainRecord{
		Domain: "signals", Collection: "ticks", OrganizationID: "org_1",
		RecordID: "r1",
		Data:     database.RecordData{{Name: "country", Value: database.StringValue("C_1")}},
	}
	sc.AddRecordLocked("r1", rec, spec)

	keys, covered := sc.IntersectFilters([]QueryFilter{
		{Field: "country", Kind: 's', Value: "NOPE"},
	})
	if !covered {
		t.Fatal("an unsaturated scope must report authoritative coverage")
	}
	if len(keys) != 0 {
		t.Fatalf("keys = %v, want none", keys)
	}
}

// TestBitmapIndexValueWithSeparatorStillMatches is the behavioural guard for the
// injection fix. A joined-string key could not tell field "a:s" from field "a"
// with value "b:", so this is the case that actually answers the wrong records.
func TestBitmapIndexValueWithSeparatorStillMatches(t *testing.T) {
	spec := ProjectionSpec{
		Name: "sep", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"label"},
		MaxRecords:    1024, MaxBytes: 8 << 20,
	}
	registry := NewBitmapIndexRegistry()
	scope := scopeKey("signals", "ticks", "org_1")

	records := map[string]string{
		"r1": "a:s:b:", // contains the separator twice
		"r2": "plain",
	}
	for id, label := range records {
		registry.Add(scope, id, database.DomainRecord{
			Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: id,
			Data: database.RecordData{{Name: "label", Value: database.StringValue(label)}},
		}, spec)
	}

	keys, covered := registry.QueryCompoundFilters(scope, []QueryFilter{
		{Field: "label", Kind: 's', Value: "a:s:b:"},
	})
	if !covered {
		t.Fatal("expected authoritative coverage")
	}
	if len(keys) != 1 || keys[0] != "r1" {
		t.Fatalf("keys = %v, want [r1]", keys)
	}

	// A label that shares the separator must not pull r1 in.
	keys, covered = registry.QueryCompoundFilters(scope, []QueryFilter{
		{Field: "label", Kind: 's', Value: "a:s:b"},
	})
	if !covered {
		t.Fatal("expected authoritative coverage for the second shape")
	}
	if len(keys) != 0 {
		t.Fatalf("keys = %v, want none", keys)
	}
}

// TestBitmapValueCapTracksTheByteBudget pins why the cap is derived rather than
// a flat constant. Retained words are values x slots / 64, so a fixed value cap
// retains proportionally more memory on a proportionally larger scope. These
// bounds are the real configurations observed in deployed projects.
func TestBitmapValueCapTracksTheByteBudget(t *testing.T) {
	cases := []struct {
		name     string
		maxBytes int64
		slots    int
	}{
		{"chowdash-shaped scope", 16 << 20, 10000},
		{"pronto names-shaped scope", 32 << 20, 50000},
		{"small scope", 16 << 20, 200},
		{"tiny scope", 1 << 20, 8},
		{"zero slots", 16 << 20, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := bitmapValueCapFor(tc.maxBytes, tc.slots)

			// Never zero: that would disable the index entirely.
			if got < minBitmapValuesFloor {
				t.Fatalf("cap = %d, want at least %d", got, minBitmapValuesFloor)
			}
			// Never unbounded.
			if got > maxBitmapValuesCeiling {
				t.Fatalf("cap = %d, want at most %d", got, maxBitmapValuesCeiling)
			}

			// The whole point: retained bitmap bytes stay near the intended
			// share of the budget, not a multiple of it.
			bytesPerValue := float64((tc.slots + 7) / 8)
			if bytesPerValue > 0 {
				retained := float64(got) * bytesPerValue
				budget := float64(tc.maxBytes) / bitmapBudgetDenominator * bitmapBudgetNumerator
				if retained > budget*1.15 {
					t.Fatalf("retained %.0f bytes exceeds 115%% of the %.0f byte bitmap budget", retained, budget)
				}
			}
		})
	}

	t.Run("a larger scope gets a tighter cap", func(t *testing.T) {
		small := bitmapValueCapFor(16<<20, 1000)
		large := bitmapValueCapFor(16<<20, 50000)
		if large >= small {
			t.Fatalf("cap did not tighten with scope size: slots=1000 -> %d, slots=50000 -> %d", small, large)
		}
	})

	t.Run("low cardinality fields are never capped", func(t *testing.T) {
		// The enums and audience fields the projects actually index must stay
		// fully indexed at every realistic scope size.
		for _, slots := range []int{200, 1000, 10000, 50000, 100000} {
			if cap := bitmapValueCapFor(16<<20, slots); cap < 32 {
				t.Fatalf("slots=%d gives cap %d, which would start clipping a low-cardinality field", slots, cap)
			}
		}
	})
}
