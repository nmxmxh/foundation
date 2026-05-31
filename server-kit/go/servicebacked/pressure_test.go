//go:build servicebacked

package servicebacked

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/observability"
	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

func TestServiceBackedRedisStreamLagAndSlowSubscriberPressure(t *testing.T) {
	observability.Default().Reset()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	env := requireServiceEnv(t)
	client := openRedis(t, env)
	defer client.Close()
	raw := openRawRedis(t, env)
	defer raw.Close()

	stream := uniqueName(env.prefix, "stream-pressure")
	group := uniqueName(env.prefix, "stream-pressure-group")
	channel := uniqueName(env.prefix, "slow-channel")
	t.Cleanup(func() {
		deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer deleteCancel()
		_ = client.Del(deleteCtx, stream)
	})

	const streamMessages = 256
	for i := 0; i < streamMessages; i++ {
		if _, err := client.XAdd(ctx, stream, map[string]any{
			"kind":      "pressure",
			"record_id": fmt.Sprintf("record-%03d", i),
		}); err != nil {
			t.Fatalf("redis xadd[%d] failed: %v", i, err)
		}
	}

	first, err := client.XReadGroup(ctx, stream, group, "consumer-a", 64)
	if err != nil || len(first) != 64 {
		t.Fatalf("first xreadgroup len=%d err=%v, want 64 nil", len(first), err)
	}
	pending := raw.XPending(ctx, qualifiedRedisKey(env.prefix, stream), group).Val()
	if pending == nil || pending.Count != 64 {
		t.Fatalf("redis pending count = %+v, want 64", pending)
	}
	if err := client.XAck(ctx, stream, group, streamIDs(first)...); err != nil {
		t.Fatalf("redis xack first batch failed: %v", err)
	}

	latencies := make(chan time.Duration, streamMessages)
	acked := len(first)
	for acked < streamMessages {
		start := time.Now()
		batch, err := client.XReadGroup(ctx, stream, group, "consumer-a", 64)
		latencies <- time.Since(start)
		if err != nil {
			t.Fatalf("redis stream pressure read failed: %v", err)
		}
		if len(batch) == 0 {
			break
		}
		if err := client.XAck(ctx, stream, group, streamIDs(batch)...); err != nil {
			t.Fatalf("redis stream pressure ack failed: %v", err)
		}
		acked += len(batch)
	}
	close(latencies)
	if acked != streamMessages {
		t.Fatalf("acked stream messages = %d, want %d", acked, streamMessages)
	}
	streamStats := summarizeDurations(latencies)
	assertLatencyBudget(t, "redis stream read/ack pressure", streamStats,
		durationBudget("SERVICE_BACKED_REDIS_STREAM_P95_BUDGET", 25*time.Millisecond),
		durationBudget("SERVICE_BACKED_REDIS_STREAM_P99_BUDGET", 75*time.Millisecond),
	)

	slow, stopSlow, err := client.Subscribe(ctx, channel)
	if err != nil {
		t.Fatalf("redis slow subscriber subscribe failed: %v", err)
	}
	defer stopSlow()
	_ = slow
	pubLatencies := make(chan time.Duration, 512)
	for i := 0; i < 512; i++ {
		start := time.Now()
		if err := client.Publish(ctx, channel, []byte("pressure")); err != nil {
			t.Fatalf("redis publish[%d] failed: %v", i, err)
		}
		pubLatencies <- time.Since(start)
	}
	close(pubLatencies)
	publishStats := summarizeDurations(pubLatencies)
	assertLatencyBudget(t, "redis slow-subscriber publish", publishStats,
		durationBudget("SERVICE_BACKED_REDIS_PUBLISH_P95_BUDGET", 25*time.Millisecond),
		durationBudget("SERVICE_BACKED_REDIS_PUBLISH_P99_BUDGET", 75*time.Millisecond),
	)
}

