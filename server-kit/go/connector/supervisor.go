package connector

import (
	"context"
	"sync"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metrics"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/slo"
)

type laneStats struct {
	consecFail    int
	consecSuccess int
	breachStreak  int
	ewmaLatencyMS float64
	winCalls      int
	winFailures   int
	lastLatency   time.Duration
	lastErr       string
}

// supervisor is the intelligent thread: a single goroutine running a MAPE-K
// control loop (Monitor -> Analyze -> Plan -> Execute over shared Knowledge)
// per connector. It owns health classification, transport failover, capability
// refresh, and status emission.
type supervisor struct {
	c *Connector

	startMu sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}

	mu        sync.Mutex
	laneStats map[int]*laneStats
}

func newSupervisor(c *Connector) *supervisor {
	return &supervisor{
		c:         c,
		laneStats: make(map[int]*laneStats),
	}
}

func (s *supervisor) getStatsLocked(idx int) *laneStats {
	st := s.laneStats[idx]
	if st == nil {
		st = &laneStats{}
		s.laneStats[idx] = st
	}
	return st
}

func (s *supervisor) start(ctx context.Context) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.cancel != nil {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.run(runCtx)
}

func (s *supervisor) stop() {
	s.startMu.Lock()
	cancel := s.cancel
	done := s.done
	s.startMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (s *supervisor) run(ctx context.Context) {
	defer close(s.done)

	interval := s.c.cfg.ProbeInterval
	// Run one cycle immediately so status reflects reality at startup.
	s.cycle(ctx)
	if interval <= 0 {
		// No active probing: the loop only services passive re-evaluation and
		// context cancellation on a slow heartbeat.
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.cycle(ctx)
		}
	}
}

// cycle runs one MAPE-K iteration.
func (s *supervisor) cycle(ctx context.Context) {
	// --- Monitor: active probe of the active lane (if probing enabled). ---
	var (
		probed     Health = HealthUnknown
		probeErr   error
		probeStart = s.c.now()
		caps       Capabilities
	)
	s.c.mu.RLock()
	idx := s.c.activeIx
	lane := s.c.lanes[idx]
	s.c.mu.RUnlock()

	if s.c.cfg.ProbeInterval > 0 {
		s.setPhase(PhaseProbing)
		pctx, cancel := context.WithTimeout(ctx, probeTimeout(s.c.cfg.ProbeInterval))
		probed, probeErr = lane.driver.Probe(pctx)
		if c, cerr := lane.driver.Capabilities(pctx); cerr == nil {
			caps = c
			lane.caps.Store(&caps)
		}
		cancel()
		s.c.metrics.Counter("connector_probe_total", metrics.Tags{
			"connector": s.c.cfg.Name, "transport": lane.cfg.Transport,
			"outcome": boolOutcome(probeErr == nil && probed.OK()),
		}, 1)
		if probeErr != nil {
			s.observe(idx, false, s.c.now().Sub(probeStart))
		} else {
			s.observe(idx, probed.OK(), s.c.now().Sub(probeStart))
		}
	}

	// --- Analyze: fuse probe + passive knowledge into a health verdict. ---
	health := s.analyze(idx, probed, probeErr)

	// --- Plan + Execute: choose and apply a healing action. ---
	phase := s.plan(health)

	// --- Knowledge: publish the status snapshot. ---
	s.publish(idx, lane, health, phase, caps, probeErr)
}

// analyze fuses the latest active probe with rolling passive observations and
// SLO thresholds to produce a health verdict.
func (s *supervisor) analyze(idx int, probed Health, probeErr error) Health {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.getStatsLocked(idx)

	// SLO breach evaluation over the current window.
	successRate := 1.0
	if st.winCalls > 0 {
		successRate = float64(st.winCalls-st.winFailures) / float64(st.winCalls)
	}
	breaches := slo.Evaluate(s.c.cfg.SLO, slo.Observation{
		DispatchP99LatencyMS: st.ewmaLatencyMS,
		WorkerSuccessRate:    successRate,
	})
	if len(breaches) > 0 {
		st.breachStreak++
	} else {
		st.breachStreak = 0
	}
	// Reset the window after evaluation.
	st.winCalls, st.winFailures = 0, 0

	// Hard-down signals dominate.
	if st.consecFail > 0 && st.consecSuccess == 0 {
		if probeErr != nil || probed == HealthNotServing {
			return HealthNotServing
		}
	}
	if probeErr != nil {
		return HealthNotServing
	}
	if probed == HealthNotServing {
		return HealthNotServing
	}
	// Degraded if SLO has been breaching past the tolerance.
	if st.breachStreak >= s.c.cfg.DegradeAfter {
		return HealthDegraded
	}
	if probed == HealthDegraded {
		return HealthDegraded
	}
	if probed == HealthServing || st.consecSuccess > 0 {
		return HealthServing
	}
	return HealthUnknown
}

