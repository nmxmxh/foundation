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
	if !HasCriticalFailureOrdered(ops, results) {
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
	if HasCriticalFailureOrdered(ops, results) {
		t.Fatalf("did not expect critical failure")
	}
	if results[1].Value != "ok" {
		t.Fatalf("required value = %q", results[1].Value)
	}
}

func TestRunParallelPropagatesParentCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	ops := []Operation[struct{}]{
		{Name: "wait", Run: func(ctx context.Context) (struct{}, error) {
			select {
			case <-ctx.Done():
				return struct{}{}, ctx.Err()
			case <-time.After(time.Second):
				t.Fatalf("operation did not observe parent cancellation")
				return struct{}{}, nil
			}
		}},
		{Name: "ready", Run: func(context.Context) (struct{}, error) {
			return struct{}{}, nil
		}},
	}
	results := RunParallelInto(parent, ops, make([]Result[struct{}], 0, len(ops)))
	if !errors.Is(results[0].Error, context.Canceled) {
		t.Fatalf("parent cancellation error = %v, want context.Canceled", results[0].Error)
	}
}

func TestRunParallelSingleNilAndNilContextBranches(t *testing.T) {
	nilCtx := nilTestContext()
	if got := RunParallel[int](nilCtx, nil); got != nil {
		t.Fatalf("empty operations = %+v, want nil", got)
	}
	results := RunParallel[int](nilCtx, []Operation[int]{{Name: "missing"}})
	if len(results) != 1 || results[0].Name != "missing" || results[0].Error == nil {
		t.Fatalf("single nil operation result = %+v", results)
	}
	results = RunParallel[int](nilCtx, []Operation[int]{{Name: "one", Run: func(ctx context.Context) (int, error) {
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

func nilTestContext() context.Context {
	return nil
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

func TestHasCriticalFailureOrdered(t *testing.T) {
	criticalErr := errors.New("critical")
	ops := []Operation[int]{
		{Name: "a", Run: func(context.Context) (int, error) { return 1, nil }},
		{Name: "b", Critical: true, Run: func(context.Context) (int, error) { return 0, criticalErr }},
		{Name: "c", Critical: true, Run: func(context.Context) (int, error) { return 3, nil }},
	}
	results := RunParallelInto(context.Background(), ops, make([]Result[int], 0, len(ops)))
	if !HasCriticalFailureOrdered(ops, results) {
		t.Fatalf("expected ordered critical failure")
	}
	if !HasCriticalFailure(ops, []Result[int]{{Name: "b", Error: criticalErr}}) {
		t.Fatalf("expected name-based critical failure for filtered result")
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

func BenchmarkHasCriticalFailure(b *testing.B) {
	err := errors.New("critical")
	ops := []Operation[int]{
		{Name: "a", Critical: true},
		{Name: "b", Critical: true},
		{Name: "c"},
	}
	results := []Result[int]{
		{Name: "a"},
		{Name: "b", Error: err},
		{Name: "c"},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		HasCriticalFailure(ops, results)
	}
}

func BenchmarkHasCriticalFailureOrdered(b *testing.B) {
	err := errors.New("critical")
	ops := []Operation[int]{
		{Name: "a", Critical: true},
		{Name: "b", Critical: true},
		{Name: "c"},
	}
	results := []Result[int]{
		{Name: "a"},
		{Name: "b", Error: err},
		{Name: "c"},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		HasCriticalFailureOrdered(ops, results)
	}
}
