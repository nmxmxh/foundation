package runtimehost

import (
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

// The shared arena is the data plane. The 4 KiB control buffer is not.
//
// runtime_buffer.capnp is deliberately small so it stays resident in L1/L2: it
// carries a schema version, a status code, a few epochs and a payload of at most
// 1 KiB. That is the right size for a control message and the wrong size for a
// workload, and the two were conflated — units handed real data (an embedding
// matrix, a ranking batch) pushed it through the control payload, exceeded the
// limit on every call, and their callers silently used a fallback path. Growing
// the control buffer to fit would have destroyed the property it exists for.
//
// runtime_shared_arena.capnp already specifies the answer, and the browser host
// already implements it: a separately mapped region, sized in tiers, holding
// page-aligned slabs addressed by a descriptor table. The control buffer then
// carries a descriptor id — a handful of bytes — and the bulk stays in the
// arena, read in place by the kernel with no copy and no serialization.
//
// This file is the Go side of that data plane. It is deliberately narrower than
// the browser implementation: the native path is single-producer
// (the host writes) and single-consumer (one pool worker reads) within a
// request/response exchange, so the arena's queue, backpressure signalling and
// SharedArrayBuffer negotiation are not needed. The layout is byte-identical, so
// a slab written here is readable by the browser host and by ovrt-native.

// Descriptor field offsets within a 32-byte table entry, little-endian.
// Mirrors ts/browser-host/src/arena.ts readDescriptor/writeDescriptor.
const (
	descriptorFieldState         = 0
	descriptorFieldOffset        = 4
	descriptorFieldLength        = 8
	descriptorFieldCapacity      = 12
	descriptorFieldType          = 16
	descriptorFieldFlags         = 20
	descriptorFieldProducerEpoch = 24
	descriptorFieldConsumerEpoch = 28
)

// ArenaDescriptor addresses one slab.
type ArenaDescriptor struct {
	ID       uint32
	State    uint32
	Offset   uint32
	Length   uint32
	Capacity uint32
	Type     uint32
	Flags    uint32
}

// Arena is a mapped region of page-aligned slabs addressed by descriptor id.
//
// Not safe for concurrent allocation by design: a pool worker owns its arena for
// the duration of an exchange, which is what makes the bump allocator correct
// without atomics. Concurrency lives at the pool level, one arena per worker.
type Arena struct {
	mu        sync.Mutex
	raw       []byte
	capacity  uint32
	allocHead uint32
	nextID    uint32

	// flush publishes the staged prefix so another process observes it. It is
	// given the number of bytes in use, because the arena is sized for the
	// worst case and a typical exchange stages a small fraction of it —
	// publishing the whole region would copy megabytes that hold nothing.
	// Nil for a plain byte slice (tests).
	flush func(staged int) error

	// readAt reads through the backing file rather than the staging buffer, for
	// regions the consumer wrote. See ReadSlab.
	readAt func(dst []byte, offset int64) error
}

// NewArenaOverMapping wraps a staging region, with a flush hook so staged writes
// can be made visible to another process.
func NewArenaOverMapping(raw []byte, flush func(staged int) error, readAt func([]byte, int64) error) (*Arena, error) {
	arena, err := NewArenaOver(raw)
	if err != nil {
		return nil, err
	}
	arena.flush = flush
	arena.readAt = readAt
	return arena, nil
}

// NewArenaOver wraps an already-mapped region.
//
// Takes the mapping rather than performing it so the platform-specific mmap
// stays in the build-tagged files and this logic is testable on any OS with a
// plain byte slice.
func NewArenaOver(raw []byte) (*Arena, error) {
	// Both bounds are checked in int/uint64 space before any narrowing. A
	// uint32(len(raw)) comparison would truncate a region larger than 4 GiB and
	// could report it as undersized rather than oversized.
	if len(raw) < int(generated.ARENA_MIN_BYTES) {
		return nil, fmt.Errorf("arena region is %d bytes, below the %d minimum",
			len(raw), generated.ARENA_MIN_BYTES)
	}
	if uint64(len(raw)) > uint64(generated.ARENA_MAX_BYTES) {
		return nil, fmt.Errorf("arena region is %d bytes, above the %d maximum",
			len(raw), generated.ARENA_MAX_BYTES)
	}
	// #nosec G115 -- guarded above: len(raw) <= ARENA_MAX_BYTES < MaxUint32.
	a := &Arena{raw: raw, capacity: uint32(len(raw))}
	a.reset()
	return a, nil
}

// reset writes the header and returns the allocator to the first slab page.
func (a *Arena) reset() {
	a.putHeader(generated.ARENA_HEADER_IDX_MAGIC, generated.ARENA_HEADER_MAGIC)
	a.putHeader(generated.ARENA_HEADER_IDX_SCHEMA_VERSION, generated.ARENA_SCHEMA_VERSION)
	a.putHeader(generated.ARENA_HEADER_IDX_CAPACITY_BYTES, a.capacity)
	a.putHeader(generated.ARENA_HEADER_IDX_ALLOCATED_BYTES, generated.ARENA_OFFSET_PAGES)
	a.putHeader(generated.ARENA_HEADER_IDX_DESCRIPTOR_COUNT, 0)
	a.putHeader(generated.ARENA_HEADER_IDX_QUEUE_DROPPED, 0)
	a.putHeader(generated.ARENA_HEADER_IDX_FLAGS, 0)

	// Clear the descriptor table so a reused mapping cannot present a stale slab
	// as READY to the consumer.
	table := generated.ARENA_OFFSET_DESCRIPTOR_TABLE
	clear(a.raw[table : table+generated.ARENA_DESCRIPTOR_TABLE_BYTES])

	a.putEpoch(generated.ARENA_IDX_ALLOC_HEAD, generated.ARENA_OFFSET_PAGES)
	a.putEpoch(generated.ARENA_IDX_READY, 1)

	a.allocHead = generated.ARENA_OFFSET_PAGES
	a.nextID = 0
}

// Reset returns the arena to its empty state, keeping the mapping.
//
// Slabs are reclaimed wholesale between exchanges rather than freed
// individually: an exchange is a batch, its slabs all die together, and a bump
// allocator plus a full reset avoids a free list and the fragmentation that
// comes with one.
func (a *Arena) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reset()
}

