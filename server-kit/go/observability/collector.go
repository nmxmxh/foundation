package observability

import (
	"sort"
	"sync"
	"time"
)

// Collector captures low-cost runtime metrics for HTTP, dispatch, and worker paths.
type Collector struct {
	mu sync.RWMutex

	httpRequests          map[string]int64
	dispatchCount         map[string]int64
	dispatchDurationMicro map[string]int64
	workerCount           map[string]int64
	workerQueueDepth      map[string]int64
}

func NewCollector() *Collector {
	return &Collector{
		httpRequests:          map[string]int64{},
		dispatchCount:         map[string]int64{},
		dispatchDurationMicro: map[string]int64{},
		workerCount:           map[string]int64{},
		workerQueueDepth:      map[string]int64{},
	}
}

var defaultCollector = NewCollector()

func Default() *Collector {
	return defaultCollector
}

func (c *Collector) RecordHTTPRequest(method, path string, status int) {
	if c == nil {
		return
	}
	key := method + " " + path + " " + itoa(status)
	c.mu.Lock()
	c.httpRequests[key]++
	c.mu.Unlock()
}

func (c *Collector) RecordDispatch(eventType, state string, duration time.Duration) {
	if c == nil {
		return
	}
	if eventType == "" {
		eventType = "unknown"
	}
	if state == "" {
		state = "unknown"
	}
	key := eventType + "|" + state
	c.mu.Lock()
	c.dispatchCount[key]++
	c.dispatchDurationMicro[key] += duration.Microseconds()
	c.mu.Unlock()
}

func (c *Collector) RecordWorker(kind, queue, state string) {
	if c == nil {
		return
	}
	if kind == "" {
		kind = "unknown"
	}
	if queue == "" {
		queue = "default"
	}
	if state == "" {
		state = "unknown"
	}
	key := kind + "|" + queue + "|" + state
	c.mu.Lock()
	c.workerCount[key]++
	c.mu.Unlock()
}

func (c *Collector) RecordQueueDepth(queue string, depth int) {
	if c == nil {
		return
	}
	if queue == "" {
		queue = "default"
	}
	if depth < 0 {
		depth = 0
	}
	c.mu.Lock()
	c.workerQueueDepth[queue] = int64(depth)
	c.mu.Unlock()
}

func (c *Collector) Snapshot() map[string]any {
	if c == nil {
		return map[string]any{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	http := cloneMap(c.httpRequests)
	dispatch := cloneMap(c.dispatchCount)
	dispatchAvgMicro := map[string]int64{}
	for key, count := range c.dispatchCount {
		if count <= 0 {
			continue
		}
		dispatchAvgMicro[key] = c.dispatchDurationMicro[key] / count
	}
	worker := cloneMap(c.workerCount)
	queueDepth := cloneMap(c.workerQueueDepth)

	return map[string]any{
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"http": map[string]any{
			"request_count": http,
		},
		"dispatch": map[string]any{
			"count":              dispatch,
			"avg_duration_micro": dispatchAvgMicro,
		},
		"worker": map[string]any{
			"count":       worker,
			"queue_depth": queueDepth,
		},
	}
}

func (c *Collector) Reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.httpRequests = map[string]int64{}
	c.dispatchCount = map[string]int64{}
	c.dispatchDurationMicro = map[string]int64{}
	c.workerCount = map[string]int64{}
	c.workerQueueDepth = map[string]int64{}
}

func cloneMap(in map[string]int64) map[string]int64 {
	if len(in) == 0 {
		return map[string]int64{}
	}
	out := make(map[string]int64, len(in))
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out[key] = in[key]
	}
	return out
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	buf := [20]byte{}
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
