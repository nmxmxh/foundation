package registry

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/bootstrap"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi"
	testprotos "github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi/testprotos"
	"google.golang.org/protobuf/proto"
)

func untypedHandler(marker string) bootstrap.HandlerFunc {
	return func(context.Context, extension.Object) (any, error) {
		return extension.Object{"served_by": extension.String(marker)}, nil
	}
}

func typedBinding() protoapi.Binding {
	return protoapi.Binding{
		Request:  func() proto.Message { return &testprotos.TestRequest{} },
		Response: func() proto.Message { return &testprotos.TestResponse{} },
	}
}

func typedHandler() bootstrap.TypedHandlerFunc {
	return func(context.Context, proto.Message) (proto.Message, error) {
		return &testprotos.TestResponse{}, nil
	}
}

// Two claims on one event type must be reported, not resolved by whichever call
// happened to run second. The displaced handler used to still compile and still
// look wired while never being called.
func TestDuplicateRegistrationIsRejected(t *testing.T) {
	registry := New(nil, nil, nil)
	const eventType = "media:process_asset:v1:requested"

	if err := registry.Register(eventType, untypedHandler("first")); err != nil {
		t.Fatalf("first registration: %v", err)
	}

	err := registry.Register(eventType, untypedHandler("second"))
	if err == nil {
		t.Fatal("second registration succeeded; it must report a conflict")
	}
	if !errors.Is(err, ErrDuplicateEventType) {
		t.Fatalf("error = %v, want ErrDuplicateEventType", err)
	}
	if !strings.Contains(err.Error(), eventType) {
		t.Errorf("error %q should name the conflicting event type", err)
	}
}

// The first claim keeps the slot. Order still decides who wins, but the loser is
// now reported rather than silently discarded, and the winner is the one that
// asked first rather than last.
func TestDuplicateRegistrationKeepsTheFirstHandler(t *testing.T) {
	registry := New(nil, nil, nil)
	const eventType = "media:process_asset:v1:requested"

	if err := registry.Register(eventType, untypedHandler("first")); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	_ = registry.Register(eventType, untypedHandler("second"))

	result, ok, err := registry.DispatchInput(context.Background(), eventType, DispatchInput{
		Payload:         extension.Object{},
		PayloadEncoding: protoapi.PayloadEncodingJSON,
	})
	if err != nil || !ok {
		t.Fatalf("dispatch: ok=%v err=%v", ok, err)
	}
	served, _ := result.Payload["served_by"].StringValue()
	if served != "first" {
		t.Errorf("dispatch served %q, want the first registration", served)
	}
}

// The exact production failure: a typed handler and an untyped handler claiming
// one event name. Previously the later registration won by map assignment, which
// is how a ranked read path lost to an unranked one.
func TestTypedAndUntypedCannotClaimTheSameEvent(t *testing.T) {
	t.Run("untyped first", func(t *testing.T) {
		registry := New(nil, nil, nil)
		const eventType = "media:process_asset:v1:requested"

		if err := registry.Register(eventType, untypedHandler("untyped")); err != nil {
			t.Fatalf("register untyped: %v", err)
		}
		err := registry.RegisterTypedWithOptions(eventType, typedBinding(), typedHandler(), bootstrap.ConcurrencyOptions{})
		if !errors.Is(err, ErrDuplicateEventType) {
			t.Fatalf("typed registration over untyped = %v, want ErrDuplicateEventType", err)
		}
		if !strings.Contains(err.Error(), "untyped") {
			t.Errorf("error %q should name what already holds the slot", err)
		}
	})

	t.Run("typed first", func(t *testing.T) {
		registry := New(nil, nil, nil)
		const eventType = "media:process_asset:v1:requested"

		if err := registry.RegisterTypedWithOptions(eventType, typedBinding(), typedHandler(), bootstrap.ConcurrencyOptions{}); err != nil {
			t.Fatalf("register typed: %v", err)
		}
		err := registry.Register(eventType, untypedHandler("untyped"))
		if !errors.Is(err, ErrDuplicateEventType) {
			t.Fatalf("untyped registration over typed = %v, want ErrDuplicateEventType", err)
		}
		if !strings.Contains(err.Error(), "typed") {
			t.Errorf("error %q should name what already holds the slot", err)
		}
	})
}

