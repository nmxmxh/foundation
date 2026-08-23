//go:build linux || darwin

package runtimehost

import (
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/placement"
	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

// TestDispatchBlockMirrorsRemoteLanesOverTheBus proves the full Phase 3
// path: a peer publishes lane reports on the transport bus, this process's
// listener applies them into locally owned mirror rows, and placement reads
// them like any other measurement.
func TestDispatchBlockMirrorsRemoteLanesOverTheBus(t *testing.T) {
	block, err := OpenDispatchRegion(tempRegion(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = block.Close() }()

	bus := rediskit.NewMemoryClient("mirror")
	ctx := t.Context()
	if err := placement.ListenMirrors(ctx, bus, "", block, nil); err != nil {
		t.Fatalf("listen: %v", err)
	}

	want := []placement.LaneMirrorUpdate{
		{Lane: 4, Jurisdiction: 7, MaxConcurrency: 8, Inflight: 3,
			Class: placement.LaneClassEdge, UnitClassMask: 0b01, AffinityBloom: 1 << 11,
			EwmaNs: 12_500, TickSeen: 900},
		{Lane: 5, EwmaNs: 30_000, TickSeen: 901},
	}
	if err := placement.PublishLaneMirrors(ctx, bus, "", "edge-9", "eu-central", placement.LaneClassEdge, want); err != nil {
		t.Fatalf("publish: %v", err)
	}

	waitForMirrorApply(t, block, 2)

	now := mustTick(t, block)
	mirrored, err := block.SnapshotStatRow(4)
	if err != nil {
		t.Fatalf("row 4: %v", err)
	}
	if mirrored.EwmaNs != 12_500 || mirrored.Inflight != 3 {
		t.Fatalf("row 4 measurement mismatch: %+v", mirrored)
	}
	// The publisher's tick (900) must be ignored: arrival re-stamps with the
	// local click, the only clock this table's freshness compares against.
	if mirrored.LastTickSeen == 900 || mirrored.LastTickSeen < 1 || mirrored.LastTickSeen > now {
		t.Fatalf("row 4 stamp %d not locally issued (now=%d)", mirrored.LastTickSeen, now)
	}
	fifth, err := block.SnapshotStatRow(5)
	if err != nil {
		t.Fatalf("row 5: %v", err)
	}
	if fifth.EwmaNs != 30_000 {
		t.Fatalf("row 5 measurement mismatch: %+v", fifth)
	}
	if fifth.LastTickSeen < 1 || fifth.LastTickSeen > now {
		t.Fatalf("row 5 stamp %d not locally issued (now=%d)", fifth.LastTickSeen, now)
	}

	// Mirror application must not touch membership: descriptors stay as the
	// local publisher wrote them (all retired here).
	descriptors, err := block.SnapshotDescriptors()
	if err != nil {
		t.Fatalf("descriptors: %v", err)
	}
	for slot, descriptor := range descriptors {
		if descriptor.UnitClassMask != 0 {
			t.Fatalf("slot %d mutated by mirror traffic: %+v", slot, descriptor)
		}
	}
}

// TestDispatchTickIsMonotonicAcrossAdvances pins the global-click contract
// for Go hosts. (Conformance anchor: DispatchPlacement/TickMonotonic.)
func TestDispatchTickIsMonotonicAcrossAdvances(t *testing.T) {
	block, err := OpenDispatchRegion(tempRegion(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = block.Close() }()

	previous, err := block.TickNow()
	if err != nil {
		t.Fatalf("read tick: %v", err)
	}
	for range 64 {
		returned, err := block.AdvanceTick()
		if err != nil {
			t.Fatalf("advance: %v", err)
		}
		current, err := block.TickNow()
		if err != nil {
			t.Fatalf("read tick: %v", err)
		}
		if returned+1 != current || current <= previous {
			t.Fatalf("tick broke monotonicity: returned=%d current=%d previous=%d", returned, current, previous)
		}
		previous = current
	}
}

// TestApplyMirrorRestampsHeartbeatWithLocalClick pins the clock-isolation
// guard: a publisher's own tick count is never trusted, because a hot peer
// legitimately runs thousands of clicks ahead of an idle consumer and a liar
// could inflate its tick to look eternally fresh. Arrival re-stamps with the
// locally issued click; freshness then decays on our clock alone.
func TestApplyMirrorRestampsHeartbeatWithLocalClick(t *testing.T) {
	block, err := OpenDispatchRegion(tempRegion(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = block.Close() }()

	// A peer claiming to be ancient-but-active, stamped far beyond any local
	// click this idle region has issued.
	update := placement.LaneMirrorUpdate{
		Lane: 2, EwmaNs: 5_000, MaxConcurrency: 4, TickSeen: ^uint64(0) - 10,
	}
	if err := block.ApplyMirrorUpdate(update); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Membership is local-only: give slot 2 a real descriptor so selection
	// can consider the mirrored measurement.
	local := DispatchLaneDescriptor{LaneID: 2, UnitClassMask: 0b01, MaxConcurrency: 4, Generation: 1}
	if _, err := block.PublishDescriptors([]DispatchLaneDescriptor{{}, {}, local}, 1); err != nil {
		t.Fatalf("publish: %v", err)
	}

	stats, err := block.SnapshotStatRow(2)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	now, err := block.TickNow()
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if stats.LastTickSeen != now || stats.LastTickSeen > now {
		t.Fatalf("stamp = %d want local click %d", stats.LastTickSeen, now)
	}
	if stats.EwmaNs != 5_000 {
		t.Fatalf("measurement lost: %+v", stats)
	}

	// And freshness must hold against our clock, not the publisher's.
	descriptors, err := block.SnapshotDescriptors()
	if err != nil {
		t.Fatalf("descriptors: %v", err)
	}
	table := make([]DispatchLaneStats, generated.DISPATCH_MAX_LANES)
	for laneIdx := range table {
		row, err := block.StatRow(laneIdx)
		if err != nil {
			t.Fatalf("row %d: %v", laneIdx, err)
		}
		if table[laneIdx], err = row.Snapshot(); err != nil {
			t.Fatalf("row %d snapshot: %v", laneIdx, err)
		}
	}
	got, ok := Decide(now, descriptors, table, DispatchRequest{RequiredClassMask: 0b01, DeadlineNs: ^uint64(0)})
	if !ok || got != 2 {
		t.Fatalf("fresh mirrored lane = %d,%v want 2,true", got, ok)
	}
}

// TestApplyMirrorRejectsImplausibleLatencyClaims pins the EWMA floor: a
// claimed mean below what one dispatch decision costs is a lie about physics.
// It must be refused loudly (error to the listener callback), leaving the row
// untouched rather than quietly poisoning argmin.
func TestApplyMirrorRejectsImplausibleLatencyClaims(t *testing.T) {
	block, err := OpenDispatchRegion(tempRegion(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = block.Close() }()

	liar := placement.LaneMirrorUpdate{Lane: 1, EwmaNs: placement.MinPlausibleEwmaNs - 1, TickSeen: 5}
	if err := block.ApplyMirrorUpdate(liar); err == nil {
		t.Fatal("sub-floor latency claim must be refused")
	}

	stats, err := block.SnapshotStatRow(1)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if stats.EwmaNs != 0 || stats.LastTickSeen != 0 {
		t.Fatalf("refused update leaked into the row: %+v", stats)
	}

	// Zero stays legal: it truthfully reports an unsampled lane.
	unsampled := placement.LaneMirrorUpdate{Lane: 1, EwmaNs: 0, TickSeen: 6}
	if err := block.ApplyMirrorUpdate(unsampled); err != nil {
		t.Fatalf("unsampled report refused: %v", err)
	}
	stats, err = block.SnapshotStatRow(1)
	if err != nil {
		t.Fatalf("post-unsampled snapshot: %v", err)
	}
	if stats.LastTickSeen == 0 {
		t.Fatal("arrival must still stamp liveness")
	}
}

func waitForMirrorApply(t *testing.T, block *DispatchBlock, wantRows int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		applied := 0
		for lane := range int(generated.DISPATCH_MAX_LANES) {
			stats, err := block.SnapshotStatRow(lane)
			if err == nil && stats.EwmaNs > 0 {
				applied++
			}
		}
		if applied >= wantRows {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("mirror updates never reached the region")
}
