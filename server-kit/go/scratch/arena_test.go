package scratch

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"unsafe"
)

type sampleStruct struct {
	ID    uint64
	Value float64
	Flag  bool
}

func TestNew_DefaultCapacity(t *testing.T) {
	a := New(0)
	if a.Capacity() != DefaultCapacity {
		t.Fatalf("expected capacity %d, got %d", DefaultCapacity, a.Capacity())
	}
	if a.Allocated() != 0 {
		t.Fatalf("expected 0 allocated bytes, got %d", a.Allocated())
	}
	if a.Available() != DefaultCapacity {
		t.Fatalf("expected available %d, got %d", DefaultCapacity, a.Available())
	}
}

func TestNew_CustomCapacity(t *testing.T) {
	const customCap = 1024
	a := New(customCap)
	if a.Capacity() != customCap {
		t.Fatalf("expected capacity %d, got %d", customCap, a.Capacity())
	}
}

func TestNewWithBuffer_AlignmentAdjustment(t *testing.T) {
	raw := make([]byte, 128)
	// Artificially misalign the slice by taking subslice from odd offset
	misaligned := raw[3:]
	a := NewWithBuffer(misaligned)

	allocated, err := a.Alloc(16)
	if err != nil {
		t.Fatalf("alloc failed: %v", err)
	}
	addr := uintptr(unsafe.Pointer(&allocated[0]))
	if addr&7 != 0 {
		t.Fatalf("expected 8-byte aligned address, got %x", addr)
	}
}

func TestArena_Alloc_EdgeCases(t *testing.T) {
	a := New(256)

	// Zero-size allocation
	nilBuf, err := a.Alloc(0)
	if err != nil {
		t.Fatalf("expected nil error for 0 size, got %v", err)
	}
	if nilBuf != nil {
		t.Fatalf("expected nil buffer for 0 size, got %v", nilBuf)
	}

	// Negative allocation size
	_, err = a.Alloc(-5)
	if !errors.Is(err, ErrNegativeAlloc) {
		t.Fatalf("expected ErrNegativeAlloc, got %v", err)
	}

	// Exceed capacity
	_, err = a.Alloc(512)
	if !errors.Is(err, ErrArenaFull) {
		t.Fatalf("expected ErrArenaFull, got %v", err)
	}
}

func TestArena_AllocBytes(t *testing.T) {
	a := New(256)

	// Valid length and capacity
	buf, err := a.AllocBytes(10, 64)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(buf) != 10 {
		t.Fatalf("expected len 10, got %d", len(buf))
	}
	if cap(buf) < 64 {
		t.Fatalf("expected cap >= 64, got %d", cap(buf))
	}

	// Zero capacity
	nilBuf, err := a.AllocBytes(0, 0)
	if err != nil || nilBuf != nil {
		t.Fatalf("expected nil buffer and nil err for 0 cap, got %v, %v", nilBuf, err)
	}

	// Negative length or capacity < length
	if _, err := a.AllocBytes(-1, 10); !errors.Is(err, ErrNegativeAlloc) {
		t.Fatalf("expected ErrNegativeAlloc for -1 len, got %v", err)
	}
	if _, err := a.AllocBytes(20, 10); !errors.Is(err, ErrNegativeAlloc) {
		t.Fatalf("expected ErrNegativeAlloc for cap < len, got %v", err)
	}

	// Exceed capacity
	if _, err := a.AllocBytes(0, 512); !errors.Is(err, ErrArenaFull) {
		t.Fatalf("expected ErrArenaFull for cap exceeding available, got %v", err)
	}
}

func TestArena_Alloc_AlignmentAndPersistence(t *testing.T) {
	a := New(512)

	b1, err := a.Alloc(7) // should pad to 8
	if err != nil {
		t.Fatalf("alloc b1: %v", err)
	}
	if len(b1) != 7 {
		t.Fatalf("expected len 7, got %d", len(b1))
	}
	copy(b1, []byte("1234567"))

	b2, err := a.Alloc(13) // should pad to 16
	if err != nil {
		t.Fatalf("alloc b2: %v", err)
	}
	copy(b2, []byte("abcdefghijklm"))

	// Ensure 8-byte alignment
	addr1 := uintptr(unsafe.Pointer(&b1[0]))
	addr2 := uintptr(unsafe.Pointer(&b2[0]))
	if addr1&7 != 0 || addr2&7 != 0 {
		t.Fatalf("unaligned addresses: %x, %x", addr1, addr2)
	}

	// Check persistence (no corruption across allocations)
	if string(b1) != "1234567" {
		t.Fatalf("data corrupted: %s", string(b1))
	}
	if string(b2) != "abcdefghijklm" {
		t.Fatalf("data corrupted: %s", string(b2))
	}

	if a.Allocated() != 24 { // 8 + 16
		t.Fatalf("expected 24 bytes allocated, got %d", a.Allocated())
	}
}

