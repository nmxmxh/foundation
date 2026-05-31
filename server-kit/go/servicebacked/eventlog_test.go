//go:build servicebacked

package servicebacked

import (
	"context"
	"testing"
	"time"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/eventlog"
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
	envelope.Metadata["organization_id"] = orgID

	if _, err := state.UpsertRecord(ctx, database.DomainRecord{
		Domain:         "signals",
		Collection:     "ticks",
		OrganizationID: orgID,
		RecordID:       "tick-eventlog",
		Data:           map[string]any{"bucket": "7", "source": "eventlog"},
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

	count, err := store.Count(ctx, "svc_eventlog_ticks", hermes.Query{
		OrganizationID: orgID,
		Filters:        map[string]any{"bucket": "7"},
	}, hermes.Fence{})
	if err != nil || count != 1 {
		t.Fatalf("Hermes Count() = %d err=%v, want 1", count, err)
	}
	report, err := store.CheckDrift(ctx, "svc_eventlog_ticks", state, hermes.Query{OrganizationID: orgID}, hermes.DriftOptions{MaxRecords: 32, SampleSize: 4})
	if err != nil || !report.OK() {
		t.Fatalf("CheckDrift() ok=%v report=%+v err=%v", report.OK(), report, err)
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
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT foundation_event_log_event_unique UNIQUE (event_id),
			CONSTRAINT foundation_event_log_metadata_object CHECK (jsonb_typeof(metadata) = 'object')
		)`,
		`CREATE INDEX IF NOT EXISTS idx_foundation_event_log_pending
			ON foundation_event_log (id)
			WHERE published_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_foundation_event_log_org_time
			ON foundation_event_log (organization_id, occurred_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_foundation_event_log_type_time
			ON foundation_event_log (event_type, occurred_at DESC, id DESC)`,
		`COMMENT ON TABLE foundation_event_log IS 'Durable typed event facts. Operational logs must not feed Hermes.'`,
	}
}
