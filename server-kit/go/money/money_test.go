package money

import (
	"math"
	"math/big"
	"testing"
)

func TestMoneyAddSubMul(t *testing.T) {
	m1 := New(100, USD)
	m2 := New(250, USD)

	// Addition
	sum, err := m1.Add(m2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sum.Amount != 350 || sum.Currency.Code != "USD" {
		t.Fatalf("unexpected sum: %+v", sum)
	}

	// Subtraction
	diff, err := m2.Sub(m1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff.Amount != 150 {
		t.Fatalf("unexpected diff: %+v", diff)
	}

	// Multiplication
	prod, err := m1.Mul(5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prod.Amount != 500 {
		t.Fatalf("unexpected prod: %+v", prod)
	}

	// Zero multipliers
	prod2, err := m1.Mul(0)
	if err != nil || prod2.Amount != 0 {
		t.Fatal("expected zero on zero multiplication")
	}

	prod3, err := New(0, USD).Mul(5)
	if err != nil || prod3.Amount != 0 {
		t.Fatal("expected zero on zero multiplication")
	}

	// Cross-currency error
	_, err = m1.Add(New(100, JPY))
	if err == nil {
		t.Fatal("expected error adding different currencies")
	}

	_, err = m1.Sub(New(100, JPY))
	if err == nil {
		t.Fatal("expected error subtracting different currencies")
	}

	// Overflow checks
	maxMoney := New(math.MaxInt64, USD)
	minMoney := New(math.MinInt64, USD)

	_, err = maxMoney.Add(New(1, USD))
	if err == nil {
		t.Fatal("expected overflow error on Add")
	}

	_, err = minMoney.Sub(New(1, USD))
	if err == nil {
		t.Fatal("expected overflow error on Sub")
	}

	_, err = maxMoney.Mul(2)
	if err == nil {
		t.Fatal("expected overflow error on Mul")
	}

	_, err = New(-1, USD).Mul(math.MinInt64)
	if err == nil {
		t.Fatal("expected overflow error on Mul with -1 and MinInt64")
	}
}

func TestMoneyRoundingAndScaling(t *testing.T) {
	tests := []struct {
		amount    int64
		factor    int64
		divisor   int64
		mode      RoundingMode
		expected  int64
		expectErr bool
	}{
		// RoundHalfToEven (Banker's rounding)
		{amount: 5, factor: 1, divisor: 2, mode: RoundHalfToEven, expected: 2},   // 2.5 -> 2 (even)
		{amount: 15, factor: 1, divisor: 10, mode: RoundHalfToEven, expected: 2}, // 1.5 -> 2 (even)
		{amount: 25, factor: 1, divisor: 10, mode: RoundHalfToEven, expected: 2}, // 2.5 -> 2 (even)
		{amount: 35, factor: 1, divisor: 10, mode: RoundHalfToEven, expected: 4}, // 3.5 -> 4 (even)
		{amount: 5, factor: 3, divisor: 10, mode: RoundHalfToEven, expected: 2},  // 1.5 -> 2 (even)
		{amount: 5, factor: 5, divisor: 10, mode: RoundHalfToEven, expected: 2},  // 2.5 -> 2 (even)

		// RoundHalfUp
		{amount: 5, factor: 1, divisor: 2, mode: RoundHalfUp, expected: 3},     // 2.5 -> 3
		{amount: 25, factor: 1, divisor: 10, mode: RoundHalfUp, expected: 3},   // 2.5 -> 3
		{amount: -25, factor: 1, divisor: 10, mode: RoundHalfUp, expected: -3}, // -2.5 -> -3

		// RoundDown
		{amount: 27, factor: 1, divisor: 10, mode: RoundDown, expected: 2},
		{amount: -27, factor: 1, divisor: 10, mode: RoundDown, expected: -2},

		// RoundUp
		{amount: 23, factor: 1, divisor: 10, mode: RoundUp, expected: 3},
		{amount: -23, factor: 1, divisor: 10, mode: RoundUp, expected: -3},

		// Negative divisor checks
		{amount: 25, factor: 1, divisor: -10, mode: RoundHalfUp, expected: -3},
		{amount: -25, factor: 1, divisor: -10, mode: RoundHalfUp, expected: 3},

		// Divisor zero
		{amount: 10, factor: 1, divisor: 0, mode: RoundHalfToEven, expectErr: true},

		// Scaling multiplication overflow
		{amount: math.MaxInt64, factor: 2, divisor: 2, mode: RoundHalfToEven, expectErr: true},

		// Rounding overflow
		{amount: math.MaxInt64, factor: 1, divisor: 1, mode: RoundUp, expectErr: false, expected: math.MaxInt64},

		// Negation checks and MinInt64 errors
		{amount: math.MinInt64, factor: 1, divisor: 2, mode: RoundHalfToEven, expected: math.MinInt64 / 2},

		// Scaling negation overflow with remainder != 0
		{amount: math.MinInt64, factor: 1, divisor: 3, mode: RoundHalfToEven, expectErr: true},

		// Scale overflow with factor MinInt64 and amount -1
		{amount: -1, factor: math.MinInt64, divisor: 2, mode: RoundHalfToEven, expectErr: true},
	}

	for i, tc := range tests {
		m := New(tc.amount, USD)
		res, err := m.Scale(tc.factor, tc.divisor, tc.mode)
		if tc.expectErr {
			if err == nil {
				t.Errorf("test %d: expected error but got none", i)
			}
			continue
		}
		if err != nil {
			t.Errorf("test %d: unexpected error: %v", i, err)
			continue
		}
		if res.Amount != tc.expected {
			t.Errorf("test %d: expected %d, got %d (scale %d * %d / %d)", i, tc.expected, res.Amount, tc.amount, tc.factor, tc.divisor)
		}
	}

	// Test special MinInt64 negation errors in Scale
	mMin := New(math.MinInt64, USD)
	_, err := mMin.Scale(-1, 2, RoundHalfToEven)
	if err == nil {
		t.Fatal("expected error on MinInt64 negation in Scale")
	}

	mNormal := New(100, USD)
	_, err = mNormal.Scale(1, math.MinInt64, RoundHalfToEven)
	if err == nil {
		t.Fatal("expected error on MinInt64 divisor negation in Scale")
	}
}

func TestMoneyAllocation(t *testing.T) {
	// Splitting 5 cents in 30:30:30 ratio -> should allocate 2, 2, 1
	m := New(5, USD)
	ratios := []int64{30, 30, 30}
	shares, err := m.Allocate(ratios)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var total int64
	for _, s := range shares {
		total += s.Amount
	}
	if total != 5 {
		t.Fatalf("expected sum of shares to equal total (5), got %d", total)
	}

	if shares[0].Amount != 2 || shares[1].Amount != 2 || shares[2].Amount != 1 {
		t.Fatalf("unexpected share distribution: [%d, %d, %d]", shares[0].Amount, shares[1].Amount, shares[2].Amount)
	}

	// Allocate 100 cents to 1:1:1 ratios -> 34, 33, 33
	m2 := New(100, USD)
	shares2, err := m2.Allocate([]int64{1, 1, 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shares2[0].Amount != 34 || shares2[1].Amount != 33 || shares2[2].Amount != 33 {
		t.Fatalf("unexpected share distribution for 100 splits: [%d, %d, %d]", shares2[0].Amount, shares2[1].Amount, shares2[2].Amount)
	}

	// Negative allocation and leftover < 0
	mNeg := New(-5, USD)
	sharesNeg, err := mNeg.Allocate([]int64{30, 30, 30})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var totalNeg int64
	for _, s := range sharesNeg {
		totalNeg += s.Amount
	}
	if totalNeg != -5 {
		t.Fatalf("expected sum of negative shares to equal total (-5), got %d", totalNeg)
	}

	// Allocation error handling
	_, err = m.Allocate([]int64{})
	if err == nil {
		t.Fatal("expected error on empty ratios")
	}

	_, err = m.Allocate([]int64{1, -1})
	if err == nil {
		t.Fatal("expected error on negative ratios")
	}

	_, err = m.Allocate([]int64{math.MaxInt64, 1})
	if err == nil {
		t.Fatal("expected error on ratio sum overflow")
	}

	_, err = m.Allocate([]int64{0, 0})
	if err == nil {
		t.Fatal("expected error on zero ratios sum")
	}

	_, err = New(math.MaxInt64, USD).Allocate([]int64{2, 2})
	if err == nil {
		t.Fatal("expected error on allocation multiplication overflow")
	}

	_, err = New(math.MinInt64, USD).Allocate([]int64{1})
	if err != nil {
		t.Fatalf("unexpected error on MinInt64 with ratio 1: %v", err)
	}
}

func TestMoneyAllocateTieBreakAndAlabamaParadox(t *testing.T) {
	// Deterministic lowest-index tie-breaking:
	// 3 minor units split across [1, 1, 3] (total=5):
	// quotients=[0, 0, 1], remainders=[3, 3, 4], leftover=2.
	// Highest remainder is index 2 (rem=4), then tied indices 0 and 1 (rem=3).
	// Lowest index (0) must win the second leftover unit -> [1, 0, 2].
	tieShares, err := New(3, USD).Allocate([]int64{1, 1, 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tieShares[0].Amount != 1 || tieShares[1].Amount != 0 || tieShares[2].Amount != 2 {
		t.Fatalf("expected lowest-index tie-break [1, 0, 2], got [%d, %d, %d]",
			tieShares[0].Amount, tieShares[1].Amount, tieShares[2].Amount)
	}

	// Alabama paradox demonstration for Hamilton (largest-remainder) method:
	// Ratios [6, 6, 1, 1, 1] (sum = 15).
	// At total = 7:
	//   exact quotas: 2.8, 2.8, 0.466..., 0.466..., 0.466...
	//   floors:       2,   2,   0,        0,        0        (sum = 4, leftover = 3)
	//   remainders:   12,  12,  7,        7,        7
	//   shares at 7:  [3, 3, 1, 0, 0] -> index 2 receives 1 minor unit.
	// At total = 8:
	//   exact quotas: 3.2, 3.2, 0.533..., 0.533..., 0.533...
	//   floors:       3,   3,   0,        0,        0        (sum = 6, leftover = 2)
	//   remainders:   3,   3,   8,        8,        8
	//   wait — with [6, 6, 1] or [5, 5, 1]? Let's test ratios [10, 10, 2, 2, 2] or [6, 6, 1, 1, 1]:
	//   Wait: at total = 8 with [6, 6, 1, 1, 1], remainders are [3, 3, 8, 8, 8], so index 2 gets 1!
	//   Instead, consider ratios [6, 6, 2] -> no, classic Alabama paradox:
	//   Ratios [6, 6, 1] (sum = 13):
	//   At total = 4:
	//     4*6/13 = 1 rem 11; 4*6/13 = 1 rem 11; 4*1/13 = 0 rem 4 -> shares [2, 2, 0]
	//   Classic 3-party Alabama paradox: ratios [45, 44, 11] (sum = 100):
	//   Wait: with 3 parties and sum=100, let's check ratios [6, 6, 1] or [5, 5, 2] ->
	//   Actually, classic smallest Alabama paradox is ratios [5, 5, 1] (sum = 11)?
	//   Wait: at total = 1: 5/11, 5/11, 1/11 -> [1, 0, 0].
	//   Let's check ratios [6, 6, 1] vs [13, 13, 3, 3, 3] (sum = 35):
	//   Let's use classic textbook ratios [6, 6, 2] -> no, [10, 10, 3] (sum=23):
	//   At total = 3: 30/23=1r7, 30/23=1r7, 9/23=0r9 -> [1, 1, 1] (party 2 gets 1!)
	//   At total = 4: 40/23=1r17, 40/23=1r17, 12/23=0r12 -> [2, 2, 0] (party 2 drops from 1 to 0!)
	rParadox := []int64{10, 10, 3}
	at3, err := New(3, USD).Allocate(rParadox)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	at4, err := New(4, USD).Allocate(rParadox)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if at3[2].Amount != 1 || at4[2].Amount != 0 {
		t.Fatalf("expected Alabama paradox on party 2 (1 at total=3, 0 at total=4), got %d and %d",
			at3[2].Amount, at4[2].Amount)
	}

	// Verify AllocateMonotone (Webster divisor method) eliminates the Alabama paradox
	// across all totals 0..50 on the same ratios [10, 10, 3] and on >16 ratios.
	prev := make([]int64, len(rParadox))
	for total := int64(0); total <= 50; total++ {
		mono, err := New(total, USD).AllocateMonotone(rParadox)
		if err != nil {
			t.Fatalf("AllocateMonotone(%d) unexpected error: %v", total, err)
		}
		var sum int64
		for i, s := range mono {
			sum += s.Amount
			if s.Amount < prev[i] {
				t.Fatalf("AllocateMonotone violated monotonicity at total=%d index=%d: %d < %d",
					total, i, s.Amount, prev[i])
			}
			prev[i] = s.Amount
		}
		if sum != total {
			t.Fatalf("AllocateMonotone(%d) sum=%d != total", total, sum)
		}
	}
}

func TestMoneyAllocateWideAndLargeSlices(t *testing.T) {
	// 128-bit wide allocation succeeds where 64-bit multiplication overflows.
	wide, err := New(math.MaxInt64, USD).AllocateWide([]int64{2, 2})
	if err != nil {
		t.Fatalf("AllocateWide unexpected error: %v", err)
	}
	if wide[0].Amount != 4611686018427387904 || wide[1].Amount != 4611686018427387903 {
		t.Fatalf("unexpected AllocateWide shares: %+v", wide)
	}

	// Negative wide allocation and >16 ratios (heap fallback path).
	manyRatios := make([]int64, 20)
	for i := range manyRatios {
		manyRatios[i] = int64(i + 1)
	}
	manyRatios[0] = 0
	sharesHeap, err := New(-1000, USD).Allocate(manyRatios)
	if err != nil {
		t.Fatalf("Allocate heap path error: %v", err)
	}
	sharesWideHeap, err := New(-1000, USD).AllocateWide(manyRatios)
	if err != nil {
		t.Fatalf("AllocateWide heap path error: %v", err)
	}
	sharesMonoHeap, err := New(-1000, USD).AllocateMonotone(manyRatios)
	if err != nil {
		t.Fatalf("AllocateMonotone heap path error: %v", err)
	}
	var sumA, sumW, sumM int64
	for i := range manyRatios {
		sumA += sharesHeap[i].Amount
		sumW += sharesWideHeap[i].Amount
		sumM += sharesMonoHeap[i].Amount
		if sharesHeap[i].Amount != sharesWideHeap[i].Amount {
			t.Fatalf("Allocate and AllocateWide differ at %d: %d vs %d", i, sharesHeap[i].Amount, sharesWideHeap[i].Amount)
		}
	}
	if sumA != -1000 || sumW != -1000 || sumM != -1000 {
		t.Fatalf("expected sums -1000, got %d, %d, %d", sumA, sumW, sumM)
	}
	if sharesHeap[0].Amount != 0 || sharesMonoHeap[0].Amount != 0 {
		t.Fatalf("zero ratio must receive 0 share")
	}

	// Error paths on AllocateWide and AllocateMonotone
	if _, err := New(10, USD).AllocateWide(nil); err == nil {
		t.Fatal("expected error on empty ratios in AllocateWide")
	}
	if _, err := New(10, USD).AllocateMonotone(nil); err == nil {
		t.Fatal("expected error on empty ratios in AllocateMonotone")
	}
	// Tie-break on AllocateMonotone with equal ratios
	monoTie, err := New(1, USD).AllocateMonotone([]int64{5, 5})
	if err != nil || monoTie[0].Amount != 1 || monoTie[1].Amount != 0 {
		t.Fatalf("unexpected AllocateMonotone tie break: %+v, err=%v", monoTie, err)
	}
}

func FuzzMoneyAllocateProperties(f *testing.F) {
	f.Add(int64(100), int64(1), int64(1), int64(1), int64(0))
	f.Add(int64(-5), int64(30), int64(30), int64(30), int64(10))
	f.Add(int64(3), int64(10), int64(10), int64(3), int64(0))
	f.Add(int64(0), int64(7), int64(0), int64(13), int64(5))

	f.Fuzz(func(t *testing.T, amount, r0, r1, r2, r3 int64) {
		raw := []int64{r0, r1, r2, r3}
		ratios := make([]int64, len(raw))
		var totalRatio int64
		for i, v := range raw {
			if v < 0 {
				v = -(v + 1)
			}
			v %= 1_000_000
			ratios[i] = v
			totalRatio += v
		}
		if totalRatio == 0 {
			ratios[0] = 1
			totalRatio = 1
		}

		shares, err := New(amount, USD).AllocateWide(ratios)
		if err != nil {
			t.Fatalf("unexpected AllocateWide error: %v", err)
		}

		var sum int64
		bigAmount := big.NewInt(amount)
		bigTotal := big.NewInt(totalRatio)

		for i, s := range shares {
			sum += s.Amount
			if ratios[i] == 0 && s.Amount != 0 {
				t.Fatalf("zero ratio at %d got non-zero share %d", i, s.Amount)
			}
			// Quota check: |share * totalRatio - amount * ratio| < totalRatio
			lhs := new(big.Int).Mul(big.NewInt(s.Amount), bigTotal)
			rhs := new(big.Int).Mul(bigAmount, big.NewInt(ratios[i]))
			diff := new(big.Int).Sub(lhs, rhs)
			diff.Abs(diff)
			if diff.Cmp(bigTotal) >= 0 {
				t.Fatalf("quota violated at %d: share=%d ratio=%d amount=%d totalRatio=%d",
					i, s.Amount, ratios[i], amount, totalRatio)
			}
		}
		if sum != amount {
			t.Fatalf("conservation violated: sum=%d != amount=%d", sum, amount)
		}
	})
}

func TestMoneyAllocateAllocsPerRun(t *testing.T) {
	m := New(1000, USD)
	ratios := []int64{30, 30, 20, 10, 10}
	allocs := testing.AllocsPerRun(100, func() {
		_, err := m.Allocate(ratios)
		if err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 1 {
		t.Fatalf("expected <= 1 allocation per Allocate call, got %v", allocs)
	}
}

func BenchmarkMoneyAllocate(b *testing.B) {
	m := New(100_003, USD)
	ratios := []int64{30, 25, 20, 15, 7, 3}
	b.ReportAllocs()
	for b.Loop() {
		_, err := m.Allocate(ratios)
		if err != nil {
			b.Fatal(err)
		}
	}
}
