package projectiongw

import (
	"context"
	"errors"
	"strings"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
)

// DefaultSnapshotLimit bounds a snapshot when the caller does not specify a
// limit, keeping the read response bounded by default (BoundedWork).
const DefaultSnapshotLimit = 1024

var (
	// ErrScopeInvalid is returned when a ProjectionScope is missing required
	// fields (tenant, domain, collection).
	ErrScopeInvalid = errors.New("projectiongw: scope tenant_id, domain, and collection are required")
	// ErrNilStore is returned when a Gateway is constructed without a store.
	ErrNilStore = errors.New("projectiongw: hermes store is required")
)

// Resolver maps a domain-agnostic ProjectionScope onto a concrete hermes
// projection partition name plus the tenant-scoped Query used to read it. It is
// the single place a project customizes how scopes bind to partitions; the
// default (DomainResolver) treats the scope domain as the projection name and
// the scope tenant as the organization filter.
type Resolver func(scope *foundationpb.ProjectionScope) (projection string, query hermes.Query, err error)

// DomainResolver is the default Resolver: one projection partition per domain,
// filtered by tenant. Collection scoping is applied by the gateway as a
// per-record filter on the resolved partition.
func DomainResolver(limit int) Resolver {
	return func(scope *foundationpb.ProjectionScope) (string, hermes.Query, error) {
		if err := validateScope(scope); err != nil {
			return "", hermes.Query{}, err
		}
		query := hermes.QueryWithFilters(scope.GetTenantId(), limit)
		return scope.GetDomain(), query, nil
	}
}

// Gateway is the generic read-path bridge over a hermes.Store. It serves scoped
// snapshots and, via an embedded Hub, fans live deltas out to subscribers. The
// projector must apply through the Gateway (ApplyEnvelopes) so applied mutations
// reach subscribers.
type Gateway struct {
	store    *hermes.Store
	hub      *Hub
	resolve  Resolver
	maxLimit int
	cancel   func()
	// warmScope lazily warms a cold scope on first read (see WithScopeWarmer):
	// a snapshot that would return ErrProjectionNotFound instead resolves the
	// scope through the projected store's warm path and retries once.
	warmScope func(ctx context.Context, scope *foundationpb.ProjectionScope) error
}

// Option configures a Gateway.
type Option func(*Gateway)

// WithResolver overrides the default scope→partition Resolver.
func WithResolver(resolver Resolver) Option {
	return func(g *Gateway) {
		if resolver != nil {
			g.resolve = resolver
		}
	}
}

// WithScopeWarmer makes cold scopes self-resolving on first read: when a
// snapshot hits ErrProjectionNotFound, the gateway invokes warm (idempotent,
// singleflighted by the projected store) and retries once. With this wired, a
// scope never needs eager warming for correctness — seeded data resolves on
// the first request, and a scope with no data anywhere serves an empty
// snapshot instead of a wiring error.
func WithScopeWarmer(warm func(ctx context.Context, scope *foundationpb.ProjectionScope) error) Option {
	return func(g *Gateway) {
		g.warmScope = warm
	}
}

// WithHub injects a shared Hub (e.g. one Hub across several gateways).
func WithHub(hub *Hub) Option {
	return func(g *Gateway) {
		if hub != nil {
			g.hub = hub
		}
	}
}

// NewGatewayForProjectedStore constructs a Gateway over a ProjectedRuntimeStore,
// wiring a Resolver that matches exactly how that store names its partitions
// (prefix:domain:collection:organization). This is the constructor the generated
// server uses: it guarantees scope→partition agreement with the write path, so
// snapshots find the same records the projected store materialized.
func NewGatewayForProjectedStore(projected *hermes.ProjectedRuntimeStore, queueSize int, opts ...Option) (*Gateway, error) {
	if projected == nil {
		return nil, ErrNilStore
	}
	resolver := func(scope *foundationpb.ProjectionScope) (string, hermes.Query, error) {
		if err := validateScope(scope); err != nil {
			return "", hermes.Query{}, err
		}
		name := projected.ProjectionName(scope.GetDomain(), scope.GetCollection(), scope.GetTenantId())
		return name, hermes.QueryWithFilters(scope.GetTenantId(), DefaultSnapshotLimit), nil
	}
	// Cold scopes lazy-warm on first read: WarmScope rebuilds from the record
	// mirror (and self-backfills via ScopeBackfill when the mirror is empty),
	// so no scope ever needs eager warming for correctness.
	warmer := func(ctx context.Context, scope *foundationpb.ProjectionScope) error {
		return projected.WarmScope(ctx, scope.GetDomain(), scope.GetCollection(), scope.GetTenantId())
	}
	return NewGateway(projected.Store(), queueSize, append([]Option{WithResolver(resolver), WithScopeWarmer(warmer)}, opts...)...)
}

