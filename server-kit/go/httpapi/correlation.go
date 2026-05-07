package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/tracing"
)

const correlationHeader = "X-Correlation-ID"

// CorrelationMiddleware guarantees every HTTP request has a correlation ID.
func CorrelationMiddleware(next http.Handler) http.Handler {
	if next == nil {
		next = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		md := MetadataFromRequest(r)
		correlationID := md.CorrelationID

		r.Header.Set(correlationHeader, correlationID)
		w.Header().Set(correlationHeader, correlationID)

		ctx := metadata.IntoContext(r.Context(), md)
		ctx = tracing.WithCorrelationID(ctx, correlationID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func CorrelationIDFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if correlationID := strings.TrimSpace(r.Header.Get(correlationHeader)); correlationID != "" {
		return correlationID
	}
	if correlationID := strings.TrimSpace(r.Header.Get("X-Request-ID")); correlationID != "" {
		return correlationID
	}
	return ""
}

func WithCorrelationMetadata(ctx context.Context, correlationID string) context.Context {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return ctx
	}
	md := metadata.FromContext(ctx)
	md.EnsureCorrelation(correlationID)
	ctx = metadata.IntoContext(ctx, md)
	return tracing.WithCorrelationID(ctx, correlationID)
}

func NewCorrelationID() string {
	return metadata.NewCorrelationID()
}

func ContextWithRequestMetadata(r *http.Request) context.Context {
	if r == nil {
		return context.Background()
	}
	md := MetadataFromRequest(r)
	ctx := metadata.IntoContext(r.Context(), md)
	return tracing.WithCorrelationID(ctx, md.CorrelationID)
}

func MetadataFromRequest(r *http.Request) metadata.EnvelopeMetadata {
	md := metadata.New()
	if r != nil {
		md = metadata.FromContext(r.Context())
		md.EnsureCorrelation(CorrelationIDFromRequest(r))
		enrichMetadataFromHeaders(&md, r)
		return md
	}
	md.EnsureCorrelation()
	return md
}

func EnrichMetadataFromRequest(md *metadata.EnvelopeMetadata, r *http.Request) {
	if md == nil {
		return
	}
	if r != nil {
		md.EnsureCorrelation(CorrelationIDFromRequest(r), metadata.FromContext(r.Context()).CorrelationID)
		enrichMetadataFromHeaders(md, r)
		return
	}
	md.EnsureCorrelation()
}

func enrichMetadataFromHeaders(md *metadata.EnvelopeMetadata, r *http.Request) {
	if md == nil || r == nil {
		return
	}
	// Header-derived identity is request metadata only. Authentication middleware
	// must overwrite user, session, device, organization, and role fields with
	// trusted claims before authorization or domain handlers rely on them.
	if requestID := strings.TrimSpace(r.Header.Get("X-Request-ID")); requestID != "" {
		md.RequestID = requestID
	}
	if idempotencyKey := strings.TrimSpace(r.Header.Get("X-Idempotency-Key")); idempotencyKey != "" {
		md.IdempotencyKey = idempotencyKey
	}
	if traceID := strings.TrimSpace(r.Header.Get("X-Trace-ID")); traceID != "" {
		md.TraceID = traceID
	}
	if spanID := strings.TrimSpace(r.Header.Get("X-Span-ID")); spanID != "" {
		md.SpanID = spanID
	}
	if channel := strings.TrimSpace(r.Header.Get("X-Channel")); channel != "" {
		md.Channel = channel
	}
	if locale := strings.TrimSpace(r.Header.Get("Accept-Language")); locale != "" {
		md.Locale = locale
	}
	if md.GlobalContext == nil {
		md.GlobalContext = &metadata.GlobalContext{}
	}
	if userID := strings.TrimSpace(r.Header.Get("X-User-ID")); userID != "" {
		md.GlobalContext.UserID = userID
	}
	if sessionID := strings.TrimSpace(r.Header.Get("X-Session-ID")); sessionID != "" {
		md.GlobalContext.SessionID = sessionID
	}
	if deviceID := strings.TrimSpace(r.Header.Get("X-Device-ID")); deviceID != "" {
		md.GlobalContext.DeviceID = deviceID
	}
	if md.GlobalContext.Source == "" {
		md.GlobalContext.Source = "api"
	}
	if md.GlobalContext.UserAgent == "" {
		md.GlobalContext.UserAgent = r.UserAgent()
	}
	if md.GlobalContext.IPAddress == "" {
		md.GlobalContext.IPAddress = clientIP(r)
	}
}

func clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if forwardedFor := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwardedFor != "" {
		if before, _, ok := strings.Cut(forwardedFor, ","); ok {
			return strings.TrimSpace(before)
		}
		return forwardedFor
	}
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}
	host, _, ok := strings.Cut(r.RemoteAddr, ":")
	if ok {
		return host
	}
	return r.RemoteAddr
}
