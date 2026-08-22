package runtimehost

import (
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

// Golden vectors mirrored from ovrt-dispatch's Rust decide tests. Both
// runtimes must answer identically for identical tables; a divergence here is
// a contract break, not a port bug.
func TestDecideParityVectors(t *testing.T) {
	tick := uint64(100)

	lane := func(id uint16) DispatchLaneDescriptor {
		return DispatchLaneDescriptor{
			LaneID:         id,
			Jurisdiction:   0,
			MaxConcurrency: 4,
			Generation:     1,
			UnitClassMask:  0b11,
		}
	}
	stats := func(ewma uint64) DispatchLaneStats {
		return DispatchLaneStats{EwmaNs: ewma, LastTickSeen: tick}
	}
	request := func(deadline uint64) DispatchRequest {
		return DispatchRequest{
			RequiredClassMask: 0b01,
			Jurisdiction:      7,
			DeadlineNs:        deadline,
		}
	}

	t.Run("picks the only lane covering the request", func(t *testing.T) {
		got, ok := Decide(tick, []DispatchLaneDescriptor{lane(1), lane(2)},
			[]DispatchLaneStats{stats(5_000), stats(9_000)}, request(1_000_000))
		if !ok || got != 1 {
			t.Fatalf("got %d,%v want 1,true", got, ok)
		}
	})

	t.Run("queue pressure flips the choice", func(t *testing.T) {
		busy := stats(5_000)
		busy.Inflight = 8 // 2x overload on max 4 → expected 15_000.
		got, ok := Decide(tick, []DispatchLaneDescriptor{lane(1), lane(2)},
			[]DispatchLaneStats{busy, stats(10_000)}, request(1_000_000))
		if !ok || got != 2 {
			t.Fatalf("got %d,%v want 2,true", got, ok)
		}
	})

	t.Run("mismatched jurisdiction is never served", func(t *testing.T) {
		restricted := lane(1)
		restricted.Jurisdiction = 9
		got, ok := Decide(tick, []DispatchLaneDescriptor{restricted},
			[]DispatchLaneStats{stats(1_000)}, request(1_000_000))
		if ok {
			t.Fatalf("got %d,%v want none", got, ok)
		}
	})

	t.Run("global lanes serve any jurisdiction", func(t *testing.T) {
		got, ok := Decide(tick, []DispatchLaneDescriptor{lane(1)},
			[]DispatchLaneStats{stats(1_000)}, request(1_000_000))
		if !ok || got != 1 {
			t.Fatalf("got %d,%v want 1,true", got, ok)
		}
	})

	t.Run("missing required classes select nothing", func(t *testing.T) {
		narrow := lane(1)
		narrow.UnitClassMask = 0b100
		if _, ok := Decide(tick, []DispatchLaneDescriptor{narrow},
			[]DispatchLaneStats{stats(1_000)}, request(1_000_000)); ok {
			t.Fatal("narrow lane must not serve")
		}
	})

	t.Run("stale heartbeats are excluded", func(t *testing.T) {
		old := stats(1_000)
		old.LastTickSeen = tick - uint64(generated.DISPATCH_STALE_TICKS) - 1
		if _, ok := Decide(tick, []DispatchLaneDescriptor{lane(1)},
			[]DispatchLaneStats{old}, request(1_000_000)); ok {
			t.Fatal("stale lane must be excluded")
		}
	})

	t.Run("deadline feasibility gates selection", func(t *testing.T) {
		if _, ok := Decide(tick, []DispatchLaneDescriptor{lane(1)},
			[]DispatchLaneStats{stats(2_000)}, request(999)); ok {
			t.Fatal("infeasible deadline must select nothing")
		}
	})

	t.Run("unsampled lanes do not look free", func(t *testing.T) {
		if _, ok := Decide(tick, []DispatchLaneDescriptor{lane(1)},
			[]DispatchLaneStats{stats(0)}, request(1_000_000)); ok {
			t.Fatal("unsampled lane must not look free")
		}
	})

	t.Run("retired slots never serve unconstrained requests", func(t *testing.T) {
		retired := lane(1)
		retired.UnitClassMask = 0
		unconstrained := DispatchRequest{Jurisdiction: 7, DeadlineNs: ^uint64(0)}
		if _, ok := Decide(tick, []DispatchLaneDescriptor{retired},
			[]DispatchLaneStats{stats(1_000)}, unconstrained); ok {
			t.Fatal("retired slot must never serve")
		}
	})

	t.Run("locality bonus breaks near ties", func(t *testing.T) {
		local := lane(1)
		local.AffinityBloom = 1 << 3 // Request key 3 hits bit 3.
		remote := lane(2)
		table := []DispatchLaneStats{stats(6_000), stats(5_500)}
		got, ok := Decide(tick, []DispatchLaneDescriptor{local, remote}, table, func() DispatchRequest {
			req := request(1_000_000)
			req.AffinityKey = 3
			return req
		}())
		if !ok || got != 1 {
			t.Fatalf("got %d,%v want 1,true", got, ok)
		}
	})

	t.Run("ties keep the lower lane id", func(t *testing.T) {
		second := lane(2)
		second.UnitClassMask = 0b01
		got, ok := Decide(tick, []DispatchLaneDescriptor{lane(1), second},
			[]DispatchLaneStats{stats(4_000), stats(4_000)}, request(1_000_000))
		if !ok || got != 1 {
			t.Fatalf("got %d,%v want 1,true", got, ok)
		}
	})
}

// TestDispatchJurisdictionFailsClosedBeforeScoring pins the fail-closed
// pre-filter: an unknown or mismatched jurisdiction selects nothing, even
// when every other property of the lane is the best on the table.
// (Conformance anchor: DispatchPlacement/JurisdictionFailClosed.)
func TestDispatchJurisdictionFailsClosedBeforeScoring(t *testing.T) {
	tick := uint64(10)
	fastest := DispatchLaneDescriptor{
		LaneID:        1,
		Jurisdiction:  9, // Request asks for 7: never serves.
		UnitClassMask: 0b01,
	}
	table := []DispatchLaneStats{{EwmaNs: 1, LastTickSeen: tick}}
	request := DispatchRequest{RequiredClassMask: 0b01, Jurisdiction: 7, DeadlineNs: ^uint64(0)}

	if got, ok := Decide(tick, []DispatchLaneDescriptor{fastest}, table, request); ok {
		t.Fatalf("mismatched jurisdiction served lane %d", got)
	}

	declared := fastest
	declared.Jurisdiction = 7
	if got, ok := Decide(tick, []DispatchLaneDescriptor{declared}, table, request); !ok || got != 1 {
		t.Fatalf("exact match = %d,%v want 1,true", got, ok)
	}

	global := fastest
	global.Jurisdiction = uint16(generated.DISPATCH_JURISDICTION_GLOBAL)
	if got, ok := Decide(tick, []DispatchLaneDescriptor{global}, table, request); !ok || got != 1 {
		t.Fatalf("global match = %d,%v want 1,true", got, ok)
	}
}

// TestDispatchStaleLanesAreExcludedFromSelection pins freshness gating in its
// pure form. (Conformance anchor: DispatchPlacement/StaleLaneExcluded.)
func TestDispatchStaleLanesAreExcludedFromSelection(t *testing.T) {
	const now = uint64(1_000)
	unconstrained := DispatchRequest{RequiredClassMask: 0b01, DeadlineNs: ^uint64(0)}
	lane := DispatchLaneDescriptor{LaneID: 3, UnitClassMask: 0b01}

	staleByWindow := DispatchLaneStats{EwmaNs: 500, LastTickSeen: now - uint64(generated.DISPATCH_STALE_TICKS) - 1}
	if got, ok := Decide(now, []DispatchLaneDescriptor{lane}, []DispatchLaneStats{staleByWindow}, unconstrained); ok {
		t.Fatalf("window-stale lane served %d", got)
	}

	atEdge := DispatchLaneStats{EwmaNs: 500, LastTickSeen: now - uint64(generated.DISPATCH_STALE_TICKS)}
	got, ok := Decide(now, []DispatchLaneDescriptor{lane}, []DispatchLaneStats{atEdge}, unconstrained)
	if !ok || got != 3 {
		t.Fatalf("boundary-fresh lane = %d,%v want 3,true", got, ok)
	}

	neverCheckedIn := DispatchLaneStats{EwmaNs: 100, LastTickSeen: 0}
	if _, ok := Decide(now+uint64(generated.DISPATCH_STALE_TICKS)+1, []DispatchLaneDescriptor{lane}, []DispatchLaneStats{neverCheckedIn}, unconstrained); ok {
		t.Fatal("zero heartbeat must read as stale")
	}
}

// TestDecideDoesNotAllocate pins the hot-path contract: the decision is pure
// argmin over caller-owned slices and must never allocate. A regression here
// puts a heap allocation inside every dispatch on every host.
func TestDecideDoesNotAllocate(t *testing.T) {
	descriptors := make([]DispatchLaneDescriptor, 8)
	stats := make([]DispatchLaneStats, 8)
	for i := range descriptors {
		descriptors[i] = DispatchLaneDescriptor{
			LaneID: uint16(i), MaxConcurrency: 4,
			UnitClassMask: 0b01, AffinityBloom: 1 << (uint64(i) % 64),
		}
		stats[i] = DispatchLaneStats{EwmaNs: 1_000 + uint64(i)*100, LastTickSeen: 100}
	}
	request := DispatchRequest{RequiredClassMask: 0b01, DeadlineNs: ^uint64(0)}

	var sinkLane uint16
	var sinkOK bool
	allocs := testing.AllocsPerRun(1_000, func() {
		sinkLane, sinkOK = Decide(102, descriptors, stats, request)
	})
	if !sinkOK {
		t.Fatalf("decide stopped deciding: %d,%v", sinkLane, sinkOK)
	}
	if allocs != 0 {
		t.Fatalf("Decide allocates %.1f objects per call; hot path must stay at zero", allocs)
	}
}

func TestBlendEwmaMatchesTheFixedAlpha(t *testing.T) {
	// (1·16_000 + 7·8_000) / 8 = 9_000.
	if got := BlendEwma(8_000, 16_000); got != 9_000 {
		t.Fatalf("blend = %d want 9000", got)
	}
	if got := BlendEwma(0, 12_345); got != 12_345/8 {
		t.Fatalf("cold blend = %d want %d", got, 12_345/8)
	}
	if got := BlendEwma(^uint64(0), ^uint64(0)); got != ^uint64(0) {
		t.Fatalf("saturated blend = %d want max", got)
	}
}

func TestDescriptorCodecRoundTripsEveryField(t *testing.T) {
	original := DispatchLaneDescriptor{
		LaneID:         7,
		Jurisdiction:   42,
		MaxConcurrency: 9,
		Generation:     3,
		UnitClassMask:  0xA5A5_5A5A_0101_00FF,
		AffinityBloom:  1<<40 | 1<<3,
	}
	decoded, err := decodeDescriptor(encodeDescriptor(original))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded != original {
		t.Fatalf("roundtrip mismatch: %+v vs %+v", decoded, original)
	}
	if _, err := decodeDescriptor(make([]byte, 16)); err == nil {
		t.Fatal("short rows must fail")
	}
}
