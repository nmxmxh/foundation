package events

import (
	"context"
	"strings"
	"sync"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/observability"
)

// Subscriber receives published envelopes.
type Subscriber func(context.Context, Envelope)

// Bus is the minimal in-process event bus contract.
type Bus interface {
	Publish(context.Context, Envelope) error
	Subscribe(pattern string, subscriber Subscriber)
	Recent(limit int) []Envelope
}

// InMemoryBus is a development-safe event bus implementation with fanout support.
type InMemoryBus struct {
	mu                 sync.RWMutex
	exactSubscribers   map[string][]Subscriber
	allSubscribers     []Subscriber
	prefixSubscribers  map[string][]Subscriber
	patternSubscribers map[string][]Subscriber
	recent             []Envelope
	recentStart        int
	recentCount        int
	maxRecent          int
}

func NewInMemoryBus(maxRecent int) *InMemoryBus {
	if maxRecent <= 0 {
		maxRecent = 200
	}
	return &InMemoryBus{
		exactSubscribers:   map[string][]Subscriber{},
		prefixSubscribers:  map[string][]Subscriber{},
		patternSubscribers: map[string][]Subscriber{},
		recent:             make([]Envelope, maxRecent),
		maxRecent:          maxRecent,
	}
}

func (b *InMemoryBus) Publish(ctx context.Context, envelope Envelope) error {
	envelope = envelopeWithContextMetadata(ctx, envelope)
	if !envelopeDispatchReady(envelope) {
		envelope.Normalize()
	}
	if err := envelope.Validate(); err != nil {
		return err
	}

	b.mu.Lock()
	b.recordRecentLocked(envelope)

	exact := b.exactSubscribers[envelope.EventType]
	if len(b.allSubscribers) == 0 && len(b.prefixSubscribers) == 0 && len(b.patternSubscribers) == 0 {
		b.mu.Unlock()
		recordEventTrace("event.publish", envelope)
		for _, subscriber := range exact {
			subscriber(ctx, envelope)
		}
		return nil
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
	b.mu.Unlock()
	recordEventTrace("event.publish", envelope)

	for _, subscriber := range matching {
		subscriber(ctx, envelope)
	}
	return nil
}

func envelopeWithContextMetadata(ctx context.Context, envelope Envelope) Envelope {
	md := metadata.FromContext(ctx)
	md.EnsureCorrelation(envelope.CorrelationID)
	envelope.Metadata = metadata.MergeMaps(md.ToMap(), envelope.Metadata)
	return envelope
}

func recordEventTrace(stage string, envelope Envelope) {
	if envelope.CorrelationID == "" {
		return
	}
	observability.Default().RecordTrace(
		envelope.CorrelationID,
		stage,
		envelope.EventType,
		TerminalState(envelope.EventType),
		"",
		nil,
	)
}

func (b *InMemoryBus) Subscribe(pattern string, subscriber Subscriber) {
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

func (b *InMemoryBus) Recent(limit int) []Envelope {
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

func (b *InMemoryBus) recordRecentLocked(envelope Envelope) {
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

func exactEventPattern(pattern string) bool {
	return pattern != "" && !strings.Contains(pattern, "*")
}

func prefixWildcardPattern(pattern string) (string, bool) {
	if pattern == "" || pattern == "*" {
		return "", true
	}
	if strings.Count(pattern, "*") != 1 || !strings.HasSuffix(pattern, "*") {
		return "", false
	}
	prefix := strings.TrimSuffix(pattern, "*")
	if prefix == "" {
		return "", true
	}
	if !strings.HasSuffix(prefix, ":") {
		return "", false
	}
	return prefix, true
}

func appendPrefixSubscribers(out []Subscriber, subscribers map[string][]Subscriber, eventType string) []Subscriber {
	if len(subscribers) == 0 {
		return out
	}
	for i := 0; i < len(eventType); i++ {
		if eventType[i] != ':' {
			continue
		}
		out = append(out, subscribers[eventType[:i+1]]...)
	}
	return out
}

func appendSubscriber(subscribers []Subscriber, subscriber Subscriber) []Subscriber {
	next := make([]Subscriber, len(subscribers)+1)
	copy(next, subscribers)
	next[len(subscribers)] = subscriber
	return next
}

func envelopeDispatchReady(envelope Envelope) bool {
	if envelope.SchemaVersion == "" || envelope.Timestamp.IsZero() || envelope.PayloadEncoding == "" || envelope.CorrelationID == "" {
		return false
	}
	if envelope.PayloadEncoding == PayloadEncodingJSON && envelope.Payload == nil {
		return false
	}
	if envelope.Metadata == nil {
		return false
	}
	if correlationID, _ := envelope.Metadata["correlation_id"].(string); correlationID == envelope.CorrelationID {
		return true
	}
	if correlationID, _ := envelope.Metadata["correlationId"].(string); correlationID == envelope.CorrelationID {
		return true
	}
	return false
}

func Matches(pattern, eventType string) bool {
	if pattern == "*" || pattern == "" {
		return true
	}
	if pattern == eventType {
		return true
	}

	pIndex := 0
	eIndex := 0
	for pIndex < len(pattern) && eIndex < len(eventType) {
		if pattern[pIndex] == '*' {
			if pIndex == len(pattern)-1 {
				return true
			}
			next := pattern[pIndex+1]
			for eIndex < len(eventType) && eventType[eIndex] != next {
				eIndex++
			}
			pIndex++
			continue
		}
		if pattern[pIndex] != eventType[eIndex] {
			return false
		}
		pIndex++
		eIndex++
	}
	return pIndex == len(pattern) && eIndex == len(eventType)
}
