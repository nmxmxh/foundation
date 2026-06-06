package appbench

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/wsrouting"
)

const defaultLoadResearchSteps = "1000,10000,50000,100000,250000,500000,1000000"

func TestLoadResearchRamps(t *testing.T) {
	if os.Getenv("RUN_LOAD_RESEARCH") != "1" {
		t.Skip("set RUN_LOAD_RESEARCH=1 to run staged load research")
	}

	steps := loadResearchSteps(t)
	samples := loadResearchPositiveIntEnv(t, "LOAD_RESEARCH_SAMPLES", 9)
	recorder := newLoadResearchRecorder(t)
	defer recorder.close()

	t.Logf("LOAD_RESEARCH_BEGIN steps=%v samples=%d", steps, samples)
	for _, step := range steps {
		t.Run(fmt.Sprintf("step_%d", step), func(t *testing.T) {
			runLoadResearchStep(t, recorder, step, samples)
		})
	}
}

func runLoadResearchStep(t *testing.T, recorder *loadResearchRecorder, step int, samples int) {
	ctx := context.Background()
	runLoadResearchLane(t, recorder, step, "memorydb_count", samples, 100, func(t testing.TB) (loadResearchOperation, loadResearchCleanup, string) {
		db, orgID, _ := setupLoadResearchMemoryDB(t, step)
		return func() error {
			count, err := db.CountRecords(ctx, "experience", "state", orgID, database.RecordQuery{})
			if err != nil {
				return err
			}
			if count <= 0 {
				return fmt.Errorf("count = %d", count)
			}
			return nil
		}, nil, "tenant indexed count"
	})
	runLoadResearchLane(t, recorder, step, "memorydb_list_limit50", samples, 20, func(t testing.TB) (loadResearchOperation, loadResearchCleanup, string) {
		db, orgID, _ := setupLoadResearchMemoryDB(t, step)
		query := scaleRecordQuery(t, 50, "state", "active")
		return func() error {
			rows, err := db.ListRecords(ctx, "experience", "state", orgID, query)
			if err != nil {
				return err
			}
			if len(rows) != 50 {
				return fmt.Errorf("rows = %d, want 50", len(rows))
			}
			return nil
		}, nil, "tenant indexed filtered list with defensive copies"
	})
	runLoadResearchLane(t, recorder, step, "hermes_count", samples, 100, func(t testing.TB) (loadResearchOperation, loadResearchCleanup, string) {
		store, projection, query, _, _, _ := setupLoadResearchHermes(t, step)
		return func() error {
			count, err := store.Count(ctx, projection, query, hermes.Fence{})
			if err != nil {
				return err
			}
			if count <= 0 {
				return fmt.Errorf("hermes count = %d", count)
			}
			return nil
		}, nil, "indexed hotplane count"
	})
	runLoadResearchLane(t, recorder, step, "hermes_get", samples, 100, func(t testing.TB) (loadResearchOperation, loadResearchCleanup, string) {
		store, projection, _, orgID, recordID, _ := setupLoadResearchHermes(t, step)
		query := hermes.Query{OrganizationID: orgID}
		return func() error {
			_, ok, err := store.GetRecord(ctx, projection, query, recordID, hermes.Fence{})
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("missing hermes record %q", recordID)
			}
			return nil
		}, nil, "copied hotplane point read"
	})
	runLoadResearchLane(t, recorder, step, "hermes_foreach_limit50", samples, 20, func(t testing.TB) (loadResearchOperation, loadResearchCleanup, string) {
		store, projection, query, _, _, _ := setupLoadResearchHermes(t, step)
		limited := query
		limited.Limit = 50
		return func() error {
			seen, err := store.ForEachView(ctx, projection, limited, hermes.Fence{}, func(view hermes.RecordView) error {
				if view.RecordID == "" {
					return fmt.Errorf("empty record view")
				}
				return nil
			})
			if err != nil {
				return err
			}
			if seen != 50 {
				return fmt.Errorf("seen = %d, want 50", seen)
			}
			return nil
		}, nil, "borrowed hotplane view iteration"
	})
	runLoadResearchLane(t, recorder, step, "hermes_apply_records64_prebuilt", samples, 1, func(t testing.TB) (loadResearchOperation, loadResearchCleanup, string) {
		store, projection, _, orgID, _, counter := setupLoadResearchHermes(t, step)
		batches := prebuildLoadResearchHermesBatches(t, orgID, int(counter.value), max(samples, 16))
		var next int
		return func() error {
			batch := batches[next%len(batches)]
			next++
			base := counter.Add(uint64(len(batch)))
			result, err := store.ApplyRecords(ctx, projection, "load-research-prebuilt", base, batch)
			if err != nil {
				return err
			}
			if result.Applied != len(batch) {
				return fmt.Errorf("applied = %d, want %d", result.Applied, len(batch))
			}
			return nil
		}, nil, "incremental pure-upsert projector batch with caller-prebuilt records"
	})
	runLoadResearchLane(t, recorder, step, "hermes_build_apply_records64", samples, 1, func(t testing.TB) (loadResearchOperation, loadResearchCleanup, string) {
		store, projection, _, orgID, _, counter := setupLoadResearchHermes(t, step)
		return func() error {
			base := counter.Add(64)
			records := make([]database.DomainRecord, 64)
			for i := range records {
				id := int(base) + i
				records[i] = database.DomainRecord{
					Domain:         "signals",
					Collection:     "ticks",
					OrganizationID: orgID,
					RecordID:       fmt.Sprintf("load_apply_%09d", id),
					Data:           scaleRecordData(t, "bucket", id%16, "symbol", "OVS", "state", "hot"),
				}
			}
			result, err := store.ApplyRecords(ctx, projection, "load-research", uint64(base), records)
			if err != nil {
				return err
			}
			if result.Applied != len(records) {
				return fmt.Errorf("applied = %d, want %d", result.Applied, len(records))
			}
			return nil
		}, nil, "incremental pure-upsert projector batch including record construction"
	})
	runLoadResearchLane(t, recorder, step, "event_exact_dispatch", samples, 100, func(t testing.TB) (loadResearchOperation, loadResearchCleanup, string) {
		bus := events.NewInMemoryBus(1024)
		eventType := fmt.Sprintf("tenant:org_%07d:signal:success", step-1)
		for i := range step {
			bus.Subscribe(fmt.Sprintf("tenant:org_%07d:signal:success", i), func(context.Context, events.Envelope) {})
		}
		env := scaleEnvelope(eventType)
		return func() error {
			return bus.Publish(ctx, env)
		}, nil, "one exact event among staged subscription cardinality"
	})
	runLoadResearchLane(t, recorder, step, "router_broadcast_batch", samples, 20, func(t testing.TB) (loadResearchOperation, loadResearchCleanup, string) {
		router := setupLoadResearchRouter(t, step)
		target := wsrouting.TargetedDelivery{TargetType: "broadcast"}
		return func() error {
			count, err := router.ForEachTargetBatch(ctx, target, 0, func(ids []string) bool {
				return len(ids) > 0
			})
			if err != nil {
				return err
			}
			if count != step {
				return fmt.Errorf("broadcast batch count = %d, want %d", count, step)
			}
			return nil
		}, nil, "borrowed broadcast target batches before socket writes"
	})
	runLoadResearchLane(t, recorder, step, "router_broadcast_materialize", samples, 5, func(t testing.TB) (loadResearchOperation, loadResearchCleanup, string) {
		router := setupLoadResearchRouter(t, step)
		target := wsrouting.TargetedDelivery{TargetType: "broadcast"}
		buf := make([]string, 0, step)
		return func() error {
			var err error
			buf = buf[:0]
			buf, err = router.ResolveTargetsInto(ctx, target, buf)
			if err != nil {
				return err
			}
			if len(buf) != step {
				return fmt.Errorf("materialized targets = %d, want %d", len(buf), step)
			}
			return nil
		}, nil, "owned broadcast target materialization before socket writes"
	})
}

