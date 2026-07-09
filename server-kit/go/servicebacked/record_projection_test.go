//go:build servicebacked

package servicebacked

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/projectiongw"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/worker"
)

// TestServiceBackedRecordProjectionCanonicalPath drives the canonical
// normalized-tables → projection bridge end-to-end against live Postgres:
//
//	write repo (BeginTx: INSERT into a normalized table, build the projection
//	job with the record identity, commit) → RecordProjectionProcessor (generic,
//	keyed by job args; read-back through a RecordFetcher whose Data keys are
//	the column names) → ProjectedRuntimeStore → durable
//	governance_state_records mirror + hot partition → projection gateway HTTP
//	snapshot.
//
// It then proves the update, delete-converge (row vanished before the job
// ran), and cold-restart (fresh process warms the projection back from
// Postgres via WarmScope) legs. EnqueueTx durability itself is River's
// contract; this test drives the seam a committed job reaches.
func TestServiceBackedRecordProjectionCanonicalPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	env := requireServiceEnv(t)
	state := openPostgres(t, env, serviceBackedPoolOptions(4))
	defer state.Close()
	applyStateSchema(t, ctx, state)
	applyDishSchema(t, ctx, state)

	orgID := uniqueName(env.prefix, "recproj-org")
	cleanupOrganization(t, ctx, state, orgID)
	cleanupDishes(t, ctx, state, orgID)

	projected, err := hermes.WrapRuntimeStore(state, hermes.RuntimeStoreOptions{
		IndexedFields:      []string{"status"},
		MaxRecordsPerScope: 64,
		MaxBytesPerScope:   1 << 20,
	})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}

	processor, err := hermes.NewRecordProjectionProcessor(projected, dishFetcher(projected))
	if err != nil {
		t.Fatalf("NewRecordProjectionProcessor() error = %v", err)
	}

	gw, err := projectiongw.NewGatewayForProjectedStore(projected, 16)
	if err != nil {
		t.Fatalf("NewGatewayForProjectedStore() error = %v", err)
	}
	defer gw.Close()
	withOrg := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(security.ContextWithOrganizationID(r.Context(), orgID)))
		})
	}
	srv := httptest.NewServer(withOrg(gw.Handler(projectiongw.HandlerConfig{})))
	defer srv.Close()

	// --- Leg 1: transactional write + projection job → snapshot serves it. ---
	dishID := uniqueName(env.prefix, "dish-jollof")
	job := insertDishTx(t, ctx, projected, orgID, dishID, "Jollof Rice", 4500, "v1")
	if err := processor.Handle(ctx, job); err != nil {
		t.Fatalf("Handle(insert job) error = %v", err)
	}

	// Durable mirror row exists (cold-start rebuild source).
	if _, found, err := state.GetRecord(ctx, "menu", "dishes", orgID, dishID); err != nil || !found {
		t.Fatalf("governance_state_records mirror: found=%v err=%v", found, err)
	}
	// Gateway snapshot serves the row with the column-name Data keys.
	snapshot := getSnapshot(t, srv.URL+"/v1/projections/menu/dishes")
	if got := snapshotRecordIDs(snapshot); !containsAll(got, dishID) {
		t.Fatalf("snapshot record ids = %v, want %s", got, dishID)
	}

	// --- Leg 2: update flows through the same seam. ---
	updateDishTx(t, ctx, projected, orgID, dishID, 5200)
	job2, err := hermes.NewRecordProjectionJob("menu", "dishes", orgID, dishID, "v2")
	if err != nil {
		t.Fatalf("NewRecordProjectionJob(v2) error = %v", err)
	}
	if err := processor.Handle(ctx, job2); err != nil {
		t.Fatalf("Handle(update job) error = %v", err)
	}
	rec, found, err := state.GetRecord(ctx, "menu", "dishes", orgID, dishID)
	if err != nil || !found {
		t.Fatalf("mirror after update: found=%v err=%v", found, err)
	}
	if value, ok := rec.Data.Get("price_minor"); !ok || value.Text != "5200" {
		t.Fatalf("mirror price_minor = %+v (ok=%v), want 5200", value, ok)
	}

	// --- Leg 3: cold restart — a fresh process rebuilds from Postgres. ---
	restarted, err := hermes.WrapRuntimeStore(state, hermes.RuntimeStoreOptions{
		MaxRecordsPerScope: 64,
		MaxBytesPerScope:   1 << 20,
	})
	if err != nil {
		t.Fatalf("restart WrapRuntimeStore() error = %v", err)
	}
	if err := restarted.WarmScope(ctx, "menu", "dishes", orgID); err != nil {
		t.Fatalf("restart WarmScope() error = %v", err)
	}
	projection := restarted.ProjectionName("menu", "dishes", orgID)
	if count, err := restarted.Store().Count(ctx, projection, hermes.Query{OrganizationID: orgID}, hermes.Fence{}); err != nil || count != 1 {
		t.Fatalf("restart hot count = %d err=%v, want 1 (projection must survive restart via Postgres rebuild)", count, err)
	}

	// --- Leg 4: delete-converge — normalized row gone, upsert job replays. ---
	deleteDishRow(t, ctx, projected, orgID, dishID)
	job3, err := hermes.NewRecordProjectionJob("menu", "dishes", orgID, dishID, "v3")
	if err != nil {
		t.Fatalf("NewRecordProjectionJob(v3) error = %v", err)
	}
	if err := processor.Handle(ctx, job3); err != nil {
		t.Fatalf("Handle(converge job) error = %v", err)
	}
	if _, found, err := state.GetRecord(ctx, "menu", "dishes", orgID, dishID); err != nil || found {
		t.Fatalf("mirror after converge: found=%v err=%v, want gone", found, err)
	}
	snapshot = getSnapshot(t, srv.URL+"/v1/projections/menu/dishes")
	if got := snapshotRecordIDs(snapshot); containsAll(got, dishID) {
		t.Fatalf("snapshot still serves %s after delete-converge", dishID)
	}

	cleanupOrganization(t, ctx, state, orgID)
	cleanupDishes(t, ctx, state, orgID)
}

