package metrics

import (
	"maps"
	"sort"
	"strconv"
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

// MetricKey builds the stable sanitized key for a metric name and tag set.
// Precompute it for hot paths where the same name/tags are recorded repeatedly.
func MetricKey(name string, tags Tags) string {
	return metricKey(name, tags)
}

func (r *Registry) Counter(name string, tags Tags, delta ...float64) {
	if r == nil {
		return
	}
	r.addCounter(metricKey(name, tags), metricDelta(delta))
}

// CounterKey records a counter using a key produced by MetricKey.
func (r *Registry) CounterKey(key string, delta ...float64) {
	if r == nil {
		return
	}
	r.addCounter(metricKeyFromPrecomputed(key), metricDelta(delta))
}

func (r *Registry) addCounter(key string, amount float64) {
	r.mu.Lock()
	r.counters[key] += amount
	r.mu.Unlock()
}

func metricDelta(delta []float64) float64 {
	if len(delta) > 0 {
		return delta[0]
	}
	return 1
}

func (r *Registry) Gauge(name string, tags Tags, value float64) {
	if r == nil {
		return
	}
	r.GaugeKey(metricKey(name, tags), value)
}

// GaugeKey records a gauge using a key produced by MetricKey.
func (r *Registry) GaugeKey(key string, value float64) {
	if r == nil {
		return
	}
	key = metricKeyFromPrecomputed(key)
	r.mu.Lock()
	r.gauges[key] = value
	r.mu.Unlock()
}

func (r *Registry) Histogram(name string, tags Tags, value float64) {
	if r == nil {
		return
	}
	r.HistogramKey(metricKey(name, tags), value)
}

// HistogramKey records a histogram sample using a key produced by MetricKey.
func (r *Registry) HistogramKey(key string, value float64) {
	if r == nil {
		return
	}
	key = metricKeyFromPrecomputed(key)
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

func metricKeyFromPrecomputed(key string) string {
	if key == "" {
		return "unknown"
	}
	return key
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
	if r == nil {
		return ""
	}
	var b strings.Builder
	r.mu.RLock()
	defer r.mu.RUnlock()
	writeFloatMetrics(&b, r.counters)
	writeFloatMetrics(&b, r.gauges)
	keys := make([]string, 0, len(r.histograms))
	for key := range r.histograms {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		h := r.histograms[key]
		writeIntMetricLine(&b, key+"_count", h.Count)
		writeFloatMetricLine(&b, key+"_sum", h.Sum)
		writeFloatMetricLine(&b, key+"_min", h.Min)
		writeFloatMetricLine(&b, key+"_max", h.Max)
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
	var b strings.Builder
	b.Grow(len(clean) + len(keys)*12)
	b.WriteString(clean)
	for _, key := range keys {
		b.WriteByte('_')
		b.WriteString(sanitize(key))
		b.WriteByte('_')
		b.WriteString(sanitize(tags[key]))
	}
	return b.String()
}

func sanitize(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	clean := true
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == ':' {
			continue
		}
		clean = false
		break
	}
	if clean {
		return value
	}
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == ':' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	return b.String()
}

func writeFloatMetrics(b *strings.Builder, values map[string]float64) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		writeFloatMetricLine(b, key, values[key])
	}
}

func writeFloatMetricLine(b *strings.Builder, key string, value float64) {
	b.WriteString(key)
	b.WriteByte(' ')
	b.WriteString(strconv.FormatFloat(value, 'g', -1, 64))
	b.WriteByte('\n')
}

func writeIntMetricLine(b *strings.Builder, key string, value int64) {
	b.WriteString(key)
	b.WriteByte(' ')
	b.WriteString(strconv.FormatInt(value, 10))
	b.WriteByte('\n')
}

func cloneFloatMap(in map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(in))
	maps.Copy(out, in)
	return out
}

func cloneHistogramMap(in map[string]HistogramSnapshot) map[string]HistogramSnapshot {
	out := make(map[string]HistogramSnapshot, len(in))
	maps.Copy(out, in)
	return out
}