// A rejected duplicate must not corrupt the slot it failed to take.
func TestRejectedDuplicateLeavesRegistryUsable(t *testing.T) {
	registry := New(nil, nil, nil)
	const eventType = "media:process_asset:v1:requested"

	if err := registry.Register(eventType, untypedHandler("first")); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	_ = registry.RegisterTypedWithOptions(eventType, typedBinding(), typedHandler(), bootstrap.ConcurrencyOptions{})

	types := registry.RegisteredEventTypes()
	if len(types) != 1 || types[0] != eventType {
		t.Fatalf("registered types = %v, want exactly [%s]", types, eventType)
	}
	if _, ok, err := registry.DispatchInput(context.Background(), eventType, DispatchInput{
		Payload:         extension.Object{},
		PayloadEncoding: protoapi.PayloadEncodingJSON,
	}); err != nil || !ok {
		t.Fatalf("dispatch after rejected duplicate: ok=%v err=%v", ok, err)
	}
}

// An advertised route with no handler answers 404 on a URL the server published
// in its own catalogue and OpenAPI document.
func TestVerifyRoutesReportsUnhandledRoutes(t *testing.T) {
	registry := New(nil, nil, nil)
	if err := registry.Register("media:process_asset:v1:requested", untypedHandler("ok")); err != nil {
		t.Fatalf("register: %v", err)
	}

	routes := []HTTPRoute{
		{Method: "POST", Path: "/v1/media/process-asset", EventType: "media:process_asset:v1:requested"},
		{Method: "GET", Path: "/v1/media/list", EventType: "media:list:v1:requested"},
		{Method: "GET", Path: "/v1/billing/profile", EventType: "billing:profile:get:v1:requested"},
	}

	err := registry.VerifyRoutes(routes)
	if err == nil {
		t.Fatal("VerifyRoutes passed with two unhandled routes")
	}
	if !errors.Is(err, ErrRouteWithoutHandler) {
		t.Fatalf("error = %v, want ErrRouteWithoutHandler", err)
	}
	for _, want := range []string{"media:list:v1:requested", "billing:profile:get:v1:requested"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should name the unhandled event %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "media:process_asset:v1:requested") {
		t.Error("error should not name the route that does have a handler")
	}
}

func TestVerifyRoutesPassesWhenFullyCovered(t *testing.T) {
	registry := New(nil, nil, nil)
	if err := registry.Register("media:process_asset:v1:requested", untypedHandler("ok")); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := registry.RegisterTypedWithOptions("media:list:v1:requested", typedBinding(), typedHandler(), bootstrap.ConcurrencyOptions{}); err != nil {
		t.Fatalf("register typed: %v", err)
	}

	routes := []HTTPRoute{
		{Method: "POST", Path: "/v1/media/process-asset", EventType: "media:process_asset:v1:requested"},
		{Method: "GET", Path: "/v1/media/list", EventType: "media:list:v1:requested"},
		// Duplicated on purpose: two HTTP shapes may share one event type.
		{Method: "POST", Path: "/v1/media/list", EventType: "media:list:v1:requested"},
		// A route serving its own http.HandlerFunc has no event to reconcile.
		{Method: "GET", Path: "/healthz"},
	}

	if err := registry.VerifyRoutes(routes); err != nil {
		t.Fatalf("VerifyRoutes = %v, want nil", err)
	}
}

// A handler with no route is normal: events driven by the worker lane or the bus
// are never exposed over HTTP, and flagging them would make the check unusable.
func TestVerifyRoutesIgnoresHandlersWithoutRoutes(t *testing.T) {
	registry := New(nil, nil, nil)
	if err := registry.Register("media:process_asset:v1:requested", untypedHandler("ok")); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := registry.Register("media:reindex:v1:requested", untypedHandler("worker only")); err != nil {
		t.Fatalf("register: %v", err)
	}

	routes := []HTTPRoute{
		{Method: "POST", Path: "/v1/media/process-asset", EventType: "media:process_asset:v1:requested"},
	}
	if err := registry.VerifyRoutes(routes); err != nil {
		t.Fatalf("VerifyRoutes = %v, want nil for a worker-only handler", err)
	}
}

func TestVerifyRoutesAcceptsAnEmptyCatalogue(t *testing.T) {
	registry := New(nil, nil, nil)
	if err := registry.VerifyRoutes(nil); err != nil {
		t.Fatalf("VerifyRoutes(nil) = %v, want nil", err)
	}
}
