package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrPoolAcquireTimeout = errors.New("database pool acquire timeout")
var ErrQueryLimitReached = errors.New("database query row limit reached")
var ErrUnsupportedFilterShape = errors.New("database filter shape cannot be pushed down")

// MemoryDB is a concurrency-safe in-memory database adapter.
// It provides deterministic persistence semantics for services and tests,
// while keeping a stable StateStore contract for future SQL-backed adapters.
type MemoryDB struct {
	mu      sync.RWMutex
	closed  bool
	records map[string]DomainRecord
	byScope map[recordScope]map[string]struct{}
	byOrg   map[string]map[string]struct{}
	byField map[fieldIndex]map[string]struct{}

	nextVersion  uint64
	versions     map[string]uint64
	byScopeOrder map[recordScope][]recordOrderEntry
	byFieldOrder map[fieldIndex][]recordOrderEntry
}

type recordScope struct {
	domain         string
	collection     string
	organizationID string
}

type fieldIndex struct {
	scope recordScope
	field string
	kind  byte
	value string
}

type recordOrderEntry struct {
	key     string
	version uint64
}

var emptyRecordKeys = map[string]struct{}{}

const (
	DriverMemory   = "memory"
	DriverPostgres = "postgres"
)

// PoolOptions controls pgxpool sizing and timeout behavior.
type PoolOptions struct {
	MaxConns                 int
	MinConns                 int
	HealthCheckPeriod        time.Duration
	ConnectTimeout           time.Duration
	QueryTimeout             time.Duration
	AcquireTimeout           time.Duration
	StatementCacheCapacity   int
	DescriptionCacheCapacity int
}

func DefaultPoolOptions() PoolOptions {
	return DefaultPoolOptionsFor(RuntimeLaneDefault)
}

type RuntimeLane string

const (
	RuntimeLaneDefault    RuntimeLane = "default"
	RuntimeLaneHotRead    RuntimeLane = "hot_read"
	RuntimeLaneHotWrite   RuntimeLane = "hot_write"
	RuntimeLaneBackground RuntimeLane = "background"
	RuntimeLaneAnalytics  RuntimeLane = "analytics"
)

func DefaultPoolOptionsFor(lane RuntimeLane) PoolOptions {
	cpus := runtime.GOMAXPROCS(0)
	maxConns := min(max(cpus*2, 8), 64)
	minConns := max(maxConns/4, 2)
	queryTimeout := 250 * time.Millisecond
	switch lane {
	case RuntimeLaneHotRead:
		maxConns = clampInt(maxConns, 8, 48)
		queryTimeout = 50 * time.Millisecond
	case RuntimeLaneHotWrite:
		maxConns = clampInt(maxConns, 8, 32)
		queryTimeout = 150 * time.Millisecond
	case RuntimeLaneBackground:
		maxConns = clampInt(cpus, 4, 24)
		minConns = min(minConns, maxConns)
		queryTimeout = 2 * time.Second
	case RuntimeLaneAnalytics:
		maxConns = clampInt(cpus/2, 2, 12)
		minConns = min(minConns, maxConns)
		queryTimeout = 5 * time.Second
	}
	return PoolOptions{
		MaxConns:                 maxConns,
		MinConns:                 minConns,
		HealthCheckPeriod:        30 * time.Second,
		ConnectTimeout:           10 * time.Second,
		QueryTimeout:             queryTimeout,
		AcquireTimeout:           100 * time.Millisecond,
		StatementCacheCapacity:   512,
		DescriptionCacheCapacity: 128,
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
	if opts.QueryTimeout <= 0 {
		opts.QueryTimeout = defaults.QueryTimeout
	}
	if opts.AcquireTimeout <= 0 {
		opts.AcquireTimeout = defaults.AcquireTimeout
	}
	if opts.StatementCacheCapacity <= 0 {
		opts.StatementCacheCapacity = defaults.StatementCacheCapacity
	}
	if opts.DescriptionCacheCapacity < 0 {
		opts.DescriptionCacheCapacity = defaults.DescriptionCacheCapacity
	}
	return opts
}

func QueryBudgetContext(ctx context.Context, opts PoolOptions) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	opts = normalizePoolOptions(opts)
	return context.WithTimeout(ctx, opts.QueryTimeout)
}

