// Package server exposes the HTTP, WebSocket, and foundation dispatch surfaces.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/auth"
	kitcompress "github.com/nmxmxh/ovasabi_foundation/server-kit/go/compress"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/domainerr"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/graceful"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/httpapi"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/observability"
	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"

	"{{MODULE_PATH}}/internal/config"
)

// Server is the HTTP ingress for the application.
type Server struct {
	cfg      *config.Config
	registry *registry.ServiceRegistry
	handler  *graceful.Handler
	log      *slog.Logger
	jwt      *auth.JWTManager
	rbac     *security.Authorizer
	routes   []registry.HTTPRoute

	// Public paths that bypass authentication
	publicPaths []string

	// Auth configuration
	requireAuthForDispatch bool
	protectOperational     bool
	allowedOrigins         []string

	// Rate limiting
	apiRateLimitEnabled  bool
	apiRateLimitRequests int
	apiRateLimitWindow   time.Duration
	apiRedisClient       rediskit.Client

	// HTTP compression
	httpCompressionEnabled  bool
	httpCompressionMinBytes int
	httpCompressionLevel    int

	// WebSocket configuration
	wsEnabled                 bool
	wsMaxConnections          int
	wsReadLimitBytes          int64
	wsWriteQueueDepth         int
	wsPingInterval            time.Duration
	wsGuestIdleTimeout        time.Duration
	wsAuthRequired            bool
	wsCompressionEnabled      bool
	wsCompressionThreshold    int
	wsUnauthenticatedAllowset map[string]struct{}
	ws                        *wsRuntime

	// Health check handlers
	healthHandler    http.Handler
	livenessHandler  http.Handler
	readinessHandler http.Handler
}

type dispatchExecution struct {
	Response      registry.DispatchResult
	EventType     string
	CorrelationID string
	Metadata      metadata.EnvelopeMetadata
}

// New creates a new server instance
func New(cfg *config.Config, reg *registry.ServiceRegistry, handler ...*graceful.Handler) *Server {
	var h *graceful.Handler
	if len(handler) > 0 {
		h = handler[0]
	}

	s := &Server{
		cfg:      cfg,
		registry: reg,
		handler:  h,
		log:      slog.Default().With("component", "http_server"),
		routes:   []registry.HTTPRoute{},
		publicPaths: []string{
			"/healthz",
			"/health",
			"/ws",
		},
		apiRateLimitEnabled:     true,
		apiRateLimitRequests:    200,
		apiRateLimitWindow:      time.Minute,
		httpCompressionEnabled:  true,
		httpCompressionMinBytes: 1024,
		httpCompressionLevel:    5,
		wsEnabled:               true,
		wsMaxConnections:        10000,
		wsReadLimitBytes:        1 << 20,
		wsWriteQueueDepth:       128,
		wsPingInterval:          25 * time.Second,
		wsGuestIdleTimeout:      15 * time.Minute,
		wsAuthRequired:          false,
		wsCompressionEnabled:    true,
		wsCompressionThreshold:  1024,
		wsUnauthenticatedAllowset: map[string]struct{}{
			"identity:ping:v1:requested": {},
		},
		protectOperational: cfg != nil && cfg.ProtectOperationalEndpoints,
		allowedOrigins:     configuredAllowedOrigins(cfg),
		ws:                 newWSRuntime(),
	}

	return s
}

// ConfigureAuth sets up JWT and RBAC authentication
func (s *Server) ConfigureAuth(jwtManager *auth.JWTManager, authorizer *security.Authorizer, requireAuth bool) {
	s.jwt = jwtManager
	s.rbac = authorizer
	s.requireAuthForDispatch = requireAuth
}

