package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi/testprotos"
	"google.golang.org/protobuf/proto"
)

func testObject(values map[string]any) extension.Object {
	out := make(extension.Object, len(values))
	for key, value := range values {
		typed, err := extension.FromJSON(value)
		if err == nil {
			out[key] = typed
		}
	}
	return out
}

func TestHandlerExecutionControllerReturnsExplicitConcurrencyError(t *testing.T) {
	controller := NewHandlerExecutionController(ConcurrencyOptions{
		MaxConcurrent:  1,
		AcquireTimeout: 20 * time.Millisecond,
	})

	release := make(chan struct{})
	started := make(chan struct{})
	wrapped := controller.Wrap(func(ctx context.Context, payload extension.Object) (any, error) {
		close(started)
		<-release
		return map[string]any{"ok": true}, nil
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = wrapped(context.Background(), testObject(map[string]any{"id": "first"}))
	}()

	<-started

	_, err := wrapped(context.Background(), testObject(map[string]any{"id": "second"}))
	if !errors.Is(err, ErrConcurrencyLimitReached) {
		t.Fatalf("expected ErrConcurrencyLimitReached, got %v", err)
	}

	close(release)
	<-done
}

func TestTokenBucketLimiterUsesBurstAndRefill(t *testing.T) {
	limiter := newTokenBucketLimiter(ConcurrencyOptions{
		RateLimitRate:   1,
		RateLimitPeriod: 120 * time.Millisecond,
		RateLimitBurst:  1,
	})
	if limiter == nil {
		t.Fatal("expected limiter to be configured")
	}

	if err := limiter.Wait(context.Background()); err != nil {
		t.Fatalf("unexpected error consuming initial token: %v", err)
	}

	shortCtx, cancelShort := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancelShort()
	if err := limiter.Wait(shortCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded before refill, got %v", err)
	}

	time.Sleep(140 * time.Millisecond)

	refillCtx, cancelRefill := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancelRefill()
	if err := limiter.Wait(refillCtx); err != nil {
		t.Fatalf("expected limiter to refill, got %v", err)
	}
}

type advancedTestRegistry struct {
	registered map[string]HandlerFunc
	opts       ConcurrencyOptions
}

func (r *advancedTestRegistry) Register(eventType string, handler HandlerFunc) error {
	r.registered[eventType] = handler
	return nil
}

func (r *advancedTestRegistry) RegisterWithOptions(eventType string, handler HandlerFunc, opts ConcurrencyOptions) error {
	r.opts = opts
	r.registered[eventType] = handler
	return nil
}

func TestRegisterHandlersAndInMemoryRegistry(t *testing.T) {
	reg := &advancedTestRegistry{registered: map[string]HandlerFunc{}}
	err := RegisterHandlers(reg, ServiceHandlers{
		"media:probe:requested": func(context.Context, extension.Object) (any, error) { return "ok", nil },
	}, ConcurrencyOptions{MaxConcurrent: 2})
	if err != nil {
		t.Fatalf("RegisterHandlers() error = %v", err)
	}
	if reg.opts.MaxConcurrent != 2 || len(reg.registered) != 1 {
		t.Fatalf("unexpected registry state: %+v", reg)
	}
	if err := RegisterHandlers(reg, ServiceHandlers{"bad": nil}); err == nil {
		t.Fatal("expected nil handler to fail")
	}
	mem := NewInMemoryRegistry()
	if err := mem.Register("media:probe:requested", func(context.Context, extension.Object) (any, error) { return nil, nil }); err != nil {
		t.Fatalf("memory Register() error = %v", err)
	}
	if _, ok := mem.Resolve("media:probe:requested"); !ok {
		t.Fatal("expected registered handler")
	}
	if err := mem.Register("media:probe:success", func(context.Context, extension.Object) (any, error) { return nil, nil }); err == nil {
		t.Fatal("expected non-requested event to fail")
	}
	if err := mem.Register("media:probe:requested", nil); err == nil {
		t.Fatal("expected nil memory handler to fail")
	}
}

type typedTestRegistry struct {
	advancedTestRegistry
	typed map[string]TypedHandlerRegistration
}

func (r *typedTestRegistry) RegisterTypedWithOptions(eventType string, binding protoapi.Binding, handler TypedHandlerFunc, opts ConcurrencyOptions) error {
	r.opts = opts
	r.typed[eventType] = TypedHandlerRegistration{Binding: binding, Handler: handler}
	return nil
}

func TestRegisterTypedHandlersAndController(t *testing.T) {
	binding := protoapi.Binding{
		Request:  func() proto.Message { return &testprotos.TestRequest{} },
		Response: func() proto.Message { return &testprotos.TestResponse{} },
	}
	reg := &typedTestRegistry{advancedTestRegistry: advancedTestRegistry{registered: map[string]HandlerFunc{}}, typed: map[string]TypedHandlerRegistration{}}
	err := RegisterTypedHandlers(reg, TypedServiceHandlers{
		"media:probe:requested": {Binding: binding, Handler: func(context.Context, proto.Message) (proto.Message, error) {
			return &testprotos.TestResponse{}, nil
		}},
	})
	if err != nil {
		t.Fatalf("RegisterTypedHandlers() error = %v", err)
	}
	if len(reg.typed) != 1 {
		t.Fatalf("typed registrations = %+v", reg.typed)
	}
	wrapped := NewHandlerExecutionController(ConcurrencyOptions{MaxConcurrent: 1}).WrapTyped(func(context.Context, proto.Message) (proto.Message, error) {
		return &testprotos.TestResponse{}, nil
	})
	if _, err := wrapped(context.Background(), &testprotos.TestRequest{}); err != nil {
		t.Fatalf("wrapped typed handler error = %v", err)
	}
	if err := RegisterTypedHandlers(&advancedTestRegistry{registered: map[string]HandlerFunc{}}, nil); err == nil {
		t.Fatal("expected typed unsupported adapter to fail")
	}
	if err := RegisterTypedHandlers(reg, TypedServiceHandlers{"media:probe:requested": {Binding: binding}}); err == nil {
		t.Fatal("expected nil typed handler to fail")
	}
	if err := RegisterTypedHandlers(reg, TypedServiceHandlers{"media:probe:success": {Binding: binding, Handler: func(context.Context, proto.Message) (proto.Message, error) {
		return &testprotos.TestResponse{}, nil
	}}}); err == nil {
		t.Fatal("expected non-requested typed event to fail")
	}
	if err := RegisterTypedHandlers(reg, TypedServiceHandlers{"media:probe:requested": {Handler: func(context.Context, proto.Message) (proto.Message, error) {
		return &testprotos.TestResponse{}, nil
	}}}); err == nil {
		t.Fatal("expected invalid typed binding to fail")
	}
}

func TestBuildServiceHandlers(t *testing.T) {
	handlers := BuildServiceHandlers(" media ", []string{"media:probe:requested"}, nil, func(_ context.Context, eventType string, payload extension.Object) (extension.Object, error) {
		assetID, _ := payload.GetString("asset_id")
		return testObject(map[string]any{"asset_id": assetID}), nil
	})
	res, err := handlers["media:probe:requested"](context.Background(), testObject(map[string]any{"asset_id": "a1"}))
	if err != nil {
		t.Fatalf("handler error = %v", err)
	}
	out := res.(extension.Object)
	domain, _ := out.GetString("domain")
	event, _ := out.GetString("event")
	state, _ := out.GetString("state")
	if domain != "media" || event != "media:probe:requested" || state != "success" {
		t.Fatalf("unexpected response: %+v", out)
	}
	if _, err := handlers["media:probe:requested"](context.Background(), nil); err == nil {
		t.Fatal("expected nil payload to fail")
	}
	if extractEntityID(testObject(map[string]any{"public_id": "p1"})) != "p1" || extractEntityID(testObject(map[string]any{"asset_id": "a1"})) != "a1" {
		t.Fatal("extractEntityID failed")
	}
	defaultHandlers := BuildServiceHandlers("", []string{"service:probe:requested"}, nil, func(context.Context, string, extension.Object) (extension.Object, error) {
		return nil, errors.New("boom")
	})
	if _, err := defaultHandlers["service:probe:requested"](context.Background(), testObject(map[string]any{"id": "1"})); err == nil {
		t.Fatal("expected executor error")
	}
	if extractEntityID(nil) != "" || extractEntityID(testObject(map[string]any{"asset_id": 10})) != "" {
		t.Fatal("expected empty entity id")
	}
}

func TestBuildTypedServiceHandlers(t *testing.T) {
	binding := protoapi.Binding{
		Request:  func() proto.Message { return &testprotos.TestRequest{} },
		Response: func() proto.Message { return &testprotos.TestResponse{} },
	}
	handlers := BuildTypedServiceHandlers(" media ", map[string]protoapi.Binding{
		"media:probe:requested": binding,
	}, nil, func(_ context.Context, eventType string, payload extension.Object) (extension.Object, error) {
		if eventType != "media:probe:requested" {
			t.Fatalf("eventType = %q", eventType)
		}
		workspaceID, _ := payload.GetString("workspace_id")
		if workspaceID != "wrk_1" {
			t.Fatalf("payload = %+v", payload)
		}
		return testObject(map[string]any{"resourceId": "asset_1", "status": "ok"}), nil
	})
	response, err := handlers["media:probe:requested"].Handler(context.Background(), &testprotos.TestRequest{WorkspaceId: "wrk_1"})
	if err != nil {
		t.Fatalf("typed handler error = %v", err)
	}
	if response.(*testprotos.TestResponse).ResourceId != "asset_1" {
		t.Fatalf("response = %+v", response)
	}
	if _, err := handlers["media:probe:requested"].Handler(context.Background(), nil); err == nil {
		t.Fatal("expected nil request error")
	}

	errorHandlers := BuildTypedServiceHandlers("", map[string]protoapi.Binding{"service:probe:requested": binding}, nil, func(context.Context, string, extension.Object) (extension.Object, error) {
		return nil, errors.New("boom")
	})
	if _, err := errorHandlers["service:probe:requested"].Handler(context.Background(), &testprotos.TestRequest{}); err == nil {
		t.Fatal("expected executor error")
	}
	encodeErrorHandlers := BuildTypedServiceHandlers("media", map[string]protoapi.Binding{"media:probe:requested": binding}, nil, func(context.Context, string, extension.Object) (extension.Object, error) {
		return testObject(map[string]any{"resourceId": map[string]any{"bad": true}}), nil
	})
	if _, err := encodeErrorHandlers["media:probe:requested"].Handler(context.Background(), &testprotos.TestRequest{}); err == nil {
		t.Fatal("expected response encode error")
	}
}
