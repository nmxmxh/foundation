//go:build servicebacked

package servicebacked

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

func TestServiceBackedVariantMetadataDuplicationPatchDeleteConsistency(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	env := requireServiceEnv(t)
	state := openPostgres(t, env, serviceBackedPoolOptions(8))
	defer state.Close()
	applyStateSchema(t, ctx, state)

	rawStore, ok := state.(database.RawStateStore)
	if !ok {
		t.Fatalf("store %T does not implement RawStateStore", state)
	}

	redisClient := openRedis(t, env)
	defer redisClient.Close()

	orgID := uniqueName(env.prefix, "variant-org")
	cleanupOrganization(t, ctx, state, orgID)
	stream := uniqueName(env.prefix, "variant-stream")
	group := uniqueName(env.prefix, "variant-group")
	t.Cleanup(func() {
		deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer deleteCancel()
		_ = redisClient.Del(deleteCtx, stream)
	})

	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name:          "svc_variant_consistency",
		Domain:        "signals",
		Collection:    "variants",
		IndexedFields: []string{"state", "status", "kind", "bucket", "actor_id", "security_tier"},
		MaxRecords:    256,
		MaxBytes:      1 << 20,
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	rawJSON := []byte(fmt.Sprintf(`{
		"organization_id": "wrong-org",
		"state": "pending",
		"state": "ready",
		"status": "active",
		"kind": "variant",
		"bucket": "01",
		"actor_id": "actor-1",
		"security_tier": "standard",
		"tags": ["alpha", "beta", "beta"],
		"metadata": {"trace_id": "trace-raw", "actor_id": "actor-1", "request_id": "%s"},
		"revision": 7,
		"enabled": true,
		"score": 12.5,
		"null_value": null
	}`, uniqueName(env.prefix, "request")))
	if _, err := rawStore.UpsertRecordJSON(ctx, database.RawDomainRecord{
		Domain:         "signals",
		Collection:     "variants",
		OrganizationID: orgID,
		RecordID:       "variant-raw",
		DataJSON:       rawJSON,
	}); err != nil {
		t.Fatalf("raw UpsertRecordJSON() error = %v", err)
	}
	rawRecord, found, err := state.GetRecord(ctx, "signals", "variants", orgID, "variant-raw")
	if err != nil || !found {
		t.Fatalf("GetRecord(raw) found=%v err=%v", found, err)
	}
	requireRecordString(t, rawRecord.Data, "organization_id", orgID)
	requireRecordString(t, rawRecord.Data, "state", "ready")
	requireRecordString(t, rawRecord.Data, "status", "active")
	requireRecordString(t, rawRecord.Data, "bucket", "01")
	requireRecordInt(t, rawRecord.Data, "revision", 7)
	requireRecordBool(t, rawRecord.Data, "enabled", true)
	requireRecordFloat(t, rawRecord.Data, "score", "12.5")
	requireRecordRawContains(t, rawRecord.Data, "metadata", `"trace_id":"trace-raw"`, `"actor_id":"actor-1"`)
	requireRecordRawContains(t, rawRecord.Data, "tags", `"alpha"`, `"beta"`)

	typedData := database.RecordData{
		{Name: "state", Value: database.StringValue("pending")},
		{Name: "state", Value: database.StringValue("ready")},
		{Name: "status", Value: database.StringValue("active")},
		{Name: "kind", Value: database.StringValue("variant")},
		{Name: "bucket", Value: database.StringValue("02")},
		{Name: "actor_id", Value: database.StringValue("actor-typed")},
		{Name: "security_tier", Value: database.StringValue("standard")},
		{Name: "metadata", Value: database.RawValue([]byte(`{"trace_id":"trace-typed","actor_id":"actor-typed"}`))},
		{Name: "revision", Value: database.IntValue(8)},
		{Name: "enabled", Value: database.BoolValue(false)},
	}
	if _, err := state.UpsertRecord(ctx, database.DomainRecord{
		Domain:         "signals",
		Collection:     "variants",
		OrganizationID: orgID,
		RecordID:       "variant-typed",
		Data:           typedData,
	}); err != nil {
		t.Fatalf("typed UpsertRecord() error = %v", err)
	}
	typedRecord, found, err := state.GetRecord(ctx, "signals", "variants", orgID, "variant-typed")
	if err != nil || !found {
		t.Fatalf("GetRecord(typed) found=%v err=%v", found, err)
	}
	requireRecordString(t, typedRecord.Data, "state", "ready")
	requireRecordBool(t, typedRecord.Data, "enabled", false)

	for i := 0; i < 64; i++ {
		if _, err := rawStore.UpsertRecordJSON(ctx, database.RawDomainRecord{
			Domain:         "signals",
			Collection:     "variants",
			OrganizationID: orgID,
			RecordID:       fmt.Sprintf("variant-load-%02d", i),
			DataJSON: []byte(fmt.Sprintf(`{
				"state": "ready",
				"status": "active",
				"kind": "load_variant",
				"bucket": "%02d",
				"actor_id": "actor-%02d",
				"security_tier": "standard",
				"metadata": {"trace_id": "trace-load-%02d", "tags": ["load", "variant"]},
				"revision": %d,
				"enabled": %t,
				"score": %.2f
			}`, i%8, i%4, i, i, i%2 == 0, float64(i)+0.25)),
		}); err != nil {
			t.Fatalf("load UpsertRecordJSON(%d) error = %v", i, err)
		}
	}

	if result, err := store.Rebuild(ctx, "svc_variant_consistency", state, hermes.Query{OrganizationID: orgID}); err != nil || result.Applied != 66 {
		t.Fatalf("Rebuild() result=%+v err=%v, want 66 applied", result, err)
	}
	requireHermesCount(t, ctx, store, orgID, map[string]any{"state": "ready"}, 66)
	requireHermesCount(t, ctx, store, orgID, map[string]any{"kind": "load_variant"}, 64)
	requireHermesCount(t, ctx, store, orgID, map[string]any{"status": "active"}, 66)
	requireHermesDriftOK(t, ctx, store, state, orgID)

	patchData := serviceRecordData(map[string]any{
		"status":        "archived",
		"bucket":        "99",
		"actor_id":      "actor-2",
		"security_tier": "restricted",
		"metadata":      []byte(`{"trace_id":"trace-patch","actor_id":"actor-2","reason":"archive"}`),
		"revision":      int64(9),
	})
	if _, err := state.UpsertRecord(ctx, database.DomainRecord{
		Domain:         "signals",
		Collection:     "variants",
		OrganizationID: orgID,
		RecordID:       "variant-raw",
		Data:           rawRecord.Data.Merge(patchData),
	}); err != nil {
		t.Fatalf("postgres patch mirror UpsertRecord() error = %v", err)
	}
	patchEnvelope := variantProjectionEnvelope(t, "service-backed:variant:patch", foundationpb.ProjectionOperation_PROJECTION_OPERATION_PATCH, 1000, orgID, "variant-raw", map[string]any{
		"status":        "archived",
		"bucket":        "99",
		"actor_id":      "actor-2",
		"security_tier": "restricted",
		"metadata":      []byte(`{"trace_id":"trace-patch","actor_id":"actor-2","reason":"archive"}`),
		"revision":      int64(9),
	})
	patchRaw, err := patchEnvelope.ToBinary()
	if err != nil {
		t.Fatalf("patch Envelope.ToBinary() error = %v", err)
	}
	if _, err := redisClient.XAdd(ctx, stream, serviceRedisValues(map[string]any{"envelope": patchRaw})); err != nil {
		t.Fatalf("redis XAdd(patch first) error = %v", err)
	}
	if _, err := redisClient.XAdd(ctx, stream, serviceRedisValues(map[string]any{"envelope": patchRaw})); err != nil {
		t.Fatalf("redis XAdd(patch duplicate) error = %v", err)
	}
	tailer := newVariantTailer(t, store, redisClient, stream, group)
	patchResult, err := tailer.PollOnce(ctx)
	if err != nil {
		t.Fatalf("PollOnce(patch) error = %v", err)
	}
	if patchResult.Read != 2 || patchResult.Acked != 2 || patchResult.Apply.Applied != 1 || patchResult.Apply.Duplicates != 1 {
		t.Fatalf("PollOnce(patch) result=%+v, want read=2 acked=2 applied=1 duplicates=1", patchResult)
	}
	requireHermesCount(t, ctx, store, orgID, map[string]any{"status": "active"}, 65)
	requireHermesCount(t, ctx, store, orgID, map[string]any{"status": "archived"}, 1)
	requireHermesCount(t, ctx, store, orgID, map[string]any{"bucket": "99"}, 1)
	requireHermesDriftOK(t, ctx, store, state, orgID)

	if err := state.DeleteRecord(ctx, "signals", "variants", orgID, "variant-raw"); err != nil {
		t.Fatalf("postgres DeleteRecord() error = %v", err)
	}
	deleteEnvelope := variantProjectionEnvelope(t, "service-backed:variant:delete", foundationpb.ProjectionOperation_PROJECTION_OPERATION_DELETE, 1001, orgID, "variant-raw", nil)
	deleteRaw, err := deleteEnvelope.ToBinary()
	if err != nil {
		t.Fatalf("delete Envelope.ToBinary() error = %v", err)
	}
	if _, err := redisClient.XAdd(ctx, stream, serviceRedisValues(map[string]any{"envelope": deleteRaw})); err != nil {
		t.Fatalf("redis XAdd(delete) error = %v", err)
	}
	deleteResult, err := tailer.PollOnce(ctx)
	if err != nil || deleteResult.Apply.Applied != 1 || deleteResult.Acked != 1 {
		t.Fatalf("PollOnce(delete) result=%+v err=%v, want applied=1 acked=1", deleteResult, err)
	}
	if _, ok, err := store.GetRecord(ctx, "svc_variant_consistency", hermes.Query{OrganizationID: orgID}, "variant-raw", hermes.Fence{}); err != nil || ok {
		t.Fatalf("Hermes GetRecord(deleted) found=%v err=%v, want not found", ok, err)
	}
	requireHermesCount(t, ctx, store, orgID, map[string]any{"status": "archived"}, 0)
	requireHermesCount(t, ctx, store, orgID, map[string]any{"state": "ready"}, 65)
	requireHermesDriftOK(t, ctx, store, state, orgID)
}

