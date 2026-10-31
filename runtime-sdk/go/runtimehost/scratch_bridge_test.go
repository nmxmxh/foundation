package runtimehost

import (
	"context"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scratch"
)

func TestNewBufferFromScratch(t *testing.T) {
	// Nil arena check
	_, err := NewBufferFromScratch(nil)
	if err == nil {
		t.Fatal("expected error for nil arena")
	}

	// Insufficient capacity
	smallArena := scratch.New(1024)
	_, err = NewBufferFromScratch(smallArena)
	if err == nil {
		t.Fatal("expected error when scratch arena capacity is too small for 4KB buffer")
	}

	// Successful allocation
	arena := scratch.New(64 * 1024)
	buf, err := NewBufferFromScratch(arena)
	if err != nil {
		t.Fatalf("unexpected error allocating buffer from scratch: %v", err)
	}
	if len(buf.RawBytes()) != int(generated.BUFFER_TOTAL_BYTES) {
		t.Fatalf("buffer size %d != %d", len(buf.RawBytes()), generated.BUFFER_TOTAL_BYTES)
	}

	// Verify buffer operations work on scratch memory
	if err := buf.SetHeaderInt(generated.INT_IDX_STATUS_CODE, 200); err != nil {
		t.Fatalf("failed to set header int: %v", err)
	}
	val, err := buf.HeaderInt(generated.INT_IDX_STATUS_CODE)
	if err != nil || val != 200 {
		t.Fatalf("expected status code 200, got %d (err: %v)", val, err)
	}

	// Verify reset
	arena.Reset()
	if arena.Allocated() != 0 {
		t.Fatalf("expected 0 bytes allocated after reset, got %d", arena.Allocated())
	}
}

func TestNewArenaFromScratch(t *testing.T) {
	// Nil arena check
	_, err := NewArenaFromScratch(nil, 64*1024)
	if err == nil {
		t.Fatal("expected error for nil arena")
	}

	// Below minimum size
	arena := scratch.New(2 * 1024 * 1024)
	_, err = NewArenaFromScratch(arena, int(generated.ARENA_MIN_BYTES)-1)
	if err == nil {
		t.Fatal("expected error for arena size below minimum")
	}

	// Insufficient capacity
	smallArena := scratch.New(1024)
	_, err = NewArenaFromScratch(smallArena, int(generated.ARENA_MIN_BYTES))
	if err == nil {
		t.Fatal("expected error for insufficient scratch capacity")
	}

	// Successful allocation
	shmArena, err := NewArenaFromScratch(arena, int(generated.ARENA_MIN_BYTES))
	if err != nil {
		t.Fatalf("unexpected error allocating shared arena from scratch: %v", err)
	}
	if shmArena.Stats().CapacityBytes != generated.ARENA_MIN_BYTES {
		t.Fatalf("expected capacity %d, got %d", generated.ARENA_MIN_BYTES, shmArena.Stats().CapacityBytes)
	}
}

func TestExecuteWithScratch(t *testing.T) {
	ctx := context.Background()

	// Nil pool
	if _, err := (*ProcessPool)(nil).ExecuteWithScratch(ctx, ProcessRequest{}, nil, nil); err == nil {
		t.Fatal("expected error for nil pool")
	}

	pool := &ProcessPool{}
	// Nil arena
	if _, err := pool.ExecuteWithScratch(ctx, ProcessRequest{}, nil, nil); err == nil {
		t.Fatal("expected error for nil arena")
	}

	arena := scratch.New(64 * 1024)
	// Missing unit id
	if _, err := pool.ExecuteWithScratch(ctx, ProcessRequest{}, arena, nil); err == nil {
		t.Fatal("expected error for missing unit id")
	}

	// Echo worker integration
	worker := workerWithEchoTransport(t, ProcessTransportStdio)
	pool = &ProcessPool{
		exchangeTimeout: DefaultProcessExchangeTimeout,
		allWorkers:      []*processWorker{worker},
	}
	pool.bufferPool.New = newPooledBuffer
	req := ProcessRequest{
		UnitID: "runtime.echo",
		Input:  []byte("hello scratch"),
	}
	resp, err := pool.ExecuteWithScratch(ctx, req, arena, nil)
	if err != nil {
		t.Fatalf("ExecuteWithScratch failed: %v", err)
	}
	if string(resp.Output) != "HELLO SCRATCH" {
		t.Fatalf("expected 'HELLO SCRATCH', got %q", string(resp.Output))
	}
	// An abandoned exchange can keep writing its control buffer, and the caller
	// resets the arena after return. So the exchange must never own arena memory.
	if arena.Allocated() != 0 {
		t.Fatalf("ExecuteWithScratch took %d arena bytes for a cancellable exchange", arena.Allocated())
	}
}

func BenchmarkBufferAllocation_Scratch(b *testing.B) {
	arena := scratch.New(1024 * 1024)

	b.ReportAllocs()
	for b.Loop() {
		arena.Reset()
		buf, err := NewBufferFromScratch(arena)
		if err != nil {
			b.Fatal(err)
		}
		_ = buf
	}
}

func BenchmarkBufferAllocation_Heap(b *testing.B) {

	b.ReportAllocs()
	for b.Loop() {
		raw := make([]byte, generated.BUFFER_TOTAL_BYTES)
		buf, err := NewBuffer(raw)
		if err != nil {
			b.Fatal(err)
		}
		_ = buf
	}
}

func BenchmarkProcessPoolExecuteWithScratch(b *testing.B) {
	worker := &processWorker{
		testExchange: benchmarkProcessExchange{},
	}
	pool := &ProcessPool{
		exchangeTimeout: DefaultProcessExchangeTimeout,
		allWorkers:      []*processWorker{worker},
	}
	pool.bufferPool.New = newPooledBuffer
	arena := scratch.New(1024 * 1024)
	req := ProcessRequest{
		UnitID: "runtime.echo",
		Input:  make([]byte, 1024),
	}
	dst := make([]byte, 1024)

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		arena.Reset()
		_, err := pool.ExecuteWithScratch(context.Background(), req, arena, dst)
		if err != nil {
			b.Fatal(err)
		}
	}
}
