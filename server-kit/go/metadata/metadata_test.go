package metadata

import (
	"context"
	"encoding/json"
	"testing"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
)

func TestFromMapAndToMap(t *testing.T) {
	raw := map[string]any{
		"global_context": map[string]any{
			"user_id":         "user_1",
			"session_id":      "sess_1",
			"source":          "mobile",
			"device_id":       "dev_1",
			"organization_id": "org_1",
			"role_id":         "dispatcher",
			"audit_context":   "incident:123",
			"ip_address":      "10.0.0.1",
			"user_agent":      "field-app",
		},
		"tags":               []any{"critical", "dispatch"},
		"categories":         []any{"ops", "work_order"},
		"ai_confidence":      0.88,
		"embedding_id":       "emb_1",
		"knowledge_graph":    "kg_1",
		"source_ref":         "sensor://proof/1",
		"validity_period":    map[string]any{"effective_from": "2026-02-14", "effective_to": "2026-02-15"},
		"gamification_state": "{\"xp\":5}",
		"correlation_id":     "corr_1",
		"causation_id":       "cause_1",
		"request_id":         "req_1",
		"idempotency_key":    "idem_1",
		"trace_id":           "trace_1",
		"span_id":            "span_1",
		"channel":            "http.dispatch",
		"locale":             "en-US",
		"tenant_region":      "us-east",
		"attributes": map[string]any{
			"priority": "high",
			"source":   "app",
		},
		"extra_field": "preserved",
	}

	md := FromMap(raw)
	if md.GlobalContext == nil {
		t.Fatalf("expected global context")
	}
	if md.GlobalContext.OrganizationID != "org_1" {
		t.Fatalf("unexpected organization_id: %s", md.GlobalContext.OrganizationID)
	}
	if md.CorrelationID != "corr_1" {
		t.Fatalf("unexpected correlation_id: %s", md.CorrelationID)
	}
	if md.Attributes["priority"] != "high" {
		t.Fatalf("missing attributes")
	}
	if value, ok := md.Extras.GetString("extra_field"); !ok || value != "preserved" {
		t.Fatalf("expected extras preservation")
	}

	out := md.ToMap()
	if out["correlation_id"] != "corr_1" {
		t.Fatalf("unexpected correlation_id in ToMap")
	}
	if out["extra_field"] != "preserved" {
		t.Fatalf("missing extra field in ToMap")
	}
}

func TestNormalizeAndDefaults(t *testing.T) {
	md := FromMap(map[string]any{
		"correlation_id": "corr_from_metadata",
	})
	corr := md.NormalizeCorrelation("")
	if corr != "corr_from_metadata" {
		t.Fatalf("expected metadata correlation fallback")
	}
	md.ApplyDefaults("http.dispatch")
	if md.Channel != "http.dispatch" {
		t.Fatalf("expected channel default")
	}
	if md.RequestID != "corr_from_metadata" {
		t.Fatalf("expected request_id fallback")
	}
}

func TestEnsureCorrelation(t *testing.T) {
	md := New()
	corr := md.EnsureCorrelation(" corr_from_request ")
	if corr != "corr_from_request" {
		t.Fatalf("unexpected correlation: %q", corr)
	}
	if md.CorrelationID != "corr_from_request" || md.RequestID != "corr_from_request" {
		t.Fatalf("expected correlation and request id to be populated: %#v", md)
	}

	md = New()
	corr = md.EnsureCorrelation()
	if corr == "" || md.CorrelationID == "" || md.RequestID == "" {
		t.Fatalf("expected generated correlation and request id: %#v", md)
	}
}

func TestValidate(t *testing.T) {
	md := FromMap(map[string]any{
		"ai_confidence":   1.2,
		"correlation_id":  "corr_1",
		"idempotency_key": "idem_1",
	})
	if err := md.Validate(); err == nil {
		t.Fatalf("expected ai_confidence validation error")
	}

	md = FromMap(map[string]any{
		"validity_period": map[string]any{
			"effective_from": "2026-02-15",
			"effective_to":   "2026-02-14",
		},
	})
	if err := md.Validate(); err == nil {
		t.Fatalf("expected validity period validation error")
	}

	md = FromMap(map[string]any{
		"correlation_id":  "corr_1",
		"idempotency_key": "idem_1",
		"validity_period": map[string]any{
			"effective_from": "2026-02-14",
			"effective_to":   "2026-02-15",
		},
	})
	if err := md.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestContextAndJSONRoundTrip(t *testing.T) {
	md := FromMap(map[string]any{
		"correlation_id": "corr_1",
		"channel":        "worker.operations",
		"global_context": map[string]any{
			"user_id": "user_1",
		},
	})

	ctx := IntoContext(context.Background(), md)
	out := FromContext(ctx)
	if out.CorrelationID != "corr_1" {
		t.Fatalf("metadata context round trip failed")
	}

	payload, err := md.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}

	decoded := map[string]any{}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("json decode failed: %v", err)
	}
	if decoded["correlation_id"] != "corr_1" {
		t.Fatalf("unexpected json payload")
	}

	parsed, err := FromJSON(payload)
	if err != nil {
		t.Fatalf("FromJSON failed: %v", err)
	}
	if parsed.CorrelationID != "corr_1" {
		t.Fatalf("json round trip mismatch")
	}

	ctx = NewContext(context.Background(), map[string]any{"correlation_id": "corr_new"})
	if got := FromContext(ctx).CorrelationID; got != "corr_new" {
		t.Fatalf("NewContext correlation = %q", got)
	}
	if got := FromContext(context.Background()); got.Attributes == nil || got.Extras == nil {
		t.Fatalf("FromContext missing should initialize maps: %+v", got)
	}
}

