package database

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/cache"
)

// CachedStateStoreOptions tunes the cache-aside behavior of CachedStateStore.
type CachedStateStoreOptions struct {
	// TTL bounds how long a positive entry may serve reads. It is the
	// staleness contract upper bound for out-of-order writes. Default: 1m.
	TTL time.Duration
	// NegativeTTL bounds how long a not-found result stays cached.
	// Default: 10s.
	NegativeTTL time.Duration
	// CacheTimeout bounds each cache-layer operation so a slow Redis cannot
	// extend the read path. Default: 100ms.
	CacheTimeout time.Duration
	// KeyPrefix namespaces record keys inside the backend. Default: "state".
	KeyPrefix string
	// OnFallback fires whenever a cache-layer error forces passthrough to the
	// inner store. Wire metrics or alerts here; the read itself still succeeds.
	OnFallback func(op string, err error)
}

func (o CachedStateStoreOptions) withDefaults() CachedStateStoreOptions {
	if o.TTL <= 0 {
		o.TTL = time.Minute
	}
	if o.NegativeTTL <= 0 {
		o.NegativeTTL = 10 * time.Second
	}
	if o.CacheTimeout <= 0 {
		o.CacheTimeout = 100 * time.Millisecond
	}
	if strings.TrimSpace(o.KeyPrefix) == "" {
		o.KeyPrefix = "state"
	}
	return o
}