// NewGateway constructs a Gateway over the given store. queueSize bounds the
// per-subscriber delta buffer (DefaultSubscriberQueue when <= 0).
func NewGateway(store *hermes.Store, queueSize int, opts ...Option) (*Gateway, error) {
	if store == nil {
		return nil, ErrNilStore
	}
	g := &Gateway{
		store:    store,
		hub:      NewHub(queueSize),
		resolve:  DomainResolver(DefaultSnapshotLimit),
		maxLimit: DefaultSnapshotLimit,
	}
	for _, opt := range opts {
		opt(g)
	}
	// Subscribe to every accepted apply so live deltas flow regardless of write
	// path: the in-process projected runtime store, the Redis envelope projector,
	// or a direct ApplyBatch all reach subscribers through this one seam.
	g.cancel = store.Observe(g.onApplied)
	return g, nil
}

// Close detaches the gateway's store observer. It is safe to call more than once.
func (g *Gateway) Close() {
	if g.cancel != nil {
		g.cancel()
		g.cancel = nil
	}
}

// Hub exposes the underlying fan-out hub.
func (g *Gateway) Hub() *Hub { return g.hub }

// onApplied fans an accepted apply batch out to subscribers, grouped by exact
// scope and stamped with the post-apply epoch. It runs after the store's
// partition lock is released, so the encode + non-blocking fan-out never
// serializes applies.
func (g *Gateway) onApplied(projection string, mutations []hermes.AppliedMutation) {
	epoch, _ := g.store.Epoch(projection)
	for key, group := range groupAccepted(mutations) {
		if g.hub.SubscriberCount(key) == 0 {
			continue
		}
		watermark := watermarkFromMutations(group.mutations)
		frame, err := encodeFrame(group.mutations, epoch, watermark, "projectiongw")
		if err != nil {
			continue
		}
		g.hub.Broadcast(key, frame)
	}
}

// Snapshot reads the materialized read model for a scope at the current epoch as
// a ProjectionSnapshot. Records are carried as upsert mutations so the snapshot
// and the delta stream share one wire shape; watermark/epoch let the client
// resume the delta stream exactly where the snapshot ended.
func (g *Gateway) Snapshot(ctx context.Context, req *foundationpb.ProjectionSnapshotRequest) (*foundationpb.ProjectionSnapshot, error) {
	scope := req.GetScope()
	projection, query, err := g.resolve(scope)
	if err != nil {
		return nil, err
	}
	if limit := int(req.GetLimit()); limit > 0 && (query.Limit == 0 || limit < query.Limit) {
		query.Limit = limit
	}
	// Always bound the snapshot. A positive limit engages hermes's ordered-index
	// early-stop (visiting ~limit newest records instead of the whole scope), so
	// an unbounded scan can never be triggered by a client or a misconfigured
	// resolver. This keeps the read O(limit), not O(scope) — the BoundedWork
	// invariant. Benchmarks: a limited 10K-scope snapshot is ~0.58ms vs ~210ms
	// unbounded; the incremental (watermark) path over the same scope is ~0.23ms.
	if query.Limit <= 0 || query.Limit > g.maxLimit {
		query.Limit = g.maxLimit
	}
	// Honor the resume watermark (forward catch-up) and the keyset cursor
	// (backward backfill). A client that already holds prior state sends its
	// watermark for the changed tail; a client backfilling a scope larger than
	// the limit pages older records via the cursor. Each request stays O(limit).
	sinceVersion := hermes.ParseWatermark(req.GetSinceWatermark())
	beforeVersion := hermes.ParseWatermark(req.GetCursor())
	snapshot, err := g.store.SnapshotPage(ctx, projection, query, hermes.Fence{}, sinceVersion, beforeVersion)
	if errors.Is(err, hermes.ErrProjectionNotFound) && g.warmScope != nil {
		// Lazy warm: resolve the cold scope through the projected store (which
		// registers the partition, rebuilds from the mirror, and self-backfills
		// an empty mirror), then retry once. Warm failure preserves the
		// original not-found so the HTTP mapping stays stable.
		if warmErr := g.warmScope(ctx, scope); warmErr == nil {
			snapshot, err = g.store.SnapshotPage(ctx, projection, query, hermes.Fence{}, sinceVersion, beforeVersion)
		}
	}
	if err != nil {
		return nil, err
	}
	mutations := filterScope(snapshot.Mutations, scope)
	return &foundationpb.ProjectionSnapshot{
		Scope:      scope,
		Batch:      &foundationpb.RecordMutationBatch{Mutations: mutations},
		Watermark:  hermes.FormatWatermark(snapshot.Watermark),
		Epoch:      snapshot.Epoch,
		NextCursor: hermes.FormatWatermark(snapshot.NextCursor),
		HasMore:    snapshot.HasMore,
	}, nil
}

