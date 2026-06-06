package hermes

import (
	"context"
	"errors"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

type Store struct {
	mu          sync.RWMutex
	projections map[string]*partition
}

type partition struct {
	spec       ProjectionSpec
	mu         sync.Mutex
	epoch      atomic.Uint64
	watermark  atomic.Uint64
	registry   atomic.Pointer[partitionRegistry]
	publishing atomic.Bool

	tombstones map[string]tombstoneEntry
	tombOrder  []string
	applied    map[string]struct{}
	applyOrder []string

	bytes            atomic.Int64
	records          atomic.Int64
	rejectedApplies  atomic.Int64
	indexCompactions atomic.Int64
}

type partitionRegistry struct {
	records sync.Map
	scopes  sync.Map
	fields  sync.Map
}

type recordCell struct {
	ptr atomic.Pointer[recordEntry]
}

type indexCell struct {
	ptr atomic.Pointer[indexSnapshot]
}

type indexSnapshot struct {
	base    *indexSnapshot
	adds    map[string]struct{}
	removes map[string]struct{}
	order   []recordOrderEntry
	size    int
	depth   int
}

func NewStore(specs ...ProjectionSpec) (*Store, error) {
	store := &Store{projections: map[string]*partition{}}
	for _, spec := range specs {
		if err := store.Register(spec); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func (s *Store) Register(spec ProjectionSpec) error {
	normalized, err := normalizeSpec(spec)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.projections[normalized.Name]; exists {
		return nil
	}
	s.projections[normalized.Name] = newPartition(normalized)
	return nil
}

func (s *Store) Epoch(projection string) (uint64, error) {
	part, err := s.partition(projection)
	if err != nil {
		return 0, err
	}
	return part.epoch.Load(), nil
}

func (s *Store) Stats(projection string) (Stats, error) {
	part, err := s.partition(projection)
	if err != nil {
		return Stats{}, err
	}
	return part.stats(), nil
}

func (s *Store) AllStats() []Stats {
	s.mu.RLock()
	names := make([]string, 0, len(s.projections))
	parts := make(map[string]*partition, len(s.projections))
	for name, part := range s.projections {
		names = append(names, name)
		parts[name] = part
	}
	s.mu.RUnlock()
	sort.Strings(names)
	stats := make([]Stats, 0, len(names))
	for _, name := range names {
		stats = append(stats, parts[name].stats())
	}
	return stats
}

func (s *Store) Apply(ctx context.Context, projection string, event Event) (ApplyResult, error) {
	return s.ApplyBatch(ctx, projection, []Event{event})
}

func (s *Store) ApplyBatch(ctx context.Context, projection string, events []Event) (ApplyResult, error) {
	if err := ctxErr(ctx); err != nil {
		return ApplyResult{}, err
	}
	part, err := s.partition(projection)
	if err != nil {
		return ApplyResult{}, err
	}
	return part.applyBatch(ctx, events)
}

func (s *Store) ApplyRecords(ctx context.Context, projection string, sourcePrefix string, baseVersion uint64, records []database.DomainRecord) (ApplyResult, error) {
	if err := ctxErr(ctx); err != nil {
		return ApplyResult{}, err
	}
	part, err := s.partition(projection)
	if err != nil {
		return ApplyResult{}, err
	}
	return part.applyRecords(ctx, sourcePrefix, baseVersion, records)
}

// BulkLoad replaces a projection with a trusted, already-materialized snapshot.
//
// It is intended for rebuild, repair, and initial seeding paths after records
// have already crossed the durable source boundary. It still normalizes records,
// validates projection scope, enforces memory/record bounds, and publishes
// indexes atomically, but it deliberately skips per-event idempotency,
// tombstones, delete semantics, and source-event bookkeeping. Use ApplyBatch
// for durable mutation events and ApplyRecords for incremental pure-upsert
// projector batches.
func (s *Store) BulkLoad(ctx context.Context, projection string, records []database.DomainRecord) (ApplyResult, error) {
	if err := ctxErr(ctx); err != nil {
		return ApplyResult{}, err
	}
	part, err := s.partition(projection)
	if err != nil {
		return ApplyResult{}, err
	}
	return part.bulkLoad(ctx, records)
}

func (s *Store) GetRecord(ctx context.Context, projection string, query Query, recordID string, fence Fence) (database.DomainRecord, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return database.DomainRecord{}, false, err
	}
	part, err := s.partition(projection)
	if err != nil {
		return database.DomainRecord{}, false, err
	}
	return part.getRecord(ctx, query, recordID, fence)
}

func (s *Store) ListRecords(ctx context.Context, projection string, query Query, fence Fence) ([]database.DomainRecord, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	part, err := s.partition(projection)
	if err != nil {
		return nil, err
	}
	return part.listRecords(ctx, query, fence)
}

func (s *Store) Count(ctx context.Context, projection string, query Query, fence Fence) (int64, error) {
	if err := ctxErr(ctx); err != nil {
		return 0, err
	}
	part, err := s.partition(projection)
	if err != nil {
		return 0, err
	}
	return part.count(ctx, query, fence)
}

func (s *Store) ForEachView(ctx context.Context, projection string, query Query, fence Fence, fn func(RecordView) error) (int, error) {
	if fn == nil {
		return 0, errors.New("hermes view callback is required")
	}
	if err := ctxErr(ctx); err != nil {
		return 0, err
	}
	part, err := s.partition(projection)
	if err != nil {
		return 0, err
	}
	return part.forEachView(ctx, query, fence, fn)
}

func (s *Store) partition(name string) (*partition, error) {
	name = strings.TrimSpace(name)
	s.mu.RLock()
	part, ok := s.projections[name]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrProjectionNotFound
	}
	return part, nil
}

func newPartition(spec ProjectionSpec) *partition {
	part := &partition{
		spec:       spec,
		tombstones: map[string]tombstoneEntry{},
		applied:    map[string]struct{}{},
	}
	part.registry.Store(&partitionRegistry{})
	return part
}

func (p *partition) activeRegistry() *partitionRegistry {
	registry := p.registry.Load()
	if registry != nil {
		return registry
	}
	registry = &partitionRegistry{}
	p.registry.Store(registry)
	return registry
}

func (p *partition) stats() Stats {
	p.mu.Lock()
	tombstones := len(p.tombstones)
	applied := len(p.applied)
	p.mu.Unlock()
	return Stats{
		Projection:       p.spec.Name,
		Epoch:            p.epoch.Load(),
		SourceWatermark:  p.watermark.Load(),
		Records:          int(p.records.Load()),
		ApproxBytes:      p.bytes.Load(),
		Tombstones:       tombstones,
		AppliedEvents:    applied,
		RejectedApplies:  p.rejectedApplies.Load(),
		IndexCompactions: p.indexCompactions.Load(),
		MaxRecords:       p.spec.MaxRecords,
		MaxBytes:         p.spec.MaxBytes,
		MaxTombstones:    p.spec.MaxTombstones,
		MaxAppliedEvents: p.spec.MaxAppliedEvents,
	}
}

func (p *partition) getRecord(ctx context.Context, query Query, recordID string, fence Fence) (database.DomainRecord, bool, error) {
	if err := p.waitForStable(ctx); err != nil {
		return database.DomainRecord{}, false, err
	}
	if err := p.checkFence(fence); err != nil {
		return database.DomainRecord{}, false, err
	}
	query = normalizeQuery(query)
	recordID = strings.TrimSpace(recordID)
	if recordID == "" || query.OrganizationID == "" {
		return database.DomainRecord{}, false, ErrInvalidEvent
	}
	key := recordKey(p.spec.Domain, p.spec.Collection, query.OrganizationID, recordID)
	entry, ok := p.recordEntry(p.activeRegistry(), key)
	if !ok || !recordMatches(entry.record, p.spec, query) {
		return database.DomainRecord{}, false, nil
	}
	return copyRecord(entry.record), true, nil
}

func (p *partition) listRecords(ctx context.Context, query Query, fence Fence) ([]database.DomainRecord, error) {
	if err := p.waitForStable(ctx); err != nil {
		return nil, err
	}
	if err := p.checkFence(fence); err != nil {
		return nil, err
	}
	query = normalizeQuery(query)
	if query.OrganizationID == "" {
		return nil, ErrInvalidEvent
	}
	candidates, err := p.collectRecords(ctx, p.activeRegistry(), query)
	if err != nil {
		return nil, err
	}
	if query.Limit <= 0 || len(candidates) > 1 {
		sort.Slice(candidates, func(i int, j int) bool {
			return recordBefore(candidates[i], candidates[j])
		})
	}
	out := make([]database.DomainRecord, len(candidates))
	for i, rec := range candidates {
		out[i] = copyRecord(rec)
	}
	return out, nil
}

func (p *partition) count(ctx context.Context, query Query, fence Fence) (int64, error) {
	if err := p.waitForStable(ctx); err != nil {
		return 0, err
	}
	if err := p.checkFence(fence); err != nil {
		return 0, err
	}
	query = normalizeQuery(query)
	if query.OrganizationID == "" {
		return 0, ErrInvalidEvent
	}
	query.Limit = 0
	registry := p.activeRegistry()
	if count, ok := p.fastCount(registry, query); ok {
		return count, nil
	}
	index := p.candidateIndex(registry, query)
	if index != nil {
		return p.countIndex(ctx, registry, query, index)
	}
	return p.countAll(ctx, registry, query)
}

func (p *partition) fastCount(registry *partitionRegistry, query Query) (int64, bool) {
	scope := scopeKey(p.spec.Domain, p.spec.Collection, query.OrganizationID)
	if query.Plan.count == 0 {
		return int64(p.scopeSnapshot(registry, scope).len()), true
	}
	if query.Plan.count > 0 {
		if query.Plan.count != 1 {
			return 0, false
		}
		filter := query.Plan.first
		if !p.isIndexedField(filter.Field) {
			return 0, false
		}
		index := fieldIndex{scope: scope, field: filter.Field, kind: filter.Kind, value: filter.Value}
		return int64(p.fieldSnapshot(registry, index).len()), true
	}
	return 0, false
}

func (p *partition) countIndex(ctx context.Context, registry *partitionRegistry, query Query, index *indexSnapshot) (int64, error) {
	var count int64
	var err error
	index.forEachKey(func(key string) bool {
		entry, ok := p.recordEntry(registry, key)
		if !ok {
			return true
		}
		if err = ctxErr(ctx); err != nil {
			return false
		}
		if recordMatches(entry.record, p.spec, query) {
			count++
		}
		return true
	})
	return count, err
}

func (p *partition) countAll(ctx context.Context, registry *partitionRegistry, query Query) (int64, error) {
	var count int64
	var err error
	registry.records.Range(func(_ any, value any) bool {
		entry, ok := recordEntryFromCell(value)
		if !ok || !recordMatches(entry.record, p.spec, query) {
			return true
		}
		if err = ctxErr(ctx); err != nil {
			return false
		}
		count++
		return true
	})
	return count, err
}

func (p *partition) forEachView(ctx context.Context, query Query, fence Fence, fn func(RecordView) error) (int, error) {
	if err := p.waitForStable(ctx); err != nil {
		return 0, err
	}
	if err := p.checkFence(fence); err != nil {
		return 0, err
	}
	query = normalizeQuery(query)
	if query.OrganizationID == "" {
		return 0, ErrInvalidEvent
	}
	registry := p.activeRegistry()
	epoch := p.epoch.Load()
	return p.forEachViewSnapshot(ctx, registry, query, epoch, fn)
}

func (p *partition) checkFence(fence Fence) error {
	if fence.MinEpoch > p.epoch.Load() {
		return ErrFenceNotSatisfied
	}
	return nil
}

func (p *partition) waitForStable(ctx context.Context) error {
	for attempts := 0; p.publishing.Load(); attempts++ {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		if attempts > 1<<20 {
			return ErrProjectionBusy
		}
		runtime.Gosched()
	}
	return nil
}
