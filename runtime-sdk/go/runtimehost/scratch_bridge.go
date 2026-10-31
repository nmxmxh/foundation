package runtimehost

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scratch"
)

var (
	// ErrNilScratchArena indicates that a nil scratch arena was provided.
	ErrNilScratchArena = errors.New("scratch arena cannot be nil")
)

// NewBufferFromScratch allocates a 4096-byte control plane buffer slice from the
// provided frame-scoped scratch arena without invoking the Go runtime heap allocator.
// WARNING: do not give this buffer to a cancellable exchange. See ExecuteWithScratch.
func NewBufferFromScratch(arena *scratch.Arena) (*Buffer, error) {
	if arena == nil {
		return nil, ErrNilScratchArena
	}
	raw, err := arena.Alloc(int(generated.BUFFER_TOTAL_BYTES))
	if err != nil {
		return nil, fmt.Errorf("failed to allocate control buffer from scratch arena: %w", err)
	}
	return &Buffer{raw: raw}, nil
}

// NewArenaFromScratch allocates a data-plane shared arena slice of the requested size
// from the frame-scoped scratch arena. The requested size must be >= ARENA_MIN_BYTES.
func NewArenaFromScratch(arena *scratch.Arena, size int) (*Arena, error) {
	if arena == nil {
		return nil, ErrNilScratchArena
	}
	if size < int(generated.ARENA_MIN_BYTES) {
		return nil, fmt.Errorf("requested arena size %d is below minimum %d", size, generated.ARENA_MIN_BYTES)
	}
	raw, err := arena.Alloc(size)
	if err != nil {
		return nil, fmt.Errorf("failed to allocate shared arena from scratch arena: %w", err)
	}
	return NewArenaOver(raw)
}

// ExecuteWithScratch executes a request on the process pool and copies output into dst.
//
// WARNING: the control buffer does not come from the arena. A cancelled request
// returns ErrExchangeAbandoned while the native exchange can still write the buffer.
// The caller then resets the arena and the next frame reuses that memory, so
// arena memory is never handed to an exchange. The pooled path already drops
// abandoned buffers. The arena parameter stays for API stability.
func (p *ProcessPool) ExecuteWithScratch(ctx context.Context, req ProcessRequest, arena *scratch.Arena, dst []byte, pinned ...*processWorker) (ProcessResponse, error) {
	if p == nil {
		return ProcessResponse{}, errors.New("process pool is nil")
	}
	if arena == nil {
		return ProcessResponse{}, ErrNilScratchArena
	}
	if strings.TrimSpace(req.UnitID) == "" {
		return ProcessResponse{}, errors.New("unit id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return p.execute(ctx, req, dst, pinned...)
}
