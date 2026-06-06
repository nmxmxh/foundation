package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCollectorSnapshotResetAndNilSafety(t *testing.T) {
	var nilCollector *Collector
	nilCollector.RecordHTTPRequest("GET", "/", 200)
	nilCollector.RecordDispatch("", "", time.Millisecond)
	nilCollector.RecordWorker("", "", "")
	nilCollector.RecordQueueDepth("", -1)
	nilCollector.RecordConcurrency("", "", "")
	nilCollector.RecordConcurrencyGauge("", "", -1)
	nilCollector.RecordConcurrencyDuration("", "", "", time.Millisecond)
	nilCollector.Reset()
	if nilCollector.Snapshot().Timestamp != "" {
		t.Fatal("nil snapshot should be empty")
	}

	c := NewCollector()
	c.RecordHTTPRequest("GET", "/health", 200)
	c.RecordDispatch("", "", 2*time.Millisecond)
	c.RecordDispatch("media:probe:requested", "success", 4*time.Millisecond)
	c.RecordRedisOperation("publish", "success", 3*time.Millisecond)
	c.RecordDatabaseOperation("query", "success", 5*time.Millisecond)
	c.RecordDatabasePool("primary", 3, 4, 7, 16, 11, 22*time.Millisecond)
	c.RecordWorker("", "", "")
	c.RecordQueueDepth("", -5)
	c.RecordConcurrency("registry", "goroutine", "started")
	c.RecordConcurrencyGauge("registry", "active_goroutines", 2)
	c.RecordConcurrencyDuration("registry", "shutdown", "success", 6*time.Millisecond)
	c.RecordTrace("corr-1", "requested", "media:probe:v1:requested", "requested", "accepted", map[string]string{"organization_id": "org-1"})
	snapshot := c.Snapshot()
	httpData := snapshot.HTTP.RequestCount
	if httpData["GET /health 200"] != 1 {
		t.Fatalf("unexpected http metrics: %+v", httpData)
	}
	dispatchAvg := snapshot.Dispatch.AvgDurationMicro
	if dispatchAvg["unknown|unknown"] != 2000 {
		t.Fatalf("unexpected dispatch avg: %+v", dispatchAvg)
	}
	worker := snapshot.Worker
	if worker.QueueDepth["default"] != 0 {
		t.Fatalf("unexpected worker metrics: %+v", worker)
	}
	concurrency := snapshot.Concurrency
	if concurrency.Count["registry|goroutine|started"] != 1 {
		t.Fatalf("unexpected concurrency counts: %+v", concurrency)
	}
	if concurrency.Gauge["registry|active_goroutines"] != 2 {
		t.Fatalf("unexpected concurrency gauges: %+v", concurrency)
	}
	if concurrency.AvgDurationMicro["registry|shutdown|success"] != 6000 {
		t.Fatalf("unexpected concurrency durations: %+v", concurrency)
	}
	redis := snapshot.Redis
	if redis.AvgDurationMicro["publish|success"] != 3000 {
		t.Fatalf("unexpected redis metrics: %+v", redis)
	}
	database := snapshot.Database
	if database.AvgDurationMicro["query|success"] != 5000 {
		t.Fatalf("unexpected database metrics: %+v", database)
	}
	pool := database.Pool["primary"]
	if pool.ActiveConns != 3 || pool.AcquireDurationMicro != 22000 {
		t.Fatalf("unexpected database pool metrics: %+v", pool)
	}
	traces := snapshot.Traces
	if traces.CorrelationCount != 1 || traces.EventCount != 1 {
		t.Fatalf("unexpected trace summary: %+v", traces)
	}
	c.Reset()
	if len(c.Snapshot().HTTP.RequestCount) != 0 {
		t.Fatal("expected reset metrics")
	}
	if itoa(-42) != "-42" || itoa(0) != "0" {
		t.Fatal("itoa failed")
	}
}

func TestCollectorTraceBoundsAndCopies(t *testing.T) {
	c := NewCollector()
	c.maxTraceCorrelations = 2
	c.maxTraceEvents = 2
	fields := map[string]string{"organization_id": "org-1"}
	c.RecordTrace("corr-1", "request", "orders:create:v1:requested", "requested", "accepted", fields)
	fields["organization_id"] = "mutated"
	c.RecordTrace("corr-1", "worker", "orders:create:v1:requested", "queued", "job queued", nil)
	c.RecordTrace("corr-1", "terminal", "orders:create:v1:success", "success", "done", nil)
	if got := c.Trace("corr-1", 0); len(got) != 2 || got[0].Stage != "worker" || got[1].Stage != "terminal" {
		t.Fatalf("corr-1 trace should keep the last two events, got %+v", got)
	}
	c.RecordTrace("corr-2", "request", "", "", "", nil)
	c.RecordTrace("corr-3", "request", "", "", "", nil)

	if got := c.Trace("corr-1", 0); len(got) != 0 {
		t.Fatalf("corr-1 should be evicted by correlation bound, got %+v", got)
	}
	got := c.Trace("corr-3", 10)
	if len(got) != 1 || got[0].Stage != "request" {
		t.Fatalf("unexpected corr-3 trace: %+v", got)
	}

	c.RecordTrace("corr-copy", "request", "orders:create:v1:requested", "requested", "accepted", fields)
	copied := c.Trace("corr-copy", 1)
	if copied[0].Fields["organization_id"] != "mutated" {
		t.Fatalf("trace should copy latest input field value, got %+v", copied[0].Fields)
	}
	copied[0].Fields["organization_id"] = "changed-again"
	if again := c.Trace("corr-copy", 1); again[0].Fields["organization_id"] != "mutated" {
		t.Fatalf("Trace should return defensive copies, got %+v", again[0].Fields)
	}
}

func TestHTTPMiddlewareAndRecorderInterfaces(t *testing.T) {
	Default().Reset()
	handler := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/items", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	snapshot := Default().Snapshot()
	count := snapshot.HTTP.RequestCount
	if count["POST /items 201"] != 1 {
		t.Fatalf("unexpected middleware count: %+v", count)
	}

	recorder := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
	recorder.Flush()
	if _, _, err := recorder.Hijack(); err == nil {
		t.Fatal("expected hijack unsupported")
	}
	if err := recorder.Push("/asset", nil); err != http.ErrNotSupported {
		t.Fatalf("push error = %v", err)
	}
	n, err := recorder.ReadFrom(strings.NewReader("payload"))
	if err != nil || n != int64(len("payload")) {
		t.Fatalf("ReadFrom = %d %v", n, err)
	}
	HTTPMiddleware(nil).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestSnapshotAndTraceHandlers(t *testing.T) {
	c := NewCollector()
	c.RecordHTTPRequest("GET", "/health", http.StatusOK)
	rec := httptest.NewRecorder()
	SnapshotHandler(c).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metricsz", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "request_count") {
		t.Fatalf("snapshot response code=%d body=%s", rec.Code, rec.Body.String())
	}

	c.RecordTrace("corr-1", "requested", "orders:create:v1:requested", "requested", "accepted", nil)
	c.RecordTrace("corr-1", "terminal", "orders:create:v1:success", "success", "done", nil)
	rec = httptest.NewRecorder()
	TraceHandler(c).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metricsz/trace?correlation_id=corr-1&limit=1", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "terminal") || strings.Contains(rec.Body.String(), "requested") {
		t.Fatalf("trace response code=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	TraceHandler(c).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metricsz/trace", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing correlation status = %d", rec.Code)
	}
}