// cachedRecordEnvelope is the opaque wire shape for one cached point read.
// DataJSON holds the canonical RecordData bytes exactly as Postgres stores
// them, so cached entries never re-round-trip dynamic JSON structures.
type cachedRecordEnvelope struct {
	Version   int64     `json:"v"`
	Found     bool      `json:"found"`
	DataJSON  []byte    `json:"data,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// CachedStateStore adds a shared, stampede-safe cache-aside layer in front of
// any StateStore (for example PostgresDB). Only point reads and their write
// paths are intercepted; list scans pass through unchanged.
//
// Durability invariant: the inner store remains the durable truth. Every
// cache-layer failure degrades to inner-store reads through OnFallback, never
// to a failed request. Staleness from concurrent writers is bounded by TTL;
// version-guarded writes shrink that window for out-of-order commits.
type CachedStateStore struct {
	StateStore

	inner StateStore
	cache *cache.Cache
	opts  CachedStateStoreOptions

	dbtx     DBTX
	statsFn  func() StoreStats
	closeFn  func()
	closer   interface{ Close() error }
	beginner TxBeginner
}

// NewCachedStateStore wraps inner with the given shared cache. A nil cache
// yields an error; construct a passthrough path at the call site instead.
func NewCachedStateStore(inner StateStore, c *cache.Cache, opts CachedStateStoreOptions) (*CachedStateStore, error) {
	if inner == nil {
		return nil, errors.New("cached state store requires an inner store")
	}
	if c == nil {
		return nil, errors.New("cached state store requires a cache")
	}
	wrapped := &CachedStateStore{
		StateStore: inner,
		inner:      inner,
		cache:      c,
		opts:       opts.withDefaults(),
	}
	wrapped.bindCapabilities()
	return wrapped, nil
}

func (s *CachedStateStore) bindCapabilities() {
	if dbtx, ok := s.inner.(DBTX); ok {
		s.dbtx = dbtx
	}
	if beginner, ok := s.inner.(TxBeginner); ok {
		s.beginner = beginner
	}
	if closer, ok := s.inner.(interface{ Close() }); ok {
		s.closeFn = closer.Close
	} else if closerErr, ok := s.inner.(interface{ Close() error }); ok {
		s.closer = closerErr
	}
	if statsSource, ok := s.inner.(interface{ Stats() StoreStats }); ok {
		s.statsFn = statsSource.Stats
	}
}

// Unwrap exposes the inner store for capability checks such as AsPostgresDB.
func (s *CachedStateStore) Unwrap() StateStore {
	if s == nil {
		return nil
	}
	return s.inner
}

// GetRecord serves point reads from the cache first. Cache errors degrade to
// the inner store through OnFallback; they never fail the caller.
func (s *CachedStateStore) GetRecord(ctx context.Context, domain, collection, organizationID, recordID string) (DomainRecord, bool, error) {
	if s == nil || s.inner == nil {
		return DomainRecord{}, false, errors.New("cached state store has no inner store")
	}
	key := s.recordKey(domain, collection, organizationID, recordID)
	if key == "" {
		return s.inner.GetRecord(ctx, domain, collection, organizationID, recordID)
	}

	var envelope cachedRecordEnvelope
	bctx, cancel := s.bounded(ctx)
	err := s.cache.Get(bctx, key, &envelope)
	cancel()
	switch {
	case err == nil:
		if !envelope.Found {
			return DomainRecord{}, false, nil
		}
		rec, decodeErr := envelope.record(domain, collection, organizationID, recordID)
		if decodeErr != nil {
			// Corrupt payload: degrade to a read-through instead of serving
			// an empty record, then let the refresh overwrite the bad entry.
			s.reportFallback("decode", decodeErr)
		} else {
			return rec, true, nil
		}
	case errors.Is(err, cache.ErrNotFound):
		// Plain miss: fall through to the read-through path below.
	default:
		s.reportFallback("get", err)
	}

	rec, found, err := s.inner.GetRecord(ctx, domain, collection, organizationID, recordID)
	if err != nil {
		return DomainRecord{}, false, err
	}
	s.storeAfterRead(ctx, key, rec, found)
	if !found {
		return DomainRecord{}, false, nil
	}
	return rec, true, nil
}

// UpsertRecord commits to the inner store first, then refreshes the cache
// entry unless a strictly newer version is already present. The skip rule
// keeps late-arriving older commits from clobbering newer cached values.
func (s *CachedStateStore) UpsertRecord(ctx context.Context, rec DomainRecord) (DomainRecord, error) {
	if s == nil || s.inner == nil {
		return DomainRecord{}, errors.New("cached state store has no inner store")
	}
	result, err := s.inner.UpsertRecord(ctx, rec)
	if err != nil {
		return DomainRecord{}, err
	}
	s.refreshAfterUpsert(ctx, result)
	return result, nil
}

// DeleteRecord commits the delete, then caches a short-lived tombstone. A
// re-read guards against a concurrent recreate between commit and tombstone.
func (s *CachedStateStore) DeleteRecord(ctx context.Context, domain, collection, organizationID, recordID string) error {
	if s == nil || s.inner == nil {
		return errors.New("cached state store has no inner store")
	}
	key := s.recordKey(domain, collection, organizationID, recordID)
	if err := s.inner.DeleteRecord(ctx, domain, collection, organizationID, recordID); err != nil {
		return err
	}
	if key == "" {
		return nil
	}
	rec, found, _ := s.inner.GetRecord(ctx, domain, collection, organizationID, recordID)
	s.storeAfterRead(ctx, key, rec, found)
	return nil
}

// InvalidateScope drops every tagged entry for one domain:collection:org
// scope across all processes sharing the backend. Call it after bulk write
// paths (postgres bulk upserts bypass this wrapper). Sweeps are bounded per
// call by the cache package; repeat for very large scopes.
func (s *CachedStateStore) InvalidateScope(ctx context.Context, domain, collection, organizationID string) error {
	if s == nil || s.cache == nil {
		return errors.New("cached state store has no cache")
	}
	inv := cache.NewInvalidator(s.cache)
	return inv.InvalidateTag(ctx, scopeTagValue(domain, collection, organizationID))
}

// InvalidateOrganization drops every tagged entry for one organization.
func (s *CachedStateStore) InvalidateOrganization(ctx context.Context, organizationID string) error {
	if s == nil || s.cache == nil {
		return errors.New("cached state store has no cache")
	}
	inv := cache.NewInvalidator(s.cache)
	return inv.InvalidateTag(ctx, orgTagValue(organizationID))
}

func (s *CachedStateStore) storeAfterRead(ctx context.Context, key string, rec DomainRecord, found bool) {
	envelope := newEnvelope(rec, found)
	ttl := s.opts.NegativeTTL
	if found {
		ttl = s.opts.TTL
	}
	bctx, cancel := s.bounded(ctx)
	defer cancel()
	if err := s.cache.Set(bctx, key, envelope, ttl); err != nil {
		s.reportFallback("set", err)
		return
	}
	s.tagKey(bctx, key, rec.Domain, rec.Collection, rec.OrganizationID)
}

func (s *CachedStateStore) refreshAfterUpsert(ctx context.Context, result DomainRecord) {
	key := s.recordKey(result.Domain, result.Collection, result.OrganizationID, result.RecordID)
	if key == "" {
		return
	}
	version := result.UpdatedAt.UnixNano()

	var current cachedRecordEnvelope
	bctx, cancel := s.bounded(ctx)
	err := s.cache.Get(bctx, key, &current)
	cancel()
	if err != nil && !errors.Is(err, cache.ErrNotFound) {
		s.reportFallback("get", err)
	}
	if err == nil && current.Version > version {
		return // A newer committed value is already cached.
	}

	bctx, cancel = s.bounded(ctx)
	defer cancel()
	envelope := newEnvelope(result, true)
	if err := s.cache.Set(bctx, key, envelope, s.opts.TTL); err != nil {
		s.reportFallback("set", err)
		return
	}
	s.tagKey(bctx, key, result.Domain, result.Collection, result.OrganizationID)
}

func (s *CachedStateStore) tagKey(ctx context.Context, key, domain, collection, organizationID string) {
	inv := cache.NewInvalidator(s.cache)
	tags := []string{scopeTagValue(domain, collection, organizationID)}
	if trimmed := strings.TrimSpace(organizationID); trimmed != "" {
		tags = append(tags, orgTagValue(trimmed))
	}
	if err := inv.Tag(ctx, key, tags...); err != nil {
		s.reportFallback("tag", err)
	}
}

func (s *CachedStateStore) reportFallback(op string, err error) {
	if s.opts.OnFallback != nil {
		s.opts.OnFallback(op, err)
	}
}

func (s *CachedStateStore) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, s.opts.CacheTimeout)
}

// recordKey builds the backend key. Empty identities return "" and force
// passthrough, because such rows cannot be addressed consistently.
func (s *CachedStateStore) recordKey(domain, collection, organizationID, recordID string) string {
	domain = strings.TrimSpace(domain)
	collection = strings.TrimSpace(collection)
	recordID = strings.TrimSpace(recordID)
	if domain == "" || collection == "" || recordID == "" {
		return ""
	}
	parts := []string{s.opts.KeyPrefix, escapeKeyPart(domain), escapeKeyPart(collection), escapeKeyPart(strings.TrimSpace(organizationID)), escapeKeyPart(recordID)}
	return strings.Join(parts, ":")
}

func escapeKeyPart(part string) string {
	return url.QueryEscape(part)
}

func scopeTagValue(domain, collection, organizationID string) string {
	return fmt.Sprintf("scope:%s:%s:%s", escapeKeyPart(domain), escapeKeyPart(collection), escapeKeyPart(organizationID))
}

func orgTagValue(organizationID string) string {
	return "org:" + escapeKeyPart(organizationID)
}

func newEnvelope(rec DomainRecord, found bool) cachedRecordEnvelope {
	envelope := cachedRecordEnvelope{Found: found, Version: rec.UpdatedAt.UnixNano()}
	if !found {
		return envelope
	}
	data, err := rec.Data.MarshalJSON()
	if err != nil {
		// Marshal failures leave an empty payload; the next read-through
		// repopulates from the inner store.
		return cachedRecordEnvelope{Version: rec.UpdatedAt.UnixNano(), Found: false}
	}
	envelope.DataJSON = data
	envelope.CreatedAt = rec.CreatedAt.UTC()
	envelope.UpdatedAt = rec.UpdatedAt.UTC()
	return envelope
}

func (e cachedRecordEnvelope) record(domain, collection, organizationID, recordID string) (DomainRecord, error) {
	data, err := parseDataJSON(e.DataJSON)
	if err != nil {
		// Unreadable payloads must never surface as empty records. The
		// caller degrades to a read-through and overwrites this entry.
		return DomainRecord{}, err
	}
	return DomainRecord{
		Domain:         strings.TrimSpace(domain),
		Collection:     strings.TrimSpace(collection),
		OrganizationID: strings.TrimSpace(organizationID),
		RecordID:       strings.TrimSpace(recordID),
		Data:           data,
		CreatedAt:      e.CreatedAt,
		UpdatedAt:      e.UpdatedAt,
	}, nil
}

// DBTX delegation keeps the wrapper usable wherever RuntimeStore is expected.

func (s *CachedStateStore) Exec(ctx context.Context, query string, args ...any) error {
	if s == nil || s.dbtx == nil {
		return errors.New("inner store does not support Exec")
	}
	return s.dbtx.Exec(ctx, query, args...)
}

func (s *CachedStateStore) QueryRow(ctx context.Context, query string, args ...any) RowScanner {
	if s == nil || s.dbtx == nil {
		return errorRowScanner{err: errors.New("inner store does not support QueryRow")}
	}
	return s.dbtx.QueryRow(ctx, query, args...)
}

func (s *CachedStateStore) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	if s == nil || s.dbtx == nil {
		return nil, errors.New("inner store does not support Query")
	}
	return s.dbtx.Query(ctx, query, args...)
}

func (s *CachedStateStore) BeginTx(ctx context.Context) (Tx, error) {
	if s == nil || s.beginner == nil {
		return nil, errors.New("inner store does not support transactions")
	}
	return s.beginner.BeginTx(ctx)
}

func (s *CachedStateStore) Stats() StoreStats {
	if s == nil || s.statsFn == nil {
		return StoreStats{}
	}
	return s.statsFn()
}

func (s *CachedStateStore) Close() {
	if s == nil {
		return
	}
	if s.closeFn != nil {
		s.closeFn()
		return
	}
	if s.closer != nil {
		_ = s.closer.Close()
	}
}

type errorRowScanner struct{ err error }

func (r errorRowScanner) Scan(_ ...any) error { return r.err }

// AsPostgresDB walks Unwrap chains (bounded depth) to find the concrete
// PostgresDB behind wrappers such as hermes projections or this cache.
func AsPostgresDB(store any) (*PostgresDB, bool) {
	const maxUnwrapDepth = 8
	for depth := 0; depth < maxUnwrapDepth && store != nil; depth++ {
		if pg, ok := store.(*PostgresDB); ok {
			return pg, true
		}
		unwrapper, ok := store.(interface{ Unwrap() StateStore })
		if !ok {
			return nil, false
		}
		store = unwrapper.Unwrap()
	}
	return nil, false
}
