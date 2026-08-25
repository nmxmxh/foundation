//go:build linux || darwin

package runtimehost

import (
	"errors"
	"sync/atomic"
	"testing"
)

// Claim-path contention benchmarks.
//
// DispatchStatRow.Claim is a fetch-and-add: one LDADD under ARMv8.1 LSE, one
// LOCK XADD on x86, and it cannot fail. ReleaseOne is a bounded compare-and-swap
// loop that gives up after eight turns and returns false, dropping the release.
// These measure both sides of that asymmetry on the real shared-memory region,
// and count how often the bound is actually exhausted.

func benchStatRow(b *testing.B) (*DispatchBlock, *DispatchStatRow) {
	b.Helper()
	block, err := OpenDispatchRegion(tempRegion(b))
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	row, err := block.StatRow(0)
	if err != nil {
		b.Fatalf("row: %v", err)
	}
	return block, row
}

// BenchmarkDispatchClaimFAA is the current claim path under producer contention.
func BenchmarkDispatchClaimFAA(b *testing.B) {
	block, row := benchStatRow(b)
	defer func() { _ = block.Close() }()
	b.ReportAllocs()
	// Sunk so the compiler cannot delete the claim as dead (TE-18).
	var sink atomic.Uint32
	b.RunParallel(func(pb *testing.PB) {
		var last uint32
		for pb.Next() {
			count, err := row.Claim()
			if err != nil {
				b.Fatalf("claim: %v", err)
			}
			last = count
		}
		sink.Store(last)
	})
	if sink.Load() == 0 {
		b.Fatal("claim count sank to zero; the benchmark measured nothing")
	}
}

// BenchmarkDispatchReleaseCAS is the bounded CAS loop under the same
// contention, with the give-up rate reported. A give-up returns false and the
// in-flight count is never decremented, so the lane reads as busier than it is.
func BenchmarkDispatchReleaseCAS(b *testing.B) {
	block, row := benchStatRow(b)
	defer func() { _ = block.Close() }()

	// Seed the counter high enough that the zero-guard never short-circuits the
	// loop; a zero read returns early and would not exercise the CAS at all.
	slot, err := row.inflight()
	if err != nil {
		b.Fatalf("inflight: %v", err)
	}
	atomic.StoreUint32(slot, ^uint32(0)>>1)

	var giveUps atomic.Uint64
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		var local uint64
		for pb.Next() {
			ok, err := row.ReleaseOne()
			switch {
			case ok:
			case errors.Is(err, ErrDispatchLaneContended):
				// The measured quantity: the retry budget ran out. Expected
				// under this synthetic contention, never under single-writer.
				local++
			case err != nil:
				b.Fatalf("release: %v", err)
			}
			// Put the unit back so the counter cannot drain to zero mid-run.
			if _, err := row.Claim(); err != nil {
				b.Fatalf("reclaim: %v", err)
			}
		}
		giveUps.Add(local)
	})
	b.ReportMetric(float64(giveUps.Load())/float64(b.N), "giveups/op")
}

// BenchmarkDispatchAdvanceTick is the global click: a single shared cache line
// hit by every producer, which is the worst case this design has.
func BenchmarkDispatchAdvanceTick(b *testing.B) {
	block, err := OpenDispatchRegion(tempRegion(b))
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer func() { _ = block.Close() }()
	b.ReportAllocs()
	var sink atomic.Uint64
	b.RunParallel(func(pb *testing.PB) {
		var last uint64
		for pb.Next() {
			previous, err := block.AdvanceTick()
			if err != nil {
				b.Fatalf("tick: %v", err)
			}
			last = previous
		}
		sink.Store(last)
	})
	if sink.Load() == 0 {
		b.Fatal("tick sank to zero; the benchmark measured nothing")
	}
}