func TestMetadataMapVariantsAndPrepareForEmit(t *testing.T) {
	md := FromMap(map[string]any{
		"globalContext": map[string]string{
			"userId":         "user_2",
			"organizationId": "org_2",
		},
		"tags":         []string{"a", "b"},
		"categories":   []any{"ops", 1, ""},
		"aiConfidence": float32(0.5),
		"attributes": map[string]any{
			"attempt": 2,
		},
		"custom": "preserved",
	})
	if md.GlobalContext == nil || md.GlobalContext.UserID != "user_2" || md.GlobalContext.OrganizationID != "org_2" {
		t.Fatalf("global context variant was not parsed: %+v", md.GlobalContext)
	}
	if custom, ok := md.Extras.GetString("custom"); len(md.Tags) != 2 || len(md.Categories) != 1 || md.Attributes["attempt"] != "2" || !ok || custom != "preserved" {
		t.Fatalf("collection parsing mismatch: %+v", md)
	}

	ctx := IntoContext(context.Background(), EnvelopeMetadata{
		CorrelationID: "corr_ctx",
		Channel:       "worker",
	})
	emitted := PrepareForEmit(ctx, map[string]any{
		"correlation_id": "",
		"global_context": map[string]any{"correlation_id": "corr_nested"},
		"channel":        "override",
	})
	if emitted["correlation_id"] != "corr_ctx" || emitted["channel"] != "override" {
		t.Fatalf("PrepareForEmit result = %+v", emitted)
	}

	emitted = PrepareForEmit(context.Background(), map[string]any{
		"global_context": map[string]any{"correlation_id": "corr_nested"},
	})
	if emitted["correlation_id"] != "corr_nested" {
		t.Fatalf("expected nested correlation fallback: %+v", emitted)
	}

	md = FromMap(map[string]any{
		"global_context": map[string]any{},
		"ai_confidence":  int64(1),
	})
	if md.GlobalContext != nil || md.AIConfidence != 1 {
		t.Fatalf("empty global context/int64 confidence mismatch: %+v", md)
	}
}

func TestMetadataTagsAreCanonicalAndSafe(t *testing.T) {
	tag, ok := BuildTag("Actor", "User 123")
	if !ok || tag != "actor:user_123" {
		t.Fatalf("BuildTag() = %q, %v", tag, ok)
	}
	if tag, ok := BuildTag("security", "Bearer abc"); ok || tag != "" {
		t.Fatalf("secret-like tag should be rejected: %q", tag)
	}

	md := FromMap(map[string]any{
		"tags": []any{
			" Actor:User_123 ",
			"actor:user_123",
			"security:jwt",
			"Search:Invoice",
			"kg:brand-profile",
		},
		"categories": []string{" Intelligence ", "intelligence", "Search"},
	})
	wantTags := []string{"actor:user_123", "kg:brand-profile", "search:invoice"}
	if got := md.Tags; len(got) != len(wantTags) {
		t.Fatalf("tags length = %d, want %d: %#v", len(got), len(wantTags), got)
	}
	for i, want := range wantTags {
		if md.Tags[i] != want {
			t.Fatalf("tag[%d] = %q, want %q", i, md.Tags[i], want)
		}
	}
	if len(md.Categories) != 2 || md.Categories[0] != "intelligence" || md.Categories[1] != "search" {
		t.Fatalf("categories were not normalized/deduped: %#v", md.Categories)
	}
}

