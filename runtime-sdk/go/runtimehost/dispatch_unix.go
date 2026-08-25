//go:build linux || darwin

package runtimehost

import (
	"fmt"
	"os"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

// DispatchBlock is the Go half of the shared dispatch region.
//
// It mirrors ovrt-dispatch's Rust block byte-for-byte: same generated offsets,
// same single-writer-per-stat-row rule, same Release/Acquire pairing on the
// flip index (Go's sequentially consistent atomics are strictly stronger than
// the release/acquire the protocol needs). The Rust publisher and this reader
// never disagree about layout because both read it from the same schema.
//
// The DispatchBlock and DispatchStatRow types are declared in dispatch.go so
// unsupported platforms compile against them too; this file owns their
// mmap-backed implementations.

// OpenDispatchRegion maps an existing region file at exactly the generated
// size. Short files fail here rather than SIGBUS later, matching the Rust
// refusal.
func OpenDispatchRegion(path string) (*DispatchBlock, error) {
	// #nosec G304 -- the region path is operator-supplied configuration
	// pointing at a pre-sized dispatch file, not request-controlled input;
	// length is verified below before mapping.
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open dispatch region %q: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat dispatch region %q: %w", path, err)
	}
	if info.Size() < int64(generated.DISPATCH_REGION_BYTES) {
		return nil, fmt.Errorf(
			"dispatch region %q holds %d bytes; %d required",
			path, info.Size(), generated.DISPATCH_REGION_BYTES,
		)
	}
	raw, err := syscall.Mmap(
		int(file.Fd()), 0,
		int(generated.DISPATCH_REGION_BYTES),
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_SHARED,
	)
	if err != nil {
		return nil, fmt.Errorf("map dispatch region %q: %w", path, err)
	}
	return &DispatchBlock{raw: raw}, nil
}

// Close unmaps the region. The underlying file stays: peers may still hold it.
func (b *DispatchBlock) Close() error {
	if b == nil || b.raw == nil {
		return nil
	}
	err := syscall.Munmap(b.raw)
	b.raw = nil
	return err
}

// dispatchWord returns the atomic view of one 8-byte word in the mapping.
//
// Same confinement rule as epochSlot: the unsafe.Pointer is unavoidable for a
// word that belongs to another process's address space as much as this one's,
// alignment is asserted rather than assumed, and bounds are checked first.
func (b *DispatchBlock) dispatchWord(offset int) (*uint64, error) {
	if offset+8 > len(b.raw) {
		return nil, fmt.Errorf("dispatch word at %d runs past the %d byte region", offset, len(b.raw))
	}
	pointer := unsafe.Pointer(&b.raw[offset]) // #nosec G103 -- page-aligned mmap offset conversion verified by bounds and alignment checks.
	if uintptr(pointer)%8 != 0 {
		return nil, fmt.Errorf("dispatch word at %d is not 8-byte aligned", offset)
	}
	return (*uint64)(pointer), nil
}

func (b *DispatchBlock) dispatchWord32(offset int) (*uint32, error) {
	if offset+4 > len(b.raw) {
		return nil, fmt.Errorf("dispatch word at %d runs past the %d byte region", offset, len(b.raw))
	}
	pointer := unsafe.Pointer(&b.raw[offset]) // #nosec G103 -- page-aligned mmap offset conversion verified by bounds and alignment checks.
	if uintptr(pointer)%4 != 0 {
		return nil, fmt.Errorf("dispatch word at %d is not 4-byte aligned", offset)
	}
	return (*uint32)(pointer), nil
}

// FlipIndex reads the active-buffer selector.
func (b *DispatchBlock) FlipIndex() (uint32, error) {
	flip, err := b.dispatchWord32(int(generated.DISPATCH_FLIP_INDEX_OFFSET))
	if err != nil {
		return 0, err
	}
	return atomic.LoadUint32(flip), nil
}

