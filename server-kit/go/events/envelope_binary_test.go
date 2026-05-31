package events

import (
	"testing"
	"time"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestEnvelopeBinaryRoundTripWithJSONPayload(t *testing.T) {
	envelope := Envelope{
		ID:              "evt_123",
		EventType:       "media:freeze_manifest:v1:requested",
		Payload:         map[string]any{"asset_public_id": "asset_123", "attempt": float64(1)},
		PayloadEncoding: PayloadEncodingJSON,
		Metadata: map[string]any{
			"correlation_id":  "corr_123",
			"request_id":      "req_123",
			"idempotency_key": "idem_123",
			"channel":         "http",
			"global_context": map[string]any{
				"user_id":   "user_123",
				"device_id": "device_123",
				"source":    "api",
			},
			"custom_flag": true,
		},
		CorrelationID: "corr_123",
		SchemaVersion: "1.0",
		Timestamp:     time.Date(2026, 3, 14, 11, 0, 0, 0, time.UTC),
		SourceNodeID:  "bus_1",
	}

	raw, err := envelope.ToBinary()
	if err != nil {
		t.Fatalf("ToBinary() error = %v", err)
	}

	decoded, err := FromBinary(raw)
	if err != nil {
		t.Fatalf("FromBinary() error = %v", err)
	}

	if decoded.EventType != envelope.EventType {
		t.Fatalf("event type mismatch: got %s want %s", decoded.EventType, envelope.EventType)
	}
	if decoded.CorrelationID != envelope.CorrelationID {
		t.Fatalf("correlation id mismatch: got %s want %s", decoded.CorrelationID, envelope.CorrelationID)
	}
	if decoded.PayloadEncoding != PayloadEncodingJSON {
		t.Fatalf("payload encoding mismatch: got %s", decoded.PayloadEncoding)
	}
	if got, _ := decoded.Payload["asset_public_id"].(string); got != "asset_123" {
		t.Fatalf("asset_public_id mismatch: got %q", got)
	}
	if got, _ := decoded.Metadata["channel"].(string); got != "http" {
		t.Fatalf("channel mismatch: got %q", got)
	}
	if got, ok := decoded.Metadata["custom_flag"].(bool); !ok || !got {
		t.Fatalf("custom_flag mismatch: got %#v", decoded.Metadata["custom_flag"])
	}
	if decoded.SourceNodeID != "bus_1" {
		t.Fatalf("source node id mismatch: got %q", decoded.SourceNodeID)
	}
}

func TestDecodeFallsBackToLegacyJSONEnvelope(t *testing.T) {
	raw := []byte(`{
		"event_type":"identity:refresh_session:v1:requested",
		"payload":{"session_id":"sess_123"},
		"metadata":{"correlation_id":"corr_legacy"},
		"correlation_id":"corr_legacy",
		"schema_version":"1.0",
		"timestamp":"2026-03-14T11:15:00Z"
	}`)

	decoded, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if decoded.EventType != "identity:refresh_session:v1:requested" {
		t.Fatalf("unexpected event type: %s", decoded.EventType)
	}
	if decoded.PayloadEncoding != PayloadEncodingJSON {
		t.Fatalf("unexpected payload encoding: %s", decoded.PayloadEncoding)
	}
	if got, _ := decoded.Payload["session_id"].(string); got != "sess_123" {
		t.Fatalf("session_id mismatch: got %q", got)
	}
}

func TestEnvelopeBinaryRoundTripWithProtobufPayloadBytes(t *testing.T) {
	envelope := Envelope{
		EventType:       "publish:webhook_ingest:v1:requested",
		PayloadBytes:    []byte{0x01, 0x02, 0x03},
		PayloadEncoding: PayloadEncodingProtobuf,
		Metadata: map[string]any{
			"correlation_id": "corr_proto",
		},
		CorrelationID: "corr_proto",
		SchemaVersion: "1.0",
		Timestamp:     time.Date(2026, 3, 14, 11, 30, 0, 0, time.UTC),
	}

	raw, err := envelope.ToBinary()
	if err != nil {
		t.Fatalf("ToBinary() error = %v", err)
	}

	decoded, err := FromBinary(raw)
	if err != nil {
		t.Fatalf("FromBinary() error = %v", err)
	}
	if decoded.PayloadEncoding != PayloadEncodingProtobuf {
		t.Fatalf("payload encoding mismatch: got %s", decoded.PayloadEncoding)
	}
	if string(decoded.PayloadBytes) != string(envelope.PayloadBytes) {
		t.Fatalf("payload bytes mismatch: got %v want %v", decoded.PayloadBytes, envelope.PayloadBytes)
	}
}

func TestEnvelopeBinaryRoundTripWithCapnpPayloadBytes(t *testing.T) {
	envelope := Envelope{
		EventType:       "publish:webhook_ingest:v1:requested",
		PayloadBytes:    []byte{0x0a, 0x0b, 0x0c},
		PayloadEncoding: PayloadEncodingCapnp,
		Metadata: map[string]any{
			"correlation_id": "corr_capnp",
		},
		CorrelationID: "corr_capnp",
		SchemaVersion: "1.0",
		Timestamp:     time.Date(2026, 3, 14, 11, 35, 0, 0, time.UTC),
	}

	raw, err := envelope.ToBinary()
	if err != nil {
		t.Fatalf("ToBinary() error = %v", err)
	}
	decoded, err := FromBinary(raw)
	if err != nil {
		t.Fatalf("FromBinary() error = %v", err)
	}
	if decoded.PayloadEncoding != PayloadEncodingCapnp {
		t.Fatalf("payload encoding mismatch: got %s", decoded.PayloadEncoding)
	}
	if string(decoded.PayloadBytes) != string(envelope.PayloadBytes) {
		t.Fatalf("payload bytes mismatch: got %v want %v", decoded.PayloadBytes, envelope.PayloadBytes)
	}
}

func TestBatchBinaryRejectsInvalidJSONPayload(t *testing.T) {
	raw, err := proto.Marshal(&foundationpb.EventBatch{
		Envelopes: []*foundationpb.EventEnvelope{
			{
				EventType:       "market:ingest_batch:v1:requested",
				Payload:         []byte(`{"broken":`),
				Metadata:        &foundationpb.Metadata{CorrelationId: "corr_bad_json"},
				CorrelationId:   "corr_bad_json",
				SchemaVersion:   "1.0",
				OccurredAt:      timestamppb.New(time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)),
				PayloadEncoding: foundationpb.PayloadEncoding_PAYLOAD_ENCODING_JSON,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal test batch: %v", err)
	}

	if _, err := FromBatchBinary(raw); err == nil {
		t.Fatal("FromBatchBinary() expected invalid JSON payload error, got nil")
	}
}
