package runtimehost

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

// Dispatch lane-table types and the pure placement decision.
//
// This file is deliberately free of any build tag and of shared-memory code:
// the decision rules are the cross-runtime contract shared byte-for-byte with
// ovrt-dispatch in Rust, so they live where every platform can exercise the
// same golden vectors. The mmap half lives in dispatch_unix.go.
//
// Layout constants come from runtime_dispatch.capnp through the generated
// bindings, exactly as on the Rust side; nothing here hard-codes geometry.

// DispatchBlock is the Go handle for one shared dispatch region.
//
// The type is declared platform-neutrally so callers and the unsupported
// stubs compile everywhere; only the method implementations differ. On
// linux/darwin it wraps an mmap'd region (dispatch_unix.go). Elsewhere every
// method returns a clear "unsupported" error instead of failing to build.
type DispatchBlock struct {
	// raw backs the region on supported platforms; always nil elsewhere.
	raw []byte
}

// DispatchStatRow is the per-lane statistics handle.
//
// Like DispatchBlock it exists on every platform so signatures hold; on
// unsupported platforms its methods refuse rather than read bytes that were
// never mapped.
type DispatchStatRow struct {
	block *DispatchBlock
	base  int
}

// DispatchLaneDescriptor is one row of a published descriptor table.
type DispatchLaneDescriptor struct {
	LaneID         uint16
	Jurisdiction   uint16
	MaxConcurrency uint32
	Generation     uint32
	UnitClassMask  uint64
	AffinityBloom  uint64
}

// Covers reports whether this lane executes every required class bit.
func (d DispatchLaneDescriptor) Covers(requiredClassMask uint64) bool {
	return d.UnitClassMask&requiredClassMask == requiredClassMask
}

// AllowsJurisdiction accepts an exact jurisdiction match or a lane declared
// global. Any other pairing is rejected before scoring: unknown or mismatched
// jurisdictions select nothing.
func (d DispatchLaneDescriptor) AllowsJurisdiction(request uint16) bool {
	const global = uint16(generated.DISPATCH_JURISDICTION_GLOBAL)
	return d.Jurisdiction == global || d.Jurisdiction == request
}

// HoldsLocality reports whether the locality key hits the lane's Bloom set.
func (d DispatchLaneDescriptor) HoldsLocality(key uint64) bool {
	return d.AffinityBloom>>(key%64)&1 == 1
}

// DispatchLaneStats is one lane's live measurement snapshot.
type DispatchLaneStats struct {
	EwmaNs         uint64
	Inflight       uint32
	MaxConcurrency uint32
	LastTickSeen   uint64
}

// DispatchRequest carries what a caller needs from the table to place work.
type DispatchRequest struct {
	RequiredClassMask uint64
	Jurisdiction      uint16
	DeadlineNs        uint64
	AffinityKey       uint64
}

const maxUint64 = uint64(math.MaxUint64)

// expectedLatencyNs mirrors Rust's DispatchRequest::expected_latency_ns:
// measured mean inflated by queue pressure. Ewma zero means unsampled, and
// unsampled lanes never look free.
func (r DispatchRequest) expectedLatencyNs(stats DispatchLaneStats, descriptor DispatchLaneDescriptor) (uint64, bool) {
	if stats.EwmaNs == 0 {
		return 0, false
	}
	concurrency := stats.MaxConcurrency
	if descriptor.MaxConcurrency > concurrency {
		concurrency = descriptor.MaxConcurrency
	}
	if concurrency == 0 {
		concurrency = 1
	}
	factor := 1 + uint64(stats.Inflight)/uint64(concurrency)
	if factor != 0 && stats.EwmaNs > maxUint64/factor {
		return maxUint64, true
	}
	return stats.EwmaNs * factor, true
}

// IsFresh applies the heartbeat window against the global click. A zero
// heartbeat means the owner never checked in, which is stale by definition
// even though wrapping arithmetic would call it fresh.
func (r DispatchRequest) IsFresh(now uint64, stats DispatchLaneStats) bool {
	return stats.LastTickSeen != 0 && now-stats.LastTickSeen <= uint64(generated.DISPATCH_STALE_TICKS)
}

// BlendEwma applies the fixed α = 1/8 blend using 128-bit intermediates so
// the multiply cannot overflow at any u64 inputs. Mirrors Rust's blend_ewma,
// which computes over u128: saturated inputs must stay saturated, not wrap.
func BlendEwma(previousNs, sampleNs uint64) uint64 {
	const alphaNum = uint64(generated.DISPATCH_EWMA_ALPHA_NUM)
	const alphaDen = uint64(generated.DISPATCH_EWMA_ALPHA_DEN)

	hi1, lo1 := bits.Mul64(alphaNum, sampleNs)
	hi2, lo2 := bits.Mul64(alphaDen-alphaNum, previousNs)
	lo, carry := bits.Add64(lo1, lo2, 0)
	hi := hi1 + hi2 + carry

	// Quotient exceeded 64 bits: saturate rather than truncate.
	if hi >= alphaDen {
		return maxUint64
	}
	quotientLow, _ := bits.Div64(hi, lo, alphaDen)
	return quotientLow
}

