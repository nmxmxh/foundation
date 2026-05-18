package chain

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunParallelCancelsChainOnCriticalFailure(t *testing.T) {
	criticalErr := errors.New("critical")
	ops := []Operation[string]{
		{
			Name:     "critical",
			Critical: true,
			Run: func(context.Context) (string, error) {
				return "", criticalErr
			},
		},
		{
			Name: "dependent",
			Run: func(ctx context.Context) (string, error) {
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(time.Second):
					return "late", nil
				}
			},
		},
	}

	results := RunParallel(context.Background(), ops)
	if !HasCriticalFailure(ops, results) {
		t.Fatalf("expected critical failure")
	}
	if results[1].Error == nil {
		t.Fatalf("expected dependent operation to observe cancellation")
	}
}

func TestRunParallelKeepsMovingOnNonCriticalFailure(t *testing.T) {
	ops := []Operation[string]{
		{Name: "optional", Run: func(context.Context) (string, error) { return "", errors.New("optional") }},
		{Name: "required", Critical: true, Run: func(context.Context) (string, error) { return "ok", nil }},
	}
	results := RunParallel(context.Background(), ops)
	if HasCriticalFailure(ops, results) {
		t.Fatalf("did not expect critical failure")
	}
	if results[1].Value != "ok" {
		t.Fatalf("required value = %q", results[1].Value)
	}
}

func TestRunParallelSingleNilAndNilContextBranches(t *testing.T) {
	if got := RunParallel[int](nil, nil); got != nil {
		t.Fatalf("empty operations = %+v, want nil", got)
	}
	results := RunParallel[int](nil, []Operation[int]{{Name: "missing"}})
	if len(results) != 1 || results[0].Name != "missing" || results[0].Error == nil {
		t.Fatalf("single nil operation result = %+v", results)
	}
	results = RunParallel[int](nil, []Operation[int]{{Name: "one", Run: func(ctx context.Context) (int, error) {
		if ctx == nil {
			t.Fatalf("expected default context")
		}
		return 42, nil
	}}})
	if len(results) != 1 || results[0].Value != 42 || results[0].Error != nil {
		t.Fatalf("single operation result = %+v", results)
	}
	results = RunParallel[int](context.Background(), []Operation[int]{
		{Name: "critical", Critical: true},
		{Name: "other", Run: func(context.Context) (int, error) { return 1, nil }},
	})
	if !HasCriticalFailure([]Operation[int]{{Name: "critical", Critical: true}}, results) {
		t.Fatalf("expected critical failure from nil run function")
	}
}

func TestRunParallelIntoReusesCallerStorage(t *testing.T) {
	ops := []Operation[int]{
		{Name: "a", Critical: true, Run: func(context.Context) (int, error) { return 1, nil }},
		{Name: "b", Run: func(context.Context) (int, error) { return 2, nil }},
	}
	results := make([]Result[int], 0, len(ops))
	results = RunParallelInto(context.Background(), ops, results)
	if len(results) != len(ops) || cap(results) != len(ops) {
		t.Fatalf("result shape = len %d cap %d, want len/cap %d", len(results), cap(results), len(ops))
	}
	if results[0].Name != "a" || results[0].Value != 1 || results[0].Error != nil {
		t.Fatalf("result[0] = %+v", results[0])
	}
	if results[1].Name != "b" || results[1].Value != 2 || results[1].Error != nil {
		t.Fatalf("result[1] = %+v", results[1])
	}
	empty := RunParallelInto[int](context.Background(), nil, results)
	if empty == nil || len(empty) != 0 || cap(empty) != cap(results) {
		t.Fatalf("empty reused result shape = len %d cap %d", len(empty), cap(empty))
	}
}

func BenchmarkRunParallel(b *testing.B) {
	ops := []Operation[int]{
		{Name: "a", Critical: true, Run: func(context.Context) (int, error) { return 1, nil }},
		{Name: "b", Critical: true, Run: func(context.Context) (int, error) { return 2, nil }},
		{Name: "c", Run: func(context.Context) (int, error) { return 3, nil }},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		RunParallel(context.Background(), ops)
	}
}

func BenchmarkRunParallelInto(b *testing.B) {
	ops := []Operation[int]{
		{Name: "a", Critical: true, Run: func(context.Context) (int, error) { return 1, nil }},
		{Name: "b", Critical: true, Run: func(context.Context) (int, error) { return 2, nil }},
		{Name: "c", Run: func(context.Context) (int, error) { return 3, nil }},
	}
	results := make([]Result[int], 0, len(ops))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		results = RunParallelInto(context.Background(), ops, results)
	}
}
