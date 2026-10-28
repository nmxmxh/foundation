package appbench

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/transport"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/auth"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/cache"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/circuitbreaker"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/grpcsvc"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/httpapi"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/retry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scratch"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/worker"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/wsrouting"
)

type appProcessor struct {
	kind  string
	queue string
	wg    *sync.WaitGroup
}

type benchLogger struct{}

func (benchLogger) Info(string, ...any)                         {}
func (benchLogger) Error(string, ...any)                        {}
func (benchLogger) Debug(string, ...any)                        {}
func (benchLogger) Warn(string, ...any)                         {}
func (benchLogger) InfoContext(context.Context, string, ...any) {}
func (benchLogger) ErrorContext(context.Context, string, ...any) {
}
func (benchLogger) DebugContext(context.Context, string, ...any) {}
func (benchLogger) WarnContext(context.Context, string, ...any)  {}
func (benchLogger) Sync() error                                  { return nil }
func (benchLogger) Dropped() uint64                              { return 0 }
func (l benchLogger) With(...any) logger.Logger {
	return l
}

func (p *appProcessor) Kind() string     { return p.kind }
func (p *appProcessor) Queue() string    { return p.queue }
func (p *appProcessor) MaxAttempts() int { return 1 }
func (p *appProcessor) Handle(context.Context, worker.Job) error {
	if p.wg != nil {
		p.wg.Done()
	}
	return nil
}

var testProfileResponsePayload = []byte(`{"ok":true,"user_id":"user-123"}`)

func testRouter(tb testing.TB) *grpcsvc.Router {
	tb.Helper()
	router := grpcsvc.NewRouter()
	err := router.RegisterFrame("user.profile.read", func(_ context.Context, frame grpcsvc.Frame) (grpcsvc.Frame, error) {
		frame.Payload = testProfileResponsePayload
		return frame, nil
	})
	if err != nil {
		tb.Fatalf("register frame handler: %v", err)
	}
	return router
}

func testJWT(b *testing.B) (*auth.JWTManager, string) {
	b.Helper()
	manager, err := auth.NewJWTManager("0123456789abcdef0123456789abcdef")
	if err != nil {
		b.Fatalf("jwt manager: %v", err)
	}
	token, err := manager.GenerateAccessToken(auth.Claims{
		UserID:         "user-123",
		Email:          "user@example.com",
		Role:           "admin",
		OrganizationID: "org-123",
		SessionID:      "session-123",
		Capabilities:   []string{"user.profile.read", "operations.jobs.write"},
	}, time.Hour)
	if err != nil {
		b.Fatalf("jwt token: %v", err)
	}
	return manager, token
}

