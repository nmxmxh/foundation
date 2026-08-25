package runtimehost

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

func testArena(t *testing.T, size uint32) *Arena {
	t.Helper()
	arena, err := NewArenaOver(make([]byte, size))
	if err != nil {
		t.Fatalf("NewArenaOver: %v", err)
	}
	return arena
}

func TestArenaRejectsRegionsOutsideTheSpecTiers(t *testing.T) {
	if _, err := NewArenaOver(make([]byte, 4096)); err == nil {
		t.Error("a 4KB region was accepted; that is the control buffer size, not an arena")
	}
	if _, err := NewArenaOver(make([]byte, generated.ARENA_MIN_BYTES)); err != nil {
		t.Errorf("the minimum tier was rejected: %v", err)
	}
}

func TestArenaHeaderMatchesTheSpec(t *testing.T) {
	arena := testArena(t, generated.ARENA_MIN_BYTES)
	read := func(idx uint32) uint32 {
		off := generated.ARENA_OFFSET_HEADER + idx*4
		return binary.LittleEndian.Uint32(arena.raw[off : off+4])
	}

	if got := read(generated.ARENA_HEADER_IDX_MAGIC); got != generated.ARENA_HEADER_MAGIC {
		t.Errorf("magic = %#x, want %#x (OVRA)", got, generated.ARENA_HEADER_MAGIC)
	}
	if got := read(generated.ARENA_HEADER_IDX_SCHEMA_VERSION); got != generated.ARENA_SCHEMA_VERSION {
		t.Errorf("schema version = %d, want %d", got, generated.ARENA_SCHEMA_VERSION)
	}
	if got := read(generated.ARENA_HEADER_IDX_CAPACITY_BYTES); got != generated.ARENA_MIN_BYTES {
		t.Errorf("capacity = %d, want %d", got, generated.ARENA_MIN_BYTES)
	}
	// Slabs start after the foundation-owned control structures; allocating over
	// the descriptor table would corrupt the addressing of every other slab.
	if got := read(generated.ARENA_HEADER_IDX_ALLOCATED_BYTES); got != generated.ARENA_OFFSET_PAGES {
		t.Errorf("initial alloc head = %d, want ARENA_OFFSET_PAGES %d", got, generated.ARENA_OFFSET_PAGES)
	}
}

func TestArenaSlabsArePageAlignedAndAboveTheControlRegion(t *testing.T) {
	arena := testArena(t, generated.ARENA_MIN_BYTES)

	for i := range 8 {
		desc, slab, err := arena.Allocate(100, generated.ARENA_DESCRIPTOR_TYPE_BYTES)
		if err != nil {
			t.Fatalf("Allocate: %v", err)
		}
		if desc.Offset%generated.ARENA_PAGE_BYTES != 0 {
			t.Errorf("slab %d offset %d is not page aligned", i, desc.Offset)
		}
		if desc.Offset < generated.ARENA_OFFSET_PAGES {
			t.Errorf("slab %d at %d overlaps the control region (< %d)",
				i, desc.Offset, generated.ARENA_OFFSET_PAGES)
		}
		if len(slab) != 100 {
			t.Errorf("slab %d is %d bytes, want the requested 100", i, len(slab))
		}
		// Capacity is rounded up, but the returned view must not let a caller
		// write past what it asked for.
		if cap(slab) != int(desc.Capacity) {
			t.Errorf("slab %d cap = %d, want the page capacity %d", i, cap(slab), desc.Capacity)
		}
	}
}

func TestArenaSlabsDoNotOverlap(t *testing.T) {
	arena := testArena(t, generated.ARENA_MIN_BYTES)

	type span struct{ start, end uint32 }
	var spans []span
	for range 16 {
		desc, _, err := arena.Allocate(5000, generated.ARENA_DESCRIPTOR_TYPE_BYTES)
		if err != nil {
			t.Fatalf("Allocate: %v", err)
		}
		spans = append(spans, span{desc.Offset, desc.Offset + desc.Capacity})
	}
	for i := range spans {
		for j := i + 1; j < len(spans); j++ {
			if spans[i].start < spans[j].end && spans[j].start < spans[i].end {
				t.Fatalf("slabs %d %v and %d %v overlap", i, spans[i], j, spans[j])
			}
		}
	}
}

