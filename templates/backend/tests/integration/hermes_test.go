//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
	"github.com/stretchr/testify/require"

	"{{MODULE_PATH}}/tests/testutil"
)

func hermesRecordData(t testing.TB, values map[string]any) database.RecordData {
	t.Helper()
	fields := make([]database.RecordField, 0, len(values))
	for name, raw := range values {
		value, ok := database.RecordValueFromAny(raw)
		require.Truef(t, ok, "unsupported Hermes record data field %q", name)
		fields = append(fields, database.RecordField{Name: name, Value: value})
	}
	return database.RecordDataFromPairs(fields...)
}

func hermesRecordQuery(t testing.TB, limit int, values map[string]any) database.RecordQuery {
	t.Helper()
	filters := make([]database.RecordFilter, 0, len(values))
	for field, raw := range values {
		value, ok := database.RecordValueFromAny(raw)
		require.Truef(t, ok, "unsupported Hermes record query field %q", field)
		filters = append(filters, database.RecordFilter{Field: field, Value: value})
	}
	return database.RecordQuery{Filters: filters, Limit: limit}
}

func TestIntegrationHermes_RebuildAndRedisProjectionEnvelope(t *testing.T) {
	env := setupTestWithDB(t)
	require.NotNil(t, env.Redis, "redis should be available for Hermes stream integration")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	state, err := database.Connect(ctx, os.Getenv("TEST_DATABASE_URL"), database.DriverPostgres)
	require.NoError(t, err)
	t.Cleanup(state.Close)

	redisClient, err := rediskit.ConnectWithOptions(rediskit.Options{
		URL:    testutil.ResolveTestRedisURL(),
		Prefix: os.Getenv("REDIS_PREFIX"),
		Driver: rediskit.DriverRedis,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, redisClient.Close())
	})

	projection := "scaffold_hermes_probe"
	orgID := "org_hermes_integration"
	baseID := fmt.Sprintf("state_%d", time.Now().UnixNano())
	streamID := fmt.Sprintf("stream_%d", time.Now().UnixNano())

	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name:          projection,
		Domain:        "scaffold",
		Collection:    "hermes_probe",
		IndexedFields: []string{"bucket"},
		MaxRecords:    32,
		MaxBytes:      1 << 20,
	})
	require.NoError(t, err)

	_, err = state.UpsertRecord(ctx, database.DomainRecord{
		Domain:         "scaffold",
		Collection:     "hermes_probe",
		OrganizationID: orgID,
		RecordID:       baseID,
		Data:           hermesRecordData(t, map[string]any{"bucket": int64(1), "source": "postgres"}),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer deleteCancel()
		_ = state.DeleteRecord(deleteCtx, "scaffold", "hermes_probe", orgID, baseID)
	})

	rebuild, err := store.Rebuild(ctx, projection, state, hermes.Query{OrganizationID: orgID})
	require.NoError(t, err)
	require.Equal(t, 1, rebuild.Applied)

	envelope, err := hermes.NewProjectionEnvelope([]*foundationpb.RecordMutation{{
		Operation:       foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT,
		SourceId:        "integration:" + streamID,
		Version:         2,
		Domain:          "scaffold",
		Collection:      "hermes_probe",
		OrganizationId:  orgID,
		RecordId:        streamID,
		CorrelationId:   "corr_" + streamID,
		PayloadEncoding: foundationpb.PayloadEncoding_PAYLOAD_ENCODING_CAPNP,
		Fields: []*foundationpb.FieldValue{
			{
				Name: "bucket",
				Value: &foundationpb.ScalarValue{
					Kind: &foundationpb.ScalarValue_Int64Value{Int64Value: 2},
				},
			},
			{
				Name: "source",
				Value: &foundationpb.ScalarValue{
					Kind: &foundationpb.ScalarValue_StringValue{StringValue: "redis"},
				},
			},
		},
	}}, "corr_"+streamID)
	require.NoError(t, err)

	raw, err := envelope.ToBinary()
	require.NoError(t, err)

	stream := "hermes:integration:projection:" + streamID
	t.Cleanup(func() {
		deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer deleteCancel()
		_ = redisClient.Del(deleteCtx, stream)
	})
	_, err = redisClient.XAdd(ctx, stream, rediskit.Values{rediskit.Field("envelope", raw)})
	require.NoError(t, err)

	source, err := hermes.NewRedisStreamEnvelopeSource(redisClient, stream, "hermes", "integration", "")
	require.NoError(t, err)
	tailer, err := hermes.NewEnvelopeTailer(store, projection, source, hermes.TailerOptions{MaxBatch: 8})
	require.NoError(t, err)

	result, err := tailer.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.Acked)
	require.Equal(t, 1, result.Apply.Applied)

	count, err := store.Count(ctx, projection, hermes.QueryFromRecordQuery(orgID, hermesRecordQuery(t, 0, map[string]any{"bucket": int64(2)})), hermes.Fence{})
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}
