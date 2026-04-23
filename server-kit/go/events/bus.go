package events

import (
	"context"
	"sync"
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
	mu          sync.RWMutex
	subscribers map[string][]Subscriber
	recent      []Envelope
	maxRecent   int
}

func NewInMemoryBus(maxRecent int) *InMemoryBus {
	if maxRecent <= 0 {
		maxRecent = 200
	}
	return &InMemoryBus{
		subscribers: map[string][]Subscriber{},
		recent:      make([]Envelope, 0, maxRecent),
		maxRecent:   maxRecent,
	}
}

func (b *InMemoryBus) Publish(ctx context.Context, envelope Envelope) error {
	envelope.Normalize()
	if err := envelope.Validate(); err != nil {
		return err
	}

	b.mu.Lock()
	b.recent = append(b.recent, envelope)
	if len(b.recent) > b.maxRecent {
		b.recent = b.recent[len(b.recent)-b.maxRecent:]
	}

	matching := make([]Subscriber, 0, 8)
	for pattern, subs := range b.subscribers {
		if Matches(pattern, envelope.EventType) {
			matching = append(matching, subs...)
		}
	}
	b.mu.Unlock()

	for _, subscriber := range matching {
		subscriber(ctx, envelope)
	}
	return nil
}

func (b *InMemoryBus) Subscribe(pattern string, subscriber Subscriber) {
	if subscriber == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subscribers[pattern] = append(b.subscribers[pattern], subscriber)
}

func (b *InMemoryBus) Recent(limit int) []Envelope {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if limit <= 0 || limit > len(b.recent) {
		limit = len(b.recent)
	}
	out := make([]Envelope, limit)
	copy(out, b.recent[len(b.recent)-limit:])
	return out
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
