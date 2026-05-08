package security

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/auth"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/domainerr"
)

// SecurityHeaders adds baseline hardening headers including CSP and HSTS.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; object-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self' data:; connect-src 'self' ws: wss:; frame-ancestors 'none'; form-action 'self'; upgrade-insecure-requests; block-all-mixed-content")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Embedder-Policy", "require-corp")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Origin-Agent-Cluster", "?1")

		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		next.ServeHTTP(w, r)
	})
}

// CORS handles cross-origin access with robust header support.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				w.Header().Add("Vary", "Origin")
				w.Header().Add("Vary", "Access-Control-Request-Method")
				w.Header().Add("Vary", "Access-Control-Request-Headers")
			}
			if isOriginAllowed(origin, allowedOrigins) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key, X-CSRF-Token, X-Requested-With, X-Idempotency-Key, X-Trace-ID, X-Span-ID, X-Request-ID, X-Correlation-ID")
				w.Header().Set("Access-Control-Max-Age", "3600")
			}
			if r.Method == http.MethodOptions {
				if origin != "" && !isOriginAllowed(origin, allowedOrigins) {
					domainerr.WriteHTTP(w, domainerr.Forbidden("origin_not_allowed", "origin not allowed"), domainerr.ResponseOptions{
						Status: http.StatusForbidden,
					})
					return
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CSRFProtection rejects non-safe cross-origin browser mutations using Go's
// Fetch-Metadata/Origin based CrossOriginProtection.
func CSRFProtection(trustedOrigins []string, bypassPatterns ...string) func(http.Handler) http.Handler {
	protection := http.NewCrossOriginProtection()
	for _, origin := range trustedOrigins {
		if err := protection.AddTrustedOrigin(strings.TrimSpace(origin)); err != nil {
			continue
		}
	}
	for _, pattern := range bypassPatterns {
		if trimmed := strings.TrimSpace(pattern); trimmed != "" {
			protection.AddInsecureBypassPattern(trimmed)
		}
	}
	protection.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		domainerr.WriteHTTP(w, domainerr.Forbidden("csrf_rejected", "cross-origin request rejected"), domainerr.ResponseOptions{
			Status: http.StatusForbidden,
		})
	}))

	return func(next http.Handler) http.Handler {
		return protection.Handler(next)
	}
}

func isOriginAllowed(origin string, allowed []string) bool {
	if origin == "" {
		return false
	}
	for _, candidate := range allowed {
		if candidate == "*" || origin == candidate {
			return true
		}
	}
	return false
}

