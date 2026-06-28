//go:build servicebacked

package servicebacked

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scaling"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/wsrouting"
)

const (
	defaultServiceBackedLoadResearchSteps = "1000,10000,50000,100000,250000,500000,1000000"
	defaultServiceBackedLoadResearchLanes = "postgres_send_batch64,postgres_copy_from1024,redis_set_get_many64,redis_xadd_many64,redis_stream_drain64,hermes_rebuild_postgres_snapshot,hermes_redis_tailer64,hermes_hot_count,mixed_pg_redis_hermes64,pipeline_pg_redis_hermes,wsroute_register_redis,wsroute_broadcast_after_register"
)

func TestServiceBackedLoadResearchRamps(t *testing.T) {
	if os.Getenv("RUN_SERVICE_BACKED_LOAD_RESEARCH") != "1" {
		t.Skip("set RUN_SERVICE_BACKED_LOAD_RESEARCH=1 to run staged service-backed load research")
	}

	env := requireServiceEnv(t)
	steps := serviceBackedLoadSteps(t)
	lanes := serviceBackedLoadLaneSet()
	cfg := scaling.AutoTune()
	postgresMaxConnections := serviceBackedLoadPostgresMaxConnections(t)
	reservedConnections := serviceBackedLoadReservedDBConnections(t, postgresMaxConnections)
	maxWorkers := serviceBackedLoadPositiveIntEnv(
		t,
		"SERVICE_BACKED_LOAD_RESEARCH_MAX_WORKERS",
		serviceBackedLoadDefaultMaxWorkers(cfg, postgresMaxConnections, reservedConnections),
	)
	timeout := serviceBackedLoadDurationEnv("SERVICE_BACKED_LOAD_RESEARCH_TIMEOUT", 90*time.Minute)
	recorder := newServiceBackedLoadRecorder(t)
	defer recorder.close()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	t.Logf(
		"SERVICE_BACKED_LOAD_RESEARCH_BEGIN steps=%v lanes=%s tier=%s cpu=%d max_workers=%d db_max=%d db_reserved=%d timeout=%s",
		steps,
		strings.Join(sortedServiceBackedLoadLanes(lanes), ","),
		cfg.Tier.String(),
		cfg.CPUCount,
		maxWorkers,
		postgresMaxConnections,
		reservedConnections,
		timeout,
	)
	for _, step := range steps {
		t.Run(fmt.Sprintf("step_%d", step), func(t *testing.T) {
			runServiceBackedLoadStep(t, ctx, env, recorder, lanes, step, maxWorkers)
		})
	}
}

func runServiceBackedLoadStep(
	t *testing.T,
	ctx context.Context,
	env serviceEnv,
	recorder *serviceBackedLoadRecorder,
	lanes map[string]bool,
	step int,
	maxWorkers int,
) {
	t.Helper()
	prepareServiceBackedLoadSchemas(t, ctx, env, lanes, maxWorkers)
	runIfServiceBackedLoadLane(t, lanes, "postgres_send_batch64", func() {
		runServiceBackedLoadPostgresSendBatch(t, ctx, env, recorder, step, maxWorkers)
	})
	runIfServiceBackedLoadLane(t, lanes, "postgres_copy_from1024", func() {
		runServiceBackedLoadPostgresCopyFrom(t, ctx, env, recorder, step, maxWorkers)
	})
	runIfServiceBackedLoadLane(t, lanes, "redis_set_get_many64", func() {
		runServiceBackedLoadRedisSetGetMany(t, ctx, env, recorder, step, maxWorkers)
	})
	runIfServiceBackedLoadLane(t, lanes, "redis_xadd_many64", func() {
		runServiceBackedLoadRedisXAddMany(t, ctx, env, recorder, step, maxWorkers)
	})
	runIfServiceBackedLoadLane(t, lanes, "redis_stream_drain64", func() {
		runServiceBackedLoadRedisStreamDrain(t, ctx, env, recorder, step)
	})
	runIfServiceBackedLoadLane(t, lanes, "hermes_rebuild_postgres_snapshot", func() {
		runServiceBackedLoadHermesRebuild(t, ctx, env, recorder, step, maxWorkers)
	})
	runIfServiceBackedLoadLane(t, lanes, "hermes_redis_tailer64", func() {
		runServiceBackedLoadHermesRedisTailer(t, ctx, env, recorder, step)
	})
	runIfServiceBackedLoadLane(t, lanes, "hermes_hot_count", func() {
		runServiceBackedLoadHermesHotCount(t, ctx, env, recorder, step, maxWorkers)
	})
	runIfServiceBackedLoadLane(t, lanes, "mixed_pg_redis_hermes64", func() {
		runServiceBackedLoadMixed(t, ctx, env, recorder, step, maxWorkers)
	})
	runIfServiceBackedLoadLane(t, lanes, "pipeline_pg_redis_hermes", func() {
		runServiceBackedLoadPipeline(t, ctx, env, recorder, step, maxWorkers)
	})
	runIfServiceBackedLoadLane(t, lanes, "wsroute_register_redis", func() {
		runServiceBackedLoadWSRouteRegister(t, ctx, env, recorder, step, maxWorkers)
	})
	runIfServiceBackedLoadLane(t, lanes, "wsroute_broadcast_after_register", func() {
		runServiceBackedLoadWSRouteBroadcast(t, ctx, env, recorder, step)
	})
}

func prepareServiceBackedLoadSchemas(
	t *testing.T,
	ctx context.Context,
	env serviceEnv,
	lanes map[string]bool,
	maxWorkers int,
) {
	t.Helper()
	needsState := serviceBackedLoadLaneEnabled(lanes,
		"postgres_send_batch64",
		"hermes_rebuild_postgres_snapshot",
		"hermes_hot_count",
		"mixed_pg_redis_hermes64",
		"pipeline_pg_redis_hermes",
	)
	needsCopy := serviceBackedLoadLaneEnabled(lanes, "postgres_copy_from1024")
	if !needsState && !needsCopy {
		return
	}
	store := openPostgres(t, env, serviceBackedLoadHermesRebuildPoolOptions(t, maxWorkers))
	defer store.Close()
	if needsState {
		applyStateSchema(t, ctx, store)
	}
	if needsCopy {
		applyCopySchema(t, ctx, store)
	}
}

func serviceBackedLoadLaneEnabled(lanes map[string]bool, names ...string) bool {
	if lanes["all"] {
		return true
	}
	for _, name := range names {
		if lanes[name] {
			return true
		}
	}
	return false
}

func runServiceBackedLoadPostgresSendBatch(
	t *testing.T,
	ctx context.Context,
	env serviceEnv,
	recorder *serviceBackedLoadRecorder,
	step int,
	maxWorkers int,
) {
	t.Helper()
	const lane = "postgres_send_batch64"
	const batchSize = 64
	before := serviceBackedLoadRuntimeSnapshot()
	setupStart := time.Now()
	poolOptions := serviceBackedLoadPoolOptions(t, maxWorkers)
	store := openPostgres(t, env, poolOptions)
	defer store.Close()
	db := requirePostgresDB(t, store)
	orgID := uniqueName(env.prefix, "load-pg-batch")
	cleanupOrganization(t, ctx, store, orgID)
	setup := time.Since(setupStart)
	workers := serviceBackedLoadDBWorkers(step, batchSize, maxWorkers, poolOptions)
	stats, err := runServiceBackedLoadBatches(ctx, step, batchSize, workers, func(ctx context.Context, _ int, start, count int) error {
		return db.SendBatch(ctx, func(batch *pgx.Batch) {
			queueStateStoreBatch(batch, orgID, start, count)
		}, consumeBatchExecs(count))
	})
	if err != nil {
		t.Fatalf("%s failed: %v", lane, err)
	}
	dbStats := store.Stats()
	recordServiceBackedLoadLane(t, recorder, serviceBackedLoadRow{
		Step:      step,
		Lane:      lane,
		BatchSize: batchSize,
		Workers:   workers,
		Setup:     setup,
		Stats:     stats,
		Before:    before,
		After:     serviceBackedLoadRuntimeSnapshot(),
		DBStats:   dbStats,
		Notes:     "Postgres SendBatch amortizes independent upserts under bounded pool acquire/query budgets",
	})
	cleanupOrganization(t, ctx, store, orgID)
}

func runServiceBackedLoadPostgresCopyFrom(
	t *testing.T,
	ctx context.Context,
	env serviceEnv,
	recorder *serviceBackedLoadRecorder,
	step int,
	maxWorkers int,
) {
	t.Helper()
	const lane = "postgres_copy_from1024"
	const batchSize = 1024
	before := serviceBackedLoadRuntimeSnapshot()
	setupStart := time.Now()
	poolOptions := serviceBackedLoadPoolOptions(t, maxWorkers)
	store := openPostgres(t, env, poolOptions)
	defer store.Close()
	db := requirePostgresDB(t, store)
	orgID := uniqueName(env.prefix, "load-pg-copy")
	setup := time.Since(setupStart)
	workers := serviceBackedLoadDBWorkers(step, batchSize, maxWorkers, poolOptions)
	stats, err := runServiceBackedLoadBatches(ctx, step, batchSize, workers, func(ctx context.Context, _ int, start, count int) error {
		_, err := db.CopyFromRows(ctx, []string{"service_backed_copy_records"}, copyColumns(), copyRows(orgID, start, count))
		return err
	})
	if err != nil {
		t.Fatalf("%s failed: %v", lane, err)
	}
	dbStats := store.Stats()
	recordServiceBackedLoadLane(t, recorder, serviceBackedLoadRow{
		Step:      step,
		Lane:      lane,
		BatchSize: batchSize,
		Workers:   workers,
		Setup:     setup,
		Stats:     stats,
		Before:    before,
		After:     serviceBackedLoadRuntimeSnapshot(),
		DBStats:   dbStats,
		Notes:     "Postgres COPY lane for append/import workloads; not a semantic upsert path",
	})
	if err := store.Exec(ctx, `DELETE FROM service_backed_copy_records WHERE organization_id = $1`, orgID); err != nil {
		t.Fatalf("copy cleanup failed: %v", err)
	}
}

