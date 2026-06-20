package connector

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/circuitbreaker"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/slo"
)

// fakeDriver is a programmable Driver used across core tests. Behavior is set
// per endpoint and looked up by the registered factory.
type fakeDriver struct {
	transport string
	endpoint  string

	mu       sync.Mutex
	probe    Health
	probeErr error
	callErr  error
	callErrN int32 // fail the first N calls, then succeed
	calls    int32
	streamFn func(resume string) (Stream, error)
	closed   bool
}

var fakeRegistry sync.Map // endpoint -> *fakeDriver

func registerFake(t *testing.T, transport, endpoint string, fd *fakeDriver) {
	t.Helper()
	fd.transport = transport
	fd.endpoint = endpoint
	fakeRegistry.Store(endpoint, fd)
	Register(transport, func(ep string, _ map[string]any) (Driver, error) {
		v, ok := fakeRegistry.Load(ep)
		if !ok {
			return nil, errors.New("no fake for endpoint " + ep)
		}
		return v.(*fakeDriver), nil
	})
}

func (f *fakeDriver) Transport() string { return f.transport }

func (f *fakeDriver) Probe(context.Context) (Health, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.probe, f.probeErr
}

func (f *fakeDriver) Capabilities(context.Context) (Capabilities, error) {
	return Capabilities{Transport: f.transport, Encodings: []string{"json"}}, nil
}