func TestArena_Reset(t *testing.T) {
	a := New(128)
	_, _ = a.Alloc(64)
	if a.Allocated() != 64 {
		t.Fatalf("expected 64 allocated, got %d", a.Allocated())
	}

	a.Reset()
	if a.Allocated() != 0 {
		t.Fatalf("expected 0 allocated after reset, got %d", a.Allocated())
	}
	if a.Available() != 128 {
		t.Fatalf("expected 128 available after reset, got %d", a.Available())
	}

	// Verify we can allocate full capacity again
	b, err := a.Alloc(128)
	if err != nil {
		t.Fatalf("alloc after reset failed: %v", err)
	}
	if len(b) != 128 {
		t.Fatalf("expected len 128, got %d", len(b))
	}
}

func TestArena_Raw(t *testing.T) {
	a := New(64)
	raw := a.Raw()
	if len(raw) != 64 {
		t.Fatalf("expected 64 raw bytes, got %d", len(raw))
	}
}

func TestAllocSlice_Typed(t *testing.T) {
	a := New(1024)

	// Negative count
	_, err := AllocSlice[uint32](a, -1)
	if !errors.Is(err, ErrNegativeAlloc) {
		t.Fatalf("expected ErrNegativeAlloc, got %v", err)
	}

	// Zero count
	empty, err := AllocSlice[uint32](a, 0)
	if err != nil || empty != nil {
		t.Fatalf("expected nil for count 0, got %v, err %v", empty, err)
	}

	// Zero-sized struct count
	zeroSlices, err := AllocSlice[struct{}](a, 5)
	if err != nil || len(zeroSlices) != 5 {
		t.Fatalf("zero-sized struct alloc failed: %v", err)
	}

	// Struct slice
	items, err := AllocSlice[sampleStruct](a, 4)
	if err != nil {
		t.Fatalf("alloc sampleStruct slice: %v", err)
	}
	if len(items) != 4 {
		t.Fatalf("expected len 4, got %d", len(items))
	}

	items[0] = sampleStruct{ID: 101, Value: 3.1415, Flag: true}
	items[3] = sampleStruct{ID: 999, Value: 2.7182, Flag: false}

	if items[0].ID != 101 || items[3].ID != 999 {
		t.Fatalf("data readback mismatch: %+v, %+v", items[0], items[3])
	}

	// Out of memory
	_, err = AllocSlice[sampleStruct](a, 1000)
	if !errors.Is(err, ErrArenaFull) {
		t.Fatalf("expected ErrArenaFull, got %v", err)
	}
}

func TestAlloc_Typed(t *testing.T) {
	a := New(256)

	item, err := Alloc[sampleStruct](a)
	if err != nil {
		t.Fatalf("Alloc failed: %v", err)
	}
	item.ID = 42
	item.Value = 9.81
	item.Flag = true

	if item.ID != 42 || item.Value != 9.81 || !item.Flag {
		t.Fatalf("unexpected value: %+v", item)
	}

	// Out of memory
	small := New(8)
	_, err = Alloc[sampleStruct](small)
	if !errors.Is(err, ErrArenaFull) {
		t.Fatalf("expected ErrArenaFull for struct exceeding arena cap, got %v", err)
	}
}

func TestAllocString(t *testing.T) {
	a := New(128)

	// Empty string
	empty, err := AllocString(a, "")
	if err != nil || empty != "" {
		t.Fatalf("expected empty string, got %q, err %v", empty, err)
	}

	// Standard string
	src := "carmack-fast-path"
	res, err := AllocString(a, src)
	if err != nil {
		t.Fatalf("alloc string failed: %v", err)
	}
	if res != src {
		t.Fatalf("expected %q, got %q", src, res)
	}

	// Out of memory
	large := make([]byte, 200)
	_, err = AllocString(a, string(large))
	if !errors.Is(err, ErrArenaFull) {
		t.Fatalf("expected ErrArenaFull, got %v", err)
	}
}

