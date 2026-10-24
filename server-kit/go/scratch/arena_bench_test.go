package scratch

import (
	"sync"
	"testing"
)

type BenchItem struct {
	ID        uint64
	Timestamp int64
	Score     float64
	Flags     uint32
}

var sinkByteSlice []byte
var sinkItemSlice []BenchItem

func BenchmarkHeapAlloc_64B(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		sinkByteSlice = make([]byte, 64)
	}
}

func BenchmarkSyncPool_64B(b *testing.B) {
	pool := &sync.Pool{
		New: func() any {
			buf := make([]byte, 64)
			return &buf
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		bufPtr := pool.Get().(*[]byte)
		sinkByteSlice = *bufPtr
		pool.Put(bufPtr)
	}
}

func BenchmarkScratchArena_64B(b *testing.B) {
	arena := New(64 * 1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		if arena.Available() < 64 {
			arena.Reset()
		}
		buf, _ := arena.Alloc(64)
		sinkByteSlice = buf
	}
}

func BenchmarkHeapAlloc_4KB(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		sinkByteSlice = make([]byte, 4096)
	}
}

func BenchmarkSyncPool_4KB(b *testing.B) {
	pool := &sync.Pool{
		New: func() any {
			buf := make([]byte, 4096)
			return &buf
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		bufPtr := pool.Get().(*[]byte)
		sinkByteSlice = *bufPtr
		pool.Put(bufPtr)
	}
}

func BenchmarkScratchArena_4KB(b *testing.B) {
	arena := New(256 * 1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		if arena.Available() < 4096 {
			arena.Reset()
		}
		buf, _ := arena.Alloc(4096)
		sinkByteSlice = buf
	}
}

func BenchmarkHeapAlloc_SliceStruct(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		sinkItemSlice = make([]BenchItem, 32)
	}
}

func BenchmarkScratchArena_SliceStruct(b *testing.B) {
	arena := New(256 * 1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		if arena.Available() < int(32*32) { // 32 items * 32 bytes
			arena.Reset()
		}
		items, _ := AllocSlice[BenchItem](arena, 32)
		sinkItemSlice = items
	}
}

func BenchmarkScopeUnwind(b *testing.B) {
	arena := New(64 * 1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		// Frame scope: sub-routine allocates scratch memory then unwinds
		scope := arena.Scope()
		buf, _ := arena.Alloc(128)
		sinkByteSlice = buf
		scope.End()
	}
}

// calleeDynamicHeap allocates dynamic return data on the heap.
func calleeDynamicHeap(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(i)
	}
	return out
}

// calleeDynamicArena extends directly into the caller's ScratchArena.
func calleeDynamicArena(a *Arena, n int) ([]byte, error) {
	out, err := a.Alloc(n)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i] = byte(i)
	}
	return out, nil
}

func BenchmarkCallerAllocates_HeapReturn(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		sinkByteSlice = calleeDynamicHeap(256)
	}
}

func BenchmarkCallerAllocates_ArenaReturn(b *testing.B) {
	arena := New(64 * 1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		if arena.Available() < 256 {
			arena.Reset()
		}
		buf, _ := calleeDynamicArena(arena, 256)
		sinkByteSlice = buf
	}
}