func variantProjectionEnvelope(t *testing.T, sourceID string, operation foundationpb.ProjectionOperation, version uint64, orgID, recordID string, values map[string]any) events.Envelope {
	t.Helper()
	mutation := &foundationpb.RecordMutation{
		Operation:      operation,
		SourceId:       sourceID,
		Version:        version,
		Domain:         "signals",
		Collection:     "variants",
		OrganizationId: orgID,
		RecordId:       recordID,
		CorrelationId:  sourceID + ":correlation",
	}
	for name, value := range values {
		mutation.Fields = append(mutation.Fields, variantProjectionField(name, value))
	}
	envelope, err := hermes.NewProjectionEnvelope([]*foundationpb.RecordMutation{mutation}, sourceID+":correlation")
	if err != nil {
		t.Fatalf("NewProjectionEnvelope(%s) error = %v", sourceID, err)
	}
	return envelope
}

func variantProjectionField(name string, value any) *foundationpb.FieldValue {
	field := &foundationpb.FieldValue{Name: name, Value: &foundationpb.ScalarValue{}}
	switch typed := value.(type) {
	case string:
		field.Value.Kind = &foundationpb.ScalarValue_StringValue{StringValue: typed}
	case bool:
		field.Value.Kind = &foundationpb.ScalarValue_BoolValue{BoolValue: typed}
	case int:
		field.Value.Kind = &foundationpb.ScalarValue_Int64Value{Int64Value: int64(typed)}
	case int64:
		field.Value.Kind = &foundationpb.ScalarValue_Int64Value{Int64Value: typed}
	case uint64:
		field.Value.Kind = &foundationpb.ScalarValue_Uint64Value{Uint64Value: typed}
	case float64:
		field.Value.Kind = &foundationpb.ScalarValue_DoubleValue{DoubleValue: typed}
	case []byte:
		field.Value.Kind = &foundationpb.ScalarValue_BytesValue{BytesValue: append([]byte(nil), typed...)}
	default:
		field.Value.Kind = &foundationpb.ScalarValue_StringValue{StringValue: fmt.Sprintf("%v", typed)}
	}
	return field
}

