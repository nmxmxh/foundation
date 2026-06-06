package appbench

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	runtimeconfig "github.com/nmxmxh/ovasabi_foundation/config-contracts/go/runtimeconfig"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/cache"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/worker"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/wsrouting"
)

func TestScaleHarness_LocalDistributedPressure(t *testing.T) {
	ctx := context.Background()

	db := seedScaleDB(t, 100, 100)
	count, err := db.CountRecords(ctx, "experience", "state", "org-0042", database.RecordQuery{})
	if err != nil {
		t.Fatalf("count tenant records: %v", err)
	}
	if count != 100 {
		t.Fatalf("tenant count = %d, want 100", count)
	}
	rows, err := db.ListRecords(ctx, "experience", "state", "org-0042", scaleRecordQuery(t, 10, "state", "active"))
	if err != nil {
		t.Fatalf("list tenant records: %v", err)
	}
	if len(rows) != 10 {
		t.Fatalf("filtered tenant rows = %d, want 10", len(rows))
	}
	for _, row := range rows {
		if row.OrganizationID != "org-0042" {
			t.Fatalf("tenant predicate leaked row: %+v", row)
		}
	}

	router := seedScaleRouter(t, 1000, 10)
	userTargets, err := router.ResolveTargets(ctx, wsrouting.TargetedDelivery{TargetType: "user", TargetID: "user-0042"})
	if err != nil {
		t.Fatalf("resolve user targets: %v", err)
	}
	if len(userTargets) != 10 {
		t.Fatalf("user target count = %d, want 10", len(userTargets))
	}
	broadcastBuf := make([]string, 0, 10000)
	broadcastTargets, err := router.ResolveTargetsInto(ctx, wsrouting.TargetedDelivery{TargetType: "broadcast"}, broadcastBuf)
	if err != nil {
		t.Fatalf("resolve broadcast targets: %v", err)
	}
	if len(broadcastTargets) != 10000 {
		t.Fatalf("broadcast target count = %d, want 10000", len(broadcastTargets))
	}
	for i := range 1000 {
		connID := fmt.Sprintf("conn-%04d-00", i)
		if err := router.Unregister(ctx, connID); err != nil {
			t.Fatalf("unregister churn conn %d: %v", i, err)
		}
		if err := router.Register(ctx, wsrouting.ConnectionInfo{
			ConnectionID: connID,
			DeviceID:     fmt.Sprintf("device-%04d-00", i),
			UserID:       fmt.Sprintf("user-%04d", i),
		}); err != nil {
			t.Fatalf("register churn conn %d: %v", i, err)
		}
	}
	if got := router.LocalConnectionCount(); got != 10000 {
		t.Fatalf("post-churn connection count = %d, want 10000", got)
	}

	assertScaleCacheStampede(t, ctx, 512)
	assertScaleEventFanoutIsolation(t, ctx, 1000)
	assertScaleRedisFanout(t, ctx, 1024)
	assertScaleQueueSaturation(t, ctx)
	assertScaleConfigConvergence(t, 2048)
}

func BenchmarkScale_MemoryDBTenantCount100K(b *testing.B) {
	db := seedScaleDB(b, 1000, 100)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		count, err := db.CountRecords(ctx, "experience", "state", "org-0420", database.RecordQuery{})
		if err != nil {
			b.Fatal(err)
		}
		if count != 100 {
			b.Fatalf("count = %d, want 100", count)
		}
	}
}

func BenchmarkScale_MemoryDBTenantListFiltered100K(b *testing.B) {
	db := seedScaleDB(b, 1000, 100)
	ctx := context.Background()
	filters := scaleRecordQuery(b, 50, "state", "active")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := db.ListRecords(ctx, "experience", "state", "org-0420", filters)
		if err != nil {
			b.Fatal(err)
		}
		if len(rows) != 50 {
			b.Fatalf("rows = %d, want 50", len(rows))
		}
	}
}

func BenchmarkScale1M_MemoryDBTenantCount(b *testing.B) {
	db := seedScaleDB(b, 10000, 100)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		count, err := db.CountRecords(ctx, "experience", "state", "org-4200", database.RecordQuery{})
		if err != nil {
			b.Fatal(err)
		}
		if count != 100 {
			b.Fatalf("count = %d, want 100", count)
		}
	}
}

