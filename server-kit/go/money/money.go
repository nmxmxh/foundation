package money

import (
	"errors"
	"fmt"
	"math/bits"
)

// Currency represents an ISO 4217 currency definition.
type Currency struct {
	Code     string
	Exponent int // number of decimal places, e.g., 2 for USD, 0 for JPY
}

// Common Currencies defined for convenience.
var (
	USD = Currency{Code: "USD", Exponent: 2}
	EUR = Currency{Code: "EUR", Exponent: 2}
	GBP = Currency{Code: "GBP", Exponent: 2}
	JPY = Currency{Code: "JPY", Exponent: 0}
	KWD = Currency{Code: "KWD", Exponent: 3}
)

// Money represents a monetary value in a specific currency.
type Money struct {
	Amount   int64 // Value in the smallest minor unit
	Currency Currency
}

// New creates a new Money instance.
func New(amount int64, currency Currency) Money {
	return Money{Amount: amount, Currency: currency}
}

// Add adds another Money value of the identical currency with overflow checks (MU-2, MU-5).
func (m Money) Add(other Money) (Money, error) {
	if m.Currency.Code != other.Currency.Code {
		return Money{}, fmt.Errorf("cannot add mismatching currencies: %s and %s", m.Currency.Code, other.Currency.Code)
	}

	sum := m.Amount + other.Amount
	// Overflow occurs if signs of inputs are same and sign of sum is different from inputs.
	if (m.Amount^sum) < 0 && (other.Amount^sum) < 0 {
		return Money{}, errors.New("money addition overflow")
	}

	return Money{Amount: sum, Currency: m.Currency}, nil
}

// Sub subtracts another Money value of the identical currency with overflow checks (MU-2, MU-5).
func (m Money) Sub(other Money) (Money, error) {
	if m.Currency.Code != other.Currency.Code {
		return Money{}, fmt.Errorf("cannot subtract mismatching currencies: %s and %s", m.Currency.Code, other.Currency.Code)
	}

	diff := m.Amount - other.Amount
	// Overflow occurs if signs of m.Amount and other.Amount are different,
	// and the sign of diff is different from the sign of m.Amount.
	if (m.Amount^other.Amount) < 0 && (diff^m.Amount) < 0 {
		return Money{}, errors.New("money subtraction overflow")
	}

	return Money{Amount: diff, Currency: m.Currency}, nil
}

// Mul multiplies the money by an integer scalar with overflow checks (MU-2).
func (m Money) Mul(scalar int64) (Money, error) {
	if scalar == 0 || m.Amount == 0 {
		return Money{Amount: 0, Currency: m.Currency}, nil
	}

	if (m.Amount == -9223372036854775808 && scalar == -1) || (scalar == -9223372036854775808 && m.Amount == -1) {
		return Money{}, errors.New("money multiplication overflow")
	}

	prod := m.Amount * scalar
	if prod/scalar != m.Amount {
		return Money{}, errors.New("money multiplication overflow")
	}

	return Money{Amount: prod, Currency: m.Currency}, nil
}

// RoundingMode defines the policy for rounding fractional remainders (MU-3).
type RoundingMode int

const (
	// RoundHalfToEven rounds half-values to the nearest even number (Banker's rounding).
	RoundHalfToEven RoundingMode = iota
	// RoundHalfUp rounds half-values up (away from zero for positive, or toward positive).
	RoundHalfUp
	// RoundDown truncates the fraction (toward zero).
	RoundDown
	// RoundUp rounds away from zero.
	RoundUp
)