func newVariantTailer(t *testing.T, store *hermes.Store, redisClient rediskit.Client, stream, group string) *hermes.EnvelopeTailer {
	t.Helper()
	source, err := hermes.NewRedisStreamEnvelopeSource(redisClient, stream, group, "variant-consumer", "")
	if err != nil {
		t.Fatalf("NewRedisStreamEnvelopeSource() error = %v", err)
	}
	tailer, err := hermes.NewEnvelopeTailer(store, "svc_variant_consistency", source, hermes.TailerOptions{MaxBatch: 8})
	if err != nil {
		t.Fatalf("NewEnvelopeTailer() error = %v", err)
	}
	return tailer
}

func requireHermesCount(t *testing.T, ctx context.Context, store *hermes.Store, orgID string, filters map[string]any, want int64) {
	t.Helper()
	got, err := store.Count(ctx, "svc_variant_consistency", hermes.QueryFromRecordQuery(orgID, serviceRecordQuery(0, filters)), hermes.Fence{})
	if err != nil || got != want {
		t.Fatalf("Hermes Count(%v) = %d err=%v, want %d", filters, got, err, want)
	}
}

func requireHermesDriftOK(t *testing.T, ctx context.Context, store *hermes.Store, state database.RuntimeStore, orgID string) {
	t.Helper()
	report, err := store.CheckDrift(ctx, "svc_variant_consistency", state, hermes.Query{OrganizationID: orgID}, hermes.DriftOptions{MaxRecords: 128, SampleSize: 16})
	if err != nil || !report.OK() {
		t.Fatalf("CheckDrift() ok=%v report=%+v err=%v", report.OK(), report, err)
	}
}

