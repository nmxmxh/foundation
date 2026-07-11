package hermes

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/singleflight"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

const (
	DefaultStateProjectionPrefix = "foundation_state"
	DefaultStateMaxRecords       = 10000
	DefaultStateMaxBytes         = 16 << 20
)

var DefaultStateIndexedFields = []string{"state", "status", "type", "kind", "bucket"}

type RuntimeStoreOptions struct {
	ProjectionPrefix   string
	IndexedFields      []string
	RangeIndexedFields []string
	MaxRecordsPerScope int
	MaxBytesPerScope   int64
	// SnapshotStore enables the shadow-mode snapshot rollout: every successful
	// source rebuild diffs the newest durable artifact against the freshly
	// rebuilt partition (evidence counters in HermesRuntimeStats) and then
	// refreshes the artifact to the rebuilt state. The served warm path is
	// unchanged — snapshots are compared and produced, never yet preferred.
	SnapshotStore SnapshotStore
	// ScopeBackfill makes warms self-backfilling: when a warm finds the record
	// mirror EMPTY for a scope, the store pulls the scope's rows from the app's
	// authoritative tables through this enumerator (batched via UpsertRecords)
	// before rebuilding — so eagerly configured warm scopes work on first boot
	// with no separate backfill step or ordering ritual. Called at most once
	// per cold-empty scope per process (inside the warm singleflight); a
	// non-empty mirror never triggers it, and ongoing EnqueueTx projection
	// writes keep the mirror current afterward. Errors degrade to the normal
	// lazy-warm fallback, never fail the store.
	ScopeBackfill func(ctx context.Context, domain, collection, organizationID string, visit database.RecordVisitor) error
}

type ProjectedRuntimeStore struct {
	base database.RuntimeStore
	hot  *Store
	opts RuntimeStoreOptions

	version       atomic.Uint64
	degradedCount atomic.Int64
	fallbackCount atomic.Int64
	registered    sync.Map
	warm          sync.Map
	degraded      sync.Map
	rebuildSF     singleflight.Group

	// Shadow-mode snapshot evidence (see RuntimeStoreOptions.SnapshotStore).
	shadowMatches    atomic.Int64
	shadowMismatches atomic.Int64
	shadowErrors     atomic.Int64
	shadowSaves      atomic.Int64
}

func WrapRuntimeStore(base database.RuntimeStore, opts RuntimeStoreOptions) (*ProjectedRuntimeStore, error) {
	if base == nil {
		return nil, errors.New("hermes runtime store requires a database runtime store")
	}
	opts = normalizeRuntimeStoreOptions(opts)
	hot, err := NewStore()
	if err != nil {
		return nil, err
	}
	return &ProjectedRuntimeStore{base: base, hot: hot, opts: opts}, nil
}

func normalizeRuntimeStoreOptions(opts RuntimeStoreOptions) RuntimeStoreOptions {
	if strings.TrimSpace(opts.ProjectionPrefix) == "" {
		opts.ProjectionPrefix = DefaultStateProjectionPrefix
	}
	if len(opts.IndexedFields) == 0 {
		opts.IndexedFields = DefaultStateIndexedFields
	}
	if opts.MaxRecordsPerScope <= 0 {
		opts.MaxRecordsPerScope = DefaultStateMaxRecords
	}
	if opts.MaxBytesPerScope <= 0 {
		opts.MaxBytesPerScope = DefaultStateMaxBytes
	}
	return opts
}

func (s *ProjectedRuntimeStore) Store() *Store {
	if s == nil {
		return nil
	}
	return s.hot
}

// Base exposes the wrapped runtime store so composition seams (e.g. a worker
// engine needing the underlying *database.PostgresDB pool for its river
// client) can reach driver capabilities without new constructor signatures.
// Writes must still go through the projected store, never the base directly.
func (s *ProjectedRuntimeStore) Base() database.RuntimeStore {
	if s == nil {
		return nil
	}
	return s.base
}

func (s *ProjectedRuntimeStore) ProjectionName(domain, collection, organizationID string) string {
	return projectionName(s.opts.ProjectionPrefix, domain, collection, organizationID)
}

