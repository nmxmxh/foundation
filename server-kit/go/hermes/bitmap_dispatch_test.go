package hermes

import "testing"

// Reference implementation of the scalar population count, spelled out here
// rather than calling the function under test so a hook bug cannot hide
// behind the fallback it is supposed to bypass.
func referenceCount(words []uint64) int {
	total := 0
	for _, word := range words {
		for ; word != 0; word &= word - 1 {
			total++
		}
	}
	return total
}

func TestBitmapCountHookClaimsAndDeclines(t *testing.T) {
	defer ClearBitmapDispatchHooks()

	words := []uint64{0xF0F0_F0F0_F0F0_F0F0, 0x0000_0000_0000_0001, 1 << 63, 0}
	scalar := referenceCount(words)

	// A claiming hook wins, even when deliberately wrong: that proves the
	// seam routes to the external executor.
	claiming := BitmapCountHook(func(got []uint64) (int, bool) {
		if len(got) != len(words) {
			t.Fatalf("hook saw %d words want %d", len(got), len(words))
		}
		return scalar + 100, true
	})
	SetBitmapCountHook(&claiming)
	if got := popcountWords(words); got != scalar+100 {
		t.Fatalf("claimed count = %d want %d", got, scalar+100)
	}

	// A declining hook hands the op back to the scalar loop unchanged.
	declining := BitmapCountHook(func([]uint64) (int, bool) { return 0, false })
	SetBitmapCountHook(&declining)
	if got := popcountWords(words); got != scalar {
		t.Fatalf("declined count = %d want %d", got, scalar)
	}
}

func TestBitmapBinaryHooksAreBitIdenticalOrDeclined(t *testing.T) {
	defer ClearBitmapDispatchHooks()

	dst := []uint64{0xFF00_FF00_FF00_FF00, 0x0000_0000_0000_000F, 1 << 63}
	src := []uint64{0x0F0F_0F0F_0F0F_0F0F, 0x0000_0000_0000_0003, 1 << 62}

	wantByOp := map[BitmapOp][]uint64{
		BitmapAnd:    {dst[0] & src[0], dst[1] & src[1], dst[2] & src[2]},
		BitmapOr:     {dst[0] | src[0], dst[1] | src[1], dst[2] | src[2]},
		BitmapAndNot: {dst[0] &^ src[0], dst[1] &^ src[1], dst[2] &^ src[2]},
	}
	kernelByOp := map[BitmapOp]func(dst, src []uint64){
		BitmapAnd:    andWords,
		BitmapOr:     orWords,
		BitmapAndNot: andNotWords,
	}

	for op, kernel := range kernelByOp {
		want := wantByOp[op]

		// The claiming hook applies the same algebra; results must be
		// bit-identical because these operations are pure boolean algebra.
		hook := BitmapBinaryHook(func(got BitmapOp, d, s []uint64) bool {
			if got != op {
				t.Fatalf("op = %d want %d", got, op)
			}
			for i := range d {
				switch got {
				case BitmapAnd:
					d[i] &= s[i]
				case BitmapOr:
					d[i] |= s[i]
				case BitmapAndNot:
					d[i] &^= s[i]
				}
			}
			return true
		})
		SetBitmapBinaryHook(&hook)
		claimed := append([]uint64(nil), dst...)
		kernel(claimed, src)
		for i := range claimed {
			if claimed[i] != want[i] {
				t.Fatalf("op %d word %d: claimed %b want %b", op, i, claimed[i], want[i])
			}
		}

		// The declining hook leaves the arrays exactly as the scalar loop.
		declining := BitmapBinaryHook(func(BitmapOp, []uint64, []uint64) bool { return false })
		SetBitmapBinaryHook(&declining)
		fallback := append([]uint64(nil), dst...)
		kernel(fallback, src)
		for i := range fallback {
			if fallback[i] != want[i] {
				t.Fatalf("op %d word %d: fallback %b want %b", op, i, fallback[i], want[i])
			}
		}
	}
}

func TestClearBitmapDispatchHooksRestoresTheScalarPath(t *testing.T) {
	words := []uint64{0xFFFF_0000_0000_0001}
	scalar := referenceCount(words)

	claiming := BitmapCountHook(func([]uint64) (int, bool) { return -1, true })
	SetBitmapCountHook(&claiming)
	ClearBitmapDispatchHooks()
	if got := popcountWords(words); got != scalar {
		t.Fatalf("post-clear count = %d want %d", got, scalar)
	}
}
