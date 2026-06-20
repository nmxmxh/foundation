package connector

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/chaos"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/circuitbreaker"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metrics"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/slo"
)

// Config describes one outbound connector. A connector may be backed by several
// transports (Transports[0] is preferred); the supervisor fails over to later
// transports when earlier ones are unhealthy.
type Config struct {
	// Name is the stable connector id used for metrics, breaker, and lookup.
	Name string
	// Transports is the ordered failover list. Each entry names a registered
	// transport plus its endpoint and driver options.
	Transports []TransportConfig
	// Retry is the per-call retry policy. Zero value uses DefaultRetryConfig.
	Retry RetryConfig
	// Breaker is the circuit-breaker policy. Zero value uses DefaultConfig.
	Breaker circuitbreaker.Config
	// ProbeInterval is how often the supervisor actively probes. <=0 disables
	// active probing (passive observation still drives health).
	ProbeInterval time.Duration
	// SLO thresholds feed the Analyze step. Zero fields are ignored.
	SLO slo.Config
	// DegradeAfter is the number of consecutive SLO breaches before the
	// connector is reported degraded. Default 1.
	DegradeAfter int
	// Chaos, when set, injects faults targeted by connector name + operation.
	Chaos *chaos.Injector
	// Metrics, when nil, uses metrics.Default().
	Metrics *metrics.Registry
	// OnStatus, when set, is called with each status change (after the supervisor
	// updates knowledge). It must not block.
	OnStatus func(Status)
	// Clock, when nil, uses wall time. Present for deterministic tests.
	Clock func() time.Time
}

// TransportConfig binds a registered transport to an endpoint and options.
type TransportConfig struct {
	Transport string
	Endpoint  string
	Options   map[string]any
}

// Connector is a transport-agnostic outbound API hook. It is safe for
// concurrent use. Construct with New, drive traffic with Call/Stream, and
// observe via Status. Start launches the intelligent thread; Close stops it.
type Connector struct {
	cfg     Config
	retry   RetryConfig
	breaker *circuitbreaker.CircuitBreaker
	metrics *metrics.Registry
	now     func() time.Time

	// lanes are the per-transport drivers in failover order.
	lanes []*lane

	mu       sync.RWMutex
	activeIx int    // index into lanes currently serving
	wm       string // last stream watermark observed

	status atomic.Pointer[Status]

	rngMu sync.Mutex
	rng   *rand.Rand

	sup      *supervisor
	closed   atomic.Bool
	startOne sync.Once
}

// lane is one transport option for a connector.
type lane struct {
	cfg    TransportConfig
	driver Driver
	caps   atomic.Pointer[Capabilities]
}

// New constructs a Connector, building a driver for each configured transport.
// It does not start the supervisor; call Start for active probing/healing.
func New(cfg Config) (*Connector, error) {
	if cfg.Name == "" {
		return nil, errors.New("connector: Name is required")
	}
	if len(cfg.Transports) == 0 {
		return nil, ErrNoTransport
	}
	if cfg.DegradeAfter <= 0 {
		cfg.DegradeAfter = 1
	}
	reg := cfg.Metrics
	if reg == nil {
		reg = metrics.Default()
	}
	now := cfg.Clock
	if now == nil {
		now = time.Now
	}

	lanes := make([]*lane, 0, len(cfg.Transports))
	for _, tc := range cfg.Transports {
		d, err := NewDriver(tc.Transport, tc.Endpoint, tc.Options)
		if err != nil {
			// Best-effort cleanup of already-built lanes.
			for _, l := range lanes {
				_ = l.driver.Close()
			}
			return nil, err
		}
		lanes = append(lanes, &lane{cfg: tc, driver: d})
	}

	c := &Connector{
		cfg:     cfg,
		retry:   cfg.Retry.normalized(),
		breaker: circuitbreaker.New(cfg.Name, cfg.Breaker),
		metrics: reg,
		now:     now,
		lanes:   lanes,
		// #nosec G404 -- connector retry jitter is load spreading, not a security token or randomness boundary.
		rng:     rand.New(rand.NewSource(now().UnixNano())),
	}
	c.storeStatus(Status{
		Name:          cfg.Name,
		Transport:     lanes[0].cfg.Transport,
		Endpoint:      lanes[0].cfg.Endpoint,
		Health:        HealthUnknown,
		Phase:         PhaseInit,
		Breaker:       c.breaker.State().String(),
		UpdatedAt:     now(),
		LastChangedAt: now(),
	})
	c.sup = newSupervisor(c)
	return c, nil
}