func runServiceBackedLoadRedisSetGetMany(
	t *testing.T,
	ctx context.Context,
	env serviceEnv,
	recorder *serviceBackedLoadRecorder,
	step int,
	maxWorkers int,
) {
	t.Helper()
	const lane = "redis_set_get_many64"
	const batchSize = 64
	before := serviceBackedLoadRuntimeSnapshot()
	setupStart := time.Now()
	client := openRedis(t, env)
	defer client.Close()
	batch := requireRedisBatch(t, client)
	ttl := serviceBackedLoadDurationEnv("SERVICE_BACKED_LOAD_RESEARCH_REDIS_TTL", 2*time.Minute)
	prefix := uniqueName(env.prefix, "load-redis-setget")
	setup := time.Since(setupStart)
	workers := serviceBackedLoadWorkers(step, batchSize, maxWorkers)
	stats, err := runServiceBackedLoadBatches(ctx, step, batchSize, workers, func(ctx context.Context, _ int, start, count int) error {
		values, keys := serviceBackedLoadRedisValues(prefix, start, count)
		got, err := batch.SetGetMany(ctx, values, ttl)
		if err != nil {
			return err
		}
		if len(got) != len(keys) {
			return fmt.Errorf("redis SetGetMany returned %d keys, want %d", len(got), len(keys))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%s failed: %v", lane, err)
	}
	recordServiceBackedLoadLane(t, recorder, serviceBackedLoadRow{
		Step:      step,
		Lane:      lane,
		BatchSize: batchSize,
		Workers:   workers,
		Setup:     setup,
		Stats:     stats,
		Before:    before,
		After:     serviceBackedLoadRuntimeSnapshot(),
		Notes:     "Redis SetGetMany pipeline lane for hot multi-key cache paths",
	})
}

func runServiceBackedLoadRedisXAddMany(
	t *testing.T,
	ctx context.Context,
	env serviceEnv,
	recorder *serviceBackedLoadRecorder,
	step int,
	maxWorkers int,
) {
	t.Helper()
	const lane = "redis_xadd_many64"
	const batchSize = 64
	before := serviceBackedLoadRuntimeSnapshot()
	setupStart := time.Now()
	client := openRedis(t, env)
	defer client.Close()
	streamBatch := requireRedisStreamBatch(t, client)
	stream := uniqueName(env.prefix, "load-xadd")
	defer cleanupRedisKeys(t, ctx, client, stream)
	setup := time.Since(setupStart)
	workers := serviceBackedLoadWorkers(step, batchSize, maxWorkers)
	stats, err := runServiceBackedLoadBatches(ctx, step, batchSize, workers, func(ctx context.Context, _ int, start, count int) error {
		_, errs := streamBatch.XAddMany(ctx, stream, serviceBackedLoadStreamEntries(start, count))
		return firstSliceError(errs)
	})
	if err != nil {
		t.Fatalf("%s failed: %v", lane, err)
	}
	recordServiceBackedLoadLane(t, recorder, serviceBackedLoadRow{
		Step:      step,
		Lane:      lane,
		BatchSize: batchSize,
		Workers:   workers,
		Setup:     setup,
		Stats:     stats,
		Before:    before,
		After:     serviceBackedLoadRuntimeSnapshot(),
		Notes:     "Redis Streams XAddMany lane for durable projector/event relay append pressure",
	})
}

func runServiceBackedLoadRedisStreamDrain(
	t *testing.T,
	ctx context.Context,
	env serviceEnv,
	recorder *serviceBackedLoadRecorder,
	step int,
) {
	t.Helper()
	const lane = "redis_stream_drain64"
	const batchSize = 64
	before := serviceBackedLoadRuntimeSnapshot()
	setupStart := time.Now()
	client := openRedis(t, env)
	defer client.Close()
	streamBatch := requireRedisStreamBatch(t, client)
	stream := uniqueName(env.prefix, "load-stream-drain")
	group := uniqueName(env.prefix, "load-stream-drain-group")
	defer cleanupRedisKeys(t, ctx, client, stream)
	if err := serviceBackedLoadSeedRedisStream(ctx, streamBatch, stream, step, batchSize); err != nil {
		t.Fatalf("%s setup failed: %v", lane, err)
	}
	setup := time.Since(setupStart)
	stats, err := runServiceBackedLoadStreamDrain(ctx, client, stream, group, "consumer-a", step, batchSize)
	if err != nil {
		t.Fatalf("%s failed: %v", lane, err)
	}
	recordServiceBackedLoadLane(t, recorder, serviceBackedLoadRow{
		Step:      step,
		Lane:      lane,
		BatchSize: batchSize,
		Workers:   1,
		Setup:     setup,
		Stats:     stats,
		Before:    before,
		After:     serviceBackedLoadRuntimeSnapshot(),
		Notes:     "Redis Stream group drain+ack lane; setup is XADD pressure, measured path is read+ack lag",
	})
}

func runServiceBackedLoadHermesRebuild(
	t *testing.T,
	ctx context.Context,
	env serviceEnv,
	recorder *serviceBackedLoadRecorder,
	step int,
	maxWorkers int,
) {
	t.Helper()
	const lane = "hermes_rebuild_postgres_snapshot"
	const seedBatchSize = 256
	before := serviceBackedLoadRuntimeSnapshot()
	setupStart := time.Now()
	poolOptions := serviceBackedLoadHermesRebuildPoolOptions(t, maxWorkers)
	store := openPostgres(t, env, poolOptions)
	defer store.Close()
	db := requirePostgresDB(t, store)
	orgID := uniqueName(env.prefix, "load-hermes-rebuild")
	cleanupOrganization(t, ctx, store, orgID)
	workers := serviceBackedLoadDBWorkers(step, seedBatchSize, maxWorkers, poolOptions)
	if _, err := runServiceBackedLoadBatches(ctx, step, seedBatchSize, workers, func(ctx context.Context, _ int, start, count int) error {
		return db.SendBatch(ctx, func(batch *pgx.Batch) {
			queueHermesStateBatch(batch, orgID, start, count)
		}, consumeBatchExecs(count))
	}); err != nil {
		t.Fatalf("%s setup seed failed: %v", lane, err)
	}
	hotplane := newServiceBackedLoadHermesStore(t, "svc_load_rebuild", step)
	setup := time.Since(setupStart)
	start := time.Now()
	result, err := hotplane.Rebuild(ctx, "svc_load_rebuild", store, hermes.Query{OrganizationID: orgID, Limit: step})
	elapsed := time.Since(start)
	if err != nil || result.Applied != step {
		t.Fatalf("%s result=%+v err=%v, want %d applied", lane, result, err, step)
	}
	hermesStats, _ := hotplane.Stats("svc_load_rebuild")
	recordServiceBackedLoadLane(t, recorder, serviceBackedLoadRow{
		Step:        step,
		Lane:        lane,
		BatchSize:   step,
		Workers:     1,
		Setup:       setup,
		Stats:       serviceBackedLoadStatsFromSingle(step, elapsed),
		Before:      before,
		After:       serviceBackedLoadRuntimeSnapshot(),
		DBStats:     store.Stats(),
		HermesStats: hermesStats,
		Notes:       "Hermes trusted rebuild from live Postgres snapshot; control-plane repair/warmup lane",
	})
	cleanupOrganization(t, ctx, store, orgID)
}

func runServiceBackedLoadHermesRedisTailer(
	t *testing.T,
	ctx context.Context,
	env serviceEnv,
	recorder *serviceBackedLoadRecorder,
	step int,
) {
	t.Helper()
	const lane = "hermes_redis_tailer64"
	const batchSize = 64
	before := serviceBackedLoadRuntimeSnapshot()
	setupStart := time.Now()
	client := openRedis(t, env)
	defer client.Close()
	streamBatch := requireRedisStreamBatch(t, client)
	stream := uniqueName(env.prefix, "load-hermes-tail")
	group := uniqueName(env.prefix, "load-hermes-tail-group")
	defer cleanupRedisKeys(t, ctx, client, stream)
	if err := serviceBackedLoadSeedHermesStream(ctx, streamBatch, stream, "org-load-hermes-tail", step, batchSize); err != nil {
		t.Fatalf("%s setup failed: %v", lane, err)
	}
	hotplane := newServiceBackedLoadHermesStore(t, "svc_load_tail", step)
	source, err := hermes.NewRedisStreamEnvelopeSource(client, stream, group, "consumer-a", "")
	if err != nil {
		t.Fatalf("NewRedisStreamEnvelopeSource() error = %v", err)
	}
	tailer, err := hermes.NewEnvelopeTailer(hotplane, "svc_load_tail", source, hermes.TailerOptions{MaxBatch: batchSize})
	if err != nil {
		t.Fatalf("NewEnvelopeTailer() error = %v", err)
	}
	setup := time.Since(setupStart)
	stats, err := runServiceBackedLoadHermesTailer(ctx, tailer, step)
	if err != nil {
		t.Fatalf("%s failed: %v", lane, err)
	}
	hermesStats, _ := hotplane.Stats("svc_load_tail")
	recordServiceBackedLoadLane(t, recorder, serviceBackedLoadRow{
		Step:        step,
		Lane:        lane,
		BatchSize:   batchSize,
		Workers:     1,
		Setup:       setup,
		Stats:       stats,
		Before:      before,
		After:       serviceBackedLoadRuntimeSnapshot(),
		HermesStats: hermesStats,
		Notes:       "Hermes tailer applies committed projection envelopes from live Redis Streams before ack",
	})
}

func runServiceBackedLoadHermesHotCount(
	t *testing.T,
	ctx context.Context,
	env serviceEnv,
	recorder *serviceBackedLoadRecorder,
	step int,
	maxWorkers int,
) {
	t.Helper()
	const lane = "hermes_hot_count"
	const seedBatchSize = 256
	const countBatchSize = 256
	before := serviceBackedLoadRuntimeSnapshot()
	setupStart := time.Now()
	poolOptions := serviceBackedLoadHermesRebuildPoolOptions(t, maxWorkers)
	store := openPostgres(t, env, poolOptions)
	defer store.Close()
	db := requirePostgresDB(t, store)
	orgID := uniqueName(env.prefix, "load-hermes-count")
	cleanupOrganization(t, ctx, store, orgID)
	seedWorkers := serviceBackedLoadDBWorkers(step, seedBatchSize, maxWorkers, poolOptions)
	if _, err := runServiceBackedLoadBatches(ctx, step, seedBatchSize, seedWorkers, func(ctx context.Context, _ int, start, count int) error {
		return db.SendBatch(ctx, func(batch *pgx.Batch) {
			queueHermesStateBatch(batch, orgID, start, count)
		}, consumeBatchExecs(count))
	}); err != nil {
		t.Fatalf("%s setup seed failed: %v", lane, err)
	}
	hotplane := newServiceBackedLoadHermesStore(t, "svc_load_count", step)
	if result, err := hotplane.Rebuild(ctx, "svc_load_count", store, hermes.Query{OrganizationID: orgID, Limit: step}); err != nil || result.Applied != step {
		t.Fatalf("%s setup rebuild result=%+v err=%v, want %d applied", lane, result, err, step)
	}
	filter, ok := hermes.NewQueryFilter("bucket", "07")
	if !ok {
		t.Fatal("bucket filter is not indexable")
	}
	query := hermes.QueryWithFilters(orgID, 0, filter)
	ops := min(step, serviceBackedLoadPositiveIntEnv(t, "SERVICE_BACKED_LOAD_RESEARCH_HERMES_READ_OPS", 100000))
	setup := time.Since(setupStart)
	workers := serviceBackedLoadWorkers(ops, countBatchSize, maxWorkers)
	stats, err := runServiceBackedLoadBatches(ctx, ops, countBatchSize, workers, func(ctx context.Context, _ int, _ int, count int) error {
		for range count {
			got, err := hotplane.Count(ctx, "svc_load_count", query, hermes.Fence{})
			if err != nil {
				return err
			}
			if got <= 0 {
				return fmt.Errorf("hermes hot count = %d", got)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%s failed: %v", lane, err)
	}
	hermesStats, _ := hotplane.Stats("svc_load_count")
	recordServiceBackedLoadLane(t, recorder, serviceBackedLoadRow{
		Step:        step,
		Lane:        lane,
		TotalUnits:  ops,
		BatchSize:   countBatchSize,
		Workers:     workers,
		Setup:       setup,
		Stats:       stats,
		Before:      before,
		After:       serviceBackedLoadRuntimeSnapshot(),
		DBStats:     store.Stats(),
		HermesStats: hermesStats,
		Notes:       "Hermes indexed hot count after live Postgres rebuild; TotalUnits may be capped by SERVICE_BACKED_LOAD_RESEARCH_HERMES_READ_OPS",
	})
	cleanupOrganization(t, ctx, store, orgID)
}

func runServiceBackedLoadMixed(
	t *testing.T,
	ctx context.Context,
	env serviceEnv,
	recorder *serviceBackedLoadRecorder,
	step int,
	maxWorkers int,
) {
	t.Helper()
	const lane = "mixed_pg_redis_hermes64"
	const batchSize = 64
	before := serviceBackedLoadRuntimeSnapshot()
	setupStart := time.Now()
	poolOptions := serviceBackedLoadPoolOptions(t, maxWorkers)
	store := openPostgres(t, env, poolOptions)
	defer store.Close()
	db := requirePostgresDB(t, store)
	client := openRedis(t, env)
	defer client.Close()
	redisBatch := requireRedisBatch(t, client)
	orgID := uniqueName(env.prefix, "load-mixed")
	cleanupOrganization(t, ctx, store, orgID)
	hotplane := newServiceBackedLoadHermesStore(t, "svc_load_mixed", step)
	ttl := serviceBackedLoadDurationEnv("SERVICE_BACKED_LOAD_RESEARCH_REDIS_TTL", 2*time.Minute)
	setup := time.Since(setupStart)
	workers := serviceBackedLoadDBWorkers(step, batchSize, maxWorkers, poolOptions)
	stats, err := runServiceBackedLoadBatches(ctx, step, batchSize, workers, func(ctx context.Context, workerID int, start, count int) error {
		if err := db.SendBatch(ctx, func(batch *pgx.Batch) {
			queueStateStoreBatch(batch, orgID, start, count)
		}, consumeBatchExecs(count)); err != nil {
			return err
		}
		values, keys := serviceBackedLoadRedisValues(orgID, start, count)
		got, err := redisBatch.SetGetMany(ctx, values, ttl)
		if err != nil {
			return err
		}
		if len(got) != len(keys) {
			return fmt.Errorf("redis SetGetMany returned %d keys, want %d", len(got), len(keys))
		}
		records := serviceBackedLoadHermesRecords(orgID, start, count)
		sourcePrefix := fmt.Sprintf("service-backed-mixed-%d", workerID)
		result, err := hotplane.ApplyRecords(ctx, "svc_load_mixed", sourcePrefix, uint64(start+1), records)
		if err != nil {
			return err
		}
		if result.Applied != count {
			return fmt.Errorf("hermes applied %d records, want %d", result.Applied, count)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%s failed: %v", lane, err)
	}
	hermesStats, _ := hotplane.Stats("svc_load_mixed")
	recordServiceBackedLoadLane(t, recorder, serviceBackedLoadRow{
		Step:        step,
		Lane:        lane,
		BatchSize:   batchSize,
		Workers:     workers,
		Setup:       setup,
		Stats:       stats,
		Before:      before,
		After:       serviceBackedLoadRuntimeSnapshot(),
		DBStats:     store.Stats(),
		HermesStats: hermesStats,
		Notes:       "Mixed workflow: Postgres SendBatch + Redis SetGetMany + Hermes ApplyRecords in one bounded batch lane",
	})
	cleanupOrganization(t, ctx, store, orgID)
}

func runServiceBackedLoadPipeline(
	t *testing.T,
	ctx context.Context,
	env serviceEnv,
	recorder *serviceBackedLoadRecorder,
	step int,
	maxWorkers int,
) {
	t.Helper()
	const lane = "pipeline_pg_redis_hermes"
	const maxEnvelopeMutations = 4096
	batchSize := min(serviceBackedLoadPositiveIntEnv(t, "SERVICE_BACKED_LOAD_RESEARCH_PIPELINE_BATCH", 512), maxEnvelopeMutations)
	tailerBatch := min(serviceBackedLoadPositiveIntEnv(t, "SERVICE_BACKED_LOAD_RESEARCH_PIPELINE_TAILER_BATCH", 256), maxEnvelopeMutations)
	redisGroupBatches := serviceBackedLoadPositiveIntEnv(t, "SERVICE_BACKED_LOAD_RESEARCH_PIPELINE_REDIS_GROUP_BATCHES", 8)
	queueDepth := serviceBackedLoadPositiveIntEnv(t, "SERVICE_BACKED_LOAD_RESEARCH_PIPELINE_QUEUE_DEPTH", max(maxWorkers*4, 16))
	maxLag := int64(serviceBackedLoadPositiveIntEnv(t, "SERVICE_BACKED_LOAD_RESEARCH_PIPELINE_MAX_LAG", max(batchSize*64, tailerBatch*64)))
	readOps := min(step, serviceBackedLoadPositiveIntEnv(t, "SERVICE_BACKED_LOAD_RESEARCH_PIPELINE_READ_OPS", 100000))
	readWorkers := min(serviceBackedLoadPositiveIntEnv(t, "SERVICE_BACKED_LOAD_RESEARCH_PIPELINE_READ_WORKERS", 4), max(maxWorkers, 1))
	before := serviceBackedLoadRuntimeSnapshot()
	setupStart := time.Now()
	poolOptions := serviceBackedLoadPoolOptions(t, maxWorkers)
	store := openPostgres(t, env, poolOptions)
	defer store.Close()
	db := requirePostgresDB(t, store)
	client := openRedis(t, env)
	defer client.Close()
	streamBatch := requireRedisStreamBatch(t, client)
	orgID := uniqueName(env.prefix, "load-pipeline")
	projection := "svc_load_pipeline"
	stream := uniqueName(env.prefix, "load-pipeline-stream")
	group := uniqueName(env.prefix, "load-pipeline-group")
	cleanupOrganization(t, ctx, store, orgID)
	defer cleanupOrganization(t, ctx, store, orgID)
	defer cleanupRedisKeys(t, ctx, client, stream)
	hotplane := newServiceBackedLoadHermesStore(t, projection, step)
	source, err := hermes.NewRedisStreamEnvelopeSource(client, stream, group, "consumer-a", "")
	if err != nil {
		t.Fatalf("NewRedisStreamEnvelopeSource() error = %v", err)
	}
	tailer, err := hermes.NewEnvelopeTailer(hotplane, projection, source, hermes.TailerOptions{MaxBatch: tailerBatch})
	if err != nil {
		t.Fatalf("NewEnvelopeTailer() error = %v", err)
	}
	setup := time.Since(setupStart)
	pgWorkers := serviceBackedLoadPipelineDBWorkers(t, step, batchSize, maxWorkers, poolOptions)
	redisWorkers := min(
		serviceBackedLoadPositiveIntEnv(t, "SERVICE_BACKED_LOAD_RESEARCH_PIPELINE_REDIS_WORKERS", min(max(maxWorkers, 1), 32)),
		max(maxWorkers, 1),
	)
	stats, stageNotes, hermesStats, dbStats, err := runServiceBackedPipelineStages(
		ctx,
		db,
		streamBatch,
		hotplane,
		tailer,
		projection,
		stream,
		orgID,
		step,
		batchSize,
		tailerBatch,
		redisGroupBatches,
		queueDepth,
		maxLag,
		pgWorkers,
		redisWorkers,
		readOps,
		readWorkers,
		store,
	)
	if err != nil {
		t.Fatalf("%s failed: %v", lane, err)
	}
	recordServiceBackedLoadLane(t, recorder, serviceBackedLoadRow{
		Step:        step,
		Lane:        lane,
		BatchSize:   batchSize,
		Workers:     pgWorkers + redisWorkers + readWorkers + 1,
		Setup:       setup,
		Stats:       stats,
		Before:      before,
		After:       serviceBackedLoadRuntimeSnapshot(),
		DBStats:     dbStats,
		HermesStats: hermesStats,
		Notes: fmt.Sprintf(
			"Concurrent Postgres durable writes -> Redis batched projection envelopes -> Hermes tail/apply -> hot reads with lag backpressure; tailer_batch=%d redis_group_batches=%d max_lag=%d read_ops=%d %s",
			tailerBatch,
			redisGroupBatches,
			maxLag,
			readOps,
			stageNotes,
		),
	})
}

type serviceBackedPipelineBatch struct {
	Start int
	Count int
}

func runServiceBackedPipelineStages(
	ctx context.Context,
	db *database.PostgresDB,
	streamBatch rediskit.StreamBatchClient,
	hotplane *hermes.Store,
	tailer *hermes.EnvelopeTailer,
	projection string,
	stream string,
	orgID string,
	total int,
	batchSize int,
	tailerBatch int,
	redisGroupBatches int,
	queueDepth int,
	maxLag int64,
	pgWorkers int,
	redisWorkers int,
	readOps int,
	readWorkers int,
	store database.RuntimeStore,
) (serviceBackedLoadStats, string, hermes.Stats, database.StoreStats, error) {
	pipelineCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	batches := int(math.Ceil(float64(total) / float64(batchSize)))
	pgOut := make(chan serviceBackedPipelineBatch, min(queueDepth, max(batches, 1)))
	errCh := make(chan error, pgWorkers+redisWorkers+readWorkers+2)
	pgDurations := make(chan serviceBackedLoadDuration, max(batches, 1))
	redisDurations := make(chan serviceBackedLoadDuration, max(batches, 1))
	tailDurations := make(chan serviceBackedLoadDuration, max(batches, 1))
	readDurations := make(chan serviceBackedLoadDuration, max(readOps, 1))
	readStop := make(chan struct{})
	readDone := make(chan struct{})
	var next atomic.Int64
	var pgWritten atomic.Int64
	var redisPublished atomic.Int64
	var hermesApplied atomic.Int64
	var readCompleted atomic.Int64
	var maxRedisLag atomic.Int64
	var maxHermesLag atomic.Int64
	var pgWG sync.WaitGroup
	started := time.Now()

	sendErr := func(err error) {
		if err == nil {
			return
		}
		select {
		case errCh <- err:
		default:
		}
		cancel()
	}

	for workerID := 0; workerID < pgWorkers; workerID++ {
		pgWG.Add(1)
		go func() {
			defer pgWG.Done()
			for {
				start := int(next.Add(int64(batchSize))) - batchSize
				if start >= total {
					return
				}
				count := min(batchSize, total-start)
				if err := pipelineCtx.Err(); err != nil {
					sendErr(err)
					return
				}
				opStart := time.Now()
				err := db.SendBatch(pipelineCtx, func(batch *pgx.Batch) {
					queueHermesStateBatch(batch, orgID, start, count)
				}, consumeBatchExecs(count))
				if err != nil {
					sendErr(err)
					return
				}
				elapsed := time.Since(opStart)
				pgWritten.Add(int64(count))
				pgDurations <- serviceBackedLoadDuration{Batch: elapsed, PerUnit: elapsed / time.Duration(count)}
				select {
				case pgOut <- serviceBackedPipelineBatch{Start: start, Count: count}:
				case <-pipelineCtx.Done():
					sendErr(pipelineCtx.Err())
					return
				}
			}
		}()
	}
	go func() {
		pgWG.Wait()
		close(pgOut)
		close(pgDurations)
	}()

	var redisWG sync.WaitGroup
	for workerID := 0; workerID < redisWorkers; workerID++ {
		redisWG.Add(1)
		go func() {
			defer redisWG.Done()
			for first := range pgOut {
				if err := waitForServiceBackedPipelineLag(pipelineCtx, &redisPublished, &hermesApplied, maxLag); err != nil {
					sendErr(err)
					return
				}
				group := serviceBackedPipelineDrainBatches(pgOut, first, redisGroupBatches)
				payloads := make([][]byte, 0, len(group))
				units := 0
				for _, item := range group {
					envelope, err := serviceBackedLoadHermesBatchEnvelope(orgID, item.Start, item.Count)
					if err != nil {
						sendErr(err)
						return
					}
					raw, err := envelope.ToBinary()
					if err != nil {
						sendErr(err)
						return
					}
					payloads = append(payloads, raw)
					units += item.Count
				}
				opStart := time.Now()
				_, errs := streamBatch.XAddManyField(pipelineCtx, stream, "envelope", payloads)
				if err := firstSliceError(errs); err != nil {
					sendErr(err)
					return
				}
				elapsed := time.Since(opStart)
				redisPublished.Add(int64(units))
				serviceBackedLoadObserveMax(&maxRedisLag, redisPublished.Load()-hermesApplied.Load())
				redisDurations <- serviceBackedLoadDuration{Batch: elapsed, PerUnit: elapsed / time.Duration(max(units, 1))}
			}
		}()
	}
	redisDone := make(chan struct{})
	go func() {
		redisWG.Wait()
		close(redisDone)
		close(redisDurations)
	}()

	tailerDone := make(chan struct{})
	go func() {
		defer close(tailerDone)
		idleAfterRelay := time.Time{}
		for hermesApplied.Load() < int64(total) {
			if err := pipelineCtx.Err(); err != nil {
				sendErr(err)
				return
			}
			opStart := time.Now()
			result, err := tailer.PollOnce(pipelineCtx)
			if err != nil {
				sendErr(err)
				return
			}
			if result.Read == 0 {
				select {
				case <-redisDone:
					if idleAfterRelay.IsZero() {
						idleAfterRelay = time.Now()
					}
					if time.Since(idleAfterRelay) > 5*time.Second {
						sendErr(fmt.Errorf("hermes applied %d records after redis relay drained, want %d", hermesApplied.Load(), total))
						return
					}
				default:
				}
				time.Sleep(time.Millisecond)
				continue
			}
			idleAfterRelay = time.Time{}
			elapsed := time.Since(opStart)
			applied := result.Apply.Applied
			if applied <= 0 {
				continue
			}
			hermesApplied.Add(int64(applied))
			serviceBackedLoadObserveMax(&maxHermesLag, pgWritten.Load()-hermesApplied.Load())
			tailDurations <- serviceBackedLoadDuration{Batch: elapsed, PerUnit: elapsed / time.Duration(applied)}
		}
	}()

	go func() {
		defer close(readDone)
		runServiceBackedPipelineReads(pipelineCtx, hotplane, projection, orgID, readOps, readWorkers, readStop, readDurations, &readCompleted, sendErr)
	}()

	var runErr error
	tailerCompleted := false
	select {
	case <-tailerDone:
		tailerCompleted = true
	case err := <-errCh:
		runErr = err
		cancel()
	}
	<-redisDone
	if !tailerCompleted {
		<-tailerDone
	}
	close(readStop)
	<-readDone
	close(tailDurations)
	close(readDurations)
	cancel()
	if runErr == nil {
		select {
		case err := <-errCh:
			runErr = err
		default:
		}
	}
	if runErr != nil && runErr != context.Canceled {
		return serviceBackedLoadStats{}, "", hermes.Stats{}, store.Stats(), runErr
	}
	elapsed := time.Since(started)
	if hermesApplied.Load() != int64(total) {
		return serviceBackedLoadStats{}, "", hermes.Stats{}, store.Stats(), fmt.Errorf("hermes applied %d records, want %d", hermesApplied.Load(), total)
	}
	hermesStats, err := hotplane.Stats(projection)
	if err != nil {
		return serviceBackedLoadStats{}, "", hermes.Stats{}, store.Stats(), err
	}
	if hermesStats.Records != total {
		return serviceBackedLoadStats{}, "", hermesStats, store.Stats(), fmt.Errorf("hermes records=%d, want %d", hermesStats.Records, total)
	}
	pgStats := summarizeServiceBackedLoadDurationsWithMeta(pgDurations, int(pgWritten.Load()), batchSize, pgWorkers)
	redisStats := summarizeServiceBackedLoadDurationsWithMeta(redisDurations, int(redisPublished.Load()), batchSize*max(redisGroupBatches, 1), redisWorkers)
	tailStats := summarizeServiceBackedLoadDurationsWithMeta(tailDurations, int(hermesApplied.Load()), tailerBatch*batchSize, 1)
	readStats := summarizeServiceBackedLoadDurationsWithMeta(readDurations, int(readCompleted.Load()), 1, readWorkers)
	stats := serviceBackedLoadStatsFromSingle(total, elapsed)
	stats.BatchSize = batchSize
	stats.Batches = batches
	stats.Workers = pgWorkers + redisWorkers + readWorkers + 1
	stageNotes := fmt.Sprintf(
		"pg=%s redis=%s tail=%s reads=%s max_redis_lag=%d max_hermes_lag=%d pg_written=%d redis_published=%d hermes_applied=%d",
		serviceBackedLoadStageSummary(pgStats),
		serviceBackedLoadStageSummary(redisStats),
		serviceBackedLoadStageSummary(tailStats),
		serviceBackedLoadStageSummary(readStats),
		maxRedisLag.Load(),
		maxHermesLag.Load(),
		pgWritten.Load(),
		redisPublished.Load(),
		hermesApplied.Load(),
	)
	return stats, stageNotes, hermesStats, store.Stats(), nil
}

func runServiceBackedPipelineReads(
	ctx context.Context,
	hotplane *hermes.Store,
	projection string,
	orgID string,
	totalReads int,
	workers int,
	stop <-chan struct{},
	durations chan<- serviceBackedLoadDuration,
	completed *atomic.Int64,
	sendErr func(error),
) {
	if totalReads <= 0 || workers <= 0 {
		return
	}
	filter, ok := hermes.NewQueryFilter("bucket", "07")
	if !ok {
		sendErr(errors.New("bucket filter is not indexable"))
		return
	}
	query := hermes.QueryWithFilters(orgID, 0, filter)
	var next atomic.Int64
	var wg sync.WaitGroup
	for workerID := 0; workerID < workers; workerID++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				readIndex := int(next.Add(1)) - 1
				if readIndex >= totalReads {
					return
				}
				select {
				case <-stop:
					return
				default:
				}
				if err := ctx.Err(); err != nil {
					sendErr(err)
					return
				}
				start := time.Now()
				if _, err := hotplane.Count(ctx, projection, query, hermes.Fence{}); err != nil {
					sendErr(err)
					return
				}
				elapsed := time.Since(start)
				completed.Add(1)
				durations <- serviceBackedLoadDuration{Batch: elapsed, PerUnit: elapsed}
			}
		}()
	}
	wg.Wait()
}

func serviceBackedPipelineDrainBatches(
	ch <-chan serviceBackedPipelineBatch,
	first serviceBackedPipelineBatch,
	limit int,
) []serviceBackedPipelineBatch {
	if limit <= 1 {
		return []serviceBackedPipelineBatch{first}
	}
	out := make([]serviceBackedPipelineBatch, 0, limit)
	out = append(out, first)
	for len(out) < limit {
		select {
		case next, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, next)
		default:
			return out
		}
	}
	return out
}

func waitForServiceBackedPipelineLag(
	ctx context.Context,
	published *atomic.Int64,
	applied *atomic.Int64,
	maxLag int64,
) error {
	if maxLag <= 0 {
		return nil
	}
	for published.Load()-applied.Load() > maxLag {
		if err := ctx.Err(); err != nil {
			return err
		}
		time.Sleep(time.Millisecond)
	}
	return nil
}

func serviceBackedLoadObserveMax(target *atomic.Int64, value int64) {
	for {
		current := target.Load()
		if value <= current || target.CompareAndSwap(current, value) {
			return
		}
	}
}

func serviceBackedLoadStageSummary(stats serviceBackedLoadStats) string {
	if stats.TotalUnits <= 0 && stats.P99Batch == 0 && stats.P99Unit == 0 {
		return "samples:0"
	}
	return fmt.Sprintf(
		"units:%d batches:%d workers:%d p99_batch:%s p99_unit:%s",
		stats.TotalUnits,
		stats.Batches,
		stats.Workers,
		stats.P99Batch,
		stats.P99Unit,
	)
}

func runServiceBackedLoadWSRouteRegister(
	t *testing.T,
	ctx context.Context,
	env serviceEnv,
	recorder *serviceBackedLoadRecorder,
	step int,
	maxWorkers int,
) {
	t.Helper()
	const lane = "wsroute_register_redis"
	batchSize := serviceBackedLoadPositiveIntEnv(t, "SERVICE_BACKED_LOAD_RESEARCH_WS_REGISTER_BATCH", 256)
	before := serviceBackedLoadRuntimeSnapshot()
	setupStart := time.Now()
	client := openRedis(t, env)
	defer client.Close()
	router := wsrouting.NewRouter(
		client,
		uniqueName(env.prefix, "load-router"),
		wsrouting.WithTTL(serviceBackedLoadDurationEnv("SERVICE_BACKED_LOAD_RESEARCH_WS_TTL", 2*time.Minute)),
		wsrouting.WithRegistrationBatchSize(batchSize),
	)
	setup := time.Since(setupStart)
	workers := serviceBackedLoadWorkers(step, batchSize, maxWorkers)
	stats, err := runServiceBackedLoadBatches(ctx, step, batchSize, workers, func(ctx context.Context, _ int, start, count int) error {
		infos := make([]wsrouting.ConnectionInfo, 0, count)
		for i := 0; i < count; i++ {
			id := start + i
			infos = append(infos, wsrouting.ConnectionInfo{
				ConnectionID: fmt.Sprintf("svc-load-conn-%09d", id),
				DeviceID:     fmt.Sprintf("svc-load-device-%09d", id),
				UserID:       fmt.Sprintf("svc-load-user-%09d", id/10),
			})
		}
		return router.RegisterMany(ctx, infos)
	})
	if err != nil {
		t.Fatalf("%s failed: %v", lane, err)
	}
	recordServiceBackedLoadLane(t, recorder, serviceBackedLoadRow{
		Step:      step,
		Lane:      lane,
		BatchSize: batchSize,
		Workers:   workers,
		Setup:     setup,
		Stats:     stats,
		Before:    before,
		After:     serviceBackedLoadRuntimeSnapshot(),
		Notes:     "WebSocket route registration with live Redis coordination keys and register pub/sub notifications",
	})
}

func runServiceBackedLoadWSRouteBroadcast(
	t *testing.T,
	ctx context.Context,
	env serviceEnv,
	recorder *serviceBackedLoadRecorder,
	step int,
) {
	t.Helper()
	const lane = "wsroute_broadcast_after_register"
	const batchSize = 4096
	before := serviceBackedLoadRuntimeSnapshot()
	setupStart := time.Now()
	client := openRedis(t, env)
	defer client.Close()
	registerBatchSize := serviceBackedLoadPositiveIntEnv(t, "SERVICE_BACKED_LOAD_RESEARCH_WS_REGISTER_BATCH", 256)
	router := wsrouting.NewRouter(
		client,
		uniqueName(env.prefix, "load-router-broadcast"),
		wsrouting.WithTTL(serviceBackedLoadDurationEnv("SERVICE_BACKED_LOAD_RESEARCH_WS_TTL", 2*time.Minute)),
		wsrouting.WithRegistrationBatchSize(registerBatchSize),
	)
	for start := 0; start < step; start += registerBatchSize {
		count := min(registerBatchSize, step-start)
		infos := make([]wsrouting.ConnectionInfo, 0, count)
		for i := 0; i < count; i++ {
			id := start + i
			infos = append(infos, wsrouting.ConnectionInfo{
				ConnectionID: fmt.Sprintf("svc-load-broadcast-conn-%09d", id),
				DeviceID:     fmt.Sprintf("svc-load-broadcast-device-%09d", id),
				UserID:       fmt.Sprintf("svc-load-broadcast-user-%09d", id/10),
			})
		}
		if err := router.RegisterMany(ctx, infos); err != nil {
			t.Fatalf("%s setup register batch start=%d count=%d failed: %v", lane, start, count, err)
		}
	}
	setup := time.Since(setupStart)
	stats, err := runServiceBackedLoadBatches(ctx, step, batchSize, 1, func(ctx context.Context, _ int, _ int, _ int) error {
		count, err := router.ForEachTargetBatch(ctx, wsrouting.TargetedDelivery{TargetType: "broadcast"}, batchSize, func(ids []string) bool {
			return len(ids) > 0
		})
		if err != nil {
			return err
		}
		if count != step {
			return fmt.Errorf("broadcast count = %d, want %d", count, step)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%s failed: %v", lane, err)
	}
	recordServiceBackedLoadLane(t, recorder, serviceBackedLoadRow{
		Step:      step,
		Lane:      lane,
		BatchSize: batchSize,
		Workers:   1,
		Setup:     setup,
		Stats:     stats,
		Before:    before,
		After:     serviceBackedLoadRuntimeSnapshot(),
		Notes:     "Borrowed WebSocket broadcast target batches after live Redis-backed registration; measured path excludes socket writes",
	})
}

type serviceBackedLoadTask func(ctx context.Context, workerID int, start int, count int) error

func runServiceBackedLoadBatches(
	ctx context.Context,
	totalUnits int,
	batchSize int,
	workers int,
	task serviceBackedLoadTask,
) (serviceBackedLoadStats, error) {
	if totalUnits <= 0 {
		return serviceBackedLoadStats{}, fmt.Errorf("total units must be positive: %d", totalUnits)
	}
	if batchSize <= 0 {
		return serviceBackedLoadStats{}, fmt.Errorf("batch size must be positive: %d", batchSize)
	}
	if workers <= 0 {
		workers = 1
	}
	batches := int(math.Ceil(float64(totalUnits) / float64(batchSize)))
	durations := make(chan serviceBackedLoadDuration, batches)
	errCh := make(chan error, workers)
	var next atomic.Int64
	var completed atomic.Int64
	var wg sync.WaitGroup
	started := time.Now()
	for workerID := 0; workerID < workers; workerID++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for {
				index := int(next.Add(1)) - 1
				start := index * batchSize
				if start >= totalUnits {
					return
				}
				count := min(batchSize, totalUnits-start)
				if err := ctx.Err(); err != nil {
					errCh <- err
					return
				}
				opStart := time.Now()
				if err := task(ctx, workerID, start, count); err != nil {
					errCh <- err
					return
				}
				elapsed := time.Since(opStart)
				completed.Add(int64(count))
				durations <- serviceBackedLoadDuration{Batch: elapsed, PerUnit: elapsed / time.Duration(count)}
			}
		}(workerID)
	}
	wg.Wait()
	close(durations)
	close(errCh)
	stats := summarizeServiceBackedLoadDurations(durations)
	stats.TotalUnits = int(completed.Load())
	stats.BatchSize = batchSize
	stats.Batches = batches
	stats.Workers = workers
	stats.Elapsed = time.Since(started)
	if err := firstError(errCh); err != nil {
		return stats, err
	}
	if stats.TotalUnits != totalUnits {
		return stats, fmt.Errorf("completed %d units, want %d", stats.TotalUnits, totalUnits)
	}
	return stats, nil
}

func runServiceBackedLoadStreamDrain(
	ctx context.Context,
	client rediskit.Client,
	stream string,
	group string,
	consumer string,
	totalMessages int,
	batchSize int,
) (serviceBackedLoadStats, error) {
	durations := make(chan serviceBackedLoadDuration, max(totalMessages/batchSize+1, 1))
	started := time.Now()
	drained := 0
	for drained < totalMessages {
		start := time.Now()
		messages, err := client.XReadGroup(ctx, stream, group, consumer, int64(batchSize))
		if err != nil {
			close(durations)
			return summarizeServiceBackedLoadDurations(durations), err
		}
		if len(messages) == 0 {
			break
		}
		if err := client.XAck(ctx, stream, group, streamIDs(messages)...); err != nil {
			close(durations)
			return summarizeServiceBackedLoadDurations(durations), err
		}
		elapsed := time.Since(start)
		drained += len(messages)
		durations <- serviceBackedLoadDuration{Batch: elapsed, PerUnit: elapsed / time.Duration(len(messages))}
	}
	close(durations)
	stats := summarizeServiceBackedLoadDurations(durations)
	stats.TotalUnits = drained
	stats.BatchSize = batchSize
	stats.Batches = int(math.Ceil(float64(max(drained, 1)) / float64(batchSize)))
	stats.Workers = 1
	stats.Elapsed = time.Since(started)
	if drained != totalMessages {
		return stats, fmt.Errorf("drained %d stream messages, want %d", drained, totalMessages)
	}
	return stats, nil
}

func runServiceBackedLoadHermesTailer(
	ctx context.Context,
	tailer *hermes.EnvelopeTailer,
	totalMessages int,
) (serviceBackedLoadStats, error) {
	durations := make(chan serviceBackedLoadDuration, max(totalMessages/64+1, 1))
	started := time.Now()
	applied := 0
	for applied < totalMessages {
		start := time.Now()
		result, err := tailer.PollOnce(ctx)
		if err != nil {
			close(durations)
			return summarizeServiceBackedLoadDurations(durations), err
		}
		if result.Read == 0 {
			break
		}
		elapsed := time.Since(start)
		applied += result.Apply.Applied
		durations <- serviceBackedLoadDuration{Batch: elapsed, PerUnit: elapsed / time.Duration(max(result.Apply.Applied, 1))}
	}
	close(durations)
	stats := summarizeServiceBackedLoadDurations(durations)
	stats.TotalUnits = applied
	stats.BatchSize = 64
	stats.Batches = int(math.Ceil(float64(max(applied, 1)) / 64.0))
	stats.Workers = 1
	stats.Elapsed = time.Since(started)
	if applied != totalMessages {
		return stats, fmt.Errorf("applied %d hermes stream messages, want %d", applied, totalMessages)
	}
	return stats, nil
}

func summarizeServiceBackedLoadDurations(values <-chan serviceBackedLoadDuration) serviceBackedLoadStats {
	batches := make([]time.Duration, 0)
	perUnit := make([]time.Duration, 0)
	for value := range values {
		batches = append(batches, value.Batch)
		perUnit = append(perUnit, value.PerUnit)
	}
	if len(batches) == 0 {
		return serviceBackedLoadStats{}
	}
	slices.SortFunc(batches, func(a, b time.Duration) int { return cmpDuration(a, b) })
	slices.SortFunc(perUnit, func(a, b time.Duration) int { return cmpDuration(a, b) })
	return serviceBackedLoadStats{
		P50Batch: percentileFromSorted(batches, 0.50),
		P95Batch: percentileFromSorted(batches, 0.95),
		P99Batch: percentileFromSorted(batches, 0.99),
		MaxBatch: batches[len(batches)-1],
		P50Unit:  percentileFromSorted(perUnit, 0.50),
		P95Unit:  percentileFromSorted(perUnit, 0.95),
		P99Unit:  percentileFromSorted(perUnit, 0.99),
		MaxUnit:  perUnit[len(perUnit)-1],
	}
}

func summarizeServiceBackedLoadDurationsWithMeta(
	values <-chan serviceBackedLoadDuration,
	totalUnits int,
	batchSize int,
	workers int,
) serviceBackedLoadStats {
	stats := summarizeServiceBackedLoadDurations(values)
	stats.TotalUnits = totalUnits
	stats.BatchSize = batchSize
	stats.Batches = int(math.Ceil(float64(max(totalUnits, 1)) / float64(max(batchSize, 1))))
	stats.Workers = workers
	return stats
}

func serviceBackedLoadStatsFromSingle(units int, elapsed time.Duration) serviceBackedLoadStats {
	return serviceBackedLoadStats{
		TotalUnits: units,
		BatchSize:  units,
		Batches:    1,
		Workers:    1,
		Elapsed:    elapsed,
		P50Batch:   elapsed,
		P95Batch:   elapsed,
		P99Batch:   elapsed,
		MaxBatch:   elapsed,
		P50Unit:    elapsed / time.Duration(max(units, 1)),
		P95Unit:    elapsed / time.Duration(max(units, 1)),
		P99Unit:    elapsed / time.Duration(max(units, 1)),
		MaxUnit:    elapsed / time.Duration(max(units, 1)),
	}
}

type serviceBackedLoadDuration struct {
	Batch   time.Duration
	PerUnit time.Duration
}

type serviceBackedLoadStats struct {
	TotalUnits int
	BatchSize  int
	Batches    int
	Workers    int
	Elapsed    time.Duration
	P50Batch   time.Duration
	P95Batch   time.Duration
	P99Batch   time.Duration
	MaxBatch   time.Duration
	P50Unit    time.Duration
	P95Unit    time.Duration
	P99Unit    time.Duration
	MaxUnit    time.Duration
}

type serviceBackedLoadRecorder struct {
	t    testing.TB
	file *os.File
}

func newServiceBackedLoadRecorder(t testing.TB) *serviceBackedLoadRecorder {
	t.Helper()
	recorder := &serviceBackedLoadRecorder{t: t}
	output := strings.TrimSpace(os.Getenv("SERVICE_BACKED_LOAD_RESEARCH_OUTPUT"))
	if output == "" {
		return recorder
	}
	file, err := os.Create(output)
	if err != nil {
		t.Fatalf("create service-backed load research output %q: %v", output, err)
	}
	recorder.file = file
	fmt.Fprintln(file, "step\tlane\ttotal_units\tbatch_size\tbatches\tworkers\tsetup_ns\ttotal_ns\tp50_batch_ns\tp95_batch_ns\tp99_batch_ns\tmax_batch_ns\tp50_unit_ns\tp95_unit_ns\tp99_unit_ns\tmax_unit_ns\tthroughput_units_sec\theap_alloc_before\theap_alloc_after\theap_alloc_delta\theap_sys_after\tgoroutines_before\tgoroutines_after\tpg_max_conns\tpg_active_conns\tpg_acquire_count\thermes_records\thermes_approx_bytes\thermes_epoch\thermes_watermark\thermes_rejected_applies\thermes_index_compactions\tnotes")
	return recorder
}

func (r *serviceBackedLoadRecorder) close() {
	if r.file != nil {
		if err := r.file.Close(); err != nil {
			r.t.Fatalf("close service-backed load output: %v", err)
		}
	}
}

type serviceBackedLoadRow struct {
	Step        int
	Lane        string
	TotalUnits  int
	BatchSize   int
	Workers     int
	Setup       time.Duration
	Stats       serviceBackedLoadStats
	Before      serviceBackedLoadSnapshot
	After       serviceBackedLoadSnapshot
	DBStats     database.StoreStats
	HermesStats hermes.Stats
	Notes       string
}

func recordServiceBackedLoadLane(t testing.TB, recorder *serviceBackedLoadRecorder, row serviceBackedLoadRow) {
	t.Helper()
	if row.TotalUnits <= 0 {
		row.TotalUnits = row.Step
	}
	if row.BatchSize <= 0 {
		row.BatchSize = row.Stats.BatchSize
	}
	if row.Workers <= 0 {
		row.Workers = row.Stats.Workers
	}
	throughput := 0.0
	if row.Stats.Elapsed > 0 {
		throughput = float64(row.Stats.TotalUnits) / row.Stats.Elapsed.Seconds()
	}
	heapDelta := int64(row.After.HeapAlloc) - int64(row.Before.HeapAlloc)
	t.Logf(
		"SERVICE_BACKED_LOAD step=%d lane=%s units=%d batch=%d workers=%d setup=%s elapsed=%s p50_batch=%s p99_batch=%s p50_unit=%s p99_unit=%s throughput=%.2f/s heap_delta=%d heap_after=%d pg_max=%d hermes_records=%d hermes_bytes=%d notes=%q",
		row.Step,
		row.Lane,
		row.Stats.TotalUnits,
		row.BatchSize,
		row.Workers,
		row.Setup,
		row.Stats.Elapsed,
		row.Stats.P50Batch,
		row.Stats.P99Batch,
		row.Stats.P50Unit,
		row.Stats.P99Unit,
		throughput,
		heapDelta,
		row.After.HeapAlloc,
		row.DBStats.MaxConns,
		row.HermesStats.Records,
		row.HermesStats.ApproxBytes,
		row.Notes,
	)
	if recorder.file == nil {
		return
	}
	fmt.Fprintf(
		recorder.file,
		"%d\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%.2f\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n",
		row.Step,
		row.Lane,
		row.Stats.TotalUnits,
		row.BatchSize,
		row.Stats.Batches,
		row.Workers,
		row.Setup.Nanoseconds(),
		row.Stats.Elapsed.Nanoseconds(),
		row.Stats.P50Batch.Nanoseconds(),
		row.Stats.P95Batch.Nanoseconds(),
		row.Stats.P99Batch.Nanoseconds(),
		row.Stats.MaxBatch.Nanoseconds(),
		row.Stats.P50Unit.Nanoseconds(),
		row.Stats.P95Unit.Nanoseconds(),
		row.Stats.P99Unit.Nanoseconds(),
		row.Stats.MaxUnit.Nanoseconds(),
		throughput,
		row.Before.HeapAlloc,
		row.After.HeapAlloc,
		heapDelta,
		row.After.HeapSys,
		row.Before.Goroutines,
		row.After.Goroutines,
		row.DBStats.MaxConns,
		row.DBStats.ActiveConns,
		row.DBStats.AcquireCount,
		row.HermesStats.Records,
		row.HermesStats.ApproxBytes,
		row.HermesStats.Epoch,
		row.HermesStats.SourceWatermark,
		row.HermesStats.RejectedApplies,
		row.HermesStats.IndexCompactions,
		strings.ReplaceAll(row.Notes, "\t", " "),
	)
}

type serviceBackedLoadSnapshot struct {
	HeapAlloc  uint64
	HeapSys    uint64
	Goroutines int
}

func serviceBackedLoadRuntimeSnapshot() serviceBackedLoadSnapshot {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return serviceBackedLoadSnapshot{
		HeapAlloc:  stats.HeapAlloc,
		HeapSys:    stats.HeapSys,
		Goroutines: runtime.NumGoroutine(),
	}
}

func newServiceBackedLoadHermesStore(tb testing.TB, projection string, records int) *hermes.Store {
	tb.Helper()
	maxBytes := max(int64(records)*512, int64(64<<20))
	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name:          projection,
		Domain:        "signals",
		Collection:    "pressure",
		IndexedFields: []string{"bucket", "source"},
		MaxRecords:    records + 4096,
		MaxBytes:      maxBytes,
	})
	if err != nil {
		tb.Fatalf("NewStore(%s) error = %v", projection, err)
	}
	return store
}

func serviceBackedLoadPoolOptions(t *testing.T, maxWorkers int) database.PoolOptions {
	t.Helper()
	cfg := scaling.AutoTune()
	postgresMaxConnections := serviceBackedLoadPostgresMaxConnections(t)
	reservedConnections := serviceBackedLoadReservedDBConnections(t, postgresMaxConnections)
	maxConns := serviceBackedLoadDBConnectionBudget(
		max(cfg.DBMaxConnections, max(maxWorkers, 1)),
		postgresMaxConnections,
		reservedConnections,
	)
	if raw := strings.TrimSpace(os.Getenv("SERVICE_BACKED_LOAD_RESEARCH_DB_MAX_CONNS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			maxConns = serviceBackedLoadDBConnectionBudget(parsed, postgresMaxConnections, reservedConnections)
		}
	}
	return database.PoolOptions{
		MaxConns:                 maxConns,
		MinConns:                 min(maxConns, 4),
		HealthCheckPeriod:        time.Second,
		ConnectTimeout:           2 * time.Second,
		QueryTimeout:             serviceBackedLoadDurationEnv("SERVICE_BACKED_LOAD_RESEARCH_DB_QUERY_TIMEOUT", 10*time.Second),
		AcquireTimeout:           serviceBackedLoadDurationEnv("SERVICE_BACKED_LOAD_RESEARCH_DB_ACQUIRE_TIMEOUT", 2*time.Second),
		StatementCacheCapacity:   256,
		DescriptionCacheCapacity: 64,
	}
}

func serviceBackedLoadHermesRebuildPoolOptions(t *testing.T, maxWorkers int) database.PoolOptions {
	t.Helper()
	opts := serviceBackedLoadPoolOptions(t, maxWorkers)
	opts.QueryTimeout = serviceBackedLoadDurationEnv(
		"SERVICE_BACKED_LOAD_RESEARCH_HERMES_REBUILD_QUERY_TIMEOUT",
		max(opts.QueryTimeout, 30*time.Second),
	)
	opts.AcquireTimeout = serviceBackedLoadDurationEnv(
		"SERVICE_BACKED_LOAD_RESEARCH_HERMES_REBUILD_ACQUIRE_TIMEOUT",
		max(opts.AcquireTimeout, 2*time.Second),
	)
	if opts.LockTimeout <= 0 || opts.LockTimeout > opts.QueryTimeout {
		opts.LockTimeout = opts.QueryTimeout / 2
	}
	if opts.LockTimeout <= 0 {
		opts.LockTimeout = time.Second
	}
	return opts
}

func serviceBackedLoadPostgresMaxConnections(t *testing.T) int {
	t.Helper()
	return serviceBackedLoadPositiveIntEnv(t, "SERVICE_BACKED_POSTGRES_MAX_CONNECTIONS", 120)
}

func serviceBackedLoadReservedDBConnections(t *testing.T, postgresMaxConnections int) int {
	t.Helper()
	return serviceBackedLoadPositiveIntEnv(
		t,
		"SERVICE_BACKED_LOAD_RESEARCH_DB_RESERVED_CONNS",
		serviceBackedLoadDefaultDBReservedConnections(postgresMaxConnections),
	)
}

func serviceBackedLoadDefaultDBReservedConnections(postgresMaxConnections int) int {
	if postgresMaxConnections <= 0 {
		postgresMaxConnections = 120
	}
	return min(max(4, postgresMaxConnections/5), max(postgresMaxConnections-1, 1))
}

func serviceBackedLoadDBConnectionBudget(requested, postgresMaxConnections, reservedConnections int) int {
	if postgresMaxConnections <= 0 {
		postgresMaxConnections = 120
	}
	if reservedConnections < 0 {
		reservedConnections = 0
	}
	maxUsable := postgresMaxConnections - reservedConnections
	if maxUsable <= 0 {
		maxUsable = 1
	}
	if requested <= 0 {
		requested = maxUsable
	}
	return min(requested, maxUsable)
}

func serviceBackedLoadDefaultMaxWorkers(cfg scaling.Config, postgresMaxConnections, reservedConnections int) int {
	requested := max(cfg.DispatchMaxConcurrent, cfg.DBMaxConnections)
	return serviceBackedLoadDBConnectionBudget(requested, postgresMaxConnections, reservedConnections)
}

func serviceBackedLoadDefaultPipelineDBWorkersForCores(cores int) int {
	if cores <= 1 {
		return 1
	}
	return min(max(cores-2, 1), 6)
}

func serviceBackedLoadPipelineDBWorkers(t *testing.T, totalUnits, batchSize, maxWorkers int, opts database.PoolOptions) int {
	t.Helper()
	defaultWorkers := serviceBackedLoadDefaultPipelineDBWorkersForCores(scaling.AutoTune().CPUCount)
	requested := serviceBackedLoadPositiveIntEnv(t, "SERVICE_BACKED_LOAD_RESEARCH_PIPELINE_DB_WORKERS", defaultWorkers)
	workerLimit := min(maxWorkers, max(opts.MaxConns, 1))
	return serviceBackedLoadWorkers(totalUnits, batchSize, min(requested, workerLimit))
}

func serviceBackedLoadDBWorkers(totalUnits, batchSize, maxWorkers int, opts database.PoolOptions) int {
	poolWorkers := max(opts.MaxConns, 1)
	return serviceBackedLoadWorkers(totalUnits, batchSize, min(maxWorkers, poolWorkers))
}

func serviceBackedLoadWorkers(totalUnits, batchSize, maxWorkers int) int {
	if totalUnits <= 0 {
		return 1
	}
	batches := int(math.Ceil(float64(totalUnits) / float64(max(batchSize, 1))))
	return min(max(batches, 1), max(maxWorkers, 1))
}

func queueHermesStateBatch(batch *pgx.Batch, orgID string, startID, count int) {
	const query = `
		INSERT INTO governance_state_records (domain, collection_name, organization_id, record_id, data)
		VALUES ('signals', 'pressure', $1, $2, $3::jsonb)
		ON CONFLICT (domain, collection_name, organization_id, record_id)
		DO UPDATE SET data = EXCLUDED.data, updated_at = NOW()
	`
	for i := 0; i < count; i++ {
		id := startID + i
		payload := fmt.Sprintf(`{"bucket":"%02d","source":"postgres","ordinal":%d}`, id%16, id)
		batch.Queue(query, orgID, fmt.Sprintf("record-%09d", id), payload)
	}
}

func serviceBackedLoadRedisValues(prefix string, startID, count int) (rediskit.Values, []string) {
	values := make(rediskit.Values, 0, count)
	keys := make([]string, 0, count)
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("%s:key:%09d", prefix, startID+i)
		keys = append(keys, key)
		values = append(values, rediskit.Field(key, []byte("foundation")))
	}
	return values, keys
}

