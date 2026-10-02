package hermes

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
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

func TestIndexCompactionPreservesQueryCorrectness(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name: "signals", Domain: "signals", Collection: "ticks",
		IndexedFields: []string{"symbol"}, MaxRecords: 8192, MaxBytes: 64 << 20,
	})
	ctx := t.Context()

	const total = maxIndexDeltaDepth + 64
	const deleted = 50
	for i := range total {
		if _, err := store.Apply(ctx, "signals", Event{
			Operation: OperationUpsert,
			SourceID:  fmt.Sprintf("src_up_%d", i),
			Version:   uint64(i + 1),
			Record:    testRecord("signals", "ticks", "org_1", fmt.Sprintf("tick_%d", i), map[string]any{"symbol": "OVS"}),
		}); err != nil {
			t.Fatalf("upsert %d err=%v", i, err)
		}
	}
	for i := range deleted {
		if _, err := store.Apply(ctx, "signals", Event{
			Operation: OperationDelete,
			SourceID:  fmt.Sprintf("src_del_%d", i),
			Version:   uint64(total + i + 1),
			Record:    testRecord("signals", "ticks", "org_1", fmt.Sprintf("tick_%d", i), nil),
		}); err != nil {
			t.Fatalf("delete %d err=%v", i, err)
		}
	}

	count, err := store.Count(ctx, "signals",
		QueryWithFilters("org_1", 0, mustFilter(t, "symbol", "OVS")), Fence{})
	if err != nil {
		t.Fatalf("Count() err=%v", err)
	}
	if want := int64(total - deleted); count != want {
		t.Fatalf("indexed count after compaction = %d, want %d", count, want)
	}
}

func TestEstimateValueBytes(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  int
	}{
		{"nil", nil, 0},
		{"string", "abcd", 4},
		{"bytes", []byte("xyz"), 3},
		{"float32 slice", []float32{1, 2, 3}, 12},
		{"float64 slice", []float64{1, 2}, 16},
		{"string slice", []string{"ab", "c"}, 3},
		{"bool", true, 1},
		{"int", int64(7), 8},
		{"record value text", database.StringValue("hello"), 5},
		{"record value raw", database.RawValue([]byte(`{"a":1}`)), len(`{"a":1}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := estimateValueBytes(tc.value); got != tc.want {
				t.Fatalf("estimateValueBytes(%v) = %d, want %d", tc.value, got, tc.want)
			}
		})
	}

	m := map[string]any{"k": "vv"}
	if got := estimateValueBytes(m); got != 1+2+16 {
		t.Fatalf("estimateValueBytes(map) = %d, want 19", got)
	}
}
