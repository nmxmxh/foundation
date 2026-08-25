package hermes

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"testing"
)

// Correctness for forEachKey's delta walk.
//
// The scan emits the live key set of a snapshot chain, newest verdict winning.
// It now dedups against the delta layers alone rather than against every key in
// the index, which is only sound because the chain bottoms out in a flat
// snapshot whose keys are unique. If that reasoning is wrong the failure is
// quiet and ugly: a key emitted twice inflates a count, and a key dropped makes
// a live record invisible to every query that goes through an index. Neither
// raises an error anywhere.
//
// forEachKeyReconciled is retained as the general walk and doubles as the
// oracle here, so the two can be compared directly on generated chains rather
// than only on cases someone thought to enumerate.

func collectScan(s *indexSnapshot) []string {
	var got []string
	s.forEachKey(func(key string) bool {
		got = append(got, key)
		return true
	})
	sort.Strings(got)
	return got
}

func collectReconciled(s *indexSnapshot) []string {
	var got []string
	s.forEachKeyReconciled(func(key string) bool {
		got = append(got, key)
		return true
	})
	sort.Strings(got)
	return got
}

func keySet(keys ...string) map[string]struct{} {
	if len(keys) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		out[key] = struct{}{}
	}
	return out
}

// layer stacks one delta on top of base.
//
// size is derived by reconciling rather than by arithmetic on the input
// lengths. Subtracting len(removes) is wrong whenever a layer retracts a key
// that was never present or restates one the base already holds, and an
// under-counted size is not a cosmetic flaw: forEachKey returns immediately
// when len() reports zero, so an inconsistent fixture silently scans nothing
// and every assertion against it passes vacuously.
func layer(base *indexSnapshot, adds, removes []string) *indexSnapshot {
	next := &indexSnapshot{
		base:    base,
		adds:    keySet(adds...),
		removes: keySet(removes...),
		depth:   base.depth + 1,
	}
	if next.adds == nil {
		next.adds = map[string]struct{}{}
	}
	// Large enough that the reconciling walk is never short-circuited, then
	// replaced with the count it produced.
	next.size = base.len() + len(adds)
	live := 0
	next.forEachKeyReconciled(func(string) bool {
		live++
		return true
	})
	next.size = live
	return next
}

func flat(keys ...string) *indexSnapshot {
	return &indexSnapshot{adds: keySet(keys...), size: len(keys)}
}

func TestForEachKeyHonoursNewestVerdict(t *testing.T) {
	cases := []struct {
		name string
		snap *indexSnapshot
		want []string
	}{
		{"flat base only", flat("a", "b", "c"), []string{"a", "b", "c"}},
		{"delta adds a new key", layer(flat("a", "b"), []string{"c"}, nil), []string{"a", "b", "c"}},
		{
			// The duplicate-emission case: restating a key the base already
			// holds must yield it once, not twice.
			"delta restates a base key",
			layer(flat("a", "b"), []string{"a"}, nil),
			[]string{"a", "b"},
		},
		{"delta removes a base key", layer(flat("a", "b", "c"), nil, []string{"b"}), []string{"a", "c"}},
		{
			"remove then re-add in a newer layer",
			layer(layer(flat("a", "b"), nil, []string{"a"}), []string{"a"}, nil),
			[]string{"a", "b"},
		},
		{
			"add then remove in a newer layer",
			layer(layer(flat("b"), []string{"a"}, nil), nil, []string{"a"}),
			[]string{"b"},
		},
		{
			// removes are scanned before adds within a layer, so a key doing
			// both in one layer reads as removed.
			"add and remove in the same layer",
			layer(flat("b"), []string{"a"}, []string{"a"}),
			[]string{"b"},
		},
		{
			"removing a key that was never present",
			layer(flat("a"), nil, []string{"ghost"}),
			[]string{"a"},
		},
		{"empty base with delta adds", layer(flat(), []string{"a", "b"}, nil), []string{"a", "b"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := collectScan(tc.snap)
			if len(got) != len(tc.want) {
				t.Fatalf("scan = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("scan = %v, want %v", got, tc.want)
				}
			}
			// The general walk must agree; if it does not, one of the two is
			// wrong and the disagreement matters more than either result.
			if oracle := collectReconciled(tc.snap); len(oracle) != len(got) {
				t.Fatalf("scan = %v but reconciled walk = %v", got, oracle)
			}
		})
	}
}

// TestForEachKeyMatchesReconciledOnGeneratedChains is the property: for any
// chain shape, the fast walk and the general walk must agree exactly. Seeded so
// a failure is reproducible (TE-27).
func TestForEachKeyMatchesReconciledOnGeneratedChains(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x5eed, 0xf00d))
	const universe = 40

	for trial := range 300 {
		baseKeys := make([]string, 0, universe)
		for i := range universe {
			if rng.IntN(2) == 0 {
				baseKeys = append(baseKeys, fmt.Sprintf("k%02d", i))
			}
		}
		snap := flat(baseKeys...)

		layers := rng.IntN(6)
		for range layers {
			var adds, removes []string
			for i := range universe {
				switch rng.IntN(6) {
				case 0:
					adds = append(adds, fmt.Sprintf("k%02d", i))
				case 1:
					removes = append(removes, fmt.Sprintf("k%02d", i))
				}
			}
			snap = layer(snap, adds, removes)
		}

		got := collectScan(snap)
		want := collectReconciled(snap)
		if len(got) != len(want) {
			t.Fatalf("trial %d: scan produced %d keys, reconciled produced %d\nscan=%v\nwant=%v",
				trial, len(got), len(want), got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("trial %d: scan=%v want=%v", trial, got, want)
			}
		}
		// No key may be emitted twice: sorted output makes a duplicate adjacent.
		for i := 1; i < len(got); i++ {
			if got[i] == got[i-1] {
				t.Fatalf("trial %d: key %q emitted twice", trial, got[i])
			}
		}
	}
}

// TestForEachKeyStopsWhenCallbackDeclines pins early termination on both sides
// of the walk: inside the delta layers, and once it has reached the flat base.
func TestForEachKeyStopsWhenCallbackDeclines(t *testing.T) {
	snap := layer(flat("a", "b", "c", "d"), []string{"x", "y"}, nil)

	for _, limit := range []int{1, 2, 3, 5} {
		seen := 0
		snap.forEachKey(func(string) bool {
			seen++
			return seen < limit
		})
		if seen != limit {
			t.Fatalf("limit %d: callback ran %d times, want %d", limit, seen, limit)
		}
	}
}

// TestForEachKeyFallsBackWhenTerminalCarriesRemoves covers the shape the fast
// walk refuses: a chain whose oldest layer still holds tombstones, where
// dedup-against-deltas-only would be unsound.
func TestForEachKeyFallsBackWhenTerminalCarriesRemoves(t *testing.T) {
	terminal := &indexSnapshot{
		adds:    keySet("a", "b"),
		removes: keySet("ghost"),
		size:    2,
	}
	snap := layer(terminal, []string{"c"}, nil)

	got := collectScan(snap)
	want := collectReconciled(snap)
	if len(got) != len(want) {
		t.Fatalf("scan=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("scan=%v want=%v", got, want)
		}
	}
}
