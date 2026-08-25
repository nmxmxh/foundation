package hermes

import (
	"fmt"
	"testing"
)

// Structural tests for compactKeys.
//
// Compaction collapses a chain of delta snapshots into one flat key set, and it
// walks that chain newest-first. The rule it must hold is that the newest
// verdict for a key wins: a key removed in a recent layer stays removed even
// though an older layer still lists it as added, and a key re-added after a
// removal comes back. Getting this backwards does not fail loudly — it silently
// resurrects deleted records or drops live ones at the next compaction, which
// is why the ordering is pinned here directly rather than only through the
// projection surface.

// chainOf builds a delta chain oldest-first, so layers[0] is the base and the
// last element is the newest snapshot.
func chainOf(t *testing.T, layers []struct{ adds, removes []string }) *indexSnapshot {
	t.Helper()
	var current *indexSnapshot
	for depth, layer := range layers {
		adds := make(map[string]struct{}, len(layer.adds))
		for _, key := range layer.adds {
			adds[key] = struct{}{}
		}
		var removes map[string]struct{}
		if len(layer.removes) > 0 {
			removes = make(map[string]struct{}, len(layer.removes))
			for _, key := range layer.removes {
				removes[key] = struct{}{}
			}
		}
		current = &indexSnapshot{
			base:    current,
			adds:    adds,
			removes: removes,
			size:    len(adds),
			depth:   depth + 1,
		}
	}
	return current
}

func TestCompactKeysKeepsTheNewestVerdict(t *testing.T) {
	cases := []struct {
		name   string
		layers []struct{ adds, removes []string }
		want   []string
	}{
		{
			name:   "single layer keeps its adds",
			layers: []struct{ adds, removes []string }{{adds: []string{"a", "b"}}},
			want:   []string{"a", "b"},
		},
		{
			name: "newer remove beats older add",
			layers: []struct{ adds, removes []string }{
				{adds: []string{"a", "b"}},
				{removes: []string{"a"}},
			},
			want: []string{"b"},
		},
		{
			name: "newer add beats older remove",
			layers: []struct{ adds, removes []string }{
				{adds: []string{"a"}},
				{removes: []string{"a"}},
				{adds: []string{"a"}},
			},
			want: []string{"a"},
		},
		{
			name: "remove of a key never added is not resurrected",
			layers: []struct{ adds, removes []string }{
				{adds: []string{"a"}},
				{removes: []string{"ghost"}},
			},
			want: []string{"a"},
		},
		{
			name: "a key added and removed in the same layer reads as removed",
			// removes is scanned before adds within a layer, so the remove wins
			// and the add is skipped as already-seen. Pinned because the two
			// loops' order is load-bearing, not incidental.
			layers: []struct{ adds, removes []string }{
				{adds: []string{"a", "b"}, removes: []string{"a"}},
			},
			want: []string{"b"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := compactKeys(chainOf(t, tc.layers))
			if len(got) != len(tc.want) {
				t.Fatalf("compactKeys returned %d keys (%v), want %d (%v)", len(got), keysOf(got), len(tc.want), tc.want)
			}
			for _, key := range tc.want {
				if _, ok := got[key]; !ok {
					t.Fatalf("key %q missing from %v", key, keysOf(got))
				}
			}
		})
	}
}

// TestCompactKeysHandlesADeepChain covers the shape compaction actually runs
// on: a chain at the configured depth bound rather than a handful of layers.
func TestCompactKeysHandlesADeepChain(t *testing.T) {
	layers := make([]struct{ adds, removes []string }, 0, maxIndexDeltaDepth)
	for i := range maxIndexDeltaDepth {
		layer := struct{ adds, removes []string }{adds: []string{fmt.Sprintf("key_%d", i)}}
		// Every third key is removed by the layer immediately after it.
		if i > 0 && i%3 == 0 {
			layer.removes = []string{fmt.Sprintf("key_%d", i-1)}
		}
		layers = append(layers, layer)
	}

	got := compactKeys(chainOf(t, layers))

	for i := range maxIndexDeltaDepth {
		_, live := got[fmt.Sprintf("key_%d", i)]
		removedByNextLayer := i+1 < maxIndexDeltaDepth && (i+1)%3 == 0
		if removedByNextLayer && live {
			t.Fatalf("key_%d was removed by a newer layer but survived compaction", i)
		}
		if !removedByNextLayer && !live {
			t.Fatalf("key_%d was never removed but did not survive compaction", i)
		}
	}
}

func keysOf(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	return out
}
