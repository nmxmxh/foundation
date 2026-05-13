//go:build servicebacked

package servicebacked

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/contracttest"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/observability"
	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/worker"
	goredis "github.com/redis/go-redis/v9"
)

type serviceEnv struct {
	databaseURL string
	redisURL    string
	prefix      string
}

type redisContractNames struct {
	key        string
	ttlKey     string
	counterKey string
	lockKey    string
	stream     string
	channel    string
	topic      string
	hll        string
	group      string
}

type latencySummary struct {
	P95 time.Duration
	P99 time.Duration
	Max time.Duration
}

func TestServiceBackedRedisContracts(t *testing.T) {
	observability.Default().Reset()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	env := requireServiceEnv(t)
	client := openRedis(t, env)
	defer client.Close()

	names := newRedisContractNames(env.prefix)
	cleanupRedisKeys(t, ctx, client, names.key, names.ttlKey, names.counterKey, names.lockKey, names.stream, names.hll)

	assertRedisKVAndTTL(t, ctx, client, names)
	assertRedisCountersAndLocks(t, ctx, client, names)
	assertRedisPubSub(t, ctx, client, names)
	assertRedisStreams(t, ctx, client, names)
	assertRedisHLL(t, ctx, client, names)
	assertSnapshotHasCount(t, "redis", "set|success")
}

func newRedisContractNames(prefix string) redisContractNames {
	return redisContractNames{
		key:        uniqueName(prefix, "kv"),
		ttlKey:     uniqueName(prefix, "ttl"),
		counterKey: uniqueName(prefix, "counter"),
		lockKey:    uniqueName(prefix, "lock"),
		stream:     uniqueName(prefix, "stream"),
		channel:    uniqueName(prefix, "channel"),
		topic:      uniqueName(prefix, "topic"),
		hll:        uniqueName(prefix, "hll"),
		group:      uniqueName(prefix, "group"),
	}
}

func assertRedisKVAndTTL(t *testing.T, ctx context.Context, client rediskit.Client, names redisContractNames) {
	t.Helper()
	if err := client.Set(ctx, names.key, []byte("foundation"), time.Minute); err != nil {
		t.Fatalf("redis set failed: %v", err)
	}
	got, err := client.Get(ctx, names.key)
	if err != nil {
		t.Fatalf("redis get failed: %v", err)
	}
	if !bytes.Equal(got, []byte("foundation")) {
		t.Fatalf("redis get = %q, want foundation", string(got))
	}
	got[0] = 'x'
	gotAgain, err := client.Get(ctx, names.key)
	if err != nil {
		t.Fatalf("redis get after caller mutation failed: %v", err)
	}
	if !bytes.Equal(gotAgain, []byte("foundation")) {
		t.Fatalf("redis get leaked caller mutation: %q", string(gotAgain))
	}
	if err := client.Set(ctx, names.ttlKey, "gone", 50*time.Millisecond); err != nil {
		t.Fatalf("redis ttl set failed: %v", err)
	}
	waitRedisMissing(t, ctx, client, names.ttlKey)
}

func assertRedisCountersAndLocks(t *testing.T, ctx context.Context, client rediskit.Client, names redisContractNames) {
	t.Helper()
	next, err := client.Incr(ctx, names.counterKey)
	if err != nil || next != 1 {
		t.Fatalf("redis incr = %d, %v; want 1, nil", next, err)
	}
	if ok, err := client.Expire(ctx, names.counterKey, time.Second); err != nil || !ok {
		t.Fatalf("redis expire = %v, %v; want true, nil", ok, err)
	}

	token, err := client.Lock(ctx, names.lockKey, time.Second)
	if err != nil {
		t.Fatalf("redis lock failed: %v", err)
	}
	if _, err := client.Lock(ctx, names.lockKey, time.Second); err == nil {
		t.Fatalf("redis lock contention succeeded unexpectedly")
	}
	if ok, err := client.Unlock(ctx, names.lockKey, "wrong-token"); err != nil || ok {
		t.Fatalf("redis unlock wrong token = %v, %v; want false, nil", ok, err)
	}
	if ok, err := client.Unlock(ctx, names.lockKey, token); err != nil || !ok {
		t.Fatalf("redis unlock = %v, %v; want true, nil", ok, err)
	}
}

func assertRedisPubSub(t *testing.T, ctx context.Context, client rediskit.Client, names redisContractNames) {
	t.Helper()
	exact, exactCancel, err := client.Subscribe(ctx, names.channel)
	if err != nil {
		t.Fatalf("redis subscribe failed: %v", err)
	}
	defer exactCancel()
	patterns, patternCancel, err := client.PSubscribe(ctx, names.topic+":*")
	if err != nil {
		t.Fatalf("redis psubscribe failed: %v", err)
	}
	defer patternCancel()
	if err := client.Publish(ctx, names.channel, []byte("exact")); err != nil {
		t.Fatalf("redis publish exact failed: %v", err)
	}
	if err := client.Publish(ctx, names.topic+":fanout", []byte("pattern")); err != nil {
		t.Fatalf("redis publish pattern failed: %v", err)
	}
	waitBytes(t, exact, []byte("exact"))
	waitBytes(t, patterns[0], []byte("pattern"))
}

