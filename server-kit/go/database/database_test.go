package database

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testData(t testing.TB, fields ...any) RecordData {
	t.Helper()
	if len(fields)%2 != 0 {
		t.Fatalf("testData requires name/value pairs")
	}
	out := make(RecordData, 0, len(fields)/2)
	for i := 0; i < len(fields); i += 2 {
		name, ok := fields[i].(string)
		if !ok {
			t.Fatalf("field name %d is %T", i, fields[i])
		}
		value, ok := RecordValueFromAny(fields[i+1])
		if !ok {
			t.Fatalf("field value %q is unsupported", name)
		}
		out = append(out, RecordField{Name: name, Value: value})
	}
	return out.Normalize()
}

func testQuery(t testing.TB, limit int, fields ...any) RecordQuery {
	t.Helper()
	return RecordQuery{Limit: limit, Filters: testFilters(t, fields...)}.Normalize()
}

func testFilters(t testing.TB, fields ...any) []RecordFilter {
	t.Helper()
	data := testData(t, fields...)
	out := make([]RecordFilter, 0, len(data))
	for _, field := range data {
		out = append(out, RecordFilter{Field: field.Name, Value: field.Value})
	}
	return out
}

func requireStringField(t testing.TB, data RecordData, name, want string) {
	t.Helper()
	value, ok := data.Get(name)
	if !ok || value.Kind != RecordValueString || value.Text != want {
		t.Fatalf("field %q = %+v ok=%v, want %q", name, value, ok, want)
	}
}

func TestRecordDataMergePreservesAndOverridesSortedFields(t *testing.T) {
	base := testData(t, "status", "active", "bucket", 1, "title", "original")
	patch := testData(t, "status", "archived", "revision", 2)
	merged := base.Merge(patch)

	requireStringField(t, merged, "title", "original")
	requireStringField(t, merged, "status", "archived")
	value, ok := merged.Get("bucket")
	if !ok || !value.Equal(IntValue(1)) {
		t.Fatalf("bucket = %+v ok=%v, want 1", value, ok)
	}
	value, ok = merged.Get("revision")
	if !ok || !value.Equal(IntValue(2)) {
		t.Fatalf("revision = %+v ok=%v, want 2", value, ok)
	}
	for i := 1; i < len(merged); i++ {
		if merged[i-1].Name >= merged[i].Name {
			t.Fatalf("merged data is not sorted unique: %+v", merged)
		}
	}
}

func TestMemoryDBUpsertGetListCount(t *testing.T) {
	db := NewMemoryDB()

	_, err := db.UpsertRecord(context.Background(), DomainRecord{
		Domain:         "workspace",
		Collection:     "brand_kits",
		OrganizationID: "org_1",
		RecordID:       "brand_1",
		Data:           testData(t, "brand_kit_id", "brand_1", "workspace_id", "ws_1", "locale_code", "en-US"),
	})
	if err != nil {
		t.Fatalf("upsert failed: %v", err)
	}

	rec, ok, err := db.GetRecord(context.Background(), "workspace", "brand_kits", "org_1", "brand_1")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if !ok {
		t.Fatalf("expected record to be retrievable")
	}
	requireStringField(t, rec.Data, "brand_kit_id", "brand_1")

	items, err := db.ListRecords(context.Background(), "workspace", "brand_kits", "org_1", testQuery(t, 10, "workspace_id", "ws_1"))
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one listed record")
	}

	count, err := db.CountRecords(context.Background(), "workspace", "brand_kits", "org_1", testQuery(t, 0, "locale_code", "en-US"))
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected count=1")
	}
}

func TestMemoryDBRawJSONStateStore(t *testing.T) {
	db := NewMemoryDB()
	raw, err := db.UpsertRecordJSON(context.Background(), RawDomainRecord{
		Domain:         "workspace",
		Collection:     "brand_kits",
		OrganizationID: "org_1",
		RecordID:       "brand_raw",
		DataJSON:       []byte(`{"brand_kit_id":"brand_raw"}`),
	})
	if err != nil {
		t.Fatalf("UpsertRecordJSON() error = %v", err)
	}
	if !bytes.Equal(raw.DataJSON, []byte(`{"brand_kit_id":"brand_raw"}`)) {
		t.Fatalf("UpsertRecordJSON() data = %s", string(raw.DataJSON))
	}
	got, ok, err := db.GetRecordJSON(context.Background(), "workspace", "brand_kits", "org_1", "brand_raw")
	if err != nil || !ok {
		t.Fatalf("GetRecordJSON() ok=%v err=%v", ok, err)
	}
	if !bytes.Contains(got.DataJSON, []byte(`"organization_id"`)) {
		t.Fatalf("GetRecordJSON() did not stamp organization: %s", string(got.DataJSON))
	}
	typed, ok, err := db.GetRecord(context.Background(), "workspace", "brand_kits", "org_1", "brand_raw")
	if err != nil || !ok {
		t.Fatalf("GetRecord() after raw upsert = %+v ok=%v err=%v", typed, ok, err)
	}
	requireStringField(t, typed.Data, "organization_id", "org_1")
}