func TestArena_Scope_Nested(t *testing.T) {
	a := New(512)

	// Outer allocation
	_, err := a.Alloc(32)
	if err != nil {
		t.Fatalf("outer alloc: %v", err)
	}
	outerAllocated := a.Allocated()

	// Begin Scope 1
	scope1 := a.Scope()
	_, err = a.Alloc(64)
	if err != nil {
		t.Fatalf("scope1 alloc: %v", err)
	}
	if scope1.BytesAllocated() != 64 {
		t.Fatalf("expected scope1 64 bytes, got %d", scope1.BytesAllocated())
	}

	// Begin nested Scope 2
	scope2 := a.Scope()
	_, err = a.Alloc(48)
	if err != nil {
		t.Fatalf("scope2 alloc: %v", err)
	}
	if scope2.BytesAllocated() != 48 {
		t.Fatalf("expected scope2 48 bytes, got %d", scope2.BytesAllocated())
	}

	// End Scope 2
	scope2.End()
	if a.Allocated() != outerAllocated+64 {
		t.Fatalf("expected allocated %d after scope2.End(), got %d", outerAllocated+64, a.Allocated())
	}

	// End Scope 1
	scope1.End()
	if a.Allocated() != outerAllocated {
		t.Fatalf("expected allocated %d after scope1.End(), got %d", outerAllocated, a.Allocated())
	}
}

func TestArena_Scope_Invalid(t *testing.T) {
	var zeroScope Scope
	// End on zero scope should not panic
	zeroScope.End()
	if zeroScope.BytesAllocated() != 0 {
		t.Fatalf("expected 0 for zero scope, got %d", zeroScope.BytesAllocated())
	}
}

func TestAdviseHugePage(t *testing.T) {
	smallBuf := make([]byte, 1024)
	if err := AdviseHugePage(smallBuf); err != nil {
		t.Fatalf("small buffer advise failed: %v", err)
	}

	largeBuf := make([]byte, HugePageSize)
	if err := AdviseHugePage(largeBuf); err != nil {
		t.Fatalf("large buffer advise failed: %v", err)
	}
}

func TestContextHelpers(t *testing.T) {
	var nilCtx context.Context
	if FromContext(nilCtx) != nil {
		t.Fatal("expected nil for nil context")
	}
	ctx := WithArena(nilCtx, nil)
	if FromContext(ctx) != nil {
		t.Fatal("expected nil arena from nil attachment")
	}

	arena := New(1024)
	ctx = WithArena(ctx, arena)
	if got := FromContext(ctx); got != arena {
		t.Fatalf("expected arena %p, got %p", arena, got)
	}
}

func TestAllocations_Alloc_Zero(t *testing.T) {
	a := New(64 * 1024)
	allocs := testing.AllocsPerRun(100, func() {
		a.Reset()
		for range 10 {
			b, err := a.Alloc(32)
			if err != nil || b == nil {
				t.Fatalf("alloc error: %v", err)
			}
		}
	})
	if allocs != 0 {
		t.Fatalf("expected 0 allocs/run, got %f", allocs)
	}
}