func BenchmarkScale1M_MemoryDBTenantListFiltered(b *testing.B) {
	db := seedScaleDB(b, 10000, 100)
	ctx := context.Background()
	filters := scaleRecordQuery(b, 50, "state", "active")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := db.ListRecords(ctx, "experience", "state", "org-4200", filters)
		if err != nil {
			b.Fatal(err)
		}
		if len(rows) != 50 {
			b.Fatalf("rows = %d, want 50", len(rows))
		}
	}
}

func BenchmarkScale1M_MemoryDBDenseTenantListFilteredLimit(b *testing.B) {
	db := seedDenseScaleDB(b, 1_000_000)
	ctx := context.Background()
	filters := scaleRecordQuery(b, 50, "state", "active")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := db.ListRecords(ctx, "experience", "state", "org-dense", filters)
		if err != nil {
			b.Fatal(err)
		}
		if len(rows) != 50 {
			b.Fatalf("rows = %d, want 50", len(rows))
		}
	}
}

func BenchmarkScale_WebSocketBroadcastResolveInto100K(b *testing.B) {
	router := seedScaleRouter(b, 10000, 10)
	ctx := context.Background()
	target := wsrouting.TargetedDelivery{TargetType: "broadcast"}
	buf := make([]string, 0, 100000)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var err error
		buf = buf[:0]
		buf, err = router.ResolveTargetsInto(ctx, target, buf)
		if err != nil {
			b.Fatal(err)
		}
		if len(buf) != 100000 {
			b.Fatalf("targets = %d, want 100000", len(buf))
		}
	}
}

func BenchmarkScale_WebSocketBroadcastResolveInto1K(b *testing.B) {
	router := seedScaleRouter(b, 100, 10)
	ctx := context.Background()
	target := wsrouting.TargetedDelivery{TargetType: "broadcast"}
	buf := make([]string, 0, 1000)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var err error
		buf = buf[:0]
		buf, err = router.ResolveTargetsInto(ctx, target, buf)
		if err != nil {
			b.Fatal(err)
		}
		if len(buf) != 1000 {
			b.Fatalf("targets = %d, want 1000", len(buf))
		}
	}
}

func BenchmarkScale_WebSocketBroadcastBatch1K(b *testing.B) {
	router := seedScaleRouter(b, 100, 10)
	ctx := context.Background()
	target := wsrouting.TargetedDelivery{TargetType: "broadcast"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		batches := 0
		count, err := router.ForEachTargetBatch(ctx, target, 0, func(ids []string) bool {
			batches++
			return len(ids) > 0
		})
		if err != nil {
			b.Fatal(err)
		}
		if count != 1000 || batches != 1 {
			b.Fatalf("targets=%d batches=%d, want 1000/1", count, batches)
		}
	}
}

func BenchmarkScale_WebSocketBroadcastBatch100K(b *testing.B) {
	router := seedScaleRouter(b, 10000, 10)
	ctx := context.Background()
	target := wsrouting.TargetedDelivery{TargetType: "broadcast"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		count, err := router.ForEachTargetBatch(ctx, target, 0, func(ids []string) bool {
			return len(ids) > 0
		})
		if err != nil {
			b.Fatal(err)
		}
		if count != 100000 {
			b.Fatalf("targets = %d, want 100000", count)
		}
	}
}

func BenchmarkScale_WebSocketUserResolve100K(b *testing.B) {
	router := seedScaleRouter(b, 10000, 10)
	ctx := context.Background()
	target := wsrouting.TargetedDelivery{TargetType: "user", TargetID: "user-4242"}
	buf := make([]string, 0, 10)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var err error
		buf = buf[:0]
		buf, err = router.ResolveTargetsInto(ctx, target, buf)
		if err != nil {
			b.Fatal(err)
		}
		if len(buf) != 10 {
			b.Fatalf("targets = %d, want 10", len(buf))
		}
	}
}

