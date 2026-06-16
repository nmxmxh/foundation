package money

import (
	"math"
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
}
