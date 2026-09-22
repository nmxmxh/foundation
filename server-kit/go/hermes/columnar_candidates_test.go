package hermes

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

func TestColumnarCandidatesMatchFullSort(t *testing.T) {
	random := rand.New(rand.NewPCG(17, 23))
	entries := make([]*recordEntry, 128)
	for i := range entries {
		entries[i] = &recordEntry{version: uint64(random.IntN(4)), record: database.DomainRecord{
			RecordID: fmt.Sprintf("row_%03d", i), UpdatedAt: time.Unix(int64(random.IntN(8)), 0),
		}}
	}
	want := slices.Clone(entries)
	slices.SortFunc(want, compareColumnarEntries)
	for _, limit := range []int{-1, 0, 1, 2, 3, 7, 50, 128, 150} {
		candidates := entryCandidates{limit: limit}
		for _, entry := range entries {
			candidates.add(entry)
			if limit > 0 && len(candidates.entries) > limit {
				t.Fatalf("limit %d retained %d entries", limit, len(candidates.entries))
			}
		}
		got := sortAndLimitEntries(candidates.entries, Query{Limit: limit})
		count := len(want)
		if limit > 0 {
			count = min(count, limit)
		}
		if !slices.Equal(got, want[:count]) {
			t.Fatalf("selection differs from full sort at limit %d", limit)
		}
	}
}

func TestColumnarOrderTieBreakers(t *testing.T) {
	base := time.Unix(10, 0)
	entries := []*recordEntry{
		{version: 1, record: database.DomainRecord{RecordID: "old", UpdatedAt: base}},
		{version: 2, record: database.DomainRecord{RecordID: "b", UpdatedAt: base}},
		{version: 2, record: database.DomainRecord{RecordID: "a", UpdatedAt: base}},
		{record: database.DomainRecord{RecordID: "new", UpdatedAt: base.Add(time.Second)}},
	}
	got := sortAndLimitEntries(entries, Query{Limit: 3})
	for i, id := range []string{"new", "a", "b"} {
		if got[i].record.RecordID != id {
			t.Fatalf("row %d = %q, want %q", i, got[i].record.RecordID, id)
		}
	}
}

func TestColumnarEntryLifetimeAndInvalidCells(t *testing.T) {
	for _, value := range []any{nil, 1, (*recordCell)(nil), &recordCell{}} {
		if liveRecordEntryPointer(value) != nil {
			t.Fatalf("invalid cell %T produced an entry", value)
		}
	}
	cell := &recordCell{}
	old := &recordEntry{version: 1, expiresAt: time.Now().Add(time.Hour)}
	cell.ptr.Store(old)
	held := liveRecordEntryPointer(cell)
	cell.ptr.Store(&recordEntry{version: 2})
	if held != old || held.version != 1 || liveRecordEntryPointer(cell).version != 2 {
		t.Fatal("replacement changed the retained immutable entry")
	}
	cell.ptr.Store(&recordEntry{expiresAt: time.Now().Add(-time.Hour)})
	if liveRecordEntryPointer(cell) != nil {
		t.Fatal("expired entry remained readable")
	}
}

func TestColumnarCollectionCancellationAndScope(t *testing.T) {
	store := buildSelectFixtureStore(t, 128)
	part, err := store.partition("ticks")
	if err != nil {
		t.Fatal(err)
	}
	registry := part.activeRegistry()
	symbol, _ := NewQueryFilter("symbol", "OVS")
	bucket, _ := NewQueryFilter("bucket", 2)
	queries := []Query{{OrganizationID: "org_1"}, {OrganizationID: "org_1", Limit: 3}, QueryWithFilters("org_1", 3, symbol, bucket)}
	for _, unordered := range []bool{false, true} {
		registry.columnarUnordered.Store(unordered)
		for _, query := range queries {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := part.collectRecordEntries(ctx, registry, query); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation: query=%+v unordered=%v err=%v", query, unordered, err)
			}
		}
	}
	for _, query := range []Query{{OrganizationID: "missing"}, QueryWithFilters("missing", 1, symbol, bucket)} {
		entries, err := part.collectRecordEntries(t.Context(), registry, query)
		if err != nil || len(entries) != 0 {
			t.Fatalf("cross-scope candidates: count=%d err=%v", len(entries), err)
		}
	}
}

func TestColumnarOrderProofInvalidation(t *testing.T) {
	base := time.Unix(100, 0)
	for _, next := range []struct {
		at      time.Time
		version uint64
	}{{base.Add(-time.Second), 2}, {base, 1}, {base, 0}} {
		registry := newPartitionRegistry()
		registry.trackColumnarOrder(base, 1)
		if registry.columnarUnordered.Load() {
			t.Fatal("initial ordered publication was rejected")
		}
		registry.trackColumnarOrder(next.at, next.version)
		registry.trackColumnarOrder(base.Add(time.Second), 10)
		if !registry.columnarUnordered.Load() {
			t.Fatal("later publication restored an invalid order proof")
		}
	}
}