func BenchmarkScale1M_WebSocketBroadcastResolveInto(b *testing.B) {
	router := seedScaleRouter(b, 100000, 10)
	ctx := context.Background()
	target := wsrouting.TargetedDelivery{TargetType: "broadcast"}
	buf := make([]string, 0, 1000000)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var err error
		buf = buf[:0]
		buf, err = router.ResolveTargetsInto(ctx, target, buf)
		if err != nil {
			b.Fatal(err)
		}
		if len(buf) != 1000000 {
			b.Fatalf("targets = %d, want 1000000", len(buf))
		}
	}
}

func BenchmarkScale1M_WebSocketBroadcastForEach(b *testing.B) {
	router := seedScaleRouter(b, 100000, 10)
	ctx := context.Background()
	target := wsrouting.TargetedDelivery{TargetType: "broadcast"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		count, err := router.ForEachTarget(ctx, target, func(string) bool {
			return true
		})
		if err != nil {
			b.Fatal(err)
		}
		if count != 1000000 {
			b.Fatalf("targets = %d, want 1000000", count)
		}
	}
}

func BenchmarkScale1M_WebSocketBroadcastBatch(b *testing.B) {
	router := seedScaleRouter(b, 100000, 10)
	ctx := context.Background()
	target := wsrouting.TargetedDelivery{TargetType: "broadcast"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		count, err := router.ForEachTargetBatch(ctx, target, 0, func(ids []string) bool {
			return len(ids) > 0
		})
		if err != nil {
			b.Fatal(err)
		}
		if count != 1000000 {
			b.Fatalf("targets = %d, want 1000000", count)
		}
	}
}

func BenchmarkScale1M_WebSocketUserResolve(b *testing.B) {
	router := seedScaleRouter(b, 100000, 10)
	ctx := context.Background()
	target := wsrouting.TargetedDelivery{TargetType: "user", TargetID: "user-42420"}
	buf := make([]string, 0, 10)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var err error
		buf = buf[:0]
		buf, err = router.ResolveTargetsInto(ctx, target, buf)
		if err != nil {
			b.Fatal(err)
		}
		if len(buf) != 10 {
			b.Fatalf("targets = %d, want 10", len(buf))
		}
	}
}

func BenchmarkScale_EventExactDispatch100KSubscriptions(b *testing.B) {
	bus := events.NewInMemoryBus(1024)
	var deliveries atomic.Int64
	for i := range 100000 {
		eventType := fmt.Sprintf("tenant:org_%05d:signal:success", i)
		bus.Subscribe(eventType, func(_ context.Context, _ events.Envelope) {
			deliveries.Add(1)
		})
	}
	ctx := context.Background()
	env := scaleEnvelope("tenant:org_99999:signal:success")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := bus.Publish(ctx, env); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if deliveries.Load() != int64(b.N) {
		b.Fatalf("deliveries = %d, want %d", deliveries.Load(), b.N)
	}
}

func BenchmarkScale1M_EventExactDispatchSubscriptions(b *testing.B) {
	bus := events.NewInMemoryBus(1024)
	var deliveries atomic.Int64
	for i := range 1000000 {
		eventType := fmt.Sprintf("tenant:org_%06d:signal:success", i)
		bus.Subscribe(eventType, func(_ context.Context, _ events.Envelope) {
			deliveries.Add(1)
		})
	}
	ctx := context.Background()
	env := scaleEnvelope("tenant:org_999999:signal:success")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := bus.Publish(ctx, env); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if deliveries.Load() != int64(b.N) {
		b.Fatalf("deliveries = %d, want %d", deliveries.Load(), b.N)
	}
}

func BenchmarkScale_EventWildcardDispatch1KSubscriptions(b *testing.B) {
	bus := events.NewInMemoryBus(1024)
	var deliveries atomic.Int64
	for i := range 1000 {
		bus.Subscribe(fmt.Sprintf("tenant:org_%04d:*", i), func(_ context.Context, _ events.Envelope) {
			deliveries.Add(1)
		})
	}
	ctx := context.Background()
	env := scaleEnvelope("tenant:org_0999:signal:success")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := bus.Publish(ctx, env); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if deliveries.Load() != int64(b.N) {
		b.Fatalf("deliveries = %d, want %d", deliveries.Load(), b.N)
	}
}

