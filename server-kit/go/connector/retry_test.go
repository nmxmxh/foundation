package connector

import "testing"

func TestZeroValueRetryAdoptsDefaultPolicy(t *testing.T) {
	got := RetryConfig{}.normalized()
	want := DefaultRetryConfig()
	if got != want {
		t.Fatalf("zero-value RetryConfig normalized to %+v, want DefaultRetryConfig %+v", got, want)
	}
	if one := (RetryConfig{MaxAttempts: 1}).normalized(); one.MaxAttempts != 1 {
		t.Fatalf("MaxAttempts:1 must stay a single attempt, got %d", one.MaxAttempts)
	}
}