func TestColumnarConcurrentReplacement(t *testing.T) {
	store := buildSelectFixtureStore(t, 64)
	var work sync.WaitGroup
	work.Go(func() {
		for i := range 200 {
			_, err := store.Apply(t.Context(), "ticks", Event{
				Operation: OperationPatch, SourceID: fmt.Sprintf("replace_%d", i), Version: uint64(1000 + i),
				Record: database.DomainRecord{Domain: "signals", Collection: "ticks", OrganizationID: "org_1",
					RecordID: fmt.Sprintf("tick_%06d", i%64), Data: database.RecordDataFromPairs(
						database.RecordField{Name: "value", Value: database.IntValue(int64(1000 + i))})},
			})
			if err != nil {
				t.Errorf("concurrent patch: %v", err)
				return
			}
		}
	})
	for range 100 {
		batch, err := store.GetColumnarBatch(t.Context(), "ticks", Query{OrganizationID: "org_1", Limit: 12}, []string{"version", "value", "organization_id"}, Fence{})
		if err != nil {
			t.Errorf("concurrent batch: %v", err)
			break
		}
		values := batch.Columns[1].Data
		for row, version := range batch.Columns[0].Data.Int64Values() {
			if batch.Columns[2].Data.(*StringVector).ValueAt(row) != "org_1" {
				t.Error("tenant changed during materialization")
			}
			if values.IsValid(row) && values.Int64Values()[row] != version {
				t.Error("columns observed different record versions")
			}
		}
	}
	work.Wait()
}

type columnarPublicationContext struct {
	context.Context
	publish func()
}

func (c *columnarPublicationContext) Err() error {
	if c.publish != nil {
		publish := c.publish
		c.publish = nil
		publish()
	}
	return c.Context.Err()
}

func TestColumnarOrderProofChangesDuringRead(t *testing.T) {
	store := buildSelectFixtureStore(t, 8)
	part, err := store.partition("ticks")
	if err != nil {
		t.Fatal(err)
	}
	ctx := &columnarPublicationContext{Context: t.Context(), publish: func() {
		_, err := store.Apply(t.Context(), "ticks", Event{
			Operation: OperationPatch, SourceID: "repeat_version", Version: 1,
			Record: database.DomainRecord{Domain: "signals", Collection: "ticks", OrganizationID: "org_1",
				RecordID: "tick_000000", UpdatedAt: time.Now().Add(time.Hour)},
		})
		if err != nil {
			t.Fatal(err)
		}
	}}
	entries, err := part.collectRecordEntries(ctx, part.activeRegistry(), Query{OrganizationID: "org_1", Limit: 1})
	if err != nil || len(entries) != 1 || entries[0].record.RecordID != "tick_000000" {
		t.Fatalf("changed order proof did not rescan: entries=%v err=%v", entries, err)
	}
}

func TestColumnarRangeCollectionRejectsInvalidCandidates(t *testing.T) {
	store := buildSelectFixtureStore(t, 32)
	part, err := store.partition("ticks")
	if err != nil {
		t.Fatal(err)
	}
	registry := part.activeRegistry()
	query := Query{OrganizationID: "org_1"}
	plan, ok := part.bestRangeCandidatePlan(registry, query, []ColumnPredicate{PredicateFloat64("price", CompareGe, 0)})
	if !ok {
		t.Fatal("missing range plan")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := part.collectRangeEntries(ctx, registry, query, plan); !errors.Is(err, context.Canceled) {
		t.Fatalf("range cancellation: %v", err)
	}
	if entries, _, err := part.collectRangeEntries(t.Context(), registry, query, rangeCandidatePlan{}); err != nil || len(entries) != 0 {
		t.Fatal("empty range plan returned entries")
	}
	if entries, _, err := part.collectRangeEntries(t.Context(), registry, Query{OrganizationID: "other"}, plan); err != nil || len(entries) != 0 {
		t.Fatal("range plan crossed tenant scope")
	}
	_, err = store.Apply(t.Context(), "ticks", Event{
		Operation: OperationPatch, SourceID: "range_replaced", Version: 1000,
		Record: database.DomainRecord{Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: "tick_000000"},
	})
	if err != nil {
		t.Fatal(err)
	}
	entries, _, err := part.collectRangeEntries(t.Context(), registry, query, plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.record.RecordID == "tick_000000" {
			t.Fatal("stale range version was accepted")
		}
	}
}