func assertRedisStreams(t *testing.T, ctx context.Context, client rediskit.Client, names redisContractNames) {
	t.Helper()
	id, err := client.XAdd(ctx, names.stream, map[string]interface{}{"kind": "requested", "record_id": "r1"})
	if err != nil || id == "" {
		t.Fatalf("redis xadd = %q, %v; want id, nil", id, err)
	}
	messages, err := client.XReadGroup(ctx, names.stream, names.group, "consumer-a", 10)
	if err != nil {
		t.Fatalf("redis xreadgroup auto-create failed: %v", err)
	}
	if len(messages) != 1 || messages[0].ID != id {
		t.Fatalf("redis xreadgroup got %#v, want id %q", messages, id)
	}
	if err := client.XAck(ctx, names.stream, names.group, id); err != nil {
		t.Fatalf("redis xack failed: %v", err)
	}
}

func assertRedisHLL(t *testing.T, ctx context.Context, client rediskit.Client, names redisContractNames) {
	t.Helper()
	if added, err := client.PFAdd(ctx, names.hll, "a", "b", "b"); err != nil || added == 0 {
		t.Fatalf("redis pfadd = %d, %v; want non-zero, nil", added, err)
	}
	cardinality, err := client.PFCount(ctx, names.hll)
	if err != nil {
		t.Fatalf("redis pfcount failed: %v", err)
	}
	if cardinality < 2 || cardinality > 3 {
		t.Fatalf("redis pfcount = %d, want approximate count around 2", cardinality)
	}
}

func TestServiceBackedPostgresStateStoreAndPool(t *testing.T) {
	observability.Default().Reset()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	env := requireServiceEnv(t)
	store := openPostgres(t, env, database.PoolOptions{
		MaxConns:                 4,
		MinConns:                 1,
		HealthCheckPeriod:        time.Second,
		ConnectTimeout:           2 * time.Second,
		QueryTimeout:             3 * time.Second,
		AcquireTimeout:           250 * time.Millisecond,
		StatementCacheCapacity:   32,
		DescriptionCacheCapacity: 16,
	})
	defer store.Close()
	applyStateSchema(t, ctx, store)

	orgID := uniqueName(env.prefix, "org")
	cleanupOrganization(t, ctx, store, orgID)
	for i := 0; i < 32; i++ {
		state := "pending"
		if i%2 == 0 {
			state = "ready"
		}
		rec := database.DomainRecord{
			Domain:         "orders",
			Collection:     "service-backed",
			OrganizationID: orgID,
			RecordID:       fmt.Sprintf("record-%02d", i),
			Data: map[string]any{
				"state":  state,
				"bucket": strconv.Itoa(i % 4),
			},
		}
		if _, err := store.UpsertRecord(ctx, rec); err != nil {
			t.Fatalf("postgres upsert[%d] failed: %v", i, err)
		}
	}
	got, ok, err := store.GetRecord(ctx, "orders", "service-backed", orgID, "record-04")
	if err != nil || !ok || got.Data["state"] != "ready" {
		t.Fatalf("postgres get = %#v, %v, %v; want ready record", got, ok, err)
	}
	ready, err := store.ListRecords(ctx, "orders", "service-backed", orgID, map[string]any{"state": "ready"}, 5)
	if err != nil {
		t.Fatalf("postgres filtered list failed: %v", err)
	}
	if len(ready) != 5 {
		t.Fatalf("postgres filtered list returned %d records, want 5", len(ready))
	}
	count, err := store.CountRecords(ctx, "orders", "service-backed", orgID, map[string]any{"state": "ready"})
	if err != nil || count != 16 {
		t.Fatalf("postgres filtered count = %d, %v; want 16, nil", count, err)
	}
	estimate, err := store.EstimateCount(ctx, "orders", "service-backed", orgID)
	if err != nil || estimate < 0 {
		t.Fatalf("postgres estimate = %d, %v; want non-negative, nil", estimate, err)
	}
	assertExecResultPath(t, ctx, store, orgID)
	assertRollbackPath(t, ctx, store, orgID)
	assertQueryBudget(t, env)
	stats := store.Stats()
	if stats.MaxConns != 4 || stats.TotalConns <= 0 {
		t.Fatalf("postgres stats = %+v, want max=4 and active pool", stats)
	}
	assertSnapshotHasCount(t, "database", "exec|success")
}

func TestServiceBackedPostgresPoolSaturationIsBounded(t *testing.T) {
	observability.Default().Reset()
	env := requireServiceEnv(t)
	acquireTimeout := 50 * time.Millisecond
	store := openPostgres(t, env, database.PoolOptions{
		MaxConns:                 1,
		MinConns:                 0,
		HealthCheckPeriod:        time.Second,
		ConnectTimeout:           2 * time.Second,
		QueryTimeout:             time.Second,
		AcquireTimeout:           acquireTimeout,
		StatementCacheCapacity:   8,
		DescriptionCacheCapacity: 4,
	})
	defer store.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	applyStateSchema(t, ctx, store)
	db := requirePostgresDB(t, store)
	held, err := db.Pool().Acquire(ctx)
	if err != nil {
		t.Fatalf("postgres pool acquire setup failed: %v", err)
	}
	defer held.Release()

	stats := store.Stats()
	if stats.MaxConns != 1 || stats.ActiveConns != 1 {
		t.Fatalf("postgres saturated stats = %+v, want max=1 active=1", stats)
	}

	const workers = 8
	durations := make(chan time.Duration, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			err := store.Exec(context.Background(), `SELECT 1`)
			durations <- time.Since(start)
			errs <- err
		}()
	}
	wg.Wait()
	close(durations)
	close(errs)

	for err := range errs {
		if !errors.Is(err, database.ErrPoolAcquireTimeout) {
			t.Fatalf("postgres saturated acquire error = %v, want ErrPoolAcquireTimeout", err)
		}
	}
	maxElapsed := maxDuration(durations)
	if maxElapsed > 500*time.Millisecond {
		t.Fatalf("postgres saturated acquire max elapsed = %s, want bounded near %s", maxElapsed, acquireTimeout)
	}
	assertSnapshotHasCount(t, "database", "exec_acquire|error")
	assertSnapshotHasPostgresPoolPressure(t, 1)
}