func serviceBackedLoadStreamEntries(startID, count int) []rediskit.Values {
	entries := make([]rediskit.Values, 0, count)
	for i := 0; i < count; i++ {
		id := startID + i
		entries = append(entries, serviceRedisValues(map[string]any{
			"record_id": fmt.Sprintf("record-%09d", id),
			"bucket":    fmt.Sprintf("%02d", id%16),
		}))
	}
	return entries
}

func serviceBackedLoadHermesRecords(orgID string, startID, count int) []database.DomainRecord {
	records := make([]database.DomainRecord, 0, count)
	for i := 0; i < count; i++ {
		id := startID + i
		records = append(records, database.DomainRecord{
			Domain:         "signals",
			Collection:     "pressure",
			OrganizationID: orgID,
			RecordID:       fmt.Sprintf("record-%09d", id),
			Data:           serviceRecordData(map[string]any{"bucket": fmt.Sprintf("%02d", id%16), "source": "mixed"}),
		})
	}
	return records
}

func serviceBackedLoadSeedRedisStream(
	ctx context.Context,
	batch rediskit.StreamBatchClient,
	stream string,
	total int,
	batchSize int,
) error {
	_, err := runServiceBackedLoadBatches(ctx, total, batchSize, 1, func(ctx context.Context, _ int, start, count int) error {
		_, errs := batch.XAddMany(ctx, stream, serviceBackedLoadStreamEntries(start, count))
		return firstSliceError(errs)
	})
	return err
}

