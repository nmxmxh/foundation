package observability

import (
	"maps"
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
	redisCount            map[string]int64
	redisDurationMicro    map[string]int64
	databaseCount         map[string]int64
	databaseDurationMicro map[string]int64
	databasePool          map[string]DatabasePoolPressure
	workerCount           map[string]int64
	workerQueueDepth      map[string]int64
	concurrencyCount      map[string]int64
	concurrencyGauge      map[string]int64
	concurrencyDuration   map[string]int64
	concurrencySamples    map[string]int64
	traceEvents           map[string]*traceBuffer
	traceOrder            []string
	maxTraceCorrelations  int
	maxTraceEvents        int
}

type traceBuffer struct {
	events []TraceEvent
	start  int
	count  int
}

type TraceEvent struct {
	CorrelationID string            `json:"correlation_id"`
	Stage         string            `json:"stage"`
	EventType     string            `json:"event_type,omitempty"`
	State         string            `json:"state,omitempty"`
	Detail        string            `json:"detail,omitempty"`
	Fields        map[string]string `json:"fields,omitempty"`
	Timestamp     time.Time         `json:"timestamp"`
}

type DatabasePoolPressure struct {
	ActiveConns          int32 `json:"active_conns"`
	IdleConns            int32 `json:"idle_conns"`
	TotalConns           int32 `json:"total_conns"`
	MaxConns             int32 `json:"max_conns"`
	AcquireCount         int64 `json:"acquire_count"`
	AcquireDurationMicro int64 `json:"acquire_duration_micro"`
}

type Snapshot struct {
	Timestamp   string              `json:"timestamp"`
	HTTP        HTTPMetrics         `json:"http"`
	Dispatch    DurationMetrics     `json:"dispatch"`
	Redis       DurationMetrics     `json:"redis"`
	Database    DatabaseMetrics     `json:"database"`
	Worker      WorkerMetrics       `json:"worker"`
	Concurrency ConcurrencyMetrics  `json:"concurrency"`
	Traces      TraceMetricsSummary `json:"traces"`
}

type HTTPMetrics struct {
	RequestCount map[string]int64 `json:"request_count"`
}

type DurationMetrics struct {
	Count            map[string]int64 `json:"count"`
	AvgDurationMicro map[string]int64 `json:"avg_duration_micro"`
}

type DatabaseMetrics struct {
	Count            map[string]int64                `json:"count"`
	AvgDurationMicro map[string]int64                `json:"avg_duration_micro"`
	Pool             map[string]DatabasePoolPressure `json:"pool"`
}

type WorkerMetrics struct {
	Count      map[string]int64 `json:"count"`
	QueueDepth map[string]int64 `json:"queue_depth"`
}

type ConcurrencyMetrics struct {
	Count            map[string]int64 `json:"count"`
	Gauge            map[string]int64 `json:"gauge"`
	AvgDurationMicro map[string]int64 `json:"avg_duration_micro"`
}

type TraceMetricsSummary struct {
	CorrelationCount int `json:"correlation_count"`
	EventCount       int `json:"event_count"`
}

