package hermes

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

func buildTestColumnarTable(rows int) (*ColumnarTable, error) {
	n := rows
	idVec := &StringVector{
		offsets:  make([]int32, n+1),
		buf:      make([]byte, 0, n*16),
		validity: newValidityBitmap(n),
		n:        n,
	}
	symVec := &StringVector{
		offsets:  make([]int32, n+1),
		buf:      make([]byte, 0, n*4),
		validity: newValidityBitmap(n),
		n:        n,
	}
	priceVec := newFloat64Vector(n)
	volVec := newInt64Vector(n)

	for i := range n {
		idStr := fmt.Sprintf("ord_%08d", i)
		idVec.offsets[i] = int32(len(idVec.buf))
		idVec.buf = append(idVec.buf, idStr...)
		idVec.validity.set(i)

		symStr := "AAPL"
		symVec.offsets[i] = int32(len(symVec.buf))
		symVec.buf = append(symVec.buf, symStr...)
		symVec.validity.set(i)

		priceVec.values[i] = float64(100 + (i % 500))
		priceVec.validity.set(i)

		volVec.values[i] = int64(1000 + i)
		volVec.validity.set(i)
	}
	idVec.offsets[n] = int32(len(idVec.buf))
	symVec.offsets[n] = int32(len(symVec.buf))

	batch := RecordBatch{
		Rows: n,
		Columns: []Column{
			{Name: "order_id", Data: idVec},
			{Name: "symbol", Data: symVec},
			{Name: "price", Data: priceVec},
			{Name: "volume", Data: volVec},
		},
	}
	return NewColumnarTable(batch, "order_id")
}

func TestColumnarTable_GetView(t *testing.T) {
	const count = 10_000
	table, err := buildTestColumnarTable(count)
	if err != nil {
		t.Fatalf("build table failed: %v", err)
	}

	targetID := "ord_00000420"
	view, ok := table.GetView(targetID)
	if !ok {
		t.Fatalf("expected to find %s", targetID)
	}

	id, err := view.String("order_id")
	if err != nil || id != targetID {
		t.Fatalf("id = %q, want %q", id, targetID)
	}

	sym, err := view.String("symbol")
	if err != nil || sym != "AAPL" {
		t.Fatalf("symbol = %q, want AAPL", sym)
	}

	price, err := view.Float64("price")
	if err != nil || price != float64(100+(420%500)) {
		t.Fatalf("price = %v, want %v", price, 100+(420%500))
	}

	vol, err := view.Int64("volume")
	if err != nil || vol != int64(1000+420) {
		t.Fatalf("volume = %v, want %v", vol, 1000+420)
	}

	// Missing ID check
	_, ok = table.GetView("ord_99999999")
	if ok {
		t.Fatal("expected missing ID to return false")
	}

	bufBytes, idxBytes, total, density := table.MemoryFootprint()
	t.Logf("10,000 row table memory: buf=%d idx=%d total=%d (%.2f B/record)",
		bufBytes, idxBytes, total, density)
}

