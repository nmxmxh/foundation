package hermes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

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
	MaxRecordsPerScope int
	MaxBytesPerScope   int64
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
		Projections:    s.hot.AllStats(),
		Fallbacks:      s.fallbackCount.Load(),
		DegradedScopes: s.degradedCount.Load(),
	}
}

func (s *ProjectedRuntimeStore) Exec(ctx context.Context, query string, args ...any) error {
	return s.base.Exec(ctx, query, args...)
}

func (s *ProjectedRuntimeStore) ExecResult(ctx context.Context, query string, args ...any) (database.CommandResult, error) {
	executor, ok := s.base.(database.ResultExecutor)
	if !ok {
		return nil, errors.New("wrapped runtime store does not expose command results")
	}
	return executor.ExecResult(ctx, query, args...)
}

func (s *ProjectedRuntimeStore) QueryRow(ctx context.Context, query string, args ...any) database.RowScanner {
	return s.base.QueryRow(ctx, query, args...)
}

func (s *ProjectedRuntimeStore) Query(ctx context.Context, query string, args ...any) (database.Rows, error) {
	queryer, ok := s.base.(database.RowQueryer)
	if !ok {
		return nil, errors.New("wrapped runtime store does not expose row queries")
	}
	return queryer.Query(ctx, query, args...)
}

func (s *ProjectedRuntimeStore) QueryMaps(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	return s.base.QueryMaps(ctx, query, args...)
}

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

func (s *ProjectedRuntimeStore) ListRecords(ctx context.Context, domain, collection, organizationID string, filters map[string]any, limit int) ([]database.DomainRecord, error) {
	if s.ensureWarm(ctx, domain, collection, organizationID) == nil {
		name := s.ProjectionName(domain, collection, organizationID)
		records, err := s.hot.ListRecords(ctx, name, Query{OrganizationID: organizationID, Filters: filters, Limit: limit}, Fence{})
		if err == nil {
			return records, nil
		}
		s.markDegraded(name)
	}
	s.recordFallback()
	return s.base.ListRecords(ctx, domain, collection, organizationID, filters, limit)
}

func (s *ProjectedRuntimeStore) CountRecords(ctx context.Context, domain, collection, organizationID string, filters map[string]any) (int64, error) {
	if s.ensureWarm(ctx, domain, collection, organizationID) == nil {
		name := s.ProjectionName(domain, collection, organizationID)
		count, err := s.hot.Count(ctx, name, Query{OrganizationID: organizationID, Filters: filters}, Fence{})
		if err == nil {
			return count, nil
		}
		s.markDegraded(name)
	}
	s.recordFallback()
	return s.base.CountRecords(ctx, domain, collection, organizationID, filters)
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

func (s *ProjectedRuntimeStore) ensureWarm(ctx context.Context, domain, collection, organizationID string) error {
	name, err := s.ensureProjection(domain, collection, organizationID)
	if err != nil {
		return err
	}
	if _, ok := s.warm.Load(name); ok && !s.isDegraded(name) {
		return nil
	}
	total, err := s.base.CountRecords(ctx, domain, collection, organizationID, nil)
	if err != nil {
		s.markDegraded(name)
		return err
	}
	if total > int64(s.opts.MaxRecordsPerScope) {
		return ErrProjectionLimit
	}
	_, err = s.hot.Rebuild(ctx, name, s.base, Query{OrganizationID: organizationID})
	if err != nil {
		s.markDegraded(name)
		return err
	}
	s.warm.Store(name, struct{}{})
	s.markHealthy(name)
	return nil
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
	data := map[string]any{}
	if err := json.Unmarshal(raw.DataJSON, &data); err != nil {
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
		Name:             name,
		Domain:           scope.domain,
		Collection:       scope.collection,
		IndexedFields:    s.opts.IndexedFields,
		MaxRecords:       s.opts.MaxRecordsPerScope,
		MaxBytes:         s.opts.MaxBytesPerScope,
		MaxIndexes:       len(s.opts.IndexedFields),
		MaxTombstones:    min(s.opts.MaxRecordsPerScope, defaultMaxTombstones),
		MaxAppliedEvents: min(s.opts.MaxRecordsPerScope*2, defaultMaxAppliedEvents),
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

func (tx passthroughTx) QueryMaps(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	return tx.db.QueryMaps(ctx, query, args...)
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
