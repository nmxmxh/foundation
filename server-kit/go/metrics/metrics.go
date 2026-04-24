package metrics

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type Tags map[string]string

type Snapshot struct {
	Counters   map[string]float64
	Gauges     map[string]float64
	Histograms map[string]HistogramSnapshot
	Timestamp  time.Time
}

type HistogramSnapshot struct {
	Count int64
	Sum   float64
	Min   float64
	Max   float64
}

type Registry struct {
	mu         sync.RWMutex
	counters   map[string]float64
	gauges     map[string]float64
	histograms map[string]HistogramSnapshot
}

func NewRegistry() *Registry {
	return &Registry{
		counters:   map[string]float64{},
		gauges:     map[string]float64{},
		histograms: map[string]HistogramSnapshot{},
	}
}

var defaultRegistry = NewRegistry()

func Default() *Registry { return defaultRegistry }

func Counter(name string, tags Tags, delta ...float64) {
	Default().Counter(name, tags, delta...)
}

func Gauge(name string, tags Tags, value float64) {
	Default().Gauge(name, tags, value)
}

func Histogram(name string, tags Tags, value float64) {
	Default().Histogram(name, tags, value)
}

func (r *Registry) Counter(name string, tags Tags, delta ...float64) {
	if r == nil {
		return
	}
	amount := 1.0
	if len(delta) > 0 {
		amount = delta[0]
	}
	r.mu.Lock()
	r.counters[metricKey(name, tags)] += amount
	r.mu.Unlock()
}

func (r *Registry) Gauge(name string, tags Tags, value float64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.gauges[metricKey(name, tags)] = value
	r.mu.Unlock()
}

func (r *Registry) Histogram(name string, tags Tags, value float64) {
	if r == nil {
		return
	}
	key := metricKey(name, tags)
	r.mu.Lock()
	current := r.histograms[key]
	if current.Count == 0 || value < current.Min {
		current.Min = value
	}
	if current.Count == 0 || value > current.Max {
		current.Max = value
	}
	current.Count++
	current.Sum += value
	r.histograms[key] = current
	r.mu.Unlock()
}

func (r *Registry) Snapshot() Snapshot {
	if r == nil {
		return Snapshot{Timestamp: time.Now().UTC()}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return Snapshot{
		Counters:   cloneFloatMap(r.counters),
		Gauges:     cloneFloatMap(r.gauges),
		Histograms: cloneHistogramMap(r.histograms),
		Timestamp:  time.Now().UTC(),
	}
}

func (r *Registry) Reset() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.counters = map[string]float64{}
	r.gauges = map[string]float64{}
	r.histograms = map[string]HistogramSnapshot{}
	r.mu.Unlock()
}

func (r *Registry) Prometheus() string {
	snapshot := r.Snapshot()
	var b strings.Builder
	writeFloatMetrics(&b, "counter", snapshot.Counters)
	writeFloatMetrics(&b, "gauge", snapshot.Gauges)
	keys := make([]string, 0, len(snapshot.Histograms))
	for key := range snapshot.Histograms {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		h := snapshot.Histograms[key]
		fmt.Fprintf(&b, "%s_count %d\n", key, h.Count)
		fmt.Fprintf(&b, "%s_sum %g\n", key, h.Sum)
		fmt.Fprintf(&b, "%s_min %g\n", key, h.Min)
		fmt.Fprintf(&b, "%s_max %g\n", key, h.Max)
	}
	return b.String()
}

func metricKey(name string, tags Tags) string {
	clean := sanitize(name)
	if len(tags) == 0 {
		return clean
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, sanitize(key)+"_"+sanitize(tags[key]))
	}
	return clean + "_" + strings.Join(parts, "_")
}

func sanitize(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == ':' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	return b.String()
}

func writeFloatMetrics(b *strings.Builder, _ string, values map[string]float64) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(b, "%s %g\n", key, values[key])
	}
}

func cloneFloatMap(in map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneHistogramMap(in map[string]HistogramSnapshot) map[string]HistogramSnapshot {
	out := make(map[string]HistogramSnapshot, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