func TestStore_GetColumnarTable(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(ProjectionSpec{
		Name:       "orders",
		Domain:     "trading",
		Collection: "orders",
		MaxRecords: 1000,
	})
	if err != nil {
		t.Fatalf("create store failed: %v", err)
	}

	records := []database.DomainRecord{
		{
			Domain:         "trading",
			Collection:     "orders",
			OrganizationID: "org-1",
			RecordID:       "ord-101",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "symbol", Value: database.StringValue("AAPL")},
				database.RecordField{Name: "price", Value: database.FloatValue(150.25)},
				database.RecordField{Name: "volume", Value: database.IntValue(500)},
			),
		},
		{
			Domain:         "trading",
			Collection:     "orders",
			OrganizationID: "org-1",
			RecordID:       "ord-102",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
			Data: database.RecordDataFromPairs(
				database.RecordField{Name: "symbol", Value: database.StringValue("MSFT")},
				database.RecordField{Name: "price", Value: database.FloatValue(420.50)},
				database.RecordField{Name: "volume", Value: database.IntValue(300)},
			),
		},
	}

	_, err = store.ApplyRecords(ctx, "orders", "test", 1, records)
	if err != nil {
		t.Fatalf("apply records failed: %v", err)
	}

	table, err := store.GetColumnarTable(ctx, "orders", Query{OrganizationID: "org-1"}, []string{"record_id", "symbol", "price", "volume"}, "record_id", Fence{})
	if err != nil {
		t.Fatalf("get columnar table failed: %v", err)
	}
	if table.Rows() != 2 {
		t.Fatalf("table rows = %d, want 2", table.Rows())
	}

	view, ok := table.GetView("ord-102")
	if !ok {
		t.Fatal("expected to find ord-102")
	}

	sym, err := view.String("symbol")
	if err != nil || sym != "MSFT" {
		t.Fatalf("symbol = %q, want MSFT", sym)
	}

	price, err := view.Float64("price")
	if err != nil || price != 420.50 {
		t.Fatalf("price = %v, want 420.50", price)
	}

	vol, err := view.Int64("volume")
	if err != nil || vol != 300 {
		t.Fatalf("volume = %v, want 300", vol)
	}

	// Verify SealColumnar and GetColumnarView
	query := Query{OrganizationID: "org-1"}
	sealed, err := store.SealColumnar(ctx, "orders", query, []string{"record_id", "symbol", "price", "volume"}, "record_id")
	if err != nil {
		t.Fatalf("SealColumnar failed: %v", err)
	}
	if sealed.Rows() != 2 {
		t.Fatalf("sealed rows = %d, want 2", sealed.Rows())
	}

	stats, err := store.Stats("orders")
	if err != nil {
		t.Fatalf("store.Stats failed: %v", err)
	}
	if stats.ColumnarRows != 2 || stats.ColumnarBytes <= 0 {
		t.Fatalf("expected columnar stats, got rows=%d bytes=%d", stats.ColumnarRows, stats.ColumnarBytes)
	}

	// Zero-alloc direct point view lookup from store
	cv, found, err := store.GetColumnarView(ctx, "orders", query, "ord-101")
	if err != nil {
		t.Fatalf("GetColumnarView failed: %v", err)
	}
	if !found {
		t.Fatal("expected to find ord-101 via GetColumnarView")
	}
	sym101, _ := cv.String("symbol")
	if sym101 != "AAPL" {
		t.Fatalf("sym101 = %q, want AAPL", sym101)
	}
}

// BenchmarkColumnarTable_GetPointRead100K measures point read latency over rotating keys
// uniformly spread across a 100,000 row table to defeat per-key CPU cache locality.
func BenchmarkColumnarTable_GetPointRead100K(b *testing.B) {
	const count = 100_000
	table, err := buildTestColumnarTable(count)
	if err != nil {
		b.Fatalf("build table failed: %v", err)
	}

	keys := []string{
		"ord_00000001",
		"ord_00025000",
		"ord_00050000",
		"ord_00075000",
		"ord_00099999",
	}

	b.ResetTimer()
	b.ReportAllocs()

	var sink float64
	for b.Loop() {
		for _, key := range keys {
			view, ok := table.GetView(key)
			if ok {
				p, _ := view.Float64("price")
				sink += p
			}
		}
	}
	_ = sink
}

// BenchmarkColumnarTable_GetPointRead1M measures point read latency over rotating keys
// uniformly spread across a 1,000,000 row table to defeat per-key CPU cache locality.
func BenchmarkColumnarTable_GetPointRead1M(b *testing.B) {
	const count = 1_000_000
	table, err := buildTestColumnarTable(count)
	if err != nil {
		b.Fatalf("build table failed: %v", err)
	}

	keys := []string{
		"ord_00000001",
		"ord_00250000",
		"ord_00500000",
		"ord_00750000",
		"ord_00999999",
	}

	b.ResetTimer()
	b.ReportAllocs()

	var sink float64
	for b.Loop() {
		for _, key := range keys {
			view, ok := table.GetView(key)
			if ok {
				p, _ := view.Float64("price")
				sink += p
			}
		}
	}
	_ = sink
}

func columnarTenantRecord(org, id, symbol string) database.DomainRecord {
	return database.DomainRecord{
		Domain:         "trading",
		Collection:     "orders",
		OrganizationID: org,
		RecordID:       id,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Data: database.RecordDataFromPairs(
			database.RecordField{Name: "symbol", Value: database.StringValue(symbol)},
		),
	}
}

