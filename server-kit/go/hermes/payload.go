package hermes

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	redispkg "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

type RecordPayload struct {
	Operation     Operation
	SourceID      string
	Version       uint64
	EventType     string
	SchemaVersion string
	Payload       []byte
}

type RecordPayloadDecoder func(context.Context, RecordPayload) (database.DomainRecord, error)
type RecordPayloadEventDecoder func(context.Context, []RecordPayload, []Event) ([]Event, error)

type PayloadMessage struct {
	AckID   string
	Payload RecordPayload
}

type PayloadSource interface {
	ReadPayloads(context.Context, int) ([]PayloadMessage, error)
	Ack(context.Context, ...string) error
}

type PayloadTailer struct {
	store      *Store
	projection string
	source     PayloadSource
	decode     RecordPayloadDecoder
	maxBatch   int
	idleWait   time.Duration
}

// ApplyRecordPayloads keeps transport bytes opaque until the caller-owned
// decoder turns them into canonical records. Use generated Cap'n Proto,
// protobuf, or runtime frame decoders here; JSON belongs only in compatibility
// adapters outside Hermes.
func (s *Store) ApplyRecordPayloads(
	ctx context.Context,
	projection string,
	payloads []RecordPayload,
	decode RecordPayloadDecoder,
) (ApplyResult, error) {
	if decode == nil {
		return ApplyResult{}, errors.New("hermes record payload decoder is required")
	}
	events := make([]Event, 0, len(payloads))
	for _, payload := range payloads {
		if err := ctxErr(ctx); err != nil {
			return ApplyResult{}, err
		}
		record, err := decode(ctx, payload)
		if err != nil {
			return ApplyResult{}, err
		}
		operation := payload.Operation
		if operation == "" {
			operation = OperationUpsert
		}
		events = append(events, Event{
			Operation: operation,
			SourceID:  strings.TrimSpace(payload.SourceID),
			Version:   payload.Version,
			Record:    record,
		})
	}
	return s.ApplyBatch(ctx, projection, events)
}

// ApplyRecordPayloadEvents is the generated-decoder lane. The decoder receives
// the whole payload batch and appends ready-to-apply Events into caller-provided
// storage, avoiding a per-payload callback and allowing generated schemas to
// preserve operation/source/version metadata without generic transport maps.
func (s *Store) ApplyRecordPayloadEvents(
	ctx context.Context,
	projection string,
	payloads []RecordPayload,
	decode RecordPayloadEventDecoder,
) (ApplyResult, error) {
	if decode == nil {
		return ApplyResult{}, errors.New("hermes record payload event decoder is required")
	}
	if err := ctxErr(ctx); err != nil {
		return ApplyResult{}, err
	}
	events, err := decode(ctx, payloads, make([]Event, 0, len(payloads)))
	if err != nil {
		return ApplyResult{}, err
	}
	return s.ApplyBatch(ctx, projection, events)
}

func NewPayloadTailer(store *Store, projection string, source PayloadSource, decode RecordPayloadDecoder, opts TailerOptions) (*PayloadTailer, error) {
	projection = strings.TrimSpace(projection)
	if store == nil || projection == "" || source == nil || decode == nil {
		return nil, errors.New("hermes payload tailer configuration is invalid")
	}
	if opts.MaxBatch <= 0 {
		opts.MaxBatch = defaultTailerBatch
	}
	if opts.IdleWait <= 0 {
		opts.IdleWait = defaultTailerIdle
	}
	return &PayloadTailer{store: store, projection: projection, source: source, decode: decode, maxBatch: opts.MaxBatch, idleWait: opts.IdleWait}, nil
}

func (t *PayloadTailer) PollOnce(ctx context.Context) (TailResult, error) {
	if err := ctxErr(ctx); err != nil {
		return TailResult{}, err
	}
	messages, err := t.source.ReadPayloads(ctx, t.maxBatch)
	if err != nil || len(messages) == 0 {
		return TailResult{Read: len(messages)}, err
	}
	payloads := make([]RecordPayload, len(messages))
	ids := make([]string, 0, len(messages))
	for i, message := range messages {
		payloads[i] = message.Payload
		if strings.TrimSpace(message.AckID) != "" {
			ids = append(ids, message.AckID)
		}
	}
	result := TailResult{Read: len(messages), Decoded: len(payloads)}
	result.Apply, err = t.store.ApplyRecordPayloads(ctx, t.projection, payloads, t.decode)
	if err != nil {
		return result, err
	}
	if len(ids) > 0 {
		if err := t.source.Ack(ctx, ids...); err != nil {
			return result, err
		}
		result.Acked = len(ids)
	}
	return result, nil
}