// InputValidation applies generic payload safety checks and content-type enforcement.
func InputValidation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > 15*1024*1024 { // 15MB limit
			domainerr.WriteHTTP(w, domainerr.Validation("request_too_large", "request too large"), domainerr.ResponseOptions{
				Status: http.StatusRequestEntityTooLarge,
			})
			return
		}

		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
			contentType := r.Header.Get("Content-Type")
			if !strings.HasPrefix(contentType, "application/json") &&
				!strings.HasPrefix(contentType, "multipart/form-data") &&
				!strings.HasPrefix(contentType, "application/x-protobuf") &&
				!strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
				domainerr.WriteHTTP(w, domainerr.Validation("invalid_content_type", "invalid content type"), domainerr.ResponseOptions{})
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// JWTAuth validates bearer tokens and injects claims into request context.
func JWTAuth(jwtManager *auth.JWTManager, publicPaths []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if jwtManager == nil || isPublicPath(r.URL.Path, publicPaths) {
				next.ServeHTTP(w, r)
				return
			}

			token, err := auth.ParseBearerToken(r.Header.Get("Authorization"))
			if err != nil {
				domainerr.WriteHTTP(w, domainerr.Unauthorized("authorization_required", "authorization required"), domainerr.ResponseOptions{})
				return
			}
			claims, err := jwtManager.ValidateToken(token)
			if err != nil {
				domainerr.WriteHTTP(w, domainerr.Unauthorized("authorization_invalid", "invalid authorization"), domainerr.ResponseOptions{})
				return
			}

			ctx := r.Context()
			ctx = ContextWithUserID(ctx, claims.UserID)
			ctx = ContextWithOrganizationID(ctx, claims.OrganizationID)
			ctx = ContextWithRole(ctx, claims.Role)
			ctx = ContextWithCapabilities(ctx, claims.Capabilities)
			ctx = ContextWithSessionID(ctx, claims.SessionID)
			if claims.ExpiresAt != 0 {
				ctx = ContextWithAccessExpiresAt(ctx, time.Unix(claims.ExpiresAt, 0).UTC().Format(time.RFC3339))
			}
			if claims.IssuedAt != 0 {
				ctx = ContextWithRefreshExpiresAt(ctx, time.Unix(claims.IssuedAt, 0).UTC().Add(14*24*time.Hour).Format(time.RFC3339))
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireCapabilities enforces RBAC capability checks for downstream handlers.
func RequireCapabilities(authorizer *Authorizer, capabilities ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if authorizer == nil || len(capabilities) == 0 {
				next.ServeHTTP(w, r)
				return
			}
			if err := authorizer.RequireAny(r.Context(), capabilities...); err != nil {
				domainerr.WriteHTTP(w, err, domainerr.ResponseOptions{})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isPublicPath(path string, publicPaths []string) bool {
	if path == "" {
		return false
	}

	// Hardcoded system public paths
	systemPublic := []string{
		"/healthz",
		"/metrics",
		"/ws",
		"/api/auth/login",
		"/api/auth/register",
		"/v1/user/authenticate",
		"/api/v1/user/authenticate",
		"/v1/user/register",
		"/api/v1/user/register",
		"/v1/user/refresh",
		"/api/v1/user/refresh",
	}

	for _, p := range systemPublic {
		if strings.HasPrefix(path, p) {
			return true
		}
	}

	for _, publicPath := range publicPaths {
		if strings.HasPrefix(path, publicPath) {
			return true
		}
	}
	return false
}

func requestFingerprint(r *http.Request) string {
	if r == nil {
		return "ip:unknown"
	}
	if apiKey := strings.TrimSpace(r.Header.Get("X-API-Key")); apiKey != "" {
		return "apikey:" + HashIdentifier(apiKey)
	}
	if authHeader := strings.TrimSpace(r.Header.Get("Authorization")); authHeader != "" {
		return "auth:" + HashIdentifier(authHeader)
	}
	if clientHash := HashIdentifier(GetClientIP(r)); clientHash != "" {
		return "ip:" + clientHash
	}
	return "ip:unknown"
}

// RateLimiter is a fixed-window rate limiter keyed by request fingerprint.
type RateLimiter struct {
	mu       sync.Mutex
	requests map[string][]time.Time
	limit    int
	window   time.Duration
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	if limit <= 0 {
		limit = 200
	}
	if window <= 0 {
		window = time.Minute
	}
	return &RateLimiter{
		requests: map[string][]time.Time{},
		limit:    limit,
		window:   window,
	}
}

func (rl *RateLimiter) Limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fingerprint := requestFingerprint(r)
		if !rl.Allow(fingerprint) {
			domainerr.WriteHTTP(w, domainerr.RateLimited("rate_limit_exceeded", "rate limit exceeded"), domainerr.ResponseOptions{})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()

	items := rl.requests[key]
	filtered := items[:0]
	for _, at := range items {
		if now.Sub(at) < rl.window {
			filtered = append(filtered, at)
		}
	}
	filtered = append(filtered, now)
	rl.requests[key] = filtered
	return len(filtered) <= rl.limit
}

func GetClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		return strings.TrimSpace(realIP)
	}
	return strings.TrimSpace(r.RemoteAddr)
}

type contextKey string

const (
	userIDKey        contextKey = "user_id"
	organizationKey  contextKey = "organization_id"
	roleKey          contextKey = "role"
	capabilityKey    contextKey = "capabilities"
	sessionIDKey     contextKey = "session_id"
	accessExpiryKey  contextKey = "access_expires_at"
	refreshExpiryKey contextKey = "refresh_expires_at"
)

func ContextWithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

func ContextWithOrganizationID(ctx context.Context, organizationID string) context.Context {
	return context.WithValue(ctx, organizationKey, organizationID)
}

func ContextWithRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, roleKey, role)
}

func ContextWithCapabilities(ctx context.Context, capabilities []string) context.Context {
	return context.WithValue(ctx, capabilityKey, capabilities)
}

func ContextWithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDKey, sessionID)
}

func ContextWithAccessExpiresAt(ctx context.Context, expiresAt string) context.Context {
	return context.WithValue(ctx, accessExpiryKey, expiresAt)
}

func ContextWithRefreshExpiresAt(ctx context.Context, expiresAt string) context.Context {
	return context.WithValue(ctx, refreshExpiryKey, expiresAt)
}

func GetUserIDFromContext(ctx context.Context) string {
	if value, ok := ctx.Value(userIDKey).(string); ok {
		return value
	}
	return ""
}

func GetOrganizationIDFromContext(ctx context.Context) string {
	if value, ok := ctx.Value(organizationKey).(string); ok {
		return value
	}
	return ""
}

func GetRoleFromContext(ctx context.Context) string {
	if value, ok := ctx.Value(roleKey).(string); ok {
		return value
	}
	return ""
}

func GetCapabilitiesFromContext(ctx context.Context) []string {
	if value, ok := ctx.Value(capabilityKey).([]string); ok {
		return value
	}
	return nil
}

func GetSessionIDFromContext(ctx context.Context) string {
	if value, ok := ctx.Value(sessionIDKey).(string); ok {
		return value
	}
	return ""
}

func GetAccessExpiresAtFromContext(ctx context.Context) string {
	if value, ok := ctx.Value(accessExpiryKey).(string); ok {
		return value
	}
	return ""
}

func GetRefreshExpiresAtFromContext(ctx context.Context) string {
	if value, ok := ctx.Value(refreshExpiryKey).(string); ok {
		return value
	}
	return ""
}
