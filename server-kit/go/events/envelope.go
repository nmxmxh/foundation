package events

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	PayloadEncodingJSON     = "json"
	PayloadEncodingProtobuf = "protobuf"
	PayloadEncodingCapnp    = "capnp"
	PayloadEncodingBinary   = "binary"
)

type Envelope struct {
	ID              string           `json:"id,omitempty"`
	EventType       string           `json:"event_type"`
	Payload         extension.Object `json:"payload"`
	PayloadBytes    []byte           `json:"-"`
	PayloadEncoding string           `json:"payload_encoding,omitempty"`
	Metadata        extension.Object `json:"metadata"`
	CorrelationID   string           `json:"correlation_id"`
	SchemaVersion   string           `json:"schema_version"`
	Timestamp       time.Time        `json:"timestamp"`
	SourceNodeID    string           `json:"-"`
}

func (e Envelope) Validate() error {
	if err := ValidateEventType(e.EventType); err != nil {
		return err
	}

	if fast, err := validateEnvelopeMetadataFast(e.Metadata, e.CorrelationID); fast {
		if err != nil {
			return err
		}
	} else {
		md := metadata.FromObject(e.Metadata)
		metadataCorrelationID := md.CorrelationID
		correlationID := md.NormalizeCorrelation(e.CorrelationID)
		if correlationID == "" {
			return errors.New("missing correlation_id")
		}
		if e.CorrelationID != "" && metadataCorrelationID != "" && metadataCorrelationID != e.CorrelationID {
			return errors.New("metadata.correlation_id must match envelope correlation_id")
		}
		if err := md.Validate(); err != nil {
			return err
		}
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
	case PayloadEncodingProtobuf, PayloadEncodingCapnp, PayloadEncodingBinary:
		if len(e.PayloadBytes) == 0 {
			return fmt.Errorf("%s payload_encoding requires payload bytes", normalized)
		}
		return nil
	default:
		return errors.New("unsupported payload_encoding")
	}
}

func validateEnvelopeMetadataFast(raw extension.Object, envelopeCorrelationID string) (bool, error) {
	correlationID := strings.TrimSpace(envelopeCorrelationID)
	metadataCorrelationID := ""
	metadataCorrelationIDRaw := ""
	tokenFields := [5]string{}
	tokenCount := 0

	for key, value := range raw {
		switch key {
		case "ai_confidence", "aiConfidence", "validity_period", "validityPeriod", "attributes", "tags", "categories":
			return false, nil
		case "correlation_id", "correlationId":
			str, ok := value.StringValue()
			if !ok {
				continue
			}
			metadataCorrelationIDRaw = str
			metadataCorrelationID = strings.TrimSpace(str)
		case "causation_id", "causationId", "request_id", "requestId", "idempotency_key", "idempotencyKey", "trace_id", "traceId", "span_id", "spanId":
			str, ok := value.StringValue()
			if !ok || strings.TrimSpace(str) == "" {
				continue
			}
			if tokenCount == len(tokenFields) {
				return false, nil
			}
			tokenFields[tokenCount] = strings.TrimSpace(str)
			tokenCount++
		default:
			continue
		}
	}

	if correlationID == "" {
		correlationID = metadataCorrelationID
	}
	if correlationID == "" {
		return true, errors.New("missing correlation_id")
	}
	if envelopeCorrelationID != "" && metadataCorrelationIDRaw != "" && metadataCorrelationIDRaw != envelopeCorrelationID {
		return true, errors.New("metadata.correlation_id must match envelope correlation_id")
	}
	if err := validateMetadataTokenFast("correlation_id", correlationID); err != nil {
		return true, err
	}
	for i := 0; i < tokenCount; i++ {
		if err := validateMetadataTokenFast("token", tokenFields[i]); err != nil {
			return true, err
		}
	}
	return true, nil
}