// TickNow reads the global click.
func (b *DispatchBlock) TickNow() (uint64, error) {
	tick, err := b.dispatchWord(int(generated.DISPATCH_TICK_OFFSET))
	if err != nil {
		return 0, err
	}
	return atomic.LoadUint64(tick), nil
}

// AdvanceTick issues one global click and returns the previous value.
func (b *DispatchBlock) AdvanceTick() (uint64, error) {
	tick, err := b.dispatchWord(int(generated.DISPATCH_TICK_OFFSET))
	if err != nil {
		return 0, err
	}
	return atomic.AddUint64(tick, 1) - 1, nil
}

// Stat-row field offsets inside one 64-byte slot.
const (
	statInflightOffset = 8
	statMaxConcurrency = 12
	statLastTickSeen   = 16
)

// Single-writer discipline for DispatchStatRow applies exactly as in Rust:
// only the owning executor writes its row; hosts writing mirror rows are that
// row's writer.

func (b *DispatchBlock) statRowBase(lane int) (int, error) {
	if lane < 0 || lane >= int(generated.DISPATCH_MAX_LANES) {
		return 0, fmt.Errorf("lane %d sits outside the %d-lane table", lane, generated.DISPATCH_MAX_LANES)
	}
	return int(generated.DISPATCH_STATS_OFFSET) + lane*dispatchSlotBytes, nil
}

// StatRow returns the handle for one lane's statistics row.
func (b *DispatchBlock) StatRow(lane int) (*DispatchStatRow, error) {
	base, err := b.statRowBase(lane)
	if err != nil {
		return nil, err
	}
	return &DispatchStatRow{block: b, base: base}, nil
}

func (s *DispatchStatRow) ewma() (*uint64, error) { return s.block.dispatchWord(s.base) }
func (s *DispatchStatRow) inflight() (*uint32, error) {
	return s.block.dispatchWord32(s.base + statInflightOffset)
}
func (s *DispatchStatRow) maxConcurrency() (*uint32, error) {
	return s.block.dispatchWord32(s.base + statMaxConcurrency)
}
func (s *DispatchStatRow) lastTickSeen() (*uint64, error) {
	return s.block.dispatchWord(s.base + statLastTickSeen)
}

// Claim marks one unit of work in flight and returns the new count.
func (s *DispatchStatRow) Claim() (uint32, error) {
	slot, err := s.inflight()
	if err != nil {
		return 0, err
	}
	return atomic.AddUint32(slot, 1), nil
}

// dispatchReleaseMaxAttempts bounds ReleaseOne's compare-and-swap loop.
//
// CP-02 requires the bound; the value is set against measurement rather than
// taste. At six concurrent releasers driving one row the loop averages about
// 2.4 turns, so eight clears the observed distribution with margin. Exhaustion
// at this bound is therefore evidence that the row's single-writer discipline
// has been broken, not evidence that eight was too small — raising it would
// hide the signal rather than fix anything.
const dispatchReleaseMaxAttempts = 8

// ReleaseOne clears one in-flight unit, refusing to wrap below zero.
//
// There are three outcomes and callers must not collapse them:
//
//   - (true, nil): one unit was released.
//   - (false, nil): the row was already at zero, so there was nothing to
//     release. An unbalanced release, refused rather than wrapping the counter
//     up to four billion.
//   - (false, ErrDispatchLaneContended): the retry budget ran out with the row
//     still non-zero. The unit remains counted in flight and the caller still
//     owns it.
//
// The third outcome is why the second cannot simply be reused for it. A caller
// that treats "did not release" as "nothing to release" leaks a phantom
// in-flight unit permanently, and because MaxConcurrency gates placement, a
// lane accumulating phantom units eventually stops being offered work at all —
// a availability failure that presents as a scheduling mystery rather than as
// an error.
//
// The Rust counterpart, StatRowHandle::release_one in ovrt-dispatch, uses
// fetch_update and so retries without a bound: it has no third outcome. The
// divergence is deliberate rather than an oversight — CP-02 requires the bound
// on this side — and the two agree on every outcome reachable while the
// single-writer discipline holds.
func (s *DispatchStatRow) ReleaseOne() (bool, error) {
	// Resolved once: the slot address is a fixed offset into the mapping and
	// cannot change between attempts, so re-resolving it per turn was pure work
	// inside the contended loop.
	slot, err := s.inflight()
	if err != nil {
		return false, err
	}
	for range dispatchReleaseMaxAttempts {
		current := atomic.LoadUint32(slot)
		if current == 0 {
			return false, nil
		}
		if atomic.CompareAndSwapUint32(slot, current, current-1) {
			return true, nil
		}
	}
	return false, ErrDispatchLaneContended
}