func serviceBackedLoadSeedHermesStream(
	ctx context.Context,
	batch rediskit.StreamBatchClient,
	stream string,
	orgID string,
	total int,
	batchSize int,
) error {
	_, err := runServiceBackedLoadBatches(ctx, total, batchSize, 1, func(ctx context.Context, _ int, start, count int) error {
		entries := make([]rediskit.Values, 0, count)
		for i := 0; i < count; i++ {
			raw, err := serviceBackedLoadHermesEnvelope(orgID, start+i).ToBinary()
			if err != nil {
				return err
			}
			entries = append(entries, serviceRedisValues(map[string]any{"envelope": raw}))
		}
		_, errs := batch.XAddMany(ctx, stream, entries)
		return firstSliceError(errs)
	})
	return err
}

func serviceBackedLoadHermesEnvelope(orgID string, id int) events.Envelope {
	envelope, err := hermes.NewProjectionEnvelope([]*foundationpb.RecordMutation{{
		Operation:      foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT,
		SourceId:       fmt.Sprintf("service-backed-load:%09d", id),
		Version:        uint64(id + 1),
		Domain:         "signals",
		Collection:     "pressure",
		OrganizationId: orgID,
		RecordId:       fmt.Sprintf("stream-record-%09d", id),
		CorrelationId:  fmt.Sprintf("corr-service-backed-load-%09d", id),
		Fields: []*foundationpb.FieldValue{
			projectionField("bucket", fmt.Sprintf("%02d", id%16)),
			projectionField("source", "redis"),
		},
	}}, fmt.Sprintf("corr-service-backed-load-%09d", id))
	if err != nil {
		panic(err)
	}
	return envelope
}

