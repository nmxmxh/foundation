//go:build servicebacked

package servicebacked

import (
	"context"
	"fmt"
	"testing"
	"time"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
)

func TestServiceBackedHermesPostgresRedisDriftProof(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	env := requireServiceEnv(t)
	state := openPostgres(t, env, serviceBackedPoolOptions(4))
	defer state.Close()
	applyStateSchema(t, ctx, state)

	redisClient := openRedis(t, env)
	defer redisClient.Close()

	orgID := uniqueName(env.prefix, "hermes-org")
	cleanupOrganization(t, ctx, state, orgID)
	stream := uniqueName(env.prefix, "hermes-stream")
	group := uniqueName(env.prefix, "hermes-group")
	t.Cleanup(func() {
		deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer deleteCancel()
		_ = redisClient.Del(deleteCtx, stream)
	})

	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name:          "svc_hermes_ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"bucket", "source"},
		MaxRecords:    32,
		MaxBytes:      1 << 20,
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	baseRecord := database.DomainRecord{
		Domain:         "signals",
		Collection:     "ticks",
		OrganizationID: orgID,
		RecordID:       "tick-base",
		Data:           serviceRecordData(map[string]any{"bucket": int64(1), "source": "postgres"}),
	}
	if _, err := state.UpsertRecord(ctx, baseRecord); err != nil {
		t.Fatalf("postgres base upsert failed: %v", err)
	}
	if result, err := store.Rebuild(ctx, "svc_hermes_ticks", state, hermes.Query{OrganizationID: orgID}); err != nil || result.Applied != 1 {
		t.Fatalf("Rebuild() result=%+v err=%v", result, err)
	}

	streamRecord := database.DomainRecord{
		Domain:         "signals",
		Collection:     "ticks",
		OrganizationID: orgID,
		RecordID:       "tick-stream",
		Data:           serviceRecordData(map[string]any{"bucket": int64(2), "source": "redis"}),
	}
	if _, err := state.UpsertRecord(ctx, streamRecord); err != nil {
		t.Fatalf("postgres stream upsert failed: %v", err)
	}
	envelope, err := hermes.NewProjectionEnvelope([]*foundationpb.RecordMutation{{
		Operation:       foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT,
		SourceId:        "service-backed:tick-stream",
		Version:         2,
		Domain:          "signals",
		Collection:      "ticks",
		OrganizationId:  orgID,
		RecordId:        "tick-stream",
		CorrelationId:   "corr-service-backed-hermes",
		PayloadEncoding: foundationpb.PayloadEncoding_PAYLOAD_ENCODING_CAPNP,
		Fields: []*foundationpb.FieldValue{
			projectionField("bucket", int64(2)),
			projectionField("source", "redis"),
		},
	}}, "corr-service-backed-hermes")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() error = %v", err)
	}
	raw, err := envelope.ToBinary()
	if err != nil {
		t.Fatalf("Envelope.ToBinary() error = %v", err)
	}
	if _, err := redisClient.XAdd(ctx, stream, serviceRedisValues(map[string]any{"envelope": raw})); err != nil {
		t.Fatalf("redis XAdd() error = %v", err)
	}

	source, err := hermes.NewRedisStreamEnvelopeSource(redisClient, stream, group, "consumer-a", "")
	if err != nil {
		t.Fatalf("NewRedisStreamEnvelopeSource() error = %v", err)
	}
	tailer, err := hermes.NewEnvelopeTailer(store, "svc_hermes_ticks", source, hermes.TailerOptions{MaxBatch: 8})
	if err != nil {
		t.Fatalf("NewEnvelopeTailer() error = %v", err)
	}
	result, err := tailer.PollOnce(ctx)
	if err != nil || result.Acked != 1 || result.Apply.Applied != 1 {
		t.Fatalf("PollOnce() result=%+v err=%v", result, err)
	}
	empty, err := tailer.PollOnce(ctx)
	if err != nil || empty.Read != 0 || empty.Acked != 0 {
		t.Fatalf("second PollOnce() result=%+v err=%v, want empty", empty, err)
	}

	count, err := store.Count(ctx, "svc_hermes_ticks", hermes.QueryFromRecordQuery(orgID, serviceRecordQuery(0, map[string]any{"bucket": int64(2)})), hermes.Fence{})
	if err != nil || count != 1 {
		t.Fatalf("Hermes Count() = %d err=%v, want 1", count, err)
	}
	report, err := store.CheckDrift(ctx, "svc_hermes_ticks", state, hermes.Query{OrganizationID: orgID}, hermes.DriftOptions{MaxRecords: 32, SampleSize: 4})
	if err != nil || !report.OK() {
		t.Fatalf("CheckDrift() ok=%v report=%+v err=%v", report.OK(), report, err)
	}
}