func AcquireBudgetContext(ctx context.Context, opts PoolOptions) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	opts = normalizePoolOptions(opts)
	return context.WithTimeout(ctx, opts.AcquireTimeout)
}

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
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
	return &MemoryDB{
		records:      map[string]DomainRecord{},
		byScope:      map[recordScope]map[string]struct{}{},
		byOrg:        map[string]map[string]struct{}{},
		byField:      map[fieldIndex]map[string]struct{}{},
		versions:     map[string]uint64{},
		byScopeOrder: map[recordScope][]recordOrderEntry{},
		byFieldOrder: map[fieldIndex][]recordOrderEntry{},
	}
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

func (db *MemoryDB) ExecResult(ctx context.Context, _ string, _ ...any) (CommandResult, error) {
	if err := db.ensureReady(ctx); err != nil {
		return nil, err
	}
	return commandResult{rowsAffected: 0}, nil
}

func (db *MemoryDB) QueryRow(ctx context.Context, _ string, _ ...any) RowScanner {
	if err := db.ensureReady(ctx); err != nil {
		return memoryRow{err: err}
	}
	return memoryRow{err: errors.New("no rows in memory database")}
}

func (db *MemoryDB) Query(ctx context.Context, _ string, _ ...any) (Rows, error) {
	if err := db.ensureReady(ctx); err != nil {
		return nil, err
	}
	return memoryRows{}, nil
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
		db.removeRecordIndexesLocked(key, existing)
	} else {
		rec.CreatedAt = now
	}
	rec.UpdatedAt = now
	db.nextVersion++
	db.versions[key] = db.nextVersion
	db.records[key] = rec
	db.addRecordIndexesLocked(key, rec)
	return copyRecord(rec), nil
}

