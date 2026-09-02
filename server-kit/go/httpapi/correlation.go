package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/tracing"
)

// Header names are spelled in canonical MIME form ("X-Request-Id", not
// "X-Request-ID"). http.Header.Get and Set canonicalize their argument, and
// textproto allocates a new string whenever the key is not already canonical
// and not in its common-header table. Ingress reads a dozen of these per
// request, so the spelling is a per-request allocation, not a style choice.
// Lookups stay case-insensitive: callers may still Set any casing they like.
const (
	correlationHeader    = "X-Correlation-Id"
	requestIDHeader      = "X-Request-Id"
	idempotencyKeyHeader = "X-Idempotency-Key"
	traceIDHeader        = "X-Trace-Id"
	spanIDHeader         = "X-Span-Id"
	channelHeader        = "X-Channel"
	localeHeader         = "Accept-Language"
	userIDHeader         = "X-User-Id"
	sessionIDHeader      = "X-Session-Id"
	deviceIDHeader       = "X-Device-Id"
	forwardedForHeader   = "X-Forwarded-For"
	realIPHeader         = "X-Real-Ip"
)

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
	if correlationID := strings.TrimSpace(r.Header.Get(requestIDHeader)); correlationID != "" {
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
	if requestID := strings.TrimSpace(r.Header.Get(requestIDHeader)); requestID != "" {
		md.RequestID = requestID
	}
	if idempotencyKey := strings.TrimSpace(r.Header.Get(idempotencyKeyHeader)); idempotencyKey != "" {
		md.IdempotencyKey = idempotencyKey
	} else if strings.TrimSpace(md.IdempotencyKey) == "" && !metadata.RequiresIdempotency(r.Method) {
		// Reads are naturally idempotent, so Foundation mints a deterministic
		// server-side key for them rather than requiring the caller to supply
		// one. This is what keeps a plain GET (a curl, an integration test, a
		// partner) from having to fabricate an idempotency key to satisfy the
		// command-metadata contract — only mutations require a client key.
		md.IdempotencyKey = deriveReadIdempotencyKey(md.CorrelationID)
	}
	if traceID := strings.TrimSpace(r.Header.Get(traceIDHeader)); traceID != "" {
		md.TraceID = traceID
	}
	if spanID := strings.TrimSpace(r.Header.Get(spanIDHeader)); spanID != "" {
		md.SpanID = spanID
	}
	if channel := strings.TrimSpace(r.Header.Get(channelHeader)); channel != "" {
		md.Channel = channel
	}
	if locale := strings.TrimSpace(r.Header.Get(localeHeader)); locale != "" {
		md.Locale = locale
	}
	if md.GlobalContext == nil {
		md.GlobalContext = &metadata.GlobalContext{}
	}
	if userID := strings.TrimSpace(r.Header.Get(userIDHeader)); userID != "" {
		md.GlobalContext.UserID = userID
	}
	if sessionID := strings.TrimSpace(r.Header.Get(sessionIDHeader)); sessionID != "" {
		md.GlobalContext.SessionID = sessionID
	}
	if deviceID := strings.TrimSpace(r.Header.Get(deviceIDHeader)); deviceID != "" {
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

// deriveReadIdempotencyKey mints the server-side idempotency key for a read.
// It is derived from the correlation ID so a retried read (same correlation)
// resolves to the same key, and namespaced so it never collides with a
// client-supplied mutation key. A read that somehow lacks a correlation still
// gets a fresh unique key rather than an empty one.
func deriveReadIdempotencyKey(correlationID string) string {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		correlationID = metadata.NewCorrelationID()
	}
	return "read:" + correlationID
}

func clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if forwardedFor := strings.TrimSpace(r.Header.Get(forwardedForHeader)); forwardedFor != "" {
		if before, _, ok := strings.Cut(forwardedFor, ","); ok {
			return strings.TrimSpace(before)
		}
		return forwardedFor
	}
	if realIP := strings.TrimSpace(r.Header.Get(realIPHeader)); realIP != "" {
		return realIP
	}
	host, _, ok := strings.Cut(r.RemoteAddr, ":")
	if ok {
		return host
	}
	return r.RemoteAddr
}