func TestServiceBackedPostgresRawJSONStateStore(t *testing.T) {
	observability.Default().Reset()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	env := requireServiceEnv(t)
	store := openPostgres(t, env, serviceBackedPoolOptions(4))
	defer store.Close()
	applyStateSchema(t, ctx, store)
	rawStore, ok := store.(database.RawStateStore)
	if !ok {
		t.Fatalf("postgres store does not implement RawStateStore")
	}

	orgID := uniqueName(env.prefix, "raw-org")
	cleanupOrganization(t, ctx, store, orgID)
	rec, err := rawStore.UpsertRecordJSON(ctx, database.RawDomainRecord{
		Domain:         "orders",
		Collection:     "raw-json",
		OrganizationID: orgID,
		RecordID:       "record-01",
		DataJSON:       []byte(`{"state":"ready","bucket":"hot"}`),
	})
	if err != nil {
		t.Fatalf("postgres raw json upsert failed: %v", err)
	}
	if !bytes.Equal(rec.DataJSON, []byte(`{"state":"ready","bucket":"hot"}`)) {
		t.Fatalf("raw json upsert did not preserve caller bytes: %s", string(rec.DataJSON))
	}

	raw, found, err := rawStore.GetRecordJSON(ctx, "orders", "raw-json", orgID, "record-01")
	if err != nil || !found {
		t.Fatalf("postgres raw json get found=%v err=%v", found, err)
	}
	if !bytes.Contains(raw.DataJSON, []byte(`"state": "ready"`)) && !bytes.Contains(raw.DataJSON, []byte(`"state":"ready"`)) {
		t.Fatalf("postgres raw json payload missing state: %s", string(raw.DataJSON))
	}
	if !bytes.Contains(raw.DataJSON, []byte(`"organization_id": "`+orgID+`"`)) &&
		!bytes.Contains(raw.DataJSON, []byte(`"organization_id":"`+orgID+`"`)) {
		t.Fatalf("postgres raw json payload missing organization: %s", string(raw.DataJSON))
	}
	typed, found, err := store.GetRecord(ctx, "orders", "raw-json", orgID, "record-01")
	if err != nil || !found || typed.Data["organization_id"] != orgID {
		t.Fatalf("postgres typed get after raw upsert = %#v found=%v err=%v", typed, found, err)
	}
	assertSnapshotHasCount(t, "database", "upsert_record_json|success")
}

func TestServiceBackedNervousSystemLifecycle(t *testing.T) {
	observability.Default().Reset()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	env := requireServiceEnv(t)
	client := openRedis(t, env)
	bus := events.NewRedisBus(client, uniqueName(env.prefix, "events"), 32, nil)
	defer bus.Close()

	recorder := contracttest.NewLifecycleRecorder()
	wrapped := recorder.WrapBus(bus)
	received := make(chan events.Envelope, 4)
	wrapped.Subscribe("orders:*", func(_ context.Context, envelope events.Envelope) {
		received <- envelope
	})

	correlationID := uniqueName(env.prefix, "corr")
	idempotencyKey := uniqueName(env.prefix, "idem")
	orgID := uniqueName(env.prefix, "org")
	metadata := map[string]any{
		"correlation_id":  correlationID,
		"idempotency_key": idempotencyKey,
		"organization_id": orgID,
	}
	requested := lifecycleEnvelope("orders:create:requested", correlationID, metadata)
	terminal := lifecycleEnvelope("orders:create:success", correlationID, metadata)
	if err := wrapped.Publish(ctx, requested); err != nil {
		t.Fatalf("publish requested failed: %v", err)
	}
	job := worker.Job{
		JobKind:        "orders.create",
		Queue:          "orders",
		CorrelationID:  correlationID,
		IdempotencyKey: idempotencyKey,
		Metadata:       map[string]any{"organization_id": orgID},
	}
	job.Normalize()
	recorder.RecordJob(job)
	if err := wrapped.Publish(ctx, terminal); err != nil {
		t.Fatalf("publish terminal failed: %v", err)
	}
	waitEnvelopes(t, received, 2)

	err := recorder.Verify("orders:create:requested", "orders:create:success", contracttest.LifecycleOptions{
		RequireIdempotency: true,
		RequireTenant:      true,
	})
	if err != nil {
		t.Fatalf("service-backed lifecycle verification failed: %v", err)
	}
	trace := observability.Default().Trace(correlationID, 0)
	if len(trace) < 2 {
		t.Fatalf("trace length = %d, want at least publish stages", len(trace))
	}
}

