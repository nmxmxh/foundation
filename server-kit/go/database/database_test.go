package database

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMemoryDBUpsertGetListCount(t *testing.T) {
	db := NewMemoryDB()

	_, err := db.UpsertRecord(context.Background(), DomainRecord{
		Domain:         "workspace",
		Collection:     "brand_kits",
		OrganizationID: "org_1",
		RecordID:       "brand_1",
		Data: map[string]any{
			"brand_kit_id": "brand_1",
			"workspace_id": "ws_1",
			"locale_code":  "en-US",
		},
	})
	if err != nil {
		t.Fatalf("upsert failed: %v", err)
	}

	rec, ok, err := db.GetRecord(context.Background(), "workspace", "brand_kits", "org_1", "brand_1")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if !ok || rec.Data["brand_kit_id"] != "brand_1" {
		t.Fatalf("expected record to be retrievable")
	}

	items, err := db.ListRecords(context.Background(), "workspace", "brand_kits", "org_1", map[string]any{"workspace_id": "ws_1"}, 10)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one listed record")
	}

	count, err := db.CountRecords(context.Background(), "workspace", "brand_kits", "org_1", map[string]any{"locale_code": "en-US"})
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
	if err != nil || !ok || typed.Data["organization_id"] != "org_1" {
		t.Fatalf("GetRecord() after raw upsert = %+v ok=%v err=%v", typed, ok, err)
	}
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
		Data:           map[string]any{"user_id": "usr_1"},
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
	if _, err := db.QueryMaps(ctx, "select 1"); err != nil {
		t.Fatalf("QueryMaps() error = %v", err)
	}
	if err := db.QueryRow(ctx, "select 1").Scan(); err == nil {
		t.Fatal("expected memory QueryRow scan to fail")
	}
	for _, rec := range []DomainRecord{
		{Domain: "media", Collection: "assets", OrganizationID: "org1", RecordID: "a", Data: map[string]any{"kind": "image"}},
		{Domain: "media", Collection: "assets", OrganizationID: "org1", RecordID: "b", Data: map[string]any{"kind": "video"}},
		{Domain: "media", Collection: "assets", OrganizationID: "org2", RecordID: "c", Data: map[string]any{"kind": "image"}},
	} {
		if _, err := db.UpsertRecord(ctx, rec); err != nil {
			t.Fatalf("UpsertRecord() error = %v", err)
		}
	}
	items, err := db.ListRecords(ctx, "media", "assets", "org1", map[string]any{"kind": "image"}, 10)
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
	if _, err := db.QueryMaps(ctx, ""); err == nil {
		t.Fatal("expected closed db query maps to fail")
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
					Data: map[string]any{
						"tenant": orgID,
						"bucket": i % 4,
					},
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
		count, err := db.CountRecords(ctx, "signals", "ticks", orgID, nil)
		if err != nil {
			t.Fatalf("CountRecords %s: %v", orgID, err)
		}
		if count != perTenant {
			t.Fatalf("CountRecords %s = %d, want %d", orgID, count, perTenant)
		}
		items, err := db.ListRecords(ctx, "signals", "ticks", orgID, map[string]any{"bucket": 2}, perTenant)
		if err != nil {
			t.Fatalf("ListRecords %s: %v", orgID, err)
		}
		for _, item := range items {
			if item.OrganizationID != orgID || item.Data["organization_id"] != orgID {
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
	if !matchesFilter(map[string]any{"a": " 1 "}, map[string]any{"a": 1}) || matchesFilter(map[string]any{}, map[string]any{"missing": 1}) {
		t.Fatal("matchesFilter failed")
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
	input := map[string]any{"nested": map[string]any{"unchanged": true}}
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
	input["mutated"] = true
	if rec.Domain != "domain" || rec.Collection != "collection" || rec.OrganizationID != "org" || rec.RecordID != "rec" {
		t.Fatalf("record fields not normalized: %+v", rec)
	}
	if rec.Data["organization_id"] != "org" || rec.Data["mutated"] != nil {
		t.Fatalf("record data copy/default mismatch: %+v", rec.Data)
	}
	got, ok, err := db.GetRecord(ctx, " domain ", " collection ", " org ", " rec ")
	if err != nil || !ok {
		t.Fatalf("GetRecord() ok=%v err=%v", ok, err)
	}
	got.Data["changed"] = true
	gotAgain, _, _ := db.GetRecord(ctx, "domain", "collection", "org", "rec")
	if gotAgain.Data["changed"] != nil {
		t.Fatalf("GetRecord should return a copy")
	}
}

func TestMemoryDBListOrderingLimitAndCancellation(t *testing.T) {
	db := NewMemoryDB()
	ctx := context.Background()
	for _, id := range []string{"b", "a", "c"} {
		if _, err := db.UpsertRecord(ctx, DomainRecord{Domain: "d", Collection: "c", OrganizationID: "o", RecordID: id, Data: map[string]any{"kind": "same"}}); err != nil {
			t.Fatalf("UpsertRecord() error = %v", err)
		}
	}
	items, err := db.ListRecords(ctx, "d", "c", "o", nil, 2)
	if err != nil || len(items) != 2 {
		t.Fatalf("ListRecords limit = %+v err=%v", items, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.ListRecords(cancelled, "d", "c", "o", nil, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ListRecords error = %v", err)
	}
	if _, _, err := db.GetRecord(cancelled, "d", "c", "o", "a"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled GetRecord error = %v", err)
	}
	if _, err := db.CountRecords(cancelled, "d", "c", "o", nil); !errors.Is(err, context.Canceled) {
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
				Data: map[string]any{
					"bucket": i % 8,
					"kind":   "tick",
				},
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
		count, err := db.CountRecords(ctx, "signals", "ticks", "org_07", nil)
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
	filter := map[string]any{"bucket": 3}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		items, err := db.ListRecords(ctx, "signals", "ticks", "org_07", filter, 64)
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
				Data:           map[string]any{"kind": "tick"},
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
func (f *fakeBeginner) QueryMaps(context.Context, string, ...any) ([]map[string]any, error) {
	return nil, nil
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
func (f *fakeTx) QueryMaps(context.Context, string, ...any) ([]map[string]any, error) {
	return nil, nil
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
	if _, err := db.QueryMaps(context.Background(), "select 1"); err == nil {
		t.Fatalf("expected nil QueryMaps error")
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
	if _, err := db.ListRecords(context.Background(), "d", "c", "o", nil, 1); err == nil {
		t.Fatalf("expected nil ListRecords error")
	}
	if _, err := db.CountRecords(context.Background(), "d", "c", "o", nil); err == nil {
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
	if _, err := tx.QueryMaps(context.Background(), "select 1"); err == nil {
		t.Fatalf("expected nil tx QueryMaps error")
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
	if rec.Domain != "d" || rec.Collection != "c" || rec.OrganizationID != "o" || rec.RecordID != "r" || rec.Data["organization_id"] != "o" {
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
	if err != nil || parsed["a"].(float64) != 1 {
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
	if err != nil || parsed["organization_id"] != nil || parsed["state"] != "ready" {
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
	where, args, pushed := buildPostgresRecordWhere("d", "c", "o", map[string]any{
		"active":   true,
		"owner'id": " user_1 ",
		"shard":    7,
	}, 1)
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
	where, args, pushed := buildPostgresRecordWhere("", "", "", map[string]any{
		"nested": map[string]any{"k": "v"},
	}, 3)
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
	if _, err := db.ListRecordsWithOptions(context.Background(), "d", "c", "o", map[string]any{"nested": map[string]any{"k": "v"}}, StateListOptions{RequirePushdown: true}); err == nil {
		t.Fatal("expected nil postgres list to fail before pushdown option")
	}
	db = &PostgresDB{}
	if _, err := db.ListRecordsWithOptions(context.Background(), "d", "c", "o", map[string]any{"nested": map[string]any{"k": "v"}}, StateListOptions{RequirePushdown: true}); !errors.Is(err, ErrUnsupportedFilterShape) {
		t.Fatalf("require-pushdown list error = %v", err)
	}
	if _, err := db.CountRecordsWithOptions(context.Background(), "d", "c", "o", map[string]any{"nested": map[string]any{"k": "v"}}, StateCountOptions{RequirePushdown: true}); !errors.Is(err, ErrUnsupportedFilterShape) {
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

func TestScanToMapsConvertsBytesAndReturnsRowErrors(t *testing.T) {
	rows := &fakeRows{
		fields: []pgconn.FieldDescription{{Name: "id"}, {Name: "payload"}},
		values: [][]any{
			{"row_1", []byte("bytes")},
		},
	}
	items, err := scanToMaps(rows)
	if err != nil {
		t.Fatalf("scanToMaps() error = %v", err)
	}
	if len(items) != 1 || items[0]["payload"] != "bytes" {
		t.Fatalf("items = %+v", items)
	}

	valueErr := errors.New("values failed")
	rows = &fakeRows{fields: []pgconn.FieldDescription{{Name: "id"}}, values: [][]any{{"row_1"}}, valueErr: valueErr}
	if _, err := scanToMaps(rows); !errors.Is(err, valueErr) {
		t.Fatalf("values error = %v", err)
	}
	rowErr := errors.New("rows failed")
	rows = &fakeRows{fields: []pgconn.FieldDescription{{Name: "id"}}, err: rowErr}
	if _, err := scanToMaps(rows); !errors.Is(err, rowErr) {
		t.Fatalf("rows error = %v", err)
	}
}

type fakeRows struct {
	fields   []pgconn.FieldDescription
	values   [][]any
	index    int
	err      error
	valueErr error
}

func (r *fakeRows) Close() {}
func (r *fakeRows) Err() error {
	return r.err
}
func (r *fakeRows) CommandTag() pgconn.CommandTag {
	return pgconn.CommandTag{}
}
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription {
	return r.fields
}
func (r *fakeRows) Next() bool {
	if r.index >= len(r.values) {
		return false
	}
	r.index++
	return true
}
func (r *fakeRows) Scan(...any) error {
	return nil
}
func (r *fakeRows) Values() ([]any, error) {
	if r.valueErr != nil {
		return nil, r.valueErr
	}
	return r.values[r.index-1], nil
}
func (r *fakeRows) RawValues() [][]byte {
	return nil
}
func (r *fakeRows) Conn() *pgx.Conn {
	return nil
}