func serviceBackedLoadHermesBatchEnvelope(orgID string, startID, count int) (events.Envelope, error) {
	mutations := make([]*foundationpb.RecordMutation, 0, count)
	for i := 0; i < count; i++ {
		id := startID + i
		mutations = append(mutations, &foundationpb.RecordMutation{
			Operation:      foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT,
			SourceId:       fmt.Sprintf("service-backed-load-pipeline-%d:%09d", startID, id),
			Version:        uint64(id + 1),
			Domain:         "signals",
			Collection:     "pressure",
			OrganizationId: orgID,
			RecordId:       fmt.Sprintf("record-%09d", id),
			CorrelationId:  fmt.Sprintf("corr-service-backed-load-pipeline-%09d", id),
			Fields: []*foundationpb.FieldValue{
				projectionField("bucket", fmt.Sprintf("%02d", id%16)),
				projectionField("source", "pipeline"),
				projectionField("ordinal", int64(id)),
			},
		})
	}
	return hermes.NewProjectionEnvelope(mutations, fmt.Sprintf("corr-service-backed-load-pipeline-%09d-%d", startID, count))
}

func requireRedisStreamBatch(tb testing.TB, client rediskit.Client) rediskit.StreamBatchClient {
	tb.Helper()
	batch, ok := client.(rediskit.StreamBatchClient)
	if !ok {
		tb.Fatalf("redis client %T does not implement StreamBatchClient", client)
	}
	return batch
}

