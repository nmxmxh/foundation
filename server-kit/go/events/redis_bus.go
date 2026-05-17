package events

import (
	"context"
	"fmt"
	"maps"
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

	mu                 sync.RWMutex
	exactSubscribers   map[string][]Subscriber
	allSubscribers     []Subscriber
	prefixSubscribers  map[string][]Subscriber
	patternSubscribers map[string][]Subscriber
	recent             []Envelope
	recentStart        int
	recentCount        int
	maxRecent          int

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
		client:             client,
		channel:            channel,
		nodeID:             fmt.Sprintf("bus-%d", time.Now().UTC().UnixNano()),
		logger:             l,
		exactSubscribers:   map[string][]Subscriber{},
		prefixSubscribers:  map[string][]Subscriber{},
		patternSubscribers: map[string][]Subscriber{},
		recent:             make([]Envelope, maxRecent),
		maxRecent:          maxRecent,
		ctx:                ctx,
		cancel:             cancel,
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
	recordEventTrace("event.publish", localEnvelope)
	b.dispatch(ctx, localEnvelope)
	return nil
}

func (b *RedisBus) Subscribe(pattern string, subscriber Subscriber) {
	if subscriber == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if exactEventPattern(pattern) {
		b.exactSubscribers[pattern] = appendSubscriber(b.exactSubscribers[pattern], subscriber)
		return
	}
	if prefix, ok := prefixWildcardPattern(pattern); ok {
		if prefix == "" {
			b.allSubscribers = appendSubscriber(b.allSubscribers, subscriber)
			return
		}
		b.prefixSubscribers[prefix] = appendSubscriber(b.prefixSubscribers[prefix], subscriber)
		return
	}
	b.patternSubscribers[pattern] = appendSubscriber(b.patternSubscribers[pattern], subscriber)
}

func (b *RedisBus) Recent(limit int) []Envelope {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if limit <= 0 || limit > b.recentCount {
		limit = b.recentCount
	}
	out := make([]Envelope, limit)
	start := b.recentStart + b.recentCount - limit
	for i := 0; i < limit; i++ {
		out[i] = b.recent[(start+i)%b.maxRecent]
	}
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
	b.wg.Go(func() {
		backoff := time.Second
		for {
			if b.ctx.Err() != nil {
				return
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
	})
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
			if b.ctx.Err() != nil {
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
			recordEventTrace("event.receive", envelope)
			b.dispatch(b.ctx, envelope)
		}
	}
}

func (b *RedisBus) record(envelope Envelope) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.recordRecentLocked(envelope)
}

func (b *RedisBus) recordRecentLocked(envelope Envelope) {
	if b.maxRecent <= 0 {
		return
	}
	if b.recentCount < b.maxRecent {
		index := (b.recentStart + b.recentCount) % b.maxRecent
		b.recent[index] = envelope
		b.recentCount++
		return
	}
	b.recent[b.recentStart] = envelope
	b.recentStart = (b.recentStart + 1) % b.maxRecent
}

func (b *RedisBus) dispatch(ctx context.Context, envelope Envelope) {
	b.mu.RLock()
	exact := b.exactSubscribers[envelope.EventType]
	if len(b.allSubscribers) == 0 && len(b.prefixSubscribers) == 0 && len(b.patternSubscribers) == 0 {
		b.mu.RUnlock()
		for _, subscriber := range exact {
			subscriber(ctx, envelope)
		}
		return
	}
	matching := make([]Subscriber, 0, len(exact)+len(b.allSubscribers)+8)
	matching = append(matching, exact...)
	matching = append(matching, b.allSubscribers...)
	matching = appendPrefixSubscribers(matching, b.prefixSubscribers, envelope.EventType)
	for pattern, subs := range b.patternSubscribers {
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
	maps.Copy(out, in)
	return out
}