func (db *MemoryDB) UpsertRecordJSON(ctx context.Context, rec RawDomainRecord) (RawDomainRecord, error) {
	if err := db.ensureReady(ctx); err != nil {
		return RawDomainRecord{}, err
	}
	payload, err := validateRawDomainRecord(&rec)
	if err != nil {
		return RawDomainRecord{}, err
	}
	data, err := parseDataJSON(payload)
	if err != nil {
		return RawDomainRecord{}, err
	}
	if rec.OrganizationID != "" {
		data["organization_id"] = rec.OrganizationID
	}
	typed, err := db.UpsertRecord(ctx, DomainRecord{
		Domain:         rec.Domain,
		Collection:     rec.Collection,
		OrganizationID: rec.OrganizationID,
		RecordID:       rec.RecordID,
		Data:           data,
	})
	if err != nil {
		return RawDomainRecord{}, err
	}
	rec.DataJSON = payload
	rec.CreatedAt = typed.CreatedAt
	rec.UpdatedAt = typed.UpdatedAt
	return rec, nil
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

func (db *MemoryDB) GetRecordJSON(ctx context.Context, domain, collection, organizationID, recordID string) (RawDomainRecord, bool, error) {
	rec, found, err := db.GetRecord(ctx, domain, collection, organizationID, recordID)
	if err != nil || !found {
		return RawDomainRecord{}, found, err
	}
	payload, err := json.Marshal(rec.Data)
	if err != nil {
		return RawDomainRecord{}, false, err
	}
	return RawDomainRecord{
		Domain:         rec.Domain,
		Collection:     rec.Collection,
		OrganizationID: rec.OrganizationID,
		RecordID:       rec.RecordID,
		DataJSON:       payload,
		CreatedAt:      rec.CreatedAt,
		UpdatedAt:      rec.UpdatedAt,
	}, true, nil
}

func (db *MemoryDB) ListRecords(ctx context.Context, domain, collection, organizationID string, filters map[string]any, limit int) ([]DomainRecord, error) {
	if err := db.ensureReady(ctx); err != nil {
		return nil, err
	}
	domain = strings.TrimSpace(domain)
	collection = strings.TrimSpace(collection)
	organizationID = strings.TrimSpace(organizationID)

	db.mu.RLock()
	ordered := db.orderedCandidatesLocked(domain, collection, organizationID, filters)
	keys := db.candidateKeysLocked(domain, collection, organizationID, filters)
	candidateCap := len(db.records)
	if limit > 0 && len(ordered) > 0 {
		candidateCap = limit
	} else if keys != nil {
		candidateCap = len(keys)
	}
	if limit > 0 && candidateCap > limit {
		candidateCap = limit
	}
	candidates := make([]DomainRecord, 0, candidateCap)
	if limit > 0 && len(ordered) > 0 {
		for i := len(ordered) - 1; i >= 0 && len(candidates) < limit; i-- {
			rec, ok := db.recordForOrderEntryLocked(ordered[i])
			if !ok {
				continue
			}
			if err := ctxErr(ctx); err != nil {
				db.mu.RUnlock()
				return nil, err
			}
			if recordMatches(rec, domain, collection, organizationID, filters) {
				candidates = append(candidates, rec)
			}
		}
	} else if keys != nil {
		for key := range keys {
			rec, ok := db.records[key]
			if !ok {
				continue
			}
			if err := ctxErr(ctx); err != nil {
				db.mu.RUnlock()
				return nil, err
			}
			if recordMatches(rec, domain, collection, organizationID, filters) {
				candidates = appendListCandidate(candidates, rec, limit)
			}
		}
	} else {
		for _, rec := range db.records {
			if err := ctxErr(ctx); err != nil {
				db.mu.RUnlock()
				return nil, err
			}
			if recordMatches(rec, domain, collection, organizationID, filters) {
				candidates = appendListCandidate(candidates, rec, limit)
			}
		}
	}
	db.mu.RUnlock()

	if limit <= 0 || len(candidates) > 1 {
		sort.Slice(candidates, func(i, j int) bool {
			return recordBefore(candidates[i], candidates[j])
		})
	}
	out := make([]DomainRecord, len(candidates))
	for i, rec := range candidates {
		out[i] = copyRecord(rec)
	}
	return out, nil
}

func appendListCandidate(candidates []DomainRecord, rec DomainRecord, limit int) []DomainRecord {
	if limit <= 0 {
		return append(candidates, rec)
	}
	insertAt := sort.Search(len(candidates), func(i int) bool {
		return recordBefore(rec, candidates[i])
	})
	if insertAt >= limit {
		return candidates
	}
	if len(candidates) < limit {
		candidates = append(candidates, DomainRecord{})
		copy(candidates[insertAt+1:], candidates[insertAt:])
		candidates[insertAt] = rec
		return candidates
	}
	copy(candidates[insertAt+1:], candidates[insertAt:len(candidates)-1])
	candidates[insertAt] = rec
	return candidates
}

func recordBefore(left, right DomainRecord) bool {
	if left.UpdatedAt.Equal(right.UpdatedAt) {
		return left.RecordID < right.RecordID
	}
	return left.UpdatedAt.After(right.UpdatedAt)
}

func (db *MemoryDB) CountRecords(ctx context.Context, domain, collection, organizationID string, filters map[string]any) (int64, error) {
	if err := db.ensureReady(ctx); err != nil {
		return 0, err
	}
	domain = strings.TrimSpace(domain)
	collection = strings.TrimSpace(collection)
	organizationID = strings.TrimSpace(organizationID)

	db.mu.RLock()
	defer db.mu.RUnlock()
	var count int64
	keys := db.candidateKeysLocked(domain, collection, organizationID, filters)
	if keys != nil {
		for key := range keys {
			rec, ok := db.records[key]
			if !ok {
				continue
			}
			if err := ctxErr(ctx); err != nil {
				return 0, err
			}
			if recordMatches(rec, domain, collection, organizationID, filters) {
				count++
			}
		}
		return count, nil
	}
	for _, rec := range db.records {
		if err := ctxErr(ctx); err != nil {
			return 0, err
		}
		if recordMatches(rec, domain, collection, organizationID, filters) {
			count++
		}
	}
	return count, nil
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
	if rec, ok := db.records[key]; ok {
		db.removeRecordIndexesLocked(key, rec)
	}
	delete(db.records, key)
	delete(db.versions, key)
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
	keys := db.byOrg[organizationID]
	for key := range keys {
		rec, ok := db.records[key]
		if !ok {
			continue
		}
		delete(db.records, key)
		delete(db.versions, key)
		db.removeRecordIndexesLocked(key, rec)
		removed++
	}
	delete(db.byOrg, organizationID)
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

type memoryRows struct{}

func (memoryRows) Close() {}

func (memoryRows) Next() bool { return false }

func (memoryRows) Scan(_ ...any) error {
	return errors.New("no rows in memory database")
}

func (memoryRows) Err() error { return nil }

type commandResult struct {
	rowsAffected int64
}

func (r commandResult) RowsAffected() int64 {
	return r.rowsAffected
}

func recordKey(domain, collection, organizationID, recordID string) string {
	return strings.TrimSpace(domain) + "|" + strings.TrimSpace(collection) + "|" + strings.TrimSpace(organizationID) + "|" + strings.TrimSpace(recordID)
}

func scopeKey(domain, collection, organizationID string) recordScope {
	return recordScope{
		domain:         strings.TrimSpace(domain),
		collection:     strings.TrimSpace(collection),
		organizationID: strings.TrimSpace(organizationID),
	}
}

func (db *MemoryDB) addRecordIndexesLocked(key string, rec DomainRecord) {
	scope := scopeKey(rec.Domain, rec.Collection, rec.OrganizationID)
	if db.byScope[scope] == nil {
		db.byScope[scope] = map[string]struct{}{}
	}
	db.byScope[scope][key] = struct{}{}
	entry := recordOrderEntry{key: key, version: db.versions[key]}
	db.byScopeOrder[scope] = append(db.byScopeOrder[scope], entry)
	if rec.OrganizationID != "" {
		if db.byOrg[rec.OrganizationID] == nil {
			db.byOrg[rec.OrganizationID] = map[string]struct{}{}
		}
		db.byOrg[rec.OrganizationID][key] = struct{}{}
	}
	forEachRecordFieldIndex(rec, func(field string, kind byte, value string) {
		index := fieldIndex{scope: scope, field: field, kind: kind, value: value}
		if db.byField[index] == nil {
			db.byField[index] = map[string]struct{}{}
		}
		db.byField[index][key] = struct{}{}
		db.byFieldOrder[index] = append(db.byFieldOrder[index], entry)
	})
}

func (db *MemoryDB) removeRecordIndexesLocked(key string, rec DomainRecord) {
	scope := scopeKey(rec.Domain, rec.Collection, rec.OrganizationID)
	delete(db.byScope[scope], key)
	if len(db.byScope[scope]) == 0 {
		delete(db.byScope, scope)
	}
	if rec.OrganizationID != "" {
		delete(db.byOrg[rec.OrganizationID], key)
		if len(db.byOrg[rec.OrganizationID]) == 0 {
			delete(db.byOrg, rec.OrganizationID)
		}
	}
	forEachRecordFieldIndex(rec, func(field string, kind byte, value string) {
		index := fieldIndex{scope: scope, field: field, kind: kind, value: value}
		delete(db.byField[index], key)
		if len(db.byField[index]) == 0 {
			delete(db.byField, index)
		}
	})
}

func (db *MemoryDB) orderedCandidatesLocked(domain, collection, organizationID string, filters map[string]any) []recordOrderEntry {
	if domain == "" || collection == "" || organizationID == "" {
		return nil
	}
	scope := scopeKey(domain, collection, organizationID)
	var selected []recordOrderEntry
	selectedCount := 0
	consider := func(entries []recordOrderEntry, liveCount int) {
		if liveCount <= 0 {
			return
		}
		if selected == nil || liveCount < selectedCount {
			selected = entries
			selectedCount = liveCount
		}
	}
	consider(db.byScopeOrder[scope], len(db.byScope[scope]))
	for field, expected := range filters {
		kind, value, ok := indexableFieldValue(expected)
		if !ok {
			continue
		}
		index := fieldIndex{scope: scope, field: field, kind: kind, value: value}
		consider(db.byFieldOrder[index], len(db.byField[index]))
	}
	return selected
}

func (db *MemoryDB) recordForOrderEntryLocked(entry recordOrderEntry) (DomainRecord, bool) {
	if db.versions[entry.key] != entry.version {
		return DomainRecord{}, false
	}
	rec, ok := db.records[entry.key]
	return rec, ok
}

func (db *MemoryDB) candidateKeysLocked(domain, collection, organizationID string, filters map[string]any) map[string]struct{} {
	var selected map[string]struct{}
	haveCandidate := false
	consider := func(keys map[string]struct{}) {
		haveCandidate = true
		if selected == nil || len(keys) < len(selected) {
			selected = keys
		}
	}

	if domain != "" && collection != "" && organizationID != "" {
		scope := scopeKey(domain, collection, organizationID)
		consider(db.byScope[scope])
		for field, expected := range filters {
			kind, value, ok := indexableFieldValue(expected)
			if !ok {
				continue
			}
			consider(db.byField[fieldIndex{scope: scope, field: field, kind: kind, value: value}])
		}
		if selected == nil {
			return emptyRecordKeys
		}
		return selected
	}
	if organizationID != "" {
		consider(db.byOrg[organizationID])
	}
	if haveCandidate {
		if selected == nil {
			return emptyRecordKeys
		}
		return selected
	}
	return nil
}

func forEachRecordFieldIndex(rec DomainRecord, fn func(field string, kind byte, value string)) {
	for field, value := range rec.Data {
		if field == "organization_id" {
			continue
		}
		kind, indexValue, ok := indexableFieldValue(value)
		if !ok {
			continue
		}
		fn(field, kind, indexValue)
	}
}

func indexableFieldValue(value any) (byte, string, bool) {
	switch typed := value.(type) {
	case string:
		return 's', typed, true
	case bool:
		if typed {
			return 'b', "1", true
		}
		return 'b', "0", true
	case int:
		return 'i', strconv.Itoa(typed), true
	case int8:
		return 'i', strconv.FormatInt(int64(typed), 10), true
	case int16:
		return 'i', strconv.FormatInt(int64(typed), 10), true
	case int32:
		return 'i', strconv.FormatInt(int64(typed), 10), true
	case int64:
		return 'i', strconv.FormatInt(typed, 10), true
	case uint:
		return 'u', strconv.FormatUint(uint64(typed), 10), true
	case uint8:
		return 'u', strconv.FormatUint(uint64(typed), 10), true
	case uint16:
		return 'u', strconv.FormatUint(uint64(typed), 10), true
	case uint32:
		return 'u', strconv.FormatUint(uint64(typed), 10), true
	case uint64:
		return 'u', strconv.FormatUint(typed, 10), true
	default:
		return 0, "", false
	}
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func recordMatches(rec DomainRecord, domain, collection, organizationID string, filters map[string]any) bool {
	if domain != "" && rec.Domain != domain {
		return false
	}
	if collection != "" && rec.Collection != collection {
		return false
	}
	if organizationID != "" && rec.OrganizationID != organizationID {
		return false
	}
	return matchesFilter(rec.Data, filters)
}

func copyRecord(in DomainRecord) DomainRecord {
	out := in
	out.Data = copyMap(in.Data)
	return out
}

func copyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	maps.Copy(out, in)
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
	switch a := actual.(type) {
	case string:
		if e, ok := expected.(string); ok {
			return strings.TrimSpace(a) == strings.TrimSpace(e)
		}
	case bool:
		if e, ok := expected.(bool); ok {
			return a == e
		}
	case int:
		if e, ok := expected.(int); ok {
			return a == e
		}
	case int8:
		if e, ok := expected.(int8); ok {
			return a == e
		}
	case int16:
		if e, ok := expected.(int16); ok {
			return a == e
		}
	case int32:
		if e, ok := expected.(int32); ok {
			return a == e
		}
	case int64:
		if e, ok := expected.(int64); ok {
			return a == e
		}
	case uint:
		if e, ok := expected.(uint); ok {
			return a == e
		}
	case uint8:
		if e, ok := expected.(uint8); ok {
			return a == e
		}
	case uint16:
		if e, ok := expected.(uint16); ok {
			return a == e
		}
	case uint32:
		if e, ok := expected.(uint32); ok {
			return a == e
		}
	case uint64:
		if e, ok := expected.(uint64); ok {
			return a == e
		}
	}
	as, aok := comparableString(actual)
	es, eok := comparableString(expected)
	if aok && eok {
		return as == es
	}
	return strings.TrimSpace(fmt.Sprintf("%v", actual)) == strings.TrimSpace(fmt.Sprintf("%v", expected))
}

func comparableString(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v), true
	case int:
		return strconv.Itoa(v), true
	case int8:
		return strconv.FormatInt(int64(v), 10), true
	case int16:
		return strconv.FormatInt(int64(v), 10), true
	case int32:
		return strconv.FormatInt(int64(v), 10), true
	case int64:
		return strconv.FormatInt(v, 10), true
	case uint:
		return strconv.FormatUint(uint64(v), 10), true
	case uint8:
		return strconv.FormatUint(uint64(v), 10), true
	case uint16:
		return strconv.FormatUint(uint64(v), 10), true
	case uint32:
		return strconv.FormatUint(uint64(v), 10), true
	case uint64:
		return strconv.FormatUint(v, 10), true
	case bool:
		return strconv.FormatBool(v), true
	default:
		return "", false
	}
}