func (s *ProjectedRuntimeStore) HermesHealth(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if s == nil || s.hot == nil {
		return errors.New("hermes projected runtime store is not initialized")
	}
	if count := s.degradedCount.Load(); count > 0 {
		return fmt.Errorf("hermes projected runtime store has %d degraded projection scopes", count)
	}
	return nil
}

func (s *ProjectedRuntimeStore) HermesRuntimeStats() RuntimeStats {
	if s == nil || s.hot == nil {
		return RuntimeStats{}
	}
	return RuntimeStats{
		Projections:              s.hot.AllStats(),
		Fallbacks:                s.fallbackCount.Load(),
		DegradedScopes:           s.degradedCount.Load(),
		SnapshotShadowMatches:    s.shadowMatches.Load(),
		SnapshotShadowMismatches: s.shadowMismatches.Load(),
		SnapshotShadowErrors:     s.shadowErrors.Load(),
		SnapshotSaves:            s.shadowSaves.Load(),
	}
}

// Exec executes a query directly on the underlying database.
// Note: This operation bypasses the Hermes hot-cache projection entirely.
func (s *ProjectedRuntimeStore) Exec(ctx context.Context, query string, args ...any) error {
	return s.base.Exec(ctx, query, args...)
}

// ExecResult executes a query directly on the underlying database and returns the command result.
// Note: This operation bypasses the Hermes hot-cache projection entirely.
func (s *ProjectedRuntimeStore) ExecResult(ctx context.Context, query string, args ...any) (database.CommandResult, error) {
	executor, ok := s.base.(database.ResultExecutor)
	if !ok {
		return nil, errors.New("wrapped runtime store does not expose command results")
	}
	return executor.ExecResult(ctx, query, args...)
}

// QueryRow executes a query directly on the underlying database and returns a row scanner.
// Note: This operation bypasses the Hermes hot-cache projection entirely.
func (s *ProjectedRuntimeStore) QueryRow(ctx context.Context, query string, args ...any) database.RowScanner {
	return s.base.QueryRow(ctx, query, args...)
}

// Query executes a query directly on the underlying database and returns rows.
// Note: This operation bypasses the Hermes hot-cache projection entirely.
func (s *ProjectedRuntimeStore) Query(ctx context.Context, query string, args ...any) (database.Rows, error) {
	queryer, ok := s.base.(database.RowQueryer)
	if !ok {
		return nil, errors.New("wrapped runtime store does not expose row queries")
	}
	return queryer.Query(ctx, query, args...)
}

// BeginTx starts a transaction directly on the underlying database.
// Note: Transactional operations bypass the Hermes hot-cache projection.
func (s *ProjectedRuntimeStore) BeginTx(ctx context.Context) (database.Tx, error) {
	if beginner, ok := s.base.(database.TxBeginner); ok {
		return beginner.BeginTx(ctx)
	}
	return passthroughTx{db: s.base}, nil
}

func (s *ProjectedRuntimeStore) Stats() database.StoreStats {
	return s.base.Stats()
}

func (s *ProjectedRuntimeStore) Close() {
	s.base.Close()
}

func (s *ProjectedRuntimeStore) UpsertRecord(ctx context.Context, rec database.DomainRecord) (database.DomainRecord, error) {
	saved, err := s.base.UpsertRecord(ctx, rec)
	if err != nil {
		return database.DomainRecord{}, err
	}
	s.projectUpsert(ctx, saved)
	return saved, nil
}

// batchRecordUpserter is the optional base-store capability UpsertRecords uses
// to write a whole batch in one pipelined round trip (PostgresDB implements it
// via UpsertRecordsBatch).
type batchRecordUpserter interface {
	UpsertRecordsBatch(ctx context.Context, records []database.DomainRecord) ([]database.DomainRecord, error)
}