// A slab must be READY only once its bytes are written: a consumer that sees an
// addressable-but-unfilled slab reads zeros and reports a plausible wrong answer.
func TestArenaCommitPublishesOnlyAfterWriting(t *testing.T) {
	arena := testArena(t, generated.ARENA_MIN_BYTES)

	desc, slab, err := arena.Allocate(64, generated.ARENA_DESCRIPTOR_TYPE_BYTES)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	stored, err := arena.Descriptor(desc.ID)
	if err != nil {
		t.Fatalf("Descriptor: %v", err)
	}
	if stored.State != generated.ARENA_DESCRIPTOR_STATE_ALLOCATED {
		t.Errorf("state after Allocate = %d, want ALLOCATED %d",
			stored.State, generated.ARENA_DESCRIPTOR_STATE_ALLOCATED)
	}

	copy(slab, []byte("filled"))
	arena.Commit(desc)

	stored, _ = arena.Descriptor(desc.ID)
	if stored.State != generated.ARENA_DESCRIPTOR_STATE_READY {
		t.Errorf("state after Commit = %d, want READY %d",
			stored.State, generated.ARENA_DESCRIPTOR_STATE_READY)
	}
}

func TestArenaCapacityExceededIsReported(t *testing.T) {
	arena := testArena(t, generated.ARENA_MIN_BYTES)

	// One allocation larger than the whole region.
	if _, _, err := arena.Allocate(generated.ARENA_MIN_BYTES, generated.ARENA_DESCRIPTOR_TYPE_BYTES); err == nil {
		t.Error("an over-capacity allocation succeeded")
	}
}

func TestArenaDescriptorTableExhaustionIsReported(t *testing.T) {
	arena := testArena(t, generated.ARENA_HEAVY_BYTES)

	var err error
	for i := uint32(0); i <= generated.ARENA_DESCRIPTOR_COUNT; i++ {
		if _, _, err = arena.Allocate(16, generated.ARENA_DESCRIPTOR_TYPE_BYTES); err != nil {
			break
		}
	}
	if err == nil {
		t.Fatalf("allocating more than %d descriptors succeeded", generated.ARENA_DESCRIPTOR_COUNT)
	}
}

// Reset must clear the descriptor table, or a reused mapping presents a stale
// slab from the previous exchange as READY.
func TestArenaResetClearsStaleDescriptors(t *testing.T) {
	arena := testArena(t, generated.ARENA_MIN_BYTES)

	desc, slab, err := arena.Allocate(32, generated.ARENA_DESCRIPTOR_TYPE_BYTES)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	copy(slab, []byte("stale"))
	arena.Commit(desc)

	arena.Reset()

	stored, err := arena.Descriptor(desc.ID)
	if err != nil {
		t.Fatalf("Descriptor: %v", err)
	}
	if stored.State != generated.ARENA_DESCRIPTOR_STATE_FREE {
		t.Errorf("descriptor state after Reset = %d, want FREE %d",
			stored.State, generated.ARENA_DESCRIPTOR_STATE_FREE)
	}
	if stats := arena.Stats(); stats.AllocatedBytes != generated.ARENA_OFFSET_PAGES {
		t.Errorf("alloc head after Reset = %d, want %d", stats.AllocatedBytes, generated.ARENA_OFFSET_PAGES)
	}
}

