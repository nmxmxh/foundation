package hermes

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

type Store struct {
	mu          sync.RWMutex
	projections map[string]*partition

	obsMu     sync.RWMutex
	observers map[int]AppliedBatchObserver
	obsSeq    int
}

// AppliedBatchObserver is notified, once per apply call, with the mutations the
// store accepted (stamped with assigned versions). It fires after the partition
// lock is released, so observers may do bounded work (e.g. fan a delta out to
// subscribers) without serializing applies. It is the universal delta seam: it
// fires for every accepted write regardless of path (in-process projected store,
// Redis envelope projector, or direct ApplyBatch/ApplyRecords).
type AppliedBatchObserver func(projection string, mutations []AppliedMutation)

// Observe registers an apply observer and returns a cancel function. Safe for
// concurrent use; cancel is idempotent.
func (s *Store) Observe(fn AppliedBatchObserver) func() {
	if fn == nil {
		return func() {}
	}
	s.obsMu.Lock()
	if s.observers == nil {
		s.observers = make(map[int]AppliedBatchObserver)
	}
	s.obsSeq++
	id := s.obsSeq
	s.observers[id] = fn
	s.obsMu.Unlock()
	return func() {
		s.obsMu.Lock()
		delete(s.observers, id)
		s.obsMu.Unlock()
	}
}

func (s *Store) hasObservers() bool {
	s.obsMu.RLock()
	defer s.obsMu.RUnlock()
	return len(s.observers) > 0
}

// collector returns an apply observe callback that appends accepted mutations to
// dst, or nil when there are no observers (so the apply path stays
// allocation-free when nothing is listening).
func (s *Store) collector(dst *[]AppliedMutation) func(AppliedMutation) {
	if !s.hasObservers() {
		return nil
	}
	return func(m AppliedMutation) { *dst = append(*dst, m) }
}

// notify fans an accepted batch out to registered observers after the partition
// lock has been released.
func (s *Store) notify(projection string, mutations []AppliedMutation) {
	if len(mutations) == 0 {
		return
	}
	s.obsMu.RLock()
	observers := make([]AppliedBatchObserver, 0, len(s.observers))
	for _, fn := range s.observers {
		observers = append(observers, fn)
	}
	s.obsMu.RUnlock()
	for _, fn := range observers {
		fn(projection, mutations)
	}
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
	watermarks map[string]uint64

	bytes            atomic.Int64
	records          atomic.Int64
	rejectedApplies  atomic.Int64
	indexCompactions atomic.Int64
}

type partitionRegistry struct {
	records shardedMap
	scopes  shardedMap
	fields  shardedMap
}

const numShards = 128

type shardedMap struct {
	shards [numShards]*sync.Map
}

func newShardedMap() *shardedMap {
	sm := &shardedMap{}
	for i := range numShards {
		sm.shards[i] = &sync.Map{}
	}
	return sm
}

func hashString(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h = (h ^ uint32(s[i])) * 16777619
	}
	return h
}

func (sm *shardedMap) getShard(key any) *sync.Map {
	var hash uint32
	switch k := key.(type) {
	case string:
		hash = hashString(k)
	case fieldIndex:
		hash = hashString(k.scope.domain + ":" + k.scope.collection + ":" + k.scope.organizationID + ":" + k.field + ":" + k.value)
	case recordScope:
		hash = hashString(k.domain + ":" + k.collection + ":" + k.organizationID)
	default:
		hash = 0
	}
	return sm.shards[hash%numShards]
}

func (sm *shardedMap) Load(key any) (any, bool) {
	return sm.getShard(key).Load(key)
}

func (sm *shardedMap) Store(key any, val any) {
	sm.getShard(key).Store(key, val)
}

func (sm *shardedMap) LoadOrStore(key any, val any) (any, bool) {
	return sm.getShard(key).LoadOrStore(key, val)
}

func (sm *shardedMap) Delete(key any) {
	sm.getShard(key).Delete(key)
}

func (sm *shardedMap) Range(f func(key, value any) bool) {
	for i := range numShards {
		var keepGoing = true
		sm.shards[i].Range(func(k, v any) bool {
			keepGoing = f(k, v)
			return keepGoing
		})
		if !keepGoing {
			break
		}
	}
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
	var accepted []AppliedMutation
	result, err := part.applyBatchObserving(ctx, events, s.collector(&accepted))
	if err == nil {
		s.notify(projection, accepted)
	}
	return result, err
}

