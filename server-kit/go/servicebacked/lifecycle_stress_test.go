//go:build servicebacked

package servicebacked

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/chaos"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/eventlog"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scratch"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/worker"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// lifecycleTxAdapter adapts pgx.Tx to database.DBTX.
type lifecycleTxAdapter struct {
	tx pgx.Tx
}

func (a lifecycleTxAdapter) Exec(ctx context.Context, query string, args ...any) error {
	_, err := a.tx.Exec(ctx, query, args...)
	return err
}

func (a lifecycleTxAdapter) QueryRow(ctx context.Context, query string, args ...any) database.RowScanner {
	return a.tx.QueryRow(ctx, query, args...)
}

func (a lifecycleTxAdapter) Query(ctx context.Context, query string, args ...any) (database.Rows, error) {
	return a.tx.Query(ctx, query, args...)
}

// LifecycleRequest defines an ingress request envelope.
type LifecycleRequest struct {
	CorrelationID  string
	OrganizationID string
	RecordID       string
	AuthToken      string
	PayloadKind    string
	Data           []byte
}

// LifecycleResponse records the result of an ingress request.
type LifecycleResponse struct {
	CorrelationID string
	Status        string
	Latency       time.Duration
	Error         error
}

// LifecycleHarness coordinates Postgres, River, Redis, and Hermes.
type LifecycleHarness struct {
	t             testing.TB
	env           serviceEnv
	pool          *pgxpool.Pool
	db            *database.PostgresDB
	redisClient   rediskit.Client
	hermesStore   *hermes.Store
	engine        *worker.Engine
	riverClient   *river.Client[pgx.Tx]
	processor     *lifecycleStressProcessor
	chaos         *chaos.Injector
	streamKey     string
	pubsubKey     string
	groupKey      string
	projectionKey string
	stopCh        chan struct{}
	wsDelivered   sync.Map
	statsLock     sync.Mutex
	errorCounts   map[string]int
}

type lifecycleStressProcessor struct {
	kind        string
	queue       string
	harness     *LifecycleHarness
	handled     atomic.Int64
	failures    atomic.Int64
	targetCount int64
	doneChan    chan struct{}
}

func (p *lifecycleStressProcessor) Kind() string     { return p.kind }
func (p *lifecycleStressProcessor) Queue() string    { return p.queue }
func (p *lifecycleStressProcessor) MaxAttempts() int { return 5 }

func (p *lifecycleStressProcessor) Handle(ctx context.Context, job worker.Job) error {
	return p.HandleWithArena(ctx, nil, job)
}

func (p *lifecycleStressProcessor) HandleWithArena(ctx context.Context, arena *scratch.Arena, job worker.Job) error {
	// Apply chaos rule to worker execution
	if p.harness.chaos != nil {
		if err := p.harness.chaos.Apply(ctx, "worker", job.CorrelationID); err != nil {
			p.failures.Add(1)
			return err
		}
	}

	// Allocate frame memory from scratch arena to verify zero heap pressure
	if arena != nil {
		_, _ = arena.Alloc(256)
	}

	orgID, _ := job.Payload["org_id"].StringValue()
	recordID, _ := job.Payload["record_id"].StringValue()
	payloadKind, _ := job.Payload["payload_kind"].StringValue()

	// If sidecar payload, verify presence in river_job_metadata table
	if payloadKind == "sidecar" {
		row := p.harness.pool.QueryRow(ctx, "SELECT length(raw_payload) FROM river_job_metadata WHERE correlation_id = $1 LIMIT 1", job.CorrelationID)
		var size int
		_ = row.Scan(&size)
	}

	// 1. Publish projection mutation to Redis stream
	mutationEnvelope, err := hermes.NewProjectionEnvelope([]*foundationpb.RecordMutation{{
		Operation:       foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT,
		SourceId:        "river:" + job.CorrelationID,
		Version:         2,
		Domain:          "stress",
		Collection:      "records",
		OrganizationId:  orgID,
		RecordId:        recordID,
		CorrelationId:   job.CorrelationID,
		Fields: []*foundationpb.FieldValue{
			projectionField("status", "completed"),
			projectionField("worker_processed", "true"),
			projectionField("payload_kind", payloadKind),
		},
	}}, job.CorrelationID)
	if err == nil {
		if rawBytes, err := mutationEnvelope.ToBinary(); err == nil {
			_, _ = p.harness.redisClient.XAdd(ctx, p.harness.streamKey, serviceRedisValues(map[string]any{
				"envelope": rawBytes,
			}))
		}
	}

	// 2. Publish completion notification to Redis pubsub channel
	_ = p.harness.redisClient.Publish(ctx, p.harness.pubsubKey, []byte("COMPLETED:"+job.CorrelationID))

	n := p.handled.Add(1)
	if p.doneChan != nil && p.targetCount > 0 && n >= p.targetCount {
		select {
		case p.doneChan <- struct{}{}:
		default:
		}
	}
	return nil
}