func TestAllocations_AllocSlice_Zero(t *testing.T) {
	a := New(64 * 1024)
	type Item struct {
		A uint64
		B uint64
	}
	allocs := testing.AllocsPerRun(100, func() {
		a.Reset()
		s, err := AllocSlice[Item](a, 16)
		if err != nil || len(s) != 16 {
			t.Fatalf("AllocSlice error: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("expected 0 allocs/run, got %f", allocs)
	}
}

func TestAllocations_AllocString_Zero(t *testing.T) {
	a := New(64 * 1024)
	const testStr = "carmack-zero-alloc-linear-pack"
	allocs := testing.AllocsPerRun(100, func() {
		a.Reset()
		str, err := AllocString(a, testStr)
		if err != nil || str != testStr {
			t.Fatalf("AllocString error: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("expected 0 allocs/run, got %f", allocs)
	}
}

func TestAllocations_Scope_Zero(t *testing.T) {
	a := New(64 * 1024)
	allocs := testing.AllocsPerRun(100, func() {
		a.Reset()
		scope := a.Scope()
		_, _ = a.Alloc(64)
		scope.End()
	})
	if allocs != 0 {
		t.Fatalf("expected 0 allocs/run, got %f", allocs)
	}
}

func TestAllocSliceRejectsPointerTypes(t *testing.T) {
	a := New(4096)
	type withString struct {
		ID   int64
		Name string
	}
	type nested struct {
		Inner [2]withString
	}
	if _, err := AllocSlice[string](a, 4); !errors.Is(err, ErrPointerType) {
		t.Fatalf("string: got %v, want ErrPointerType", err)
	}
	if _, err := AllocSlice[*int](a, 4); !errors.Is(err, ErrPointerType) {
		t.Fatalf("pointer: got %v, want ErrPointerType", err)
	}
	if _, err := Alloc[withString](a); !errors.Is(err, ErrPointerType) {
		t.Fatalf("struct with string: got %v, want ErrPointerType", err)
	}
	if _, err := Alloc[nested](a); !errors.Is(err, ErrPointerType) {
		t.Fatalf("nested array of pointer struct: got %v, want ErrPointerType", err)
	}
	if _, err := AllocSlice[[]byte](a, 1); !errors.Is(err, ErrPointerType) {
		t.Fatalf("slice: got %v, want ErrPointerType", err)
	}
	if _, err := AllocSlice[any](a, 1); !errors.Is(err, ErrPointerType) {
		t.Fatalf("interface: got %v, want ErrPointerType", err)
	}
	if a.Allocated() != 0 {
		t.Fatalf("rejected allocations must not consume arena space, got %d", a.Allocated())
	}

	type flat struct {
		A int64
		B [4]uint32
		C float64
	}
	if _, err := AllocSlice[flat](a, 4); err != nil {
		t.Fatalf("pointer-free struct rejected: %v", err)
	}
	allocs := testing.AllocsPerRun(100, func() {
		a.Reset()
		if _, err := AllocSlice[flat](a, 4); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("pointer-free check must not allocate after first use, got %.1f", allocs)
	}
}

func TestAllocSliceRejectsOverflow(t *testing.T) {
	a := New(4096)
	if _, err := AllocSlice[uint64](a, math.MaxInt/4); !errors.Is(err, ErrArenaFull) {
		t.Fatalf("overflowing count: got %v, want ErrArenaFull", err)
	}
	if _, err := a.Alloc(math.MaxInt); !errors.Is(err, ErrArenaFull) {
		t.Fatalf("MaxInt size: got %v, want ErrArenaFull", err)
	}
	if _, err := a.Alloc(math.MaxInt - 3); !errors.Is(err, ErrArenaFull) {
		t.Fatalf("align overflow size: got %v, want ErrArenaFull", err)
	}
	if a.Allocated() != 0 {
		t.Fatalf("failed allocations must not move head, got %d", a.Allocated())
	}
}

func TestDetachDropsArenaAndCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(WithArena(context.Background(), New(64)))
	detached := Detach(parent)
	cancel()
	if FromContext(detached) != nil {
		t.Fatal("detached context must not carry the request arena")
	}
	if detached.Err() != nil {
		t.Fatal("detached context must not inherit cancellation")
	}
	//nolint:staticcheck // nil context is the case under test.
	if FromContext(Detach(nil)) != nil {
		t.Fatal("Detach(nil) must give an arena-free context")
	}
}

func TestPointerFreeEdgeCases(t *testing.T) {
	type empty struct{}
	if !PointerFree[[0]*int]() {
		t.Fatal("a zero-length array holds no pointers")
	}
	if !PointerFree[[4][2]uint16]() {
		t.Fatal("nested numeric arrays are pointer-free")
	}
	a := New(64)
	if s, err := AllocSlice[empty](a, 3); err != nil || len(s) != 3 {
		t.Fatalf("zero-size element: len=%d err=%v", len(s), err)
	}
	if _, err := a.Alloc(1 << 20); !errors.Is(err, ErrArenaFull) {
		t.Fatalf("oversize Alloc: got %v, want ErrArenaFull", err)
	}
	// Deep nesting beyond the walk bound is treated as unsafe, never as pointer-free.
	if typePointerFree(reflect.TypeFor[[1][1][1][1][1][1][1][1][1][1][1][1][1][1][1][1][1][1][1][1][1][1][1][1][1][1][1][1][1][1][1][1][1][1]int](), 0) {
		t.Fatal("type nesting beyond the bound must not be reported pointer-free")
	}
}

// TestAllocFailsWhenRemainingSpaceIsTooSmall covers a request that fits the
// buffer but not the space left after earlier allocations.
func TestAllocFailsWhenRemainingSpaceIsTooSmall(t *testing.T) {
	a := New(64)
	if _, err := a.Alloc(48); err != nil {
		t.Fatalf("first alloc: %v", err)
	}
	if _, err := a.Alloc(32); !errors.Is(err, ErrArenaFull) {
		t.Fatalf("second alloc: got %v, want ErrArenaFull", err)
	}
	if _, err := AllocSlice[uint64](a, 4); !errors.Is(err, ErrArenaFull) {
		t.Fatalf("typed alloc past remaining space: got %v, want ErrArenaFull", err)
	}
}
