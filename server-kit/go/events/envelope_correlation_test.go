package events

import "testing"

func TestEnvelopeNormalizeUsesSingleCorrelationID(t *testing.T) {
	env := Envelope{
		EventType: "media:upload:v1:requested",
		Metadata:  ObjectFromMap(map[string]any{"correlation_id": "corr_metadata"}),
	}
	env.Normalize()

	if env.CorrelationID != "corr_metadata" {
		t.Fatalf("CorrelationID = %q, want metadata correlation", env.CorrelationID)
	}
	if got, _ := env.Metadata.GetString("correlation_id"); got != "corr_metadata" {
		t.Fatalf("metadata.correlation_id = %q, want corr_metadata", got)
	}
	if got, _ := env.Metadata.GetString("request_id"); got != "corr_metadata" {
		t.Fatalf("metadata.request_id = %q, want corr_metadata", got)
	}
}

func TestEnvelopeNormalizeGeneratesCorrelationID(t *testing.T) {
	env := Envelope{EventType: "media:upload:v1:requested"}
	env.Normalize()

	if env.CorrelationID == "" {
		t.Fatal("expected generated correlation id")
	}
	if got, _ := env.Metadata.GetString("correlation_id"); got != env.CorrelationID {
		t.Fatalf("metadata.correlation_id = %q, want %q", got, env.CorrelationID)
	}
}

func TestEnvelopeValidateRejectsSplitCorrelationIDs(t *testing.T) {
	env := Envelope{
		EventType:     "media:upload:v1:requested",
		Metadata:      ObjectFromMap(map[string]any{"correlation_id": "corr_metadata"}),
		CorrelationID: "corr_envelope",
	}
	env.Normalize()

	if err := env.Validate(); err == nil {
		t.Fatal("expected split correlation ids to fail validation")
	}
}
