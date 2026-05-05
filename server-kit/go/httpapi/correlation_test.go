package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
)

func TestCorrelationMiddlewareGeneratesAndPropagatesID(t *testing.T) {
	var got metadata.EnvelopeMetadata
	handler := CorrelationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = metadata.FromContext(r.Context())
		if r.Header.Get("X-Correlation-ID") == "" {
			t.Fatal("expected request correlation header")
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got.CorrelationID == "" {
		t.Fatal("expected context correlation_id")
	}
	if got.RequestID != got.CorrelationID {
		t.Fatalf("request_id = %q, want correlation_id %q", got.RequestID, got.CorrelationID)
	}
	if rec.Header().Get("X-Correlation-ID") != got.CorrelationID {
		t.Fatalf("response correlation header = %q, want %q", rec.Header().Get("X-Correlation-ID"), got.CorrelationID)
	}
}

func TestCorrelationMiddlewarePreservesIncomingID(t *testing.T) {
	const incoming = "corr_incoming"
	handler := CorrelationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got := metadata.FromContext(r.Context())
		if got.CorrelationID != incoming {
			t.Fatalf("correlation_id = %q, want %q", got.CorrelationID, incoming)
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.Header.Set("X-Correlation-ID", incoming)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Correlation-ID") != incoming {
		t.Fatalf("response correlation header = %q, want %q", rec.Header().Get("X-Correlation-ID"), incoming)
	}
}

func TestMetadataFromRequestUsesRequestIDFallback(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.Header.Set("X-Request-ID", "req_1")
	req.Header.Set("X-User-ID", "user_1")

	md := MetadataFromRequest(req)
	if md.CorrelationID != "req_1" || md.RequestID != "req_1" {
		t.Fatalf("unexpected correlation/request id: %#v", md)
	}
	if md.GlobalContext == nil || md.GlobalContext.UserID != "user_1" {
		t.Fatalf("expected global context from request headers: %#v", md.GlobalContext)
	}
}

func TestMetadataFromRequestPreservesCommunicationHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/test", nil)
	req.Header.Set("X-Correlation-ID", "corr_keep")
	req.Header.Set("X-Request-ID", "req_keep")
	req.Header.Set("X-Channel", "worker.operations")
	req.Header.Set("X-Idempotency-Key", "idem_keep")
	req.Header.Set("X-Trace-ID", "trace_keep")
	req.Header.Set("X-Span-ID", "span_keep")
	req.Header.Set("Accept-Language", "en-NG")
	req.Header.Set("X-Session-ID", "session_keep")
	req.Header.Set("X-Device-ID", "device_keep")
	req.Header.Set("X-Forwarded-For", "203.0.113.10, 10.0.0.1")
	req.Header.Set("User-Agent", "foundation-test")

	md := MetadataFromRequest(req)
	if md.CorrelationID != "corr_keep" || md.RequestID != "req_keep" || md.Channel != "worker.operations" || md.Locale != "en-NG" {
		t.Fatalf("metadata identity headers were not preserved: %#v", md)
	}
	if md.IdempotencyKey != "idem_keep" || md.TraceID != "trace_keep" || md.SpanID != "span_keep" {
		t.Fatalf("metadata tracing/idempotency headers were not preserved: %#v", md)
	}
	if md.GlobalContext == nil {
		t.Fatal("expected global context")
	}
	if md.GlobalContext.SessionID != "session_keep" || md.GlobalContext.DeviceID != "device_keep" {
		t.Fatalf("global context identity was not preserved: %#v", md.GlobalContext)
	}
	if md.GlobalContext.IPAddress != "203.0.113.10" || md.GlobalContext.UserAgent != "foundation-test" || md.GlobalContext.Source != "api" {
		t.Fatalf("request communication context was not preserved: %#v", md.GlobalContext)
	}
}