func BenchmarkScale_EventPrefixWildcardDispatch100KSubscriptions(b *testing.B) {
	bus := events.NewInMemoryBus(1024)
	var deliveries atomic.Int64
	for i := range 100000 {
		bus.Subscribe(fmt.Sprintf("tenant:org_%05d:*", i), func(_ context.Context, _ events.Envelope) {
			deliveries.Add(1)
		})
	}
	ctx := context.Background()
	env := scaleEnvelope("tenant:org_99999:signal:success")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := bus.Publish(ctx, env); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if deliveries.Load() != int64(b.N) {
		b.Fatalf("deliveries = %d, want %d", deliveries.Load(), b.N)
	}
}

func BenchmarkScale_ConfigConvergence10K(b *testing.B) {
	cfg := scaleServerConfig()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := runtimeconfig.ValidateServer(cfg); err != nil {
			b.Fatal(err)
		}
		_ = runtimeconfig.DerivePublic(cfg)
	}
}

func BenchmarkScale_LocalOperationMixLatency(b *testing.B) {
	harness := newLocalOperationMixHarness(b)
	defer harness.close()

	sampleLimit := min(b.N, 100000)
	samples := make([]int64, 0, sampleLimit)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		if err := harness.runOnce(); err != nil {
			b.Fatal(err)
		}
		if len(samples) < sampleLimit {
			samples = append(samples, time.Since(start).Nanoseconds())
		}
	}
	b.StopTimer()
	reportPercentiles(b, samples)
}

func BenchmarkScale_LocalOperationMixLatencyBreakdown(b *testing.B) {
	harness := newLocalOperationMixHarness(b)
	defer harness.close()

	sampleLimit := min(b.N, 100000)
	totalSamples := make([]int64, 0, sampleLimit)
	dbSamples := make([]int64, 0, sampleLimit)
	routeSamples := make([]int64, 0, sampleLimit)
	cacheSamples := make([]int64, 0, sampleLimit)
	eventSamples := make([]int64, 0, sampleLimit)
	configSamples := make([]int64, 0, sampleLimit)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(totalSamples) >= sampleLimit {
			if err := harness.runOnce(); err != nil {
				b.Fatal(err)
			}
			continue
		}
		start := time.Now()
		mark := start
		if err := harness.countRecords(); err != nil {
			b.Fatal(err)
		}
		now := time.Now()
		dbSamples = append(dbSamples, now.Sub(mark).Nanoseconds())
		mark = now
		if err := harness.resolveTargets(); err != nil {
			b.Fatal(err)
		}
		now = time.Now()
		routeSamples = append(routeSamples, now.Sub(mark).Nanoseconds())
		mark = now
		if err := harness.cacheGet(); err != nil {
			b.Fatal(err)
		}
		now = time.Now()
		cacheSamples = append(cacheSamples, now.Sub(mark).Nanoseconds())
		mark = now
		if err := harness.publishEvent(); err != nil {
			b.Fatal(err)
		}
		now = time.Now()
		eventSamples = append(eventSamples, now.Sub(mark).Nanoseconds())
		mark = now
		if err := harness.validateConfig(); err != nil {
			b.Fatal(err)
		}
		now = time.Now()
		configSamples = append(configSamples, now.Sub(mark).Nanoseconds())
		totalSamples = append(totalSamples, now.Sub(start).Nanoseconds())
	}
	b.StopTimer()
	reportPercentilesNamed(b, "mix-total-", totalSamples)
	reportPercentilesNamed(b, "mix-db-count-", dbSamples)
	reportPercentilesNamed(b, "mix-route-resolve-", routeSamples)
	reportPercentilesNamed(b, "mix-cache-get-", cacheSamples)
	reportPercentilesNamed(b, "mix-event-publish-", eventSamples)
	reportPercentilesNamed(b, "mix-config-validate-", configSamples)
}

type localOperationMixHarness struct {
	ctx     context.Context
	db      *database.MemoryDB
	router  *wsrouting.Router
	bus     *events.InMemoryBus
	cache   *cache.Cache
	cfg     runtimeconfig.ServerRuntimeConfig
	env     events.Envelope
	target  wsrouting.TargetedDelivery
	targets []string
	close   func()
}