// TestStore_ColumnarCacheIsTenantScoped guards tenant isolation on the sealed table cache.
// A table sealed for one organization must never answer another organization.
func TestStore_ColumnarCacheIsTenantScoped(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(ProjectionSpec{
		Name: "orders", Domain: "trading", Collection: "orders", MaxRecords: 1000,
		ColumnarFields: []string{"record_id", "symbol"}, ColumnarPK: "record_id",
	})
	if err != nil {
		t.Fatalf("create store failed: %v", err)
	}
	if _, err := store.ApplyRecords(ctx, "orders", "test", 1, []database.DomainRecord{
		columnarTenantRecord("org-a", "a-1", "AAPL"),
		columnarTenantRecord("org-b", "b-1", "MSFT"),
	}); err != nil {
		t.Fatalf("apply records failed: %v", err)
	}

	// Seal for org-a first, so a shared cache would hand org-a's table to org-b.
	if _, found, err := store.GetColumnarView(ctx, "orders", Query{OrganizationID: "org-a"}, "a-1"); err != nil || !found {
		t.Fatalf("org-a own view: found=%v err=%v", found, err)
	}
	if _, found, err := store.GetColumnarView(ctx, "orders", Query{OrganizationID: "org-b"}, "a-1"); err != nil || found {
		t.Fatalf("org-b read org-a row: found=%v err=%v", found, err)
	}
	if _, found, err := store.GetColumnarView(ctx, "orders", Query{OrganizationID: "org-b"}, "b-1"); err != nil || !found {
		t.Fatalf("org-b own view: found=%v err=%v", found, err)
	}
	table, err := store.GetColumnarTable(ctx, "orders", Query{OrganizationID: "org-a"}, nil, "", Fence{})
	if err != nil || table.Rows() != 1 {
		t.Fatalf("org-a table: rows=%v err=%v", table, err)
	}

	// The guards run before a cache hit.
	if _, err := store.GetColumnarTable(ctx, "orders", Query{OrganizationID: "org-a"}, nil, "", Fence{MinEpoch: 1 << 40}); !errors.Is(err, ErrFenceNotSatisfied) {
		t.Fatalf("fence on cache hit: got %v, want ErrFenceNotSatisfied", err)
	}
	if _, _, err := store.GetColumnarView(ctx, "orders", Query{OrganizationID: "  "}, "a-1"); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("empty org on cache hit: got %v, want ErrInvalidEvent", err)
	}

	// A new epoch makes the cached table stale.
	if _, err := store.ApplyRecords(ctx, "orders", "test", 10, []database.DomainRecord{
		columnarTenantRecord("org-a", "a-2", "NVDA"),
	}); err != nil {
		t.Fatalf("apply records failed: %v", err)
	}
	if _, found, err := store.GetColumnarView(ctx, "orders", Query{OrganizationID: "org-a"}, "a-2"); err != nil || !found {
		t.Fatalf("view after epoch advance: found=%v err=%v", found, err)
	}

	allocs := testing.AllocsPerRun(100, func() {
		if _, _, err := store.GetColumnarView(ctx, "orders", Query{OrganizationID: "org-a"}, "a-1"); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("cached GetColumnarView allocated %.1f times, want 0", allocs)
	}
}

func TestStore_ColumnarCacheConcurrentApplyAndView(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(ProjectionSpec{
		Name: "orders", Domain: "trading", Collection: "orders", MaxRecords: 10000,
		ColumnarFields: []string{"record_id", "symbol"}, ColumnarPK: "record_id",
	})
	if err != nil {
		t.Fatalf("create store failed: %v", err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 200 {
			id := "a-" + strconv.Itoa(i)
			if _, err := store.ApplyRecords(ctx, "orders", "test", uint64(i+1), []database.DomainRecord{
				columnarTenantRecord("org-a", id, "AAPL"),
			}); err != nil {
				t.Errorf("apply %d: %v", i, err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range 200 {
			if _, _, err := store.GetColumnarView(ctx, "orders", Query{OrganizationID: "org-b"}, "a-0"); err != nil {
				t.Errorf("view: %v", err)
				return
			}
			if _, found, _ := store.GetColumnarView(ctx, "orders", Query{OrganizationID: "org-b"}, "a-0"); found {
				t.Errorf("org-b saw org-a row during concurrent apply")
				return
			}
		}
	}()
	wg.Wait()
	_, found, err := store.GetColumnarView(ctx, "orders", Query{OrganizationID: "org-a"}, "a-199")
	if err != nil || !found {
		t.Fatalf("final view must see last write: found=%v err=%v", found, err)
	}
}
