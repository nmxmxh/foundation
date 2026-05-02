package database

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryDB is a concurrency-safe in-memory database adapter.
// It provides deterministic persistence semantics for services and tests,
// while keeping a stable StateStore contract for future SQL-backed adapters.
type MemoryDB struct {
	mu      sync.RWMutex
	closed  bool
	records map[string]DomainRecord
}

const (
	DriverMemory   = "memory"
	DriverPostgres = "postgres"
)

// PoolOptions controls pgxpool sizing and timeout behavior.
type PoolOptions struct {
	MaxConns          int
	MinConns          int
	HealthCheckPeriod time.Duration
	ConnectTimeout    time.Duration
}

func DefaultPoolOptions() PoolOptions {
	return PoolOptions{
		MaxConns:          20,
		MinConns:          2,
		HealthCheckPeriod: 30 * time.Second,
		ConnectTimeout:    10 * time.Second,
	}
}

func normalizePoolOptions(opts PoolOptions) PoolOptions {
	defaults := DefaultPoolOptions()
	if opts.MaxConns <= 0 {
		opts.MaxConns = defaults.MaxConns
	}
	if opts.MinConns < 0 {
		opts.MinConns = defaults.MinConns
	}
	if opts.MinConns > opts.MaxConns {
		opts.MinConns = opts.MaxConns
	}
	if opts.HealthCheckPeriod <= 0 {
		opts.HealthCheckPeriod = defaults.HealthCheckPeriod
	}
	if opts.ConnectTimeout <= 0 {
		opts.ConnectTimeout = defaults.ConnectTimeout
	}
	return opts
}

func Connect(ctx context.Context, databaseURL, driver string, options ...PoolOptions) (RuntimeStore, error) {
	poolOptions := DefaultPoolOptions()
	if len(options) > 0 {
		poolOptions = normalizePoolOptions(options[0])
	}

	switch normalizeDriver(driver) {
	case DriverPostgres:
		return newPostgresDB(ctx, databaseURL, poolOptions)
	default:
		return NewMemoryDB(), nil
	}
}

func normalizeDriver(driver string) string {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case DriverPostgres:
		return DriverPostgres
	default:
		return DriverMemory
	}
}

func NewMemoryDB() *MemoryDB {
	return &MemoryDB{records: map[string]DomainRecord{}}
}

func (db *MemoryDB) Close() {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.closed = true
}

func (db *MemoryDB) Exec(ctx context.Context, _ string, _ ...any) error {
	if err := db.ensureReady(ctx); err != nil {
		return err
	}
	return nil
}

func (db *MemoryDB) QueryRow(ctx context.Context, _ string, _ ...any) RowScanner {
	if err := db.ensureReady(ctx); err != nil {
		return memoryRow{err: err}
	}
	return memoryRow{err: errors.New("no rows in memory database")}
}

func (db *MemoryDB) QueryMaps(ctx context.Context, _ string, _ ...any) ([]map[string]any, error) {
	if err := db.ensureReady(ctx); err != nil {
		return nil, err
	}
	return nil, nil // MemoryDB doesn't support generic SQL queries
}

func (db *MemoryDB) Stats() StoreStats {
	return StoreStats{
		TotalConns:    1,
		IdleConns:     0,
		ActiveConns:   1,
		ConstructedAt: time.Now(),
	}
}

func (db *MemoryDB) UpsertRecord(ctx context.Context, rec DomainRecord) (DomainRecord, error) {
	if err := db.ensureReady(ctx); err != nil {
		return DomainRecord{}, err
	}
	if strings.TrimSpace(rec.Domain) == "" {
		return DomainRecord{}, errors.New("domain is required")
	}
	if strings.TrimSpace(rec.Collection) == "" {
		return DomainRecord{}, errors.New("collection is required")
	}
	if strings.TrimSpace(rec.RecordID) == "" {
		return DomainRecord{}, errors.New("record id is required")
	}

	now := time.Now().UTC()
	rec.Domain = strings.TrimSpace(rec.Domain)
	rec.Collection = strings.TrimSpace(rec.Collection)
	rec.OrganizationID = strings.TrimSpace(rec.OrganizationID)
	rec.RecordID = strings.TrimSpace(rec.RecordID)
	rec.Data = copyMap(rec.Data)
	if rec.Data == nil {
		rec.Data = map[string]any{}
	}
	if rec.OrganizationID != "" {
		rec.Data["organization_id"] = rec.OrganizationID
	}

	key := recordKey(rec.Domain, rec.Collection, rec.OrganizationID, rec.RecordID)

	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return DomainRecord{}, errors.New("database is closed")
	}
	if existing, ok := db.records[key]; ok {
		rec.CreatedAt = existing.CreatedAt
	} else {
		rec.CreatedAt = now
	}
	rec.UpdatedAt = now
	db.records[key] = rec
	return copyRecord(rec), nil
}