func newLocalOperationMixHarness(tb testing.TB) *localOperationMixHarness {
	tb.Helper()
	ctx := context.Background()
	db := seedScaleDB(tb, 100, 100)
	router := seedScaleRouter(tb, 1000, 10)
	bus := events.NewInMemoryBus(1024)
	bus.Subscribe("tenant:org_0042:signal:success", func(context.Context, events.Envelope) {})
	backend := cache.NewMemoryBackend()
	c := cache.New(cache.Config{Backend: backend, DefaultTTL: time.Minute})
	if err := c.Set(ctx, "tenant:org_0042:profile", map[string]any{"status": "ready"}); err != nil {
		tb.Fatal(err)
	}
	return &localOperationMixHarness{
		ctx:     ctx,
		db:      db,
		router:  router,
		bus:     bus,
		cache:   c,
		cfg:     scaleServerConfig(),
		env:     scaleEnvelope("tenant:org_0042:signal:success"),
		target:  wsrouting.TargetedDelivery{TargetType: "user", TargetID: "user-0042"},
		targets: make([]string, 0, 10),
		close: func() {
			_ = backend.Close()
		},
	}
}

func (h *localOperationMixHarness) runOnce() error {
	if err := h.countRecords(); err != nil {
		return err
	}
	if err := h.resolveTargets(); err != nil {
		return err
	}
	if err := h.cacheGet(); err != nil {
		return err
	}
	if err := h.publishEvent(); err != nil {
		return err
	}
	return h.validateConfig()
}

func (h *localOperationMixHarness) countRecords() error {
	if _, err := h.db.CountRecords(h.ctx, "experience", "state", "org-0042", database.RecordQuery{}); err != nil {
		return err
	}
	return nil
}

func (h *localOperationMixHarness) resolveTargets() error {
	h.targets = h.targets[:0]
	targets, err := h.router.ResolveTargetsInto(h.ctx, h.target, h.targets)
	if err != nil {
		return err
	}
	h.targets = targets
	return nil
}

func (h *localOperationMixHarness) cacheGet() error {
	var cached map[string]any
	if err := h.cache.Get(h.ctx, "tenant:org_0042:profile", &cached); err != nil {
		return err
	}
	return nil
}

func (h *localOperationMixHarness) publishEvent() error {
	if err := h.bus.Publish(h.ctx, h.env); err != nil {
		return err
	}
	return nil
}

func (h *localOperationMixHarness) validateConfig() error {
	return runtimeconfig.ValidateServer(h.cfg)
}

func scaleRecordData(tb testing.TB, fields ...any) database.RecordData {
	tb.Helper()
	if len(fields)%2 != 0 {
		tb.Fatalf("scaleRecordData requires name/value pairs")
	}
	out := make(database.RecordData, 0, len(fields)/2)
	for i := 0; i < len(fields); i += 2 {
		name, ok := fields[i].(string)
		if !ok {
			tb.Fatalf("field name %d is %T", i, fields[i])
		}
		value, ok := database.RecordValueFromAny(fields[i+1])
		if !ok {
			tb.Fatalf("field value %q is unsupported", name)
		}
		out = append(out, database.RecordField{Name: name, Value: value})
	}
	return out.Normalize()
}

func scaleRecordQuery(tb testing.TB, limit int, fields ...any) database.RecordQuery {
	tb.Helper()
	data := scaleRecordData(tb, fields...)
	filters := make([]database.RecordFilter, 0, len(data))
	for _, field := range data {
		filters = append(filters, database.RecordFilter{Field: field.Name, Value: field.Value})
	}
	return database.RecordQuery{Limit: limit, Filters: filters}.Normalize()
}

func seedScaleDB(tb testing.TB, tenants, recordsPerTenant int) *database.MemoryDB {
	tb.Helper()
	db := database.NewMemoryDB()
	ctx := context.Background()
	for tenant := range tenants {
		orgID := fmt.Sprintf("org-%04d", tenant)
		for rec := range recordsPerTenant {
			state := "idle"
			if rec%2 == 0 {
				state = "active"
			}
			_, err := db.UpsertRecord(ctx, database.DomainRecord{
				Domain:         "experience",
				Collection:     "state",
				OrganizationID: orgID,
				RecordID:       fmt.Sprintf("record-%05d", rec),
				Data:           scaleRecordData(tb, "organization_id", orgID, "state", state, "shard", rec%16),
			})
			if err != nil {
				tb.Fatalf("seed db tenant=%d record=%d: %v", tenant, rec, err)
			}
		}
	}
	return db
}

