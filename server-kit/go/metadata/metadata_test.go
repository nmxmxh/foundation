package metadata

import (
	"context"
	"encoding/json"
	"testing"
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
	if md.Extras["extra_field"] != "preserved" {
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
}