func (t *PayloadTailer) Run(ctx context.Context) error {
	for {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		result, err := t.PollOnce(ctx)
		if err != nil {
			return err
		}
		if result.Read > 0 {
			continue
		}
		if err := waitForTailerIdle(ctx, t.idleWait); err != nil {
			return err
		}
	}
}

type RedisStreamPayloadSource struct {
	client       redispkg.Client
	stream       string
	group        string
	consumer     string
	payloadField string
}

func NewRedisStreamPayloadSource(client redispkg.Client, stream string, group string, consumer string, payloadField string) (*RedisStreamPayloadSource, error) {
	stream = strings.TrimSpace(stream)
	group = strings.TrimSpace(group)
	consumer = strings.TrimSpace(consumer)
	payloadField = strings.TrimSpace(payloadField)
	if payloadField == "" {
		payloadField = "payload"
	}
	if client == nil || stream == "" || group == "" || consumer == "" {
		return nil, errors.New("hermes redis payload source configuration is invalid")
	}
	return &RedisStreamPayloadSource{client: client, stream: stream, group: group, consumer: consumer, payloadField: payloadField}, nil
}

func (s *RedisStreamPayloadSource) ReadPayloads(ctx context.Context, count int) ([]PayloadMessage, error) {
	messages, err := s.client.XReadGroupPending(ctx, s.stream, s.group, s.consumer, int64(count))
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		messages, err = s.client.XReadGroup(ctx, s.stream, s.group, s.consumer, int64(count))
	}
	if err != nil || len(messages) == 0 {
		return nil, err
	}
	out := make([]PayloadMessage, 0, len(messages))
	for _, message := range messages {
		payload, ok := payloadBytes(message.Values, s.payloadField)
		if !ok {
			return nil, fmt.Errorf("%w: redis stream payload field %q is required", ErrInvalidEvent, s.payloadField)
		}
		out = append(out, PayloadMessage{
			AckID: message.ID,
			Payload: RecordPayload{
				Operation:     operationValue(message.Values["operation"]),
				SourceID:      sourceIDValue(message),
				Version:       uint64Value(message.Values["version"]),
				EventType:     stringValue(message.Values["event_type"]),
				SchemaVersion: stringValue(message.Values["schema_version"]),
				Payload:       payload,
			},
		})
	}
	return out, nil
}

func (s *RedisStreamPayloadSource) Ack(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.client.XAck(ctx, s.stream, s.group, ids...)
}

func SourceMessagePayload(message SourceMessage, field string) ([]byte, bool) {
	return payloadBytes(message.Values, field)
}

func payloadBytes(values map[string]any, field string) ([]byte, bool) {
	value, ok := values[field]
	if !ok {
		return nil, false
	}
	switch typed := value.(type) {
	case []byte:
		return typed, true
	case string:
		return []byte(typed), true
	default:
		return nil, false
	}
}

func sourceIDValue(message redispkg.StreamMessage) string {
	if value := strings.TrimSpace(stringValue(message.Values["source_id"])); value != "" {
		return value
	}
	return message.ID
}

func operationValue(value any) Operation {
	switch strings.ToLower(strings.TrimSpace(stringValue(value))) {
	case string(OperationDelete):
		return OperationDelete
	default:
		return OperationUpsert
	}
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(value)
	}
}

func uint64Value(value any) uint64 {
	switch typed := value.(type) {
	case uint64:
		return typed
	case uint:
		return uint64(typed)
	case int:
		if typed > 0 {
			return uint64(typed)
		}
	case int64:
		if typed > 0 {
			return uint64(typed)
		}
	case float64:
		if typed > 0 && math.Trunc(typed) == typed {
			return uint64(typed)
		}
	case string:
		parsed, _ := strconv.ParseUint(strings.TrimSpace(typed), 10, 64)
		return parsed
	}
	return 0
}
