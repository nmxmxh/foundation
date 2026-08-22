//go:build linux || darwin

package runtimehost

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

// tempRegion creates a zeroed region file sized by the generated constant.
// Accepts testing.TB so benchmarks share the helper (TE-42 build-tag file).
func tempRegion(tb testing.TB) string {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), "dispatch-region")
	writeZeroedRegionSize(path, int(generated.DISPATCH_REGION_BYTES))
	return path
}

func TestOpenDispatchRegionRefusesShortFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "short")
	if err := writeZeroedRegionSize(path, 128); err != nil {
		t.Fatalf("shrink: %v", err)
	}
	if _, err := OpenDispatchRegion(path); err == nil {
		t.Fatal("short region must be refused")
	}
}

func TestDispatchBlockPublishSnapshotAndTick(t *testing.T) {
	block, err := OpenDispatchRegion(tempRegion(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = block.Close() }()

	first, err := block.PublishDescriptors([]DispatchLaneDescriptor{
		{Jurisdiction: 7, MaxConcurrency: 4, Generation: 1, UnitClassMask: 0b01, AffinityBloom: 1 << 3},
	}, 1)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if first != 1 {
		t.Fatalf("first publication targets buffer 1, got %d", first)
	}

	descriptors, err := block.SnapshotDescriptors()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if descriptors[0].LaneID != 0 || descriptors[0].Jurisdiction != 7 || descriptors[0].Generation != 1 {
		t.Fatalf("slot 0 mismatch: %+v", descriptors[0])
	}
	if descriptors[5].UnitClassMask != 0 {
		t.Fatalf("unlisted slot must retire: %+v", descriptors[5])
	}

	second, err := block.PublishDescriptors(nil, 2)
	if err != nil {
		t.Fatalf("republish: %v", err)
	}
	if second == first {
		t.Fatal("generations must alternate buffers")
	}
	if _, err := block.AdvanceTick(); err != nil {
		t.Fatalf("advance: %v", err)
	}
	now, err := block.TickNow()
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if now != 1 {
		t.Fatalf("tick = %d want 1", now)
	}
}

func TestDispatchStatRowSamplingAndBalance(t *testing.T) {
	block, err := OpenDispatchRegion(tempRegion(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = block.Close() }()

	row, err := block.StatRow(3)
	if err != nil {
		t.Fatalf("stat row: %v", err)
	}
	if err := row.ApplyMirror(DispatchLaneStats{EwmaNs: 8_000, MaxConcurrency: 4}); err != nil {
		t.Fatalf("mirror: %v", err)
	}
	if claimed, err := row.Claim(); err != nil || claimed != 1 {
		t.Fatalf("claim = %d,%v", claimed, err)
	}
	if claimed, err := row.Claim(); err != nil || claimed != 2 {
		t.Fatalf("second claim = %d,%v", claimed, err)
	}

	tick, err := block.AdvanceTick()
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	blended, err := row.RecordCompletion(16_000, tick+1)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	// (1·16_000 + 7·8_000) / 8 = 9_000.
	if blended != 9_000 {
		t.Fatalf("blend = %d want 9000", blended)
	}

	for i := 0; i < 2; i++ {
		ok, err := row.ReleaseOne()
		if err != nil || !ok {
			t.Fatalf("release %d = %v,%v", i, ok, err)
		}
	}
	if ok, _ := row.ReleaseOne(); ok {
		t.Fatal("release below zero must be refused")
	}
	snapshot, err := row.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.Inflight != 0 || snapshot.LastTickSeen != tick+1 {
		t.Fatalf("snapshot mismatch: %+v", snapshot)
	}
}

func TestDispatchEndToEndDecisionThroughRealBytes(t *testing.T) {
	block, err := OpenDispatchRegion(tempRegion(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = block.Close() }()

	local := DispatchLaneDescriptor{Jurisdiction: 7, MaxConcurrency: 4, UnitClassMask: 0b01, AffinityBloom: 1 << 3}
	global := DispatchLaneDescriptor{MaxConcurrency: 4, UnitClassMask: 0b01}
	if _, err := block.PublishDescriptors([]DispatchLaneDescriptor{local, global}, 1); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Sample both lanes once so neither looks unsampled.
	localRow, err := block.StatRow(0)
	if err != nil {
		t.Fatalf("row 0: %v", err)
	}
	globalRow, err := block.StatRow(1)
	if err != nil {
		t.Fatalf("row 1: %v", err)
	}
	if _, err := block.AdvanceTick(); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if _, err := localRow.RecordCompletion(5_000, mustTick(t, block)); err != nil {
		t.Fatalf("sample local: %v", err)
	}
	if _, err := block.AdvanceTick(); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if _, err := globalRow.RecordCompletion(6_000, mustTick(t, block)); err != nil {
		t.Fatalf("sample global: %v", err)
	}

	request := DispatchRequest{RequiredClassMask: 0b01, Jurisdiction: 7, DeadlineNs: ^uint64(0), AffinityKey: 9}
	descriptors, err := block.SnapshotDescriptors()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	table := make([]DispatchLaneStats, generated.DISPATCH_MAX_LANES)
	for lane := range table {
		stats, err := block.StatRow(lane)
		if err != nil {
			t.Fatalf("row %d: %v", lane, err)
		}
		if table[lane], err = stats.Snapshot(); err != nil {
			t.Fatalf("row %d snapshot: %v", lane, err)
		}
	}

	got, ok := Decide(mustTick(t, block), descriptors, table, request)
	if !ok || got != 0 {
		t.Fatalf("decide = %d,%v want 0,true", got, ok)
	}

	// Queue pressure flips the choice through real region bytes. The stats
	// table must be re-read after claiming: placement reads the region, not
	// a caller's stale copy.
	localRow2, err := block.StatRow(0)
	if err != nil {
		t.Fatalf("row 0: %v", err)
	}
	for i := 0; i < 4; i++ {
		if _, err := localRow2.Claim(); err != nil {
			t.Fatalf("claim: %v", err)
		}
	}
	for lane := range table {
		stats, err := block.StatRow(lane)
		if err != nil {
			t.Fatalf("row %d: %v", lane, err)
		}
		if table[lane], err = stats.Snapshot(); err != nil {
			t.Fatalf("row %d snapshot: %v", lane, err)
		}
	}
	if got, ok := Decide(mustTick(t, block), descriptors, table, request); !ok || got != 1 {
		t.Fatalf("pressured decide = %d,%v want 1,true", got, ok)
	}

	// Age the clock past the freshness window. Both sampled heartbeats are
	// now ancient; only the peer gets refreshed, so selection must settle on
	// it once the table is re-read from the region.
	for i := 0; i < int(generated.DISPATCH_STALE_TICKS)+2; i++ {
		if _, err := block.AdvanceTick(); err != nil {
			t.Fatalf("age clock: %v", err)
		}
	}
	now := mustTick(t, block)
	if err := globalRow.Heartbeat(now); err != nil {
		t.Fatalf("heartbeat peer: %v", err)
	}
	for lane := range table {
		stats, err := block.StatRow(lane)
		if err != nil {
			t.Fatalf("row %d: %v", lane, err)
		}
		if table[lane], err = stats.Snapshot(); err != nil {
			t.Fatalf("row %d snapshot: %v", lane, err)
		}
	}
	if got, ok := Decide(now, descriptors, table, request); !ok || got != 1 {
		t.Fatalf("stale-filtered decide = %d,%v want 1,true", got, ok)
	}
}

func mustTick(t *testing.T, block *DispatchBlock) uint64 {
	t.Helper()
	tick, err := block.TickNow()
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	return tick
}

// writeZeroedRegionSize creates a region file of the exact size on disk; the
// mmap-backed OpenDispatchRegion refuses shorter files, so tests size before
// opening. Unix-only: its only callers exercise the mapped implementation.
func writeZeroedRegionSize(path string, size int) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return file.Truncate(int64(size))
}