func TestMergeMapsPreservesGraphMetadataAndUnionsTags(t *testing.T) {
	merged := MergeMaps(
		map[string]any{
			"tags":            []string{"actor:user_1", "domain:docuos"},
			"categories":      []string{"workflow"},
			"knowledge_graph": "docuos.documents",
			"source_ref":      "request:req_1",
			"attributes":      map[string]any{"actor_kind": "user"},
		},
		map[string]any{
			"tags":            []string{"actor:user_1", "entity:brand_profile", "secret:abc"},
			"categories":      []string{"security", "Workflow"},
			"knowledge_graph": "",
			"source_ref":      "",
		},
	)
	tags := stringsFromAny(t, merged["tags"])
	wantTags := []string{"actor:user_1", "domain:docuos", "entity:brand_profile"}
	if len(tags) != len(wantTags) {
		t.Fatalf("tags length = %d, want %d: %#v", len(tags), len(wantTags), tags)
	}
	for i, want := range wantTags {
		if tags[i] != want {
			t.Fatalf("tag[%d] = %q, want %q", i, tags[i], want)
		}
	}
	categories := stringsFromAny(t, merged["categories"])
	if len(categories) != 2 || categories[0] != "security" || categories[1] != "workflow" {
		t.Fatalf("categories mismatch: %#v", categories)
	}
	if merged["knowledge_graph"] != "docuos.documents" || merged["source_ref"] != "request:req_1" {
		t.Fatalf("graph provenance should survive empty overlays: %#v", merged)
	}
}

func stringsFromAny(t *testing.T, value any) []string {
	t.Helper()
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			str, ok := item.(string)
			if !ok {
				t.Fatalf("list item has unexpected type: %T", item)
			}
			out = append(out, str)
		}
		return out
	default:
		t.Fatalf("list has unexpected type: %T", value)
		return nil
	}
}

func TestMetadataNilReceiversAndValidationEdges(t *testing.T) {
	var nilMeta *EnvelopeMetadata
	if got := nilMeta.EnsureCorrelation(" corr_nil "); got != "corr_nil" {
		t.Fatalf("nil EnsureCorrelation = %q", got)
	}
	if got := nilMeta.NormalizeCorrelation(" corr_norm "); got != "corr_norm" {
		t.Fatalf("nil NormalizeCorrelation = %q", got)
	}
	nilMeta.ApplyDefaults("ignored")

	md := EnvelopeMetadata{CorrelationID: "bad space"}
	if err := md.Validate(); err == nil {
		t.Fatalf("expected token validation error")
	}
	md = EnvelopeMetadata{ValidityPeriod: &ValidityPeriod{EffectiveFrom: "not-a-date"}}
	if err := md.Validate(); err == nil {
		t.Fatalf("expected invalid date error")
	}
	md = EnvelopeMetadata{ValidityPeriod: &ValidityPeriod{EffectiveFrom: "2026-01-01T00:00:00Z", EffectiveTo: "2026-01-02T00:00:00Z"}}
	if err := md.Validate(); err != nil {
		t.Fatalf("RFC3339 validity period should pass: %v", err)
	}
	if _, err := FromJSON([]byte("{bad")); err == nil {
		t.Fatalf("expected invalid JSON error")
	}
}

func TestTransportProtoRoundTrip(t *testing.T) {
	md := EnvelopeMetadata{
		GlobalContext: &GlobalContext{
			UserID:         "user_1",
			SessionID:      "session_1",
			Source:         "api",
			DeviceID:       "device_1",
			OrganizationID: "org_1",
			RoleID:         "admin",
			AuditContext:   "audit",
			IPAddress:      "203.0.113.1",
			UserAgent:      "test",
		},
		Tags:           []string{"tag"},
		AIConfidence:   0.7,
		EmbeddingID:    "emb",
		Categories:     []string{"cat"},
		KnowledgeGraph: "kg",
		SourceRef:      "source",
		ValidityPeriod: &ValidityPeriod{EffectiveFrom: "2026-01-01", EffectiveTo: "2026-01-02"},
		CorrelationID:  "corr_1",
		Attributes:     map[string]string{"k": "v"},
		Extras:         extension.Object{"extra": extension.String("value")},
	}
	pb, err := md.ToTransportProto()
	if err != nil {
		t.Fatalf("ToTransportProto() error = %v", err)
	}
	roundTrip, err := FromTransportProto(pb)
	if err != nil {
		t.Fatalf("FromTransportProto() error = %v", err)
	}
	if extra, ok := roundTrip.Extras.GetString("extra"); roundTrip.CorrelationID != "corr_1" || roundTrip.GlobalContext.UserID != "user_1" || !ok || extra != "value" {
		t.Fatalf("transport roundtrip mismatch: %+v", roundTrip)
	}
	empty, err := FromTransportProto(nil)
	if err != nil || empty.Attributes == nil {
		t.Fatalf("nil transport metadata = %+v err=%v", empty, err)
	}
	_, err = FromTransportProto(&foundationpb.Metadata{ExtrasJson: []byte("{bad")})
	if err == nil {
		t.Fatalf("expected invalid extras JSON error")
	}
}
