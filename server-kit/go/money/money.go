package money

import (
	"errors"
	"fmt"
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

// Allocate splits the money proportional to the given ratios, conserving the total (MU-4).
// Uses the largest-remainder method.
func (m Money) Allocate(ratios []int64) ([]Money, error) {
	if len(ratios) == 0 {
		return nil, errors.New("cannot allocate to zero ratios")
	}

	var totalRatio int64
	for _, r := range ratios {
		if r < 0 {
			return nil, errors.New("allocation ratios must be non-negative")
		}
		next := totalRatio + r
		if next < totalRatio {
			return nil, errors.New("allocation ratio sum overflow")
		}
		totalRatio = next
	}

	if totalRatio == 0 {
		return nil, errors.New("sum of allocation ratios must be greater than zero")
	}

	shares := make([]Money, len(ratios))
	remainders := make([]struct {
		index     int
		remainder int64
	}, len(ratios))

	var allocatedAmount int64
	for i, r := range ratios {
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
		remainders[i] = struct {
			index     int
			remainder int64
		}{index: i, remainder: rem}

		allocatedAmount += share
	}

	leftover := m.Amount - allocatedAmount
	if leftover != 0 {
		// Sort remainders descending (simple stable sort to preserve order for matching remainders)
		for i := 0; i < len(remainders)-1; i++ {
			for j := i + 1; j < len(remainders); j++ {
				if remainders[i].remainder < remainders[j].remainder {
					remainders[i], remainders[j] = remainders[j], remainders[i]
				}
			}
		}

		step := int64(1)
		if leftover < 0 {
			step = -1
		}

		for leftover != 0 {
			for i := 0; i < len(remainders) && leftover != 0; i++ {
				idx := remainders[i].index
				shares[idx].Amount += step
				leftover -= step
			}
		}
	}

	// Post-condition invariant: sum of shares must equal total
	var sum int64
	for _, s := range shares {
		sum += s.Amount
	}
	if sum != m.Amount {
		return nil, fmt.Errorf("allocation invariant violated: sum=%d, total=%d", sum, m.Amount)
	}

	return shares, nil
}
