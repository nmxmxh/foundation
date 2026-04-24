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
