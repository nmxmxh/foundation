package tracing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/attribute"
)

func TestProviderStdoutStartAndShutdown(t *testing.T) {
	provider, err := NewProvider(Config{
		ServiceName:    "foundation-test",
		ServiceVersion: "v1",
		Environment:    "test",
		SampleRate:     -1,
		Attributes:     map[string]string{"component": "tracing"},
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	ctx, span := provider.Start(context.Background(), "operation")
	SetAttribute(ctx, "string", "value")
	SetAttribute(ctx, "int", 1)
	SetAttribute(ctx, "int64", int64(2))
	SetAttribute(ctx, "float", 1.5)
	SetAttribute(ctx, "bool", true)
	SetAttribute(ctx, "slice", []string{"a", "b"})
	SetAttribute(ctx, "other", struct{ Name string }{"x"})
	AddEvent(ctx, "checkpoint", attribute.String("phase", "test"))
	SetError(ctx, errors.New("boom"))
	if SpanFromContext(ctx) == nil || provider.Tracer() == nil {
		t.Fatalf("expected span and tracer")
	}
	span.End()
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestCorrelationAndSpanHelpers(t *testing.T) {
	ctx, span := Start(context.Background(), "root")
	defer span.End()
	ctx = ContextWithSpan(ctx, span)
	ctx = WithCorrelationID(ctx, "corr_1")
	if got := CorrelationIDFromContext(ctx); got != "corr_1" {
		t.Fatalf("CorrelationIDFromContext() = %q", got)
	}
	if CorrelationIDFromContext(context.Background()) != "" {
		t.Fatalf("empty context should not have correlation id")
	}
	_ = TraceIDFromContext(ctx)
	_ = SpanIDFromContext(ctx)

	_, clientSpan := StartClientSpan(ctx, "client", attribute.String("target", "partner"))
	clientSpan.End()
	_, internalSpan := StartInternalSpan(ctx, "internal")
	internalSpan.End()
	err := Timed(ctx, "timed", func(context.Context) error { return errors.New("timed-error") })
	if err == nil {
		t.Fatalf("Timed should return function error")
	}
}

func TestMiddlewareAndInjectContext(t *testing.T) {
	middleware := Middleware("api")
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if CorrelationIDFromContext(r.Context()) != "corr_http" {
			t.Fatalf("missing correlation in request context")
		}
		w.WriteHeader(http.StatusTeapot)
	}))
	req := httptest.NewRequest(http.MethodPost, "https://api.example.com/v1/test", nil)
	req.Header.Set("X-Correlation-ID", "corr_http")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d", rec.Code)
	}

	outReq := httptest.NewRequest(http.MethodGet, "https://partner.example.com", nil)
	ctx, span := Start(context.Background(), "inject")
	defer span.End()
	InjectContext(ctx, outReq)
}
