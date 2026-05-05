package worker

import (
	"testing"
	"time"
)

func TestJobNextBackoffCapsLargeAttempts(t *testing.T) {
	job := Job{Attempt: 10_000}
	backoff := job.NextBackoff()
	if backoff < 0 {
		t.Fatalf("backoff must not be negative: %s", backoff)
	}
	if backoff > 45*time.Second {
		t.Fatalf("backoff exceeded jittered cap: %s", backoff)
	}
}