type loadResearchOperation func() error
type loadResearchCleanup func()
type loadResearchSetup func(testing.TB) (loadResearchOperation, loadResearchCleanup, string)

func runLoadResearchLane(
	t *testing.T,
	recorder *loadResearchRecorder,
	step int,
	lane string,
	samples int,
	opsPerSample int,
	setup loadResearchSetup,
) {
	t.Helper()
	runtime.GC()
	before := loadResearchRuntimeSnapshot()
	setupStart := time.Now()
	op, cleanup, notes := setup(t)
	setupElapsed := time.Since(setupStart)
	if cleanup != nil {
		defer cleanup()
	}
	stats := measureLoadResearchOperation(t, samples, opsPerSample, op)
	after := loadResearchRuntimeSnapshot()
	recorder.record(loadResearchRow{
		Step:           step,
		Lane:           lane,
		Setup:          setupElapsed,
		Stats:          stats,
		Before:         before,
		After:          after,
		HeapAllocDelta: int64(after.HeapAlloc) - int64(before.HeapAlloc),
		Notes:          notes,
	})
	runtime.GC()
}

func setupLoadResearchMemoryDB(tb testing.TB, totalRecords int) (*database.MemoryDB, string, int) {
	tb.Helper()
	recordsPerTenant := 100
	tenants := max(totalRecords/recordsPerTenant, 1)
	db := seedScaleDB(tb, tenants, recordsPerTenant)
	orgID := fmt.Sprintf("org-%04d", tenants/2)
	return db, orgID, tenants * recordsPerTenant
}