// Decide selects the fastest eligible lane, mirroring ovrt-dispatch's decide.
//
// Rule order, identical on both runtimes: retired slots (empty class mask)
// select nothing; capability cover; jurisdiction exact-or-global; freshness;
// sampling; deadline feasibility; then argmin of expected latency minus the
// locality bonus. Ties keep the lower lane id so equal lanes stay stable.
//
// The ok result is false when nothing qualifies and the caller should fall
// back to its static routing.
func Decide(
	now uint64,
	descriptors []DispatchLaneDescriptor,
	stats []DispatchLaneStats,
	request DispatchRequest,
) (laneID uint16, ok bool) {
	bestScore := maxUint64
	found := false
	count := len(descriptors)
	if len(stats) < count {
		count = len(stats)
	}
	if count > int(generated.DISPATCH_MAX_LANES) {
		count = int(generated.DISPATCH_MAX_LANES)
	}
	for index := 0; index < count; index++ {
		descriptor := descriptors[index]
		laneStats := stats[index]
		// Retired or unpublished slots carry an empty class mask and must
		// never serve traffic, even when the request constrains nothing.
		if descriptor.UnitClassMask == 0 {
			continue
		}
		if !descriptor.Covers(request.RequiredClassMask) {
			continue
		}
		if !descriptor.AllowsJurisdiction(request.Jurisdiction) {
			continue
		}
		if !request.IsFresh(now, laneStats) {
			continue
		}
		expected, sampled := request.expectedLatencyNs(laneStats, descriptor)
		if !sampled {
			continue
		}
		score := expected
		if descriptor.HoldsLocality(request.AffinityKey) {
			score = saturatingSub(score, uint64(generated.DISPATCH_AFFINITY_BONUS_NS))
		}
		if score > request.DeadlineNs {
			continue
		}
		if !found || score < bestScore {
			bestScore = score
			laneID = descriptor.LaneID
			found = true
		}
	}
	return laneID, found
}

func saturatingSub(value, delta uint64) uint64 {
	if delta > value {
		return 0
	}
	return value - delta
}

// Descriptor field offsets inside one 64-byte slot, matching the Rust codec.
const (
	rowUnitClasses    = 0
	rowAffinityBloom  = 8
	rowLaneID         = 16
	rowJurisdiction   = 18
	rowMaxConcurrency = 20
	rowGeneration     = 24
	dispatchSlotBytes = int(generated.DISPATCH_LANE_ROW_BYTES)
)

// encodeDescriptor packs one row into its fixed slot, little-endian.
func encodeDescriptor(descriptor DispatchLaneDescriptor) []byte {
	bytes := make([]byte, dispatchSlotBytes)
	binary.LittleEndian.PutUint64(bytes[rowUnitClasses:], descriptor.UnitClassMask)
	binary.LittleEndian.PutUint64(bytes[rowAffinityBloom:], descriptor.AffinityBloom)
	binary.LittleEndian.PutUint16(bytes[rowLaneID:], descriptor.LaneID)
	binary.LittleEndian.PutUint16(bytes[rowJurisdiction:], descriptor.Jurisdiction)
	binary.LittleEndian.PutUint32(bytes[rowMaxConcurrency:], descriptor.MaxConcurrency)
	binary.LittleEndian.PutUint32(bytes[rowGeneration:], descriptor.Generation)
	return bytes
}

// decodeDescriptor unpacks one published slot. Short rows are refused rather
// than silently misread.
func decodeDescriptor(row []byte) (DispatchLaneDescriptor, error) {
	if len(row) < dispatchSlotBytes {
		return DispatchLaneDescriptor{}, fmt.Errorf("descriptor row holds %d bytes; %d required", len(row), dispatchSlotBytes)
	}
	return DispatchLaneDescriptor{
		UnitClassMask:  binary.LittleEndian.Uint64(row[rowUnitClasses:]),
		AffinityBloom:  binary.LittleEndian.Uint64(row[rowAffinityBloom:]),
		LaneID:         binary.LittleEndian.Uint16(row[rowLaneID:]),
		Jurisdiction:   binary.LittleEndian.Uint16(row[rowJurisdiction:]),
		MaxConcurrency: binary.LittleEndian.Uint32(row[rowMaxConcurrency:]),
		Generation:     binary.LittleEndian.Uint32(row[rowGeneration:]),
	}, nil
}
