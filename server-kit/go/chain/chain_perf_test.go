//go:build perf

package chain

import (
	"context"
	"testing"
)

func TestRunParallelAllocBudget(t *testing.T) {
	ops := []Operation[int]{
		{Name: "a", Run: func(context.Context) (int, error) { return 1, nil }},
		{Name: "b", Run: func(context.Context) (int, error) { return 2, nil }},
		{Name: "c", Run: func(context.Context) (int, error) { return 3, nil }},
	}
	allocs := testing.AllocsPerRun(100, func() {
		RunParallel(context.Background(), ops)
	})
	if allocs > 8 {
		t.Fatalf("parallel chain allocation budget exceeded: got %0.1f allocs/run, want <= 8", allocs)
	}

	results := make([]Result[int], 0, len(ops))
	intoAllocs := testing.AllocsPerRun(100, func() {
		results = RunParallelInto(context.Background(), ops, results)
	})
	if intoAllocs > 7 {
		t.Fatalf("parallel chain caller-owned allocation budget exceeded: got %0.1f allocs/run, want <= 7", intoAllocs)
	}
}
