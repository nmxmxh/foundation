package logger

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

type asyncHandler struct {
	next  slog.Handler
	state *asyncState
}

type asyncState struct {
	queue   chan logEntry
	dropped atomic.Uint64
}

type logEntry struct {
	ctx    context.Context
	record slog.Record
	next   slog.Handler
	flush  chan struct{}
}

func newAsyncHandler(next slog.Handler, depth int) (slog.Handler, *asyncState) {
	state := &asyncState{queue: make(chan logEntry, depth)}
	go state.run()
	return &asyncHandler{next: next, state: state}, state
}

func (h *asyncHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *asyncHandler) Handle(ctx context.Context, r slog.Record) error {
	entry := logEntry{ctx: ctx, record: r.Clone(), next: h.next}
	select {
	case h.state.queue <- entry:
	default:
		h.state.dropped.Add(1)
	}
	return nil
}

func (h *asyncHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &asyncHandler{next: h.next.WithAttrs(attrs), state: h.state}
}

func (h *asyncHandler) WithGroup(name string) slog.Handler {
	return &asyncHandler{next: h.next.WithGroup(name), state: h.state}
}

func (s *asyncState) run() {
	for entry := range s.queue {
		if entry.flush != nil {
			close(entry.flush)
			continue
		}
		_ = entry.next.Handle(entry.ctx, entry.record)
	}
}

func (s *asyncState) flush(timeout time.Duration) bool {
	if s == nil {
		return true
	}
	done := make(chan struct{})
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case s.queue <- logEntry{flush: done}:
	case <-timer.C:
		return false
	}
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}