// RecordCompletion blends one latency sample into the EWMA and stamps the
// heartbeat with the given click.
func (s *DispatchStatRow) RecordCompletion(sampleNs uint64, tickNow uint64) (uint64, error) {
	slot, err := s.ewma()
	if err != nil {
		return 0, err
	}
	blended := BlendEwma(atomic.LoadUint64(slot), sampleNs)
	atomic.StoreUint64(slot, blended)
	if seen, err := s.lastTickSeen(); err == nil {
		atomic.StoreUint64(seen, tickNow)
	}
	return blended, nil
}

// Heartbeat refreshes liveness without touching the latency estimate.
func (s *DispatchStatRow) Heartbeat(tickNow uint64) error {
	seen, err := s.lastTickSeen()
	if err != nil {
		return err
	}
	atomic.StoreUint64(seen, tickNow)
	return nil
}

// ApplyMirror overwrites the row with remotely reported statistics. Only the
// process that owns this mirror row locally may call it.
func (s *DispatchStatRow) ApplyMirror(stats DispatchLaneStats) error {
	ewmaSlot, err := s.ewma()
	if err != nil {
		return err
	}
	inflightSlot, err := s.inflight()
	if err != nil {
		return err
	}
	maxSlot, err := s.maxConcurrency()
	if err != nil {
		return err
	}
	seenSlot, err := s.lastTickSeen()
	if err != nil {
		return err
	}
	atomic.StoreUint64(ewmaSlot, stats.EwmaNs)
	atomic.StoreUint32(inflightSlot, stats.Inflight)
	atomic.StoreUint32(maxSlot, stats.MaxConcurrency)
	atomic.StoreUint64(seenSlot, stats.LastTickSeen)
	return nil
}

// SnapshotStatRow reads one row atomically word-by-word.
func (b *DispatchBlock) SnapshotStatRow(lane int) (DispatchLaneStats, error) {
	row, err := b.StatRow(lane)
	if err != nil {
		return DispatchLaneStats{}, err
	}
	return row.Snapshot()
}

// SnapshotStats reads every lane's statistics in one pass without
// materializing per-row handles.
//
// The per-row handle API pays one escaped allocation per lane because each
// call wraps a new struct; placement sweeps run once per decision, so this
// batch form reads the same words directly and keeps the sweep at a single
// slice allocation. Semantics are identical: every field is an atomic acquire
// load from the same fixed offsets.
func (b *DispatchBlock) SnapshotStats() ([]DispatchLaneStats, error) {
	out := make([]DispatchLaneStats, generated.DISPATCH_MAX_LANES)
	for lane := range out {
		base := int(generated.DISPATCH_STATS_OFFSET) + lane*int(generated.DISPATCH_LANE_ROW_BYTES)
		ewma, err := b.dispatchWord(base)
		if err != nil {
			return nil, err
		}
		inflight, err := b.dispatchWord32(base + statInflightOffset)
		if err != nil {
			return nil, err
		}
		maxConcurrency, err := b.dispatchWord32(base + statMaxConcurrency)
		if err != nil {
			return nil, err
		}
		lastSeen, err := b.dispatchWord(base + statLastTickSeen)
		if err != nil {
			return nil, err
		}
		out[lane] = DispatchLaneStats{
			EwmaNs:         atomic.LoadUint64(ewma),
			Inflight:       atomic.LoadUint32(inflight),
			MaxConcurrency: atomic.LoadUint32(maxConcurrency),
			LastTickSeen:   atomic.LoadUint64(lastSeen),
		}
	}
	return out, nil
}