// BenchmarkServiceBackedRecordProjectionHandle measures the canonical
// projection job unit against live Postgres: one Handle = read-back SELECT
// from the normalized table + ProjectedRuntimeStore.UpsertRecord (durable
// mirror write + hot apply). This is the per-mutation cost a write repo adds
// by enqueueing a projection job.
func BenchmarkServiceBackedRecordProjectionHandle(b *testing.B) {
	ctx := context.Background()
	env := requireServiceEnv(b)
	state := openPostgres(b, env, serviceBackedPoolOptions(8))
	defer state.Close()
	applyStateSchema(b, ctx, state)
	applyDishSchema(b, ctx, state)

	orgID := uniqueName(env.prefix, "recproj-bench-org")
	cleanupOrganization(b, ctx, state, orgID)
	cleanupDishes(b, ctx, state, orgID)

	projected, err := hermes.WrapRuntimeStore(state, hermes.RuntimeStoreOptions{
		MaxRecordsPerScope: 16, MaxBytesPerScope: 1 << 20,
	})
	if err != nil {
		b.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	processor, err := hermes.NewRecordProjectionProcessor(projected, dishFetcher(projected))
	if err != nil {
		b.Fatalf("NewRecordProjectionProcessor() error = %v", err)
	}
	dishID := uniqueName(env.prefix, "dish-bench")
	insertDishTx(b, ctx, projected, orgID, dishID, "Bench Suya", 3000, "v0")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		job, err := hermes.NewRecordProjectionJob("menu", "dishes", orgID, dishID, fmt.Sprintf("v%d", i))
		if err != nil {
			b.Fatalf("NewRecordProjectionJob() error = %v", err)
		}
		if err := processor.Handle(ctx, job); err != nil {
			b.Fatalf("Handle() error = %v", err)
		}
	}
	b.StopTimer()
	cleanupOrganization(b, ctx, state, orgID)
	cleanupDishes(b, ctx, state, orgID)
}

