package security

import (
	"context"
	"net"
	"net/http"
	stdpath "path"
	"strings"
	"sync"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/auth"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/domainerr"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scratch"
)

// DefaultOrganizationID is the deployment-default tenant for applications
// without a user-facing organization model. Tenancy is load-bearing across the
// foundation — every projection scope, record key, and command is
// tenant-scoped, which is what guarantees isolation when a deployment is
// multi-tenant — so "no organization" cannot mean an empty tenant in storage.
// Instead it normalizes to this well-known constant at the trust boundary
// (token issuance): absent at the API surface, one stable tenant underneath.
// It must never be derived from user attributes (an email domain, a name) and
// must not depend on any seed data existing.
const DefaultOrganizationID = "org_default"

// SecurityHeaders adds baseline hardening headers including CSP and HSTS.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Each response owns one backing array. Capacity limits prevent headers from sharing writable slice capacity.
		values := [...]string{
			"default-src 'self'; base-uri 'self'; object-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self' data:; connect-src 'self' ws: wss:; frame-ancestors 'none'; form-action 'self'; upgrade-insecure-requests; block-all-mixed-content",
			"nosniff", "DENY", "strict-origin-when-cross-origin",
			"geolocation=(), microphone=(), camera=()", "same-origin", "require-corp", "same-origin", "?1",
			"max-age=31536000; includeSubDomains",
		}
		headers := w.Header()
		headers["Content-Security-Policy"] = values[0:1:1]
		headers["X-Content-Type-Options"] = values[1:2:2]
		headers["X-Frame-Options"] = values[2:3:3]
		headers["Referrer-Policy"] = values[3:4:4]
		headers["Permissions-Policy"] = values[4:5:5]
		headers["Cross-Origin-Opener-Policy"] = values[5:6:6]
		headers["Cross-Origin-Embedder-Policy"] = values[6:7:7]
		headers["Cross-Origin-Resource-Policy"] = values[7:8:8]
		headers["Origin-Agent-Cluster"] = values[8:9:9]

		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			headers["Strict-Transport-Security"] = values[9:10:10]
		}

		next.ServeHTTP(w, r)
	})
}

// baseAllowedHeaders are the headers every foundation service accepts.
const baseAllowedHeaders = "Content-Type, Authorization, X-API-Key, X-CSRF-Token, X-Requested-With, X-Idempotency-Key, X-Trace-ID, X-Span-ID, X-Request-ID, X-Correlation-ID"

