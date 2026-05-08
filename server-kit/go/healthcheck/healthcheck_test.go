package healthcheck

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type testPinger struct {
	err error
}

func (p testPinger) Ping(context.Context) error {
	return p.err
}

func TestRunChecksAggregatesStatusAndCache(t *testing.T) {
	calls := 0
	hc := New(Config{ServiceName: "svc", ServiceVersion: "1.2.3", DefaultTimeout: time.Second, CacheDuration: time.Hour})
	hc.AddCheck("ready", func(context.Context) CheckResult {
		calls++
		return CheckResult{Status: StatusHealthy, Message: "ok", Timestamp: time.Now()}
	}, WithLiveness(true))
	hc.AddCheck("optional", func(context.Context) CheckResult {
		return CheckResult{Status: StatusDegraded, Message: "slow", Timestamp: time.Now()}
	}, WithCritical(false))

	response := hc.RunChecks(context.Background(), false)
	if response.Status != StatusDegraded || len(response.Checks) != 2 || calls != 1 {
		t.Fatalf("unexpected response: %+v calls=%d", response, calls)
	}
	live := hc.RunChecks(context.Background(), true)
	if live.Status != StatusHealthy || len(live.Checks) != 1 {
		t.Fatalf("unexpected liveness response: %+v", live)
	}
	cached := hc.RunChecks(context.Background(), true)
	if cached.Checks["ready"].Message != "ok" || calls != 1 {
		t.Fatalf("expected cached result, calls=%d response=%+v", calls, cached)
	}
}

func TestHandlersAndChecks(t *testing.T) {
	hc := New(Config{ServiceName: "svc"})
	hc.AddCheck("critical", func(context.Context) CheckResult {
		return CheckResult{Status: StatusUnhealthy, Message: "down", Timestamp: time.Now()}
	}, WithTimeout(time.Millisecond))
	hc.AddCheck("live", func(context.Context) CheckResult {
		return CheckResult{Status: StatusHealthy, Message: "live", Timestamp: time.Now()}
	}, WithLiveness(true))
	liveRec := httptest.NewRecorder()
	hc.LivenessHandler().ServeHTTP(liveRec, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if liveRec.Code != http.StatusOK {
		t.Fatalf("liveness status = %d", liveRec.Code)
	}
	for _, handler := range []http.Handler{hc.Handler(), hc.ReadinessHandler()} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("handler status = %d", rec.Code)
		}
	}

	if PingCheck()(context.Background()).Status != StatusHealthy {
		t.Fatal("ping check should be healthy")
	}
	if CustomCheck("custom", func(context.Context) error { return nil })(context.Background()).Status != StatusHealthy {
		t.Fatal("custom success should be healthy")
	}
	if CustomCheck("custom", func(context.Context) error { return errors.New("bad") })(context.Background()).Status != StatusUnhealthy {
		t.Fatal("custom failure should be unhealthy")
	}
	if PingerCheck(testPinger{}, "redis")(context.Background()).Status != StatusHealthy {
		t.Fatal("pinger success should be healthy")
	}
	if PingerCheck(testPinger{err: errors.New("nope")}, "redis")(context.Background()).Status != StatusUnhealthy {
		t.Fatal("pinger failure should be unhealthy")
	}
}

func TestHTTPAndTCPAndSystemChecks(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("network listeners unavailable in this sandbox: %v", err)
	}
	_ = ln.Close()

	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer okServer.Close()
	if got := HTTPCheck(okServer.URL)(context.Background()); got.Status != StatusHealthy {
		t.Fatalf("HTTPCheck healthy = %+v", got)
	}

	badServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer badServer.Close()
	if got := HTTPCheck(badServer.URL)(context.Background()); got.Status != StatusUnhealthy {
		t.Fatalf("HTTPCheck unhealthy = %+v", got)
	}
	if got := HTTPCheck("://bad-url")(context.Background()); got.Status != StatusUnhealthy {
		t.Fatalf("HTTPCheck invalid URL = %+v", got)
	}

	ln, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()
	if got := TCPCheck(ln.Addr().String())(context.Background()); got.Status != StatusHealthy {
		t.Fatalf("TCPCheck healthy = %+v", got)
	}
	if got := DiskSpaceCheck(".", 1)(context.Background()); got.Status != StatusHealthy {
		t.Fatalf("DiskSpaceCheck = %+v", got)
	}
	if got := DiskSpaceCheck("/definitely/missing/path", 1)(context.Background()); got.Status != StatusUnhealthy {
		t.Fatalf("DiskSpaceCheck missing = %+v", got)
	}
	if got := MemoryCheck(1)(context.Background()); got.Status != StatusHealthy {
		t.Fatalf("MemoryCheck = %+v", got)
	}
}
