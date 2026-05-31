package hermes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	redispkg "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
	"google.golang.org/protobuf/proto"
)

const (
	ProjectionEnvelopeEventType = "hermes:projection:v1:success"
	ProjectionPayloadSchema     = "foundation.hermes.projection.v1"
	maxProjectionMutations      = 4096
)

type EnvelopeMessage struct {
	AckID    string
	Envelope events.Envelope
}

type EnvelopeSource interface {
	ReadEnvelopes(context.Context, int) ([]EnvelopeMessage, error)
	Ack(context.Context, ...string) error
}

type EnvelopeTailer struct {
	store      *Store
	projection string
	source     EnvelopeSource
	maxBatch   int
	idleWait   time.Duration
}

func NewProjectionEnvelope(mutations []*foundationpb.RecordMutation, correlationID string) (events.Envelope, error) {
	raw, err := proto.Marshal(&foundationpb.RecordMutationBatch{Mutations: mutations})
	if err != nil {
		return events.Envelope{}, err
	}
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return events.Envelope{}, errors.New("hermes projection envelope correlation_id is required")
	}
	return events.Envelope{
		EventType:       ProjectionEnvelopeEventType,
		PayloadBytes:    raw,
		PayloadEncoding: events.PayloadEncodingProtobuf,
		Metadata:        map[string]any{"correlation_id": correlationID},
		CorrelationID:   correlationID,
		SchemaVersion:   events.EnvelopeSchemaVersion,
		Timestamp:       time.Now().UTC(),
	}, nil
}

func (s *Store) ApplyEnvelopeBytes(ctx context.Context, projection string, raw []byte) (ApplyResult, error) {
	envelope, err := events.FromBinary(raw)
	if err != nil {
		return ApplyResult{}, err
	}
	return s.ApplyEnvelope(ctx, projection, envelope)
}

func (s *Store) ApplyEnvelope(ctx context.Context, projection string, envelope events.Envelope) (ApplyResult, error) {
	return s.ApplyEnvelopes(ctx, projection, []events.Envelope{envelope})
}

func (s *Store) ApplyEnvelopes(ctx context.Context, projection string, envelopes []events.Envelope) (ApplyResult, error) {
	if err := ctxErr(ctx); err != nil {
		return ApplyResult{}, err
	}
	out := make([]Event, 0, len(envelopes))
	for _, envelope := range envelopes {
		if err := ctxErr(ctx); err != nil {
			return ApplyResult{}, err
		}
		decoded, err := EventsFromEnvelope(envelope)
		if err != nil {
			return ApplyResult{}, err
		}
		out = append(out, decoded...)
	}
	if len(out) == 0 {
		part, err := s.partition(projection)
		if err != nil {
			return ApplyResult{}, err
		}
		return ApplyResult{Epoch: part.epoch.Load()}, nil
	}
	return s.ApplyBatch(ctx, projection, out)
}

