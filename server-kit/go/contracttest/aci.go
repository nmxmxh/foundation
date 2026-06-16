package contracttest

import (
	"testing"
)

// AssertCommutative asserts that a merge operation is commutative: Merge(a, b) == Merge(b, a)
func AssertCommutative[T any](t *testing.T, merge func(T, T) T, a, b T, equals func(T, T) bool) {
	t.Helper()
	r1 := merge(a, b)
	r2 := merge(b, a)
	if !equals(r1, r2) {
		t.Errorf("Commutativity violated:\nMerge(%v, %v) = %v\nMerge(%v, %v) = %v", a, b, r1, b, a, r2)
	}
}

// AssertAssociative asserts that a merge operation is associative: Merge(Merge(a, b), c) == Merge(a, Merge(b, c))
func AssertAssociative[T any](t *testing.T, merge func(T, T) T, a, b, c T, equals func(T, T) bool) {
	t.Helper()
	r1 := merge(merge(a, b), c)
	r2 := merge(a, merge(b, c))
	if !equals(r1, r2) {
		t.Errorf("Associativity violated:\nMerge(Merge(%v, %v), %v) = %v\nMerge(%v, Merge(%v, %v)) = %v", a, b, c, r1, a, b, c, r2)
	}
}

// AssertIdempotent asserts that a merge operation is idempotent: Merge(a, a) == a
func AssertIdempotent[T any](t *testing.T, merge func(T, T) T, a T, equals func(T, T) bool) {
	t.Helper()
	r := merge(a, a)
	if !equals(r, a) {
		t.Errorf("Idempotency violated:\nMerge(%v, %v) = %v\nExpected = %v", a, a, r, a)
	}
}

// AssertACILaws asserts commutativity, associativity, and idempotency for the given merge function and inputs.
func AssertACILaws[T any](t *testing.T, merge func(T, T) T, inputs []T, equals func(T, T) bool) {
	t.Helper()
	n := len(inputs)
	if n < 3 {
		t.Fatalf("AssertACILaws requires at least 3 inputs, got %d", n)
	}
	for i := range n {
		AssertIdempotent(t, merge, inputs[i], equals)
		for j := range n {
			AssertCommutative(t, merge, inputs[i], inputs[j], equals)
			for k := range n {
				AssertAssociative(t, merge, inputs[i], inputs[j], inputs[k], equals)
			}
		}
	}
}