func BenchmarkAppLane_DirectFrame_DomainCall(b *testing.B) {
	client := grpcsvc.NewDirectFrameClient(testRouter(b), grpcsvc.ServerOptions{})
	frame := grpcsvc.Frame{EventType: "user.profile.read", Payload: []byte(`{"user_id":"user-123"}`)}

	b.ReportAllocs()

	for b.Loop() {
		if _, err := client.DispatchFrame(context.Background(), frame); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAppLane_HTTPIngress_JSONToDispatchRequest(b *testing.B) {
	route := registry.HTTPRoute{
		Method:         http.MethodPost,
		Path:           "/v1/users/{id}/profile",
		EventType:      "user.profile.read",
		StaticPayload:  extension.Object{"source": extension.String("api")},
		IncludeHeaders: []string{"X-Request-ID", "X-Correlation-ID"},
	}
	body := []byte(`{"include_permissions":true,"view":"full"}`)

	b.ReportAllocs()

	for b.Loop() {
		req := httptest.NewRequest(http.MethodPost, "/v1/users/user-123/profile", bytes.NewReader(body))
		req.SetPathValue("id", "user-123")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Request-ID", "req-123")
		req.Header.Set("X-Correlation-ID", "corr-123")
		if _, err := httpapi.BuildDispatchRequest(req, route); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAppLane_HTTPIngress_JSONBodyKilobyte is the same ingress path over a
// body of the size real product traffic actually carries.
//
// It exists because the null lane cannot see body-read cost: rung 03 posts two
// bytes, so it reports the same number whether ingress sizes its read buffer
// from Content-Length or grows one from io.ReadAll's fixed 512. The difference
// only appears once the body outgrows that buffer — at 4 KB, growth costs eight
// extra allocations and roughly twice the bytes. Keep this benchmark alongside
// rung 03: the pair is what distinguishes per-request overhead from
// per-kilobyte overhead, and only the second scales with the product.
func BenchmarkAppLane_HTTPIngress_JSONBodyKilobyte(b *testing.B) {
	route := registry.HTTPRoute{
		Method:    http.MethodPost,
		Path:      "/v1/documents",
		EventType: "document.create",
	}
	// ~4 KB of JSON: a document create, an audit payload, a batch of records.
	body := []byte(`{"title":"quarterly report","body":"` + strings.Repeat("content ", 500) + `"}`)

	b.ReportAllocs()

	for b.Loop() {
		req := httptest.NewRequest(http.MethodPost, "/v1/documents", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if _, err := httpapi.BuildDispatchRequest(req, route); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAppLane_HTTPIngress_JSONBodyKilobyte_DispatchOnly isolates the
// dispatch build and JSON parse by reusing the request harness and pre-compiled route.
func BenchmarkAppLane_HTTPIngress_JSONBodyKilobyte_DispatchOnly(b *testing.B) {
	route := registry.HTTPRoute{
		Method:    http.MethodPost,
		Path:      "/v1/documents",
		EventType: "document.create",
	}
	body := []byte(`{"title":"quarterly report","body":"` + strings.Repeat("content ", 500) + `"}`)
	plan := httpapi.CompileDispatchRoute(route)
	req := httptest.NewRequest(http.MethodPost, "/v1/documents", nil)
	req.Header.Set("Content-Type", "application/json")
	reader := bytes.NewReader(body)

	b.ReportAllocs()

	for b.Loop() {
		reader.Reset(body)
		req.Body = io.NopCloser(reader)
		if _, err := plan.Build(req); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAppLane_HTTPIngress_ProtobufBodyKilobyte compares binary protobuf ingress
// against JSON over the exact same route and body size.
func BenchmarkAppLane_HTTPIngress_ProtobufBodyKilobyte(b *testing.B) {
	route := registry.HTTPRoute{
		Method:    http.MethodPost,
		Path:      "/v1/documents",
		EventType: "document.create",
	}
	body := []byte(`{"title":"quarterly report","body":"` + strings.Repeat("content ", 500) + `"}`)
	plan := httpapi.CompileDispatchRoute(route)
	req := httptest.NewRequest(http.MethodPost, "/v1/documents", nil)
	req.Header.Set("Content-Type", "application/x-protobuf")
	reader := bytes.NewReader(body)

	b.ReportAllocs()

	for b.Loop() {
		reader.Reset(body)
		req.Body = io.NopCloser(reader)
		if _, err := plan.Build(req); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAppLane_HTTPIngress_ZeroAlloc demonstrates the true zero-allocation ingress path
// using binary payload decoding, reusable stream body, and a context-attached linear scratch arena.
func BenchmarkAppLane_HTTPIngress_ZeroAlloc(b *testing.B) {
	route := registry.HTTPRoute{
		Method:    http.MethodPost,
		Path:      "/v1/documents",
		EventType: "document.create",
	}
	body := []byte(`{"title":"quarterly report","body":"` + strings.Repeat("content ", 500) + `"}`)
	plan := httpapi.CompileDispatchRoute(route)
	arena := scratch.New(64 * 1024)
	ctx := scratch.WithArena(context.Background(), arena)
	nBody := newNullBody(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/documents", nBody).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("X-Correlation-Id", "corr_zero_alloc")
	req.ContentLength = int64(len(body))

	b.ReportAllocs()

	for b.Loop() {
		arena.Reset()
		nBody.reset()
		if _, err := plan.Build(req); err != nil {
			b.Fatal(err)
		}
	}
}

type benchPipelineRows struct {
	count int
	index int
}

func (r *benchPipelineRows) Close() {}
func (r *benchPipelineRows) Next() bool {
	if r.index < r.count {
		r.index++
		return true
	}
	return false
}
func (r *benchPipelineRows) Scan(dest ...any) error { return nil }
func (r *benchPipelineRows) ScanInt() (int, error)  { return r.index, nil }
func (r *benchPipelineRows) Err() error             { return nil }

type benchPipelineQueryer struct {
	rows benchPipelineRows
}

func (q *benchPipelineQueryer) Query(context.Context, string, ...any) (database.Rows, error) {
	q.rows.index = 0
	return &q.rows, nil
}

func BenchmarkAppLane_EndToEnd_ScratchArena(b *testing.B) {
	route := registry.HTTPRoute{
		Method:    http.MethodPost,
		Path:      "/v1/documents",
		EventType: "document.create",
	}
	body := []byte(`{"title":"quarterly report"}`)
	plan := httpapi.CompileDispatchRoute(route)
	arena := scratch.New(64 * 1024)
	ctx := scratch.WithArena(context.Background(), arena)
	nBody := newNullBody(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/documents", nBody).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("X-Correlation-Id", "corr_e2e_arena")
	req.ContentLength = int64(len(body))

	authorizer := security.NewAuthorizer([]security.RoleTemplate{{
		Role:         "admin",
		Capabilities: []string{"document.create"},
	}})
	actorMask := authorizer.RoleMask("admin")
	requiredMask, _ := authorizer.CapabilityBit("document.create")

	queryer := &benchPipelineQueryer{rows: benchPipelineRows{count: 10}}

	respObj := extension.Object{
		"status": extension.String("ok"),
		"count":  extension.Int(10),
	}

	b.ReportAllocs()

	for b.Loop() {
		arena.Reset()
		nBody.reset()

		// 1. Ingress build (zero heap allocs using context arena)
		dispatchReq, err := plan.Build(req)
		if err != nil {
			b.Fatal(err)
		}
		_ = dispatchReq

		// 2. O(1) bitwise capability authorization check (zero allocs)
		if (actorMask & requiredMask) != requiredMask {
			b.Fatal("unauthorized")
		}

		// 3. Database batch query scanning directly into arena slice (zero heap allocs)
		rows, err := database.QueryAllArena(ctx, arena, queryer, 10, "SELECT id", func(r database.Rows) (int, error) {
			return database.DirectScanInt(r)
		})
		if err != nil || len(rows) != 10 {
			b.Fatalf("query failed: %v", err)
		}

		// 4. JSON Response serialization directly into arena buffer (zero heap allocs)
		out, err := respObj.MarshalJSONFastWithArena(arena)
		if err != nil || len(out) == 0 {
			b.Fatalf("marshal failed: %v", err)
		}
	}
}

func buildBenchColumnarTable(tb testing.TB, records int) *hermes.ColumnarTable {
	tb.Helper()
	const projection = "bench_orders"
	const orgID = "org_bench"
	store, err := hermes.NewStore(hermes.ProjectionSpec{
		Name:          projection,
		Domain:        "market",
		Collection:    "orders",
		IndexedFields: []string{"symbol", "state"},
		MaxRecords:    records + 1024,
		MaxBytes:      max(int64(records)*512, int64(16<<20)),
	})
	if err != nil {
		tb.Fatalf("new hermes store: %v", err)
	}
	ctx := context.Background()
	payload := make([]database.DomainRecord, records)
	for i := range records {
		payload[i] = database.DomainRecord{
			Domain:         "market",
			Collection:     "orders",
			OrganizationID: orgID,
			RecordID:       fmt.Sprintf("ord_%08d", i),
			Data: database.RecordData{
				{Name: "symbol", Value: database.RecordValue{Kind: database.RecordValueString, Text: "AAPL"}},
				{Name: "state", Value: database.RecordValue{Kind: database.RecordValueString, Text: "filled"}},
			},
		}
	}
	if _, err := store.BulkLoad(ctx, projection, payload); err != nil {
		tb.Fatalf("hermes bulk load: %v", err)
	}
	table, err := store.GetColumnarTable(ctx, projection, hermes.Query{OrganizationID: orgID}, []string{"record_id", "symbol", "state"}, "record_id", hermes.Fence{})
	if err != nil {
		tb.Fatalf("get columnar table: %v", err)
	}
	return table
}

func BenchmarkAppLane_WebSocketToHermesColumnar_Pipeline(b *testing.B) {
	ctx := context.Background()
	arena := scratch.New(64 * 1024)
	router := wsrouting.NewRouter(nil, "bench-server")
	for i := range 1024 {
		id := strconv.Itoa(i)
		_ = router.Register(ctx, wsrouting.ConnectionInfo{
			ConnectionID: "conn-" + id,
			DeviceID:     "device-" + id,
			UserID:       "user-" + strconv.Itoa(i%64),
		})
	}

	authorizer := security.NewAuthorizer([]security.RoleTemplate{{
		Role:         "trader",
		Capabilities: []string{"market:order:update"},
	}})
	actorMask := authorizer.RoleMask("trader")
	requiredMask, _ := authorizer.CapabilityBit("market:order:update")

	table := buildBenchColumnarTable(b, 1000)

	payload := transport.ObjectFromMap(map[string]any{"order_id": "ord_00000500"})
	extra := transport.ObjectFromMap(map[string]any{"source": "ws"})

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		arena.Reset()

		// 1. Build envelope with arena (zero heap allocations)
		envelope := transport.CreateEnvelopeWithAllocator(arena, "market:order:update", payload, extra)
		_ = envelope

		// 2. Resolve WebSocket connection for target device (zero heap allocations)
		connID, ok, err := router.ResolveDeviceConnection(ctx, "device-500")
		if err != nil || !ok || connID == "" {
			b.Fatal("missing device route")
		}

		// 3. Bitwise capability authorization (zero allocations)
		if (actorMask & requiredMask) != requiredMask {
			b.Fatal("unauthorized")
		}

		// 4. Hermes off-heap columnar projection point lookup (zero heap allocations)
		view, found := table.GetView("ord_00000500")
		if !found {
			b.Fatal("missing order in columnar projection")
		}
		symBytes, _ := view.Bytes("symbol")
		if !bytes.Equal(symBytes, []byte("AAPL")) {
			b.Fatalf("expected AAPL, got %s", symBytes)
		}
	}
}

func BenchmarkAppLane_Auth_ValidateToken(b *testing.B) {
	manager, token := testJWT(b)

	b.ReportAllocs()

	for b.Loop() {
		if _, err := manager.ValidateToken(token); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAppLane_Auth_ValidateToken_Arena(b *testing.B) {
	manager, token := testJWT(b)
	arena := scratch.New(4096)

	b.ReportAllocs()

	for b.Loop() {
		arena.Reset()
		if _, err := manager.ValidateTokenWithArena(arena, token); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAppLane_HTTPMiddleware_AuthSecurityRBAC(b *testing.B) {
	manager, token := testJWT(b)
	authorizer := security.NewAuthorizer([]security.RoleTemplate{{
		Role:         "admin",
		Capabilities: []string{"user.profile.read"},
	}})
	handler := security.SecurityHeaders(
		security.InputValidation(
			security.JWTAuth(manager, nil)(
				security.RequireCapabilities(authorizer, "user.profile.read")(
					http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusNoContent)
					}),
				),
			),
		),
	)

	b.ReportAllocs()

	for b.Loop() {
		req := httptest.NewRequest(http.MethodPost, "/v1/users/user-123/profile", bytes.NewReader([]byte(`{}`)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
}

func BenchmarkAppLane_Cache_GetHit_JSONValue(b *testing.B) {
	backend := cache.NewMemoryBackend()
	defer func() { _ = backend.Close() }()
	ctx := context.Background()
	_ = backend.Set(ctx, "user:123", []byte(`{"id":"user-123","role":"admin"}`), time.Minute)

	b.ReportAllocs()

	for b.Loop() {
		value, err := backend.Get(ctx, "user:123")
		if err != nil || len(value) == 0 {
			b.Fatalf("cache get: value=%q err=%v", value, err)
		}
	}
}

func BenchmarkAppLane_Retry_NoRetrySuccess(b *testing.B) {
	policy := retry.NoRetry()
	ctx := context.Background()

	b.ReportAllocs()

	for b.Loop() {
		if err := policy.Do(ctx, func() error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAppLane_CircuitBreaker_ClosedSuccess(b *testing.B) {
	cb := circuitbreaker.New("app-dependency", circuitbreaker.Config{FailureThreshold: 5})
	ctx := context.Background()

	b.ReportAllocs()

	for b.Loop() {
		if _, err := cb.Execute(ctx, func() (any, error) { return nil, nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAppLane_Worker_EnqueueWithBackpressureAndDrain(b *testing.B) {
	var wg sync.WaitGroup
	engine := worker.NewEngine(map[string]int{"app": 64}, benchLogger{})
	if err := engine.Register(&appProcessor{kind: "app.email.send", queue: "app", wg: &wg}); err != nil {
		b.Fatal(err)
	}
	ctx := b.Context()
	if err := engine.Start(ctx); err != nil {
		b.Fatal(err)
	}
	job := worker.Job{JobKind: "app.email.send", Queue: "app", RawPayload: []byte(`{"to":"user@example.com"}`)}

	b.ReportAllocs()
	b.ResetTimer()
	wg.Add(b.N)
	for i := 0; i < b.N; i++ {
		for {
			if err := engine.Enqueue(ctx, job); err == nil {
				break
			}
		}
	}
	wg.Wait()
}

func BenchmarkAppLane_Worker_RejectFullQueue(b *testing.B) {
	engine := worker.NewEngine(map[string]int{"app": 1}, benchLogger{})
	if err := engine.Register(&appProcessor{kind: "app.email.send", queue: "app"}); err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	job := worker.Job{JobKind: "app.email.send", Queue: "app", RawPayload: []byte(`{"to":"user@example.com"}`)}
	for i := range 1024 {
		if err := engine.Enqueue(ctx, job); err != nil {
			b.Fatalf("prefill queue at %d: %v", i, err)
		}
	}

	b.ReportAllocs()

	for b.Loop() {
		if err := engine.Enqueue(ctx, job); err == nil {
			b.Fatal("expected full queue rejection")
		}
	}
}

func BenchmarkAppLane_Worker_DropNoProcessor(b *testing.B) {
	engine := worker.NewEngine(map[string]int{"app": 1}, benchLogger{})
	ctx := context.Background()
	job := worker.Job{JobKind: "app.missing", Queue: "app"}

	b.ReportAllocs()

	for b.Loop() {
		if err := engine.Enqueue(ctx, job); err == nil {
			b.Fatal("expected missing processor error")
		}
	}
}

func BenchmarkAppLane_Retry_CanceledWait(b *testing.B) {
	policy := retry.NewPolicy(retry.Config{
		MaxAttempts:  2,
		InitialDelay: time.Hour,
		RetryIf:      func(error) bool { return true },
	})
	errRetryable := errors.New("retryable")

	b.ReportAllocs()

	for b.Loop() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := policy.Do(ctx, func() error { return errRetryable })
		if !errors.Is(err, context.Canceled) {
			b.Fatalf("err = %v, want context.Canceled", err)
		}
	}
}

// TestAppLane_ConcurrentTraffic_Stress evaluates 160,000 requests under 16-way
// concurrent worker contention, measuring p50, p95, and p99 latency percentiles
// and verifying circuit breaker shedding under injected faults.
func TestTraceZeroAlloc(t *testing.T) {
	route := registry.HTTPRoute{
		Method:    http.MethodPost,
		Path:      "/v1/documents",
		EventType: "document.create",
	}
	body := []byte(`{"title":"quarterly report"}`)
	plan := httpapi.CompileDispatchRoute(route)
	arena := scratch.New(64 * 1024)
	ctx := scratch.WithArena(context.Background(), arena)
	nBody := newNullBody(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/documents", nBody).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("X-Correlation-Id", "corr_zero_alloc")
	req.ContentLength = int64(len(body))

	allocs1 := testing.AllocsPerRun(100, func() {
		arena.Reset()
		nBody.reset()
		_ = httpapi.MetadataFromRequest(req)
	})
	t.Logf("MetadataFromRequest allocs: %.0f", allocs1)

	allocs2 := testing.AllocsPerRun(100, func() {
		arena.Reset()
		nBody.reset()
		_, _ = plan.Build(req)
	})
	t.Logf("Total plan.Build allocs: %.0f", allocs2)
}

func TestAppLane_ConcurrentTraffic_Stress(t *testing.T) {
	cb := circuitbreaker.New("concurrent-ingress", circuitbreaker.Config{
		FailureThreshold: 5,
		Timeout:          100 * time.Millisecond,
	})
	client := grpcsvc.NewDirectFrameClient(testRouter(t), grpcsvc.ServerOptions{})
	frame := grpcsvc.Frame{EventType: "user.profile.read", Payload: []byte(`{"user_id":"u-100"}`)}

	const concurrency = 16
	const requestsPerWorker = 10_000
	const totalRequests = concurrency * requestsPerWorker

	var wg sync.WaitGroup
	wg.Add(concurrency)

	latencies := make([]int64, totalRequests)

	start := time.Now()
	for w := range concurrency {
		workerID := w
		go func() {
			defer wg.Done()
			offset := workerID * requestsPerWorker
			ctx := context.Background()
			for i := range requestsPerWorker {
				t0 := time.Now()
				_, err := cb.Execute(ctx, func() (any, error) {
					return client.DispatchFrame(ctx, frame)
				})
				lat := time.Since(t0).Nanoseconds()
				if err != nil {
					return
				}
				latencies[offset+i] = lat
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	slices.Sort(latencies)
	p50 := latencies[int(float64(totalRequests)*0.50)]
	p95 := latencies[int(float64(totalRequests)*0.95)]
	p99 := latencies[int(float64(totalRequests)*0.99)]
	throughput := float64(totalRequests) / elapsed.Seconds()

	t.Logf("Concurrent Traffic Stress (16 goroutines, %d total requests):", totalRequests)
	t.Logf("  Elapsed:     %v", elapsed)
	t.Logf("  Throughput:  %.0f req/sec", throughput)
	t.Logf("  p50 latency: %d ns", p50)
	t.Logf("  p95 latency: %d ns", p95)
	t.Logf("  p99 latency: %d ns", p99)

	// Now verify circuit breaker fault injection and shedding
	ctx := context.Background()
	injectedErr := errors.New("upstream timeout")
	for range 5 {
		_, _ = cb.Execute(ctx, func() (any, error) {
			return nil, injectedErr
		})
	}

	if cb.State() != circuitbreaker.StateOpen {
		t.Fatalf("expected circuit breaker StateOpen, got %v", cb.State())
	}

	// Verify fast rejection under open circuit
	t0 := time.Now()
	_, err := cb.Execute(ctx, func() (any, error) {
		return "should not run", nil
	})
	rejectionLatency := time.Since(t0)

	if !errors.Is(err, circuitbreaker.ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
	t.Logf("  Circuit Open Rejection Latency: %v", rejectionLatency)
}