// allowedRequestHeaders answers a preflight with the baseline set plus whatever
// the caller asked for.
//
// The foundation cannot know a project's own headers — chowdash sends
// X-Chow-Edition and X-Chow-Role, another service will send something else —
// and a static list silently blocks every one of them the moment the frontend
// and the API sit on different origins. Reflecting the requested headers keeps
// this layer agnostic to domain concerns.
//
// This is not a security boundary being relaxed: these headers are only echoed
// after isOriginAllowed has already accepted the origin, and CORS header
// negotiation never authorises anything on its own — the origin allow-list,
// authentication and CSRF protection do.
func allowedRequestHeaders(requested string) string {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return baseAllowedHeaders
	}
	seen := make(map[string]struct{})
	for name := range strings.SplitSeq(baseAllowedHeaders, ",") {
		seen[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	var allowed strings.Builder
	allowed.WriteString(baseAllowedHeaders)
	for name := range strings.SplitSeq(requested, ",") {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		allowed.WriteString(", ")
		allowed.WriteString(trimmed)
	}
	return allowed.String()
}

// CORS handles cross-origin access with robust header support.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			allowedOrigin, isWildcard := matchOrigin(origin, allowedOrigins)
			if origin != "" {
				headers := w.Header()
				if allowedOrigin {
					allowOriginValue := origin
					if isWildcard {
						allowOriginValue = "*"
					}
					corsValues := [...]string{
						"Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers",
						allowOriginValue, "true", "GET, POST, PUT, PATCH, DELETE, OPTIONS",
						allowedRequestHeaders(r.Header.Get("Access-Control-Request-Headers")), "3600",
					}
					if len(headers["Vary"]) == 0 {
						headers["Vary"] = corsValues[0:3:3]
					} else {
						headers["Vary"] = append(headers["Vary"], corsValues[0:3]...)
					}
					headers["Access-Control-Allow-Origin"] = corsValues[3:4:4]
					if !isWildcard {
						headers["Access-Control-Allow-Credentials"] = corsValues[4:5:5]
					}
					headers["Access-Control-Allow-Methods"] = corsValues[5:6:6]
					headers["Access-Control-Allow-Headers"] = corsValues[6:7:7]
					headers["Access-Control-Max-Age"] = corsValues[7:8:8]
				} else {
					varyValues := [...]string{"Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"}
					if len(headers["Vary"]) == 0 {
						headers["Vary"] = varyValues[0:3:3]
					} else {
						headers["Vary"] = append(headers["Vary"], varyValues[0:3]...)
					}
				}
			}
			if r.Method == http.MethodOptions {
				if origin != "" && !allowedOrigin {
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
	matched, _ := matchOrigin(origin, allowed)
	return matched
}

func matchOrigin(origin string, allowed []string) (bool, bool) {
	if origin == "" {
		return false, false
	}
	hasWildcard := false
	for _, candidate := range allowed {
		if candidate == "*" {
			hasWildcard = true
		} else if origin == candidate {
			return true, false
		}
	}
	if hasWildcard {
		return true, true
	}
	return false, false
}

// MaxRequestBytes is the ingress ceiling on a request body, measured on the
// wire. A compressed body is additionally bounded by its decoded size in the
// decompression middleware.
const MaxRequestBytes int64 = 15 * 1024 * 1024

// InputValidation applies generic payload safety checks and content-type enforcement.
func InputValidation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Declared length is a fast reject, not the enforcement: ContentLength
		// is -1 under Transfer-Encoding: chunked, which passed this test
		// trivially and left the body unbounded all the way to the handler.
		if r.ContentLength > MaxRequestBytes {
			domainerr.WriteHTTP(w, domainerr.Validation("request_too_large", "request too large"), domainerr.ResponseOptions{
				Status: http.StatusRequestEntityTooLarge,
			})
			return
		}
		// The ceiling every downstream reader inherits, whatever the request
		// claimed about its own size.
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBytes)
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
// Requests without a credential are rejected on non-public paths.
func JWTAuth(jwtManager *auth.JWTManager, publicPaths []string) func(http.Handler) http.Handler {
	return jwtAuth(jwtManager, publicPaths, true)
}

// OptionalJWTAuth authenticates like JWTAuth without requiring a credential:
// a request presenting a valid token gets its claims injected into context; a
// request without one proceeds anonymously. This is the development posture —
// identity is established whenever it is presented, so identity-scoped
// surfaces (command metadata, projection tenancy) behave the same as under
// enforced auth. A presented-but-invalid token is still rejected on
// non-public paths: credentials fail closed.
func OptionalJWTAuth(jwtManager *auth.JWTManager, publicPaths []string) func(http.Handler) http.Handler {
	return jwtAuth(jwtManager, publicPaths, false)
}

func jwtAuth(jwtManager *auth.JWTManager, publicPaths []string, required bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if jwtManager == nil {
				next.ServeHTTP(w, r)
				return
			}
			// CORS preflights never carry credentials; the CORS middleware
			// downstream answers them.
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			public := isPublicPath(r.URL.Path, publicPaths)
			token := requestToken(r)
			if token == "" {
				if required && !public {
					domainerr.WriteHTTP(w, domainerr.Unauthorized("authorization_required", "authorization required"), domainerr.ResponseOptions{})
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			claims, err := jwtManager.ValidateTokenWithArena(scratch.FromContext(r.Context()), token)
			if err != nil {
				if public {
					// A public path stays reachable even when a stale token
					// rides along; serve it anonymously.
					next.ServeHTTP(w, r)
					return
				}
				domainerr.WriteHTTP(w, domainerr.Unauthorized("authorization_invalid", "invalid authorization"), domainerr.ResponseOptions{})
				return
			}

			ctx := context.WithValue(r.Context(), claimsContextKey{}, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// requestToken extracts the credential a request carries: the Authorization
// bearer header or — for WebSocket upgrades only — the access_token query
// parameter. Browsers cannot set headers on WebSocket handshakes, so the
// query parameter is the standard channel there (and only there: tokens must
// not ride URLs on ordinary requests, where they leak into logs and caches).
func requestToken(r *http.Request) string {
	if bearer, err := auth.ParseBearerToken(r.Header.Get("Authorization")); err == nil {
		if token := strings.TrimSpace(bearer); token != "" {
			return token
		}
	}
	if isWebSocketUpgrade(r) {
		return strings.TrimSpace(r.URL.Query().Get("access_token"))
	}
	return ""
}

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket")
}

// RequireSubject rejects a request that reaches a non-public path without an
// authenticated subject with a clean 401 at the security boundary, before it
// reaches a handler that needs an identity. It is the guard that closes the
// dev-mode fall-through: an unauthenticated write to a protected command route
// fails as authentication_required (401) rather than continuing anonymously and
// producing a confusing downstream error about a missing organization or tenant.
//
// It is installed in the default middleware stack regardless of REQUIRE_AUTH.
// Under the development posture (OptionalJWTAuth) a request without a credential
// is admitted anonymously and GetUserIDFromContext is empty; this guard still
// returns 401 for non-public paths there, so the exposed boundary behaves the
// same in dev and under enforced auth. A valid token populates the subject
// upstream, so an authenticated request passes through.
//
// publicPaths and the system public set (health, ws, auth issuance) are exempt,
// matched with the same rules JWTAuth uses so the two never disagree about what
// is public. Preflight requests carry no credential and are answered by the
// CORS middleware, so they are not gated here.
func RequireSubject(publicPaths []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions || isPublicPath(r.URL.Path, publicPaths) {
				next.ServeHTTP(w, r)
				return
			}
			if strings.TrimSpace(GetUserIDFromContext(r.Context())) == "" {
				domainerr.WriteHTTP(w, domainerr.Unauthorized("authentication_required", "authentication required"), domainerr.ResponseOptions{})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireCapabilities enforces RBAC capability checks for downstream handlers.
func RequireCapabilities(authorizer *Authorizer, capabilities ...string) func(http.Handler) http.Handler {
	var requiredMask CapabilityMask
	var hasMask bool
	if authorizer != nil && len(capabilities) > 0 {
		requiredMask, hasMask = authorizer.CapabilityMaskFor(capabilities...)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if authorizer == nil || len(capabilities) == 0 {
				next.ServeHTTP(w, r)
				return
			}
			ctx := r.Context()
			if hasMask && requiredMask != CapabilityMaskNone {
				role := GetRoleFromContext(ctx)
				if role != "" {
					roleMask := authorizer.RoleMask(role)
					if (roleMask & requiredMask) == requiredMask {
						next.ServeHTTP(w, r)
						return
					}
				}
			}
			if err := authorizer.RequireAny(ctx, capabilities...); err != nil {
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

	// A path that does not survive cleaning is never public. Go's ServeMux
	// redirects unclean paths rather than serving them, so traversal cannot
	// reach a handler today — but that makes the mux the thing standing
	// between "/healthz/../v1/dispatch" and an unauthenticated dispatch. This
	// check keeps the auth decision from depending on routing behaviour.
	// A single trailing slash on an otherwise clean directory path is permitted
	// because path.Clean strips trailing slashes.
	if cleaned := stdpath.Clean(path); cleaned != path && (cleaned == "/" || cleaned+"/" != path) {
		return false
	}

	// The server root serves API docs (see apidocs.ServeIndex); expose it
	// publicly by exact match. "/" must never be added to a prefix-matched
	// public list — every path starts with "/", which would make the whole
	// surface public.
	if path == "/" || isSystemPublicPath(path) {
		return true
	}

	// Configured public paths stay prefix-matched: registerPublicRoutePaths
	// truncates route templates at their first "{" precisely so the static
	// prefix covers every concrete parameter value. The match must land on a
	// segment boundary, so "/v1/media/objects" cannot also open
	// "/v1/media/objectstore-admin".
	for _, publicPath := range publicPaths {
		if matchesPathPrefix(path, publicPath) {
			return true
		}
	}
	return false
}

// isSystemPublicPath matches system public paths exactly so prefix collisions
// cannot expose operational endpoints.
func isSystemPublicPath(path string) bool {
	switch path {
	case "/healthz",
		"/health",
		"/health/live",
		"/health/ready",
		"/ws",
		"/api/auth/login",
		"/api/auth/register",
		"/v1/user/authenticate",
		"/api/v1/user/authenticate",
		"/v1/user/register",
		"/api/v1/user/register",
		"/v1/user/refresh",
		"/api/v1/user/refresh":
		return true
	default:
		return false
	}
}

// matchesPathPrefix reports whether path is prefix itself or sits beneath it,
// requiring the prefix to end on a "/" boundary rather than mid-segment.
func matchesPathPrefix(path, prefix string) bool {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || prefix == "/" {
		return false
	}
	if path == prefix {
		return true
	}
	if strings.HasSuffix(prefix, "/") {
		return strings.HasPrefix(path, prefix)
	}
	return strings.HasPrefix(path, prefix) && path[len(prefix)] == '/'
}

func requestFingerprint(r *http.Request) string {
	if r == nil {
		return "ip:unknown"
	}
	if apiKey := strings.TrimSpace(r.Header.Get("X-API-Key")); apiKey != "" {
		return prefixedHashIdentifier("apikey:", apiKey)
	}
	if authHeader := strings.TrimSpace(r.Header.Get("Authorization")); authHeader != "" {
		return prefixedHashIdentifier("auth:", authHeader)
	}
	if clientHash := prefixedHashIdentifier("ip:", GetClientIP(r)); clientHash != "" {
		return clientHash
	}
	return "ip:unknown"
}

const maxRateLimiterKeys = 10000

// RateLimiter is a fixed-window rate limiter keyed by request fingerprint.
type RateLimiter struct {
	mu       sync.Mutex
	requests map[string][]time.Time
	limit    int
	window   time.Duration
	ops      uint64
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

	rl.ops++
	if rl.ops%256 == 0 || len(rl.requests) >= maxRateLimiterKeys {
		rl.pruneExpired(now)
	}

	items := rl.requests[key]
	filtered := items[:0]
	for _, at := range items {
		if now.Sub(at) < rl.window {
			filtered = append(filtered, at)
		}
	}
	// Rejected requests are not recorded: a client that is over the limit and
	// keeps retrying must recover once its allowed requests age out, not be
	// throttled indefinitely by its own rejections.
	if len(filtered) >= rl.limit {
		rl.requests[key] = filtered
		return false
	}
	if len(filtered) == 0 && len(rl.requests) >= maxRateLimiterKeys {
		return false
	}
	rl.requests[key] = append(filtered, now)
	return true
}

// Prune removes expired entries from the tracked requests map.
func (rl *RateLimiter) Prune(now time.Time) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.pruneExpired(now)
}

func (rl *RateLimiter) pruneExpired(now time.Time) {
	for k, timestamps := range rl.requests {
		valid := false
		for _, at := range timestamps {
			if now.Sub(at) < rl.window {
				valid = true
				break
			}
		}
		if !valid {
			delete(rl.requests, k)
		}
	}
}

// KeyCount returns the count of keys currently stored in memory.
func (rl *RateLimiter) KeyCount() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.requests)
}

func cleanIP(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	raw = strings.TrimPrefix(raw, "[")
	raw = strings.TrimSuffix(raw, "]")
	if parsed := net.ParseIP(raw); parsed != nil {
		return parsed.String()
	}
	return raw
}

// TrustedProxyFunc evaluates whether a remote address belongs to a trusted proxy.
type TrustedProxyFunc func(remoteAddr string) bool

var (
	trustedProxyLock        sync.RWMutex
	globalTrustedProxyCheck TrustedProxyFunc
)

// SetTrustedProxyChecker configures a global validator for reverse proxy trust.
func SetTrustedProxyChecker(fn TrustedProxyFunc) {
	trustedProxyLock.Lock()
	defer trustedProxyLock.Unlock()
	globalTrustedProxyCheck = fn
}

func isTrustedPeer(remoteAddr string) bool {
	trustedProxyLock.RLock()
	fn := globalTrustedProxyCheck
	trustedProxyLock.RUnlock()
	if fn == nil {
		return true
	}
	return fn(remoteAddr)
}

func GetClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if isTrustedPeer(r.RemoteAddr) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			return cleanIP(strings.TrimSpace(first))
		}
		if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
			return cleanIP(strings.TrimSpace(realIP))
		}
	}
	return cleanIP(strings.TrimSpace(r.RemoteAddr))
}

type contextKey string
type claimsContextKey struct{}

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
	if claims, ok := ctx.Value(claimsContextKey{}).(*auth.Claims); ok && claims != nil {
		return claims.UserID
	}
	return ""
}