func TestMemoryDBContextCancel(t *testing.T) {
	db := NewMemoryDB()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := db.UpsertRecord(ctx, DomainRecord{
		Domain:         "identity",
		Collection:     "users",
		OrganizationID: "org_1",
		RecordID:       "usr_1",
		Data:           testData(t, "user_id", "usr_1"),
	}); err == nil {
		t.Fatalf("expected context canceled error")
	}
}

func TestMemoryDBQueryDeleteAtomicAndClosedPaths(t *testing.T) {
	db := NewMemoryDB()
	ctx := context.Background()
	if err := db.Exec(ctx, "select 1"); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	rows, err := db.Query(ctx, "select 1")
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	rows.Close()
	if err := db.QueryRow(ctx, "select 1").Scan(); err == nil {
		t.Fatal("expected memory QueryRow scan to fail")
	}
	for _, rec := range []DomainRecord{
		{Domain: "media", Collection: "assets", OrganizationID: "org1", RecordID: "a", Data: testData(t, "kind", "image")},
		{Domain: "media", Collection: "assets", OrganizationID: "org1", RecordID: "b", Data: testData(t, "kind", "video")},
		{Domain: "media", Collection: "assets", OrganizationID: "org2", RecordID: "c", Data: testData(t, "kind", "image")},
	} {
		if _, err := db.UpsertRecord(ctx, rec); err != nil {
			t.Fatalf("UpsertRecord() error = %v", err)
		}
	}
	items, err := db.ListRecords(ctx, "media", "assets", "org1", testQuery(t, 10, "kind", "image"))
	if err != nil || len(items) != 1 || items[0].RecordID != "a" {
		t.Fatalf("ListRecords() = %+v err=%v", items, err)
	}
	count, err := db.EstimateCount(ctx, "media", "assets", "org1")
	if err != nil || count != 2 {
		t.Fatalf("EstimateCount() = %d err=%v", count, err)
	}
	if err := db.DeleteRecord(ctx, "media", "assets", "org1", "a"); err != nil {
		t.Fatalf("DeleteRecord() error = %v", err)
	}
	if _, ok, err := db.GetRecord(ctx, "media", "assets", "org1", "a"); err != nil || ok {
		t.Fatalf("deleted GetRecord ok=%v err=%v", ok, err)
	}
	removed, err := db.DeleteRecordsByOrganization(ctx, "org1")
	if err != nil || removed != 1 {
		t.Fatalf("DeleteRecordsByOrganization() = %d err=%v", removed, err)
	}
	removed, err = db.DeleteRecordsByOrganization(ctx, " ")
	if err != nil || removed != 0 {
		t.Fatalf("empty org delete = %d err=%v", removed, err)
	}
	if err := Atomic(ctx, db, func(DBTX) error { return nil }); err != nil {
		t.Fatalf("Atomic() without tx beginner error = %v", err)
	}
	if err := Atomic(ctx, db, nil); err == nil {
		t.Fatal("expected nil atomic function to fail")
	}
	db.Close()
	if err := db.Exec(ctx, ""); err == nil {
		t.Fatal("expected closed db exec to fail")
	}
	if _, err := db.Query(ctx, ""); err == nil {
		t.Fatal("expected closed db query to fail")
	}
	if err := db.QueryRow(ctx, "").Scan(); err == nil {
		t.Fatal("expected closed db query row to fail")
	}
}

