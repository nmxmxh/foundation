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