func TestServiceBackedRedisConcurrentLoadSmoke(t *testing.T) {
	env := requireServiceEnv(t)
	client := openRedis(t, env)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	stats, err := runRedisConcurrentLoad(ctx, client, env.prefix, 16, 64)
	if err != nil {
		t.Fatalf("redis concurrent load failed: %v", err)
	}
	p95Budget := durationBudget("SERVICE_BACKED_REDIS_P95_BUDGET", 20*time.Millisecond)
	p99Budget := durationBudget("SERVICE_BACKED_REDIS_P99_BUDGET", 50*time.Millisecond)
	if stats.P95 > p95Budget || stats.P99 > p99Budget {
		t.Fatalf("redis concurrent set/get latency = %+v, budgets p95=%s p99=%s", stats, p95Budget, p99Budget)
	}
}

func TestServiceBackedPostgresConcurrentLoadSmoke(t *testing.T) {
	env := requireServiceEnv(t)
	store := openPostgres(t, env, serviceBackedPoolOptions(8))
	defer store.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	applyStateSchema(t, ctx, store)
	orgID := uniqueName(env.prefix, "pg-load-org")
	cleanupOrganization(t, ctx, store, orgID)
	stats, err := runPostgresConcurrentLoad(ctx, store, orgID, 8, 32)
	if err != nil {
		t.Fatalf("postgres concurrent load failed: %v", err)
	}
	p95Budget := durationBudget("SERVICE_BACKED_POSTGRES_P95_BUDGET", 50*time.Millisecond)
	p99Budget := durationBudget("SERVICE_BACKED_POSTGRES_P99_BUDGET", 100*time.Millisecond)
	if stats.P95 > p95Budget || stats.P99 > p99Budget {
		t.Fatalf("postgres concurrent upsert latency = %+v, budgets p95=%s p99=%s", stats, p95Budget, p99Budget)
	}
}

func BenchmarkServiceBackedRedisSetGet(b *testing.B) {
	env := requireServiceEnv(b)
	client := openRedis(b, env)
	defer client.Close()
	ctx := context.Background()
	key := uniqueName(env.prefix, "bench-kv")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := client.Set(ctx, key, []byte("foundation"), 0); err != nil {
			b.Fatalf("redis set failed: %v", err)
		}
		if _, err := client.Get(ctx, key); err != nil {
			b.Fatalf("redis get failed: %v", err)
		}
	}
}

func BenchmarkServiceBackedRedisSet(b *testing.B) {
	env := requireServiceEnv(b)
	client := openRedis(b, env)
	defer client.Close()
	ctx := context.Background()
	key := uniqueName(env.prefix, "bench-set")
	value := []byte("foundation")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := client.Set(ctx, key, value, 0); err != nil {
			b.Fatalf("redis set failed: %v", err)
		}
	}
}

func BenchmarkServiceBackedRedisGet(b *testing.B) {
	env := requireServiceEnv(b)
	client := openRedis(b, env)
	defer client.Close()
	ctx := context.Background()
	key := uniqueName(env.prefix, "bench-get")
	if err := client.Set(ctx, key, []byte("foundation"), 0); err != nil {
		b.Fatalf("redis set failed: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := client.Get(ctx, key); err != nil {
			b.Fatalf("redis get failed: %v", err)
		}
	}
}

func BenchmarkServiceBackedRedisSetGetParallel(b *testing.B) {
	env := requireServiceEnv(b)
	client := openRedis(b, env)
	defer client.Close()
	ctx := context.Background()
	var seq atomic.Uint64

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			key := fmt.Sprintf("%s-parallel-%d", env.prefix, seq.Add(1))
			if err := client.Set(ctx, key, []byte("foundation"), time.Minute); err != nil {
				b.Fatalf("redis set failed: %v", err)
			}
			if _, err := client.Get(ctx, key); err != nil {
				b.Fatalf("redis get failed: %v", err)
			}
		}
	})
}

func BenchmarkServiceBackedRedisSetManyGetMany64(b *testing.B) {
	env := requireServiceEnv(b)
	client := openRedis(b, env)
	defer client.Close()
	batch := requireRedisBatch(b, client)
	ctx := context.Background()
	values, keys := redisBatchValues(env.prefix, 64)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := batch.SetMany(ctx, values, time.Minute); err != nil {
			b.Fatalf("redis set many failed: %v", err)
		}
		got, err := batch.GetMany(ctx, keys...)
		if err != nil || len(got) != len(keys) {
			b.Fatalf("redis get many len=%d err=%v", len(got), err)
		}
	}
	b.ReportMetric(64, "keys/op")
}

func BenchmarkServiceBackedRedisSetGetMany64(b *testing.B) {
	env := requireServiceEnv(b)
	client := openRedis(b, env)
	defer client.Close()
	batch := requireRedisBatch(b, client)
	ctx := context.Background()
	values, keys := redisBatchValues(env.prefix, 64)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := batch.SetGetMany(ctx, values, time.Minute)
		if err != nil || len(got) != len(keys) {
			b.Fatalf("redis set/get many len=%d err=%v", len(got), err)
		}
	}
	b.ReportMetric(64, "keys/op")
}

