// Package projectiongw is the generic transport bridge for the Hermes read
// path. Writes are events; reads are projections. hermes.Store already
// materializes the node-local read model and consumes deltas; frontend-kit
// already defines HermesProjectionSource + adapter + connectLiveProjection. The
// missing piece was the transport between them: a scoped snapshot endpoint and a
// live delta stream. projectiongw is that bridge.
//
// It is domain-agnostic. Every subscription keys entirely on a ProjectionScope
// (tenant, domain, collection) plus a watermark/epoch resume cursor. The wire
// shape is the canonical foundation.v1.RecordMutationBatch carried in an
// events.Envelope (protobuf payload on the hot path; JSON only as the
// compatibility lane), exactly the proto contract hermes.Store consumes on the
// write side. Deltas are observed at the apply path: the projector applies
// through the Gateway, which fans the same mutations out to subscribers.
package projectiongw

import (
	"sync"
	"sync/atomic"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
)

// DefaultSubscriberQueue is the bounded per-subscriber frame buffer. A consumer
// slower than this many pending frames is dropped (slow-client drop) rather than
// allowed to grow unbounded, honoring the BoundedWork invariant.
const DefaultSubscriberQueue = 256

// Frame is a fan-out-ready encoded delta plus the resume cursor it advances to.
// Encoding happens once per broadcast; every subscriber writes the same bytes.
type Frame struct {
	// Envelope is the binary events.Envelope (protobuf RecordMutationBatch
	// payload) ready to write to a socket.
	Envelope []byte
	// Watermark is the resume token a client should present to continue after
	// this frame.
	Watermark string
	// Epoch is the hermes projection epoch the frame was produced at.
	Epoch uint64
}

// Subscription is a live per-scope delta feed. Frames is closed when the
// subscription is cancelled. Dropped counts frames shed because the consumer
// could not keep up; a non-zero value means the client must reconcile via a
// fresh snapshot.
type Subscription struct {
	Frames  <-chan Frame
	cancel  func()
	dropped *atomic.Uint64
	// drops is signalled (coalesced, non-blocking) whenever the hub sheds a
	// frame for this subscriber, so the writer can emit a resync immediately
	// instead of waiting for the next delivered frame to carry the notice.
	drops <-chan struct{}
}

// Cancel releases the subscription and closes Frames. It is safe to call more
// than once.
func (s *Subscription) Cancel() {
	if s.cancel != nil {
		s.cancel()
	}
}

// Dropped reports how many frames were shed for this subscriber due to slow
// consumption.
func (s *Subscription) Dropped() uint64 {
	if s.dropped == nil {
		return 0
	}
	return s.dropped.Load()
}

// Drops returns a channel signalled (coalesced) when frames are shed for this
// subscriber. A nil-safe zero-value Subscription returns nil, which blocks
// forever in a select — the intended no-op.
func (s *Subscription) Drops() <-chan struct{} { return s.drops }

type subscriber struct {
	frames  chan Frame
	dropped *atomic.Uint64
	drops   chan struct{}
}

// Hub fans encoded delta frames out to subscribers keyed by exact scope. It uses
// an exact-topic index (the colon-prefix scope key), the hot fanout shape the
// foundation prefers over wildcard scans.
type Hub struct {
	mu        sync.RWMutex
	queueSize int
	subs      map[string]map[*subscriber]struct{}
}

// NewHub constructs an empty Hub. queueSize <= 0 uses DefaultSubscriberQueue.
func NewHub(queueSize int) *Hub {
	if queueSize <= 0 {
		queueSize = DefaultSubscriberQueue
	}
	return &Hub{queueSize: queueSize, subs: make(map[string]map[*subscriber]struct{})}
}

// Subscribe registers a feed for an exact scope. The returned Subscription must
// be cancelled to release resources.
func (h *Hub) Subscribe(scope *foundationpb.ProjectionScope) *Subscription {
	key := ScopeKey(scope)
	sub := &subscriber{
		frames:  make(chan Frame, h.queueSize),
		dropped: &atomic.Uint64{},
		drops:   make(chan struct{}, 1),
	}

	h.mu.Lock()
	bucket := h.subs[key]
	if bucket == nil {
		bucket = make(map[*subscriber]struct{})
		h.subs[key] = bucket
	}
	bucket[sub] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.mu.Lock()
			if bucket, ok := h.subs[key]; ok {
				delete(bucket, sub)
				if len(bucket) == 0 {
					delete(h.subs, key)
				}
			}
			h.mu.Unlock()
			close(sub.frames)
		})
	}

	return &Subscription{Frames: sub.frames, cancel: cancel, dropped: sub.dropped, drops: sub.drops}
}

// Broadcast delivers one already-encoded frame to every subscriber of key. A
// subscriber whose buffer is full has the frame dropped and its drop counter
// incremented; the broadcast never blocks on a slow consumer.
func (h *Hub) Broadcast(key string, frame Frame) {
	// The fan-out holds the read lock for its whole duration. cancel() closes
	// sub.frames under the write lock, so doing the (non-blocking) sends under
	// RLock makes send and close mutually exclusive — without it, a subscriber
	// cancelling concurrently with a broadcast could close sub.frames between the
	// target snapshot and the send, panicking with "send on closed channel". Sends
	// stay non-blocking (select/default), so holding RLock never stalls on a slow
	// consumer and concurrent broadcasts still proceed in parallel.
	h.mu.RLock()
	defer h.mu.RUnlock()
	for sub := range h.subs[key] {
		select {
		case sub.frames <- frame:
		default:
			sub.dropped.Add(1)
			// Wake the writer so it can emit a resync even if no further frame
			// is ever delivered. Coalesced: a pending signal already covers it.
			select {
			case sub.drops <- struct{}{}:
			default:
			}
		}
	}
}

// SubscriberCount reports the number of live subscribers for an exact scope key.
func (h *Hub) SubscriberCount(key string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs[key])
}
