package slo

import "fmt"

type Config struct {
	DispatchP99LatencyMS float64
	WorkerSuccessRate    float64
	EventDeliveryLagMS   float64
}

type Observation struct {
	DispatchP99LatencyMS float64
	WorkerSuccessRate    float64
	EventDeliveryLagMS   float64
}

type Breach struct {
	Name      string
	Observed  float64
	Threshold float64
}

func Evaluate(cfg Config, observed Observation) []Breach {
	var breaches []Breach
	if cfg.DispatchP99LatencyMS > 0 && observed.DispatchP99LatencyMS > cfg.DispatchP99LatencyMS {
		breaches = append(breaches, Breach{"dispatch_p99_latency_ms", observed.DispatchP99LatencyMS, cfg.DispatchP99LatencyMS})
	}
	if cfg.WorkerSuccessRate > 0 && observed.WorkerSuccessRate < cfg.WorkerSuccessRate {
		breaches = append(breaches, Breach{"worker_success_rate", observed.WorkerSuccessRate, cfg.WorkerSuccessRate})
	}
	if cfg.EventDeliveryLagMS > 0 && observed.EventDeliveryLagMS > cfg.EventDeliveryLagMS {
		breaches = append(breaches, Breach{"event_delivery_lag_ms", observed.EventDeliveryLagMS, cfg.EventDeliveryLagMS})
	}
	return breaches
}

func (b Breach) Error() string {
	return fmt.Sprintf("%s breach: observed=%g threshold=%g", b.Name, b.Observed, b.Threshold)
}