func seedDenseScaleDB(tb testing.TB, records int) *database.MemoryDB {
	tb.Helper()
	db := database.NewMemoryDB()
	ctx := context.Background()
	for rec := range records {
		state := "idle"
		if rec%2 == 0 {
			state = "active"
		}
		_, err := db.UpsertRecord(ctx, database.DomainRecord{
			Domain:         "experience",
			Collection:     "state",
			OrganizationID: "org-dense",
			RecordID:       fmt.Sprintf("record-%07d", rec),
			Data:           scaleRecordData(tb, "organization_id", "org-dense", "state", state, "shard", rec%16),
		})
		if err != nil {
			tb.Fatalf("seed dense db record=%d: %v", rec, err)
		}
	}
	return db
}

func seedScaleRouter(tb testing.TB, users, connsPerUser int) *wsrouting.Router {
	tb.Helper()
	router := wsrouting.NewRouter(nil, "scale-node-1")
	ctx := context.Background()
	for user := range users {
		for conn := range connsPerUser {
			err := router.Register(ctx, wsrouting.ConnectionInfo{
				ConnectionID: fmt.Sprintf("conn-%04d-%02d", user, conn),
				DeviceID:     fmt.Sprintf("device-%04d-%02d", user, conn),
				UserID:       fmt.Sprintf("user-%04d", user),
			})
			if err != nil {
				tb.Fatalf("seed router user=%d conn=%d: %v", user, conn, err)
			}
		}
	}
	return router
}

func assertScaleCacheStampede(t *testing.T, ctx context.Context, callers int) {
	t.Helper()
	backend := cache.NewMemoryBackend()
	defer func() { _ = backend.Close() }()
	c := cache.New(cache.Config{Backend: backend, DefaultTTL: time.Minute})
	var computes atomic.Int32
	start := make(chan struct{})
	release := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup

	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			<-start
			value, err := cache.GetOrSet(ctx, c, "tenant:org_0042:profile", func() (string, error) {
				computes.Add(1)
				<-release
				return "ready", nil
			}, time.Minute)
			if err != nil {
				errs <- err
				return
			}
			if value != "ready" {
				errs <- fmt.Errorf("cache value = %q, want ready", value)
			}
		}()
	}

	close(start)
	deadline := time.After(2 * time.Second)
	for computes.Load() == 0 {
		select {
		case <-deadline:
			close(release)
			t.Fatal("cache stampede compute did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("cache stampede caller failed: %v", err)
		}
	}
	if computes.Load() != 1 {
		t.Fatalf("cache computations = %d, want 1", computes.Load())
	}
}

func assertScaleEventFanoutIsolation(t *testing.T, ctx context.Context, subscribers int) {
	t.Helper()
	bus := events.NewInMemoryBus(1024)
	var org42, org99 atomic.Int64
	for range subscribers {
		bus.Subscribe("tenant:org_0042:signal:success", func(_ context.Context, env events.Envelope) {
			if orgID, _ := env.Metadata.GetString("organization_id"); orgID != "org-0042" {
				t.Errorf("org-0042 metadata leak: %+v", env.Metadata)
			}
			org42.Add(1)
		})
		bus.Subscribe("tenant:org_0099:signal:success", func(_ context.Context, _ events.Envelope) {
			org99.Add(1)
		})
	}
	env := scaleEnvelope("tenant:org_0042:signal:success")
	env.Metadata["organization_id"] = extension.String("org-0042")
	if err := bus.Publish(ctx, env); err != nil {
		t.Fatalf("publish exact fanout event: %v", err)
	}
	if org42.Load() != int64(subscribers) {
		t.Fatalf("org-0042 deliveries = %d, want %d", org42.Load(), subscribers)
	}
	if org99.Load() != 0 {
		t.Fatalf("org-0099 deliveries = %d, want 0", org99.Load())
	}
}

