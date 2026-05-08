package slo

import "testing"

func TestEvaluateReportsBreaches(t *testing.T) {
	breaches := Evaluate(
		Config{DispatchP99LatencyMS: 100, WorkerSuccessRate: 0.999, EventDeliveryLagMS: 500},
		Observation{DispatchP99LatencyMS: 120, WorkerSuccessRate: 0.990, EventDeliveryLagMS: 100},
	)
	if len(breaches) != 2 {
		t.Fatalf("breaches = %d, want 2: %+v", len(breaches), breaches)
	}
	if breaches[0].Name != "dispatch_p99_latency_ms" || breaches[1].Name != "worker_success_rate" {
		t.Fatalf("unexpected breaches: %+v", breaches)
	}
}

func TestEvaluateLagAndBreachError(t *testing.T) {
	breaches := Evaluate(
		Config{DispatchP99LatencyMS: 100, WorkerSuccessRate: 0.99, EventDeliveryLagMS: 500},
		Observation{DispatchP99LatencyMS: 90, WorkerSuccessRate: 0.995, EventDeliveryLagMS: 600},
	)
	if len(breaches) != 1 || breaches[0].Name != "event_delivery_lag_ms" {
		t.Fatalf("unexpected breaches: %+v", breaches)
	}
	if got := breaches[0].Error(); got == "" {
		t.Fatalf("expected breach error string")
	}
	if got := Evaluate(Config{}, Observation{DispatchP99LatencyMS: 1, WorkerSuccessRate: 0, EventDeliveryLagMS: 1}); len(got) != 0 {
		t.Fatalf("disabled thresholds should not breach: %+v", got)
	}
}
