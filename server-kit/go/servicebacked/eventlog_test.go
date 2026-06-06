//go:build servicebacked

package servicebacked

import (
	"context"
	"fmt"
	"testing"
	"time"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/eventlog"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
)

func TestServiceBackedEventLogToRedisToHermes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	env := requireServiceEnv(t)
	state := openPostgres(t, env, serviceBackedPoolOptions(4))
	defer state.Close()
	applyStateSchema(t, ctx, state)
	applyEventLogSchema(t, ctx, state)

	redisClient := openRedis(t, env)
	defer redisClient.Close()

	orgID := uniqueName(env.prefix, "eventlog-org")
	cleanupOrganization(t, ctx, state, orgID)
	stream := uniqueName(env.prefix, "eventlog-stream")
	t.Cleanup(func() {
		deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer deleteCancel()
		_ = redisClient.Del(deleteCtx, stream)
	})

	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name:          "svc_eventlog_ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"bucket"},
		MaxRecords:    32,
		MaxBytes:      1 << 20,
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	envelope, err := hermes.NewProjectionEnvelope([]*foundationpb.RecordMutation{{
		Operation:      foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT,
		SourceId:       "eventlog:tick",
		Version:        1,
		Domain:         "signals",
		Collection:     "ticks",
		OrganizationId: orgID,
		RecordId:       "tick-eventlog",
		CorrelationId:  "corr-service-backed-eventlog",
		Fields: []*foundationpb.FieldValue{
			projectionField("bucket", "7"),
			projectionField("source", "eventlog"),
		},
	}}, "corr-service-backed-eventlog")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() error = %v", err)
	}
	envelope.Metadata["organization_id"] = extension.String(orgID)

	if _, err := state.UpsertRecord(ctx, database.DomainRecord{
		Domain:         "signals",
		Collection:     "ticks",
		OrganizationID: orgID,
		RecordID:       "tick-eventlog",
		Data:           serviceRecordData(map[string]any{"bucket": "7", "source": "eventlog"}),
	}); err != nil {
		t.Fatalf("state upsert failed: %v", err)
	}
	entry, err := eventlog.Append(ctx, state, envelope)
	if err != nil {
		t.Fatalf("eventlog Append() error = %v", err)
	}
	if entry.ID == 0 || len(entry.Envelope) == 0 {
		t.Fatalf("eventlog entry not populated: %+v", entry)
	}

	result, err := eventlog.PublishPending(ctx, state, redisClient, eventlog.PublishOptions{
		Stream: stream,
		Limit:  4,
	})
	if err != nil || result.Published != 1 {
		t.Fatalf("PublishPending() result=%+v err=%v", result, err)
	}

	source, err := hermes.NewRedisStreamEnvelopeSource(redisClient, stream, uniqueName(env.prefix, "eventlog-group"), "consumer-a", eventlog.DefaultStreamField)
	if err != nil {
		t.Fatalf("NewRedisStreamEnvelopeSource() error = %v", err)
	}
	tailer, err := hermes.NewEnvelopeTailer(store, "svc_eventlog_ticks", source, hermes.TailerOptions{MaxBatch: 8})
	if err != nil {
		t.Fatalf("NewEnvelopeTailer() error = %v", err)
	}
	applied, err := tailer.PollOnce(ctx)
	if err != nil || applied.Acked != 1 || applied.Apply.Applied != 1 {
		t.Fatalf("PollOnce() result=%+v err=%v", applied, err)
	}

	count, err := store.Count(ctx, "svc_eventlog_ticks", hermes.QueryFromRecordQuery(orgID, serviceRecordQuery(0, map[string]any{"bucket": "7"})), hermes.Fence{})
	if err != nil || count != 1 {
		t.Fatalf("Hermes Count() = %d err=%v, want 1", count, err)
	}
	report, err := store.CheckDrift(ctx, "svc_eventlog_ticks", state, hermes.Query{OrganizationID: orgID}, hermes.DriftOptions{MaxRecords: 32, SampleSize: 4})
	if err != nil || !report.OK() {
		t.Fatalf("CheckDrift() ok=%v report=%+v err=%v", report.OK(), report, err)
	}
}