// plan decides the phase and executes healing: failover when the active lane is
// down and another lane exists; otherwise let the breaker manage recovery.
func (s *supervisor) plan(health Health) Phase {
	switch health {
	case HealthServing:
		return PhaseConnected
	case HealthDegraded:
		return PhaseDegraded
	case HealthNotServing:
		// Try to fail over to a healthier transport.
		if s.failover() {
			return PhaseRecovering
		}
		if s.c.breaker.State().String() == "open" {
			return PhaseDown
		}
		return PhaseRecovering
	default:
		return PhaseProbing
	}
}

// failover advances the active lane to the next one, wrapping around. It returns
// false when there is only a single lane (nothing to fail over to).
func (s *supervisor) failover() bool {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	if len(s.c.lanes) <= 1 {
		return false
	}
	prev := s.c.activeIx
	s.c.activeIx = (s.c.activeIx + 1) % len(s.c.lanes)
	if s.c.activeIx == prev {
		return false
	}
	s.c.metrics.Counter("connector_failover_total", metrics.Tags{
		"connector": s.c.cfg.Name,
		"from":      s.c.lanes[prev].cfg.Transport,
		"to":        s.c.lanes[s.c.activeIx].cfg.Transport,
	}, 1)
	// Reset the circuit breaker so the new lane has a clean slate.
	s.c.breaker.Reset()

	// Reset consecFail and consecSuccess for the new lane to give it a fresh chance.
	s.mu.Lock()
	st := s.getStatsLocked(s.c.activeIx)
	st.consecFail = 0
	st.consecSuccess = 0
	s.mu.Unlock()
	return true
}

// observe feeds a single call/probe outcome into rolling knowledge. It is the
// passive half of monitoring and is called on every Call/Stream outcome.
func (s *supervisor) observe(laneIdx int, success bool, latency time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.getStatsLocked(laneIdx)
	st.winCalls++
	if success {
		st.consecSuccess++
		st.consecFail = 0
	} else {
		st.consecFail++
		st.consecSuccess = 0
		st.winFailures++
	}
	if latency > 0 {
		st.lastLatency = latency
		ms := float64(latency.Microseconds()) / 1000.0
		const alpha = 0.3
		if st.ewmaLatencyMS == 0 {
			st.ewmaLatencyMS = ms
		} else {
			st.ewmaLatencyMS = alpha*ms + (1-alpha)*st.ewmaLatencyMS
		}
	}
}

func (s *supervisor) setPhase(p Phase) {
	cur := s.c.Status()
	if cur.Phase == p {
		return
	}
	cur.Phase = p
	cur.UpdatedAt = s.c.now()
	s.c.storeStatus(cur)
}

// publish writes a new status snapshot and notifies OnStatus when health/phase
// changed.
func (s *supervisor) publish(idx int, active *lane, health Health, phase Phase, caps Capabilities, probeErr error) {
	s.c.mu.RLock()
	wm := s.c.wm
	s.c.mu.RUnlock()

	s.mu.Lock()
	st := s.getStatsLocked(idx)
	consecFail, consecSuccess := st.consecFail, st.consecSuccess
	lastLatency := st.lastLatency
	if probeErr != nil {
		st.lastErr = probeErr.Error()
	}
	lastErr := st.lastErr
	s.mu.Unlock()

	prev := s.c.Status()
	now := s.c.now()
	next := Status{
		Name:            s.c.cfg.Name,
		Transport:       active.cfg.Transport,
		Endpoint:        active.cfg.Endpoint,
		Health:          health,
		Phase:           phase,
		Breaker:         s.c.breaker.State().String(),
		ConsecFailures:  consecFail,
		ConsecSuccesses: consecSuccess,
		LastLatency:     lastLatency,
		LastError:       lastErr,
		LastProbedAt:    now,
		UpdatedAt:       now,
		Watermark:       wm,
	}
	if cp := active.caps.Load(); cp != nil {
		next.Capabilities = *cp
	} else {
		next.Capabilities = caps
	}

	changed := prev.Health != next.Health || prev.Phase != next.Phase || prev.Transport != next.Transport
	if changed {
		next.LastChangedAt = now
		s.c.metrics.Gauge("connector_health", metrics.Tags{
			"connector": s.c.cfg.Name, "transport": next.Transport,
		}, float64(next.Health))
	} else {
		next.LastChangedAt = prev.LastChangedAt
	}
	s.c.storeStatus(next)
	if changed && s.c.cfg.OnStatus != nil {
		s.c.cfg.OnStatus(next)
	}
}

func probeTimeout(interval time.Duration) time.Duration {
	if interval <= 0 {
		return 5 * time.Second
	}
	if interval < 2*time.Second {
		return interval
	}
	return interval / 2
}

func boolOutcome(ok bool) string {
	if ok {
		return "ok"
	}
	return "error"
}