// Scale multiplies the amount by a factor and divides by a divisor, using the specified rounding mode.
// Useful for tax, split, fee, or rate calculations.
func (m Money) Scale(factor int64, divisor int64, mode RoundingMode) (Money, error) {
	if divisor == 0 {
		return Money{}, errors.New("divisor cannot be zero")
	}

	if (m.Amount == -9223372036854775808 && factor == -1) || (factor == -9223372036854775808 && m.Amount == -1) {
		return Money{}, errors.New("money scaling multiplication overflow")
	}

	// Checked multiplication of Amount * factor
	prod := m.Amount * factor
	if factor != 0 && prod/factor != m.Amount {
		return Money{}, errors.New("money scaling multiplication overflow")
	}

	quotient := prod / divisor
	remainder := prod % divisor

	if remainder == 0 {
		return Money{Amount: quotient, Currency: m.Currency}, nil
	}

	absProd := prod
	if absProd < 0 {
		if absProd == -9223372036854775808 { // MinInt64 negation overflow
			return Money{}, errors.New("scaling negation overflow")
		}
		absProd = -absProd
	}

	absDivisor := divisor
	if absDivisor < 0 {
		if absDivisor == -9223372036854775808 {
			return Money{}, errors.New("scaling negation overflow")
		}
		absDivisor = -absDivisor
	}

	absQuotient := absProd / absDivisor
	absRemainder := absProd % absDivisor

	roundUp := false
	switch mode {
	case RoundDown:
		roundUp = false
	case RoundUp:
		roundUp = true
	case RoundHalfUp:
		// round up if remainder * 2 >= divisor
		roundUp = absRemainder*2 >= absDivisor
	case RoundHalfToEven:
		// round up if remainder * 2 > divisor.
		// if remainder * 2 == divisor, round up only if quotient is odd (so result becomes even).
		half := absRemainder * 2
		if half > absDivisor {
			roundUp = true
		} else if half == absDivisor {
			roundUp = (absQuotient % 2) != 0
		}
	}

	finalAbs := absQuotient
	if roundUp {
		finalAbs++
		if finalAbs < 0 { // overflow
			return Money{}, errors.New("money rounding overflow")
		}
	}

	finalAmount := finalAbs
	// Apply correct sign
	if (prod < 0) != (divisor < 0) {
		finalAmount = -finalAmount
	}

	return Money{Amount: finalAmount, Currency: m.Currency}, nil
}

// Allocate splits the money proportional to the given ratios and conserves the total (MU-4).
//
// It uses the Hamilton (largest-remainder) method: each party receives its truncated
// ideal share, and leftover minor units go one at a time in descending remainder order,
// breaking ties in favor of the lowest index.
//
// Mathematical properties:
//   - Conservation: the sum of shares equals m.Amount.
//   - Quota property: every share is within 1 minor unit of its exact rational ideal.
//   - Alabama paradox (Balinski-Young theorem): increasing m.Amount can decrease an
//     individual share by 1 minor unit because no method satisfies both quota and
//     house monotonicity. For monotonic incremental splits, use AllocateMonotone.
func (m Money) Allocate(ratios []int64) ([]Money, error) {
	totalRatio, err := sumRatios(ratios)
	if err != nil {
		return nil, err
	}

	n := len(ratios)
	shares := make([]Money, n)

	const stackCap = 16
	var remStack [stackCap]int64
	var remainders []int64
	if n > stackCap {
		remainders = make([]int64, n)
	} else {
		remainders = remStack[:n]
	}

	var allocatedAmount int64
	for i, r := range ratios {
		if (m.Amount == -9223372036854775808 && r == -1) || (r == -9223372036854775808 && m.Amount == -1) {
			return nil, errors.New("allocation multiplication overflow")
		}
		prod := m.Amount * r
		if r != 0 && prod/r != m.Amount {
			return nil, errors.New("allocation multiplication overflow")
		}

		share := prod / totalRatio
		rem := prod % totalRatio
		if rem < 0 {
			rem = -rem
		}

		shares[i] = Money{Amount: share, Currency: m.Currency}
		remainders[i] = rem
		allocatedAmount += share
	}

	distributeLeftover(shares, remainders, m.Amount-allocatedAmount)
	return shares, nil
}

// AllocateWide splits the money proportional to the given ratios using 128-bit
// intermediate arithmetic (math/bits.Mul64 and Div64) so that large amounts and
// ratios do not overflow 64-bit multiplication before division by the ratio sum.
func (m Money) AllocateWide(ratios []int64) ([]Money, error) {
	totalRatio, err := sumRatios(ratios)
	if err != nil {
		return nil, err
	}

	n := len(ratios)
	shares := make([]Money, n)

	const stackCap = 16
	var remStack [stackCap]int64
	var remainders []int64
	if n > stackCap {
		remainders = make([]int64, n)
	} else {
		remainders = remStack[:n]
	}

	absAmount, neg := absUint64(m.Amount)
	uTotal := uint64(totalRatio) // #nosec G115 -- totalRatio is validated positive by sumRatios.

	var allocatedAmount int64
	for i, r := range ratios {
		hi, lo := bits.Mul64(absAmount, uint64(r)) // #nosec G115 -- r is validated non-negative by sumRatios.
		q, rem := bits.Div64(hi, lo, uTotal)
		share := int64(q) // #nosec G115 -- q is bounded by absAmount <= math.MaxInt64.
		if neg {
			share = -share
		}
		shares[i] = Money{Amount: share, Currency: m.Currency}
		remainders[i] = int64(rem) // #nosec G115 -- rem is bounded by uTotal <= math.MaxInt64.
		allocatedAmount += share
	}

	distributeLeftover(shares, remainders, m.Amount-allocatedAmount)
	return shares, nil
}