// Name returns the connector name.
func (c *Connector) Name() string { return c.cfg.Name }

// Start launches the intelligent thread. It is idempotent. The supervisor runs
// until ctx is cancelled or Close is called.
func (c *Connector) Start(ctx context.Context) {
	c.startOne.Do(func() { c.sup.start(ctx) })
}

// Close stops the supervisor and releases all driver connections.
func (c *Connector) Close() error {
	if c.closed.Swap(true) {
		return nil
	}
	c.sup.stop()
	var firstErr error
	for _, l := range c.lanes {
		if err := l.driver.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	s := c.Status()
	s.Phase = PhaseClosed
	s.UpdatedAt = c.now()
	c.storeStatus(s)
	return firstErr
}

// Status returns the latest knowledge snapshot.
func (c *Connector) Status() Status { return *c.status.Load() }

func (c *Connector) storeStatus(s Status) {
	c.status.Store(&s)
}

// activeLane returns the currently serving lane and its index.
func (c *Connector) activeLane() (*lane, int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lanes[c.activeIx], c.activeIx
}

// Call performs a request/response exchange with full healing: chaos injection,
// circuit breaking, and jittered retry across the active transport. On a
// transport-level failure with the breaker open, it fails over to the next
// healthy lane (driven by supervisor knowledge) on the next attempt.
func (c *Connector) Call(ctx context.Context, req Request) (Response, error) {
	if c.closed.Load() {
		return Response{}, ErrClosed
	}
	var (
		resp    Response
		lastErr error
	)
	for attempt := 0; attempt < c.retry.MaxAttempts; attempt++ {
		if attempt > 0 {
			d := c.retry.backoff(attempt, c.lockedRng())
			select {
			case <-ctx.Done():
				return Response{}, ctx.Err()
			case <-time.After(d):
			}
		}
		l, idx := c.activeLane()
		start := c.now()

		// Propagate Correlation ID from context if not already set.
		if req.Headers == nil {
			req.Headers = make(map[string]string)
		}
		if _, exists := req.Headers["X-Correlation-ID"]; !exists {
			// Try X-Correlation-ID, x-correlation-id, and server-kit metadata.
			corr := req.Headers["x-correlation-id"]
			if corr == "" {
				corr = metadata.FromContext(ctx).CorrelationID
			}
			if corr != "" {
				req.Headers["X-Correlation-ID"] = corr
			}
		}

		out, err := c.breaker.Execute(ctx, func() (any, error) {
			if c.cfg.Chaos != nil {
				if cerr := c.cfg.Chaos.Apply(ctx, c.cfg.Name, req.Operation); cerr != nil {
					return nil, cerr
				}
			}
			return l.driver.Call(ctx, req)
		})
		latency := c.now().Sub(start)
		c.recordCall(l.cfg.Transport, req.Operation, latency, err)

		if err == nil {
			resp = out.(Response)
			c.sup.observe(idx, true, latency)
			return resp, nil
		}
		lastErr = err
		c.sup.observe(idx, false, latency)

		// A breaker-open error means do not hammer the remote; let the
		// supervisor decide failover, and retry against whatever it selects.
		if errors.Is(err, circuitbreaker.ErrCircuitOpen) {
			c.sup.failover()
		}
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
	}
	return Response{}, lastErr
}

// ManagedStream is a resumable server stream. It transparently re-establishes
// the underlying transport stream from the last watermark when the remote
// disconnects, up to Retry.MaxAttempts consecutive reconnects.
type ManagedStream struct {
	c       *Connector
	req     Request
	ctx     context.Context
	inner   Stream
	wm      string
	reconns int
	closed  bool
	laneIdx int
}

// Stream opens a resumable stream. The returned ManagedStream survives transient
// disconnects by resuming from the last received watermark.
func (c *Connector) Stream(ctx context.Context, req Request) (*ManagedStream, error) {
	if c.closed.Load() {
		return nil, ErrClosed
	}
	ms := &ManagedStream{c: c, req: req, ctx: ctx}
	if err := ms.open(""); err != nil {
		return nil, err
	}
	return ms, nil
}

func (ms *ManagedStream) open(resume string) error {
	l, idx := ms.c.activeLane()
	s, err := l.driver.Stream(ms.ctx, ms.req, resume)
	if err != nil {
		ms.c.sup.observe(idx, false, 0)
		return err
	}
	ms.inner = s
	ms.laneIdx = idx
	return nil
}

// Recv returns the next message, reconnecting from the watermark on transient
// stream errors. It returns the underlying error once reconnect budget is spent.
func (ms *ManagedStream) Recv() (StreamMessage, error) {
	if ms.closed {
		return StreamMessage{}, ErrClosed
	}
	for {
		msg, err := ms.inner.Recv()
		if err == nil {
			if msg.Watermark != "" {
				ms.wm = msg.Watermark
				ms.c.setWatermark(msg.Watermark)
			}
			ms.reconns = 0
			ms.c.sup.observe(ms.laneIdx, true, 0)
			return msg, nil
		}
		// EOF or context cancellation are terminal, not transient.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ms.ctx.Err() != nil {
			return StreamMessage{}, err
		}
		if isEOF(err) {
			return StreamMessage{}, err
		}
		ms.c.sup.observe(ms.laneIdx, false, 0)
		if ms.reconns >= ms.c.retry.MaxAttempts {
			return StreamMessage{}, err
		}
		ms.reconns++
		_ = ms.inner.Close()
		delay := ms.c.retry.backoff(ms.reconns, ms.c.lockedRng())
		select {
		case <-ms.ctx.Done():
			return StreamMessage{}, ms.ctx.Err()
		case <-time.After(delay):
		}
		if rerr := ms.open(ms.wm); rerr != nil {
			// Keep trying within budget; surface the open error if exhausted.
			if ms.reconns >= ms.c.retry.MaxAttempts {
				return StreamMessage{}, rerr
			}
			continue
		}
	}
}

