package worker

import (
	"maps"
	"sync"
	"sync/atomic"
)

// ProcessorMaplet provides lock-free, zero-allocation routing for worker processors
// and queues. It uses an immutable copy-on-write snapshot loaded atomically.
type ProcessorMaplet struct {
	processors map[string]Processor
	queues     map[string]chan Job
}

// NewProcessorMaplet constructs an immutable routing maplet.
func NewProcessorMaplet(processors map[string]Processor, queues map[string]chan Job) *ProcessorMaplet {
	pCopy := make(map[string]Processor, len(processors))
	maps.Copy(pCopy, processors)
	qCopy := make(map[string]chan Job, len(queues))
	maps.Copy(qCopy, queues)
	return &ProcessorMaplet{
		processors: pCopy,
		queues:     qCopy,
	}
}

// Lookup returns the registered Processor for the given kind in lock-free O(1) time.
func (m *ProcessorMaplet) Lookup(kind string) (Processor, bool) {
	if m == nil || m.processors == nil {
		return nil, false
	}
	p, ok := m.processors[kind]
	return p, ok
}

// Queue returns the channel for the given queue name in lock-free O(1) time.
func (m *ProcessorMaplet) Queue(name string) (chan Job, bool) {
	if m == nil || m.queues == nil {
		return nil, false
	}
	ch, ok := m.queues[name]
	return ch, ok
}

// MapletHolder manages atomic updates to a ProcessorMaplet.
type MapletHolder struct {
	current atomic.Pointer[ProcessorMaplet]
	mu      sync.Mutex
}

// NewMapletHolder initializes a holder with an empty maplet.
func NewMapletHolder() *MapletHolder {
	h := &MapletHolder{}
	h.current.Store(NewProcessorMaplet(nil, nil))
	return h
}

// Load atomically retrieves the current immutable ProcessorMaplet.
func (h *MapletHolder) Load() *ProcessorMaplet {
	return h.current.Load()
}

// Update rebuilds the maplet from the provided processors and queues under lock.
func (h *MapletHolder) Update(processors map[string]Processor, queues map[string]chan Job) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.current.Store(NewProcessorMaplet(processors, queues))
}