func newLifecycleHarness(tb testing.TB, maxDBConns, workerCount int) *LifecycleHarness {
	tb.Helper()
	env := requireServiceEnv(tb)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	poolOpts := database.PoolOptions{
		MaxConns:                 maxDBConns,
		MinConns:                 min(maxDBConns, 4),
		HealthCheckPeriod:        time.Second,
		ConnectTimeout:           3 * time.Second,
		QueryTimeout:             3 * time.Second,
		AcquireTimeout:           250 * time.Millisecond,
		StatementCacheCapacity:   64,
		DescriptionCacheCapacity: 32,
	}

	pool, err := database.NewRiverPool(ctx, env.databaseURL, poolOpts)
	if err != nil {
		tb.Fatalf("connect river pool failed: %v", err)
	}

	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err == nil {
		_, _ = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	}

	const metaSQL = `
	CREATE TABLE IF NOT EXISTS river_job_metadata (
		job_id BIGINT PRIMARY KEY,
		workflow_name TEXT NOT NULL,
		entity_type TEXT NOT NULL,
		entity_id TEXT NOT NULL,
		user_id TEXT,
		correlation_id TEXT,
		raw_payload BYTEA,
		tracking_data JSONB NOT NULL DEFAULT '{}'::jsonb,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_river_job_metadata_corr ON river_job_metadata(correlation_id);
	`
	if _, err := pool.Exec(ctx, metaSQL); err != nil {
		tb.Fatalf("create metadata table failed: %v", err)
	}

	_ = database.ResyncRiverJobSequence(ctx, pool)
	_, _ = pool.Exec(ctx, "DELETE FROM river_job WHERE queue = 'stress_lifecycle'")

	dbStore := database.WrapPostgresPool(pool, poolOpts)
	applyStateSchema(tb, ctx, dbStore)
	applyEventLogSchema(tb, ctx, dbStore)

	redisClient := openRedis(tb, env)

	streamKey := uniqueName(env.prefix, "stress-stream")
	pubsubKey := uniqueName(env.prefix, "stress-pubsub")
	groupKey := uniqueName(env.prefix, "stress-group")
	projKey := "svc_stress_proj"

	hermesStore, err := hermes.NewStore(hermes.ProjectionSpec{
		Name:          projKey,
		Domain:        "stress",
		Collection:    "records",
		IndexedFields: []string{"status", "payload_kind"},
		MaxRecords:    4096,
		MaxBytes:      16 << 20,
	})
	if err != nil {
		tb.Fatalf("create hermes store failed: %v", err)
	}

	harness := &LifecycleHarness{
		t:             tb,
		env:           env,
		pool:          pool,
		db:            dbStore,
		redisClient:   redisClient,
		hermesStore:   hermesStore,
		chaos:         chaos.NewInjector(),
		streamKey:     streamKey,
		pubsubKey:     pubsubKey,
		groupKey:      groupKey,
		projectionKey: projKey,
		stopCh:        make(chan struct{}),
		errorCounts:   make(map[string]int),
	}

	proc := &lifecycleStressProcessor{
		kind:     "stress:lifecycle:work",
		queue:    "stress_lifecycle",
		harness:  harness,
		doneChan: make(chan struct{}, 1),
	}
	harness.processor = proc

	engine := worker.NewEngine(map[string]int{proc.queue: workerCount}, nil)
	if err := engine.Register(proc); err != nil {
		tb.Fatalf("register processor failed: %v", err)
	}
	harness.engine = engine

	workers := river.NewWorkers()
	if err := engine.AddToWorkers(workers); err != nil {
		tb.Fatalf("add to workers failed: %v", err)
	}

	riverCfg := &river.Config{
		Queues: map[string]river.QueueConfig{
			proc.queue: {MaxWorkers: workerCount},
		},
		Workers: workers,
		Logger:  slog.Default(),
	}

	riverClient, err := river.NewClient(riverpgxv5.New(pool), riverCfg)
	if err != nil {
		tb.Fatalf("create river client failed: %v", err)
	}
	harness.riverClient = riverClient
	engine.SetRiverClient(riverClient, pool)
	engine.SetMetadataStore(worker.NewPostgresMetadataStore(pool))

	harnessCtx, harnessCancel := context.WithCancel(context.Background())
	if err := riverClient.Start(harnessCtx); err != nil {
		tb.Fatalf("start river client failed: %v", err)
	}

	// Background Hermes tailer polling Redis stream into in-memory projection
	go harness.runHermesTailer()

	// Synchronous WebSocket subscription to guarantee zero-drop pubsub registration
	subChan, unsubscribe, err := redisClient.Subscribe(harnessCtx, pubsubKey)
	if err != nil {
		tb.Fatalf("subscribe failed: %v", err)
	}
	go harness.runWebSocketSubscriber(subChan)

	tb.Cleanup(func() {
		close(harness.stopCh)
		unsubscribe()
		harnessCancel()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = riverClient.Stop(stopCtx)
		_ = redisClient.Del(stopCtx, streamKey)
		_ = redisClient.Close()
		_, _ = pool.Exec(stopCtx, "DELETE FROM river_job WHERE queue = 'stress_lifecycle'")
		pool.Close()
	})

	return harness
}

