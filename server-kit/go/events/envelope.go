package events

import (
	"encoding/json"
	"errors"
	"time"

	transportpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/transport/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	PayloadEncodingJSON     = "json"
	PayloadEncodingProtobuf = "protobuf"
)

type Envelope struct {
	ID              string         `json:"id,omitempty"`
	EventType       string         `json:"event_type"`
	Payload         map[string]any `json:"payload"`
	PayloadBytes    []byte         `json:"-"`
	PayloadEncoding string         `json:"payload_encoding,omitempty"`
	Metadata        map[string]any `json:"metadata"`
	CorrelationID   string         `json:"correlation_id"`
	SchemaVersion   string         `json:"schema_version"`
	Timestamp       time.Time      `json:"timestamp"`
	SourceNodeID    string         `json:"-"`
}

func (e Envelope) Validate() error {
	if err := ValidateEventType(e.EventType); err != nil {
		return err
	}

	md := metadata.FromMap(e.Metadata)
	correlationID := e.CorrelationID
	if correlationID == "" {
		correlationID = md.CorrelationID
	}
	if correlationID == "" {
		return errors.New("missing correlation_id")
	}
	if md.CorrelationID != "" && md.CorrelationID != correlationID {
		return errors.New("metadata.correlation_id must match envelope correlation_id")
	}
	md.CorrelationID = correlationID
	if err := md.Validate(); err != nil {
		return err
	}

	if err := ValidateSchemaVersion(e.SchemaVersion); err != nil {
		return err
	}
	if e.Timestamp.IsZero() {
		return errors.New("missing timestamp")
	}
	switch normalized := normalizePayloadEncoding(e.PayloadEncoding); normalized {
	case PayloadEncodingJSON:
		return nil
	case PayloadEncodingProtobuf:
		if len(e.PayloadBytes) == 0 {
			return errors.New("protobuf payload_encoding requires payload bytes")
		}
		return nil
	default:
		return errors.New("unsupported payload_encoding")
	}
}

// Normalize mutates envelope defaults for schema version and timestamp.
func (e *Envelope) Normalize() {
	if e == nil {
		return
	}
	e.SchemaVersion = NormalizeSchemaVersion(e.SchemaVersion)
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	e.PayloadEncoding = normalizePayloadEncoding(e.PayloadEncoding)
	if e.PayloadEncoding == PayloadEncodingJSON && e.Payload == nil {
		e.Payload = map[string]any{}
	}
}

// ToMap creates a JSON-friendly envelope map shape.
func (e Envelope) ToMap() map[string]any {
	result := map[string]any{
		"event_type":       e.EventType,
		"metadata":         e.Metadata,
		"correlation_id":   e.CorrelationID,
		"schema_version":   e.SchemaVersion,
		"timestamp":        e.Timestamp.UTC().Format(time.RFC3339),
		"payload_encoding": normalizePayloadEncoding(e.PayloadEncoding),
	}
	if e.ID != "" {
		result["id"] = e.ID
	}
	if normalizePayloadEncoding(e.PayloadEncoding) == PayloadEncodingJSON {
		result["payload"] = e.Payload
	}
	return result
}

// ToJSON serializes envelope into canonical JSON.
func (e Envelope) ToJSON() ([]byte, error) {
	env := e
	env.Normalize()
	if env.PayloadEncoding != PayloadEncodingJSON {
		return nil, errors.New("json envelope serialization only supports json payload encoding")
	}
	return json.Marshal(env.ToMap())
}

func (e Envelope) ToBinary() ([]byte, error) {
	env := e
	env.Normalize()
	if err := env.Validate(); err != nil {
		return nil, err
	}

	metadataProto, err := metadata.FromMap(env.Metadata).ToTransportProto()
	if err != nil {
		return nil, err
	}

	payload := append([]byte(nil), env.PayloadBytes...)
	if env.PayloadEncoding == PayloadEncodingJSON {
		payload, err = json.Marshal(env.Payload)
		if err != nil {
			return nil, err
		}
	}

	return proto.Marshal(&transportpb.EventEnvelope{
		Id:              env.ID,
		EventType:       env.EventType,
		Payload:         payload,
		Metadata:        metadataProto,
		CorrelationId:   env.CorrelationID,
		SchemaVersion:   env.SchemaVersion,
		OccurredAt:      timestamppb.New(env.Timestamp.UTC()),
		PayloadEncoding: payloadEncodingToProto(env.PayloadEncoding),
		SourceNodeId:    env.SourceNodeID,
	})
}