func (db *MemoryDB) GetRecord(ctx context.Context, domain, collection, organizationID, recordID string) (DomainRecord, bool, error) {
	if err := db.ensureReady(ctx); err != nil {
		return DomainRecord{}, false, err
	}
	key := recordKey(domain, collection, organizationID, recordID)

	db.mu.RLock()
	rec, ok := db.records[key]
	db.mu.RUnlock()
	if !ok {
		return DomainRecord{}, false, nil
	}
	return copyRecord(rec), true, nil
}

func (db *MemoryDB) ListRecords(ctx context.Context, domain, collection, organizationID string, filters map[string]any, limit int) ([]DomainRecord, error) {
	if err := db.ensureReady(ctx); err != nil {
		return nil, err
	}
	filters = copyMap(filters)
	domain = strings.TrimSpace(domain)
	collection = strings.TrimSpace(collection)
	organizationID = strings.TrimSpace(organizationID)

	db.mu.RLock()
	candidates := make([]DomainRecord, 0, len(db.records))
	for _, rec := range db.records {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				db.mu.RUnlock()
				return nil, err
			}
		}
		if domain != "" && rec.Domain != domain {
			continue
		}
		if collection != "" && rec.Collection != collection {
			continue
		}
		if organizationID != "" && rec.OrganizationID != organizationID {
			continue
		}
		if !matchesFilter(rec.Data, filters) {
			continue
		}
		candidates = append(candidates, copyRecord(rec))
	}
	db.mu.RUnlock()

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].UpdatedAt.Equal(candidates[j].UpdatedAt) {
			return candidates[i].RecordID < candidates[j].RecordID
		}
		return candidates[i].UpdatedAt.After(candidates[j].UpdatedAt)
	})

	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, nil
}

func (db *MemoryDB) CountRecords(ctx context.Context, domain, collection, organizationID string, filters map[string]any) (int64, error) {
	items, err := db.ListRecords(ctx, domain, collection, organizationID, filters, 0)
	if err != nil {
		return 0, err
	}
	return int64(len(items)), nil
}

func (db *MemoryDB) EstimateCount(ctx context.Context, domain, collection, organizationID string) (int64, error) {
	return db.CountRecords(ctx, domain, collection, organizationID, nil)
}

// DeleteRecord removes a single domain record when present.
func (db *MemoryDB) DeleteRecord(ctx context.Context, domain, collection, organizationID, recordID string) error {
	if err := db.ensureReady(ctx); err != nil {
		return err
	}
	key := recordKey(domain, collection, organizationID, recordID)
	db.mu.Lock()
	defer db.mu.Unlock()
	delete(db.records, key)
	return nil
}

// DeleteRecordsByOrganization removes every record for a specific organization.
func (db *MemoryDB) DeleteRecordsByOrganization(ctx context.Context, organizationID string) (int64, error) {
	if err := db.ensureReady(ctx); err != nil {
		return 0, err
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return 0, nil
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	var removed int64
	for key, rec := range db.records {
		if rec.OrganizationID != organizationID {
			continue
		}
		delete(db.records, key)
		removed++
	}
	return removed, nil
}

func Atomic(ctx context.Context, db DBTX, fn func(DBTX) error) error {
	if fn == nil {
		return errors.New("transaction function is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	beginner, ok := db.(TxBeginner)
	if !ok {
		return fn(db)
	}

	tx, err := beginner.BeginTx(ctx)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

func (db *MemoryDB) ensureReady(ctx context.Context) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	db.mu.RLock()
	closed := db.closed
	db.mu.RUnlock()
	if closed {
		return errors.New("database is closed")
	}
	return nil
}

type memoryRow struct {
	err error
}

func (r memoryRow) Scan(_ ...any) error {
	if r.err != nil {
		return r.err
	}
	return errors.New("no rows in memory database")
}

func recordKey(domain, collection, organizationID, recordID string) string {
	return strings.TrimSpace(domain) + "|" + strings.TrimSpace(collection) + "|" + strings.TrimSpace(organizationID) + "|" + strings.TrimSpace(recordID)
}

func copyRecord(in DomainRecord) DomainRecord {
	out := in
	out.Data = copyMap(in.Data)
	return out
}

func copyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func matchesFilter(record map[string]any, filters map[string]any) bool {
	if len(filters) == 0 {
		return true
	}
	for k, expected := range filters {
		actual, ok := record[k]
		if !ok {
			return false
		}
		if !equalValue(actual, expected) {
			return false
		}
	}
	return true
}

func equalValue(actual any, expected any) bool {
	as := strings.TrimSpace(fmt.Sprintf("%v", actual))
	es := strings.TrimSpace(fmt.Sprintf("%v", expected))
	return as == es
}
