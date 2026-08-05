package hermes

import (
	"math/bits"
	"math/rand/v2"
	"testing"
)

// naive references: the pre-MLP single-accumulator / single-stream forms the
// interleaved kernels replace. The helpers must match these bit-for-bit.

func popcountWordsNaive(words []uint64) int {
	total := 0
	for _, w := range words {
		total += bits.OnesCount64(w)
	}
	return total
}

func randWords(n int, seed uint64) []uint64 {
	r := rand.New(rand.NewPCG(seed, seed*2+1))
	w := make([]uint64, n)
	for i := range w {
		w[i] = r.Uint64()
	}
	return w
}

// tailLengths exercises every remainder around the 4-word interleave stride so
// the bulk loop and the scalar tail are both covered.
var tailLengths = []int{0, 1, 2, 3, 4, 5, 7, 8, 9, 15, 16, 17, 63, 64, 65}

func TestPopcountWordsMatchesReference(t *testing.T) {
	for _, n := range tailLengths {
		w := randWords(n, uint64(n)+1)
		if got, want := popcountWords(w), popcountWordsNaive(w); got != want {
			t.Fatalf("n=%d: popcountWords=%d reference=%d", n, got, want)
		}
	}
}

func TestAndWordsMatchesReference(t *testing.T) {
	for _, n := range tailLengths {
		dst := randWords(n, uint64(n)+2)
		src := randWords(n, uint64(n)+3)
		want := make([]uint64, n)
		for i := range want {
			want[i] = dst[i] & src[i]
		}
		andWords(dst, src)
		for i := range dst {
			if dst[i] != want[i] {
				t.Fatalf("n=%d word=%d: andWords=%#x reference=%#x", n, i, dst[i], want[i])
			}
		}
	}
}

func TestOrWordsMatchesReference(t *testing.T) {
	for _, n := range tailLengths {
		dst := randWords(n, uint64(n)+4)
		src := randWords(n, uint64(n)+5)
		want := make([]uint64, n)
		for i := range want {
			want[i] = dst[i] | src[i]
		}
		orWords(dst, src)
		for i := range dst {
			if dst[i] != want[i] {
				t.Fatalf("n=%d word=%d: orWords=%#x reference=%#x", n, i, dst[i], want[i])
			}
		}
	}
}

func TestAndNotWordsMatchesReference(t *testing.T) {
	for _, n := range tailLengths {
		dst := randWords(n, uint64(n)+6)
		src := randWords(n, uint64(n)+7)
		want := make([]uint64, n)
		for i := range want {
			want[i] = dst[i] &^ src[i]
		}
		andNotWords(dst, src)
		for i := range dst {
			if dst[i] != want[i] {
				t.Fatalf("n=%d word=%d: andNotWords=%#x reference=%#x", n, i, dst[i], want[i])
			}
		}
	}
}

func TestNotWordsMatchesReference(t *testing.T) {
	for _, n := range tailLengths {
		dst := randWords(n, uint64(n)+8)
		want := make([]uint64, n)
		for i := range want {
			want[i] = ^dst[i]
		}
		notWords(dst)
		for i := range dst {
			if dst[i] != want[i] {
				t.Fatalf("n=%d word=%d: notWords=%#x reference=%#x", n, i, dst[i], want[i])
			}
		}
	}
}

// andWords requires len(src) >= len(dst); a shorter src must panic loudly
// rather than silently corrupt, so the invariant fails closed.
func TestAndWordsShorterSrcPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("andWords with a shorter src did not panic")
		}
	}()
	andWords(make([]uint64, 4), make([]uint64, 2))
}

// --- benchmarks: interleaved (MLP) vs the naive single-stream forms ---

const benchWords = 4096 // 256 Ki rows of selection bitmap

func BenchmarkPopcountWordsInterleaved(b *testing.B) {
	w := randWords(benchWords, 1)
	b.ReportAllocs()
	var sink int
	for b.Loop() {
		sink = popcountWords(w)
	}
	_ = sink
}

func BenchmarkPopcountWordsNaive(b *testing.B) {
	w := randWords(benchWords, 1)
	b.ReportAllocs()
	var sink int
	for b.Loop() {
		sink = popcountWordsNaive(w)
	}
	_ = sink
}

func BenchmarkAndWordsInterleaved(b *testing.B) {
	dst := randWords(benchWords, 2)
	src := randWords(benchWords, 3)
	b.ReportAllocs()
	for b.Loop() {
		andWords(dst, src)
	}
}

func BenchmarkAndWordsNaive(b *testing.B) {
	dst := randWords(benchWords, 2)
	src := randWords(benchWords, 3)
	b.ReportAllocs()
	for b.Loop() {
		for i := range dst {
			dst[i] &= src[i]
		}
	}
}