func BenchmarkServiceBackedRedisRawPipelineSetGet64(b *testing.B) {
	env := requireServiceEnv(b)
	client := openRawRedis(b, env)
	defer client.Close()
	ctx := context.Background()
	keys := make([]string, 0, 64)
	for _, key := range redisBatchKeys(env.prefix, 64) {
		keys = append(keys, qualifiedRedisKey(env.prefix, key))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pipe := client.Pipeline()
		for _, key := range keys {
			pipe.Set(ctx, key, []byte("foundation"), time.Minute)
			pipe.Get(ctx, key)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			b.Fatalf("redis raw pipeline failed: %v", err)
		}
	}
	b.ReportMetric(64, "keys/op")
}

func BenchmarkServiceBackedPostgresUpsert(b *testing.B) {
	env := requireServiceEnv(b)
	ctx := context.Background()
	store := openPostgres(b, env, serviceBackedPoolOptions(8))
	defer store.Close()
	applyStateSchema(b, ctx, store)
	orgID := uniqueName(env.prefix, "bench-org")
	cleanupOrganization(b, ctx, store, orgID)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := database.DomainRecord{
			Domain:         "orders",
			Collection:     "benchmark",
			OrganizationID: orgID,
			RecordID:       strconv.Itoa(i),
			Data:           map[string]any{"state": "ready"},
		}
		if _, err := store.UpsertRecord(ctx, rec); err != nil {
			b.Fatalf("postgres upsert failed: %v", err)
		}
	}
}

func BenchmarkServiceBackedPostgresUpsertRawJSON(b *testing.B) {
	env := requireServiceEnv(b)
	ctx := context.Background()
	store := openPostgres(b, env, serviceBackedPoolOptions(8))
	defer store.Close()
	rawStore, ok := store.(database.RawStateStore)
	if !ok {
		b.Fatalf("postgres store does not implement RawStateStore")
	}
	applyStateSchema(b, ctx, store)
	orgID := uniqueName(env.prefix, "bench-raw-org")
	cleanupOrganization(b, ctx, store, orgID)
	payload := []byte(`{"state":"ready"}`)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := database.RawDomainRecord{
			Domain:         "orders",
			Collection:     "benchmark-raw",
			OrganizationID: orgID,
			RecordID:       strconv.Itoa(i),
			DataJSON:       payload,
		}
		if _, err := rawStore.UpsertRecordJSON(ctx, rec); err != nil {
			b.Fatalf("postgres raw json upsert failed: %v", err)
		}
	}
}

func BenchmarkServiceBackedPostgresUpsertParallel(b *testing.B) {
	env := requireServiceEnv(b)
	ctx := context.Background()
	store := openPostgres(b, env, serviceBackedPoolOptions(16))
	defer store.Close()
	applyStateSchema(b, ctx, store)
	orgID := uniqueName(env.prefix, "bench-parallel-org")
	cleanupOrganization(b, ctx, store, orgID)
	var seq atomic.Uint64

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			id := seq.Add(1)
			rec := database.DomainRecord{
				Domain:         "orders",
				Collection:     "benchmark-parallel",
				OrganizationID: orgID,
				RecordID:       strconv.FormatUint(id, 10),
				Data:           map[string]any{"state": "ready"},
			}
			if _, err := store.UpsertRecord(ctx, rec); err != nil {
				b.Fatalf("postgres upsert failed: %v", err)
			}
		}
	})
}

func BenchmarkServiceBackedPostgresSendBatchUpsert64(b *testing.B) {
	env := requireServiceEnv(b)
	ctx := context.Background()
	store := openPostgres(b, env, serviceBackedPoolOptions(8))
	defer store.Close()
	db := requirePostgresDB(b, store)
	applyStateSchema(b, ctx, store)
	orgID := uniqueName(env.prefix, "bench-batch-org")
	cleanupOrganization(b, ctx, store, orgID)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		startID := i * 64
		err := db.SendBatch(ctx, func(batch *pgx.Batch) {
			queueStateStoreBatch(batch, orgID, startID, 64)
		}, consumeBatchExecs(64))
		if err != nil {
			b.Fatalf("postgres send batch failed: %v", err)
		}
	}
	b.ReportMetric(64, "rows/op")
}

func BenchmarkServiceBackedPostgresCopyFrom64(b *testing.B) {
	env := requireServiceEnv(b)
	ctx := context.Background()
	store := openPostgres(b, env, serviceBackedPoolOptions(8))
	defer store.Close()
	db := requirePostgresDB(b, store)
	applyCopySchema(b, ctx, store)
	orgID := uniqueName(env.prefix, "bench-copy-org")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows := copyRows(orgID, i*64, 64)
		if _, err := db.CopyFromRows(ctx, []string{"service_backed_copy_records"}, copyColumns(), rows); err != nil {
			b.Fatalf("postgres copy failed: %v", err)
		}
	}
	b.ReportMetric(64, "rows/op")
}

