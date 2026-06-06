package appbench

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/auth"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/cache"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/circuitbreaker"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/grpcsvc"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/httpapi"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/retry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/worker"
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

func testRouter(b *testing.B) *grpcsvc.Router {
	b.Helper()
	router := grpcsvc.NewRouter()
	err := router.RegisterFrame("user.profile.read", func(_ context.Context, frame grpcsvc.Frame) (grpcsvc.Frame, error) {
		frame.Payload = []byte(`{"ok":true,"user_id":"user-123"}`)
		return frame, nil
	})
	if err != nil {
		b.Fatalf("register frame handler: %v", err)
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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

func BenchmarkAppLane_Auth_ValidateToken(b *testing.B) {
	manager, token := testJWT(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := manager.ValidateToken(token); err != nil {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := policy.Do(ctx, func() error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAppLane_CircuitBreaker_ClosedSuccess(b *testing.B) {
	cb := circuitbreaker.New("app-dependency", circuitbreaker.Config{FailureThreshold: 5})
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := policy.Do(ctx, func() error { return errRetryable })
		if !errors.Is(err, context.Canceled) {
			b.Fatalf("err = %v, want context.Canceled", err)
		}
	}
}
