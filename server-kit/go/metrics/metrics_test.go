package metrics

import (
	"strings"
	"testing"
)

func TestRegistryRecordsMetricTypes(t *testing.T) {
	r := NewRegistry()
	r.Counter("orders.created", Tags{"tenant": "org_1"})
	r.Counter("orders.created", Tags{"tenant": "org_1"}, 2)
	r.Gauge("worker.queue_depth", Tags{"queue": "default"}, 7)
	r.Histogram("dispatch.latency_ms", nil, 10)
	r.Histogram("dispatch.latency_ms", nil, 30)

	s := r.Snapshot()
	if got := s.Counters["orders_created_tenant_org_1"]; got != 3 {
		t.Fatalf("counter = %v, want 3", got)
	}
	if got := s.Gauges["worker_queue_depth_queue_default"]; got != 7 {
		t.Fatalf("gauge = %v, want 7", got)
	}
	h := s.Histograms["dispatch_latency_ms"]
	if h.Count != 2 || h.Sum != 40 || h.Min != 10 || h.Max != 30 {
		t.Fatalf("histogram = %+v", h)
	}
}

func TestPrometheusExportIsStable(t *testing.T) {
	r := NewRegistry()
	r.Counter("orders.created", Tags{"tenant": "org-1"})
	out := r.Prometheus()
	if !strings.Contains(out, "orders_created_tenant_org_1 1") {
		t.Fatalf("unexpected prometheus output: %s", out)
	}
}

func TestDefaultMetricsResetAndNilRegistry(t *testing.T) {
	Default().Reset()
	Counter("requests.total", Tags{"route": "/v1/test"})
	Gauge("queue.depth", nil, 4)
	Histogram("latency", nil, 10)
	if snapshot := Default().Snapshot(); len(snapshot.Counters) == 0 || len(snapshot.Gauges) == 0 || len(snapshot.Histograms) == 0 {
		t.Fatalf("default registry did not record metrics: %+v", snapshot)
	}
	Default().Reset()
	if snapshot := Default().Snapshot(); len(snapshot.Counters) != 0 || len(snapshot.Gauges) != 0 || len(snapshot.Histograms) != 0 {
		t.Fatalf("default registry did not reset: %+v", snapshot)
	}

	var nilRegistry *Registry
	nilRegistry.Counter("ignored", nil)
	nilRegistry.Gauge("ignored", nil, 1)
	nilRegistry.Histogram("ignored", nil, 1)
	nilRegistry.Reset()
	if nilRegistry.Snapshot().Timestamp.IsZero() {
		t.Fatalf("nil snapshot should include timestamp")
	}
	if got := sanitize(" $ "); got != "_" {
		t.Fatalf("sanitize symbol = %q", got)
	}
	if got := sanitize(" "); got != "unknown" {
		t.Fatalf("sanitize blank = %q", got)
	}
}