func requireRecordString(t *testing.T, data database.RecordData, field, want string) {
	t.Helper()
	value, ok := data.Get(field)
	if !ok || value.Kind != database.RecordValueString || value.Text != want {
		t.Fatalf("record field %q = %+v found=%v, want string %q", field, value, ok, want)
	}
}

func requireRecordInt(t *testing.T, data database.RecordData, field string, want int64) {
	t.Helper()
	value, ok := data.Get(field)
	if !ok || value.Kind != database.RecordValueInt {
		t.Fatalf("record field %q = %+v found=%v, want int %d", field, value, ok, want)
	}
	got, err := strconv.ParseInt(strings.TrimSpace(value.Text), 10, 64)
	if err != nil || got != want {
		t.Fatalf("record field %q text=%q parsed=%d err=%v, want %d", field, value.Text, got, err, want)
	}
}

func requireRecordBool(t *testing.T, data database.RecordData, field string, want bool) {
	t.Helper()
	value, ok := data.Get(field)
	if !ok || value.Kind != database.RecordValueBool {
		t.Fatalf("record field %q = %+v found=%v, want bool %v", field, value, ok, want)
	}
	got := strings.EqualFold(value.Text, "true") || value.Text == "1"
	if got != want {
		t.Fatalf("record field %q bool=%v raw=%q, want %v", field, got, value.Text, want)
	}
}

func requireRecordFloat(t *testing.T, data database.RecordData, field, want string) {
	t.Helper()
	value, ok := data.Get(field)
	if !ok || value.Kind != database.RecordValueFloat || strings.TrimSpace(value.Text) != want {
		t.Fatalf("record field %q = %+v found=%v, want float text %q", field, value, ok, want)
	}
}

func requireRecordRawContains(t *testing.T, data database.RecordData, field string, parts ...string) {
	t.Helper()
	value, ok := data.Get(field)
	if !ok || value.Kind != database.RecordValueRaw {
		t.Fatalf("record field %q = %+v found=%v, want raw JSON", field, value, ok)
	}
	for _, part := range parts {
		if !bytes.Contains(value.Raw, []byte(part)) {
			t.Fatalf("record field %q raw=%s missing %q", field, string(value.Raw), part)
		}
	}
}