// A descriptor is untrusted input even when this process wrote it: the region is
// shared, so bounds come from the table and are validated against the mapping.
func TestArenaSlabRejectsOutOfBoundsDescriptor(t *testing.T) {
	arena := testArena(t, generated.ARENA_MIN_BYTES)

	desc, _, err := arena.Allocate(64, generated.ARENA_DESCRIPTOR_TYPE_BYTES)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	// Corrupt the stored offset the way another process could.
	base := generated.ARENA_OFFSET_DESCRIPTOR_TABLE + desc.ID*generated.ARENA_DESCRIPTOR_SIZE
	binary.LittleEndian.PutUint32(arena.raw[base+descriptorFieldOffset:], generated.ARENA_MIN_BYTES-8)
	binary.LittleEndian.PutUint32(arena.raw[base+descriptorFieldLength:], 4096)

	if _, err := arena.Slab(desc.ID); err == nil {
		t.Error("Slab returned bytes for a descriptor addressing past the mapping")
	}

	// And one pointing into the control region.
	binary.LittleEndian.PutUint32(arena.raw[base+descriptorFieldOffset:], 0)
	binary.LittleEndian.PutUint32(arena.raw[base+descriptorFieldLength:], 64)
	if _, err := arena.Slab(desc.ID); err == nil {
		t.Error("Slab returned bytes for a descriptor addressing the control region")
	}
}

// The whole point: an embedding matrix that does not fit the 1 KiB control
// payload rides the arena instead, and the control buffer carries only an id.
func TestColumnarBatchCarriesAnEmbeddingMatrix(t *testing.T) {
	const (
		rows = 2000
		dims = 384
	)
	arena := testArena(t, generated.ARENA_HEAVY_BYTES)

	values := make([]float32, rows*dims)
	for i := range values {
		values[i] = float32(i%97) * 0.01
	}

	column, err := WriteFloat32Column(arena, 0, values)
	if err != nil {
		t.Fatalf("WriteFloat32Column: %v", err)
	}
	batchID, err := WriteColumnarBatch(arena, rows, []ColumnarField{column})
	if err != nil {
		t.Fatalf("WriteColumnarBatch: %v", err)
	}

	payloadBytes := len(values) * 4
	if payloadBytes <= int(generated.INPUT_MAX_BYTES) {
		t.Fatalf("test payload is %d bytes, which fits the control buffer; it proves nothing",
			payloadBytes)
	}

	gotRows, fields, err := ReadColumnarBatch(arena, batchID)
	if err != nil {
		t.Fatalf("ReadColumnarBatch: %v", err)
	}
	if gotRows != rows || len(fields) != 1 {
		t.Fatalf("batch = %d rows / %d fields, want %d / 1", gotRows, len(fields), rows)
	}
	if fields[0].Length != uint32(len(values)) {
		t.Fatalf("column length = %d, want %d", fields[0].Length, len(values))
	}

	back, err := ReadFloat32Column(arena, fields[0])
	if err != nil {
		t.Fatalf("ReadFloat32Column: %v", err)
	}
	for i := range values {
		if back[i] != values[i] {
			t.Fatalf("value %d round-tripped as %v, want %v", i, back[i], values[i])
		}
	}
}

// Fixed-width columns must start page aligned so the consumer can take a SIMD
// view over the mapping without a copy.
func TestColumnarValueSlabsAreAlignedForSIMD(t *testing.T) {
	arena := testArena(t, generated.ARENA_DEFAULT_BYTES)

	column, err := WriteFloat32Column(arena, 0, make([]float32, 1024))
	if err != nil {
		t.Fatalf("WriteFloat32Column: %v", err)
	}
	desc, err := arena.Descriptor(column.ValuesDescriptorID)
	if err != nil {
		t.Fatalf("Descriptor: %v", err)
	}
	if desc.Offset%generated.COLUMNAR_BATCH_ALIGNMENT_BYTES != 0 {
		t.Errorf("values slab at %d is not %d-byte aligned",
			desc.Offset, generated.COLUMNAR_BATCH_ALIGNMENT_BYTES)
	}
}

func TestColumnarBatchRejectsCorruptMagic(t *testing.T) {
	arena := testArena(t, generated.ARENA_MIN_BYTES)

	column, err := WriteFloat32Column(arena, 0, []float32{1, 2, 3})
	if err != nil {
		t.Fatalf("WriteFloat32Column: %v", err)
	}
	batchID, err := WriteColumnarBatch(arena, 3, []ColumnarField{column})
	if err != nil {
		t.Fatalf("WriteColumnarBatch: %v", err)
	}

	slab, err := arena.Slab(batchID)
	if err != nil {
		t.Fatalf("Slab: %v", err)
	}
	binary.LittleEndian.PutUint32(slab[0:4], 0xdeadbeef)

	if _, _, err := ReadColumnarBatch(arena, batchID); err == nil {
		t.Error("a batch with the wrong magic was accepted")
	}
}

