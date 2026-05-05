package transport

import "testing"

func TestCreateEnvelopeIncludesRequiredMetadata(t *testing.T) {
	envelope := CreateEnvelope("workspace.v1.created", map[string]any{"workspace_id": "ws_1"}, nil)
	if envelope.Metadata.CorrelationID == "" || envelope.Metadata.RequestID == "" || envelope.Metadata.IdempotencyKey == "" {
		t.Fatalf("envelope metadata is incomplete: %+v", envelope.Metadata)
	}
	if envelope.Metadata.RequestID != envelope.Metadata.CorrelationID {
		t.Fatalf("request_id = %q, want correlation_id %q", envelope.Metadata.RequestID, envelope.Metadata.CorrelationID)
	}
	if envelope.PayloadEncoding != PayloadEncodingJSON {
		t.Fatalf("payload encoding = %s, want %s", envelope.PayloadEncoding, PayloadEncodingJSON)
	}
}

func TestCanDispatchRespectsCapability(t *testing.T) {
	route := &Route{
		EventType:          "workspace.v1.created",
		RequiredCapability: "workspace.write",
	}
	allowed := CanDispatch(route, []string{"workspace.write"}, func(_ *Route) bool { return true })
	if !allowed {
		t.Fatal("expected route to be dispatchable")
	}
}