func GetOrganizationIDFromContext(ctx context.Context) string {
	if value, ok := ctx.Value(organizationKey).(string); ok {
		return value
	}
	if claims, ok := ctx.Value(claimsContextKey{}).(*auth.Claims); ok && claims != nil {
		return claims.OrganizationID
	}
	return ""
}

func GetRoleFromContext(ctx context.Context) string {
	if value, ok := ctx.Value(roleKey).(string); ok {
		return value
	}
	if claims, ok := ctx.Value(claimsContextKey{}).(*auth.Claims); ok && claims != nil {
		return claims.Role
	}
	return ""
}

func GetCapabilitiesFromContext(ctx context.Context) []string {
	if value, ok := ctx.Value(capabilityKey).([]string); ok {
		return value
	}
	if claims, ok := ctx.Value(claimsContextKey{}).(*auth.Claims); ok && claims != nil {
		return claims.Capabilities
	}
	return nil
}

func GetSessionIDFromContext(ctx context.Context) string {
	if value, ok := ctx.Value(sessionIDKey).(string); ok {
		return value
	}
	if claims, ok := ctx.Value(claimsContextKey{}).(*auth.Claims); ok && claims != nil {
		return claims.SessionID
	}
	return ""
}

func GetAccessExpiresAtFromContext(ctx context.Context) string {
	if value, ok := ctx.Value(accessExpiryKey).(string); ok {
		return value
	}
	if claims, ok := ctx.Value(claimsContextKey{}).(*auth.Claims); ok && claims != nil && claims.ExpiresAt != 0 {
		return time.Unix(claims.ExpiresAt, 0).UTC().Format(time.RFC3339)
	}
	return ""
}

func GetRefreshExpiresAtFromContext(ctx context.Context) string {
	if value, ok := ctx.Value(refreshExpiryKey).(string); ok {
		return value
	}
	if claims, ok := ctx.Value(claimsContextKey{}).(*auth.Claims); ok && claims != nil && claims.IssuedAt != 0 {
		return time.Unix(claims.IssuedAt, 0).UTC().Add(14 * 24 * time.Hour).Format(time.RFC3339)
	}
	return ""
}