func NewCollector() *Collector {
	return &Collector{
		httpRequests:          map[string]int64{},
		dispatchCount:         map[string]int64{},
		dispatchDurationMicro: map[string]int64{},
		redisCount:            map[string]int64{},
		redisDurationMicro:    map[string]int64{},
		databaseCount:         map[string]int64{},
		databaseDurationMicro: map[string]int64{},
		databasePool:          map[string]DatabasePoolPressure{},
		workerCount:           map[string]int64{},
		workerQueueDepth:      map[string]int64{},
		concurrencyCount:      map[string]int64{},
		concurrencyGauge:      map[string]int64{},
		concurrencyDuration:   map[string]int64{},
		concurrencySamples:    map[string]int64{},
		traceEvents:           map[string]*traceBuffer{},
		traceOrder:            []string{},
		maxTraceCorrelations:  1024,
		maxTraceEvents:        256,
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

func (c *Collector) RecordRedisOperation(operation, state string, duration time.Duration) {
	if c == nil {
		return
	}
	if operation == "" {
		operation = "unknown"
	}
	if state == "" {
		state = "unknown"
	}
	key := operation + "|" + state
	c.mu.Lock()
	c.redisCount[key]++
	c.redisDurationMicro[key] += duration.Microseconds()
	c.mu.Unlock()
}

func (c *Collector) RecordDatabaseOperation(operation, state string, duration time.Duration) {
	if c == nil {
		return
	}
	if operation == "" {
		operation = "unknown"
	}
	if state == "" {
		state = "unknown"
	}
	key := operation + "|" + state
	c.mu.Lock()
	c.databaseCount[key]++
	c.databaseDurationMicro[key] += duration.Microseconds()
	c.mu.Unlock()
}

func (c *Collector) RecordDatabasePool(name string, active, idle, total, max int32, acquireCount int64, acquireDuration time.Duration) {
	if c == nil {
		return
	}
	if name == "" {
		name = "default"
	}
	c.mu.Lock()
	c.databasePool[name] = DatabasePoolPressure{
		ActiveConns:          active,
		IdleConns:            idle,
		TotalConns:           total,
		MaxConns:             max,
		AcquireCount:         acquireCount,
		AcquireDurationMicro: acquireDuration.Microseconds(),
	}
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

func (c *Collector) RecordConcurrency(component, primitive, state string) {
	if c == nil {
		return
	}
	if component == "" {
		component = "unknown"
	}
	if primitive == "" {
		primitive = "unknown"
	}
	if state == "" {
		state = "unknown"
	}
	key := component + "|" + primitive + "|" + state
	c.mu.Lock()
	c.concurrencyCount[key]++
	c.mu.Unlock()
}

func (c *Collector) RecordConcurrencyGauge(component, name string, value int64) {
	if c == nil {
		return
	}
	if component == "" {
		component = "unknown"
	}
	if name == "" {
		name = "unknown"
	}
	if value < 0 {
		value = 0
	}
	key := component + "|" + name
	c.mu.Lock()
	c.concurrencyGauge[key] = value
	c.mu.Unlock()
}

func (c *Collector) RecordConcurrencyDuration(component, operation, state string, duration time.Duration) {
	if c == nil {
		return
	}
	if component == "" {
		component = "unknown"
	}
	if operation == "" {
		operation = "unknown"
	}
	if state == "" {
		state = "unknown"
	}
	key := component + "|" + operation + "|" + state
	c.mu.Lock()
	c.concurrencySamples[key]++
	c.concurrencyDuration[key] += duration.Microseconds()
	c.mu.Unlock()
}

func (c *Collector) RecordTrace(correlationID, stage, eventType, state, detail string, fields map[string]string) {
	if c == nil {
		return
	}
	if correlationID == "" {
		return
	}
	if stage == "" {
		stage = "unknown"
	}
	event := TraceEvent{
		CorrelationID: correlationID,
		Stage:         stage,
		EventType:     eventType,
		State:         state,
		Detail:        detail,
		Fields:        cloneStringMap(fields),
		Timestamp:     time.Now().UTC(),
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	buf, exists := c.traceEvents[correlationID]
	if !exists {
		size := c.maxTraceEvents
		if size <= 0 {
			return
		}
		buf = &traceBuffer{events: make([]TraceEvent, size)}
		c.traceEvents[correlationID] = buf
		c.traceOrder = append(c.traceOrder, correlationID)
		c.evictTraceCorrelationsLocked()
	}
	if len(buf.events) == 0 {
		return
	}
	if buf.count < len(buf.events) {
		index := (buf.start + buf.count) % len(buf.events)
		buf.events[index] = event
		buf.count++
		return
	}
	buf.events[buf.start] = event
	buf.start = (buf.start + 1) % len(buf.events)
}

func (c *Collector) Trace(correlationID string, limit int) []TraceEvent {
	if c == nil || correlationID == "" {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	buf := c.traceEvents[correlationID]
	if buf == nil || buf.count == 0 || len(buf.events) == 0 {
		return nil
	}
	if limit <= 0 || limit > buf.count {
		limit = buf.count
	}
	out := make([]TraceEvent, limit)
	start := (buf.start + buf.count - limit) % len(buf.events)
	for i := 0; i < limit; i++ {
		out[i] = cloneTraceEvent(buf.events[(start+i)%len(buf.events)])
	}
	return out
}

func (c *Collector) Snapshot() Snapshot {
	if c == nil {
		return Snapshot{}
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
	redis := cloneMap(c.redisCount)
	redisAvgMicro := averageMap(c.redisCount, c.redisDurationMicro)
	database := cloneMap(c.databaseCount)
	databaseAvgMicro := averageMap(c.databaseCount, c.databaseDurationMicro)
	databasePool := cloneDatabasePoolMap(c.databasePool)
	worker := cloneMap(c.workerCount)
	queueDepth := cloneMap(c.workerQueueDepth)
	concurrency := cloneMap(c.concurrencyCount)
	concurrencyGauge := cloneMap(c.concurrencyGauge)
	concurrencyAvgMicro := averageMap(c.concurrencySamples, c.concurrencyDuration)
	traceCount := len(c.traceEvents)
	traceEvents := 0
	for _, buf := range c.traceEvents {
		if buf != nil {
			traceEvents += buf.count
		}
	}

	return Snapshot{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		HTTP: HTTPMetrics{
			RequestCount: http,
		},
		Dispatch: DurationMetrics{
			Count:            dispatch,
			AvgDurationMicro: dispatchAvgMicro,
		},
		Redis: DurationMetrics{
			Count:            redis,
			AvgDurationMicro: redisAvgMicro,
		},
		Database: DatabaseMetrics{
			Count:            database,
			AvgDurationMicro: databaseAvgMicro,
			Pool:             databasePool,
		},
		Worker: WorkerMetrics{
			Count:      worker,
			QueueDepth: queueDepth,
		},
		Concurrency: ConcurrencyMetrics{
			Count:            concurrency,
			Gauge:            concurrencyGauge,
			AvgDurationMicro: concurrencyAvgMicro,
		},
		Traces: TraceMetricsSummary{
			CorrelationCount: traceCount,
			EventCount:       traceEvents,
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
	c.redisCount = map[string]int64{}
	c.redisDurationMicro = map[string]int64{}
	c.databaseCount = map[string]int64{}
	c.databaseDurationMicro = map[string]int64{}
	c.databasePool = map[string]DatabasePoolPressure{}
	c.workerCount = map[string]int64{}
	c.workerQueueDepth = map[string]int64{}
	c.concurrencyCount = map[string]int64{}
	c.concurrencyGauge = map[string]int64{}
	c.concurrencyDuration = map[string]int64{}
	c.concurrencySamples = map[string]int64{}
	c.traceEvents = map[string]*traceBuffer{}
	c.traceOrder = []string{}
}

func (c *Collector) evictTraceCorrelationsLocked() {
	if c.maxTraceCorrelations <= 0 {
		return
	}
	for len(c.traceOrder) > c.maxTraceCorrelations {
		evicted := c.traceOrder[0]
		c.traceOrder = c.traceOrder[1:]
		delete(c.traceEvents, evicted)
	}
}

func cloneTraceEvent(in TraceEvent) TraceEvent {
	in.Fields = cloneStringMap(in.Fields)
	return in
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
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

func averageMap(counts, durations map[string]int64) map[string]int64 {
	out := map[string]int64{}
	if len(counts) == 0 {
		return out
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		count := counts[key]
		if count <= 0 {
			continue
		}
		out[key] = durations[key] / count
	}
	return out
}

func cloneDatabasePoolMap(in map[string]DatabasePoolPressure) map[string]DatabasePoolPressure {
	if len(in) == 0 {
		return map[string]DatabasePoolPressure{}
	}
	out := make(map[string]DatabasePoolPressure, len(in))
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