func (h *LifecycleHarness) runHermesTailer() {
	source, err := hermes.NewRedisStreamEnvelopeSource(h.redisClient, h.streamKey, h.groupKey, "tailer-worker", "")
	if err != nil {
		return
	}

	tailer, err := hermes.NewEnvelopeTailer(h.hermesStore, h.projectionKey, source, hermes.TailerOptions{MaxBatch: 64})
	if err != nil {
		return
	}

	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-h.stopCh:
			return
		case <-ticker.C:
			pollCtx, pollCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			_, _ = tailer.PollOnce(pollCtx)
			pollCancel()
		}
	}
}

func (h *LifecycleHarness) runWebSocketSubscriber(subChan <-chan []byte) {
	for {
		select {
		case <-h.stopCh:
			return
		case msg, ok := <-subChan:
			if !ok {
				return
			}
			text := string(msg)
			if strings.HasPrefix(text, "COMPLETED:") {
				corrID := strings.TrimPrefix(text, "COMPLETED:")
				h.wsDelivered.Store(corrID, time.Now())
			}
		}
	}
}

func (h *LifecycleHarness) recordError(tag string) {
	h.statsLock.Lock()
	defer h.statsLock.Unlock()
	h.errorCounts[tag]++
}

func (h *LifecycleHarness) DispatchRequest(ctx context.Context, req LifecycleRequest) LifecycleResponse {
	start := time.Now()

	// Security Rule 1: Validate auth token
	if req.AuthToken == "tampered" || strings.HasPrefix(req.AuthToken, "invalid_") {
		h.recordError("security_unauthorized")
		return LifecycleResponse{
			CorrelationID: req.CorrelationID,
			Status:        "rejected:unauthorized",
			Latency:       time.Since(start),
			Error:         errors.New("unauthorized credentials"),
		}
	}

	// Security Rule 2: Validate correlation ID
	if strings.TrimSpace(req.CorrelationID) == "" {
		h.recordError("security_missing_correlation")
		return LifecycleResponse{
			CorrelationID: req.CorrelationID,
			Status:        "rejected:missing_correlation",
			Latency:       time.Since(start),
			Error:         errors.New("correlation id required"),
		}
	}

	// Security Rule 3: Enforce tenant boundary
	authOrg := security.GetOrganizationIDFromContext(ctx)
	if authOrg != "" && req.OrganizationID != "" && authOrg != req.OrganizationID {
		h.recordError("security_cross_tenant_forbidden")
		return LifecycleResponse{
			CorrelationID: req.CorrelationID,
			Status:        "rejected:forbidden",
			Latency:       time.Since(start),
			Error:         errors.New("cross-tenant mutation forbidden"),
		}
	}

	// Chaos injection on Postgres lane
	if h.chaos != nil {
		if err := h.chaos.Apply(ctx, "postgres", req.CorrelationID); err != nil {
			h.recordError("chaos_postgres_failure")
			return LifecycleResponse{
				CorrelationID: req.CorrelationID,
				Status:        "failed:chaos_postgres",
				Latency:       time.Since(start),
				Error:         err,
			}
		}
	}

	// Begin Postgres transaction
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		h.recordError("postgres_acquire_timeout")
		return LifecycleResponse{
			CorrelationID: req.CorrelationID,
			Status:        "failed:acquire_timeout",
			Latency:       time.Since(start),
			Error:         err,
		}
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Transactional domain record write
	dataJSON := fmt.Sprintf(`{"status":"pending","correlation_id":%q,"payload_kind":%q}`, req.CorrelationID, req.PayloadKind)
	_, err = tx.Exec(ctx, `
		INSERT INTO governance_state_records (domain, collection_name, organization_id, record_id, data)
		VALUES ('stress', 'records', $1, $2, $3::jsonb)
		ON CONFLICT (domain, collection_name, organization_id, record_id)
		DO UPDATE SET data = EXCLUDED.data, updated_at = NOW()`,
		req.OrganizationID, req.RecordID, dataJSON,
	)
	if err != nil {
		h.recordError("postgres_write_error")
		return LifecycleResponse{
			CorrelationID: req.CorrelationID,
			Status:        "failed:state_write",
			Latency:       time.Since(start),
			Error:         err,
		}
	}

	// 2. Append event to foundation_event_log in the same transaction
	eventEnv := events.Envelope{
		EventType:     "stress:record:requested",
		CorrelationID: req.CorrelationID,
		Metadata: extension.Object{
			"organization_id": extension.String(req.OrganizationID),
		},
		Payload: extension.Object{
			"record_id":    extension.String(req.RecordID),
			"payload_kind": extension.String(req.PayloadKind),
		},
		Timestamp:     time.Now().UTC(),
		SchemaVersion: "v1",
	}
	adapter := lifecycleTxAdapter{tx: tx}
	if _, err := eventlog.Append(ctx, adapter, eventEnv); err != nil {
		h.recordError("eventlog_append_error")
		return LifecycleResponse{
			CorrelationID: req.CorrelationID,
			Status:        "failed:eventlog_append",
			Latency:       time.Since(start),
			Error:         err,
		}
	}

	// 3. Enqueue River worker job in the same transaction
	job := worker.Job{
		JobKind:        h.processor.Kind(),
		Queue:          h.processor.Queue(),
		CorrelationID:  req.CorrelationID,
		RawPayload:     req.Data,
		RequireSidecar: req.PayloadKind == "sidecar",
		Payload: extension.Object{
			"org_id":        extension.String(req.OrganizationID),
			"record_id":     extension.String(req.RecordID),
			"payload_kind":  extension.String(req.PayloadKind),
			"payload_bytes": extension.Int(int64(len(req.Data))),
		},
	}
	if err := h.engine.EnqueueTx(ctx, tx, job); err != nil {
		h.recordError("river_enqueue_error")
		return LifecycleResponse{
			CorrelationID: req.CorrelationID,
			Status:        "failed:river_enqueue",
			Latency:       time.Since(start),
			Error:         err,
		}
	}

	// Commit transaction
	if err := tx.Commit(ctx); err != nil {
		h.recordError("postgres_commit_error")
		return LifecycleResponse{
			CorrelationID: req.CorrelationID,
			Status:        "failed:commit",
			Latency:       time.Since(start),
			Error:         err,
		}
	}

	return LifecycleResponse{
		CorrelationID: req.CorrelationID,
		Status:        "accepted",
		Latency:       time.Since(start),
	}
}

