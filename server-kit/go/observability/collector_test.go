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
	nilCollector.Reset()
	if len(nilCollector.Snapshot()) != 0 {
		t.Fatal("nil snapshot should be empty")
	}

	c := NewCollector()
	c.RecordHTTPRequest("GET", "/health", 200)
	c.RecordDispatch("", "", 2*time.Millisecond)
	c.RecordDispatch("media:probe:requested", "success", 4*time.Millisecond)
	c.RecordWorker("", "", "")
	c.RecordQueueDepth("", -5)
	snapshot := c.Snapshot()
	httpData := snapshot["http"].(map[string]any)["request_count"].(map[string]int64)
	if httpData["GET /health 200"] != 1 {
		t.Fatalf("unexpected http metrics: %+v", httpData)
	}
	dispatchAvg := snapshot["dispatch"].(map[string]any)["avg_duration_micro"].(map[string]int64)
	if dispatchAvg["unknown|unknown"] != 2000 {
		t.Fatalf("unexpected dispatch avg: %+v", dispatchAvg)
	}
	worker := snapshot["worker"].(map[string]any)
	if worker["queue_depth"].(map[string]int64)["default"] != 0 {
		t.Fatalf("unexpected worker metrics: %+v", worker)
	}
	c.Reset()
	if len(c.Snapshot()["http"].(map[string]any)["request_count"].(map[string]int64)) != 0 {
		t.Fatal("expected reset metrics")
	}
	if itoa(-42) != "-42" || itoa(0) != "0" {
		t.Fatal("itoa failed")
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
	count := snapshot["http"].(map[string]any)["request_count"].(map[string]int64)
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
