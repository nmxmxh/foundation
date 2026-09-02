//go:build linux || darwin

package runtimehost

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// TE-07 coverage for ReleaseOne's bounded retry: zero, one, two, many, and
// exhausted.
//
// The defect these guard against is not a crash. ReleaseOne used to return
// (false, nil) both when the row was already at zero and when its retry budget
// ran out, so a caller could not tell "nothing to release" from "your release
// did not land". Treating the second as the first leaks an in-flight unit that
// is never reclaimed, and because MaxConcurrency gates placement, a lane that
// accrues phantom units quietly stops being offered work. The failure surfaces
// far from its cause, which is why the two outcomes are now distinct and why
// the conservation property below is asserted rather than assumed.

func releaseTestRow(t *testing.T) (*DispatchBlock, *DispatchStatRow) {
	t.Helper()
	block, err := OpenDispatchRegion(tempRegion(t))
	if err != nil {
		t.Fatalf("OpenDispatchRegion: %v", err)
	}
	t.Cleanup(func() { _ = block.Close() })
	row, err := block.StatRow(0)
	if err != nil {
		t.Fatalf("StatRow: %v", err)
	}
	return block, row
}

// TestReleaseOneDrainsExactlyWhatWasClaimed walks the loop counts TE-07 names.
// The "many" case is deliberately larger than dispatchReleaseMaxAttempts so a
// bound accidentally applied to the drain rather than to one attempt would show.
func TestReleaseOneDrainsExactlyWhatWasClaimed(t *testing.T) {
	for _, claims := range []int{0, 1, 2, dispatchReleaseMaxAttempts, 64} {
		t.Run(strconvItoa(claims), func(t *testing.T) {
			_, row := releaseTestRow(t)

			for i := range claims {
				if _, err := row.Claim(); err != nil {
					t.Fatalf("claim %d: %v", i, err)
				}
			}

			for i := range claims {
				ok, err := row.ReleaseOne()
				if err != nil {
					t.Fatalf("release %d: unexpected error %v", i, err)
				}
				if !ok {
					t.Fatalf("release %d refused while %d units were outstanding", i, claims-i)
				}
			}

			// One release past the end: the row is now at zero, which is the
			// refusal case and must not be reported as contention.
			ok, err := row.ReleaseOne()
			if ok {
				t.Fatal("release below zero must be refused")
			}
			if err != nil {
				t.Fatalf("release at zero returned %v, want nil; at-zero is a refusal, not a failure", err)
			}

			snapshot, err := row.Snapshot()
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			if snapshot.Inflight != 0 {
				t.Fatalf("in-flight = %d after draining %d claims, want 0", snapshot.Inflight, claims)
			}
		})
	}
}

// TestReleaseOneAtZeroIsDistinctFromContention pins the contract that motivated
// the change: the two false outcomes must be distinguishable by error alone.
func TestReleaseOneAtZeroIsDistinctFromContention(t *testing.T) {
	_, row := releaseTestRow(t)

	ok, err := row.ReleaseOne()
	if ok {
		t.Fatal("an untouched row reported a release")
	}
	if err != nil {
		t.Fatalf("at-zero returned %v, want nil", err)
	}
	if errors.Is(err, ErrDispatchLaneContended) {
		t.Fatal("at-zero must not be reported as contention")
	}
}

// TestReleaseOneUnderContentionConservesUnits is the invariant that makes the
// sentinel worth having: across any interleaving, every claimed unit is either
// released or still counted. None may vanish.
//
// Exhaustion cannot be forced deterministically without injecting into the CAS
// loop, so this does not assert that it occurs. It asserts the two things that
// must hold whether or not it does — conservation, and that any failure to
// release carries the sentinel rather than a bare false.
func TestReleaseOneUnderContentionConservesUnits(t *testing.T) {
	const (
		releasers    = 8
		perGoroutine = 256
		claims       = releasers * perGoroutine
	)
	_, row := releaseTestRow(t)

	for i := range claims {
		if _, err := row.Claim(); err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
	}

	var released, contended, refusedAtZero atomic.Uint64
	var wg sync.WaitGroup
	for range releasers {
		wg.Go(func() {
			for range perGoroutine {
				ok, err := row.ReleaseOne()
				switch {
				case ok:
					released.Add(1)
				case errors.Is(err, ErrDispatchLaneContended):
					contended.Add(1)
				case err == nil:
					refusedAtZero.Add(1)
				default:
					t.Errorf("unexpected release error: %v", err)
					return
				}
			}
		})
	}
	wg.Wait()

	snapshot, err := row.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// Conservation: what was claimed is either gone or still counted.
	if total := released.Load() + uint64(snapshot.Inflight); total != claims {
		t.Fatalf("released %d + in-flight %d = %d, want %d claimed; a unit was lost or invented",
			released.Load(), snapshot.Inflight, total, claims)
	}

	// Every attempt is accounted for by exactly one outcome.
	attempts := released.Load() + contended.Load() + refusedAtZero.Load()
	if attempts != claims {
		t.Fatalf("outcomes total %d, want %d attempts", attempts, claims)
	}

	// A refusal is only legitimate when the row actually reached zero. With
	// attempts equal to claims, that can only happen after every unit is gone.
	if refusedAtZero.Load() > 0 && released.Load() != claims {
		t.Fatalf("%d refusals reported while %d of %d units were still outstanding; "+
			"a contended failure was misreported as at-zero",
			refusedAtZero.Load(), claims-released.Load(), claims)
	}

	t.Logf("released=%d contended=%d refused=%d remaining=%d",
		released.Load(), contended.Load(), refusedAtZero.Load(), snapshot.Inflight)
}

// strconvItoa keeps the subtest names readable without importing strconv for a
// single call in a file that otherwise needs none of it.
func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