func requireServiceEnv(tb testing.TB) serviceEnv {
	tb.Helper()
	env := serviceEnv{
		databaseURL: strings.TrimSpace(os.Getenv("SERVICE_BACKED_DATABASE_URL")),
		redisURL:    strings.TrimSpace(os.Getenv("SERVICE_BACKED_REDIS_URL")),
		prefix:      strings.TrimSpace(os.Getenv("SERVICE_BACKED_REDIS_PREFIX")),
	}
	if env.databaseURL == "" || env.redisURL == "" {
		tb.Fatalf("SERVICE_BACKED_DATABASE_URL and SERVICE_BACKED_REDIS_URL are required")
	}
	if env.prefix == "" {
		env.prefix = "service-backed-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return env
}

func openRedis(tb testing.TB, env serviceEnv) rediskit.Client {
	tb.Helper()
	client, err := rediskit.ConnectWithOptions(rediskit.Options{
		URL:          env.redisURL,
		Prefix:       env.prefix,
		Driver:       rediskit.DriverRedis,
		PoolSize:     8,
		MinIdle:      2,
		MaxRetries:   1,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  500 * time.Millisecond,
		WriteTimeout: 500 * time.Millisecond,
	})
	if err != nil {
		tb.Fatalf("connect redis failed: %v", err)
	}
	return client
}

func openPostgres(tb testing.TB, env serviceEnv, opts database.PoolOptions) database.RuntimeStore {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store, err := database.Connect(ctx, env.databaseURL, database.DriverPostgres, opts)
	if err != nil {
		tb.Fatalf("connect postgres failed: %v", err)
	}
	return store
}

func serviceBackedPoolOptions(maxConns int) database.PoolOptions {
	if maxConns <= 0 {
		maxConns = 8
	}
	return database.PoolOptions{
		MaxConns:                 maxConns,
		MinConns:                 min(maxConns, 2),
		HealthCheckPeriod:        time.Second,
		ConnectTimeout:           2 * time.Second,
		QueryTimeout:             2 * time.Second,
		AcquireTimeout:           250 * time.Millisecond,
		StatementCacheCapacity:   128,
		DescriptionCacheCapacity: 32,
	}
}

func openRawRedis(tb testing.TB, env serviceEnv) *goredis.Client {
	tb.Helper()
	opts, err := goredis.ParseURL(env.redisURL)
	if err != nil {
		tb.Fatalf("parse redis url failed: %v", err)
	}
	opts.PoolSize = 16
	opts.MinIdleConns = 4
	opts.MaxRetries = 1
	opts.DialTimeout = 2 * time.Second
	opts.ReadTimeout = 500 * time.Millisecond
	opts.WriteTimeout = 500 * time.Millisecond
	client := goredis.NewClient(opts)
	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		tb.Fatalf("raw redis ping failed: %v", err)
	}
	return client
}

func requireRedisBatch(tb testing.TB, client rediskit.Client) rediskit.BatchClient {
	tb.Helper()
	batch, ok := client.(rediskit.BatchClient)
	if !ok {
		tb.Fatalf("redis client %T does not implement BatchClient", client)
	}
	return batch
}

func requirePostgresDB(tb testing.TB, store database.RuntimeStore) *database.PostgresDB {
	tb.Helper()
	db, ok := store.(*database.PostgresDB)
	if !ok {
		tb.Fatalf("store %T is not *database.PostgresDB", store)
	}
	return db
}

func redisBatchValues(prefix string, size int) (map[string]interface{}, []string) {
	keys := redisBatchKeys(prefix, size)
	values := make(map[string]interface{}, len(keys))
	for _, key := range keys {
		values[key] = []byte("foundation")
	}
	return values, keys
}

func redisBatchKeys(prefix string, size int) []string {
	keys := make([]string, 0, size)
	base := uniqueName(prefix, "batch")
	for i := 0; i < size; i++ {
		keys = append(keys, fmt.Sprintf("%s:%02d", base, i))
	}
	return keys
}

func qualifiedRedisKey(prefix, key string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), ":")
	if prefix == "" || strings.HasPrefix(key, prefix+":") {
		return key
	}
	return prefix + ":" + strings.TrimLeft(key, ":")
}

