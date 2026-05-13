package contracttest

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/worker"
)

// LifecycleRecorder captures real envelopes and jobs during service tests.
// It can wrap an events.Bus so implementation tests verify observed runtime
// behavior instead of only checking generated contract vectors.
type LifecycleRecorder struct {
	mu     sync.Mutex
	events []events.Envelope
	jobs   []worker.Job
}

func NewLifecycleRecorder() *LifecycleRecorder {
	return &LifecycleRecorder{}
}

func (r *LifecycleRecorder) RecordEnvelope(envelope events.Envelope) {
	if r == nil {
		return
	}
	envelope.Normalize()
	r.mu.Lock()
	r.events = append(r.events, envelope)
	r.mu.Unlock()
}

func (r *LifecycleRecorder) RecordJob(job worker.Job) {
	if r == nil {
		return
	}
	job.Normalize()
	r.mu.Lock()
	r.jobs = append(r.jobs, job)
	r.mu.Unlock()
}

func (r *LifecycleRecorder) Events() []events.Envelope {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]events.Envelope, len(r.events))
	copy(out, r.events)
	return out
}

func (r *LifecycleRecorder) Jobs() []worker.Job {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]worker.Job, len(r.jobs))
	copy(out, r.jobs)
	return out
}

func (r *LifecycleRecorder) Observation(requestedEventType, terminalEventType string) (LifecycleObservation, error) {
	if r == nil {
		return LifecycleObservation{}, fmt.Errorf("lifecycle recorder is nil")
	}
	requestedEventType = strings.TrimSpace(requestedEventType)
	terminalEventType = strings.TrimSpace(terminalEventType)
	if requestedEventType == "" || terminalEventType == "" {
		return LifecycleObservation{}, fmt.Errorf("requested and terminal event types are required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	obs := LifecycleObservation{Jobs: make([]worker.Job, len(r.jobs))}
	copy(obs.Jobs, r.jobs)
	for _, envelope := range r.events {
		switch envelope.EventType {
		case requestedEventType:
			obs.Requested = envelope
		case terminalEventType:
			obs.Terminal = envelope
		default:
			continue
		}
	}
	if strings.TrimSpace(obs.Requested.EventType) == "" {
		return LifecycleObservation{}, fmt.Errorf("requested event %q was not observed", requestedEventType)
	}
	if strings.TrimSpace(obs.Terminal.EventType) == "" {
		return LifecycleObservation{}, fmt.Errorf("terminal event %q was not observed", terminalEventType)
	}
	return obs, nil
}

func (r *LifecycleRecorder) Verify(requestedEventType, terminalEventType string, opts LifecycleOptions) error {
	obs, err := r.Observation(requestedEventType, terminalEventType)
	if err != nil {
		return err
	}
	return VerifyCommandLifecycle(obs, opts)
}

func (r *LifecycleRecorder) WrapBus(bus events.Bus) events.Bus {
	if bus == nil {
		bus = events.NewInMemoryBus(200)
	}
	return lifecycleRecordingBus{bus: bus, recorder: r}
}

type lifecycleRecordingBus struct {
	bus      events.Bus
	recorder *LifecycleRecorder
}

func (b lifecycleRecordingBus) Publish(ctx context.Context, envelope events.Envelope) error {
	if err := b.bus.Publish(ctx, envelope); err != nil {
		return err
	}
	b.recorder.RecordEnvelope(envelope)
	return nil
}

func (b lifecycleRecordingBus) Subscribe(pattern string, subscriber events.Subscriber) {
	b.bus.Subscribe(pattern, subscriber)
}

func (b lifecycleRecordingBus) Recent(limit int) []events.Envelope {
	return b.bus.Recent(limit)
}
