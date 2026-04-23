package events

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
	"go.uber.org/zap"
)

// RedisBus is a redis-backed event bus with local fanout and recent-event caching.
type RedisBus struct {
	client  rediskit.Client
	channel string
	nodeID  string
	logger  logger.Logger

	mu          sync.RWMutex
	subscribers map[string][]Subscriber
	recent      []Envelope
	maxRecent   int

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewRedisBus(client rediskit.Client, channel string, maxRecent int, l logger.Logger) *RedisBus {
	if strings.TrimSpace(channel) == "" {
		channel = "ovasabi:events"
	}
	if maxRecent <= 0 {
		maxRecent = 200
	}
	if l == nil {
		l, _ = logger.NewDefault()
		l = l.With(zap.String("component", "redis_event_bus"))
	}
	ctx, cancel := context.WithCancel(context.Background())
	bus := &RedisBus{
		client:      client,
		channel:     channel,
		nodeID:      fmt.Sprintf("bus-%d", time.Now().UTC().UnixNano()),
		logger:      l,
		subscribers: map[string][]Subscriber{},
		recent:      make([]Envelope, 0, maxRecent),
		maxRecent:   maxRecent,
		ctx:         ctx,
		cancel:      cancel,
	}
	bus.startListener()
	return bus
}

func (b *RedisBus) Publish(ctx context.Context, envelope Envelope) error {
	envelope.Normalize()
	envelope.Metadata = copyMap(envelope.Metadata)
	envelope.SourceNodeID = b.nodeID
	if err := envelope.Validate(); err != nil {
		return err
	}

	raw, err := envelope.ToBinary()
	if err != nil {
		return err
	}
	if err := b.client.Publish(ctx, b.channel, raw); err != nil {
		return err
	}

	localEnvelope := envelope
	localEnvelope.SourceNodeID = ""
	b.record(localEnvelope)
	b.dispatch(ctx, localEnvelope)
	return nil
}

func (b *RedisBus) Subscribe(pattern string, subscriber Subscriber) {
	if subscriber == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subscribers[pattern] = append(b.subscribers[pattern], subscriber)
}

func (b *RedisBus) Recent(limit int) []Envelope {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if limit <= 0 || limit > len(b.recent) {
		limit = len(b.recent)
	}
	out := make([]Envelope, limit)
	copy(out, b.recent[len(b.recent)-limit:])
	return out
}

func (b *RedisBus) Close() error {
	if b == nil {
		return nil
	}
	b.cancel()
	b.wg.Wait()
	if b.client != nil {
		return b.client.Close()
	}
	return nil
}

func (b *RedisBus) startListener() {
	if b.client == nil {
		return
	}
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		backoff := time.Second
		for {
			select {
			case <-b.ctx.Done():
				return
			default:
			}

			msgs, cancel, err := b.client.Subscribe(b.ctx, b.channel)
			if err != nil {
				b.logger.Warn("event bus subscribe failed", zap.String("channel", b.channel), zap.Error(err))
				if !sleepWithContext(b.ctx, backoff) {
					return
				}
				if backoff < 5*time.Second {
					backoff *= 2
				}
				continue
			}
			backoff = time.Second

			b.consumeLoop(msgs)
			cancel()
		}
	}()
}

func (b *RedisBus) consumeLoop(msgs <-chan []byte) {
	for {
		select {
		case <-b.ctx.Done():
			return
		case raw, ok := <-msgs:
			if !ok {
				return
			}
			envelope, err := Decode(raw)
			if err != nil {
				b.logger.Warn("invalid event envelope from redis", zap.Error(err))
				continue
			}
			if sourceNode := envelope.SourceNodeID; sourceNode == "" {
				if legacySourceNode, _ := envelope.Metadata["_bus_node_id"].(string); legacySourceNode == b.nodeID {
					continue
				}
			} else if sourceNode == b.nodeID {
				continue
			}
			envelope.Metadata = copyMap(envelope.Metadata)
			delete(envelope.Metadata, "_bus_node_id")
			envelope.SourceNodeID = ""
			b.record(envelope)
			b.dispatch(b.ctx, envelope)
		}
	}
}

func (b *RedisBus) record(envelope Envelope) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.recent = append(b.recent, envelope)
	if len(b.recent) > b.maxRecent {
		b.recent = b.recent[len(b.recent)-b.maxRecent:]
	}
}

func (b *RedisBus) dispatch(ctx context.Context, envelope Envelope) {
	b.mu.RLock()
	matching := make([]Subscriber, 0, 8)
	for pattern, subs := range b.subscribers {
		if Matches(pattern, envelope.EventType) {
			matching = append(matching, subs...)
		}
	}
	b.mu.RUnlock()

	for _, subscriber := range matching {
		subscriber(ctx, envelope)
	}
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func copyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