func TestServiceBackedHermesProjectionLatencyProfile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	env := requireServiceEnv(t)
	state := openPostgres(t, env, serviceBackedPoolOptions(8))
	defer state.Close()
	applyStateSchema(t, ctx, state)
	rawStore, ok := state.(database.RawStateStore)
	if !ok {
		t.Fatalf("postgres store does not implement RawStateStore")
	}
	redisClient := openRedis(t, env)
	defer redisClient.Close()

	orgID := uniqueName(env.prefix, "hermes-pressure-org")
	cleanupOrganization(t, ctx, state, orgID)
	stream := uniqueName(env.prefix, "hermes-pressure-stream")
	group := uniqueName(env.prefix, "hermes-pressure-group")
	t.Cleanup(func() {
		deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer deleteCancel()
		_ = redisClient.Del(deleteCtx, stream)
	})

	store := newHermesPressureStore(t, "svc_hermes_pressure")
	const records = 512
	for i := 0; i < records; i++ {
		_, err := rawStore.UpsertRecordJSON(ctx, database.RawDomainRecord{
			Domain:         "signals",
			Collection:     "pressure",
			OrganizationID: orgID,
			RecordID:       fmt.Sprintf("record-%03d", i),
			DataJSON:       []byte(fmt.Sprintf(`{"bucket":"%02d","source":"postgres"}`, i%16)),
		})
		if err != nil {
			t.Fatalf("postgres raw upsert[%d] failed: %v", i, err)
		}
	}

	rebuildStart := time.Now()
	result, err := store.Rebuild(ctx, "svc_hermes_pressure", state, hermes.Query{OrganizationID: orgID, Limit: records})
	rebuildElapsed := time.Since(rebuildStart)
	if err != nil || result.Applied != records {
		t.Fatalf("hermes rebuild result=%+v err=%v, want %d applied", result, err, records)
	}
	if budget := durationBudget("SERVICE_BACKED_HERMES_REBUILD_512_BUDGET", 2*time.Second); rebuildElapsed > budget {
		t.Fatalf("hermes rebuild elapsed=%s, budget=%s", rebuildElapsed, budget)
	}

	for i := 0; i < records; i++ {
		raw, err := hermesPressureEnvelope(t, orgID, i).ToBinary()
		if err != nil {
			t.Fatalf("hermes envelope binary[%d] failed: %v", i, err)
		}
		if _, err := redisClient.XAdd(ctx, stream, map[string]any{"envelope": raw}); err != nil {
			t.Fatalf("redis xadd hermes[%d] failed: %v", i, err)
		}
	}
	source, err := hermes.NewRedisStreamEnvelopeSource(redisClient, stream, group, "consumer-a", "")
	if err != nil {
		t.Fatalf("NewRedisStreamEnvelopeSource() error = %v", err)
	}
	tailer, err := hermes.NewEnvelopeTailer(store, "svc_hermes_pressure", source, hermes.TailerOptions{MaxBatch: 64})
	if err != nil {
		t.Fatalf("NewEnvelopeTailer() error = %v", err)
	}

	tailLatencies := make(chan time.Duration, records/64+1)
	applied := 0
	for applied < records {
		start := time.Now()
		poll, err := tailer.PollOnce(ctx)
		tailLatencies <- time.Since(start)
		if err != nil {
			t.Fatalf("hermes tailer poll failed: %v", err)
		}
		if poll.Read == 0 {
			break
		}
		applied += poll.Apply.Applied
	}
	close(tailLatencies)
	if applied != records {
		t.Fatalf("hermes tailer applied = %d, want %d", applied, records)
	}
	tailStats := summarizeDurations(tailLatencies)
	assertLatencyBudget(t, "hermes redis tailer pressure", tailStats,
		durationBudget("SERVICE_BACKED_HERMES_TAIL_P95_BUDGET", 50*time.Millisecond),
		durationBudget("SERVICE_BACKED_HERMES_TAIL_P99_BUDGET", 150*time.Millisecond),
	)

	countLatencies := make(chan time.Duration, 128)
	for i := 0; i < 128; i++ {
		start := time.Now()
		count, err := store.Count(ctx, "svc_hermes_pressure", hermes.Query{
			OrganizationID: orgID,
			Filters:        map[string]any{"bucket": fmt.Sprintf("%02d", i%16)},
		}, hermes.Fence{})
		countLatencies <- time.Since(start)
		if err != nil || count == 0 {
			t.Fatalf("hermes hot count[%d] count=%d err=%v", i, count, err)
		}
	}
	close(countLatencies)
	countStats := summarizeDurations(countLatencies)
	assertLatencyBudget(t, "hermes hot count pressure", countStats,
		durationBudget("SERVICE_BACKED_HERMES_COUNT_P95_BUDGET", 5*time.Millisecond),
		durationBudget("SERVICE_BACKED_HERMES_COUNT_P99_BUDGET", 15*time.Millisecond),
	)
}