func setupLoadResearchHermes(
	tb testing.TB,
	records int,
) (*hermes.Store, string, hermes.Query, string, string, loadResearchCounter) {
	tb.Helper()
	projection := "load_research_ticks"
	orgID := "org_load_research"
	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name:          projection,
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"bucket", "symbol", "state"},
		MaxRecords:    records + 4096,
		MaxBytes:      max(int64(records)*512, int64(64<<20)),
	})
	if err != nil {
		tb.Fatalf("new hermes store: %v", err)
	}
	payload := make([]database.DomainRecord, records)
	for i := range payload {
		payload[i] = database.DomainRecord{
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationID: orgID,
			RecordID:       fmt.Sprintf("load_tick_%09d", i),
			Data:           scaleRecordData(tb, "bucket", i%16, "symbol", "OVS", "state", "hot"),
		}
	}
	if _, err := store.BulkLoad(context.Background(), projection, payload); err != nil {
		tb.Fatalf("hermes bulk load records=%d: %v", records, err)
	}
	filter, ok := hermes.NewQueryFilter("bucket", 7)
	if !ok {
		tb.Fatal("bucket filter is not indexable")
	}
	recordID := fmt.Sprintf("load_tick_%09d", max(records/2, 0))
	if records == 1 {
		recordID = "load_tick_000000000"
	}
	return store, projection, hermes.QueryWithFilters(orgID, 0, filter), orgID, recordID, loadResearchCounter{value: uint64(records)}
}

func setupLoadResearchRouter(tb testing.TB, connections int) *wsrouting.Router {
	tb.Helper()
	connsPerUser := 10
	users := max(connections/connsPerUser, 1)
	router := wsrouting.NewRouter(nil, "load-research-node")
	ctx := context.Background()
	registered := 0
	for user := range users {
		for conn := range connsPerUser {
			if registered >= connections {
				return router
			}
			err := router.Register(ctx, wsrouting.ConnectionInfo{
				ConnectionID: fmt.Sprintf("load-conn-%07d-%02d", user, conn),
				DeviceID:     fmt.Sprintf("load-device-%07d-%02d", user, conn),
				UserID:       fmt.Sprintf("load-user-%07d", user),
			})
			if err != nil {
				tb.Fatalf("router register user=%d conn=%d: %v", user, conn, err)
			}
			registered++
		}
	}
	return router
}

func prebuildLoadResearchHermesBatches(
	tb testing.TB,
	orgID string,
	startID int,
	batches int,
) [][]database.DomainRecord {
	tb.Helper()
	out := make([][]database.DomainRecord, batches)
	for batch := range out {
		records := make([]database.DomainRecord, 64)
		for i := range records {
			id := startID + batch*len(records) + i
			records[i] = database.DomainRecord{
				Domain:         "signals",
				Collection:     "ticks",
				OrganizationID: orgID,
				RecordID:       fmt.Sprintf("load_apply_prebuilt_%09d", id),
				Data:           scaleRecordData(tb, "bucket", id%16, "symbol", "OVS", "state", "hot"),
			}
		}
		out[batch] = records
	}
	return out
}

func measureLoadResearchOperation(
	t testing.TB,
	samples int,
	opsPerSample int,
	op loadResearchOperation,
) loadResearchStats {
	t.Helper()
	durations := make([]int64, 0, samples)
	for range samples {
		start := time.Now()
		for range opsPerSample {
			if err := op(); err != nil {
				t.Fatalf("load research operation failed: %v", err)
			}
		}
		durations = append(durations, time.Since(start).Nanoseconds()/int64(opsPerSample))
	}
	slices.Sort(durations)
	return loadResearchStats{
		Samples: samples,
		Ops:     opsPerSample,
		P50:     time.Duration(loadResearchPercentile(durations, 0.50)),
		P95:     time.Duration(loadResearchPercentile(durations, 0.95)),
		P99:     time.Duration(loadResearchPercentile(durations, 0.99)),
		Max:     time.Duration(durations[len(durations)-1]),
	}
}

type loadResearchCounter struct {
	value uint64
}

func (c *loadResearchCounter) Add(delta uint64) uint64 {
	c.value += delta
	return c.value
}

type loadResearchRecorder struct {
	t    testing.TB
	file *os.File
}