func TestServiceBackedEventLogBatchPublishPending(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	env := requireServiceEnv(t)
	state := openPostgres(t, env, serviceBackedPoolOptions(4))
	defer state.Close()
	applyEventLogSchema(t, ctx, state)

	redisClient := openRedis(t, env)
	defer redisClient.Close()

	stream := uniqueName(env.prefix, "eventlog-batch-stream")
	t.Cleanup(func() {
		deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer deleteCancel()
		_ = redisClient.Del(deleteCtx, stream)
	})

	for i := 0; i < 3; i++ {
		envelope := events.Envelope{
			ID:              fmt.Sprintf("evt_service_batch_%d_%d", time.Now().UnixNano(), i),
			EventType:       "signals:tick:success",
			PayloadEncoding: events.PayloadEncodingJSON,
			CorrelationID:   fmt.Sprintf("corr-service-batch-%d", i),
			SchemaVersion:   events.EnvelopeSchemaVersion,
			Metadata:        serviceObject(map[string]any{"organization_id": "org-service-batch"}),
			Payload:         serviceObject(map[string]any{"record_id": fmt.Sprintf("record-%d", i)}),
			Timestamp:       time.Now().UTC(),
		}
		if _, err := eventlog.Append(ctx, state, envelope); err != nil {
			t.Fatalf("Append(%d) error = %v", i, err)
		}
	}

	result, err := eventlog.PublishPending(ctx, state, redisClient, eventlog.PublishOptions{Stream: stream, Limit: 8})
	if err != nil || result.Read != 3 || result.Published != 3 || result.Failed != 0 {
		t.Fatalf("PublishPending(batch) result=%+v err=%v, want 3 published", result, err)
	}
	pending, err := eventlog.FetchPending(ctx, state, 8, eventlog.DefaultMaxAttempts)
	if err != nil || len(pending) != 0 {
		t.Fatalf("FetchPending(after batch publish) len=%d err=%v, want 0", len(pending), err)
	}
	messages, err := redisClient.XReadGroup(ctx, stream, uniqueName(env.prefix, "eventlog-batch-group"), "consumer-a", 8)
	if err != nil || len(messages) != 3 {
		t.Fatalf("redis stream messages len=%d err=%v, want 3", len(messages), err)
	}
}

func TestServiceBackedEventLogConcurrentPublishClaimsDoNotDuplicate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	env := requireServiceEnv(t)
	state := openPostgres(t, env, serviceBackedPoolOptions(8))
	defer state.Close()
	applyEventLogSchema(t, ctx, state)

	redisClient := openRedis(t, env)
	defer redisClient.Close()

	stream := uniqueName(env.prefix, "eventlog-claim-stream")
	t.Cleanup(func() {
		deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer deleteCancel()
		_ = redisClient.Del(deleteCtx, stream)
	})

	const eventCount = 8
	for i := 0; i < eventCount; i++ {
		envelope := events.Envelope{
			ID:              fmt.Sprintf("evt_service_claim_%d_%d", time.Now().UnixNano(), i),
			EventType:       "signals:claim:success",
			PayloadEncoding: events.PayloadEncodingJSON,
			CorrelationID:   fmt.Sprintf("corr-service-claim-%d", i),
			SchemaVersion:   events.EnvelopeSchemaVersion,
			Metadata:        serviceObject(map[string]any{"organization_id": "org-service-claim"}),
			Payload:         serviceObject(map[string]any{"record_id": fmt.Sprintf("claim-record-%d", i)}),
			Timestamp:       time.Now().UTC(),
		}
		if _, err := eventlog.Append(ctx, state, envelope); err != nil {
			t.Fatalf("Append(%d) error = %v", i, err)
		}
	}

	const drainers = 4
	start := make(chan struct{})
	results := make(chan publishOutcome, drainers)
	for i := 0; i < drainers; i++ {
		go func(index int) {
			<-start
			result, err := eventlog.PublishPending(ctx, state, redisClient, eventlog.PublishOptions{
				Stream:   stream,
				Limit:    2,
				ClaimTTL: 10 * time.Second,
			})
			results <- publishOutcome{result: result, err: err, drainer: index}
		}(i)
	}
	close(start)

	totalPublished := 0
	for i := 0; i < drainers; i++ {
		outcome := <-results
		if outcome.err != nil {
			t.Fatalf("drainer %d PublishPending() result=%+v err=%v", outcome.drainer, outcome.result, outcome.err)
		}
		if outcome.result.Failed != 0 {
			t.Fatalf("drainer %d result=%+v, want no failures", outcome.drainer, outcome.result)
		}
		totalPublished += outcome.result.Published
	}
	if totalPublished != eventCount {
		t.Fatalf("total published=%d, want %d", totalPublished, eventCount)
	}
	pending, err := eventlog.FetchPending(ctx, state, eventCount, eventlog.DefaultMaxAttempts)
	if err != nil || len(pending) != 0 {
		t.Fatalf("FetchPending(after concurrent claim publish) len=%d err=%v, want 0", len(pending), err)
	}
	messages, err := redisClient.XReadGroup(ctx, stream, uniqueName(env.prefix, "eventlog-claim-group"), "consumer-a", eventCount)
	if err != nil || len(messages) != eventCount {
		t.Fatalf("redis stream messages len=%d err=%v, want %d", len(messages), err, eventCount)
	}
	seen := make(map[string]struct{}, eventCount)
	for _, message := range messages {
		rawValue, ok := message.Values.Get(eventlog.DefaultStreamField)
		raw, rawOK := streamEnvelopeBytes(rawValue)
		if !ok || !rawOK {
			t.Fatalf("stream message %s missing envelope bytes: %#v", message.ID, message.Values)
		}
		envelope, err := events.FromBinary(raw)
		if err != nil {
			t.Fatalf("stream message %s envelope decode failed: %v", message.ID, err)
		}
		if _, duplicate := seen[envelope.ID]; duplicate {
			t.Fatalf("duplicate event id published: %s", envelope.ID)
		}
		seen[envelope.ID] = struct{}{}
	}
}