// UpsertRecords writes a batch of records through the projected store: one
// pipelined round trip to the base store when it supports batching (falling
// back to per-record upserts), then one hot-partition ApplyRecords per scope
// group. This is the round-trip-amortized shape of UpsertRecord — the
// per-record projection cost drops toward the per-batch boundary cost, which
// is what makes high mutation rates cheap. Semantics per record are identical
// to UpsertRecord (idempotent, LWW-versioned from the same counter, live
// fan-out through the store observer).
func (s *ProjectedRuntimeStore) UpsertRecords(ctx context.Context, records []database.DomainRecord) ([]database.DomainRecord, error) {
	switch len(records) {
	case 0:
		return nil, nil
	case 1:
		saved, err := s.UpsertRecord(ctx, records[0])
		if err != nil {
			return nil, err
		}
		return []database.DomainRecord{saved}, nil
	}

	var saved []database.DomainRecord
	if batcher, ok := s.base.(batchRecordUpserter); ok {
		batched, err := batcher.UpsertRecordsBatch(ctx, records)
		if err != nil {
			return nil, err
		}
		saved = batched
	} else {
		saved = make([]database.DomainRecord, 0, len(records))
		for _, rec := range records {
			one, err := s.base.UpsertRecord(ctx, rec)
			if err != nil {
				return nil, err
			}
			saved = append(saved, one)
		}
	}

	s.projectUpsertBatch(ctx, saved)
	return saved, nil
}

// projectUpsertBatch applies saved records to the hot plane grouped by scope,
// one ApplyRecords call per projection partition. Versions are reserved from
// the same counter as single-record projectUpsert, so LWW and the "state"
// watermark dedup stay coherent across both paths.
func (s *ProjectedRuntimeStore) projectUpsertBatch(ctx context.Context, records []database.DomainRecord) {
	groups := make(map[string][]database.DomainRecord)
	for _, rec := range records {
		name, err := s.ensureProjection(rec.Domain, rec.Collection, rec.OrganizationID)
		if err != nil {
			continue
		}
		groups[name] = append(groups[name], rec)
	}
	for name, group := range groups {
		count := uint64(len(group))
		baseVersion := s.version.Add(count) - count + 1
		_, err := s.hot.ApplyRecords(ctx, name, "state", baseVersion, group)
		s.rememberProjectionResult(name, err)
	}
}

func (s *ProjectedRuntimeStore) UpsertRecordJSON(ctx context.Context, rec database.RawDomainRecord) (database.RawDomainRecord, error) {
	raw, err := s.rawStore().UpsertRecordJSON(ctx, rec)
	if err != nil {
		return database.RawDomainRecord{}, err
	}
	s.projectRaw(ctx, raw)
	return raw, nil
}

func (s *ProjectedRuntimeStore) GetRecord(ctx context.Context, domain, collection, organizationID, recordID string) (database.DomainRecord, bool, error) {
	if rec, ok, authoritative := s.getHot(ctx, domain, collection, organizationID, recordID); ok || authoritative {
		return rec, ok, nil
	}
	s.recordFallback()
	rec, found, err := s.base.GetRecord(ctx, domain, collection, organizationID, recordID)
	if err != nil || !found {
		return rec, found, err
	}
	s.projectUpsert(ctx, rec)
	return rec, true, nil
}

func (s *ProjectedRuntimeStore) GetRecordJSON(ctx context.Context, domain, collection, organizationID, recordID string) (database.RawDomainRecord, bool, error) {
	return s.rawStore().GetRecordJSON(ctx, domain, collection, organizationID, recordID)
}