func firstSliceError(errs []error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func cmpDuration(a, b time.Duration) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func serviceBackedLoadSteps(t testing.TB) []int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("SERVICE_BACKED_LOAD_RESEARCH_STEPS"))
	if raw == "" {
		raw = defaultServiceBackedLoadResearchSteps
	}
	steps := make([]int, 0, 8)
	for _, part := range strings.Split(raw, ",") {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		step, err := strconv.Atoi(value)
		if err != nil || step <= 0 {
			t.Fatalf("invalid SERVICE_BACKED_LOAD_RESEARCH_STEPS value %q", value)
		}
		steps = append(steps, step)
	}
	if len(steps) == 0 {
		t.Fatal("SERVICE_BACKED_LOAD_RESEARCH_STEPS did not contain any positive steps")
	}
	slices.Sort(steps)
	maxStep := serviceBackedLoadPositiveIntEnv(t, "SERVICE_BACKED_LOAD_RESEARCH_MAX_STEP", 1_000_000)
	filtered := steps[:0]
	for _, step := range steps {
		if step <= maxStep {
			filtered = append(filtered, step)
		}
	}
	if len(filtered) == 0 {
		t.Fatalf("all SERVICE_BACKED_LOAD_RESEARCH_STEPS exceed SERVICE_BACKED_LOAD_RESEARCH_MAX_STEP=%d", maxStep)
	}
	return filtered
}

func serviceBackedLoadLaneSet() map[string]bool {
	raw := strings.TrimSpace(os.Getenv("SERVICE_BACKED_LOAD_RESEARCH_LANES"))
	if raw == "" {
		raw = defaultServiceBackedLoadResearchLanes
	}
	lanes := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		lane := strings.TrimSpace(part)
		if lane != "" {
			lanes[lane] = true
		}
	}
	return lanes
}

func sortedServiceBackedLoadLanes(lanes map[string]bool) []string {
	out := make([]string, 0, len(lanes))
	for lane := range lanes {
		out = append(out, lane)
	}
	slices.Sort(out)
	return out
}

func runIfServiceBackedLoadLane(t testing.TB, lanes map[string]bool, lane string, fn func()) {
	t.Helper()
	if lanes["all"] || lanes[lane] {
		fn()
	}
}

func serviceBackedLoadPositiveIntEnv(t testing.TB, name string, fallback int) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		t.Fatalf("%s must be a positive integer, got %q", name, raw)
	}
	return value
}

func serviceBackedLoadDurationEnv(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
