package worker

// Conformance test linking the WorkerRetryQueue TLA spec
// (docs/specs/tla/WorkerRetryQueue.tla) to the real worker implementation.
//
// The spec's RetryBudgetBounded invariant -- attempts[j] <= MaxAttempts -- is
// enforced in production code by Job.Validate (job.go: rejects
// "attempt N exceeds max_attempts M") and by the engine's exhaustion decision
// (engine.go: `if job.Attempt >= job.MaxAttempts`). TLC proves no reachable
// model state violates the invariant; this test proves the real code refuses to
// enter the violating state on real inputs.

import "testing"

// TestConformanceWorkerRetryBudgetBounded drives the real Job.Validate across
// the budget boundary. A job within budget must validate; a job past budget
// must be rejected -- that rejection is the concrete enforcement the TLA
// RetryBudgetBounded invariant abstracts.
func TestConformanceWorkerRetryBudgetBounded(t *testing.T) {
	for maxAttempts := 1; maxAttempts <= 5; maxAttempts++ {
		for attempt := 0; attempt <= maxAttempts+2; attempt++ {
			job := Job{
				JobKind:     "conformance",
				Queue:       "default",
				MaxAttempts: maxAttempts,
				Attempt:     attempt,
			}
			err := job.Validate()
			withinBudget := attempt <= maxAttempts
			switch {
			case withinBudget && err != nil:
				t.Fatalf("within-budget job (attempt=%d max=%d) rejected: %v",
					attempt, maxAttempts, err)
			case !withinBudget && err == nil:
				t.Fatalf("over-budget job (attempt=%d max=%d) accepted; "+
					"RetryBudgetBounded is not enforced by Job.Validate",
					attempt, maxAttempts)
			}
		}
	}
}