func EventsFromEnvelope(envelope events.Envelope) ([]Event, error) {
	envelope.Normalize()
	if envelope.PayloadEncoding != events.PayloadEncodingProtobuf {
		return nil, fmt.Errorf("%w: hermes projection envelope must use protobuf payload encoding", ErrInvalidEvent)
	}
	if state := events.TerminalState(envelope.EventType); state != "success" && state != "ack" {
		return nil, fmt.Errorf("%w: hermes projection envelope must be terminal", ErrInvalidEvent)
	}
	if err := envelope.Validate(); err != nil {
		return nil, err
	}
	var batch foundationpb.RecordMutationBatch
	if err := proto.Unmarshal(envelope.PayloadBytes, &batch); err != nil {
		return nil, err
	}
	mutations := batch.GetMutations()
	if len(mutations) > maxProjectionMutations {
		return nil, fmt.Errorf("%w: hermes projection envelope mutation count exceeds %d", ErrProjectionLimit, maxProjectionMutations)
	}
	out := make([]Event, 0, len(mutations))
	for i, mutation := range mutations {
		event, err := eventFromMutation(envelope, mutation, i, len(mutations))
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, nil
}

func eventFromMutation(envelope events.Envelope, mutation *foundationpb.RecordMutation, index int, total int) (Event, error) {
	if mutation == nil {
		return Event{}, fmt.Errorf("%w: nil hermes projection mutation", ErrInvalidEvent)
	}
	rec, err := recordFromMutation(mutation)
	if err != nil {
		return Event{}, err
	}
	return Event{
		Operation:     operationFromMutation(mutation.GetOperation()),
		SourceID:      sourceIDFromMutation(envelope, mutation, index, total),
		Version:       mutation.GetVersion(),
		CorrelationID: correlationIDFromMutation(envelope, mutation),
		Record:        rec,
	}, nil
}

func recordFromMutation(mutation *foundationpb.RecordMutation) (database.DomainRecord, error) {
	data := make(map[string]any, len(mutation.GetFields()))
	for _, field := range mutation.GetFields() {
		name := strings.TrimSpace(field.GetName())
		if name == "" {
			return database.DomainRecord{}, fmt.Errorf("%w: hermes projection field name is required", ErrInvalidEvent)
		}
		if _, exists := data[name]; exists {
			return database.DomainRecord{}, fmt.Errorf("%w: duplicate hermes projection field %q", ErrInvalidEvent, name)
		}
		value, err := scalarFromProto(field.GetValue())
		if err != nil {
			return database.DomainRecord{}, err
		}
		data[name] = value
	}
	return database.DomainRecord{
		Domain:         strings.TrimSpace(mutation.GetDomain()),
		Collection:     strings.TrimSpace(mutation.GetCollection()),
		OrganizationID: strings.TrimSpace(mutation.GetOrganizationId()),
		RecordID:       strings.TrimSpace(mutation.GetRecordId()),
		Data:           data,
		Vector:         append([]float32(nil), mutation.GetVector()...),
		CreatedAt:      timestampTime(mutation.GetCreatedAt()),
		UpdatedAt:      timestampTime(mutation.GetUpdatedAt()),
	}, nil
}

func scalarFromProto(value *foundationpb.ScalarValue) (any, error) {
	if value == nil {
		return nil, fmt.Errorf("%w: hermes projection field value is required", ErrInvalidEvent)
	}
	switch typed := value.GetKind().(type) {
	case *foundationpb.ScalarValue_StringValue:
		return typed.StringValue, nil
	case *foundationpb.ScalarValue_Int64Value:
		return typed.Int64Value, nil
	case *foundationpb.ScalarValue_Uint64Value:
		return typed.Uint64Value, nil
	case *foundationpb.ScalarValue_DoubleValue:
		return typed.DoubleValue, nil
	case *foundationpb.ScalarValue_BoolValue:
		return typed.BoolValue, nil
	case *foundationpb.ScalarValue_BytesValue:
		return append([]byte(nil), typed.BytesValue...), nil
	default:
		return nil, fmt.Errorf("%w: unsupported hermes projection field value", ErrInvalidEvent)
	}
}

func operationFromMutation(operation foundationpb.ProjectionOperation) Operation {
	if operation == foundationpb.ProjectionOperation_PROJECTION_OPERATION_DELETE {
		return OperationDelete
	}
	return OperationUpsert
}

func sourceIDFromMutation(envelope events.Envelope, mutation *foundationpb.RecordMutation, index int, total int) string {
	if sourceID := strings.TrimSpace(mutation.GetSourceId()); sourceID != "" {
		return sourceID
	}
	base := strings.TrimSpace(envelope.ID)
	if base == "" {
		base = strings.TrimSpace(envelope.CorrelationID)
	}
	if total <= 1 || base == "" {
		return base
	}
	return fmt.Sprintf("%s#%d", base, index)
}

func correlationIDFromMutation(envelope events.Envelope, mutation *foundationpb.RecordMutation) string {
	if correlationID := strings.TrimSpace(mutation.GetCorrelationId()); correlationID != "" {
		return correlationID
	}
	return strings.TrimSpace(envelope.CorrelationID)
}

func timestampTime(ts interface{ AsTime() time.Time }) time.Time {
	if ts == nil {
		return time.Time{}
	}
	return ts.AsTime().UTC()
}

func NewEnvelopeTailer(store *Store, projection string, source EnvelopeSource, opts TailerOptions) (*EnvelopeTailer, error) {
	projection = strings.TrimSpace(projection)
	if store == nil || projection == "" || source == nil {
		return nil, errors.New("hermes envelope tailer configuration is invalid")
	}
	if opts.MaxBatch <= 0 {
		opts.MaxBatch = defaultTailerBatch
	}
	if opts.IdleWait <= 0 {
		opts.IdleWait = defaultTailerIdle
	}
	return &EnvelopeTailer{store: store, projection: projection, source: source, maxBatch: opts.MaxBatch, idleWait: opts.IdleWait}, nil
}

func (t *EnvelopeTailer) PollOnce(ctx context.Context) (TailResult, error) {
	if err := ctxErr(ctx); err != nil {
		return TailResult{}, err
	}
	messages, err := t.source.ReadEnvelopes(ctx, t.maxBatch)
	if err != nil || len(messages) == 0 {
		return TailResult{Read: len(messages)}, err
	}
	envelopes := make([]events.Envelope, len(messages))
	ids := make([]string, 0, len(messages))
	for i, message := range messages {
		envelopes[i] = message.Envelope
		if strings.TrimSpace(message.AckID) != "" {
			ids = append(ids, message.AckID)
		}
	}
	result := TailResult{Read: len(messages), Decoded: len(envelopes)}
	result.Apply, err = t.store.ApplyEnvelopes(ctx, t.projection, envelopes)
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

func (t *EnvelopeTailer) Run(ctx context.Context) error {
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

type RedisStreamEnvelopeSource struct {
	client   redispkg.Client
	stream   string
	group    string
	consumer string
	field    string
}

func NewRedisStreamEnvelopeSource(client redispkg.Client, stream string, group string, consumer string, field string) (*RedisStreamEnvelopeSource, error) {
	stream = strings.TrimSpace(stream)
	group = strings.TrimSpace(group)
	consumer = strings.TrimSpace(consumer)
	field = strings.TrimSpace(field)
	if field == "" {
		field = "envelope"
	}
	if client == nil || stream == "" || group == "" || consumer == "" {
		return nil, errors.New("hermes redis envelope source configuration is invalid")
	}
	return &RedisStreamEnvelopeSource{client: client, stream: stream, group: group, consumer: consumer, field: field}, nil
}

func (s *RedisStreamEnvelopeSource) ReadEnvelopes(ctx context.Context, count int) ([]EnvelopeMessage, error) {
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
	out := make([]EnvelopeMessage, 0, len(messages))
	for _, message := range messages {
		raw, ok := payloadBytes(message.Values, s.field)
		if !ok {
			return nil, fmt.Errorf("%w: redis stream envelope field %q is required", ErrInvalidEvent, s.field)
		}
		envelope, err := events.FromBinary(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, EnvelopeMessage{AckID: message.ID, Envelope: envelope})
	}
	return out, nil
}

func (s *RedisStreamEnvelopeSource) Ack(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.client.XAck(ctx, s.stream, s.group, ids...)
}