func TestServiceBackedHermesRedisPendingWindowIsNotDuplicated(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	env := requireServiceEnv(t)
	redisClient := openRedis(t, env)
	defer redisClient.Close()

	stream := uniqueName(env.prefix, "hermes-pending")
	group := uniqueName(env.prefix, "hermes-pending-group")
	t.Cleanup(func() {
		deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer deleteCancel()
		_ = redisClient.Del(deleteCtx, stream)
	})

	envelope, err := hermes.NewProjectionEnvelope([]*foundationpb.RecordMutation{{
		Operation:      foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT,
		SourceId:       "service-backed:pending",
		Version:        1,
		Domain:         "signals",
		Collection:     "ticks",
		OrganizationId: "org-pending",
		RecordId:       "tick-pending",
		CorrelationId:  "corr-pending",
		Fields: []*foundationpb.FieldValue{
			projectionField("source", "redis"),
		},
	}}, "corr-pending")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope() error = %v", err)
	}
	raw, err := envelope.ToBinary()
	if err != nil {
		t.Fatalf("Envelope.ToBinary() error = %v", err)
	}
	if _, err := redisClient.XAdd(ctx, stream, serviceRedisValues(map[string]any{"envelope": raw})); err != nil {
		t.Fatalf("redis XAdd() error = %v", err)
	}
	source, err := hermes.NewRedisStreamEnvelopeSource(redisClient, stream, group, "consumer-a", "")
	if err != nil {
		t.Fatalf("NewRedisStreamEnvelopeSource() error = %v", err)
	}
	first, err := source.ReadEnvelopes(ctx, 1)
	if err != nil || len(first) != 1 {
		t.Fatalf("ReadEnvelopes(first) len=%d err=%v, want 1", len(first), err)
	}
	second, err := source.ReadEnvelopes(ctx, 1)
	if err != nil || len(second) != 1 || second[0].AckID != first[0].AckID {
		t.Fatalf("ReadEnvelopes(second) = %+v err=%v, want pending retry", second, err)
	}
	if err := source.Ack(ctx, first[0].AckID); err != nil {
		t.Fatalf("Ack() error = %v", err)
	}
	empty, err := source.ReadEnvelopes(ctx, 1)
	if err != nil || len(empty) != 0 {
		t.Fatalf("ReadEnvelopes(after ack) len=%d err=%v, want empty", len(empty), err)
	}
}

func projectionField(name string, value any) *foundationpb.FieldValue {
	field := &foundationpb.FieldValue{Name: name, Value: &foundationpb.ScalarValue{}}
	switch typed := value.(type) {
	case int64:
		field.Value.Kind = &foundationpb.ScalarValue_Int64Value{Int64Value: typed}
	case string:
		field.Value.Kind = &foundationpb.ScalarValue_StringValue{StringValue: typed}
	default:
		field.Value.Kind = &foundationpb.ScalarValue_StringValue{StringValue: fmt.Sprintf("%v", typed)}
	}
	return field
}