func (a *Arena) putHeader(index, value uint32) {
	off := generated.ARENA_OFFSET_HEADER + index*4
	binary.LittleEndian.PutUint32(a.raw[off:off+4], value)
}

func (a *Arena) putEpoch(index, value uint32) {
	off := generated.ARENA_OFFSET_EPOCHS + index*4
	binary.LittleEndian.PutUint32(a.raw[off:off+4], value)
}

func alignToPage(n uint32) uint32 {
	page := generated.ARENA_PAGE_BYTES
	if n == 0 {
		return page
	}
	return ((n + page - 1) / page) * page
}

// Allocate reserves a page-aligned slab and returns its descriptor.
//
// The returned descriptor is in state ALLOCATED. Call Commit once the bytes are
// written to publish it as READY — a consumer must never observe a slab that is
// addressable but not yet filled.
func (a *Arena) Allocate(length uint32, slabType uint32) (ArenaDescriptor, []byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if length == 0 {
		return ArenaDescriptor{}, nil, fmt.Errorf("arena allocation length must be positive")
	}
	if a.nextID >= generated.ARENA_DESCRIPTOR_COUNT {
		return ArenaDescriptor{}, nil, fmt.Errorf(
			"arena descriptor table is full (%d entries); batch fewer slabs per exchange",
			generated.ARENA_DESCRIPTOR_COUNT)
	}

	capacity := alignToPage(length)
	offset := a.allocHead
	end := uint64(offset) + uint64(capacity)
	if end > uint64(a.capacity) {
		return ArenaDescriptor{}, nil, fmt.Errorf(
			"arena capacity exceeded: need %d bytes, have %d", end, a.capacity)
	}

	id := a.nextID
	a.nextID++
	a.allocHead = offset + capacity

	desc := ArenaDescriptor{
		ID:       id,
		State:    generated.ARENA_DESCRIPTOR_STATE_ALLOCATED,
		Offset:   offset,
		Length:   length,
		Capacity: capacity,
		Type:     slabType,
	}
	a.writeDescriptor(desc)
	a.putHeader(generated.ARENA_HEADER_IDX_ALLOCATED_BYTES, a.allocHead)
	a.putHeader(generated.ARENA_HEADER_IDX_DESCRIPTOR_COUNT, a.nextID)
	a.putEpoch(generated.ARENA_IDX_ALLOC_HEAD, a.allocHead)

	return desc, a.raw[offset : offset+length : offset+capacity], nil
}

// Commit publishes a slab as READY.
func (a *Arena) Commit(desc ArenaDescriptor) {
	a.mu.Lock()
	defer a.mu.Unlock()
	desc.State = generated.ARENA_DESCRIPTOR_STATE_READY
	a.writeDescriptor(desc)
	a.putEpoch(generated.ARENA_IDX_DESCRIPTOR_EPOCH, desc.ID+1)
}