func BenchmarkServiceBackedEventLogPublishPending64(b *testing.B) {
	env := requireServiceEnv(b)
	ctx := context.Background()
	state := openPostgres(b, env, serviceBackedPoolOptions(8))
	defer state.Close()
	applyEventLogSchema(b, ctx, state)

	redisClient := openRedis(b, env)
	defer redisClient.Close()
	stream := uniqueName(env.prefix, "bench-eventlog-stream")
	b.Cleanup(func() {
		deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer deleteCancel()
		_ = redisClient.Del(deleteCtx, stream)
	})

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		seedEventLogPendingRows(b, ctx, state, env.prefix, i*64, 64)
		b.StartTimer()
		result, err := eventlog.PublishPending(ctx, state, redisClient, eventlog.PublishOptions{
			Stream: stream,
			Limit:  64,
		})
		if err != nil || result.Published != 64 {
			b.Fatalf("PublishPending64 result=%+v err=%v, want 64 published", result, err)
		}
	}
	b.ReportMetric(64, "events/op")
}

type publishOutcome struct {
	result  eventlog.PublishResult
	err     error
	drainer int
}

func streamEnvelopeBytes(value any) ([]byte, bool) {
	switch typed := value.(type) {
	case []byte:
		return append([]byte(nil), typed...), true
	case string:
		return []byte(typed), true
	default:
		return nil, false
	}
}

func seedEventLogPendingRows(tb testing.TB, ctx context.Context, store database.RuntimeStore, prefix string, startID int, count int) {
	tb.Helper()
	const query = `
		INSERT INTO foundation_event_log (
			event_id,
			event_type,
			organization_id,
			correlation_id,
			schema_version,
			payload_encoding,
			envelope,
			metadata,
			occurred_at,
			source_node_id
		)
		SELECT
			$1 || '-' || gs::text,
			'signals:tick:success',
			$2,
			'corr-bench-' || gs::text,
			$3,
			$4,
			$5::bytea,
			'{}'::jsonb,
			NOW(),
			'service-backed-benchmark'
		FROM generate_series($6::int, $7::int) AS gs
	`
	err := store.Exec(ctx,
		query,
		uniqueName(prefix, "eventlog-bench"),
		"org-eventlog-bench",
		events.EnvelopeSchemaVersion,
		events.PayloadEncodingProtobuf,
		[]byte("foundation-eventlog-envelope"),
		startID,
		startID+count-1,
	)
	if err != nil {
		tb.Fatalf("seed event log pending rows failed: %v", err)
	}
}

func applyEventLogSchema(tb testing.TB, ctx context.Context, store database.RuntimeStore) {
	tb.Helper()
	for _, statement := range eventLogSchemaStatements() {
		if err := store.Exec(ctx, statement); err != nil {
			tb.Fatalf("apply event log schema failed: %v", err)
		}
	}
}

func eventLogSchemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS foundation_event_log (
			id BIGSERIAL PRIMARY KEY,
			event_id TEXT NOT NULL DEFAULT ('evt_' || gen_random_uuid()::text),
			event_type TEXT NOT NULL,
			organization_id TEXT NOT NULL DEFAULT '',
			correlation_id TEXT NOT NULL,
			schema_version TEXT NOT NULL,
			payload_encoding TEXT NOT NULL,
			envelope BYTEA NOT NULL,
			metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
			occurred_at TIMESTAMPTZ NOT NULL,
			source_node_id TEXT NOT NULL DEFAULT '',
			publish_stream TEXT,
			publish_stream_id TEXT,
			published_at TIMESTAMPTZ,
			publish_attempts INTEGER NOT NULL DEFAULT 0,
			last_publish_error TEXT,
			publish_claim_token TEXT,
			publish_claimed_at TIMESTAMPTZ,
			publish_claim_expires_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT foundation_event_log_event_unique UNIQUE (event_id),
			CONSTRAINT foundation_event_log_metadata_object CHECK (jsonb_typeof(metadata) = 'object')
		)`,
		`ALTER TABLE foundation_event_log
			ADD COLUMN IF NOT EXISTS publish_claim_token TEXT`,
		`ALTER TABLE foundation_event_log
			ADD COLUMN IF NOT EXISTS publish_claimed_at TIMESTAMPTZ`,
		`ALTER TABLE foundation_event_log
			ADD COLUMN IF NOT EXISTS publish_claim_expires_at TIMESTAMPTZ`,
		`CREATE INDEX IF NOT EXISTS idx_foundation_event_log_pending
			ON foundation_event_log (id)
			WHERE published_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_foundation_event_log_claim
			ON foundation_event_log (publish_claim_expires_at, id)
			WHERE published_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_foundation_event_log_org_time
			ON foundation_event_log (organization_id, occurred_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_foundation_event_log_type_time
			ON foundation_event_log (event_type, occurred_at DESC, id DESC)`,
		`COMMENT ON TABLE foundation_event_log IS 'Durable typed event facts. Operational logs must not feed Hermes.'`,
	}
}