func assertScaleRedisFanout(t *testing.T, ctx context.Context, subscribers int) {
	t.Helper()
	client := rediskit.NewMemoryClient("scale")
	defer func() { _ = client.Close() }()
	cancelFns := make([]func(), 0, subscribers)
	channels := make([]<-chan []byte, 0, subscribers)
	for i := range subscribers {
		ch, cancel, err := client.Subscribe(ctx, "tenant:org_0042:fanout")
		if err != nil {
			t.Fatalf("redis subscribe %d: %v", i, err)
		}
		cancelFns = append(cancelFns, cancel)
		channels = append(channels, ch)
	}
	defer func() {
		for _, cancel := range cancelFns {
			cancel()
		}
	}()

	payload := []byte("event-ready")
	if err := client.Publish(ctx, "tenant:org_0042:fanout", payload); err != nil {
		t.Fatalf("redis publish fanout: %v", err)
	}
	deadline := time.After(2 * time.Second)
	for i, ch := range channels {
		select {
		case got := <-ch:
			if string(got) != string(payload) {
				t.Fatalf("redis payload %d = %q, want %q", i, got, payload)
			}
		case <-deadline:
			t.Fatalf("redis fanout delivered %d/%d before timeout", i, subscribers)
		}
	}
}

func assertScaleQueueSaturation(t *testing.T, ctx context.Context) {
	t.Helper()
	engine := worker.NewEngine(map[string]int{"scale": 1}, benchLogger{})
	if err := engine.Register(&appProcessor{kind: "scale.job", queue: "scale"}); err != nil {
		t.Fatalf("register scale processor: %v", err)
	}
	job := worker.Job{
		JobKind:       "scale.job",
		Queue:         "scale",
		RawPayload:    []byte(`{"tenant":"org-0042"}`),
		CorrelationID: "corr-scale",
		MaxAttempts:   1,
	}
	for i := range 1024 {
		job.ID = fmt.Sprintf("job-%04d", i)
		if err := engine.Enqueue(ctx, job); err != nil {
			t.Fatalf("prefill queue at %d: %v", i, err)
		}
	}
	job.ID = "job-overflow"
	err := engine.Enqueue(ctx, job)
	if err == nil {
		t.Fatal("expected queue saturation rejection")
	}
	if !strings.Contains(err.Error(), "worker queue full") {
		t.Fatalf("unexpected queue saturation error: %v", err)
	}
}

func assertScaleConfigConvergence(t *testing.T, goroutines int) {
	t.Helper()
	cfg := scaleServerConfig()
	var failures atomic.Int32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			if err := runtimeconfig.ValidateServer(cfg); err != nil {
				failures.Add(1)
				return
			}
			public := runtimeconfig.DerivePublic(cfg)
			if public.APIBaseURL == "" || public.WSBaseURL == "" {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatalf("config convergence failures = %d, want 0", failures.Load())
	}
}