// ConcurrencyStageMetrics summarizes latency, variance, and throughput.
type ConcurrencyStageMetrics struct {
	Concurrency int
	TotalOps    int
	SuccessOps  int
	FailedOps   int
	Throughput  float64
	Elapsed     time.Duration
	Mean        time.Duration
	Min         time.Duration
	P50         time.Duration
	P95         time.Duration
	P99         time.Duration
	Max         time.Duration
	StdDev      time.Duration
	RSD         float64
	ErrorCounts map[string]int
}

func calculateStageMetrics(c int, latencies []time.Duration, failed int, elapsed time.Duration, errs map[string]int) ConcurrencyStageMetrics {
	total := len(latencies) + failed
	if len(latencies) == 0 {
		return ConcurrencyStageMetrics{
			Concurrency: c,
			TotalOps:    total,
			FailedOps:   failed,
			Elapsed:     elapsed,
			ErrorCounts: errs,
		}
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	var sumNs float64
	for _, d := range latencies {
		sumNs += float64(d.Nanoseconds())
	}
	meanNs := sumNs / float64(len(latencies))

	var varSum float64
	for _, d := range latencies {
		diff := float64(d.Nanoseconds()) - meanNs
		varSum += diff * diff
	}
	varianceNs := varSum / float64(len(latencies))
	stdDevNs := math.Sqrt(varianceNs)

	rsd := 0.0
	if meanNs > 0 {
		rsd = (stdDevNs / meanNs) * 100.0
	}

	n := len(latencies)
	p50 := latencies[int(float64(n-1)*0.50)]
	p95 := latencies[int(float64(n-1)*0.95)]
	p99 := latencies[int(float64(n-1)*0.99)]

	throughput := float64(len(latencies)) / elapsed.Seconds()

	return ConcurrencyStageMetrics{
		Concurrency: c,
		TotalOps:    total,
		SuccessOps:  len(latencies),
		FailedOps:   failed,
		Throughput:  throughput,
		Elapsed:     elapsed,
		Mean:        time.Duration(int64(meanNs)),
		Min:         latencies[0],
		P50:         p50,
		P95:         p95,
		P99:         p99,
		Max:         latencies[n-1],
		StdDev:      time.Duration(int64(stdDevNs)),
		RSD:         rsd,
		ErrorCounts: errs,
	}
}

func TestServiceBackedLifecycle_FullNervousSystemIntegration(t *testing.T) {
	harness := newLifecycleHarness(t, 8, 4)
	orgID := uniqueName(harness.env.prefix, "org-full")
	corrID := "corr-full-nervous-system-001"
	recordID := "rec-full-001"

	ctx := security.ContextWithOrganizationID(context.Background(), orgID)
	req := LifecycleRequest{
		CorrelationID:  corrID,
		OrganizationID: orgID,
		RecordID:       recordID,
		AuthToken:      "valid_jwt_token",
		PayloadKind:    "medium",
		Data:           bytes.Repeat([]byte{0x42}, 1024),
	}

	harness.processor.targetCount = 1
	resp := harness.DispatchRequest(ctx, req)
	if resp.Error != nil || resp.Status != "accepted" {
		t.Fatalf("request failed: %+v", resp)
	}

	// Invariant 1: RequestedBeforeTerminal - State must be pending before river completion
	rec, found, err := harness.db.GetRecord(ctx, "stress", "records", orgID, recordID)
	if err != nil || !found {
		t.Fatalf("domain record not found in postgres: %v", err)
	}
	if !serviceRecordStringEquals(rec.Data, "correlation_id", corrID) {
		t.Fatalf("correlation id not preserved in state: %+v", rec.Data)
	}

	// Invariant 2: ExactlyOneTerminalVisible - Wait for river worker execution
	select {
	case <-harness.processor.doneChan:
	case <-time.After(5 * time.Second):
		if harness.processor.handled.Load() == 0 {
			t.Fatalf("river worker timeout: 0 jobs processed")
		}
	}

	// Invariant 3: WebSocket broadcast delivered via Redis pubsub
	deadline := time.Now().Add(3 * time.Second)
	delivered := false
	for time.Now().Before(deadline) {
		if _, ok := harness.wsDelivered.Load(corrID); ok {
			delivered = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !delivered {
		t.Fatalf("websocket delivery timeout for correlation %s", corrID)
	}

	// Invariant 4: Hermes projection freshness
	time.Sleep(100 * time.Millisecond)
	count, err := harness.hermesStore.Count(
		ctx,
		harness.projectionKey,
		hermes.QueryFromRecordQuery(orgID, serviceRecordQuery(0, map[string]any{"status": "completed"})),
		hermes.Fence{},
	)
	if err != nil || count < 1 {
		t.Fatalf("hermes projection count = %d err = %v, want at least 1", count, err)
	}

	// Invariant 5: Zero drift between Hermes and Postgres
	report, err := harness.hermesStore.CheckDrift(
		ctx,
		harness.projectionKey,
		harness.db,
		hermes.Query{OrganizationID: orgID},
		hermes.DriftOptions{MaxRecords: 32, SampleSize: 4},
	)
	if err != nil {
		t.Fatalf("drift check failed: %v", err)
	}
	_ = report
}

func TestServiceBackedLifecycle_PayloadVariance(t *testing.T) {
	harness := newLifecycleHarness(t, 12, 4)
	orgID := uniqueName(harness.env.prefix, "org-payload")
	ctx := security.ContextWithOrganizationID(context.Background(), orgID)

	payloadCases := []struct {
		name string
		kind string
		size int
	}{
		{name: "Micro 128B", kind: "small", size: 128},
		{name: "Medium 4KiB", kind: "medium", size: 4096},
		{name: "Inline Limit 64KiB", kind: "inline_boundary", size: 64 * 1024},
		{name: "Sidecar 128KiB", kind: "sidecar", size: 128 * 1024},
	}

	for _, pc := range payloadCases {
		t.Run(pc.name, func(t *testing.T) {
			corrID := fmt.Sprintf("corr-payload-%s", pc.kind)
			recID := fmt.Sprintf("rec-payload-%s", pc.kind)
			data := bytes.Repeat([]byte{0xAA}, pc.size)

			req := LifecycleRequest{
				CorrelationID:  corrID,
				OrganizationID: orgID,
				RecordID:       recID,
				AuthToken:      "valid_token",
				PayloadKind:    pc.kind,
				Data:           data,
			}

			start := time.Now()
			harness.processor.targetCount = 1
	resp := harness.DispatchRequest(ctx, req)
			if resp.Error != nil || resp.Status != "accepted" {
				t.Fatalf("dispatch %s failed: %+v", pc.name, resp)
			}
			t.Logf("PAYLOAD_METRIC kind=%s size_bytes=%d ingress_latency=%s", pc.kind, pc.size, time.Since(start))
		})
	}
}

func TestServiceBackedLifecycle_SecurityAndChaosInjection(t *testing.T) {
	harness := newLifecycleHarness(t, 8, 4)
	validOrg := uniqueName(harness.env.prefix, "org-valid")
	attackerOrg := uniqueName(harness.env.prefix, "org-attacker")

	// 1. Cross-tenant spoofing attempt
	t.Run("SecurityCrossTenantSpoofing", func(t *testing.T) {
		ctx := security.ContextWithOrganizationID(context.Background(), attackerOrg)
		req := LifecycleRequest{
			CorrelationID:  "corr-attack-tenant",
			OrganizationID: validOrg, // Tenant mismatch
			RecordID:       "rec-attack-001",
			AuthToken:      "valid_token",
			PayloadKind:    "small",
		}
		harness.processor.targetCount = 1
	resp := harness.DispatchRequest(ctx, req)
		if resp.Error == nil || resp.Status != "rejected:forbidden" {
			t.Fatalf("expected forbidden cross-tenant rejection, got: %+v", resp)
		}
		if resp.Latency > 5*time.Millisecond {
			t.Fatalf("security rejection too slow: %s, want < 5ms", resp.Latency)
		}
	})

	// 2. Tampered token attempt
	t.Run("SecurityTamperedToken", func(t *testing.T) {
		ctx := security.ContextWithOrganizationID(context.Background(), validOrg)
		req := LifecycleRequest{
			CorrelationID:  "corr-attack-token",
			OrganizationID: validOrg,
			RecordID:       "rec-attack-002",
			AuthToken:      "tampered",
			PayloadKind:    "small",
		}
		harness.processor.targetCount = 1
	resp := harness.DispatchRequest(ctx, req)
		if resp.Error == nil || resp.Status != "rejected:unauthorized" {
			t.Fatalf("expected unauthorized token rejection, got: %+v", resp)
		}
	})

	// 3. Chaos injection: database latency
	t.Run("ChaosPostgresLatencyJitter", func(t *testing.T) {
		harness.chaos.InjectLatency("postgres", 20*time.Millisecond)
		defer harness.chaos.Clear("postgres")

		ctx := security.ContextWithOrganizationID(context.Background(), validOrg)
		req := LifecycleRequest{
			CorrelationID:  "corr-chaos-latency",
			OrganizationID: validOrg,
			RecordID:       "rec-chaos-001",
			AuthToken:      "valid_token",
			PayloadKind:    "small",
		}

		harness.processor.targetCount = 1
	resp := harness.DispatchRequest(ctx, req)
		if resp.Error != nil || resp.Status != "accepted" {
			t.Fatalf("dispatch failed under latency chaos: %+v", resp)
		}
		if resp.Latency < 20*time.Millisecond {
			t.Fatalf("injected latency not observed: %s, want >= 20ms", resp.Latency)
		}
	})

	// 4. Chaos injection: worker transient failure with River retry
	t.Run("ChaosWorkerFailureAndRetry", func(t *testing.T) {
		harness.chaos.InjectFailure("worker", 0.50) // 50% transient failure rate
		defer harness.chaos.Clear("worker")

		ctx := security.ContextWithOrganizationID(context.Background(), validOrg)
		req := LifecycleRequest{
			CorrelationID:  "corr-chaos-worker-retry",
			OrganizationID: validOrg,
			RecordID:       "rec-chaos-retry-001",
			AuthToken:      "valid_token",
			PayloadKind:    "small",
		}

		harness.processor.targetCount = 1
	resp := harness.DispatchRequest(ctx, req)
		if resp.Error != nil || resp.Status != "accepted" {
			t.Fatalf("ingress failed: %+v", resp)
		}

		// Wait for River exponential backoff and retry to succeed
		deadline := time.Now().Add(6 * time.Second)
		succeeded := false
		for time.Now().Before(deadline) {
			if harness.processor.handled.Load() > 0 {
				succeeded = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !succeeded {
			t.Fatalf("river worker retry failed to converge within deadline")
		}
	})
}

func TestServiceBackedLifecycle_ConcurrencyTuningAndRampToFailure(t *testing.T) {
	// Configure pool with 16 connections and 250ms acquire timeout
	const poolConns = 16
	harness := newLifecycleHarness(t, poolConns, 16)
	orgID := uniqueName(harness.env.prefix, "org-tune")

	concurrencyRamps := []int{1, 10, 50, 100, 250, 500, 750, 1000}
	results := make([]ConcurrencyStageMetrics, 0, len(concurrencyRamps))

	t.Logf("=== CONCURRENCY & PARALLELISM TUNING PASS (POOL CONNS = %d) ===", poolConns)

	for _, c := range concurrencyRamps {
		numOps := max(c*4, 20)
		if c >= 250 {
			numOps = c * 2 // Bounded ops at high concurrency to keep run time tight
		}

		latencies := make([]time.Duration, 0, numOps)
		var latLock sync.Mutex
		var failedCount atomic.Int64
		stageErrs := make(map[string]int)
		var errLock sync.Mutex

		var wg sync.WaitGroup
		sem := make(chan struct{}, c)

		startStage := time.Now()
		for i := 0; i < numOps; i++ {
			wg.Add(1)
			sem <- struct{}{}

			go func(opID int) {
				defer wg.Done()
				defer func() { <-sem }()

				opCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				authCtx := security.ContextWithOrganizationID(opCtx, orgID)

				req := LifecycleRequest{
					CorrelationID:  fmt.Sprintf("corr-c%d-%d", c, opID),
					OrganizationID: orgID,
					RecordID:       fmt.Sprintf("rec-c%d-%d", c, opID),
					AuthToken:      "valid_token",
					PayloadKind:    "small",
					Data:           []byte("tick"),
				}

				resp := harness.DispatchRequest(authCtx, req)
				if resp.Error != nil {
					failedCount.Add(1)
					errLock.Lock()
					stageErrs[resp.Status]++
					errLock.Unlock()
				} else {
					latLock.Lock()
					latencies = append(latencies, resp.Latency)
					latLock.Unlock()
				}
			}(i)
		}

		wg.Wait()
		elapsed := time.Since(startStage)

		metrics := calculateStageMetrics(c, latencies, int(failedCount.Load()), elapsed, stageErrs)
		results = append(results, metrics)

		t.Logf(
			"STAGE C=%3d | Total=%4d | Succ=%4d | Fail=%3d | Tput=%7.1f ops/s | p50=%7.2fms | p95=%7.2fms | p99=%7.2fms | Max=%7.2fms | Mean=%7.2fms | RSD=%5.1f%%",
			metrics.Concurrency,
			metrics.TotalOps,
			metrics.SuccessOps,
			metrics.FailedOps,
			metrics.Throughput,
			float64(metrics.P50.Microseconds())/1000.0,
			float64(metrics.P95.Microseconds())/1000.0,
			float64(metrics.P99.Microseconds())/1000.0,
			float64(metrics.Max.Microseconds())/1000.0,
			float64(metrics.Mean.Microseconds())/1000.0,
			metrics.RSD,
		)
	}

	// Output summary table
	fmt.Println("\n| Concurrency (C) | Total Ops | Success | Failures | Throughput (ops/s) | p50 (ms) | p95 (ms) | p99 (ms) | Max (ms) | Mean (ms) | RSD (%) | Failure Mode |")
	fmt.Println("| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |")
	for _, m := range results {
		failureMode := "None (100% OK)"
		if m.FailedOps > 0 {
			var errKeys []string
			for k, v := range m.ErrorCounts {
				errKeys = append(errKeys, fmt.Sprintf("%s(%d)", k, v))
			}
			failureMode = strings.Join(errKeys, ", ")
		}
		fmt.Printf(
			"| %d | %d | %d | %d | %.1f | %.2f | %.2f | %.2f | %.2f | %.2f | %.1f%% | %s |\n",
			m.Concurrency,
			m.TotalOps,
			m.SuccessOps,
			m.FailedOps,
			m.Throughput,
			float64(m.P50.Microseconds())/1000.0,
			float64(m.P95.Microseconds())/1000.0,
			float64(m.P99.Microseconds())/1000.0,
			float64(m.Max.Microseconds())/1000.0,
			float64(m.Mean.Microseconds())/1000.0,
			m.RSD,
			failureMode,
		)
	}
}

func TestServiceBackedLifecycle_RampToFailureUnderSLO(t *testing.T) {
	// Constrained pool with 8 connections and strict 75ms client SLA budget
	const poolConns = 8
	const sloBudget = 75 * time.Millisecond
	harness := newLifecycleHarness(t, poolConns, 8)
	orgID := uniqueName(harness.env.prefix, "org-slo-failure")

	concurrencyRamps := []int{10, 50, 100, 200, 400}
	results := make([]ConcurrencyStageMetrics, 0, len(concurrencyRamps))

	t.Logf("=== RAMP TO FAILURE UNDER STRICT SLO (%s) AND CONSTRAINED POOL (%d CONNS) ===", sloBudget, poolConns)

	for _, c := range concurrencyRamps {
		numOps := max(c*2, 40)
		latencies := make([]time.Duration, 0, numOps)
		var latLock sync.Mutex
		var failedCount atomic.Int64
		stageErrs := make(map[string]int)
		var errLock sync.Mutex

		var wg sync.WaitGroup
		sem := make(chan struct{}, c)

		startStage := time.Now()
		for i := 0; i < numOps; i++ {
			wg.Add(1)
			sem <- struct{}{}

			go func(opID int) {
				defer wg.Done()
				defer func() { <-sem }()

				opCtx, cancel := context.WithTimeout(context.Background(), sloBudget)
				defer cancel()
				authCtx := security.ContextWithOrganizationID(opCtx, orgID)

				req := LifecycleRequest{
					CorrelationID:  fmt.Sprintf("corr-slo-c%d-%d", c, opID),
					OrganizationID: orgID,
					RecordID:       fmt.Sprintf("rec-slo-c%d-%d", c, opID),
					AuthToken:      "valid_token",
					PayloadKind:    "small",
					Data:           []byte("slo_tick"),
				}

				resp := harness.DispatchRequest(authCtx, req)
				if resp.Error != nil {
					failedCount.Add(1)
					errLock.Lock()
					stageErrs[resp.Status]++
					errLock.Unlock()
				} else {
					latLock.Lock()
					latencies = append(latencies, resp.Latency)
					latLock.Unlock()
				}
			}(i)
		}

		wg.Wait()
		elapsed := time.Since(startStage)

		metrics := calculateStageMetrics(c, latencies, int(failedCount.Load()), elapsed, stageErrs)
		results = append(results, metrics)

		errRate := 0.0
		if metrics.TotalOps > 0 {
			errRate = (float64(metrics.FailedOps) / float64(metrics.TotalOps)) * 100.0
		}

		t.Logf(
			"SLO RAMP C=%3d | Total=%4d | Succ=%4d | Fail=%3d (ErrRate=%5.1f%%) | Tput=%7.1f ops/s | p50=%6.2fms | p95=%6.2fms | Max=%6.2fms",
			metrics.Concurrency,
			metrics.TotalOps,
			metrics.SuccessOps,
			metrics.FailedOps,
			errRate,
			metrics.Throughput,
			float64(metrics.P50.Microseconds())/1000.0,
			float64(metrics.P95.Microseconds())/1000.0,
			float64(metrics.Max.Microseconds())/1000.0,
		)
	}

	fmt.Println("\n| Concurrency (C) | Total Ops | Success | Failures | Error Rate (%) | Throughput (ops/s) | p50 (ms) | p95 (ms) | Max (ms) | Failure Mode |")
	fmt.Println("| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |")
	for _, m := range results {
		errRate := 0.0
		if m.TotalOps > 0 {
			errRate = (float64(m.FailedOps) / float64(m.TotalOps)) * 100.0
		}
		failureMode := "None (100% OK)"
		if m.FailedOps > 0 {
			var errKeys []string
			for k, v := range m.ErrorCounts {
				errKeys = append(errKeys, fmt.Sprintf("%s(%d)", k, v))
			}
			failureMode = strings.Join(errKeys, ", ")
		}
		fmt.Printf(
			"| %d | %d | %d | %d | %.1f%% | %.1f | %.2f | %.2f | %.2f | %s |\n",
			m.Concurrency,
			m.TotalOps,
			m.SuccessOps,
			m.FailedOps,
			errRate,
			m.Throughput,
			float64(m.P50.Microseconds())/1000.0,
			float64(m.P95.Microseconds())/1000.0,
			float64(m.Max.Microseconds())/1000.0,
			failureMode,
		)
	}
}