func TestServiceBackedMixedWorkflowLatencyProfile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	env := requireServiceEnv(t)
	state := openPostgres(t, env, serviceBackedPoolOptions(8))
	defer state.Close()
	applyStateSchema(t, ctx, state)
	rawStore, ok := state.(database.RawStateStore)
	if !ok {
		t.Fatalf("postgres store does not implement RawStateStore")
	}
	redisClient := openRedis(t, env)
	defer redisClient.Close()
	batch := requireRedisBatch(t, redisClient)

	orgID := uniqueName(env.prefix, "mixed-org")
	cleanupOrganization(t, ctx, state, orgID)
	store := newHermesPressureStore(t, "svc_mixed_pressure")

	const workers = 8
	const iterations = 32
	latencies := make(chan time.Duration, workers*iterations)
	errCh := make(chan error, workers)
	var seq atomic.Uint64
	var wg sync.WaitGroup
	for workerID := 0; workerID < workers; workerID++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				id := seq.Add(1)
				recordID := fmt.Sprintf("mixed-%03d", id)
				start := time.Now()
				if _, err := rawStore.UpsertRecordJSON(ctx, database.RawDomainRecord{
					Domain:         "signals",
					Collection:     "pressure",
					OrganizationID: orgID,
					RecordID:       recordID,
					DataJSON:       []byte(fmt.Sprintf(`{"bucket":"%02d","source":"mixed","worker":%d}`, id%16, workerID)),
				}); err != nil {
					errCh <- err
					return
				}
				values := map[string]any{
					fmt.Sprintf("mixed:%03d:a", id): []byte("a"),
					fmt.Sprintf("mixed:%03d:b", id): []byte("b"),
				}
				if _, err := batch.SetGetMany(ctx, values, time.Minute); err != nil {
					errCh <- err
					return
				}
				_, err := store.Apply(ctx, "svc_mixed_pressure", hermes.Event{
					Operation: hermes.OperationUpsert,
					SourceID:  recordID,
					Version:   id,
					Record: database.DomainRecord{
						Domain:         "signals",
						Collection:     "pressure",
						OrganizationID: orgID,
						RecordID:       recordID,
						Data:           map[string]any{"bucket": fmt.Sprintf("%02d", id%16), "source": "mixed"},
					},
				})
				if err != nil {
					errCh <- err
					return
				}
				latencies <- time.Since(start)
			}
		}(workerID)
	}
	wg.Wait()
	close(latencies)
	close(errCh)
	if err := firstError(errCh); err != nil {
		t.Fatalf("mixed workflow failed: %v", err)
	}
	stats := summarizeDurations(latencies)
	assertLatencyBudget(t, "mixed postgres/redis/hermes workflow", stats,
		durationBudget("SERVICE_BACKED_MIXED_P95_BUDGET", 75*time.Millisecond),
		durationBudget("SERVICE_BACKED_MIXED_P99_BUDGET", 150*time.Millisecond),
	)
}

