package chaos

import (
	"context"
	"errors"
	"hash/fnv"
	"sync"
	"time"
)

var ErrInjectedFailure = errors.New("chaos injected failure")

type Rule struct {
	Latency     time.Duration
	FailureRate float64
	Partition   bool
}

type Injector struct {
	mu    sync.RWMutex
	rules map[string]Rule
}

func NewInjector() *Injector {
	return &Injector{rules: map[string]Rule{}}
}

func (i *Injector) InjectLatency(target string, latency time.Duration) {
	i.set(target, func(rule Rule) Rule { rule.Latency = latency; return rule })
}

func (i *Injector) InjectFailure(target string, rate float64) {
	if rate < 0 {
		rate = 0
	}
	if rate > 1 {
		rate = 1
	}
	i.set(target, func(rule Rule) Rule { rule.FailureRate = rate; return rule })
}

func (i *Injector) InjectPartition(target string) {
	i.set(target, func(rule Rule) Rule { rule.Partition = true; return rule })
}

func (i *Injector) Clear(target string) {
	i.mu.Lock()
	delete(i.rules, target)
	i.mu.Unlock()
}

func (i *Injector) Apply(ctx context.Context, target, operationID string) error {
	if i == nil {
		return nil
	}
	i.mu.RLock()
	rule := i.rules[target]
	i.mu.RUnlock()
	if rule.Partition {
		return ErrInjectedFailure
	}
	if rule.Latency > 0 {
		timer := time.NewTimer(rule.Latency)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if shouldFail(target, operationID, rule.FailureRate) {
		return ErrInjectedFailure
	}
	return nil
}

func (i *Injector) set(target string, mutate func(Rule) Rule) {
	i.mu.Lock()
	i.rules[target] = mutate(i.rules[target])
	i.mu.Unlock()
}

func shouldFail(target, operationID string, rate float64) bool {
	if rate <= 0 {
		return false
	}
	if rate >= 1 {
		return true
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(target + ":" + operationID))
	return float64(h.Sum32()%10000)/10000 < rate
}