func (f *fakeDriver) Call(context.Context, Request) (Response, error) {
	n := atomic.AddInt32(&f.calls, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if n <= atomic.LoadInt32(&f.callErrN) {
		return Response{}, errors.New("transient failure")
	}
	if f.callErr != nil {
		return Response{}, f.callErr
	}
	return Response{Status: 200, Body: []byte("ok"), Encoding: "json"}, nil
}

func (f *fakeDriver) Stream(_ context.Context, _ Request, resume string) (Stream, error) {
	if f.streamFn != nil {
		return f.streamFn(resume)
	}
	return nil, ErrUnsupported
}

func (f *fakeDriver) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func TestCallSuccess(t *testing.T) {
	registerFake(t, "faceA", "epA", &fakeDriver{probe: HealthServing})
	c, err := New(Config{Name: "a", Transports: []TransportConfig{{Transport: "faceA", Endpoint: "epA"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	resp, err := c.Call(context.Background(), Request{Operation: "ping"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if string(resp.Body) != "ok" {
		t.Fatalf("body = %q", resp.Body)
	}
}

func TestCallRetriesThenSucceeds(t *testing.T) {
	fd := &fakeDriver{probe: HealthServing, callErrN: 2}
	registerFake(t, "faceB", "epB", fd)
	c, err := New(Config{
		Name:       "b",
		Transports: []TransportConfig{{Transport: "faceB", Endpoint: "epB"}},
		Retry:      RetryConfig{MaxAttempts: 5, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Call(context.Background(), Request{Operation: "ping"}); err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if got := atomic.LoadInt32(&fd.calls); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestBreakerOpensAndFailsOver(t *testing.T) {
	down := &fakeDriver{probe: HealthNotServing, callErr: errors.New("down")}
	up := &fakeDriver{probe: HealthServing}
	registerFake(t, "primary", "epDown", down)
	registerFake(t, "secondary", "epUp", up)
	c, err := New(Config{
		Name: "c",
		Transports: []TransportConfig{
			{Transport: "primary", Endpoint: "epDown"},
			{Transport: "secondary", Endpoint: "epUp"},
		},
		Retry:   RetryConfig{MaxAttempts: 8, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond},
		Breaker: circuitbreaker.Config{FailureThreshold: 2, SuccessThreshold: 1, Timeout: time.Hour, HalfOpenMaxCalls: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// With a low failure threshold the breaker opens on the primary, triggering
	// failover to the healthy secondary, and the call ultimately succeeds.
	resp, err := c.Call(context.Background(), Request{Operation: "ping"})
	if err != nil {
		t.Fatalf("expected eventual success via failover, got %v", err)
	}
	if string(resp.Body) != "ok" {
		t.Fatalf("body = %q", resp.Body)
	}
	l, _ := c.activeLane()
	if l.cfg.Transport != "secondary" {
		t.Fatalf("expected active transport secondary, got %s", l.cfg.Transport)
	}
}

func TestSupervisorProbeSetsHealth(t *testing.T) {
	registerFake(t, "probed", "epProbe", &fakeDriver{probe: HealthServing})
	var statuses int32
	c, err := New(Config{
		Name:          "d",
		Transports:    []TransportConfig{{Transport: "probed", Endpoint: "epProbe"}},
		ProbeInterval: 10 * time.Millisecond,
		OnStatus:      func(Status) { atomic.AddInt32(&statuses, 1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	c.Start(ctx)
	defer c.Close()

	deadline := time.After(2 * time.Second)
	for {
		if c.Status().Health == HealthServing {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("connector never reached serving, status=%+v", c.Status())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if atomic.LoadInt32(&statuses) == 0 {
		t.Fatal("OnStatus was never called on change")
	}
}

func TestSupervisorDegradedOnSLOBreach(t *testing.T) {
	fd := &fakeDriver{probe: HealthServing}
	registerFake(t, "slow", "epSlow", fd)
	c, err := New(Config{
		Name:          "e",
		Transports:    []TransportConfig{{Transport: "slow", Endpoint: "epSlow"}},
		ProbeInterval: 10 * time.Millisecond,
		SLO:           slo.Config{DispatchP99LatencyMS: 1}, // 1ms threshold, easily breached
		DegradeAfter:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	c.Start(ctx)
	defer c.Close()

	// Feed a slow observation so EWMA latency breaches the SLO.
	c.sup.observe(0, true, 50*time.Millisecond)

	deadline := time.After(2 * time.Second)
	for {
		if c.Status().Health == HealthDegraded {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("expected degraded, got %+v", c.Status())
		case <-time.After(5 * time.Millisecond):
			c.sup.observe(0, true, 50*time.Millisecond)
		}
	}
}

func TestConnectorConcurrencyStress(t *testing.T) {
	fd := &fakeDriver{probe: HealthServing}
	registerFake(t, "stress", "epStress", fd)
	c, err := New(Config{
		Name:          "stress",
		Transports:    []TransportConfig{{Transport: "stress", Endpoint: "epStress"}},
		ProbeInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	c.Start(ctx)
	defer c.Close()

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for range 100 {
				_, _ = c.Call(context.Background(), Request{Operation: "stress_op"})
				_ = c.Status()
				time.Sleep(time.Microsecond)
			}
		}(i)
	}
	wg.Wait()
}

// scriptedStream yields queued messages then a transient error, used to test
// ManagedStream resumption.
type scriptedStream struct {
	msgs []StreamMessage
	i    int
	err  error
	wm   string
}

func (s *scriptedStream) Recv() (StreamMessage, error) {
	if s.i >= len(s.msgs) {
		return StreamMessage{}, s.err
	}
	m := s.msgs[s.i]
	s.i++
	s.wm = m.Watermark
	return m, nil
}
func (s *scriptedStream) Watermark() string { return s.wm }
func (s *scriptedStream) Close() error      { return nil }

func TestManagedStreamResumesFromWatermark(t *testing.T) {
	var resumes []string
	fd := &fakeDriver{probe: HealthServing}
	fd.streamFn = func(resume string) (Stream, error) {
		resumes = append(resumes, resume)
		if len(resumes) == 1 {
			// First stream: one message then a transient (non-EOF) error.
			return &scriptedStream{
				msgs: []StreamMessage{{Data: []byte("a"), Watermark: "wm-1"}},
				err:  errors.New("connection reset"),
			}, nil
		}
		// Second stream (after resume): final message then EOF.
		return &scriptedStream{
			msgs: []StreamMessage{{Data: []byte("b"), Watermark: "wm-2"}},
			err:  io.EOF,
		}, nil
	}
	registerFake(t, "streamer", "epStream", fd)
	c, err := New(Config{
		Name:       "f",
		Transports: []TransportConfig{{Transport: "streamer", Endpoint: "epStream"}},
		Retry:      RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	ms, err := c.Stream(context.Background(), Request{Operation: "sub"})
	if err != nil {
		t.Fatal(err)
	}
	defer ms.Close()

	var got []string
	for {
		msg, err := ms.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		got = append(got, string(msg.Data))
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("messages = %v", got)
	}
	if len(resumes) != 2 || resumes[1] != "wm-1" {
		t.Fatalf("expected resume from wm-1, got %v", resumes)
	}
	if ms.Watermark() != "wm-2" {
		t.Fatalf("final watermark = %q", ms.Watermark())
	}
}

func TestManagerLifecycle(t *testing.T) {
	registerFake(t, "mgr", "epMgr", &fakeDriver{probe: HealthServing})
	m := NewManager()
	if _, err := m.Add(Config{Name: "x", Transports: []TransportConfig{{Transport: "mgr", Endpoint: "epMgr"}}, ProbeInterval: 10 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	m.Start(ctx)
	if len(m.Statuses()) != 1 {
		t.Fatalf("expected 1 status, got %d", len(m.Statuses()))
	}
	if _, ok := m.Get("x"); !ok {
		t.Fatal("connector x not found")
	}
	if err := m.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