// TestServiceBackedUpsertRecordsBatchParity proves the single-statement unnest
// batch refines sequential UpsertRecord per record on live Postgres: same
// final rows, same IS-DISTINCT-FROM change detection (an unchanged replay
// never bumps updated_at; a real change bumps it without touching created_at),
// and last-write-wins for duplicate identities inside one batch.
func TestServiceBackedUpsertRecordsBatchParity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	env := requireServiceEnv(t)
	state := openPostgres(t, env, serviceBackedPoolOptions(4))
	defer state.Close()
	applyStateSchema(t, ctx, state)
	db := requirePostgresDB(t, state)

	orgID := uniqueName(env.prefix, "batch-parity-org")
	cleanupOrganization(t, ctx, state, orgID)

	mk := func(id string, price int64) database.DomainRecord {
		return database.DomainRecord{
			Domain: "menu", Collection: "dishes", OrganizationID: orgID, RecordID: id,
			Data: serviceRecordData(map[string]any{"name": "Parity Dish", "price_minor": price}),
		}
	}

	// Lane A: sequential singles. Lane B: one unnest batch with the same shapes.
	for _, rec := range []database.DomainRecord{mk("seq_1", 100), mk("seq_2", 200)} {
		if _, err := state.UpsertRecord(ctx, rec); err != nil {
			t.Fatalf("sequential UpsertRecord() error = %v", err)
		}
	}
	batch1, err := db.UpsertRecordsBatch(ctx, []database.DomainRecord{mk("bat_1", 100), mk("bat_2", 200)})
	if err != nil || len(batch1) != 2 {
		t.Fatalf("UpsertRecordsBatch() len=%d err=%v", len(batch1), err)
	}
	for _, pair := range [][2]string{{"seq_1", "bat_1"}, {"seq_2", "bat_2"}} {
		seq, foundSeq, err1 := state.GetRecord(ctx, "menu", "dishes", orgID, pair[0])
		bat, foundBat, err2 := state.GetRecord(ctx, "menu", "dishes", orgID, pair[1])
		if err1 != nil || err2 != nil || !foundSeq || !foundBat {
			t.Fatalf("parity read %v: found=%v/%v err=%v/%v", pair, foundSeq, foundBat, err1, err2)
		}
		seqJSON, err1 := seq.Data.MarshalJSON()
		batJSON, err2 := bat.Data.MarshalJSON()
		if err1 != nil || err2 != nil || string(seqJSON) != string(batJSON) {
			t.Fatalf("parity data mismatch %v: %s vs %s (err=%v/%v)", pair, seqJSON, batJSON, err1, err2)
		}
	}

	// Change detection: replaying identical data must not bump updated_at.
	replay, err := db.UpsertRecordsBatch(ctx, []database.DomainRecord{mk("bat_1", 100), mk("bat_2", 200)})
	if err != nil {
		t.Fatalf("replay batch error = %v", err)
	}
	for i := range replay {
		if !replay[i].UpdatedAt.Equal(batch1[i].UpdatedAt) {
			t.Fatalf("unchanged replay bumped updated_at for %s: %v -> %v (change detection lost)",
				replay[i].RecordID, batch1[i].UpdatedAt, replay[i].UpdatedAt)
		}
	}

	// Real change: updated_at bumps, created_at stays.
	changed, err := db.UpsertRecordsBatch(ctx, []database.DomainRecord{mk("bat_1", 150)})
	if err != nil || len(changed) != 1 {
		t.Fatalf("changed batch len=%d err=%v", len(changed), err)
	}
	if !changed[0].UpdatedAt.After(batch1[0].UpdatedAt) {
		t.Fatalf("data change did not bump updated_at: %v -> %v", batch1[0].UpdatedAt, changed[0].UpdatedAt)
	}
	if !changed[0].CreatedAt.Equal(batch1[0].CreatedAt) {
		t.Fatalf("created_at drifted on update: %v -> %v", batch1[0].CreatedAt, changed[0].CreatedAt)
	}

	// Duplicate identity in one batch: last write wins, both positions get the
	// final row's timestamps — the sequential-loop outcome.
	dup, err := db.UpsertRecordsBatch(ctx, []database.DomainRecord{mk("bat_dup", 1), mk("bat_dup", 2)})
	if err != nil || len(dup) != 2 {
		t.Fatalf("duplicate batch len=%d err=%v", len(dup), err)
	}
	final, found, err := state.GetRecord(ctx, "menu", "dishes", orgID, "bat_dup")
	if err != nil || !found {
		t.Fatalf("duplicate read: found=%v err=%v", found, err)
	}
	if value, ok := final.Data.Get("price_minor"); !ok || value.Text != "2" {
		t.Fatalf("duplicate LWW price = %+v (ok=%v), want 2", value, ok)
	}
	if !dup[0].UpdatedAt.Equal(dup[1].UpdatedAt) {
		t.Fatalf("duplicate positions returned different timestamps: %v vs %v", dup[0].UpdatedAt, dup[1].UpdatedAt)
	}

	cleanupOrganization(t, ctx, state, orgID)
}