func TestColumnarBatchSupportsMixedColumns(t *testing.T) {
	arena := testArena(t, generated.ARENA_DEFAULT_BYTES)

	vectors, err := WriteFloat32Column(arena, 0, []float32{0.5, 0.25, 0.125, 0.0625})
	if err != nil {
		t.Fatalf("WriteFloat32Column: %v", err)
	}
	placeIDs, err := WriteUint32Column(arena, 1, []uint32{7, 7, 0, 12})
	if err != nil {
		t.Fatalf("WriteUint32Column: %v", err)
	}

	batchID, err := WriteColumnarBatch(arena, 4, []ColumnarField{vectors, placeIDs})
	if err != nil {
		t.Fatalf("WriteColumnarBatch: %v", err)
	}
	rows, fields, err := ReadColumnarBatch(arena, batchID)
	if err != nil {
		t.Fatalf("ReadColumnarBatch: %v", err)
	}
	if rows != 4 || len(fields) != 2 {
		t.Fatalf("batch = %d rows / %d fields, want 4 / 2", rows, len(fields))
	}
	if fields[0].LogicalType != generated.COLUMNAR_LOGICAL_TYPE_FLOAT {
		t.Errorf("field 0 logical type = %d, want FLOAT", fields[0].LogicalType)
	}
	if fields[1].LogicalType != generated.COLUMNAR_LOGICAL_TYPE_UINT {
		t.Errorf("field 1 logical type = %d, want UINT", fields[1].LogicalType)
	}
}

// A worker configured with an arena must map one and advertise it to the child,
// or the data plane exists on paper only — which is how the shared-memory
// transport itself stayed dead for so long.
func TestProcessPoolProvisionsArenaPerWorker(t *testing.T) {
	if !sharedMemorySupported("") {
		t.Skip("shared memory transport is not supported on this runtime")
	}

	worker := &processWorker{
		mode:       ProcessTransportSharedMemory,
		arenaBytes: normalizeArenaBytes(generated.ARENA_MIN_BYTES),
	}
	t.Cleanup(func() {
		if worker.arenaShm != nil {
			_ = worker.arenaShm.Close()
		}
		if worker.shm != nil {
			_ = worker.shm.Close()
		}
	})

	segment, err := newSharedMemorySegment(worker.shmDir, int(generated.BUFFER_TOTAL_BYTES))
	if err != nil {
		t.Fatalf("control segment: %v", err)
	}
	worker.shm = segment

	arenaSegment, err := newSharedMemorySegment(worker.shmDir, int(worker.arenaBytes))
	if err != nil {
		t.Fatalf("arena segment: %v", err)
	}
	worker.arenaShm = arenaSegment
	arena, err := NewArenaOver(arenaSegment.raw)
	if err != nil {
		t.Fatalf("NewArenaOver: %v", err)
	}
	worker.arena = arena

	if worker.Arena() == nil {
		t.Fatal("worker reports no arena after provisioning one")
	}

	// A payload the control buffer cannot carry must survive the arena.
	values := make([]float32, 3000)
	for i := range values {
		values[i] = float32(i)
	}
	if len(values)*4 <= int(generated.INPUT_MAX_BYTES) {
		t.Fatal("test payload fits the control buffer; it proves nothing")
	}

	column, err := WriteFloat32Column(arena, 0, values)
	if err != nil {
		t.Fatalf("WriteFloat32Column: %v", err)
	}
	batchID, err := WriteColumnarBatch(arena, uint32(len(values)), []ColumnarField{column})
	if err != nil {
		t.Fatalf("WriteColumnarBatch: %v", err)
	}
	// The id is what crosses the control plane: four bytes for 12KB of payload.
	if batchID > generated.ARENA_DESCRIPTOR_COUNT {
		t.Fatalf("descriptor id %d out of range", batchID)
	}
}