// ApplyBatchObserved applies events and invokes observe for each event the
// store accepts, stamped with the assigned version. It is the source-of-truth
// apply path for the projection gateway: only accepted mutations reach the
// observer, so the live delta stream never reports a rejected or deduplicated
// write as visible state.
func (s *Store) ApplyBatchObserved(ctx context.Context, projection string, events []Event, observe func(AppliedMutation)) (ApplyResult, error) {
	if err := ctxErr(ctx); err != nil {
		return ApplyResult{}, err
	}
	part, err := s.partition(projection)
	if err != nil {
		return ApplyResult{}, err
	}
	var accepted []AppliedMutation
	collect := s.collector(&accepted)
	combined := observe
	if collect != nil {
		combined = func(m AppliedMutation) {
			if observe != nil {
				observe(m)
			}
			collect(m)
		}
	}
	result, err := part.applyBatchObserving(ctx, events, combined)
	if err == nil {
		s.notify(projection, accepted)
	}
	return result, err
}

func (s *Store) ApplyRecords(ctx context.Context, projection string, sourcePrefix string, baseVersion uint64, records []database.DomainRecord) (ApplyResult, error) {
	if err := ctxErr(ctx); err != nil {
		return ApplyResult{}, err
	}
	part, err := s.partition(projection)
	if err != nil {
		return ApplyResult{}, err
	}
	var accepted []AppliedMutation
	result, err := part.applyRecords(ctx, sourcePrefix, baseVersion, records, s.collector(&accepted))
	if err == nil {
		s.notify(projection, accepted)
	}
	return result, err
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

func newPartitionRegistry() *partitionRegistry {
	return &partitionRegistry{
		records: *newShardedMap(),
		scopes:  *newShardedMap(),
		fields:  *newShardedMap(),
	}
}

func newPartition(spec ProjectionSpec) *partition {
	part := &partition{
		spec:       spec,
		tombstones: map[string]tombstoneEntry{},
		applied:    map[string]struct{}{},
		watermarks: map[string]uint64{},
	}
	part.registry.Store(newPartitionRegistry())
	return part
}

func (p *partition) activeRegistry() *partitionRegistry {
	registry := p.registry.Load()
	if registry != nil {
		return registry
	}
	registry = newPartitionRegistry()
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
	batch, err := p.getColumnarBatch(ctx, query, []string{"_record"}, fence)
	if err != nil {
		return nil, err
	}

	out := make([]database.DomainRecord, batch.Rows)
	var recordVec *DomainRecordVector
	for _, col := range batch.Columns {
		if col.Name == "_record" {
			if rv, ok := col.Data.(*DomainRecordVector); ok {
				recordVec = rv
			}
		}
	}

	if recordVec == nil {
		return nil, errors.New("hermes list records missing _record vector")
	}

	for i := 0; i < batch.Rows; i++ {
		out[i] = copyRecord(recordVec.values[i])
	}

	return out, nil
}

func (p *partition) count(ctx context.Context, query Query, fence Fence) (int64, error) {
	batch, err := p.getColumnarBatch(ctx, query, []string{"record_id"}, fence)
	if err != nil {
		return 0, err
	}
	return int64(batch.Rows), nil
}

func (p *partition) forEachView(ctx context.Context, query Query, fence Fence, fn func(RecordView) error) (int, error) {
	batch, err := p.getColumnarBatch(ctx, query, []string{"_record", "version"}, fence)
	if err != nil {
		return 0, err
	}

	var recordVec *DomainRecordVector
	var versionVec Vector
	for _, col := range batch.Columns {
		switch col.Name {
		case "_record":
			if rv, ok := col.Data.(*DomainRecordVector); ok {
				recordVec = rv
			}
		case "version":
			versionVec = col.Data
		}
	}

	if recordVec == nil || versionVec == nil {
		return 0, errors.New("hermes view batch missing record or version vector")
	}

	epoch := p.epoch.Load()
	seen := 0
	versions := versionVec.Int64Values()

	for i := 0; i < batch.Rows; i++ {
		if err := ctxErr(ctx); err != nil {
			return seen, err
		}
		rec := recordVec.values[i]
		view := RecordView{
			Domain:         rec.Domain,
			Collection:     rec.Collection,
			OrganizationID: rec.OrganizationID,
			RecordID:       rec.RecordID,
			Data:           rec.Data,
			Vector:         rec.Vector,
			CreatedAt:      rec.CreatedAt,
			UpdatedAt:      rec.UpdatedAt,
			// #nosec G115
			Version: uint64(versions[i]),
			Epoch:   epoch,
		}
		if err := fn(view); err != nil {
			return seen, err
		}
		seen++
	}

	return seen, nil
}

func (p *partition) checkFence(fence Fence) error {
	if fence.MinEpoch > p.epoch.Load() {
		return ErrFenceNotSatisfied
	}
	return nil
}

func (p *partition) waitForStable(_ context.Context) error {
	return nil
}