// Subscribe opens a live delta feed for a scope. The caller should typically
// take a snapshot first and present that snapshot's watermark to reconcile any
// gap; the Hub itself does not replay history.
func (g *Gateway) Subscribe(scope *foundationpb.ProjectionScope) (*Subscription, error) {
	if err := validateScope(scope); err != nil {
		return nil, err
	}
	return g.hub.Subscribe(scope), nil
}

// ApplyEnvelopes applies projection envelopes to the store. Broadcasting happens
// through the store apply observer (onApplied), so this is a thin pass-through
// retained for callers (e.g. an envelope projector) that want a single call that
// both applies and — via the observer — fans accepted deltas out. Only mutations
// hermes accepts are broadcast, each stamped with the version hermes assigned, so
// the stream is the source of truth.
func (g *Gateway) ApplyEnvelopes(ctx context.Context, projection string, envelopes []events.Envelope) (hermes.ApplyResult, error) {
	return g.store.ApplyEnvelopes(ctx, projection, envelopes)
}

type scopeGroup struct {
	scope     *foundationpb.ProjectionScope
	mutations []*foundationpb.RecordMutation
}

// groupAccepted converts each accepted mutation into a RecordMutation stamped
// with its assigned version and groups them by exact scope for fan-out.
func groupAccepted(accepted []hermes.AppliedMutation) map[string]*scopeGroup {
	groups := make(map[string]*scopeGroup)
	for _, applied := range accepted {
		mutation := hermes.MutationFromRecord(applied.Record, applied.Operation, applied.Version)
		scope := &foundationpb.ProjectionScope{
			TenantId:   mutation.GetOrganizationId(),
			Domain:     mutation.GetDomain(),
			Collection: mutation.GetCollection(),
		}
		key := ScopeKey(scope)
		group := groups[key]
		if group == nil {
			group = &scopeGroup{scope: scope}
			groups[key] = group
		}
		group.mutations = append(group.mutations, mutation)
	}
	return groups
}

func watermarkFromMutations(mutations []*foundationpb.RecordMutation) string {
	var max uint64
	for _, mutation := range mutations {
		if v := mutation.GetVersion(); v > max {
			max = v
		}
	}
	return hermes.FormatWatermark(max)
}

// encodeFrame builds a binary events.Envelope carrying a RecordMutationBatch and
// wraps it as a fan-out Frame. The envelope reuses the canonical hermes
// projection envelope shape (protobuf payload, terminal event type).
func encodeFrame(mutations []*foundationpb.RecordMutation, epoch uint64, watermark, correlationID string) (Frame, error) {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		correlationID = "projectiongw"
	}
	envelope, err := hermes.NewProjectionEnvelope(mutations, correlationID)
	if err != nil {
		return Frame{}, err
	}
	raw, err := envelope.ToBinary()
	if err != nil {
		return Frame{}, err
	}
	return Frame{Envelope: raw, Watermark: watermark, Epoch: epoch}, nil
}

// filterScope keeps only mutations whose domain/collection/tenant match the
// requested scope. The resolved partition may hold multiple collections; the
// gateway is the place collection scoping is enforced.
func filterScope(mutations []*foundationpb.RecordMutation, scope *foundationpb.ProjectionScope) []*foundationpb.RecordMutation {
	domain := scope.GetDomain()
	collection := scope.GetCollection()
	tenant := scope.GetTenantId()
	out := make([]*foundationpb.RecordMutation, 0, len(mutations))
	for _, mutation := range mutations {
		if mutation.GetDomain() != domain || mutation.GetCollection() != collection {
			continue
		}
		if org := mutation.GetOrganizationId(); org != "" && org != tenant {
			continue
		}
		out = append(out, mutation)
	}
	return out
}

// ScopeKey is the exact colon-prefix topic for a scope: tenant:domain:collection.
func ScopeKey(scope *foundationpb.ProjectionScope) string {
	if scope == nil {
		return "::"
	}
	return scope.GetTenantId() + ":" + scope.GetDomain() + ":" + scope.GetCollection()
}

func validateScope(scope *foundationpb.ProjectionScope) error {
	if scope == nil ||
		strings.TrimSpace(scope.GetTenantId()) == "" ||
		strings.TrimSpace(scope.GetDomain()) == "" ||
		strings.TrimSpace(scope.GetCollection()) == "" {
		return ErrScopeInvalid
	}
	return nil
}
