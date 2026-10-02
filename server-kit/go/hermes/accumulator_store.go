package hermes

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

// MetricSummary holds null-safe running aggregate metrics for a numeric field.
type MetricSummary struct {
	Count int64   `json:"count"`
	Sum   float64 `json:"sum"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Mean  float64 `json:"mean"`
}

// FacetCount represents a single value count within a dimension.
type FacetCount struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

// DimensionSummary holds frequency counts and distinct cardinality for a facet.
type DimensionSummary struct {
	Dimension     string       `json:"dimension"`
	DistinctCount int64        `json:"distinct_count"`
	TotalCount    int64        `json:"total_count"`
	TopValues     []FacetCount `json:"top_values,omitempty"`
}

// AccumulatorConfig configures the tracked dimensions and metrics for an AccumulatorStateStore.
type AccumulatorConfig struct {
	// Dimensions defines string/tag fields to track with frequency distributions and reference counts.
	Dimensions []string
	// NumericMetrics defines numeric fields to track with running sum, min, max, count, and mean.
	NumericMetrics []string
	// MaxDistinctPerDimension bounds distinct cardinality memory per dimension. Default is 10,000.
	MaxDistinctPerDimension int
	// MaxRecordsPerScope bounds how many record states one scope may retain.
	// Default is 10,000.
	//
	// A record state is what makes an update reversible: it holds the previous
	// dimension values and metric values so a later write can subtract them. So
	// the map cannot be trimmed entry by entry without silently double counting
	// whichever record lost its state. Exceeding the bound therefore drops the
	// whole scope, which reports no data rather than wrong data.
	MaxRecordsPerScope int
	// TTL mirrors the projection TTL, so a record the projection retires is
	// also released here. Default is 0, meaning no expiry, which suits an
	// accumulator over a projection without a TTL.
	TTL time.Duration
}

// recordAccumulatorState tracks the cached values and version of a single record for delta reversals.
type recordAccumulatorState struct {
	version    uint64
	dimensions map[string][]string
	metrics    map[string]float64
	expiresAt  time.Time
}

// numericAccumulator stores running sum and count plus raw values or extremum tracking.
type numericAccumulator struct {
	count int64
	sum   float64
	min   float64
	max   float64
}

func newNumericAccumulator() *numericAccumulator {
	return &numericAccumulator{
		min: math.Inf(1),
		max: math.Inf(-1),
	}
}

func (a *numericAccumulator) add(val float64) {
	a.count++
	a.sum += val
	if val < a.min {
		a.min = val
	}
	if val > a.max {
		a.max = val
	}
}

// summary computes the snapshot metric summary.
func (a *numericAccumulator) summary() MetricSummary {
	if a.count == 0 {
		return MetricSummary{}
	}
	return MetricSummary{
		Count: a.count,
		Sum:   a.sum,
		Min:   a.min,
		Max:   a.max,
		Mean:  a.sum / float64(a.count),
	}
}

// scopeAccumulator maintains aggregate statistics for one tenant/scope.
type scopeAccumulator struct {
	mu sync.RWMutex

	// records stores prior state for delta subtraction upon update or delete.
	records map[string]*recordAccumulatorState

	// dimensionFrequencies: dimension -> value -> reference count
	dimensionFrequencies map[string]map[string]int64

	// metrics: metricField -> numericAccumulator
	metrics map[string]*numericAccumulator

	maxDistinct int
}

func newScopeAccumulator(maxDistinct int) *scopeAccumulator {
	if maxDistinct <= 0 {
		maxDistinct = 10000
	}
	return &scopeAccumulator{
		records:              make(map[string]*recordAccumulatorState),
		dimensionFrequencies: make(map[string]map[string]int64),
		metrics:              make(map[string]*numericAccumulator),
		maxDistinct:          maxDistinct,
	}
}

// AccumulatorStateStore maintains incremental aggregates over Hermes projections.
// It eliminates O(N) full scans by computing O(1) state on every mutation.
type AccumulatorStateStore struct {
	cfg    AccumulatorConfig
	scopes sync.Map // map[string]*scopeAccumulator
}

// NewAccumulatorStateStore creates an accumulator store with the given configuration.
func NewAccumulatorStateStore(cfg AccumulatorConfig) *AccumulatorStateStore {
	if cfg.MaxDistinctPerDimension <= 0 {
		cfg.MaxDistinctPerDimension = 10000
	}
	if cfg.MaxRecordsPerScope <= 0 {
		cfg.MaxRecordsPerScope = defaultAccumulatorMaxRecordsPerScope
	}
	if cfg.TTL < 0 {
		cfg.TTL = 0
	}
	return &AccumulatorStateStore{
		cfg: cfg,
	}
}

// defaultAccumulatorMaxRecordsPerScope bounds the per-record state that makes
// delta reversal possible.
const defaultAccumulatorMaxRecordsPerScope = 10000

func (s *AccumulatorStateStore) getScope(scope string) *scopeAccumulator {
	if val, ok := s.scopes.Load(scope); ok {
		return val.(*scopeAccumulator)
	}
	created := newScopeAccumulator(s.cfg.MaxDistinctPerDimension)
	actual, _ := s.scopes.LoadOrStore(scope, created)
	return actual.(*scopeAccumulator)
}

// dropScope retires a scope whose record count passed the bound. The scope is
// removed rather than trimmed, because a partial trim would leave the surviving
// records unable to reverse a later write.
func (s *AccumulatorStateStore) dropScope(scope string) {
	s.scopes.Delete(scope)
}

func (s *AccumulatorStateStore) extractDimensions(rec database.DomainRecord) map[string][]string {
	result := make(map[string][]string, len(s.cfg.Dimensions))
	for _, dim := range s.cfg.Dimensions {
		switch dim {
		case "domain":
			if rec.Domain != "" {
				result[dim] = []string{rec.Domain}
			}
		case "collection":
			if rec.Collection != "" {
				result[dim] = []string{rec.Collection}
			}
		case "organization_id":
			if rec.OrganizationID != "" {
				result[dim] = []string{rec.OrganizationID}
			}
		default:
			val, ok := rec.Data.Get(dim)
			if !ok || val.Kind == database.RecordValueNull {
				continue
			}
			if val.Kind == database.RecordValueRaw && len(val.Raw) > 0 {
				trimmed := bytes.TrimSpace(val.Raw)
				if len(trimmed) > 0 && trimmed[0] == '[' {
					var arr []string
					if err := json.Unmarshal(trimmed, &arr); err == nil && len(arr) > 0 {
						result[dim] = arr
						continue
					}
				}
			}
			_, strVal, okScalar := val.ScalarIndex()
			if okScalar && strVal != "" {
				result[dim] = []string{strVal}
			} else if val.Text != "" {
				result[dim] = []string{val.Text}
			}
		}
	}
	return result
}

func (s *AccumulatorStateStore) extractMetrics(rec database.DomainRecord) map[string]float64 {
	result := make(map[string]float64, len(s.cfg.NumericMetrics))
	for _, metric := range s.cfg.NumericMetrics {
		val, ok := rec.Data.Get(metric)
		if !ok {
			continue
		}
		kind, idxVal, scalar := val.ScalarIndex()
		if !scalar {
			continue
		}
		if kind == 'f' || kind == 'i' || kind == 'u' {
			if f, err := strconv.ParseFloat(idxVal, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
				result[metric] = f
			}
		}
	}
	return result
}

// ApplyRecord updates the accumulator with a domain record mutation.
// It handles version ordering, updates with delta reversal, and reference counting.
func (s *AccumulatorStateStore) ApplyRecord(rec database.DomainRecord, version uint64, op Operation) {
	scopeKey := rec.Domain + ":" + rec.Collection + ":" + rec.OrganizationID
	sc := s.getScope(scopeKey)

	sc.mu.Lock()
	defer sc.mu.Unlock()

	recKey := rec.RecordID
	existing, found := sc.records[recKey]

	if op == OperationDelete {
		if found {
			if version >= existing.version {
				s.removeStateLocked(sc, existing)
				delete(sc.records, recKey)
			}
		}
		return
	}

	if found && version < existing.version {
		return
	}

	if found {
		s.removeStateLocked(sc, existing)
	}

	dims := s.extractDimensions(rec)
	metrics := s.extractMetrics(rec)

	for dim, vals := range dims {
		freqMap, exists := sc.dimensionFrequencies[dim]
		if !exists {
			freqMap = make(map[string]int64)
			sc.dimensionFrequencies[dim] = freqMap
		}
		for _, v := range vals {
			if len(freqMap) < sc.maxDistinct || freqMap[v] > 0 {
				freqMap[v]++
			}
		}
	}

	for metric, val := range metrics {
		acc, exists := sc.metrics[metric]
		if !exists {
			acc = newNumericAccumulator()
			sc.metrics[metric] = acc
		}
		acc.add(val)
	}

	sc.records[recKey] = &recordAccumulatorState{
		version:    version,
		dimensions: dims,
		metrics:    metrics,
		expiresAt:  s.expiresAtLocked(),
	}
	if len(sc.records) > s.cfg.MaxRecordsPerScope {
		s.dropScope(scopeKey)
	}
}

// expiresAtLocked reports when a record state collected now stops being
// reversible. It is zero when no TTL is configured, which is what makes
// EvictExpired a no-op rather than a lie.
func (s *AccumulatorStateStore) expiresAtLocked() time.Time {
	if s.cfg.TTL <= 0 {
		return time.Time{}
	}
	return time.Now().Add(s.cfg.TTL)
}

func (s *AccumulatorStateStore) removeStateLocked(sc *scopeAccumulator, state *recordAccumulatorState) {
	for dim, vals := range state.dimensions {
		freqMap, exists := sc.dimensionFrequencies[dim]
		if !exists {
			continue
		}
		for _, v := range vals {
			count := freqMap[v]
			if count <= 1 {
				delete(freqMap, v)
			} else {
				freqMap[v] = count - 1
			}
		}
		if len(freqMap) == 0 {
			delete(sc.dimensionFrequencies, dim)
		}
	}

	for metric, val := range state.metrics {
		acc, exists := sc.metrics[metric]
		if !exists {
			continue
		}
		if acc.count <= 1 {
			delete(sc.metrics, metric)
		} else {
			acc.count--
			acc.sum -= val
		}
	}
}

// EvictExpired removes expired records from the accumulator and returns how
// many it released.
//
// It only runs where AccumulatorConfig.TTL is set, because that is the only way
// a record state can know it is stale. A projection TTL alone does not reach
// here, but the projection now reports its own retirements as deletes, and
// ApplyRecord reverses those exactly, so the two paths agree.
func (s *AccumulatorStateStore) EvictExpired(now time.Time) int {
	if s.cfg.TTL <= 0 {
		return 0
	}
	evicted := 0
	s.scopes.Range(func(key, val any) bool {
		sc := val.(*scopeAccumulator)
		sc.mu.Lock()
		for recKey, state := range sc.records {
			if !state.expiresAt.IsZero() && now.After(state.expiresAt) {
				s.removeStateLocked(sc, state)
				delete(sc.records, recKey)
				evicted++
			}
		}
		sc.mu.Unlock()
		return true
	})
	return evicted
}

// GetDimensionSummary returns the O(1) precomputed summary for a dimension in a scope.
func (s *AccumulatorStateStore) GetDimensionSummary(scope, dimension string, topN int) DimensionSummary {
	sc := s.getScope(scope)
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	freqMap, ok := sc.dimensionFrequencies[dimension]
	if !ok || len(freqMap) == 0 {
		return DimensionSummary{Dimension: dimension}
	}

	var total int64
	facets := make([]FacetCount, 0, len(freqMap))
	for val, count := range freqMap {
		total += count
		facets = append(facets, FacetCount{Value: val, Count: count})
	}

	if topN > 0 && len(facets) > topN {
		sort.Slice(facets, func(i, j int) bool {
			if facets[i].Count == facets[j].Count {
				return facets[i].Value < facets[j].Value
			}
			return facets[i].Count > facets[j].Count
		})
		facets = facets[:topN]
	}

	return DimensionSummary{
		Dimension:     dimension,
		DistinctCount: int64(len(freqMap)),
		TotalCount:    total,
		TopValues:     facets,
	}
}

// GetMetricSummary returns the O(1) precomputed metric summary for a numeric field in a scope.
func (s *AccumulatorStateStore) GetMetricSummary(scope, metricField string) MetricSummary {
	sc := s.getScope(scope)
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	acc, ok := sc.metrics[metricField]
	if !ok {
		return MetricSummary{}
	}
	return acc.summary()
}

// GetFacetManifest returns the full manifest of all dimensions and counts in O(1) memory lookup.
func (s *AccumulatorStateStore) GetFacetManifest(scope string, topNPerDim int) []DimensionSummary {
	sc := s.getScope(scope)
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	result := make([]DimensionSummary, 0, len(s.cfg.Dimensions))
	for _, dim := range s.cfg.Dimensions {
		freqMap, ok := sc.dimensionFrequencies[dim]
		if !ok || len(freqMap) == 0 {
			result = append(result, DimensionSummary{Dimension: dim})
			continue
		}
		var total int64
		facets := make([]FacetCount, 0, len(freqMap))
		for val, count := range freqMap {
			total += count
			facets = append(facets, FacetCount{Value: val, Count: count})
		}
		sort.Slice(facets, func(i, j int) bool {
			if facets[i].Count == facets[j].Count {
				return facets[i].Value < facets[j].Value
			}
			return facets[i].Count > facets[j].Count
		})
		if topNPerDim > 0 && len(facets) > topNPerDim {
			facets = facets[:topNPerDim]
		}
		result = append(result, DimensionSummary{
			Dimension:     dim,
			DistinctCount: int64(len(freqMap)),
			TotalCount:    total,
			TopValues:     facets,
		})
	}
	return result
}

// AttachToStore connects the accumulator store to a Hermes Store via an apply observer.
func (s *AccumulatorStateStore) AttachToStore(store *Store) func() {
	if store == nil {
		return func() {}
	}
	return store.Observe(func(projection string, mutations []AppliedMutation) {
		for _, m := range mutations {
			s.ApplyRecord(m.Record, m.Version, m.Operation)
		}
	})
}

// Observer returns an AppliedBatchObserver function for manual registration.
func (s *AccumulatorStateStore) Observer() AppliedBatchObserver {
	return func(projection string, mutations []AppliedMutation) {
		for _, m := range mutations {
			s.ApplyRecord(m.Record, m.Version, m.Operation)
		}
	}
}

// ScopeKey returns the formatted scope identifier.
func ScopeKey(domain, collection, organizationID string) string {
	return domain + ":" + collection + ":" + organizationID
}

// CheckContext verifies whether the context was canceled.
func (s *AccumulatorStateStore) CheckContext(ctx context.Context) error {
	return ctxErr(ctx)
}