func TestMemoryDBConcurrentTenantIsolationUnderPressure(t *testing.T) {
	db := NewMemoryDB()
	ctx := context.Background()
	const tenants = 8
	const perTenant = 64

	var wg sync.WaitGroup
	wg.Add(tenants)
	for tenant := range tenants {
		go func(tenant int) {
			defer wg.Done()
			orgID := fmt.Sprintf("org_%02d", tenant)
			for i := range perTenant {
				_, err := db.UpsertRecord(ctx, DomainRecord{
					Domain:         "signals",
					Collection:     "ticks",
					OrganizationID: orgID,
					RecordID:       fmt.Sprintf("tick_%03d", i),
					Data:           testData(t, "tenant", orgID, "bucket", i%4),
				})
				if err != nil {
					t.Errorf("upsert %s/%d: %v", orgID, i, err)
				}
			}
		}(tenant)
	}
	wg.Wait()

	for tenant := range tenants {
		orgID := fmt.Sprintf("org_%02d", tenant)
		count, err := db.CountRecords(ctx, "signals", "ticks", orgID, RecordQuery{})
		if err != nil {
			t.Fatalf("CountRecords %s: %v", orgID, err)
		}
		if count != perTenant {
			t.Fatalf("CountRecords %s = %d, want %d", orgID, count, perTenant)
		}
		items, err := db.ListRecords(ctx, "signals", "ticks", orgID, testQuery(t, perTenant, "bucket", 2))
		if err != nil {
			t.Fatalf("ListRecords %s: %v", orgID, err)
		}
		for _, item := range items {
			orgValue, ok := item.Data.Get("organization_id")
			if item.OrganizationID != orgID || !ok || orgValue.Text != orgID {
				t.Fatalf("tenant isolation breach for %s: %+v", orgID, item)
			}
		}
	}
}

func TestDatabaseHelpers(t *testing.T) {
	if normalizeDriver(" POSTGRES ") != DriverPostgres || normalizeDriver("bad") != DriverMemory {
		t.Fatal("normalizeDriver failed")
	}
	if clampInt(1, 2, 4) != 2 || clampInt(5, 2, 4) != 4 || clampInt(3, 2, 4) != 3 {
		t.Fatal("clampInt failed")
	}
	opts := normalizePoolOptions(PoolOptions{MaxConns: 2, MinConns: 10})
	if opts.MinConns != opts.MaxConns || opts.QueryTimeout <= 0 || opts.AcquireTimeout <= 0 {
		t.Fatalf("normalizePoolOptions = %+v", opts)
	}
	for _, lane := range []RuntimeLane{RuntimeLaneDefault, RuntimeLaneHotRead, RuntimeLaneHotWrite, RuntimeLaneBackground, RuntimeLaneAnalytics} {
		if got := DefaultPoolOptionsFor(lane); got.MaxConns <= 0 || got.QueryTimeout <= 0 {
			t.Fatalf("DefaultPoolOptionsFor(%s) = %+v", lane, got)
		}
	}
	ctx, cancel := QueryBudgetContext(context.TODO(), PoolOptions{QueryTimeout: time.Millisecond})
	defer cancel()
	if ctx == nil {
		t.Fatal("expected query budget context")
	}
	store, err := Connect(context.Background(), "", "memory")
	if err != nil || store == nil {
		t.Fatalf("Connect(memory) = %v err=%v", store, err)
	}
	if !testData(t, "a", " 1 ").Matches(testFilters(t, "a", " 1 ")) || testData(t).Matches(testFilters(t, "missing", 1)) {
		t.Fatal("RecordData.Matches failed")
	}
}

func TestMemoryDBValidationAndCopySemantics(t *testing.T) {
	db := NewMemoryDB()
	ctx := context.Background()
	for _, rec := range []DomainRecord{
		{Collection: "c", RecordID: "r"},
		{Domain: "d", RecordID: "r"},
		{Domain: "d", Collection: "c"},
	} {
		if _, err := db.UpsertRecord(ctx, rec); err == nil {
			t.Fatalf("expected validation error for %+v", rec)
		}
	}
	input := testData(t, "nested", map[string]any{"unchanged": true})
	rec, err := db.UpsertRecord(ctx, DomainRecord{
		Domain:         " domain ",
		Collection:     " collection ",
		OrganizationID: " org ",
		RecordID:       " rec ",
		Data:           input,
	})
	if err != nil {
		t.Fatalf("UpsertRecord() error = %v", err)
	}
	if rec.Domain != "domain" || rec.Collection != "collection" || rec.OrganizationID != "org" || rec.RecordID != "rec" {
		t.Fatalf("record fields not normalized: %+v", rec)
	}
	if _, ok := rec.Data.Get("mutated"); ok {
		t.Fatalf("record data copy/default mismatch: %+v", rec.Data)
	}
	requireStringField(t, rec.Data, "organization_id", "org")
	got, ok, err := db.GetRecord(ctx, " domain ", " collection ", " org ", " rec ")
	if err != nil || !ok {
		t.Fatalf("GetRecord() ok=%v err=%v", ok, err)
	}
	got.Data = got.Data.With("changed", BoolValue(true))
	gotAgain, _, _ := db.GetRecord(ctx, "domain", "collection", "org", "rec")
	if _, ok := gotAgain.Data.Get("changed"); ok {
		t.Fatalf("GetRecord should return a copy")
	}
}