// AllocateMonotone splits the money proportional to the given ratios using the
// Webster / Sainte-Lague highest-averages divisor method.
//
// Unlike Allocate (Hamilton), AllocateMonotone is house-monotone: increasing the
// total amount never reduces any recipient's share (no Alabama paradox). By the
// Balinski-Young impossibility theorem, it can deviate from the ideal quota when
// ratios are highly skewed.
func (m Money) AllocateMonotone(ratios []int64) ([]Money, error) {
	totalRatio, err := sumRatios(ratios)
	if err != nil {
		return nil, err
	}

	n := len(ratios)
	shares := make([]Money, n)

	absAmount, neg := absUint64(m.Amount)
	uTotal := uint64(totalRatio) // #nosec G115 -- totalRatio is validated positive by sumRatios.

	const stackCap = 16
	var countStack [stackCap]uint64
	var counts []uint64
	if n > stackCap {
		counts = make([]uint64, n)
	} else {
		counts = countStack[:n]
	}

	// Safe lower-bound jump: start from max(0, floor(absAmount*r/totalRatio) - 1)
	// so the divisor step loop runs at most O(n) iterations instead of O(absAmount).
	var assigned uint64
	for i, r := range ratios {
		if r == 0 {
			continue
		}
		hi, lo := bits.Mul64(absAmount, uint64(r)) // #nosec G115 -- r is validated non-negative by sumRatios.
		q, _ := bits.Div64(hi, lo, uTotal)
		if q > 1 {
			base := q - 1
			counts[i] = base
			assigned += base
		}
	}

	for assigned < absAmount {
		bestIdx := -1
		for i, r := range ratios {
			if r == 0 {
				continue
			}
			// #nosec G115 -- ratios are validated non-negative by sumRatios.
			if bestIdx == -1 || websterHigherPriority(uint64(r), counts[i], uint64(ratios[bestIdx]), counts[bestIdx]) {
				bestIdx = i
			}
		}
		counts[bestIdx]++
		assigned++
	}

	for i, c := range counts {
		amt := int64(c) // #nosec G115 -- c is bounded by absAmount <= math.MaxInt64.
		if neg {
			amt = -amt
		}
		shares[i] = Money{Amount: amt, Currency: m.Currency}
	}
	return shares, nil
}

func sumRatios(ratios []int64) (int64, error) {
	if len(ratios) == 0 {
		return 0, errors.New("cannot allocate to zero ratios")
	}
	var totalRatio int64
	for _, r := range ratios {
		if r < 0 {
			return 0, errors.New("allocation ratios must be non-negative")
		}
		next := totalRatio + r
		if next < totalRatio {
			return 0, errors.New("allocation ratio sum overflow")
		}
		totalRatio = next
	}
	if totalRatio == 0 {
		return 0, errors.New("sum of allocation ratios must be greater than zero")
	}
	return totalRatio, nil
}

func absUint64(v int64) (uint64, bool) {
	if v < 0 {
		return uint64(^v) + 1, true // #nosec G115 -- bitwise NOT is non-negative for negative v.
	}
	return uint64(v), false // #nosec G115 -- v is non-negative.
}

func distributeLeftover(shares []Money, remainders []int64, leftover int64) {
	if leftover == 0 {
		return
	}
	step := int64(1)
	count := leftover
	if leftover < 0 {
		step = -1
		count = -leftover
	}
	// Each party has 0 <= rem < totalRatio, so 0 <= count < len(shares).
	// Select the top `count` remainders directly, breaking ties by lowest index.
	for k := int64(0); k < count; k++ {
		bestIdx := 0
		bestRem := int64(-1)
		for i, rem := range remainders {
			if rem > bestRem {
				bestRem = rem
				bestIdx = i
			}
		}
		shares[bestIdx].Amount += step
		remainders[bestIdx] = -1
	}
}

// websterHigherPriority reports whether ratioA/(2*countA+1) > ratioB/(2*countB+1)
// using exact 128-bit cross-multiplication. Ties return false so the lower index wins.
func websterHigherPriority(ratioA, countA, ratioB, countB uint64) bool {
	denB := 2*countB + 1
	denA := 2*countA + 1
	hiA, loA := bits.Mul64(ratioA, denB)
	hiB, loB := bits.Mul64(ratioB, denA)
	if hiA != hiB {
		return hiA > hiB
	}
	return loA > loB
}