func applyStateSchema(tb testing.TB, ctx context.Context, store database.RuntimeStore) {
	tb.Helper()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS governance_state_records (
			id BIGSERIAL PRIMARY KEY,
			domain TEXT NOT NULL,
			collection_name TEXT NOT NULL,
			organization_id TEXT NOT NULL DEFAULT '',
			record_id TEXT NOT NULL,
			data JSONB NOT NULL DEFAULT '{}'::jsonb,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT governance_state_records_identity_unique
				UNIQUE (domain, collection_name, organization_id, record_id),
			CONSTRAINT governance_state_records_data_object
				CHECK (jsonb_typeof(data) = 'object')
		)`,
		`CREATE INDEX IF NOT EXISTS idx_governance_state_scope_updated
			ON governance_state_records (domain, collection_name, organization_id, updated_at DESC, record_id ASC)`,
		`CREATE INDEX IF NOT EXISTS idx_governance_state_org
			ON governance_state_records (organization_id)`,
		`CREATE INDEX IF NOT EXISTS idx_governance_state_data_gin
			ON governance_state_records USING GIN (data jsonb_path_ops)`,
	}
	for _, statement := range statements {
		if err := store.Exec(ctx, statement); err != nil {
			tb.Fatalf("apply state schema failed: %v", err)
		}
	}
}

func applyCopySchema(tb testing.TB, ctx context.Context, store database.RuntimeStore) {
	tb.Helper()
	err := store.Exec(ctx, `CREATE UNLOGGED TABLE IF NOT EXISTS service_backed_copy_records (
		organization_id TEXT NOT NULL,
		record_id TEXT NOT NULL,
		data JSONB NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (organization_id, record_id)
	)`)
	if err != nil {
		tb.Fatalf("apply copy schema failed: %v", err)
	}
}

func queueStateStoreBatch(batch *pgx.Batch, orgID string, startID, count int) {
	const query = `
		INSERT INTO governance_state_records (domain, collection_name, organization_id, record_id, data)
		VALUES ('orders', 'benchmark-batch', $1, $2, '{"state":"ready"}'::jsonb)
		ON CONFLICT (domain, collection_name, organization_id, record_id)
		DO UPDATE SET data = EXCLUDED.data, updated_at = NOW()
	`
	for i := 0; i < count; i++ {
		batch.Queue(query, orgID, fmt.Sprintf("record-%d", startID+i))
	}
}

func consumeBatchExecs(count int) func(pgx.BatchResults) error {
	return func(results pgx.BatchResults) error {
		for i := 0; i < count; i++ {
			if _, err := results.Exec(); err != nil {
				return err
			}
		}
		return nil
	}
}

func copyRows(orgID string, startID, count int) [][]any {
	rows := make([][]any, 0, count)
	payload := `{"state":"ready"}`
	for i := 0; i < count; i++ {
		rows = append(rows, []any{orgID, fmt.Sprintf("record-%d", startID+i), payload})
	}
	return rows
}

func copyColumns() []string {
	return []string{"organization_id", "record_id", "data"}
}

func assertExecResultPath(t *testing.T, ctx context.Context, store database.RuntimeStore, orgID string) {
	t.Helper()
	executor, ok := store.(database.ResultExecutor)
	if !ok {
		t.Fatalf("postgres store does not implement ResultExecutor")
	}
	result, err := executor.ExecResult(ctx, `
		UPDATE governance_state_records
		SET data = data || '{"checked": true}'::jsonb
		WHERE organization_id = $1
	`, orgID)
	if err != nil {
		t.Fatalf("postgres exec result failed: %v", err)
	}
	if result.RowsAffected() != 32 {
		t.Fatalf("postgres exec result rows = %d, want 32", result.RowsAffected())
	}
}

func assertRollbackPath(t *testing.T, ctx context.Context, store database.RuntimeStore, orgID string) {
	t.Helper()
	beginner, ok := store.(database.TxBeginner)
	if !ok {
		t.Fatalf("postgres store does not implement TxBeginner")
	}
	tx, err := beginner.BeginTx(ctx)
	if err != nil {
		t.Fatalf("postgres begin tx failed: %v", err)
	}
	err = tx.Exec(ctx, `
		INSERT INTO governance_state_records (domain, collection_name, organization_id, record_id, data)
		VALUES ('orders', 'service-backed', $1, 'rolled-back', '{"state": "ready"}'::jsonb)
	`, orgID)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("postgres tx insert failed: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("postgres rollback failed: %v", err)
	}
	_, found, err := store.GetRecord(ctx, "orders", "service-backed", orgID, "rolled-back")
	if err != nil || found {
		t.Fatalf("postgres rollback visibility found=%v err=%v, want false nil", found, err)
	}
}

func assertQueryBudget(t *testing.T, env serviceEnv) {
	t.Helper()
	store := openPostgres(t, env, database.PoolOptions{
		MaxConns:                 2,
		MinConns:                 1,
		HealthCheckPeriod:        time.Second,
		ConnectTimeout:           2 * time.Second,
		QueryTimeout:             50 * time.Millisecond,
		AcquireTimeout:           50 * time.Millisecond,
		StatementCacheCapacity:   8,
		DescriptionCacheCapacity: 4,
	})
	defer store.Close()
	ctx := context.Background()
	start := time.Now()
	var ignored any
	err := store.QueryRow(ctx, `SELECT pg_sleep(0.2)`).Scan(&ignored)
	if err == nil {
		t.Fatalf("postgres query budget did not interrupt slow query")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("postgres query budget elapsed %s, want under 1s", elapsed)
	}
}

func cleanupRedisKeys(tb testing.TB, ctx context.Context, client rediskit.Client, keys ...string) {
	tb.Helper()
	if err := client.Del(ctx, keys...); err != nil {
		tb.Fatalf("redis cleanup failed: %v", err)
	}
}

func cleanupOrganization(tb testing.TB, ctx context.Context, store database.RuntimeStore, orgID string) {
	tb.Helper()
	if cleaner, ok := store.(interface {
		DeleteRecordsByOrganization(context.Context, string) (int64, error)
	}); ok {
		if _, err := cleaner.DeleteRecordsByOrganization(ctx, orgID); err != nil {
			tb.Fatalf("postgres organization cleanup failed: %v", err)
		}
	}
}

func runRedisConcurrentLoad(
	ctx context.Context,
	client rediskit.Client,
	prefix string,
	workers int,
	iterations int,
) (latencySummary, error) {
	durations := make(chan time.Duration, workers*iterations)
	errCh := make(chan error, workers)
	var wg sync.WaitGroup
	for workerID := 0; workerID < workers; workerID++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				key := fmt.Sprintf("%s-load-%02d-%03d", prefix, workerID, i)
				start := time.Now()
				if err := client.Set(ctx, key, []byte("foundation"), time.Minute); err != nil {
					errCh <- err
					return
				}
				if _, err := client.Get(ctx, key); err != nil {
					errCh <- err
					return
				}
				durations <- time.Since(start)
			}
		}(workerID)
	}
	wg.Wait()
	close(durations)
	close(errCh)
	return summarizeDurations(durations), firstError(errCh)
}

func runPostgresConcurrentLoad(
	ctx context.Context,
	store database.RuntimeStore,
	orgID string,
	workers int,
	iterations int,
) (latencySummary, error) {
	durations := make(chan time.Duration, workers*iterations)
	errCh := make(chan error, workers)
	var wg sync.WaitGroup
	for workerID := 0; workerID < workers; workerID++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			runPostgresWorker(ctx, store, orgID, workerID, iterations, durations, errCh)
		}(workerID)
	}
	wg.Wait()
	close(durations)
	close(errCh)
	return summarizeDurations(durations), firstError(errCh)
}

func runPostgresWorker(
	ctx context.Context,
	store database.RuntimeStore,
	orgID string,
	workerID int,
	iterations int,
	durations chan<- time.Duration,
	errCh chan<- error,
) {
	for i := 0; i < iterations; i++ {
		rec := database.DomainRecord{
			Domain:         "orders",
			Collection:     "load-smoke",
			OrganizationID: orgID,
			RecordID:       fmt.Sprintf("record-%02d-%03d", workerID, i),
			Data:           map[string]any{"state": "ready"},
		}
		start := time.Now()
		if _, err := store.UpsertRecord(ctx, rec); err != nil {
			errCh <- err
			return
		}
		durations <- time.Since(start)
	}
}

func summarizeDurations(values <-chan time.Duration) latencySummary {
	durations := make([]time.Duration, 0)
	for value := range values {
		durations = append(durations, value)
	}
	if len(durations) == 0 {
		return latencySummary{}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	return latencySummary{
		P95: percentileFromSorted(durations, 0.95),
		P99: percentileFromSorted(durations, 0.99),
		Max: durations[len(durations)-1],
	}
}

func percentileFromSorted(durations []time.Duration, percentile float64) time.Duration {
	if len(durations) == 0 {
		return 0
	}
	index := int(float64(len(durations)-1) * percentile)
	return durations[index]
}

func maxDuration(values <-chan time.Duration) time.Duration {
	var max time.Duration
	for value := range values {
		if value > max {
			max = value
		}
	}
	return max
}

func firstError(errCh <-chan error) error {
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

func durationBudget(envName string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(envName))
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func waitRedisMissing(t *testing.T, ctx context.Context, client rediskit.Client, key string) {
	t.Helper()
	for attempt := 0; attempt < 100; attempt++ {
		got, err := client.Get(ctx, key)
		if err != nil {
			t.Fatalf("redis ttl get failed: %v", err)
		}
		if got == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("redis key %q did not expire", key)
}

func waitBytes(t *testing.T, ch <-chan []byte, want []byte) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case got := <-ch:
		if !bytes.Equal(got, want) {
			t.Fatalf("pubsub payload = %q, want %q", string(got), string(want))
		}
	case <-timer.C:
		t.Fatalf("timed out waiting for pubsub payload %q", string(want))
	}
}

func waitEnvelopes(t *testing.T, ch <-chan events.Envelope, want int) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for i := 0; i < want; i++ {
		select {
		case <-ch:
		case <-timer.C:
			t.Fatalf("timed out waiting for %d envelopes, received %d", want, i)
		}
	}
}

func lifecycleEnvelope(eventType, correlationID string, metadata map[string]any) events.Envelope {
	envelope := events.Envelope{
		EventType:     eventType,
		Payload:       map[string]any{"record_id": "record-01"},
		Metadata:      metadata,
		CorrelationID: correlationID,
	}
	envelope.Normalize()
	return envelope
}

func assertSnapshotHasCount(t *testing.T, section, key string) {
	t.Helper()
	snapshot := observability.Default().Snapshot()
	sectionMap, ok := snapshot[section].(map[string]any)
	if !ok {
		t.Fatalf("observability snapshot missing section %q: %#v", section, snapshot)
	}
	counts, ok := sectionMap["count"].(map[string]int64)
	if !ok {
		t.Fatalf("observability snapshot section %q missing counts: %#v", section, sectionMap)
	}
	if counts[key] == 0 {
		t.Fatalf("observability count %s/%s = 0, snapshot=%#v", section, key, snapshot)
	}
}

func assertSnapshotHasPostgresPoolPressure(t *testing.T, maxConns int32) {
	t.Helper()
	snapshot := observability.Default().Snapshot()
	databaseSection, ok := snapshot["database"].(map[string]any)
	if !ok {
		t.Fatalf("observability snapshot missing database section: %#v", snapshot)
	}
	pools, ok := databaseSection["pool"].(map[string]observability.DatabasePoolPressure)
	if !ok {
		t.Fatalf("observability database snapshot missing pool pressure: %#v", databaseSection)
	}
	pressure, ok := pools["postgres"]
	if !ok {
		t.Fatalf("observability database pool missing postgres: %#v", pools)
	}
	if pressure.MaxConns != maxConns || pressure.TotalConns == 0 || pressure.AcquireCount == 0 {
		t.Fatalf("postgres pool pressure = %+v, want max=%d with acquire activity", pressure, maxConns)
	}
}

func uniqueName(prefix, suffix string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), ":")
	if prefix == "" {
		prefix = "service-backed"
	}
	suffix = strings.Trim(strings.TrimSpace(suffix), ":")
	return prefix + "-" + suffix + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}