// ConfigureRateLimit sets up API rate limiting
func (s *Server) ConfigureRateLimit(enabled bool, requests int, window time.Duration, redisClient ...rediskit.Client) {
	s.apiRateLimitEnabled = enabled
	if requests <= 0 {
		requests = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	s.apiRateLimitRequests = requests
	s.apiRateLimitWindow = window
	if len(redisClient) > 0 {
		s.apiRedisClient = redisClient[0]
	}
}

// ConfigureCompression sets up HTTP response compression
func (s *Server) ConfigureCompression(enabled bool, minBytes, level int) {
	s.httpCompressionEnabled = enabled
	if minBytes <= 0 {
		minBytes = 1024
	}
	s.httpCompressionMinBytes = minBytes
	s.httpCompressionLevel = level
}

// ConfigureWebSocket sets up WebSocket communication
func (s *Server) ConfigureWebSocket(enabled bool, maxConnections int, authRequired bool) {
	s.wsEnabled = enabled
	if maxConnections <= 0 {
		maxConnections = 10000
	}
	s.wsMaxConnections = maxConnections
	s.wsAuthRequired = authRequired
}

// SetHTTPRoutes sets the HTTP routes for domain handlers
func (s *Server) SetHTTPRoutes(routes []registry.HTTPRoute) {
	if len(routes) == 0 {
		s.routes = []registry.HTTPRoute{}
		return
	}
	s.routes = make([]registry.HTTPRoute, len(routes))
	copy(s.routes, routes)
}

// AddPublicPath adds a path that bypasses authentication
func (s *Server) AddPublicPath(path string) {
	s.publicPaths = append(s.publicPaths, path)
}

// AddUnauthenticatedWSEvent allows an event type for unauthenticated WebSocket connections
func (s *Server) AddUnauthenticatedWSEvent(eventType string) {
	if s.wsUnauthenticatedAllowset == nil {
		s.wsUnauthenticatedAllowset = map[string]struct{}{}
	}
	s.wsUnauthenticatedAllowset[eventType] = struct{}{}
}

// ConfigureHealthChecks sets up health check handlers
func (s *Server) ConfigureHealthChecks(health, liveness, readiness http.Handler) {
	s.healthHandler = health
	s.livenessHandler = liveness
	s.readinessHandler = readiness
}

func configuredAllowedOrigins(cfg *config.Config) []string {
	if cfg == nil || len(cfg.AllowedOrigins) == 0 {
		return nil
	}
	origins := make([]string, 0, len(cfg.AllowedOrigins))
	for _, origin := range cfg.AllowedOrigins {
		trimmed := strings.TrimSpace(origin)
		if trimmed != "" {
			origins = append(origins, trimmed)
		}
	}
	return origins
}

// Handler returns the configured HTTP handler with all middleware
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Health endpoints
	mux.HandleFunc("/healthz", s.healthz)
	mux.HandleFunc("/health", s.healthz)
	mux.HandleFunc("/health/live", s.liveness)
	mux.HandleFunc("/health/ready", s.readiness)
	mux.Handle("/metricsz", s.operationalHandler(http.HandlerFunc(s.metrics)))
	mux.Handle("/metricsz/trace", s.operationalHandler(observability.TraceHandler(observability.Default())))

	// Event dispatch endpoint
	mux.HandleFunc("/v1/dispatch", s.dispatch)
	mux.Handle("/v1/events/recent", s.operationalHandler(http.HandlerFunc(s.recentEvents)))

	// WebSocket endpoint
	if s.wsEnabled {
		mux.HandleFunc("/ws", s.websocket)
		s.ensureEventSubscription()
	}

	// Register domain routes
	s.registerDomainRoutes(mux)

	// Build middleware stack
	handler := security.SecurityHeaders(mux)
	handler = kitcompress.HTTPRequestDecompressionMiddleware(true, 10*1024*1024)(handler)
	handler = security.InputValidation(handler)
	handler = security.CORS(s.allowedOrigins)(handler)

	if s.apiRateLimitEnabled {
		if s.apiRedisClient != nil {
			handler = security.NewRedisRateLimiter(s.apiRedisClient, s.apiRateLimitRequests, s.apiRateLimitWindow).Limit(handler)
		} else {
			handler = security.NewRateLimiter(s.apiRateLimitRequests, s.apiRateLimitWindow).Limit(handler)
		}
	}

	if s.requireAuthForDispatch {
		handler = security.JWTAuth(s.jwt, s.publicPaths)(handler)
	}

	handler = kitcompress.HTTPMiddleware(s.httpCompressionEnabled, s.httpCompressionMinBytes, s.httpCompressionLevel)(handler)
	handler = observability.HTTPMiddleware(handler)

	return handler
}

