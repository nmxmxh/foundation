package worker

import (
	"context"
	"sync"
)

// ScalingPredictor defines the interface for pre-emptive worker scaling.
type ScalingPredictor interface {
	Predict(ctx context.Context, queue string, currentDepth int, currentWorkers int) int
}

// TrendPredictor uses a simple derivative-based signal to predict future queue depth.
// This is the "Phase 3" baseline for ML-driven scaling.
type TrendPredictor struct {
	mu      sync.Mutex
	history map[string][]int
}

func NewTrendPredictor() *TrendPredictor {
	return &TrendPredictor{
		history: make(map[string][]int),
	}
}

func (p *TrendPredictor) Predict(_ context.Context, queue string, currentDepth int, currentWorkers int) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	history := p.history[queue]
	history = append(history, currentDepth)
	if len(history) > 10 {
		history = history[1:]
	}
	p.history[queue] = history

	if len(history) < 3 {
		return 0
	}

	// Calculate velocity (rate of change)
	last := history[len(history)-1]
	prev := history[len(history)-2]
	velocity := last - prev

	// If velocity is positive and accelerating, suggest pre-emptive workers
	if velocity > 50 && currentWorkers < 64 {
		// Pre-emptively add workers proportional to velocity
		preemptive := velocity / 100
		if preemptive == 0 {
			preemptive = 1
		}
		return preemptive
	}

	return 0
}