// Watermark returns the last resume token observed on this stream.
func (ms *ManagedStream) Watermark() string { return ms.wm }

// Close terminates the stream.
func (ms *ManagedStream) Close() error {
	if ms.closed {
		return nil
	}
	ms.closed = true
	if ms.inner != nil {
		return ms.inner.Close()
	}
	return nil
}

func (c *Connector) setWatermark(wm string) {
	c.mu.Lock()
	c.wm = wm
	c.mu.Unlock()
}

func (c *Connector) lockedRng() *rand.Rand {
	// rand.Rand is not concurrency-safe; callers serialize via this accessor's
	// returned generator only under rngMu in helpers. We snapshot a seed-based
	// child to avoid holding the lock across sleeps.
	c.rngMu.Lock()
	// #nosec G404 -- connector retry jitter is load spreading, not a security token or randomness boundary.
	child := rand.New(rand.NewSource(c.rng.Int63()))
	c.rngMu.Unlock()
	return child
}

func (c *Connector) recordCall(transport, op string, latency time.Duration, err error) {
	tags := metrics.Tags{"connector": c.cfg.Name, "transport": transport, "op": op}
	c.metrics.Histogram("connector_call_latency_ms", tags, float64(latency.Microseconds())/1000.0)
	if err != nil {
		fail := metrics.Tags{"connector": c.cfg.Name, "transport": transport, "op": op, "outcome": "error"}
		c.metrics.Counter("connector_call_total", fail, 1)
		return
	}
	ok := metrics.Tags{"connector": c.cfg.Name, "transport": transport, "op": op, "outcome": "ok"}
	c.metrics.Counter("connector_call_total", ok, 1)
}