func (a *Arena) writeDescriptor(desc ArenaDescriptor) {
	base := generated.ARENA_OFFSET_DESCRIPTOR_TABLE + desc.ID*generated.ARENA_DESCRIPTOR_SIZE
	put := func(field, value uint32) {
		off := base + field
		binary.LittleEndian.PutUint32(a.raw[off:off+4], value)
	}
	put(descriptorFieldState, desc.State)
	put(descriptorFieldOffset, desc.Offset)
	put(descriptorFieldLength, desc.Length)
	put(descriptorFieldCapacity, desc.Capacity)
	put(descriptorFieldType, desc.Type)
	put(descriptorFieldFlags, desc.Flags)
	put(descriptorFieldProducerEpoch, desc.ID+1)
	put(descriptorFieldConsumerEpoch, 0)
}

// Descriptor reads one table entry.
func (a *Arena) Descriptor(id uint32) (ArenaDescriptor, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.descriptor(id)
}

func (a *Arena) descriptor(id uint32) (ArenaDescriptor, error) {
	if id >= generated.ARENA_DESCRIPTOR_COUNT {
		return ArenaDescriptor{}, fmt.Errorf("arena descriptor id %d out of range", id)
	}
	base := generated.ARENA_OFFSET_DESCRIPTOR_TABLE + id*generated.ARENA_DESCRIPTOR_SIZE
	get := func(field uint32) uint32 {
		off := base + field
		return binary.LittleEndian.Uint32(a.raw[off : off+4])
	}
	return ArenaDescriptor{
		ID:       id,
		State:    get(descriptorFieldState),
		Offset:   get(descriptorFieldOffset),
		Length:   get(descriptorFieldLength),
		Capacity: get(descriptorFieldCapacity),
		Type:     get(descriptorFieldType),
		Flags:    get(descriptorFieldFlags),
	}, nil
}

// Slab returns the bytes a descriptor addresses.
//
// Bounds are re-derived from the table rather than trusted from the caller: the
// region is shared with another process, so a descriptor is untrusted input even
// when this process wrote it.
func (a *Arena) Slab(id uint32) ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	desc, err := a.descriptor(id)
	if err != nil {
		return nil, err
	}
	end := uint64(desc.Offset) + uint64(desc.Length)
	if desc.Offset < generated.ARENA_OFFSET_PAGES || end > uint64(a.capacity) {
		return nil, fmt.Errorf("arena descriptor %d addresses [%d,%d) outside the slab region",
			id, desc.Offset, end)
	}
	return a.raw[desc.Offset : desc.Offset+desc.Length], nil
}

// Sync flushes staged writes so a consumer reading the mapping through ordinary
// file I/O observes them.
//
// Must be called after staging and before dispatching, whenever the consumer is
// another process. Without it the reader can see a descriptor that is
// addressable but still reads FREE — intermittently, under load.
func (a *Arena) Sync() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.flush == nil {
		return nil
	}
	return a.flush(int(a.allocHead))
}

// ReadSlab returns a slab's bytes, reading through the backing file when one is
// present.
//
// Use this for any region the *consumer* wrote. Slab returns a view of the host's
// mapping, which is correct for data the host staged and wrong for a result a
// kernel produced: a kernel that cannot use unsafe code writes with pwrite, and
// those bytes never appear in an already-established mapping. Reading them back
// through the mapping silently returns the pre-call contents — a zeroed slab that
// decodes as an empty result rather than an error.
func (a *Arena) ReadSlab(id uint32) ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	desc, err := a.descriptor(id)
	if err != nil {
		return nil, err
	}
	end := uint64(desc.Offset) + uint64(desc.Length)
	if desc.Offset < generated.ARENA_OFFSET_PAGES || end > uint64(a.capacity) {
		return nil, fmt.Errorf("arena descriptor %d addresses [%d,%d) outside the slab region",
			id, desc.Offset, end)
	}
	if a.readAt == nil {
		return a.raw[desc.Offset : desc.Offset+desc.Length], nil
	}
	out := make([]byte, desc.Length)
	if err := a.readAt(out, int64(desc.Offset)); err != nil {
		return nil, fmt.Errorf("read arena slab %d: %w", id, err)
	}
	return out, nil
}

// Stats reports allocator occupancy, for capacity tuning and tests.
type ArenaStats struct {
	CapacityBytes  uint32
	AllocatedBytes uint32
	Descriptors    uint32
}

func (a *Arena) Stats() ArenaStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return ArenaStats{
		CapacityBytes:  a.capacity,
		AllocatedBytes: a.allocHead,
		Descriptors:    a.nextID,
	}
}