func TestNormalizeArenaBytesClampsToSpecTiers(t *testing.T) {
	if got := normalizeArenaBytes(0); got != 0 {
		t.Errorf("zero requested arena = %d, want 0 (no data plane)", got)
	}
	if got := normalizeArenaBytes(1024); got != generated.ARENA_MIN_BYTES {
		t.Errorf("undersized request = %d, want the %d minimum", got, generated.ARENA_MIN_BYTES)
	}
	if got := normalizeArenaBytes(generated.ARENA_MAX_BYTES * 2); got != generated.ARENA_MAX_BYTES {
		t.Errorf("oversized request = %d, want the %d maximum", got, generated.ARENA_MAX_BYTES)
	}
	if got := normalizeArenaBytes(generated.ARENA_DEFAULT_BYTES); got != generated.ARENA_DEFAULT_BYTES {
		t.Errorf("in-range request was altered: %d", got)
	}
}

// countingExchange records how many exchanges it served.
type countingExchange struct{ calls int }

func (c *countingExchange) Exchange(context.Context, string, []byte) error { c.calls++; return nil }
func (c *countingExchange) Close() error                                   { return nil }
func (c *countingExchange) Restart() error                                 { return nil }

// ExecuteOnWorker must run on the worker it names, every time.
//
// The arena makes this a correctness requirement rather than a scheduling
// preference: a request carries descriptor ids into one worker's arena, and any
// other worker's arena does not contain them. When this pin was silently dropped
// the pool round-robined, so alternate calls read an arena that had never been
// staged — and the arena tests still passed, because a pool with one worker
// cannot tell the difference. Two workers is the whole point of this test.
func TestExecuteOnWorkerAlwaysRunsOnTheNamedWorker(t *testing.T) {
	first, second := &countingExchange{}, &countingExchange{}
	pool := &ProcessPool{
		allWorkers: []*processWorker{
			{logger: testLogger(t), mode: ProcessTransportStdio, testExchange: first},
			{logger: testLogger(t), mode: ProcessTransportStdio, testExchange: second},
		},
	}
	pool.bufferPool.New = func() any {
		buffer := make([]byte, generated.BUFFER_TOTAL_BYTES)
		return &buffer
	}

	const calls = 6
	for range calls {
		if _, err := pool.ExecuteOnWorker(context.Background(), 1, ProcessRequest{UnitID: "runtime.echo"}); err != nil {
			t.Fatalf("ExecuteOnWorker() error = %v", err)
		}
	}
	if second.calls != calls || first.calls != 0 {
		t.Fatalf("pinned worker served %d of %d exchanges; worker 0 served %d",
			second.calls, calls, first.calls)
	}
}

func TestArenaMappingAndSlab(t *testing.T) {
	data := make([]byte, generated.ARENA_MIN_BYTES)
	flushed := 0
	flush := func(staged int) error {
		flushed = staged
		return nil
	}
	readAt := func(dst []byte, off int64) error {
		copy(dst, data[off:])
		return nil
	}

	arena, err := NewArenaOverMapping(data, flush, readAt)
	if err != nil {
		t.Fatalf("failed to create arena over mapping: %v", err)
	}

	desc, slice, err := arena.Allocate(100, 1)
	if err != nil {
		t.Fatalf("failed to allocate: %v", err)
	}
	copy(slice, []byte("payload-data"))
	arena.Commit(desc)

	if err := arena.Sync(); err != nil {
		t.Fatalf("failed to sync arena: %v", err)
	}
	if flushed == 0 {
		t.Fatalf("expected flushed > 0")
	}

	readData, err := arena.ReadSlab(desc.ID)
	if err != nil {
		t.Fatalf("failed to read slab: %v", err)
	}
	if string(readData[:12]) != "payload-data" {
		t.Fatalf("unexpected read data: %s", string(readData[:12]))
	}
}