func validateMetadataTokenFast(name, value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 128 {
		return fmt.Errorf("metadata.%s has invalid format", name)
	}
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == ':' || r == '-' {
			continue
		}
		return fmt.Errorf("metadata.%s has invalid format", name)
	}
	return nil
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
	if e.PayloadEncoding == PayloadEncodingJSON && e.Payload == nil && len(e.PayloadBytes) == 0 {
		e.Payload = extension.Object{}
	}
	md := metadata.FromObject(e.Metadata)
	if e.CorrelationID != "" && md.CorrelationID != "" && e.CorrelationID != md.CorrelationID {
		return
	}
	e.CorrelationID = md.EnsureCorrelation(e.CorrelationID)
	e.Metadata = md.ToObject()
}

// ToJSON serializes envelope into canonical JSON.
func (e Envelope) ToJSON() ([]byte, error) {
	env := e
	env.Normalize()
	if env.PayloadEncoding != PayloadEncodingJSON {
		return nil, errors.New("json envelope serialization only supports json payload encoding")
	}
	if err := env.MaterializePayload(); err != nil {
		return nil, err
	}
	return json.Marshal(envelopeJSON{
		ID:              env.ID,
		EventType:       env.EventType,
		Payload:         env.Payload,
		Metadata:        env.Metadata,
		CorrelationID:   env.CorrelationID,
		SchemaVersion:   env.SchemaVersion,
		Timestamp:       env.Timestamp.UTC().Format(time.RFC3339),
		PayloadEncoding: normalizePayloadEncoding(env.PayloadEncoding),
	})
}

type envelopeJSON struct {
	ID              string           `json:"id,omitempty"`
	EventType       string           `json:"event_type"`
	Payload         extension.Object `json:"payload,omitempty"`
	Metadata        extension.Object `json:"metadata"`
	CorrelationID   string           `json:"correlation_id"`
	SchemaVersion   string           `json:"schema_version"`
	Timestamp       string           `json:"timestamp"`
	PayloadEncoding string           `json:"payload_encoding"`
}

func (e Envelope) ToBinary() ([]byte, error) {
	env := e
	env.Normalize()
	if err := env.Validate(); err != nil {
		return nil, err
	}

	metadataProto, err := metadata.FromObject(env.Metadata).ToTransportProto()
	if err != nil {
		return nil, err
	}

	payload := append([]byte(nil), env.PayloadBytes...)
	if env.PayloadEncoding == PayloadEncodingJSON {
		payload, err = encodePayloadObject(env.Payload)
		if err != nil {
			return nil, err
		}
	}

	return proto.Marshal(&foundationpb.EventEnvelope{
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
	env, err := parseEnvelopeJSON(data)
	if err != nil {
		return Envelope{}, err
	}
	env.Normalize()
	return env, nil
}

func parseEnvelopeJSON(data []byte) (Envelope, error) {
	parser := jsonFieldScanner{data: data}
	return parser.parseEnvelope()
}

type jsonFieldScanner struct {
	data []byte
	pos  int
}

func (s *jsonFieldScanner) parseEnvelope() (Envelope, error) {
	env := Envelope{Metadata: extension.Object{}}
	s.skipSpace()
	if !s.consume('{') {
		return Envelope{}, errors.New("json object expected")
	}
	s.skipSpace()
	if s.consume('}') {
		return env, nil
	}
	for {
		key, err := s.scanString()
		if err != nil {
			return Envelope{}, err
		}
		s.skipSpace()
		if !s.consume(':') {
			return Envelope{}, errors.New("json object key missing ':'")
		}
		s.skipSpace()
		start := s.pos
		if err := s.skipValue(); err != nil {
			return Envelope{}, err
		}
		raw := s.data[start:s.pos]
		if err := assignEnvelopeJSONField(&env, key, raw); err != nil {
			return Envelope{}, err
		}
		s.skipSpace()
		if s.consume('}') {
			s.skipSpace()
			if s.pos != len(s.data) {
				return Envelope{}, errors.New("json envelope contains trailing data")
			}
			return env, nil
		}
		if !s.consume(',') {
			return Envelope{}, errors.New("json object entries must be separated by ','")
		}
		s.skipSpace()
	}
}

func assignEnvelopeJSONField(env *Envelope, key string, raw []byte) error {
	switch key {
	case "id":
		value, err := unquoteJSONField(raw)
		if err != nil {
			return err
		}
		env.ID = value
	case "event_type":
		value, err := unquoteJSONField(raw)
		if err != nil {
			return err
		}
		env.EventType = value
	case "correlation_id":
		value, err := unquoteJSONField(raw)
		if err != nil {
			return err
		}
		env.CorrelationID = value
	case "schema_version":
		value, err := unquoteJSONField(raw)
		if err != nil {
			return err
		}
		env.SchemaVersion = NormalizeSchemaVersion(value)
	case "payload_encoding":
		value, err := unquoteJSONField(raw)
		if err != nil {
			return err
		}
		env.PayloadEncoding = value
	case "timestamp":
		value, err := unquoteJSONField(raw)
		if err != nil {
			return err
		}
		if value == "" {
			return nil
		}
		timestamp, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return errors.New("invalid timestamp format")
		}
		env.Timestamp = timestamp
	case "metadata":
		metadataObject, err := extension.ObjectFromJSON(raw)
		if err != nil {
			return err
		}
		env.Metadata = metadataObject
	case "payload":
		env.PayloadBytes = append(env.PayloadBytes[:0], raw...)
	default:
		return nil
	}
	return nil
}

func unquoteJSONField(raw []byte) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	if raw[0] != '"' {
		return "", errors.New("json envelope field must be a string")
	}
	return strconv.Unquote(string(raw))
}