func newLoadResearchRecorder(t testing.TB) *loadResearchRecorder {
	t.Helper()
	recorder := &loadResearchRecorder{t: t}
	output := strings.TrimSpace(os.Getenv("LOAD_RESEARCH_OUTPUT"))
	if output == "" {
		return recorder
	}
	file, err := os.Create(output)
	if err != nil {
		t.Fatalf("create load research output %q: %v", output, err)
	}
	recorder.file = file
	fmt.Fprintln(file, "step\tlane\tsetup_ns\tp50_ns_per_op\tp95_ns_per_op\tp99_ns_per_op\tmax_ns_per_op\tsamples\tops_per_sample\theap_alloc_before\theap_alloc_after\theap_alloc_delta\theap_sys_after\tgoroutines_before\tgoroutines_after\tnotes")
	return recorder
}

func (r *loadResearchRecorder) close() {
	if r.file != nil {
		if err := r.file.Close(); err != nil {
			r.t.Fatalf("close load research output: %v", err)
		}
	}
}

func (r *loadResearchRecorder) record(row loadResearchRow) {
	r.t.Helper()
	r.t.Logf(
		"LOAD_RESEARCH step=%d lane=%s setup=%s p50=%s p95=%s p99=%s max=%s samples=%d ops_per_sample=%d heap_delta=%d heap_after=%d goroutines=%d->%d notes=%q",
		row.Step,
		row.Lane,
		row.Setup,
		row.Stats.P50,
		row.Stats.P95,
		row.Stats.P99,
		row.Stats.Max,
		row.Stats.Samples,
		row.Stats.Ops,
		row.HeapAllocDelta,
		row.After.HeapAlloc,
		row.Before.Goroutines,
		row.After.Goroutines,
		row.Notes,
	)
	if r.file == nil {
		return
	}
	fmt.Fprintf(
		r.file,
		"%d\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n",
		row.Step,
		row.Lane,
		row.Setup.Nanoseconds(),
		row.Stats.P50.Nanoseconds(),
		row.Stats.P95.Nanoseconds(),
		row.Stats.P99.Nanoseconds(),
		row.Stats.Max.Nanoseconds(),
		row.Stats.Samples,
		row.Stats.Ops,
		row.Before.HeapAlloc,
		row.After.HeapAlloc,
		row.HeapAllocDelta,
		row.After.HeapSys,
		row.Before.Goroutines,
		row.After.Goroutines,
		strings.ReplaceAll(row.Notes, "\t", " "),
	)
}

type loadResearchRow struct {
	Step           int
	Lane           string
	Setup          time.Duration
	Stats          loadResearchStats
	Before         loadResearchSnapshot
	After          loadResearchSnapshot
	HeapAllocDelta int64
	Notes          string
}

type loadResearchStats struct {
	Samples int
	Ops     int
	P50     time.Duration
	P95     time.Duration
	P99     time.Duration
	Max     time.Duration
}

type loadResearchSnapshot struct {
	HeapAlloc  uint64
	HeapSys    uint64
	Goroutines int
}

func loadResearchRuntimeSnapshot() loadResearchSnapshot {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return loadResearchSnapshot{
		HeapAlloc:  stats.HeapAlloc,
		HeapSys:    stats.HeapSys,
		Goroutines: runtime.NumGoroutine(),
	}
}

func loadResearchSteps(t testing.TB) []int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOAD_RESEARCH_STEPS"))
	if raw == "" {
		raw = defaultLoadResearchSteps
	}
	steps := make([]int, 0, 8)
	for part := range strings.SplitSeq(raw, ",") {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		step, err := strconv.Atoi(value)
		if err != nil || step <= 0 {
			t.Fatalf("invalid LOAD_RESEARCH_STEPS value %q", value)
		}
		steps = append(steps, step)
	}
	if len(steps) == 0 {
		t.Fatal("LOAD_RESEARCH_STEPS did not contain any positive steps")
	}
	slices.Sort(steps)
	maxStep := loadResearchPositiveIntEnv(t, "LOAD_RESEARCH_MAX_STEP", 1_000_000)
	filtered := steps[:0]
	for _, step := range steps {
		if step <= maxStep {
			filtered = append(filtered, step)
		}
	}
	if len(filtered) == 0 {
		t.Fatalf("all LOAD_RESEARCH_STEPS exceed LOAD_RESEARCH_MAX_STEP=%d", maxStep)
	}
	return filtered
}

func loadResearchPositiveIntEnv(t testing.TB, name string, fallback int) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		t.Fatalf("%s must be a positive integer, got %q", name, raw)
	}
	return value
}

func loadResearchPercentile(samples []int64, quantile float64) int64 {
	if len(samples) == 0 {
		return 0
	}
	index := max(int(float64(len(samples))*quantile+0.999999)-1, 0)
	if index >= len(samples) {
		index = len(samples) - 1
	}
	return samples[index]
}