func TestMemoryDBListOrderingLimitAndCancellation(t *testing.T) {
	db := NewMemoryDB()
	ctx := context.Background()
	for _, id := range []string{"b", "a", "c"} {
		if _, err := db.UpsertRecord(ctx, DomainRecord{Domain: "d", Collection: "c", OrganizationID: "o", RecordID: id, Data: testData(t, "kind", "same")}); err != nil {
			t.Fatalf("UpsertRecord() error = %v", err)
		}
	}
	items, err := db.ListRecords(ctx, "d", "c", "o", RecordQuery{Limit: 2})
	if err != nil || len(items) != 2 {
		t.Fatalf("ListRecords limit = %+v err=%v", items, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.ListRecords(cancelled, "d", "c", "o", RecordQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ListRecords error = %v", err)
	}
	if _, _, err := db.GetRecord(cancelled, "d", "c", "o", "a"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled GetRecord error = %v", err)
	}
	if _, err := db.CountRecords(cancelled, "d", "c", "o", RecordQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled CountRecords error = %v", err)
	}
	if err := db.DeleteRecord(cancelled, "d", "c", "o", "a"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled DeleteRecord error = %v", err)
	}
	if _, err := db.DeleteRecordsByOrganization(cancelled, "o"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled DeleteRecordsByOrganization error = %v", err)
	}
}

func TestAtomicWithBeginnerCommitRollback(t *testing.T) {
	tx := &fakeTx{}
	db := &fakeBeginner{tx: tx}
	if err := Atomic(context.Background(), db, func(DBTX) error { return nil }); err != nil {
		t.Fatalf("Atomic commit path error = %v", err)
	}
	if !tx.committed || tx.rolledBack {
		t.Fatalf("commit path state: %+v", tx)
	}
	tx = &fakeTx{}
	db.tx = tx
	errBoom := errors.New("boom")
	if err := Atomic(context.Background(), db, func(DBTX) error { return errBoom }); !errors.Is(err, errBoom) {
		t.Fatalf("Atomic rollback error = %v", err)
	}
	if !tx.rolledBack || tx.committed {
		t.Fatalf("rollback path state: %+v", tx)
	}
	db.beginErr = errors.New("begin")
	if err := Atomic(context.Background(), db, func(DBTX) error { return nil }); !errors.Is(err, db.beginErr) {
		t.Fatalf("begin error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Atomic(cancelled, db, func(DBTX) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Atomic error = %v", err)
	}
}

func benchmarkMemoryDBWithTenants(b *testing.B, tenants, perTenant int) *MemoryDB {
	b.Helper()
	db := NewMemoryDB()
	ctx := context.Background()
	for tenant := range tenants {
		orgID := fmt.Sprintf("org_%02d", tenant)
		for i := range perTenant {
			if _, err := db.UpsertRecord(ctx, DomainRecord{
				Domain:         "signals",
				Collection:     "ticks",
				OrganizationID: orgID,
				RecordID:       fmt.Sprintf("tick_%05d", i),
				Data:           testData(b, "bucket", i%8, "kind", "tick"),
			}); err != nil {
				b.Fatalf("UpsertRecord() error = %v", err)
			}
		}
	}
	return db
}

func BenchmarkMemoryDBCountRecordsTenantScoped(b *testing.B) {
	db := benchmarkMemoryDBWithTenants(b, 32, 256)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		count, err := db.CountRecords(ctx, "signals", "ticks", "org_07", RecordQuery{})
		if err != nil {
			b.Fatal(err)
		}
		if count != 256 {
			b.Fatalf("count = %d, want 256", count)
		}
	}
}

func BenchmarkMemoryDBListRecordsTenantScopedFiltered(b *testing.B) {
	db := benchmarkMemoryDBWithTenants(b, 32, 256)
	ctx := context.Background()
	filter := testQuery(b, 64, "bucket", 3)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		items, err := db.ListRecords(ctx, "signals", "ticks", "org_07", filter)
		if err != nil {
			b.Fatal(err)
		}
		if len(items) != 32 {
			b.Fatalf("items = %d, want 32", len(items))
		}
	}
}