// FromJSON parses and normalizes an envelope.
func FromJSON(data []byte) (Envelope, error) {
	var raw struct {
		ID              string         `json:"id"`
		EventType       string         `json:"event_type"`
		Payload         map[string]any `json:"payload"`
		Metadata        map[string]any `json:"metadata"`
		CorrelationID   string         `json:"correlation_id"`
		SchemaVersion   string         `json:"schema_version"`
		Timestamp       string         `json:"timestamp"`
		PayloadEncoding string         `json:"payload_encoding"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Envelope{}, err
	}

	timestamp := time.Time{}
	if raw.Timestamp != "" {
		t, err := time.Parse(time.RFC3339, raw.Timestamp)
		if err != nil {
			return Envelope{}, errors.New("invalid timestamp format")
		}
		timestamp = t
	}

	env := Envelope{
		ID:              raw.ID,
		EventType:       raw.EventType,
		Payload:         raw.Payload,
		PayloadEncoding: raw.PayloadEncoding,
		Metadata:        raw.Metadata,
		CorrelationID:   raw.CorrelationID,
		SchemaVersion:   NormalizeSchemaVersion(raw.SchemaVersion),
		Timestamp:       timestamp,
	}
	env.Normalize()
	return env, nil
}

func FromBinary(data []byte) (Envelope, error) {
	message := &transportpb.EventEnvelope{}
	if err := proto.Unmarshal(data, message); err != nil {
		return Envelope{}, err
	}

	md, err := metadata.FromTransportProto(message.GetMetadata())
	if err != nil {
		return Envelope{}, err
	}

	env := Envelope{
		ID:              message.GetId(),
		EventType:       message.GetEventType(),
		PayloadBytes:    append([]byte(nil), message.GetPayload()...),
		PayloadEncoding: payloadEncodingFromProto(message.GetPayloadEncoding()),
		Metadata:        md.ToMap(),
		CorrelationID:   message.GetCorrelationId(),
		SchemaVersion:   NormalizeSchemaVersion(message.GetSchemaVersion()),
		SourceNodeID:    message.GetSourceNodeId(),
	}
	if occurredAt := message.GetOccurredAt(); occurredAt != nil {
		env.Timestamp = occurredAt.AsTime().UTC()
	}
	env.Normalize()
	if env.PayloadEncoding == PayloadEncodingJSON {
		if len(env.PayloadBytes) == 0 {
			env.Payload = map[string]any{}
			return env, nil
		}
		if err := json.Unmarshal(env.PayloadBytes, &env.Payload); err != nil {
			return Envelope{}, err
		}
		if env.Payload == nil {
			env.Payload = map[string]any{}
		}
		return env, nil
	}
	return env, nil
}

func Decode(data []byte) (Envelope, error) {
	if env, err := FromBinary(data); err == nil {
		return env, nil
	}
	return FromJSON(data)
}

func normalizePayloadEncoding(value string) string {
	switch value {
	case "", PayloadEncodingJSON:
		return PayloadEncodingJSON
	case PayloadEncodingProtobuf:
		return PayloadEncodingProtobuf
	default:
		return value
	}
}

func payloadEncodingToProto(value string) transportpb.PayloadEncoding {
	switch normalizePayloadEncoding(value) {
	case PayloadEncodingJSON:
		return transportpb.PayloadEncoding_PAYLOAD_ENCODING_JSON
	case PayloadEncodingProtobuf:
		return transportpb.PayloadEncoding_PAYLOAD_ENCODING_PROTOBUF
	default:
		return transportpb.PayloadEncoding_PAYLOAD_ENCODING_UNSPECIFIED
	}
}

func payloadEncodingFromProto(value transportpb.PayloadEncoding) string {
	switch value {
	case transportpb.PayloadEncoding_PAYLOAD_ENCODING_PROTOBUF:
		return PayloadEncodingProtobuf
	case transportpb.PayloadEncoding_PAYLOAD_ENCODING_JSON, transportpb.PayloadEncoding_PAYLOAD_ENCODING_UNSPECIFIED:
		return PayloadEncodingJSON
	default:
		return PayloadEncodingJSON
	}
}

// Batch is a collection of envelopes for vectorized processing.
type Batch struct {
	Envelopes []Envelope `json:"envelopes"`
}

func (b Batch) ToBinary() ([]byte, error) {
	batch := &transportpb.EventBatch{
		Envelopes: make([]*transportpb.EventEnvelope, len(b.Envelopes)),
	}
	for i, e := range b.Envelopes {
		e.Normalize()
		if err := e.Validate(); err != nil {
			return nil, err
		}

		metadataProto, err := metadata.FromMap(e.Metadata).ToTransportProto()
		if err != nil {
			return nil, err
		}

		payload := append([]byte(nil), e.PayloadBytes...)
		if e.PayloadEncoding == PayloadEncodingJSON {
			var err error
			payload, err = json.Marshal(e.Payload)
			if err != nil {
				return nil, err
			}
		}

		batch.Envelopes[i] = &transportpb.EventEnvelope{
			Id:              e.ID,
			EventType:       e.EventType,
			Payload:         payload,
			Metadata:        metadataProto,
			CorrelationId:   e.CorrelationID,
			SchemaVersion:   e.SchemaVersion,
			OccurredAt:      timestamppb.New(e.Timestamp.UTC()),
			PayloadEncoding: payloadEncodingToProto(e.PayloadEncoding),
			SourceNodeId:    e.SourceNodeID,
		}
	}
	return proto.Marshal(batch)
}

func FromBatchBinary(data []byte) (Batch, error) {
	message := &transportpb.EventBatch{}
	if err := proto.Unmarshal(data, message); err != nil {
		return Batch{}, err
	}

	batch := Batch{
		Envelopes: make([]Envelope, len(message.GetEnvelopes())),
	}
	for i, me := range message.GetEnvelopes() {
		md, err := metadata.FromTransportProto(me.GetMetadata())
		if err != nil {
			return Batch{}, err
		}

		env := Envelope{
			ID:              me.GetId(),
			EventType:       me.GetEventType(),
			PayloadBytes:    append([]byte(nil), me.GetPayload()...),
			PayloadEncoding: payloadEncodingFromProto(me.GetPayloadEncoding()),
			Metadata:        md.ToMap(),
			CorrelationID:   me.GetCorrelationId(),
			SchemaVersion:   NormalizeSchemaVersion(me.GetSchemaVersion()),
			SourceNodeID:    me.GetSourceNodeId(),
		}
		if occurredAt := me.GetOccurredAt(); occurredAt != nil {
			env.Timestamp = occurredAt.AsTime().UTC()
		}
		env.Normalize()
		if env.PayloadEncoding == PayloadEncodingJSON && len(env.PayloadBytes) > 0 {
			_ = json.Unmarshal(env.PayloadBytes, &env.Payload)
		}
		batch.Envelopes[i] = env
	}
	return batch, nil
}