// BenchmarkServiceBackedRecordProjectionBatch64 measures the round-trip-
// amortized shape: one RecordWorkerProcessor job carrying 64 records →
// UpsertRecords → one pipelined base round trip + one grouped hot apply.
// rec_ns/op is the per-record projection cost to compare against the
// per-record Handle benchmark (~0.9 ms): the durable boundary is paid per
// batch, not per record.
func BenchmarkServiceBackedRecordProjectionBatch64(b *testing.B) {
	const batchSize = 64
	ctx := context.Background()
	env := requireServiceEnv(b)
	state := openPostgres(b, env, serviceBackedPoolOptions(8))
	defer state.Close()
	applyStateSchema(b, ctx, state)

	orgID := uniqueName(env.prefix, "recproj-batch-org")
	cleanupOrganization(b, ctx, state, orgID)

	projected, err := hermes.WrapRuntimeStore(state, hermes.RuntimeStoreOptions{
		MaxRecordsPerScope: batchSize * 2, MaxBytesPerScope: 1 << 20,
	})
	if err != nil {
		b.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	processor, err := hermes.NewRecordWorkerProcessor(projected, "menu_projection_batch", "hotplane", func(_ context.Context, job worker.Job) ([]database.DomainRecord, error) {
		records := make([]database.DomainRecord, batchSize)
		for i := range records {
			records[i] = database.DomainRecord{
				Domain:         "menu",
				Collection:     "dishes",
				OrganizationID: orgID,
				RecordID:       fmt.Sprintf("dish_%d", i),
				Data:           serviceRecordData(map[string]any{"name": "Batch Dish", "price_minor": int64(1000 + i), "seq": job.IdempotencyKey}),
			}
		}
		return records, nil
	})
	if err != nil {
		b.Fatalf("NewRecordWorkerProcessor() error = %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		job := worker.Job{JobKind: "menu_projection_batch", Queue: "hotplane", IdempotencyKey: fmt.Sprintf("batch_%d", i)}
		if err := processor.Handle(ctx, job); err != nil {
			b.Fatalf("Handle() error = %v", err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*batchSize), "rec_ns/op")
	cleanupOrganization(b, ctx, state, orgID)
}

// --- normalized-table fixture: the app-side seams, minimally simulated ---

// applyDishSchema stands in for an app's normalized domain table.
func applyDishSchema(tb testing.TB, ctx context.Context, store database.RuntimeStore) {
	tb.Helper()
	if err := store.Exec(ctx, `CREATE TABLE IF NOT EXISTS service_backed_menu_dishes (
		id TEXT PRIMARY KEY,
		organization_id TEXT NOT NULL,
		name TEXT NOT NULL,
		price_minor BIGINT NOT NULL,
		status TEXT NOT NULL DEFAULT 'published',
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`); err != nil {
		tb.Fatalf("dish schema failed: %v", err)
	}
}

func cleanupDishes(tb testing.TB, ctx context.Context, store database.RuntimeStore, orgID string) {
	tb.Helper()
	if err := store.Exec(ctx, `DELETE FROM service_backed_menu_dishes WHERE organization_id = $1`, orgID); err != nil {
		tb.Fatalf("dish cleanup failed: %v", err)
	}
}

// dishFetcher is the app-side RecordFetcher: read-back from the normalized
// table, Data keys = the column names the projection consumers read.
func dishFetcher(db database.RuntimeStore) hermes.RecordFetcher {
	return func(ctx context.Context, domain, collection, organizationID, recordID string) (database.RecordData, bool, error) {
		if domain != "menu" || collection != "dishes" {
			return nil, false, fmt.Errorf("no fetcher for %s/%s", domain, collection)
		}
		var name, status string
		var priceMinor int64
		row := db.QueryRow(ctx,
			`SELECT name, price_minor, status FROM service_backed_menu_dishes WHERE id = $1 AND organization_id = $2`,
			recordID, organizationID)
		if err := row.Scan(&name, &priceMinor, &status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, false, nil
			}
			return nil, false, err
		}
		return serviceRecordData(map[string]any{
			"name": name, "price_minor": priceMinor, "status": status,
		}), true, nil
	}
}

// insertDishTx is the write-repo pattern: BeginTx, domain INSERT, build the
// projection job with the record identity inside the transaction scope,
// commit. (In an app the job goes to engine.EnqueueTx(ctx, tx, job) on the
// same tx; River owns that durability. The returned job is what the worker
// would receive after commit.)
func insertDishTx(tb testing.TB, ctx context.Context, db database.RuntimeStore, orgID, dishID, name string, priceMinor int64, mutationTag string) worker.Job {
	tb.Helper()
	beginner, ok := db.(database.TxBeginner)
	if !ok {
		tb.Fatal("store does not support transactions")
	}
	tx, err := beginner.BeginTx(ctx)
	if err != nil {
		tb.Fatalf("BeginTx() error = %v", err)
	}
	if err := tx.Exec(ctx,
		`INSERT INTO service_backed_menu_dishes (id, organization_id, name, price_minor) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, price_minor = EXCLUDED.price_minor, updated_at = NOW()`,
		dishID, orgID, name, priceMinor); err != nil {
		_ = tx.Rollback(ctx)
		tb.Fatalf("dish insert failed: %v", err)
	}
	built, err := hermes.NewRecordProjectionJob("menu", "dishes", orgID, dishID, mutationTag)
	if err != nil {
		_ = tx.Rollback(ctx)
		tb.Fatalf("NewRecordProjectionJob() error = %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		tb.Fatalf("Commit() error = %v", err)
	}
	return built
}

func updateDishTx(tb testing.TB, ctx context.Context, db database.RuntimeStore, orgID, dishID string, priceMinor int64) {
	tb.Helper()
	if err := db.Exec(ctx,
		`UPDATE service_backed_menu_dishes SET price_minor = $1, updated_at = NOW() WHERE id = $2 AND organization_id = $3`,
		priceMinor, dishID, orgID); err != nil {
		tb.Fatalf("dish update failed: %v", err)
	}
}

func deleteDishRow(tb testing.TB, ctx context.Context, db database.RuntimeStore, orgID, dishID string) {
	tb.Helper()
	if err := db.Exec(ctx,
		`DELETE FROM service_backed_menu_dishes WHERE id = $1 AND organization_id = $2`,
		dishID, orgID); err != nil {
		tb.Fatalf("dish delete failed: %v", err)
	}
}

func getSnapshot(t *testing.T, url string) *foundationpb.ProjectionSnapshot {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("snapshot GET error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("snapshot status = %d, want 200", resp.StatusCode)
	}
	return decodeSnapshot(t, resp)
}