func BenchmarkMemoryDBUpsertTenantScopedParallel(b *testing.B) {
	db := NewMemoryDB()
	var index atomic.Int64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			n := index.Add(1)
			orgID := fmt.Sprintf("org_%02d", n%32)
			if _, err := db.UpsertRecord(context.Background(), DomainRecord{
				Domain:         "signals",
				Collection:     "ticks",
				OrganizationID: orgID,
				RecordID:       fmt.Sprintf("tick_%09d", n),
				Data:           testData(b, "kind", "tick"),
			}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

type fakeBeginner struct {
	tx       Tx
	beginErr error
}

func (f *fakeBeginner) Exec(context.Context, string, ...any) error { return nil }
func (f *fakeBeginner) QueryRow(context.Context, string, ...any) RowScanner {
	return memoryRow{}
}
func (f *fakeBeginner) Query(context.Context, string, ...any) (Rows, error) {
	return memoryRows{}, nil
}
func (f *fakeBeginner) BeginTx(context.Context) (Tx, error) {
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	return f.tx, nil
}

type fakeTx struct {
	committed  bool
	rolledBack bool
}

func (f *fakeTx) Exec(context.Context, string, ...any) error { return nil }
func (f *fakeTx) QueryRow(context.Context, string, ...any) RowScanner {
	return memoryRow{}
}
func (f *fakeTx) Query(context.Context, string, ...any) (Rows, error) {
	return memoryRows{}, nil
}
func (f *fakeTx) Commit(context.Context) error {
	f.committed = true
	return nil
}
func (f *fakeTx) Rollback(context.Context) error {
	f.rolledBack = true
	return nil
}

func TestPostgresNilAndHelperPaths(t *testing.T) {
	var db *PostgresDB
	if db.Pool() != nil {
		t.Fatalf("nil Pool should be nil")
	}
	db.Close()
	if _, err := db.BeginTx(context.Background()); err == nil {
		t.Fatalf("expected nil BeginTx error")
	}
	if err := db.Exec(context.Background(), "select 1"); err == nil {
		t.Fatalf("expected nil Exec error")
	}
	if _, err := db.ExecResult(context.Background(), "select 1"); err == nil {
		t.Fatalf("expected nil ExecResult error")
	}
	if err := db.QueryRow(context.Background(), "select 1").Scan(); err == nil {
		t.Fatalf("expected nil QueryRow scan error")
	}
	if _, err := db.Query(context.Background(), "select 1"); err == nil {
		t.Fatalf("expected nil Query error")
	}
	if db.Stats().TotalConns != 0 {
		t.Fatalf("nil Stats should be zero")
	}
	if _, err := db.UpsertRecord(context.Background(), DomainRecord{}); err == nil {
		t.Fatalf("expected nil UpsertRecord error")
	}
	if _, _, err := db.GetRecord(context.Background(), "d", "c", "o", "r"); err == nil {
		t.Fatalf("expected nil GetRecord error")
	}
	if _, err := db.ListRecords(context.Background(), "d", "c", "o", RecordQuery{Limit: 1}); err == nil {
		t.Fatalf("expected nil ListRecords error")
	}
	if _, err := db.CountRecords(context.Background(), "d", "c", "o", RecordQuery{}); err == nil {
		t.Fatalf("expected nil CountRecords error")
	}
	if _, err := db.EstimateCount(context.Background(), "d", "c", "o"); err == nil {
		t.Fatalf("expected nil EstimateCount error")
	}
	if err := db.DeleteRecord(context.Background(), "d", "c", "o", "r"); err == nil {
		t.Fatalf("expected nil DeleteRecord error")
	}
	if _, err := db.DeleteRecordsByOrganization(context.Background(), "o"); err == nil {
		t.Fatalf("expected nil DeleteRecordsByOrganization error")
	}
	if clampInt32(-1) != 0 || clampInt32(maxInt32Value+1) != maxInt32Value || clampInt32(3) != 3 {
		t.Fatalf("clampInt32 failed")
	}
	if _, err := newPostgresDB(context.Background(), "", PoolOptions{}); err == nil {
		t.Fatalf("expected empty postgres URL error")
	}
	wrapped := WrapPostgresPool(nil, PoolOptions{StatementCacheCapacity: 16})
	if wrapped == nil || wrapped.Pool() != nil {
		t.Fatalf("wrapped nil pool should preserve a nil raw pool")
	}
	if wrapped.opts.StatementCacheCapacity != 16 {
		t.Fatalf("wrapped statement cache capacity = %d, want 16", wrapped.opts.StatementCacheCapacity)
	}

	var tx *postgresTx
	if err := tx.Exec(context.Background(), "select 1"); err == nil {
		t.Fatalf("expected nil tx Exec error")
	}
	if err := tx.QueryRow(context.Background(), "select 1").Scan(); err == nil {
		t.Fatalf("expected nil tx QueryRow error")
	}
	if _, err := tx.Query(context.Background(), "select 1"); err == nil {
		t.Fatalf("expected nil tx Query error")
	}
	if err := tx.Commit(context.Background()); err == nil {
		t.Fatalf("expected nil tx Commit error")
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatalf("nil tx Rollback should no-op: %v", err)
	}
}

func TestPostgresRecordValidationAndJSONParsing(t *testing.T) {
	if err := validateDomainRecord(nil); err == nil {
		t.Fatalf("expected nil record validation error")
	}
	rec := DomainRecord{Domain: " d ", Collection: " c ", OrganizationID: " o ", RecordID: " r "}
	if err := validateDomainRecord(&rec); err != nil {
		t.Fatalf("validateDomainRecord() error = %v", err)
	}
	orgValue, ok := rec.Data.Get("organization_id")
	if rec.Domain != "d" || rec.Collection != "c" || rec.OrganizationID != "o" || rec.RecordID != "r" || !ok || orgValue.Text != "o" {
		t.Fatalf("record normalization failed: %+v", rec)
	}
	for _, rec := range []DomainRecord{
		{Collection: "c", RecordID: "r"},
		{Domain: "d", RecordID: "r"},
		{Domain: "d", Collection: "c"},
	} {
		if err := validateDomainRecord(&rec); err == nil {
			t.Fatalf("expected validation error for %+v", rec)
		}
	}
	parsed, err := parseDataJSON([]byte(`{"a":1}`))
	aValue, ok := parsed.Get("a")
	if err != nil || !ok || aValue.Text != "1" {
		t.Fatalf("parseDataJSON object = %+v err=%v", parsed, err)
	}
	if parsed, err := parseDataJSON(nil); err != nil || len(parsed) != 0 {
		t.Fatalf("parseDataJSON nil = %+v err=%v", parsed, err)
	}
	if _, err := parseDataJSON([]byte(`bad`)); err == nil {
		t.Fatalf("expected invalid JSON error")
	}

	raw := RawDomainRecord{Domain: " d ", Collection: " c ", OrganizationID: " o ", RecordID: " r ", DataJSON: []byte(`{"state":"ready"}`)}
	payload, err := validateRawDomainRecord(&raw)
	if err != nil {
		t.Fatalf("validateRawDomainRecord() error = %v", err)
	}
	if raw.Domain != "d" || raw.Collection != "c" || raw.OrganizationID != "o" || raw.RecordID != "r" {
		t.Fatalf("raw record identity normalization failed: %+v", raw)
	}
	parsed, err = parseDataJSON(payload)
	if err != nil {
		t.Fatalf("raw payload parse error = %v", err)
	}
	if _, ok := parsed.Get("organization_id"); ok {
		t.Fatalf("raw payload unexpectedly stamped organization: %s parsed=%+v", string(payload), parsed)
	}
	stateValue, ok := parsed.Get("state")
	if !ok || stateValue.Text != "ready" {
		t.Fatalf("raw payload = %s parsed=%+v err=%v", string(payload), parsed, err)
	}
	if _, err := normalizeDataJSON([]byte(`[1]`)); err == nil {
		t.Fatalf("expected non-object raw JSON error")
	}
	if _, err := normalizeDataJSON([]byte(`bad`)); err == nil {
		t.Fatalf("expected invalid raw JSON error")
	}
}

func TestPostgresRecordWhereMatchesStateStoreShape(t *testing.T) {
	where, args, pushed := buildPostgresRecordWhere("d", "c", "o", testFilters(t, "active", true, "owner'id", " user_1 ", "shard", 7), 1)
	if !pushed {
		t.Fatalf("expected scalar filters to push down")
	}
	expectedWhere := "domain = $1 AND collection_name = $2 AND organization_id = $3 AND btrim(data ->> 'active') = $4 AND btrim(data ->> 'owner''id') = $5 AND btrim(data ->> 'shard') = $6"
	if where != expectedWhere {
		t.Fatalf("where = %q, want %q", where, expectedWhere)
	}
	expectedArgs := []any{"d", "c", "o", "true", "user_1", "7"}
	if len(args) != len(expectedArgs) {
		t.Fatalf("args = %#v, want %#v", args, expectedArgs)
	}
	for i := range args {
		if args[i] != expectedArgs[i] {
			t.Fatalf("args[%d] = %#v, want %#v", i, args[i], expectedArgs[i])
		}
	}
}

func TestPostgresRecordWhereLeavesNestedFiltersForStoreRecheck(t *testing.T) {
	where, args, pushed := buildPostgresRecordWhere("", "", "", testFilters(t, "nested", map[string]any{"k": "v"}), 3)
	if pushed {
		t.Fatalf("expected nested filter to remain app-side")
	}
	if where != "TRUE" || len(args) != 0 {
		t.Fatalf("where=%q args=%#v, want TRUE and no args", where, args)
	}
}

func TestPostgresRecheckBudgetOptions(t *testing.T) {
	if normalizedRecheckRowBudget(0) != defaultPostgresRecheckRowBudget {
		t.Fatalf("default recheck budget = %d", normalizedRecheckRowBudget(0))
	}
	if normalizedRecheckRowBudget(maxPostgresRecheckRowBudget+1) != maxPostgresRecheckRowBudget {
		t.Fatalf("max recheck budget = %d", normalizedRecheckRowBudget(maxPostgresRecheckRowBudget+1))
	}
	var db *PostgresDB
	nestedQuery := testQuery(t, 0, "nested", map[string]any{"k": "v"})
	if _, err := db.ListRecordsWithOptions(context.Background(), "d", "c", "o", nestedQuery, StateListOptions{RequirePushdown: true}); err == nil {
		t.Fatal("expected nil postgres list to fail before pushdown option")
	}
	db = &PostgresDB{}
	if _, err := db.ListRecordsWithOptions(context.Background(), "d", "c", "o", nestedQuery, StateListOptions{RequirePushdown: true}); !errors.Is(err, ErrUnsupportedFilterShape) {
		t.Fatalf("require-pushdown list error = %v", err)
	}
	if _, err := db.CountRecordsWithOptions(context.Background(), "d", "c", "o", nestedQuery, StateCountOptions{RequirePushdown: true}); !errors.Is(err, ErrUnsupportedFilterShape) {
		t.Fatalf("require-pushdown count error = %v", err)
	}
}

func TestPostgresUpsertSQLAvoidsNoopJSONRewrites(t *testing.T) {
	for name, query := range map[string]string{
		"typed": upsertRecordSQL,
		"raw":   upsertRecordJSONSQL,
	} {
		if !strings.Contains(query, "IS DISTINCT FROM EXCLUDED.data") {
			t.Fatalf("%s upsert query should skip no-op JSON rewrites: %s", name, query)
		}
		if !strings.Contains(query, "UNION ALL") || !strings.Contains(query, "NOT EXISTS (SELECT 1 FROM upsert)") {
			t.Fatalf("%s upsert query should return existing timestamps on no-op conflict: %s", name, query)
		}
	}
}

func TestNormalizePostgresOperationError(t *testing.T) {
	lockErr := normalizePostgresOperationError(nil, &pgconn.PgError{
		Code:    postgresSQLStateLockNotAvailable,
		Message: "canceling statement due to lock timeout",
	})
	if !errors.Is(lockErr, ErrLockTimeout) {
		t.Fatalf("lock error = %v", lockErr)
	}

	queryErr := normalizePostgresOperationError(nil, &pgconn.PgError{
		Code:    postgresSQLStateQueryCanceled,
		Message: "canceling statement due to statement timeout",
	})
	if !errors.Is(queryErr, ErrQueryTimeout) {
		t.Fatalf("query error = %v", queryErr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := normalizePostgresOperationError(ctx.Err(), context.Canceled); errors.Is(err, ErrQueryTimeout) {
		t.Fatalf("canceled context should not normalize as query timeout: %v", err)
	}
}

func TestParseExplainPlanRows(t *testing.T) {
	count, err := parseExplainPlanRows([]byte(`[{"Plan":{"Node Type":"Index Scan","Plan Rows":42}}]`))
	if err != nil || count != 42 {
		t.Fatalf("parseExplainPlanRows() = %d err=%v", count, err)
	}
	count, err = parseExplainPlanRows(nil)
	if err != nil || count != 0 {
		t.Fatalf("parseExplainPlanRows(nil) = %d err=%v", count, err)
	}
	if _, err := parseExplainPlanRows([]byte(`bad`)); err == nil {
		t.Fatal("expected invalid explain JSON to fail")
	}
}
func TestConnectMemoryDriver(t *testing.T) {
	store, err := Connect(context.Background(), "", DriverMemory)
	if err != nil {
		t.Fatalf("connect memory driver failed: %v", err)
	}
	if store == nil {
		t.Fatalf("expected runtime store")
	}
	store.Close()
}

func TestConnectUnknownDriverDefaultsToMemory(t *testing.T) {
	store, err := Connect(context.Background(), "", "unknown")
	if err != nil {
		t.Fatalf("connect unknown driver failed: %v", err)
	}
	if store == nil {
		t.Fatalf("expected runtime store")
	}
	store.Close()
}

func TestConnectPostgresRequiresURL(t *testing.T) {
	_, err := Connect(context.Background(), "", DriverPostgres)
	if err == nil {
		t.Fatalf("expected error for postgres driver without url")
	}
}
func TestNormalizePoolOptionsDefaults(t *testing.T) {
	opts := normalizePoolOptions(PoolOptions{})
	if opts.MaxConns <= 0 {
		t.Fatalf("expected default max conns")
	}
	if opts.MinConns < 0 {
		t.Fatalf("expected non-negative min conns")
	}
	if opts.ConnectTimeout <= 0 {
		t.Fatalf("expected default connect timeout")
	}
	if opts.LockTimeout <= 0 || opts.LockTimeout > opts.QueryTimeout {
		t.Fatalf("expected bounded lock timeout, got lock=%s query=%s", opts.LockTimeout, opts.QueryTimeout)
	}
	if opts.IdleTxTimeout <= 0 {
		t.Fatalf("expected idle transaction timeout")
	}
	if opts.StatementCacheCapacity <= 0 {
		t.Fatalf("expected statement cache capacity")
	}
	if opts.DescriptionCacheCapacity < 0 {
		t.Fatalf("expected non-negative description cache capacity")
	}
}

func TestNormalizePoolOptionsClampMinToMax(t *testing.T) {
	opts := normalizePoolOptions(PoolOptions{
		MaxConns:     4,
		MinConns:     10,
		QueryTimeout: 100 * time.Millisecond,
		LockTimeout:  500 * time.Millisecond,
	})
	if opts.MinConns != opts.MaxConns {
		t.Fatalf("expected min conns to clamp to max conns")
	}
	if opts.LockTimeout != opts.QueryTimeout {
		t.Fatalf("expected lock timeout to clamp to query timeout, got lock=%s query=%s", opts.LockTimeout, opts.QueryTimeout)
	}
}

func TestDefaultPoolOptionsForLanes(t *testing.T) {
	hotRead := DefaultPoolOptionsFor(RuntimeLaneHotRead)
	background := DefaultPoolOptionsFor(RuntimeLaneBackground)
	analytics := DefaultPoolOptionsFor(RuntimeLaneAnalytics)

	if hotRead.QueryTimeout >= background.QueryTimeout {
		t.Fatalf("hot read query budget should be tighter than background: hot=%s background=%s", hotRead.QueryTimeout, background.QueryTimeout)
	}
	if analytics.MaxConns >= hotRead.MaxConns {
		t.Fatalf("analytics should use fewer DB connections than hot reads: analytics=%d hot=%d", analytics.MaxConns, hotRead.MaxConns)
	}
	if hotRead.AcquireTimeout <= 0 {
		t.Fatalf("expected acquire timeout")
	}
}

func TestQueryBudgetContextUsesDefaultTimeout(t *testing.T) {
	ctx, cancel := QueryBudgetContext(context.TODO(), PoolOptions{})
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatalf("expected query budget deadline")
	}
}

func TestApplyPoolOptionsConfiguresPgxPool(t *testing.T) {
	cfg, err := pgxpool.ParseConfig("postgres://user:pass@localhost:5432/db?sslmode=disable")
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	ApplyPoolOptions(cfg, PoolOptions{
		MaxConns:                 12,
		MinConns:                 3,
		HealthCheckPeriod:        9 * time.Second,
		ConnectTimeout:           4 * time.Second,
		QueryTimeout:             75 * time.Millisecond,
		LockTimeout:              50 * time.Millisecond,
		IdleTxTimeout:            11 * time.Second,
		StatementCacheCapacity:   128,
		DescriptionCacheCapacity: 32,
	})
	if cfg.MaxConns != 12 || cfg.MinConns != 3 || cfg.HealthCheckPeriod != 9*time.Second {
		t.Fatalf("pool sizing not applied: %+v", cfg)
	}
	if cfg.ConnConfig.ConnectTimeout != 4*time.Second || cfg.ConnConfig.StatementCacheCapacity != 128 {
		t.Fatalf("connection options not applied: %+v", cfg.ConnConfig)
	}
	if cfg.ConnConfig.DescriptionCacheCapacity != 32 || cfg.ConnConfig.DefaultQueryExecMode != pgx.QueryExecModeCacheStatement {
		t.Fatalf("cache options not applied: %+v", cfg.ConnConfig)
	}
	if got := cfg.ConnConfig.RuntimeParams["statement_timeout"]; got != "75" {
		t.Fatalf("statement_timeout = %q, want 75", got)
	}
	if got := cfg.ConnConfig.RuntimeParams["lock_timeout"]; got != "50" {
		t.Fatalf("lock_timeout = %q, want 50", got)
	}
	if got := cfg.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"]; got != "11000" {
		t.Fatalf("idle_in_transaction_session_timeout = %q, want 11000", got)
	}
	ApplyPoolOptions(nil, PoolOptions{})
}
