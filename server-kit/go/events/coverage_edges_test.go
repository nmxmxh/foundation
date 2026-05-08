package events

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	transportpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/transport/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestEventContractTerminalHelpers(t *testing.T) {
	if got := TerminalState("orders:create:v1:requested"); got != "requested" {
		t.Fatalf("TerminalState = %q", got)
	}
	if got := TerminalState("bad"); got != "" {
		t.Fatalf("TerminalState bad = %q", got)
	}
	cases := map[string]string{
		"orders:create:v1:requested": "orders:create:v1:success",
		"orders:create:v1:failed":    "orders:create:v1:success",
		"orders:create:v1":           "orders:create:v1:success",
		"orders":                     "orders:success",
		"":                           "success",
	}
	for input, want := range cases {
		if got := EnsureTerminalState(input, "success"); got != want {
			t.Fatalf("EnsureTerminalState(%q) = %q, want %q", input, got, want)
		}
	}
	if got := EnsureTerminalState("orders:create:v1:requested", "bad"); got != "orders:create:v1:requested" {
		t.Fatalf("invalid terminal changed event: %q", got)
	}
}

func TestEnvelopeJSONMapAndBinaryErrors(t *testing.T) {
	env := Envelope{
		ID:            "evt_1",
		EventType:     "orders:create:v1:requested",
		Payload:       map[string]any{"ok": true},
		Metadata:      map[string]any{"correlation_id": "corr_1"},
		CorrelationID: "corr_1",
		Timestamp:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	env.Normalize()
	asMap := env.ToMap()
	if asMap["id"] != "evt_1" || asMap["payload_encoding"] != PayloadEncodingJSON {
		t.Fatalf("ToMap() = %+v", asMap)
	}
	raw, err := env.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded["event_type"] != env.EventType {
		t.Fatalf("ToJSON decoded = %+v err=%v", decoded, err)
	}
	env.PayloadEncoding = PayloadEncodingProtobuf
	if _, err := env.ToJSON(); err == nil {
		t.Fatalf("expected protobuf ToJSON error")
	}
	if _, err := (Envelope{EventType: "bad"}).ToBinary(); err == nil {
		t.Fatalf("expected invalid envelope ToBinary error")
	}
	if _, err := FromJSON([]byte(`{"timestamp":"bad"}`)); err == nil {
		t.Fatalf("expected invalid timestamp error")
	}
}

func TestBinaryDecodeEdgeCases(t *testing.T) {
	if _, err := FromBinary([]byte("bad")); err == nil {
		t.Fatalf("expected invalid binary error")
	}
	raw, err := proto.Marshal(&transportpb.EventEnvelope{
		EventType:       "orders:create:v1:requested",
		Payload:         []byte(`null`),
		Metadata:        &transportpb.Metadata{CorrelationId: "corr_null"},
		CorrelationId:   "corr_null",
		SchemaVersion:   "1.0",
		OccurredAt:      timestamppb.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		PayloadEncoding: transportpb.PayloadEncoding_PAYLOAD_ENCODING_JSON,
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	env, err := FromBinary(raw)
	if err != nil {
		t.Fatalf("FromBinary(null JSON) error = %v", err)
	}
	if len(env.Payload) != 0 {
		t.Fatalf("null JSON payload should normalize to empty map: %+v", env.Payload)
	}
	if got := payloadEncodingToProto("bad"); got != transportpb.PayloadEncoding_PAYLOAD_ENCODING_UNSPECIFIED {
		t.Fatalf("payloadEncodingToProto bad = %v", got)
	}
	if got := payloadEncodingFromProto(transportpb.PayloadEncoding(99)); got != PayloadEncodingJSON {
		t.Fatalf("payloadEncodingFromProto unknown = %q", got)
	}
}

func TestBatchBinaryRoundTripAndErrors(t *testing.T) {
	env := makeTestEnvelope("orders:create:v1:requested", "corr_batch")
	raw, err := (Batch{Envelopes: []Envelope{env}}).ToBinary()
	if err != nil {
		t.Fatalf("Batch.ToBinary() error = %v", err)
	}
	batch, err := FromBatchBinary(raw)
	if err != nil {
		t.Fatalf("FromBatchBinary() error = %v", err)
	}
	if len(batch.Envelopes) != 1 || batch.Envelopes[0].CorrelationID != "corr_batch" {
		t.Fatalf("batch roundtrip mismatch: %+v", batch)
	}
	if _, err := (Batch{Envelopes: []Envelope{{EventType: "bad"}}}).ToBinary(); err == nil {
		t.Fatalf("expected invalid batch envelope error")
	}
	if _, err := FromBatchBinary([]byte("bad")); err == nil {
		t.Fatalf("expected invalid batch binary error")
	}
}

func TestRedisBusConsumeLoopAndSleep(t *testing.T) {
	client := redis.NewMemoryClient("events")
	bus := NewRedisBus(client, "orders-channel", 8, nil)
	defer func() { _ = bus.Close() }()
	var got Envelope
	done := make(chan struct{})
	bus.Subscribe("orders:*", func(_ context.Context, env Envelope) {
		got = env
		close(done)
	})
	env := makeTestEnvelope("orders:create:v1:requested", "corr_redis_loop")
	raw, err := env.ToBinary()
	if err != nil {
		t.Fatalf("ToBinary() error = %v", err)
	}
	if err := client.Publish(context.Background(), "events:orders-channel", raw); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	bus.consumeLoop(singleMessage(raw))
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for consumeLoop dispatch")
	}
	if got.CorrelationID != "corr_redis_loop" {
		t.Fatalf("dispatched envelope = %+v", got)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	sleepWithContext(cancelled, time.Hour)
}

func singleMessage(raw []byte) <-chan []byte {
	ch := make(chan []byte, 1)
	ch <- raw
	close(ch)
	return ch
}
