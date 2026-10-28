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

// ExecuteWithScratch executes a request on the process pool using a control buffer
// rented directly from the caller's scratch arena. This eliminates sync.Pool lock contention
// and heap churn during high-frequency dispatch frames.
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

	buffer, err := NewBufferFromScratch(arena)
	if err != nil {
		return ProcessResponse{}, err
	}
	raw := buffer.RawBytes()
	buffer.Reset()
	buffer.Initialize(req.ModuleVersion)
	if err := buffer.SetHeaderInt(generated.INT_IDX_CONTEXT_HASH, req.ContextHash); err != nil {
		return ProcessResponse{}, err
	}
	if err := buffer.SetInputBytesFast(req.Input); err != nil {
		return ProcessResponse{}, err
	}
	if _, err := buffer.AddEpoch(generated.IDX_INPUT_WRITTEN, 1); err != nil {
		return ProcessResponse{}, err
	}

	if err := p.executeOnSelectedWorker(ctx, req, raw, pinned...); err != nil {
		return ProcessResponse{}, err
	}

	statusCode, err := buffer.HeaderInt(generated.INT_IDX_STATUS_CODE)
	if err != nil {
		return ProcessResponse{}, err
	}
	output, err := buffer.OutputBytesView()
	if err != nil {
		return ProcessResponse{}, err
	}

	ownedOutput := dst
	if ownedOutput == nil {
		ownedOutput = make([]byte, len(output))
	} else if len(ownedOutput) < len(output) {
		return ProcessResponse{}, fmt.Errorf("process output destination too small: %d < %d", len(ownedOutput), len(output))
	}
	ownedOutput = ownedOutput[:len(output)]
	copy(ownedOutput, output)

	response := ProcessResponse{
		Output:      ownedOutput,
		Diagnostics: strings.TrimSpace(buffer.DiagnosticsText()),
		StatusCode:  statusCode,
		OutputEpoch: buffer.LoadEpoch(generated.IDX_OUTPUT_WRITTEN),
	}
	_, _ = buffer.AddEpoch(generated.IDX_OUTPUT_CONSUMED, 1)

	if statusCode != 0 {
		if response.Diagnostics == "" {
			response.Diagnostics = "native runtime returned non-zero status"
		}
		return response, errors.New(response.Diagnostics)
	}
	return response, nil
}
