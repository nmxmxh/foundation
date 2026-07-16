package events

import (
	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"strings"
	"testing"
	"time"
)

func TestEnvelopeBinaryRoundTripWithJSONPayload(t *testing.T) {
	envelope := Envelope{
		ID:              "evt_123",
		EventType:       "media:freeze_manifest:v1:requested",
		Payload:         ObjectFromMap(map[string]any{"asset_public_id": "asset_123", "attempt": float64(1)}),
		PayloadEncoding: PayloadEncodingJSON,
		Metadata: ObjectFromMap(map[string]any{
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
		}),
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
	if decoded.Payload != nil {
		t.Fatalf("json payload should stay lazy before materialization: %+v", decoded.Payload)
	}
	if err := decoded.MaterializePayload(); err != nil {
		t.Fatalf("MaterializePayload() error = %v", err)
	}
	if got, _ := decoded.Payload.GetString("asset_public_id"); got != "asset_123" {
		t.Fatalf("asset_public_id mismatch: got %q", got)
	}
	if decoded.Metadata != nil {
		t.Fatalf("metadata should stay lazy before materialization: %+v", decoded.Metadata)
	}
	if err := decoded.MaterializeMetadata(); err != nil {
		t.Fatalf("MaterializeMetadata() error = %v", err)
	}
	if got, _ := decoded.Metadata.GetString("channel"); got != "http" {
		t.Fatalf("channel mismatch: got %q", got)
	}
	if got, ok := decoded.Metadata["custom_flag"].BoolValue(); !ok || !got {
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
	if err := decoded.MaterializePayload(); err != nil {
		t.Fatalf("MaterializePayload() error = %v", err)
	}
	if got, _ := decoded.Payload.GetString("session_id"); got != "sess_123" {
		t.Fatalf("session_id mismatch: got %q", got)
	}
}

func TestEnvelopeBinaryRoundTripWithProtobufPayloadBytes(t *testing.T) {
	envelope := Envelope{
		EventType:       "publish:webhook_ingest:v1:requested",
		PayloadBytes:    []byte{0x01, 0x02, 0x03},
		PayloadEncoding: PayloadEncodingProtobuf,
		Metadata: ObjectFromMap(map[string]any{
			"correlation_id": "corr_proto",
		}),
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
		Metadata: ObjectFromMap(map[string]any{
			"correlation_id": "corr_capnp",
		}),
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

	batch, err := FromBatchBinary(raw)
	if err != nil {
		t.Fatalf("FromBatchBinary() error = %v", err)
	}
	if len(batch.Envelopes) != 1 {
		t.Fatalf("batch envelopes = %d, want 1", len(batch.Envelopes))
	}
	if err := batch.Envelopes[0].MaterializePayload(); err == nil {
		t.Fatal("MaterializePayload() expected invalid JSON payload error, got nil")
	}
}
func lazyEnvelope(mut func(env *Envelope, pb *foundationpb.Metadata)) Envelope {
	pb := &foundationpb.Metadata{CorrelationId: "corr_lazy"}
	env := Envelope{
		EventType:     "orders:create:v1:requested",
		CorrelationID: "corr_lazy",
		SchemaVersion: EnvelopeSchemaVersion,
		Timestamp:     time.Now().UTC(),
		lazyMetadata:  pb,
	}
	if mut != nil {
		mut(&env, pb)
	}
	return env
}

func TestValidateLazyMetadataFastPath(t *testing.T) {
	cases := map[string]struct {
		mut     func(env *Envelope, pb *foundationpb.Metadata)
		wantErr string
	}{
		"happy json": {},
		"correlation from metadata only": {
			mut: func(env *Envelope, _ *foundationpb.Metadata) { env.CorrelationID = "" },
		},
		"valid token fields": {
			mut: func(_ *Envelope, pb *foundationpb.Metadata) {
				pb.CausationId = "cause.1"
				pb.RequestId = "req_1"
				pb.IdempotencyKey = "idem:1"
				pb.TraceId = "trace-1"
				pb.SpanId = "span1"
			},
		},
		"missing correlation": {
			mut: func(env *Envelope, pb *foundationpb.Metadata) {
				env.CorrelationID = ""
				pb.CorrelationId = ""
			},
			wantErr: "missing correlation_id",
		},
		"correlation mismatch": {
			mut:     func(_ *Envelope, pb *foundationpb.Metadata) { pb.CorrelationId = "corr_other" },
			wantErr: "must match",
		},
		"invalid correlation format": {
			mut: func(env *Envelope, pb *foundationpb.Metadata) {
				env.CorrelationID = "bad corr!"
				pb.CorrelationId = ""
			},
			wantErr: "invalid format",
		},
		"invalid token format": {
			mut:     func(_ *Envelope, pb *foundationpb.Metadata) { pb.TraceId = "bad trace!" },
			wantErr: "invalid format",
		},
		"unsupported schema version": {
			mut:     func(env *Envelope, _ *foundationpb.Metadata) { env.SchemaVersion = "9.9" },
			wantErr: "schema_version",
		},
		"missing timestamp": {
			mut:     func(env *Envelope, _ *foundationpb.Metadata) { env.Timestamp = time.Time{} },
			wantErr: "missing timestamp",
		},
		"binary encoding requires bytes": {
			mut:     func(env *Envelope, _ *foundationpb.Metadata) { env.PayloadEncoding = PayloadEncodingProtobuf },
			wantErr: "requires payload bytes",
		},
		"binary encoding with bytes": {
			mut: func(env *Envelope, _ *foundationpb.Metadata) {
				env.PayloadEncoding = PayloadEncodingBinary
				env.PayloadBytes = []byte{0x1}
			},
		},
		"unsupported encoding": {
			mut:     func(env *Envelope, _ *foundationpb.Metadata) { env.PayloadEncoding = "weird" },
			wantErr: "unsupported payload_encoding",
		},
		"rich metadata materializes": {
			mut: func(_ *Envelope, pb *foundationpb.Metadata) { pb.Tags = []string{"t1"} },
		},
		"rich metadata materialize failure": {
			mut: func(_ *Envelope, pb *foundationpb.Metadata) {
				pb.ExtrasJson = []byte("{not json")
			},
			wantErr: "invalid character",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env := lazyEnvelope(tc.mut)
			err := env.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func metadataEnvelope(correlationID string, md extension.Object) Envelope {
	return Envelope{
		EventType:     "orders:create:v1:requested",
		CorrelationID: correlationID,
		SchemaVersion: EnvelopeSchemaVersion,
		Timestamp:     time.Now().UTC(),
		Metadata:      md,
	}
}

func TestValidateMetadataObjectBranches(t *testing.T) {
	longToken := strings.Repeat("a", 129)
	cases := map[string]struct {
		env     Envelope
		wantErr string
	}{
		"non-string correlation ignored": {
			env: metadataEnvelope("corr_1", extension.Object{"correlation_id": extension.Int(5)}),
		},
		"non-string token ignored": {
			env: metadataEnvelope("corr_1", extension.Object{"trace_id": extension.Int(9)}),
		},
		"blank token ignored": {
			env: metadataEnvelope("corr_1", extension.Object{"span_id": extension.String("  ")}),
		},
		"token overflow falls back to slow path": {
			env: metadataEnvelope("corr_1", extension.Object{
				"causation_id":    extension.String("tok1"),
				"request_id":      extension.String("tok2"),
				"idempotency_key": extension.String("tok3"),
				"trace_id":        extension.String("tok4"),
				"span_id":         extension.String("tok5"),
				"requestId":       extension.String("tok6"),
			}),
		},
		"rich key falls back to slow path": {
			env: metadataEnvelope("corr_1", extension.Object{"ai_confidence": extension.Int(1)}),
		},
		"fast mismatch": {
			env:     metadataEnvelope("corr_1", extension.Object{"correlation_id": extension.String("corr_other")}),
			wantErr: "must match",
		},
		"fast invalid token": {
			env:     metadataEnvelope("corr_1", extension.Object{"trace_id": extension.String("bad trace!")}),
			wantErr: "invalid format",
		},
		"fast correlation too long": {
			env:     metadataEnvelope(longToken, extension.Object{}),
			wantErr: "invalid format",
		},
		"slow path valid": {
			env: metadataEnvelope("corr_1", extension.Object{
				"tags":           extension.String("ignored"),
				"correlation_id": extension.String("corr_1"),
			}),
		},
		"slow path missing correlation": {
			env:     metadataEnvelope("", extension.Object{"tags": extension.String("ignored")}),
			wantErr: "missing correlation_id",
		},
		"slow path mismatch": {
			env: metadataEnvelope("corr_1", extension.Object{
				"tags":           extension.String("ignored"),
				"correlation_id": extension.String("corr_other"),
			}),
			wantErr: "must match",
		},
		"slow path invalid token": {
			env: metadataEnvelope("corr_1", extension.Object{
				"tags":           extension.String("ignored"),
				"correlation_id": extension.String("corr_1"),
				"trace_id":       extension.String("bad trace!"),
			}),
			wantErr: "invalid format",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := tc.env.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestFromJSONScannerMalformedInputs(t *testing.T) {
	cases := map[string]string{
		"empty input":                 "",
		"not an object":               "[]",
		"unterminated object":         "{",
		"trailing data":               `{"a":1}x`,
		"missing colon":               `{"a" 1}`,
		"missing comma":               `{"a":1 "b":2}`,
		"empty scalar":                `{"a":}`,
		"unterminated string":         `{"a":"x`,
		"escape at end":               `{"a":"x\`,
		"control character":           "{\"a\":\"b\x01c\"}",
		"non-string id":               `{"id":123}`,
		"invalid timestamp":           `{"timestamp":"not-a-time"}`,
		"unterminated composite":      `{"a":{`,
		"composite key not string":    `{"a":{bad}}`,
		"composite missing colon":     `{"a":{"k" true}}`,
		"composite missing comma":     `{"a":[1 2]}`,
		"composite empty scalar":      `{"a":[}`,
		"metadata not an object":      `{"metadata":[1]}`,
		"non-string payload encoding": `{"payload_encoding":7}`,
		"non-string schema version":   `{"schema_version":{}}`,
		"non-string correlation":      `{"correlation_id":[1]}`,
		"non-string event type":       `{"event_type":false}`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := FromJSON([]byte(input)); err == nil {
				t.Fatalf("FromJSON(%q) expected error", input)
			}
		})
	}
}

func TestFromJSONScannerFeatures(t *testing.T) {
	raw := ` {
		"id": "esc\"aped",
		"event_type": "orders:create:v1:requested",
		"payload": {"k": "v"},
		"metadata": {"correlation_id": "corr_9", "nested": {"list": [1, true, null, "s"]}},
		"correlation_id": "corr_9",
		"schema_version": "1.0",
		"timestamp": "2026-01-01T00:00:00Z",
		"payload_encoding": "json",
		"unknown_key": [1, {"deep": "skip"}]
	} `
	env, err := FromJSON([]byte(raw))
	if err != nil {
		t.Fatalf("FromJSON() error = %v", err)
	}
	if env.ID != `esc"aped` || env.EventType != "orders:create:v1:requested" || env.CorrelationID != "corr_9" {
		t.Fatalf("unexpected envelope: %+v", env)
	}
	if !env.Timestamp.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("timestamp = %v", env.Timestamp)
	}

	empty, err := FromJSON([]byte(`{}`))
	if err != nil {
		t.Fatalf("FromJSON({}) error = %v", err)
	}
	if empty.ID != "" {
		t.Fatalf("empty envelope = %+v", empty)
	}

	nulls, err := FromJSON([]byte(`{"id":null,"timestamp":"","payload":{},"a":{}}`))
	if err != nil {
		t.Fatalf("FromJSON(null fields) error = %v", err)
	}
	if nulls.ID != "" {
		t.Fatalf("null id = %q", nulls.ID)
	}
}

func TestMaterializeBranches(t *testing.T) {
	var nilEnv *Envelope
	if err := nilEnv.MaterializePayload(); err != nil {
		t.Fatalf("nil MaterializePayload() = %v", err)
	}
	if err := nilEnv.MaterializeMetadata(); err != nil {
		t.Fatalf("nil MaterializeMetadata() = %v", err)
	}

	binary := &Envelope{PayloadEncoding: PayloadEncodingProtobuf, PayloadBytes: []byte{0x1}}
	if err := binary.MaterializePayload(); err != nil || binary.Payload != nil {
		t.Fatalf("binary MaterializePayload() = %v payload=%v", err, binary.Payload)
	}

	badJSON := &Envelope{PayloadBytes: []byte("{bad")}
	if err := badJSON.MaterializePayload(); err == nil {
		t.Fatalf("expected payload decode error")
	}

	already := &Envelope{Payload: extension.Object{"k": extension.String("v")}}
	if err := already.MaterializePayload(); err != nil {
		t.Fatalf("existing payload MaterializePayload() = %v", err)
	}

	emptyBytes := &Envelope{}
	if err := emptyBytes.MaterializePayload(); err != nil || emptyBytes.Payload == nil {
		t.Fatalf("empty MaterializePayload() = %v payload=%v", err, emptyBytes.Payload)
	}

	noLazy := &Envelope{}
	if err := noLazy.MaterializeMetadata(); err != nil || noLazy.Metadata == nil {
		t.Fatalf("no-lazy MaterializeMetadata() = %v metadata=%v", err, noLazy.Metadata)
	}
	if err := noLazy.MaterializeMetadata(); err != nil {
		t.Fatalf("repeat MaterializeMetadata() = %v", err)
	}

	badExtras := &Envelope{lazyMetadata: &foundationpb.Metadata{ExtrasJson: []byte("{bad")}}
	if err := badExtras.MaterializeMetadata(); err == nil {
		t.Fatalf("expected extras decode error")
	}

	if raw, err := encodePayloadObject(nil); err != nil || string(raw) != "{}" {
		t.Fatalf("encodePayloadObject(nil) = %q, %v", raw, err)
	}
}

func TestEnvelopeDispatchReadyBranches(t *testing.T) {
	base := func(mut func(env *Envelope)) Envelope {
		env := Envelope{
			EventType:       "orders:create:v1:requested",
			CorrelationID:   "corr_d",
			SchemaVersion:   EnvelopeSchemaVersion,
			Timestamp:       time.Now().UTC(),
			PayloadEncoding: PayloadEncodingJSON,
			Payload:         extension.Object{},
			Metadata:        extension.Object{"correlation_id": extension.String("corr_d")},
		}
		if mut != nil {
			mut(&env)
		}
		return env
	}
	if !envelopeDispatchReady(base(nil)) {
		t.Fatal("expected ready envelope")
	}
	if envelopeDispatchReady(base(func(env *Envelope) { env.SchemaVersion = "" })) {
		t.Fatal("missing schema should not be ready")
	}
	if envelopeDispatchReady(base(func(env *Envelope) { env.Payload = nil })) {
		t.Fatal("json without payload should not be ready")
	}
	if envelopeDispatchReady(base(func(env *Envelope) { env.Metadata = nil })) {
		t.Fatal("missing metadata should not be ready")
	}
	if !envelopeDispatchReady(base(func(env *Envelope) {
		env.Metadata = extension.Object{"correlationId": extension.String("corr_d")}
	})) {
		t.Fatal("camelCase correlation should be ready")
	}
	if envelopeDispatchReady(base(func(env *Envelope) {
		env.Metadata = extension.Object{"correlation_id": extension.String("corr_other")}
	})) {
		t.Fatal("mismatched correlation should not be ready")
	}
}

func TestRecordEventTraceSkipsMissingCorrelation(t *testing.T) {
	recordEventTrace("emit", Envelope{EventType: "orders:create:v1:requested"})
}

func TestSegmentValidators(t *testing.T) {
	if isLowerSnake("") || isLowerSnake("Bad") || isLowerSnake("has-dash") {
		t.Fatal("isLowerSnake accepted invalid segment")
	}
	if !isLowerSnake("ok_2") {
		t.Fatal("isLowerSnake rejected valid segment")
	}
	if isDigits("") || isDigits("1a") {
		t.Fatal("isDigits accepted invalid input")
	}
	if !isDigits("123") {
		t.Fatal("isDigits rejected digits")
	}
}
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
func ObjectFromMap(input map[string]any) extension.Object {
	if len(input) == 0 {
		return extension.Object{}
	}
	value, err := extension.FromJSON(input)
	if err != nil {
		return extension.Object{}
	}
	out, ok := value.ObjectValue()
	if !ok {
		return extension.Object{}
	}
	return out
}

func objectFromMap(input map[string]any) extension.Object {
	return ObjectFromMap(input)
}