// Run starts the server and handles graceful shutdown
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", s.cfg.Port),
		Handler:      s.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	errChan := make(chan error, 1)
	go func() {
		s.log.Info("server listening", "addr", srv.Addr, "websocket", s.wsEnabled)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- err
		}
	}()

	select {
	case err := <-errChan:
		return fmt.Errorf("server error: %w", err)
	case <-ctx.Done():
		s.log.Info("shutting down server")
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	s.log.Info("server stopped")
	return nil
}

func (s *Server) registerDomainRoutes(mux *http.ServeMux) {
	pathMethods := map[string]map[string]http.HandlerFunc{}

	for _, route := range s.routes {
		if strings.TrimSpace(route.Path) == "" {
			continue
		}
		if route.Handler == nil && strings.TrimSpace(route.EventType) == "" {
			continue
		}

		routeHandler := route.Handler
		if routeHandler == nil {
			routeHandler = s.routeDispatch(route)
		} else {
			routeHandler = s.wrapRouteRBAC(route, routeHandler)
		}

		method := normalizedRouteMethod(route.Method)
		if _, ok := pathMethods[route.Path]; !ok {
			pathMethods[route.Path] = map[string]http.HandlerFunc{}
		}
		pathMethods[route.Path][method] = routeHandler
		s.log.Info("registered route", "method", route.Method, "path", route.Path, "event_type", route.EventType)
	}

	for path, methodHandlers := range pathMethods {
		dispatcher := methodMux(methodHandlers)
		mux.HandleFunc(path, dispatcher)
		mux.HandleFunc("/api"+path, dispatcher)
	}
}

func (s *Server) routeDispatch(route registry.HTTPRoute) http.HandlerFunc {
	if s.isPublicPath(route.Path) {
		route.RequiredCapability = ""
		route.Permission = ""
	}
	return httpapi.NewEventRouteHandler(route, s.executeDispatch)
}

