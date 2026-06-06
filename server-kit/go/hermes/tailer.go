package hermes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	redispkg "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

const (
	defaultTailerBatch = 64
	defaultTailerIdle  = 25 * time.Millisecond
)

type SourceMessage struct {
	ID     string
	Values redispkg.Values
}

type MessageSource interface {
	Read(context.Context, int) ([]SourceMessage, error)
	Ack(context.Context, ...string) error
}

type MessageDecoder func(context.Context, SourceMessage) ([]Event, error)

type Tailer struct {
	store      *Store
	projection string
	source     MessageSource
	decode     MessageDecoder
	maxBatch   int
	idleWait   time.Duration
}

type TailerOptions struct {
	MaxBatch int
	IdleWait time.Duration
}

type TailResult struct {
	Read    int
	Decoded int
	Acked   int
	Apply   ApplyResult
}

func NewTailer(store *Store, projection string, source MessageSource, decode MessageDecoder, opts TailerOptions) (*Tailer, error) {
	projection = strings.TrimSpace(projection)
	if store == nil || projection == "" || source == nil || decode == nil {
		return nil, errors.New("hermes tailer configuration is invalid")
	}
	if opts.MaxBatch <= 0 {
		opts.MaxBatch = defaultTailerBatch
	}
	if opts.IdleWait <= 0 {
		opts.IdleWait = defaultTailerIdle
	}
	return &Tailer{store: store, projection: projection, source: source, decode: decode, maxBatch: opts.MaxBatch, idleWait: opts.IdleWait}, nil
}

func (t *Tailer) PollOnce(ctx context.Context) (TailResult, error) {
	if err := ctxErr(ctx); err != nil {
		return TailResult{}, err
	}
	messages, err := t.source.Read(ctx, t.maxBatch)
	if err != nil || len(messages) == 0 {
		return TailResult{Read: len(messages)}, err
	}
	events, ids, err := t.decodeMessages(ctx, messages)
	if err != nil {
		return TailResult{Read: len(messages)}, err
	}
	result := TailResult{Read: len(messages), Decoded: len(events)}
	if len(events) > 0 {
		result.Apply, err = t.store.ApplyBatch(ctx, t.projection, events)
		if err != nil {
			return result, err
		}
	}
	if len(ids) > 0 {
		if err := t.source.Ack(ctx, ids...); err != nil {
			return result, err
		}
		result.Acked = len(ids)
	}
	return result, nil
}

func (t *Tailer) Run(ctx context.Context) error {
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

func (t *Tailer) decodeMessages(ctx context.Context, messages []SourceMessage) ([]Event, []string, error) {
	events := make([]Event, 0, len(messages))
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		decoded, err := t.decode(ctx, message)
		if err != nil {
			return nil, nil, err
		}
		for i := range decoded {
			fillEventSource(&decoded[i], message.ID, i, len(decoded))
			events = append(events, decoded[i])
		}
		if strings.TrimSpace(message.ID) != "" {
			ids = append(ids, message.ID)
		}
	}
	return events, ids, nil
}

func fillEventSource(event *Event, messageID string, index int, total int) {
	if event.SourceID != "" || messageID == "" {
		return
	}
	if total <= 1 {
		event.SourceID = messageID
		return
	}
	event.SourceID = fmt.Sprintf("%s#%d", messageID, index)
}

func waitForTailerIdle(ctx context.Context, wait time.Duration) error {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type RedisStreamSource struct {
	client   redispkg.Client
	stream   string
	group    string
	consumer string
}

func NewRedisStreamSource(client redispkg.Client, stream string, group string, consumer string) (*RedisStreamSource, error) {
	stream = strings.TrimSpace(stream)
	group = strings.TrimSpace(group)
	consumer = strings.TrimSpace(consumer)
	if client == nil || stream == "" || group == "" || consumer == "" {
		return nil, errors.New("hermes redis stream source configuration is invalid")
	}
	return &RedisStreamSource{client: client, stream: stream, group: group, consumer: consumer}, nil
}

func (s *RedisStreamSource) Read(ctx context.Context, count int) ([]SourceMessage, error) {
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
	out := make([]SourceMessage, len(messages))
	for i, message := range messages {
		out[i] = SourceMessage{ID: message.ID, Values: message.Values}
	}
	return out, nil
}

func (s *RedisStreamSource) Ack(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.client.XAck(ctx, s.stream, s.group, ids...)
}