func BenchmarkServiceBackedHermesRebuild512(b *testing.B) {
	env := requireServiceEnv(b)
	ctx := context.Background()
	state := openPostgres(b, env, serviceBackedPoolOptions(8))
	defer state.Close()
	applyStateSchema(b, ctx, state)
	orgID := uniqueName(env.prefix, "bench-hermes-rebuild")
	cleanupOrganization(b, ctx, state, orgID)
	for i := 0; i < 512; i++ {
		if _, err := state.UpsertRecord(ctx, database.DomainRecord{
			Domain:         "signals",
			Collection:     "pressure",
			OrganizationID: orgID,
			RecordID:       fmt.Sprintf("record-%03d", i),
			Data:           map[string]any{"bucket": fmt.Sprintf("%02d", i%16), "source": "bench"},
		}); err != nil {
			b.Fatalf("postgres seed[%d] failed: %v", i, err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store := newHermesPressureStore(b, "svc_bench_rebuild")
		result, err := store.Rebuild(ctx, "svc_bench_rebuild", state, hermes.Query{OrganizationID: orgID, Limit: 512})
		if err != nil || result.Applied != 512 {
			b.Fatalf("hermes rebuild result=%+v err=%v", result, err)
		}
	}
	b.ReportMetric(512, "records/op")
}

func BenchmarkServiceBackedHermesApplyBatch512(b *testing.B) {
	ctx := context.Background()
	env := requireServiceEnv(b)
	orgID := uniqueName(env.prefix, "bench-hermes-apply")
	events := make([]hermes.Event, 0, 512)
	for i := 0; i < 512; i++ {
		events = append(events, hermes.Event{
			Operation: hermes.OperationUpsert,
			SourceID:  fmt.Sprintf("source-%03d", i),
			Version:   uint64(i + 1),
			Record: database.DomainRecord{
				Domain:         "signals",
				Collection:     "pressure",
				OrganizationID: orgID,
				RecordID:       fmt.Sprintf("record-%03d", i),
				Data:           map[string]any{"bucket": fmt.Sprintf("%02d", i%16), "source": "bench"},
			},
		})
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store := newHermesPressureStore(b, "svc_bench_apply")
		result, err := store.ApplyBatch(ctx, "svc_bench_apply", events)
		if err != nil || result.Applied != 512 {
			b.Fatalf("hermes apply result=%+v err=%v", result, err)
		}
	}
	b.ReportMetric(512, "records/op")
}

func newHermesPressureStore(tb testing.TB, projection string) *hermes.Store {
	tb.Helper()
	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name:          projection,
		Domain:        "signals",
		Collection:    "pressure",
		IndexedFields: []string{"bucket", "source"},
		MaxRecords:    2048,
		MaxBytes:      8 << 20,
	})
	if err != nil {
		tb.Fatalf("NewStore(%s) error = %v", projection, err)
	}
	return store
}

func hermesPressureEnvelope(tb testing.TB, orgID string, i int) events.Envelope {
	tb.Helper()
	envelope, err := hermes.NewProjectionEnvelope([]*foundationpb.RecordMutation{{
		Operation:      foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT,
		SourceId:       fmt.Sprintf("service-backed:pressure:%03d", i),
		Version:        uint64(i + 1),
		Domain:         "signals",
		Collection:     "pressure",
		OrganizationId: orgID,
		RecordId:       fmt.Sprintf("stream-record-%03d", i),
		CorrelationId:  fmt.Sprintf("corr-hermes-pressure-%03d", i),
		Fields: []*foundationpb.FieldValue{
			projectionField("bucket", fmt.Sprintf("%02d", i%16)),
			projectionField("source", "redis"),
		},
	}}, fmt.Sprintf("corr-hermes-pressure-%03d", i))
	if err != nil {
		tb.Fatalf("NewProjectionEnvelope(%d) error = %v", i, err)
	}
	return envelope
}

func streamIDs(messages []rediskit.StreamMessage) []string {
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
	}
	return ids
}

func assertLatencyBudget(tb testing.TB, label string, stats latencySummary, p95Budget, p99Budget time.Duration) {
	tb.Helper()
	tb.Logf("%s latency p95=%s p99=%s max=%s", label, stats.P95, stats.P99, stats.Max)
	if stats.P95 > p95Budget || stats.P99 > p99Budget {
		tb.Fatalf("%s latency = %+v, budgets p95=%s p99=%s", label, stats, p95Budget, p99Budget)
	}
}