func (s *Server) wrapRouteRBAC(route registry.HTTPRoute, next http.HandlerFunc) http.HandlerFunc {
	if next == nil {
		return nil
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if s.isPublicPath(route.Path) || s.isPublicPath(r.URL.Path) {
			next(w, r)
			return
		}
		if strings.TrimSpace(route.RequiredCapability) != "" {
			if err := s.enforceRBAC(r.Context(), route.EventType, route.RequiredCapability, route.Permission); err != nil {
				domainerr.WriteHTTP(w, err, domainerr.ResponseOptions{
					EventType: strings.TrimSpace(route.EventType),
				})
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) isPublicPath(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	for _, publicPath := range s.publicPaths {
		if strings.HasPrefix(path, publicPath) {
			return true
		}
	}
	return false
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if s.healthHandler != nil {
		s.healthHandler.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(s.log, w, map[string]any{"status": "ok"})
}

func (s *Server) liveness(w http.ResponseWriter, r *http.Request) {
	if s.livenessHandler != nil {
		s.livenessHandler.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(s.log, w, map[string]any{"status": "ok"})
}

func (s *Server) readiness(w http.ResponseWriter, r *http.Request) {
	if s.readinessHandler != nil {
		s.readinessHandler.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(s.log, w, map[string]any{"status": "ok"})
}

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	writeJSON(s.log, w, observability.Default().Snapshot())
}

func (s *Server) operationalHandler(next http.Handler) http.Handler {
	if next == nil {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	}
	if !s.protectOperational {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextRequest, err := s.authenticateOperationalRequest(r)
		if err != nil {
			domainerr.WriteHTTP(w, err, domainerr.ResponseOptions{})
			return
		}
		next.ServeHTTP(w, nextRequest)
	})
}

func (s *Server) authenticateOperationalRequest(r *http.Request) (*http.Request, error) {
	if r == nil {
		return r, domainerr.Unauthorized("authorization_required", "authorization required")
	}
	if strings.TrimSpace(security.GetUserIDFromContext(r.Context())) != "" {
		return r, s.authorizeOperationalContext(r.Context())
	}
	if s.jwt == nil {
		return r, domainerr.Unauthorized("authorization_required", "authorization required")
	}
	token, err := auth.ParseBearerToken(r.Header.Get("Authorization"))
	if err != nil {
		return r, domainerr.Unauthorized("authorization_required", "authorization required")
	}
	claims, err := s.jwt.ValidateToken(token)
	if err != nil {
		return r, domainerr.Unauthorized("authorization_invalid", "invalid authorization")
	}
	ctx := r.Context()
	ctx = security.ContextWithUserID(ctx, claims.UserID)
	ctx = security.ContextWithOrganizationID(ctx, claims.OrganizationID)
	ctx = security.ContextWithRole(ctx, claims.Role)
	ctx = security.ContextWithCapabilities(ctx, claims.Capabilities)
	ctx = security.ContextWithSessionID(ctx, claims.SessionID)
	if err := s.authorizeOperationalContext(ctx); err != nil {
		return r, err
	}
	return r.WithContext(ctx), nil
}

func (s *Server) authorizeOperationalContext(ctx context.Context) error {
	if s.rbac == nil {
		return nil
	}
	return s.rbac.RequireAccess(ctx, "ops.metrics", security.PermissionView)
}

func (s *Server) recentEvents(w http.ResponseWriter, _ *http.Request) {
	if s.handler == nil {
		w.Header().Set("Content-Type", "application/json")
		writeJSON(s.log, w, map[string]any{"events": []any{}})
		return
	}

	inMemoryEmitter, ok := s.handler.EventEmitter.(*graceful.InMemoryEventEmitter)
	if !ok || inMemoryEmitter == nil || inMemoryEmitter.Bus == nil {
		w.Header().Set("Content-Type", "application/json")
		writeJSON(s.log, w, map[string]any{"events": []any{}})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(s.log, w, map[string]any{
		"events": inMemoryEmitter.Bus.Recent(50),
	})
}

func (s *Server) dispatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		domainerr.WriteHTTP(w, domainerr.Validation("method_not_allowed", "method not allowed"), domainerr.ResponseOptions{
			Status: http.StatusMethodNotAllowed,
		})
		return
	}

	var req httpapi.DispatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		domainerr.WriteHTTP(w, domainerr.Validation("invalid_json", "invalid json"), domainerr.ResponseOptions{
			EventType: strings.TrimSpace(req.EventType),
		})
		return
	}
	s.executeDispatch(w, r, req)
}

func (s *Server) executeDispatch(w http.ResponseWriter, r *http.Request, req httpapi.DispatchRequest) {
	observedEventType := strings.TrimSpace(req.EventType)
	if observedEventType == "" {
		observedEventType = "unknown"
	}
	observedState := "failed"
	started := time.Now().UTC()
	defer func() {
		observability.Default().RecordDispatch(observedEventType, observedState, time.Since(started))
	}()

	execution, ok, err := s.performDispatch(r, req)
	if err != nil {
		s.writeDispatchError(w, execution.EventType, execution.CorrelationID, err)
		return
	}
	if !ok {
		observedState = "handler_not_found"
		domainerr.WriteHTTP(w, domainerr.NotFound("handler_not_found", "handler not found"), domainerr.ResponseOptions{
			EventType:     execution.EventType,
			CorrelationID: execution.CorrelationID,
		})
		return
	}

	// Handle streaming responses
	if execution.Response.Stream != nil {
		s.handleStreamResponse(w, r, execution)
		observedState = "success"
		return
	}

	// Handle protobuf responses
	if execution.Response.PayloadEncoding == "protobuf" && execution.Response.PayloadBytes != nil {
		w.Header().Set("Content-Type", "application/x-protobuf")
		if _, writeErr := w.Write(execution.Response.PayloadBytes); writeErr != nil {
			s.log.Warn("failed to write protobuf dispatch response", "event_type", execution.EventType, "error", writeErr)
			return
		}
		observedState = "success"
		return
	}

	// Standard JSON response
	w.Header().Set("Content-Type", "application/json")
	writeJSON(s.log, w, map[string]any{
		"event_type":       execution.EventType,
		"correlation_id":   execution.CorrelationID,
		"state":            "success",
		"response_payload": execution.Response.Payload,
	})
	observedState = "success"
}

//nolint:gocognit // Streaming response handling keeps channel type selection and cancellation together.
func (s *Server) handleStreamResponse(w http.ResponseWriter, r *http.Request, execution dispatchExecution) {
	switch stream := execution.Response.Stream.(type) {
	case <-chan map[string]any:
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Transfer-Encoding", "chunked")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			flusher = nil
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case item, ok := <-stream:
				if !ok {
					return
				}
				if err := json.NewEncoder(w).Encode(item); err != nil {
					s.log.Warn("failed to encode stream item", "event_type", execution.EventType, "error", err)
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	case <-chan []byte:
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Transfer-Encoding", "chunked")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			flusher = nil
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case chunk, ok := <-stream:
				if !ok {
					return
				}
				if _, err := w.Write(chunk); err != nil {
					s.log.Warn("failed to write stream chunk", "event_type", execution.EventType, "error", err)
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	}
}

func writeJSON(log *slog.Logger, w http.ResponseWriter, value any) {
	if err := json.NewEncoder(w).Encode(value); err != nil && log != nil {
		log.Warn("failed to encode json response", "error", err)
	}
}

func (s *Server) performDispatch(r *http.Request, req httpapi.DispatchRequest) (dispatchExecution, bool, error) {
	eventType := strings.TrimSpace(req.EventType)
	if eventType == "" {
		return dispatchExecution{}, false, domainerr.Validation("event_type_required", "event_type is required")
	}

	t, err := time.Parse(time.RFC3339, req.Timestamp)
	if err != nil {
		return dispatchExecution{EventType: eventType, CorrelationID: req.CorrelationID}, false, domainerr.Validation("invalid_timestamp", "invalid timestamp")
	}

	md := metadata.FromMap(req.Metadata)
	httpapi.EnrichMetadataFromRequest(&md, r)
	req.CorrelationID = md.EnsureCorrelation(req.CorrelationID)
	md.ApplyDefaults("http.dispatch")
	enrichMetadataFromRequest(&md, r)
	enrichMetadataFromAuthContext(r.Context(), &md)

	if validateErr := md.Validate(); validateErr != nil {
		return dispatchExecution{EventType: eventType, CorrelationID: req.CorrelationID, Metadata: md}, false, validateErr
	}

	if req.Payload == nil {
		req.Payload = map[string]any{}
	}

	env := events.Envelope{
		EventType:       eventType,
		Payload:         req.Payload,
		PayloadBytes:    append([]byte(nil), req.PayloadBytes...),
		PayloadEncoding: req.PayloadEncoding,
		Metadata:        md.ToMap(),
		CorrelationID:   req.CorrelationID,
		SchemaVersion:   req.SchemaVersion,
		Timestamp:       t,
	}
	env.Normalize()

	if validateErr := env.Validate(); validateErr != nil {
		return dispatchExecution{EventType: eventType, CorrelationID: req.CorrelationID, Metadata: md}, false, validateErr
	}

	ctx := metadata.IntoContext(r.Context(), md)

	if !s.isPublicPath(r.URL.Path) {
		if accessErr := s.enforceRBAC(ctx, eventType, req.RequiredCapability, req.RequiredPermission); accessErr != nil {
			return dispatchExecution{EventType: eventType, CorrelationID: req.CorrelationID, Metadata: md}, false, accessErr
		}
	}

	res, ok, err := s.registry.DispatchInput(ctx, eventType, registry.DispatchInput{
		Payload:          req.Payload,
		PayloadBytes:     req.PayloadBytes,
		PayloadEncoding:  req.PayloadEncoding,
		ResponseEncoding: req.ResponseEncoding,
		Metadata:         md.ToMap(),
	})
	if err != nil {
		return dispatchExecution{EventType: eventType, CorrelationID: req.CorrelationID, Metadata: md}, ok, err
	}

	return dispatchExecution{
		Response:      res,
		EventType:     eventType,
		CorrelationID: req.CorrelationID,
		Metadata:      md,
	}, ok, nil
}

func (s *Server) writeDispatchError(w http.ResponseWriter, eventType, correlationID string, err error) {
	domainerr.WriteHTTP(w, err, domainerr.ResponseOptions{
		EventType:     eventType,
		CorrelationID: correlationID,
	})
}

func (s *Server) enforceRBAC(ctx context.Context, eventType, requiredCapability, requiredPermission string) error {
	if !s.requireAuthForDispatch {
		return nil
	}
	userID := security.GetUserIDFromContext(ctx)
	if strings.TrimSpace(userID) == "" {
		return domainerr.Unauthorized("unauthenticated", "unauthenticated")
	}
	if s.rbac == nil {
		return nil
	}

	capability := strings.TrimSpace(requiredCapability)
	if capability == "" {
		capability = security.CapabilityFromEvent(eventType)
	}
	if capability == "" {
		return nil
	}
	permission := strings.TrimSpace(requiredPermission)
	if permission == "" {
		permission = security.PermissionFromEvent(eventType)
	}
	return s.rbac.RequireAccess(ctx, capability, permission)
}

func enrichMetadataFromRequest(md *metadata.EnvelopeMetadata, r *http.Request) {
	if md == nil || r == nil {
		return
	}
	if md.GlobalContext == nil {
		md.GlobalContext = &metadata.GlobalContext{}
	}

	if md.GlobalContext.Source == "" {
		md.GlobalContext.Source = "api"
	}
	if md.GlobalContext.UserAgent == "" {
		md.GlobalContext.UserAgent = r.UserAgent()
	}
	if md.GlobalContext.IPAddress == "" {
		md.GlobalContext.IPAddress = requestIP(r)
	}
}

func enrichMetadataFromAuthContext(ctx context.Context, md *metadata.EnvelopeMetadata) {
	if md == nil {
		return
	}

	userID := strings.TrimSpace(security.GetUserIDFromContext(ctx))
	orgID := strings.TrimSpace(security.GetOrganizationIDFromContext(ctx))
	roleID := strings.TrimSpace(security.GetRoleFromContext(ctx))
	if userID == "" && orgID == "" && roleID == "" {
		return
	}

	if md.GlobalContext == nil {
		md.GlobalContext = &metadata.GlobalContext{}
	}
	if userID != "" {
		md.GlobalContext.UserID = userID
	}
	if orgID != "" {
		md.GlobalContext.OrganizationID = orgID
	}
	if roleID != "" {
		md.GlobalContext.RoleID = roleID
	}
}

func requestIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if forwarded != "" {
		parts := strings.Split(forwarded, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

func normalizedRouteMethod(method string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		return http.MethodGet
	}
	return method
}

func methodMux(methodHandlers map[string]http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if len(methodHandlers) == 0 {
			domainerr.WriteHTTP(w, domainerr.NotFound("handler_not_found", "handler not found"), domainerr.ResponseOptions{})
			return
		}
		method := strings.ToUpper(strings.TrimSpace(r.Method))
		if h, ok := methodHandlers[method]; ok {
			h(w, r)
			return
		}
		if method == http.MethodHead {
			if h, ok := methodHandlers[http.MethodGet]; ok {
				h(w, r)
				return
			}
		}
		domainerr.WriteHTTP(w, domainerr.Validation("method_not_allowed", "method not allowed"), domainerr.ResponseOptions{
			Status: http.StatusMethodNotAllowed,
		})
	}
}
