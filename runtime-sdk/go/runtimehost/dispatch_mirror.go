//go:build linux || darwin

package runtimehost

import "github.com/nmxmxh/ovasabi_foundation/server-kit/go/placement"

// DispatchBlock is a placement.MirrorSink: inbound lane reports land in the
// locally owned mirror rows.
//
// Single-writer discipline survives mirroring because only this process ever
// writes its own copy of a remote lane's stats, no matter how many nodes
// publish them.
var _ placement.MirrorSink = (*DispatchBlock)(nil)

// ApplyMirrorUpdate applies one remote lane's reported statistics.
//
// Two trust rules shape this write:
//
//  1. The heartbeat is re-stamped with the LOCAL click, never the publisher's
//     tick. Cross-region clicks are independent counters — a hot peer runs
//     thousands ahead of an idle consumer, so comparing them raw would
//     wrongly mark healthy lanes stale (and a liar could inflate its tick to
//     look eternally fresh). Arrival is itself the liveness proof; freshness
//     then decays against our clock until the next report.
//  2. Latency claims below the plausibility floor are rejected outright so
//     they surface in listener errors instead of biasing argmin quietly.
//
// The update carries measurement state only. Descriptor fields on the wire
// (class mask, jurisdiction, Bloom set) describe the remote lane to hubs
// aggregating tables; applying them here would mutate local membership, which
// stays under the local publisher's exclusive control.
func (b *DispatchBlock) ApplyMirrorUpdate(update placement.LaneMirrorUpdate) error {
	if err := placement.ValidateMirrorStats(update); err != nil {
		return err
	}
	// Issuing a fresh click here (rather than reading a possibly-zero one)
	// keeps the restamped heartbeat meaningful on an otherwise idle region.
	if _, err := b.AdvanceTick(); err != nil {
		return err
	}
	localTick, err := b.TickNow()
	if err != nil {
		return err
	}
	row, err := b.StatRow(int(update.Lane))
	if err != nil {
		return err
	}
	return row.ApplyMirror(DispatchLaneStats{
		EwmaNs:         update.EwmaNs,
		Inflight:       update.Inflight,
		MaxConcurrency: update.MaxConcurrency,
		LastTickSeen:   localTick,
	})
}