// Snapshot reads the row with acquire semantics per word.
func (s *DispatchStatRow) Snapshot() (DispatchLaneStats, error) {
	var stats DispatchLaneStats
	ewmaSlot, err := s.ewma()
	if err != nil {
		return stats, err
	}
	inflightSlot, err := s.inflight()
	if err != nil {
		return stats, err
	}
	maxSlot, err := s.maxConcurrency()
	if err != nil {
		return stats, err
	}
	seenSlot, err := s.lastTickSeen()
	if err != nil {
		return stats, err
	}
	stats.EwmaNs = atomic.LoadUint64(ewmaSlot)
	stats.Inflight = atomic.LoadUint32(inflightSlot)
	stats.MaxConcurrency = atomic.LoadUint32(maxSlot)
	stats.LastTickSeen = atomic.LoadUint64(seenSlot)
	return stats, nil
}

// PublishDescriptors publishes a full descriptor table and returns the buffer
// index now active.
//
// Rows are positional: slot i serves lane id i. Unlisted slots become retired
// rows whose empty class mask makes them ineligible. Every slot of the
// inactive buffer is written before the flip store, so no reader can observe
// a half-written table.
func (b *DispatchBlock) PublishDescriptors(rows []DispatchLaneDescriptor, generation uint32) (uint32, error) {
	if len(rows) > int(generated.DISPATCH_MAX_LANES) {
		return 0, fmt.Errorf("%d rows exceed the %d-lane table", len(rows), generated.DISPATCH_MAX_LANES)
	}
	active, err := b.FlipIndex()
	if err != nil {
		return 0, err
	}
	target := 1 - active
	base := int(generated.DISPATCH_BUFFERS_OFFSET) + int(target)*int(generated.DISPATCH_BUFFER_BYTES)
	for slot := range int(generated.DISPATCH_MAX_LANES) {
		descriptor := DispatchLaneDescriptor{
			LaneID:       uint16(slot),
			Jurisdiction: uint16(generated.DISPATCH_JURISDICTION_GLOBAL),
			Generation:   generation,
		}
		if slot < len(rows) {
			descriptor = rows[slot]
			descriptor.LaneID = uint16(slot)
			descriptor.Generation = generation
		}
		encoded := encodeDescriptor(descriptor)
		copy(b.raw[base+slot*dispatchSlotBytes:], encoded)
	}
	flip, err := b.dispatchWord32(int(generated.DISPATCH_FLIP_INDEX_OFFSET))
	if err != nil {
		return 0, err
	}
	atomic.StoreUint32(flip, target)
	return target, nil
}

// SnapshotDescriptors decodes the descriptor table currently published.
//
// Rows are plain reads taken after the flip index Acquire load, which orders
// them after every write of that generation.
func (b *DispatchBlock) SnapshotDescriptors() ([]DispatchLaneDescriptor, error) {
	active, err := b.FlipIndex()
	if err != nil {
		return nil, err
	}
	if active >= 2 {
		return nil, fmt.Errorf("flip index %d selects no buffer", active)
	}
	base := int(generated.DISPATCH_BUFFERS_OFFSET) + int(active)*int(generated.DISPATCH_BUFFER_BYTES)
	out := make([]DispatchLaneDescriptor, generated.DISPATCH_MAX_LANES)
	for slot := range out {
		start := base + slot*dispatchSlotBytes
		end := start + dispatchSlotBytes
		if end > len(b.raw) {
			return nil, fmt.Errorf("descriptor slot %d runs past the %d byte region", slot, len(b.raw))
		}
		descriptor, err := decodeDescriptor(b.raw[start:end])
		if err != nil {
			return nil, err
		}
		out[slot] = descriptor
	}
	return out, nil
}
