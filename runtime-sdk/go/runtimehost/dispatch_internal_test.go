//go:build linux || darwin

package runtimehost

import (
	"strings"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/placement"
)

// In-package coverage for guard branches that black-box tests cannot reach:
// short-region bounds checks and the batch stats sweep.
func TestDispatchInternalBoundsRefuseShortRegions(t *testing.T) {
	block := &DispatchBlock{raw: make([]byte, 16)} // Far below any real offset.

	if _, err := block.dispatchWord(24); err == nil {
		t.Fatal("dispatchWord past region must fail")
	}
	if _, err := block.dispatchWord(100); err == nil {
		t.Fatal("dispatchWord past region must fail")
	}
	if _, err := block.dispatchWord32(14); err == nil {
		t.Fatal("dispatchWord32 past region must fail")
	}
	if _, err := block.statRowBase(-1); err == nil {
		t.Fatal("negative lane must fail")
	}
	if _, err := block.StatRow(int(generated.DISPATCH_MAX_LANES)); err == nil {
		t.Fatal("out-of-range lane must fail")
	}
}

func TestSnapshotStatsReadsEveryLaneWithoutHandles(t *testing.T) {
	block, err := OpenDispatchRegion(tempRegion(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = block.Close() }()

	row, err := block.StatRow(5)
	if err != nil {
		t.Fatalf("row: %v", err)
	}
	if err := row.ApplyMirror(DispatchLaneStats{EwmaNs: 4_321, MaxConcurrency: 2, LastTickSeen: 9}); err != nil {
		t.Fatalf("mirror: %v", err)
	}

	stats, err := block.SnapshotStats()
	if err != nil {
		t.Fatalf("snapshot stats: %v", err)
	}
	if len(stats) != int(generated.DISPATCH_MAX_LANES) {
		t.Fatalf("len = %d want %d", len(stats), generated.DISPATCH_MAX_LANES)
	}
	if stats[5].EwmaNs != 4_321 || stats[5].MaxConcurrency != 2 || stats[5].LastTickSeen != 9 {
		t.Fatalf("lane 5 mismatch: %+v", stats[5])
	}
	if stats[0].EwmaNs != 0 {
		t.Fatalf("untouched lane should read zero: %+v", stats[0])
	}
}

func TestSaturatingSubBothBranches(t *testing.T) {
	if got := saturatingSub(10, 4); got != 6 {
		t.Fatalf("10-4 = %d want 6", got)
	}
	if got := saturatingSub(4, 10); got != 0 {
		t.Fatalf("4-10 = %d want clamped 0", got)
	}
}

// TestApplyMirrorUpdateRefusesOutOfRangeLane covers the StatRow error arm of
// the sink adapter.
func TestApplyMirrorUpdateRefusesOutOfRangeLane(t *testing.T) {
	block, err := OpenDispatchRegion(tempRegion(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = block.Close() }()

	err = block.ApplyMirrorUpdate(placement.LaneMirrorUpdate{
		Lane: uint16(generated.DISPATCH_MAX_LANES + 3), EwmaNs: 100, TickSeen: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("err = %v want lane-range refusal", err)
	}
}

func TestSnapshotStatsRefusesShortRegions(t *testing.T) {
	block := &DispatchBlock{raw: make([]byte, 16)}
	if _, err := block.SnapshotStats(); err == nil || !strings.Contains(err.Error(), "past the") {
		t.Fatalf("err = %v want bounds refusal", err)
	}
}

func TestDecideClampsOversizedLaneTables(t *testing.T) {
	descriptors := make([]DispatchLaneDescriptor, generated.DISPATCH_MAX_LANES+5)
	stats := make([]DispatchLaneStats, generated.DISPATCH_MAX_LANES+5)
	for i := range descriptors {
		descriptors[i] = DispatchLaneDescriptor{
			LaneID: uint16(i), UnitClassMask: 0b01,
			AffinityBloom: 1 << (uint64(i) % 64),
		}
		stats[i] = DispatchLaneStats{EwmaNs: uint64(100 + i), LastTickSeen: 50}
	}
	request := DispatchRequest{RequiredClassMask: 0b01, DeadlineNs: ^uint64(0)}
	got, ok := Decide(51, descriptors, stats, request)
	if !ok || got != 0 {
		t.Fatalf("clamp walk = %d,%v want lane 0,true", got, ok)
	}
}

func TestDecideSkipsRetiredSlotUnderClamp(t *testing.T) {
	count := generated.DISPATCH_MAX_LANES
	descriptors := make([]DispatchLaneDescriptor, count)
	stats := make([]DispatchLaneStats, count)
	descriptors[count-1] = DispatchLaneDescriptor{LaneID: uint16(count - 1)} // retired slot
	if got, ok := Decide(10, descriptors, stats,
		DispatchRequest{RequiredClassMask: 0, DeadlineNs: ^uint64(0)}); ok || got != 0 {
		t.Fatalf("retired-under-clamp = %d,%v", got, ok)
	}
}