func (s *ProjectedRuntimeStore) ListRecords(ctx context.Context, domain, collection, organizationID string, query database.RecordQuery) ([]database.DomainRecord, error) {
	records := make([]database.DomainRecord, 0)
	err := s.ForEachRecord(ctx, domain, collection, organizationID, query, func(rec database.DomainRecord) error {
		records = append(records, rec)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

func (s *ProjectedRuntimeStore) ForEachRecord(ctx context.Context, domain, collection, organizationID string, query database.RecordQuery, fn database.RecordVisitor) error {
	if fn == nil {
		return errors.New("record visitor is required")
	}
	if s.ensureWarm(ctx, domain, collection, organizationID) == nil {
		name := s.ProjectionName(domain, collection, organizationID)
		_, err := s.hot.ForEachView(ctx, name, QueryFromRecordQuery(organizationID, query), Fence{}, func(view RecordView) error {
			return fn(recordFromView(view))
		})
		if err == nil {
			return nil
		}
		s.markDegraded(name)
	}
	s.recordFallback()
	return s.base.ForEachRecord(ctx, domain, collection, organizationID, query, fn)
}

func (s *ProjectedRuntimeStore) CountRecords(ctx context.Context, domain, collection, organizationID string, query database.RecordQuery) (int64, error) {
	if s.ensureWarm(ctx, domain, collection, organizationID) == nil {
		name := s.ProjectionName(domain, collection, organizationID)
		count, err := s.hot.Count(ctx, name, QueryFromRecordQuery(organizationID, query), Fence{})
		if err == nil {
			return count, nil
		}
		s.markDegraded(name)
	}
	s.recordFallback()
	return s.base.CountRecords(ctx, domain, collection, organizationID, query)
}

func (s *ProjectedRuntimeStore) EstimateCount(ctx context.Context, domain, collection, organizationID string) (int64, error) {
	return s.base.EstimateCount(ctx, domain, collection, organizationID)
}

func (s *ProjectedRuntimeStore) DeleteRecord(ctx context.Context, domain, collection, organizationID, recordID string) error {
	if err := s.base.DeleteRecord(ctx, domain, collection, organizationID, recordID); err != nil {
		return err
	}
	s.projectDelete(ctx, domain, collection, organizationID, recordID)
	return nil
}

// WarmScope eagerly rebuilds the hermes hot partition for a scope from the
// underlying database, so the projection gateway (which reads the hot partition
// directly and does not trigger the lazy read-through warm) returns SQL-seeded
// rows instead of "projection not found". Call it at startup for each scope that
// was populated out-of-band (e.g. raw SQL seeds) rather than through the
// projected write path. It is idempotent: an already-warm, non-degraded scope
// is a no-op. Returns ErrProjectionLimit if the scope exceeds MaxRecordsPerScope.
func (s *ProjectedRuntimeStore) WarmScope(ctx context.Context, domain, collection, organizationID string) error {
	if s == nil || s.hot == nil {
		return errors.New("hermes projected runtime store is not initialized")
	}
	return s.ensureWarm(ctx, domain, collection, organizationID)
}

func (s *ProjectedRuntimeStore) ensureWarm(ctx context.Context, domain, collection, organizationID string) error {
	name, err := s.ensureProjection(domain, collection, organizationID)
	if err != nil {
		return err
	}
	if _, ok := s.warm.Load(name); ok && !s.isDegraded(name) {
		return nil
	}
	_, err, _ = s.rebuildSF.Do(name, func() (any, error) {
		if _, ok := s.warm.Load(name); ok && !s.isDegraded(name) {
			return nil, nil
		}
		total, err := s.base.CountRecords(ctx, domain, collection, organizationID, database.RecordQuery{})
		if err != nil {
			s.markDegraded(name)
			return nil, err
		}
		// Self-backfilling warm: an empty mirror with a configured backfiller
		// means the scope's truth lives only in the app's authoritative tables
		// (raw seeds, pre-projection writes). Pull it through the batch lane
		// once, then warm normally — so eager warm scopes work on first boot
		// without a separate backfill step.
		if total == 0 && s.opts.ScopeBackfill != nil {
			if backfilled, err := s.backfillScope(ctx, domain, collection, organizationID); err != nil {
				s.markDegraded(name)
				return nil, err
			} else {
				total = backfilled
			}
		}
		if total > int64(s.opts.MaxRecordsPerScope) {
			return nil, ErrProjectionLimit
		}
		_, err = s.hot.Rebuild(ctx, name, s.base, Query{OrganizationID: organizationID})
		if err != nil {
			s.markDegraded(name)
			return nil, err
		}
		s.warm.Store(name, struct{}{})
		s.markHealthy(name)
		s.shadowSnapshot(ctx, name, organizationID)
		return nil, nil
	})
	return err
}

// shadowSnapshot runs the shadow-mode half of the snapshot rollout after a
// successful source rebuild: diff the newest durable artifact against the
// freshly rebuilt partition (the rebuild is the oracle), record the outcome,
// and refresh the artifact so the next cold process compares against current
// evidence. Strictly best-effort — no error here can degrade the warm that
// already succeeded (FallbackRefinement).
func (s *ProjectedRuntimeStore) shadowSnapshot(ctx context.Context, name, organizationID string) {
	if s.opts.SnapshotStore == nil {
		return
	}
	query := Query{OrganizationID: organizationID}
	report, ok, err := s.hot.ShadowCompareSnapshot(ctx, name, query, s.opts.SnapshotStore)
	switch {
	case err != nil:
		s.shadowErrors.Add(1)
	case ok && report.Match():
		s.shadowMatches.Add(1)
	case ok:
		s.shadowMismatches.Add(1)
	}
	if _, err := s.hot.SaveSnapshot(ctx, name, query, s.opts.SnapshotStore); err == nil {
		s.shadowSaves.Add(1)
	} else {
		s.shadowErrors.Add(1)
	}
}

// backfillScope streams the scope's rows from the app's authoritative tables
// into the projected store in bounded batches (one pipelined round trip +
// grouped hot apply per batch). Returns how many records landed.
func (s *ProjectedRuntimeStore) backfillScope(ctx context.Context, domain, collection, organizationID string) (int64, error) {
	const batchSize = 256
	var total int64
	batch := make([]database.DomainRecord, 0, batchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if _, err := s.UpsertRecords(ctx, batch); err != nil {
			return err
		}
		total += int64(len(batch))
		batch = batch[:0]
		return nil
	}
	err := s.opts.ScopeBackfill(ctx, domain, collection, organizationID, func(rec database.DomainRecord) error {
		batch = append(batch, rec)
		if len(batch) >= batchSize {
			return flush()
		}
		return nil
	})
	if err != nil {
		return total, err
	}
	return total, flush()
}

func (s *ProjectedRuntimeStore) getHot(ctx context.Context, domain, collection, organizationID, recordID string) (database.DomainRecord, bool, bool) {
	name, err := s.ensureProjection(domain, collection, organizationID)
	if err != nil || s.isDegraded(name) {
		return database.DomainRecord{}, false, false
	}
	rec, found, err := s.hot.GetRecord(ctx, name, Query{OrganizationID: organizationID}, recordID, Fence{})
	if err == nil && found {
		return rec, true, true
	}
	if err != nil {
		s.markDegraded(name)
	}
	_, warm := s.warm.Load(name)
	return database.DomainRecord{}, false, warm && err == nil
}

func (s *ProjectedRuntimeStore) projectUpsert(ctx context.Context, rec database.DomainRecord) {
	name, err := s.ensureProjection(rec.Domain, rec.Collection, rec.OrganizationID)
	if err != nil {
		return
	}
	version := s.version.Add(1)
	_, err = s.hot.Apply(ctx, name, Event{
		Operation: OperationUpsert,
		SourceID:  sourceID("upsert", rec, version),
		Version:   version,
		Record:    rec,
	})
	s.rememberProjectionResult(name, err)
}

func (s *ProjectedRuntimeStore) projectRaw(ctx context.Context, raw database.RawDomainRecord) {
	var data database.RecordData
	if err := data.UnmarshalJSON(raw.DataJSON); err != nil {
		return
	}
	s.projectUpsert(ctx, database.DomainRecord{
		Domain:         raw.Domain,
		Collection:     raw.Collection,
		OrganizationID: raw.OrganizationID,
		RecordID:       raw.RecordID,
		Data:           data,
		CreatedAt:      raw.CreatedAt,
		UpdatedAt:      raw.UpdatedAt,
	})
}

func (s *ProjectedRuntimeStore) projectDelete(ctx context.Context, domain, collection, organizationID, recordID string) {
	name, err := s.ensureProjection(domain, collection, organizationID)
	if err != nil {
		return
	}
	version := s.version.Add(1)
	rec := database.DomainRecord{Domain: domain, Collection: collection, OrganizationID: organizationID, RecordID: recordID}
	_, err = s.hot.Apply(ctx, name, Event{
		Operation: OperationDelete,
		SourceID:  sourceID("delete", rec, version),
		Version:   version,
		Record:    rec,
	})
	s.rememberProjectionResult(name, err)
}

func (s *ProjectedRuntimeStore) ensureProjection(domain, collection, organizationID string) (string, error) {
	scope, err := runtimeProjectionScopeFrom(domain, collection, organizationID)
	if err != nil {
		return "", err
	}
	if cached, ok := s.registered.Load(scope); ok {
		return cached.(string), nil
	}
	name := projectionName(s.opts.ProjectionPrefix, scope.domain, scope.collection, scope.organizationID)
	err = s.hot.Register(ProjectionSpec{
		Name:               name,
		Domain:             scope.domain,
		Collection:         scope.collection,
		IndexedFields:      s.opts.IndexedFields,
		RangeIndexedFields: s.opts.RangeIndexedFields,
		MaxRecords:         s.opts.MaxRecordsPerScope,
		MaxBytes:           s.opts.MaxBytesPerScope,
		MaxIndexes:         len(s.opts.IndexedFields),
		MaxRangeIndexes:    len(s.opts.RangeIndexedFields),
		MaxTombstones:      min(s.opts.MaxRecordsPerScope, defaultMaxTombstones),
		MaxAppliedEvents:   min(s.opts.MaxRecordsPerScope*2, defaultMaxAppliedEvents),
	})
	if err != nil {
		return "", err
	}
	actual, _ := s.registered.LoadOrStore(scope, name)
	return actual.(string), nil
}

type runtimeProjectionScope struct {
	domain         string
	collection     string
	organizationID string
}

func runtimeProjectionScopeFrom(domain, collection, organizationID string) (runtimeProjectionScope, error) {
	scope := runtimeProjectionScope{
		domain:         strings.TrimSpace(domain),
		collection:     strings.TrimSpace(collection),
		organizationID: strings.TrimSpace(organizationID),
	}
	if scope.domain == "" || scope.collection == "" || scope.organizationID == "" {
		return runtimeProjectionScope{}, ErrInvalidEvent
	}
	return scope, nil
}

func (s *ProjectedRuntimeStore) rawStore() database.RawStateStore {
	raw, ok := s.base.(database.RawStateStore)
	if !ok {
		return missingRawStateStore{}
	}
	return raw
}

func (s *ProjectedRuntimeStore) rememberProjectionResult(name string, err error) {
	if err != nil {
		if errors.Is(err, ErrProjectionLimit) {
			return
		}
		s.markDegraded(name)
		return
	}
	s.markHealthy(name)
}

func (s *ProjectedRuntimeStore) markDegraded(name string) {
	if _, loaded := s.degraded.LoadOrStore(name, struct{}{}); !loaded {
		s.degradedCount.Add(1)
	}
}

func (s *ProjectedRuntimeStore) markHealthy(name string) {
	if _, loaded := s.degraded.LoadAndDelete(name); loaded {
		s.degradedCount.Add(-1)
	}
}

func (s *ProjectedRuntimeStore) isDegraded(name string) bool {
	_, ok := s.degraded.Load(name)
	return ok
}

func (s *ProjectedRuntimeStore) recordFallback() {
	if s != nil {
		s.fallbackCount.Add(1)
	}
}

func projectionName(prefix, domain, collection, organizationID string) string {
	return strings.TrimSpace(prefix) + ":" +
		strings.TrimSpace(domain) + ":" +
		strings.TrimSpace(collection) + ":" +
		strings.TrimSpace(organizationID)
}

func sourceID(operation string, rec database.DomainRecord, version uint64) string {
	return strings.Join([]string{
		"state",
		operation,
		rec.Domain,
		rec.Collection,
		rec.OrganizationID,
		rec.RecordID,
		strconv.FormatUint(version, 10),
	}, ":")
}

type passthroughTx struct {
	db database.DBTX
}

func (tx passthroughTx) Exec(ctx context.Context, query string, args ...any) error {
	return tx.db.Exec(ctx, query, args...)
}

func (tx passthroughTx) QueryRow(ctx context.Context, query string, args ...any) database.RowScanner {
	return tx.db.QueryRow(ctx, query, args...)
}

func (tx passthroughTx) Query(ctx context.Context, query string, args ...any) (database.Rows, error) {
	return tx.db.Query(ctx, query, args...)
}

func (passthroughTx) Commit(context.Context) error {
	return nil
}

func (passthroughTx) Rollback(context.Context) error {
	return nil
}

type missingRawStateStore struct{}

func (missingRawStateStore) UpsertRecordJSON(context.Context, database.RawDomainRecord) (database.RawDomainRecord, error) {
	return database.RawDomainRecord{}, errors.New("wrapped runtime store does not expose raw state writes")
}

func (missingRawStateStore) GetRecordJSON(context.Context, string, string, string, string) (database.RawDomainRecord, bool, error) {
	return database.RawDomainRecord{}, false, errors.New("wrapped runtime store does not expose raw state reads")
}
