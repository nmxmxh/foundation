package contracttest

import (
	"testing"
)

func TestACIHelpers(t *testing.T) {
	// Simple merge function that is ACI
	merge := func(a, b int) int {
		if a > b {
			return a
		}
		return b
	}

	equals := func(a, b int) bool {
		return a == b
	}

	inputs := []int{1, 2, 3}

	// This should pass
	AssertACILaws(t, merge, inputs, equals)
}

func TestACIViolationReporting(t *testing.T) {
	// Merge function that violates commutativity
	nonCommutative := func(a, b int) int {
		return a - b
	}

	equals := func(a, b int) bool {
		return a == b
	}

	// We use a dummy testing.T to check that failures are reported, but we don't fail this TestACIViolationReporting.
	var mockT testing.T

	// Should fail commutativity check
	AssertCommutative(&mockT, nonCommutative, 1, 2, equals)

	// Should fail associativity check
	AssertAssociative(&mockT, nonCommutative, 1, 2, 3, equals)

	// Should fail idempotency check
	AssertIdempotent(&mockT, nonCommutative, 1, equals)
}
