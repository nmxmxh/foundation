package scratch

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"unsafe"
)

var (
	// ErrArenaFull indicates that requested allocation exceeds available capacity.
	ErrArenaFull = errors.New("scratch arena capacity exceeded")

	// ErrNegativeAlloc indicates an invalid negative allocation size request.
	ErrNegativeAlloc = errors.New("negative allocation size")

	// ErrPointerType indicates that a typed allocation requested a type that holds Go pointers.
	// The arena buffer is a []byte, so the GC does not scan it for pointers.
	ErrPointerType = errors.New("scratch arena cannot hold types that contain Go pointers")
)

// DefaultCapacity is the default buffer size for scratch arenas (64 KiB).
const DefaultCapacity = 64 * 1024

// Arena is a single-worker linear bump allocator for frame-scoped operations.
// It provides zero-heap allocations, O(1) allocation, and O(0) reset.
type Arena struct {
	buf  []byte
	base int
	head int
}

// New creates an Arena with the given capacity in bytes.
// If capacity is <= 0, DefaultCapacity is used.
func New(capacity int) *Arena {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return NewWithBuffer(make([]byte, capacity))
}

// NewWithBuffer wraps an existing byte slice as an Arena.
// It ensures that initial allocations align to 8-byte boundaries.
func NewWithBuffer(buf []byte) *Arena {
	base := 0
	if len(buf) > 0 {
		addr := uintptr(unsafe.Pointer(&buf[0])) // #nosec G103 -- buffer alignment calculation.
		misalign := addr & 7
		if misalign != 0 {
			base = int(8 - misalign)
		}
	}
	return &Arena{
		buf:  buf,
		base: base,
		head: base,
	}
}

func align8(n int) int {
	return (n + 7) &^ 7
}

// Alloc reserves size bytes aligned to an 8-byte boundary.
// It returns a slice view of the reserved region.
func (a *Arena) Alloc(size int) ([]byte, error) {
	if size < 0 {
		return nil, ErrNegativeAlloc
	}
	if size == 0 {
		return nil, nil
	}
	if size > len(a.buf) {
		return nil, fmt.Errorf("%w: requested %d bytes, available %d",
			ErrArenaFull, size, len(a.buf)-a.head)
	}
	aligned := align8(size)
	if aligned > len(a.buf)-a.head {
		return nil, fmt.Errorf("%w: requested %d bytes, available %d",
			ErrArenaFull, size, len(a.buf)-a.head)
	}
	start := a.head
	a.head += aligned
	return a.buf[start : start+size : start+aligned], nil
}

// Reset rewinds the allocation pointer to the beginning.
// This reclaims all arena memory in O(0) time without garbage collection.
func (a *Arena) Reset() {
	a.head = a.base
}

// Capacity returns the total usable byte capacity of the arena.
func (a *Arena) Capacity() int {
	return len(a.buf) - a.base
}

// Allocated returns the number of bytes currently allocated from the arena.
func (a *Arena) Allocated() int {
	return a.head - a.base
}

// Available returns the remaining unallocated bytes in the arena.
func (a *Arena) Available() int {
	return len(a.buf) - a.head
}

// AllocBytes reserves capacity bytes and returns a slice with length and capacity.
// Both length and capacity must be non-negative with length <= capacity.
func (a *Arena) AllocBytes(length, capacity int) ([]byte, error) {
	if length < 0 || capacity < length {
		return nil, ErrNegativeAlloc
	}
	if capacity == 0 {
		return nil, nil
	}
	raw, err := a.Alloc(capacity)
	if err != nil {
		return nil, err
	}
	return raw[:length], nil
}

// Raw returns the underlying storage slice.
func (a *Arena) Raw() []byte {
	return a.buf[a.base:]
}

// Alloc allocates a single value of type T in the arena and returns a pointer to it.
// The allocation is 8-byte aligned and incurs zero heap allocations.
// T must be pointer-free. Otherwise Alloc returns ErrPointerType.
func Alloc[T any](a *Arena) (*T, error) {
	slice, err := AllocSlice[T](a, 1)
	if err != nil {
		return nil, err
	}
	return &slice[0], nil
}

// AllocSlice allocates a contiguous slice of type T with the specified count.
// Allocations are 8-byte aligned and incur zero heap allocations.
//
// WARNING: T must be pointer-free (no string, slice, map, pointer, interface,
// chan, or func fields). The GC does not scan arena memory. A Go pointer kept
// only there can be freed while in use. AllocSlice returns ErrPointerType for such T.
func AllocSlice[T any](a *Arena, count int) ([]T, error) {
	if count < 0 {
		return nil, ErrNegativeAlloc
	}
	if count == 0 {
		return nil, nil
	}
	if !PointerFree[T]() {
		return nil, ErrPointerType
	}
	var zero T
	elemSize := int(unsafe.Sizeof(zero))
	if elemSize == 0 {
		return make([]T, count), nil
	}
	if count > len(a.buf)/elemSize {
		return nil, fmt.Errorf("%w: requested %d elements of %d bytes, available %d",
			ErrArenaFull, count, elemSize, len(a.buf)-a.head)
	}
	totalBytes := elemSize * count
	raw, err := a.Alloc(totalBytes)
	if err != nil {
		return nil, err
	}
	clear(raw)
	return unsafe.Slice((*T)(unsafe.Pointer(&raw[0])), count), nil // #nosec G103 -- arena typed slice view.
}

var pointerFreeCache sync.Map // reflect.Type -> bool

// PointerFree reports whether T holds no Go pointers and is safe to place in an arena.
// The result is cached per type. Lookups after the first do not allocate.
func PointerFree[T any]() bool {
	t := reflect.TypeFor[T]()
	if v, ok := pointerFreeCache.Load(t); ok {
		return v.(bool)
	}
	free := typePointerFree(t, 0)
	pointerFreeCache.Store(t, free)
	return free
}

// typePointerFree walks the type. The depth bound stops pathological nesting (CP-02).
func typePointerFree(t reflect.Type, depth int) bool {
	if depth > 32 {
		return false
	}
	switch t.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
		return true
	case reflect.Array:
		return t.Len() == 0 || typePointerFree(t.Elem(), depth+1)
	case reflect.Struct:
		for field := range t.Fields() {
			if !typePointerFree(field.Type, depth+1) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// AllocString copies string s into the arena and returns an immutable string view.
// The returned string references arena memory and incurs zero heap allocations.
//
// WARNING: the string is valid only until Reset. Do not keep it in a map,
// cache, event, log, or any value that outlives the arena scope.
func AllocString(a *Arena, s string) (string, error) {
	if len(s) == 0 {
		return "", nil
	}
	raw, err := a.Alloc(len(s))
	if err != nil {
		return "", err
	}
	copy(raw, s)
	return unsafe.String(&raw[0], len(s)), nil // #nosec G103 -- arena immutable string view.
}

type contextKey struct{}

// WithArena attaches a ScratchArena to the given context.
func WithArena(ctx context.Context, a *Arena) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, contextKey{}, a)
}

// FromContext extracts a ScratchArena from the context, or returns nil if absent.
func FromContext(ctx context.Context) *Arena {
	if ctx == nil {
		return nil
	}
	a, _ := ctx.Value(contextKey{}).(*Arena)
	return a
}

// Detach returns a context that outlives ctx: no cancellation, no deadline, and no arena.
// Use it for work that continues after the request returns. The request arena
// is reset and reused then, so detached work must never see it.
func Detach(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return WithArena(context.WithoutCancel(ctx), nil)
}