func (s *jsonFieldScanner) scanString() (string, error) {
	start := s.pos
	if !s.consume('"') {
		return "", errors.New("json string expected")
	}
	escaped := false
	for s.pos < len(s.data) {
		ch := s.data[s.pos]
		s.pos++
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			return strconv.Unquote(string(s.data[start:s.pos]))
		}
		if ch < 0x20 {
			return "", errors.New("json string contains control character")
		}
	}
	return "", io.ErrUnexpectedEOF
}

func (s *jsonFieldScanner) skipValue() error {
	s.skipSpace()
	if s.pos >= len(s.data) {
		return io.ErrUnexpectedEOF
	}
	switch s.data[s.pos] {
	case '"':
		_, err := s.scanString()
		return err
	case '{':
		return s.skipComposite('{', '}')
	case '[':
		return s.skipComposite('[', ']')
	default:
		start := s.pos
		for s.pos < len(s.data) {
			switch s.data[s.pos] {
			case ' ', '\n', '\r', '\t', ',', '}', ']':
				if s.pos == start {
					return errors.New("json scalar expected")
				}
				return nil
			default:
				s.pos++
			}
		}
		return nil
	}
}

func (s *jsonFieldScanner) skipComposite(open, close byte) error {
	if !s.consume(open) {
		return errors.New("json composite expected")
	}
	for {
		s.skipSpace()
		if s.pos >= len(s.data) {
			return io.ErrUnexpectedEOF
		}
		if s.consume(close) {
			return nil
		}
		if open == '{' {
			if _, err := s.scanString(); err != nil {
				return err
			}
			s.skipSpace()
			if !s.consume(':') {
				return errors.New("json object key missing ':'")
			}
		}
		if err := s.skipValue(); err != nil {
			return err
		}
		s.skipSpace()
		if s.consume(close) {
			return nil
		}
		if !s.consume(',') {
			return errors.New("json composite entries must be separated by ','")
		}
	}
}

func (s *jsonFieldScanner) skipSpace() {
	for s.pos < len(s.data) {
		switch s.data[s.pos] {
		case ' ', '\n', '\r', '\t':
			s.pos++
		default:
			return
		}
	}
}

func (s *jsonFieldScanner) consume(ch byte) bool {
	if s.pos >= len(s.data) || s.data[s.pos] != ch {
		return false
	}
	s.pos++
	return true
}

func FromBinary(data []byte) (Envelope, error) {
	message := &foundationpb.EventEnvelope{}
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
		PayloadBytes:    message.GetPayload(),
		PayloadEncoding: payloadEncodingFromProto(message.GetPayloadEncoding()),
		Metadata:        md.ToObject(),
		CorrelationID:   message.GetCorrelationId(),
		SchemaVersion:   NormalizeSchemaVersion(message.GetSchemaVersion()),
		SourceNodeID:    message.GetSourceNodeId(),
	}
	if occurredAt := message.GetOccurredAt(); occurredAt != nil {
		env.Timestamp = occurredAt.AsTime().UTC()
	}
	env.Normalize()
	return env, nil
}

