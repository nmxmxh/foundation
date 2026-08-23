//go:build linux || darwin

package runtimehost

import (
	"testing"
)

// BenchmarkPlacementFullHostPath is what a Go host pays per placement
// decision today: tick advance, fresh 32-row descriptor snapshot, stats sweep,
// argmin. The cached-tables floor lives in the Rust ns_bench; this number is
// the honest uncached Go figure.
func BenchmarkPlacementFullHostPath(b *testing.B) {
	block, err := OpenDispatchRegion(tempRegion(b))
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer func() { _ = block.Close() }()

	rows := make([]DispatchLaneDescriptor, 8)
	for slot := range rows {
		rows[slot] = DispatchLaneDescriptor{
			LaneID: uint16(slot), MaxConcurrency: 8,
			Generation: 1, UnitClassMask: 0b01,
			AffinityBloom: 1 << (uint64(slot) % 64),
		}
	}
	if _, err := block.PublishDescriptors(rows, 1); err != nil {
		b.Fatalf("publish: %v", err)
	}
	if _, err := block.AdvanceTick(); err != nil {
		b.Fatalf("seed tick: %v", err)
	}
	seedTick, err := block.TickNow()
	if err != nil {
		b.Fatalf("read tick: %v", err)
	}
	for lane := range 8 {
		row, err := block.StatRow(lane)
		if err != nil {
			b.Fatalf("row %d: %v", lane, err)
		}
		// Stamp with the post-advance click: fetch_add hands back the
		// previous value, and a zero heartbeat reads as never-checked-in.
		if _, err := row.RecordCompletion(1_000+uint64(lane)*100, seedTick); err != nil {
			b.Fatalf("sample %d: %v", lane, err)
		}
	}

	request := DispatchRequest{RequiredClassMask: 0b01, DeadlineNs: ^uint64(0)}
	var sink uint16
	var sunk bool
	for b.Loop() {
		now, err := block.TickNow()
		if err != nil {
			b.Fatalf("tick: %v", err)
		}
		descriptors, err := block.SnapshotDescriptors()
		if err != nil {
			b.Fatalf("snapshot: %v", err)
		}
		table, err := block.SnapshotStats()
		if err != nil {
			b.Fatalf("stats: %v", err)
		}
		sink, sunk = Decide(now, descriptors, table, request)
	}
	if !sunk {
		b.Fatalf("decision lost: %d,%v", sink, sunk)
	}
}