func scaleServerConfig() runtimeconfig.ServerRuntimeConfig {
	return runtimeconfig.ServerRuntimeConfig{
		SchemaVersion: runtimeconfig.RuntimeConfigSchemaVersion,
		Public: runtimeconfig.PublicRuntimeConfig{
			SchemaVersion:       runtimeconfig.RuntimeConfigSchemaVersion,
			APIBaseURL:          "https://api.foundation.local",
			WSBaseURL:           "wss://api.foundation.local/ws",
			AuthMode:            "jwt",
			DefaultLocale:       "en-US",
			FeatureFlags:        map[string]bool{"runtime_binary": true, "tenant_fanout": true},
			TransportTimeoutsMS: runtimeconfig.TransportTimeouts{HTTP: 1000, WS: 1000, WASM: 250},
			WASMAssets: runtimeconfig.WASMAssets{
				ModulePath:           "/wasm/ovrt.wasm",
				CompressedModulePath: "/wasm/ovrt.wasm.br",
			},
			RuntimeMemory: runtimeconfig.RuntimeMemoryConfig{
				SharedMemory:   "auto",
				TransportOrder: []string{"sab", "transferable", "postMessage", "ws", "http"},
				Compression:    []string{"br", "gzip", "identity"},
				ArenaBytes:     4 << 20,
			},
			DiagnosticsEnabled: true,
			LocaleDefaults:     runtimeconfig.LocaleDefaults{Timezone: "UTC", Currency: "USD"},
		},
		Database: runtimeconfig.DatabaseConfig{
			URL:              "postgres://foundation.local/app",
			MaxConnections:   64,
			MinConnections:   8,
			AcquireTimeoutMS: 100,
			QueryTimeoutMS:   50,
			HotReadTimeoutMS: 25,
			ShardCount:       16,
		},
		Redis: runtimeconfig.RedisConfig{
			URL:               "redis://foundation.local:6379/0",
			ShardURLs:         []string{"redis://foundation.local:6379/0", "redis://foundation.local:6380/0"},
			KeyPrefix:         "foundation",
			DefaultTTLSeconds: 300,
			PoolSize:          64,
			MinIdle:           8,
			MaxRetries:        2,
		},
		ObjectStorage: runtimeconfig.ObjectStorageConfig{
			Endpoint:        "https://objects.foundation.local",
			PresignEndpoint: "https://objects.foundation.local/presign",
			Region:          "us-east-1",
			Bucket:          "foundation",
			AccessKey:       "test-access-key",
			SecretKey:       "test-secret-key",
			UseTLS:          true,
			Strict:          true,
		},
		JWT: runtimeconfig.JWTConfig{
			Secret:   "0123456789abcdef0123456789abcdef",
			Issuer:   "foundation",
			Audience: "foundation-users",
		},
		RuntimeBudgets: runtimeconfig.RuntimeBudgetConfig{
			DispatchMaxConcurrent:    8192,
			DispatchAcquireTimeoutMS: 25,
		},
		SLOs: runtimeconfig.SLOConfig{
			DispatchP99LatencyMS: 50,
			WorkerSuccessRate:    0.999,
			EventDeliveryLagMS:   100,
		},
		Compression: runtimeconfig.CompressionConfig{
			APIMinBytes:           1024,
			WASMPreferredEncoding: "br",
		},
		Security: runtimeconfig.ServerSecurityConfig{
			PostQuantum: runtimeconfig.PostQuantumConfig{
				TLSHybridKEM:             "auto",
				SignatureAlgorithm:       "classical",
				CryptoInventoryRequired:  true,
				LongLivedArtifactSigning: true,
			},
		},
		Queues: map[string]runtimeconfig.QueueConfig{
			"default": {Concurrency: 16, MaxRetries: 3},
			"scale":   {Concurrency: 64, MaxRetries: 1},
		},
	}
}

func scaleEnvelope(eventType string) events.Envelope {
	env := events.Envelope{
		EventType:     eventType,
		Payload:       extension.Object{"ok": extension.Bool(true)},
		Metadata:      extension.Object{"correlation_id": extension.String("corr-scale"), "organization_id": extension.String("org-0042")},
		CorrelationID: "corr-scale",
		SchemaVersion: "1.0",
		Timestamp:     time.Now().UTC(),
	}
	env.Normalize()
	return env
}

func reportPercentiles(b *testing.B, samples []int64) {
	b.Helper()
	reportPercentilesNamed(b, "", samples)
}

func reportPercentilesNamed(b *testing.B, prefix string, samples []int64) {
	b.Helper()
	if len(samples) == 0 {
		return
	}
	slices.Sort(samples)
	b.ReportMetric(float64(percentile(samples, 0.50)), prefix+"p50-ns/op")
	b.ReportMetric(float64(percentile(samples, 0.95)), prefix+"p95-ns/op")
	b.ReportMetric(float64(percentile(samples, 0.99)), prefix+"p99-ns/op")
	b.ReportMetric(float64(samples[len(samples)-1]), prefix+"max-ns/op")
}

func percentile(samples []int64, quantile float64) int64 {
	if len(samples) == 0 {
		return 0
	}
	index := max(int(math.Ceil(float64(len(samples))*quantile))-1, 0)
	if index >= len(samples) {
		index = len(samples) - 1
	}
	return samples[index]
}

func TestPercentileUsesConservativeNearestRank(t *testing.T) {
	samples := []int64{1, 2, 3, 4, 100}
	if got := percentile(samples, 0.50); got != 3 {
		t.Fatalf("p50 = %d, want 3", got)
	}
	if got := percentile(samples, 0.95); got != 100 {
		t.Fatalf("p95 = %d, want 100", got)
	}
	if got := percentile(samples, 0.99); got != 100 {
		t.Fatalf("p99 = %d, want 100", got)
	}
}