func (e *Envelope) MaterializePayload() error {
	if e == nil || e.Payload != nil {
		return nil
	}
	if normalizePayloadEncoding(e.PayloadEncoding) != PayloadEncodingJSON {
		return nil
	}
	if len(e.PayloadBytes) == 0 {
		e.Payload = extension.Object{}
		return nil
	}
	payload, err := decodePayloadObject(e.PayloadBytes)
	if err != nil {
		return err
	}
	e.Payload = payload
	return nil
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
	case PayloadEncodingProtobuf, PayloadEncodingCapnp, PayloadEncodingBinary:
		return value
	default:
		return value
	}
}

func payloadEncodingToProto(value string) foundationpb.PayloadEncoding {
	switch normalizePayloadEncoding(value) {
	case PayloadEncodingJSON:
		return foundationpb.PayloadEncoding_PAYLOAD_ENCODING_JSON
	case PayloadEncodingProtobuf:
		return foundationpb.PayloadEncoding_PAYLOAD_ENCODING_PROTOBUF
	case PayloadEncodingCapnp:
		return foundationpb.PayloadEncoding_PAYLOAD_ENCODING_CAPNP
	case PayloadEncodingBinary:
		return foundationpb.PayloadEncoding_PAYLOAD_ENCODING_BINARY
	default:
		return foundationpb.PayloadEncoding_PAYLOAD_ENCODING_UNSPECIFIED
	}
}

func payloadEncodingFromProto(value foundationpb.PayloadEncoding) string {
	switch value {
	case foundationpb.PayloadEncoding_PAYLOAD_ENCODING_PROTOBUF:
		return PayloadEncodingProtobuf
	case foundationpb.PayloadEncoding_PAYLOAD_ENCODING_CAPNP:
		return PayloadEncodingCapnp
	case foundationpb.PayloadEncoding_PAYLOAD_ENCODING_BINARY:
		return PayloadEncodingBinary
	case foundationpb.PayloadEncoding_PAYLOAD_ENCODING_JSON, foundationpb.PayloadEncoding_PAYLOAD_ENCODING_UNSPECIFIED:
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
	batch := &foundationpb.EventBatch{
		Envelopes: make([]*foundationpb.EventEnvelope, len(b.Envelopes)),
	}
	for i, e := range b.Envelopes {
		e.Normalize()
		if err := e.Validate(); err != nil {
			return nil, err
		}

		metadataProto, err := metadata.FromObject(e.Metadata).ToTransportProto()
		if err != nil {
			return nil, err
		}

		payload := append([]byte(nil), e.PayloadBytes...)
		if e.PayloadEncoding == PayloadEncodingJSON {
			var err error
			payload, err = encodePayloadObject(e.Payload)
			if err != nil {
				return nil, err
			}
		}

		batch.Envelopes[i] = &foundationpb.EventEnvelope{
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
	message := &foundationpb.EventBatch{}
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
			PayloadBytes:    me.GetPayload(),
			PayloadEncoding: payloadEncodingFromProto(me.GetPayloadEncoding()),
			Metadata:        md.ToObject(),
			CorrelationID:   me.GetCorrelationId(),
			SchemaVersion:   NormalizeSchemaVersion(me.GetSchemaVersion()),
			SourceNodeID:    me.GetSourceNodeId(),
		}
		if occurredAt := me.GetOccurredAt(); occurredAt != nil {
			env.Timestamp = occurredAt.AsTime().UTC()
		}
		env.Normalize()
		batch.Envelopes[i] = env
	}
	return batch, nil
}

func encodePayloadObject(payload extension.Object) ([]byte, error) {
	if payload == nil {
		payload = extension.Object{}
	}
	return payload.MarshalJSON()
}

func decodePayloadObject(data []byte) (extension.Object, error) {
	return extension.ObjectFromJSON(data)
}
