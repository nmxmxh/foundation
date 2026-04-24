package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	kitcompress "github.com/nmxmxh/ovasabi_foundation/server-kit/go/compress"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/domainerr"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/graceful"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/httpapi"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scaling"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/wsmetrics"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/wsrouting"
)

type wsRuntime struct {
	upgrader      websocket.Upgrader
	connections   sync.Map // map[string]*wsConnection
	connectionCnt atomic.Int64
	subscribeOnce sync.Once
	guestLimiter  *security.RateLimiter

	// Scaling and observability (optional, may be nil)
	scalingConfig *scaling.Config
	router        *wsrouting.Router
	metrics       *wsmetrics.Collector
	startedAt     time.Time
}

type wsConnection struct {
	id       string
	deviceID string
	ip       string
	conn     *websocket.Conn
	send     chan wsOutbound
	cancel   context.CancelFunc

	mu            sync.RWMutex
	authenticated bool
	userID        string
	orgID         string
	roleID        string
	capabilities  []string
	subscriptions map[string]struct{}
	createdAt     time.Time
	binaryFormat  bool
}

type wsOutbound struct {
	messageType int
	payload     []byte
}

func newWSRuntime() *wsRuntime {
	// Auto-tune based on available CPU cores
	cfg := scaling.AutoTune()

	return &wsRuntime{
		upgrader: websocket.Upgrader{
			ReadBufferSize:  cfg.ScaleBuffer(1024),
			WriteBufferSize: cfg.ScaleBuffer(1024),
			CheckOrigin: func(_ *http.Request) bool {
				return true
			},
		},
		guestLimiter:  security.NewRateLimiter(cfg.WSGuestRateLimit, time.Minute),
		scalingConfig: &cfg,
		metrics:       wsmetrics.NewCollector(""),
		startedAt:     time.Now().UTC(),
	}
}

// WithRouter configures a WebSocket connection router for horizontal scaling.
// Pass a Redis client and server ID to enable cross-instance routing.
func (ws *wsRuntime) WithRouter(router *wsrouting.Router) {
	if ws != nil {
		ws.router = router
	}
}

// WithMetrics configures a custom metrics collector.
func (ws *wsRuntime) WithMetrics(collector *wsmetrics.Collector) {
	if ws != nil && collector != nil {
		ws.metrics = collector
	}
}

// Metrics returns the WebSocket metrics snapshot.
func (ws *wsRuntime) Metrics() *wsmetrics.Snapshot {
	if ws == nil || ws.metrics == nil {
		return nil
	}
	snap := ws.metrics.Snapshot()
	return &snap
}

// ScalingConfig returns the current scaling configuration.
func (ws *wsRuntime) ScalingConfig() *scaling.Config {
	if ws == nil {
		return nil
	}
	return ws.scalingConfig
}

func (s *Server) websocket(w http.ResponseWriter, r *http.Request) {
	if !s.wsEnabled {
		domainerr.WriteHTTP(w, domainerr.NotFound("websocket_disabled", "websocket disabled"), domainerr.ResponseOptions{
			Status: http.StatusNotFound,
		})
		return
	}
	if s.ws == nil {
		domainerr.WriteHTTP(w, domainerr.Unavailable("ws_runtime_unavailable", "websocket runtime unavailable"), domainerr.ResponseOptions{
			Status: http.StatusServiceUnavailable,
		})
		return
	}

	ip := security.GetClientIP(r)
	if s.ws.guestLimiter != nil && !s.ws.guestLimiter.Allow(ip) {
		domainerr.WriteHTTP(w, domainerr.RateLimited("ws_rate_limit_exceeded", "websocket rate limit exceeded"), domainerr.ResponseOptions{})
		return
	}

	current := int(s.ws.connectionCnt.Load())
	if current >= s.wsMaxConnections {
		domainerr.WriteHTTP(w, domainerr.Unavailable("ws_capacity_reached", "websocket capacity reached"), domainerr.ResponseOptions{
			Status: http.StatusServiceUnavailable,
		})
		return
	}

	conn, err := s.ws.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Error("websocket upgrade failed", "error", err.Error())
		return
	}

	connectionID := strings.TrimSpace(r.URL.Query().Get("connection_id"))
	if connectionID == "" {
		connectionID = strings.TrimSpace(r.URL.Query().Get("deviceId"))
	}
	if connectionID == "" {
		connectionID = fmt.Sprintf("conn_%d", time.Now().UTC().UnixNano())
	}
	deviceID := strings.TrimSpace(r.URL.Query().Get("deviceId"))
	if deviceID == "" {
		deviceID = connectionID
	}

	ctx, cancel := context.WithCancel(r.Context())
	wsConn := &wsConnection{
		id:        connectionID,
		deviceID:  deviceID,
		ip:        ip,
		conn:      conn,
		send:      make(chan wsOutbound, s.wsWriteQueueDepth),
		cancel:    cancel,
		createdAt: time.Now().UTC(),
		subscriptions: map[string]struct{}{
			"identity:connection_open:v1:ack":  {},
			"system:websocket_error:v1:failed": {},
		},
	}
	wsConn.binaryFormat = strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("format")), "binary")

	if !s.registerWSConnection(wsConn) {
		_ = conn.Close()
		cancel()
		domainerr.WriteHTTP(w, domainerr.Unavailable("ws_capacity_reached", "websocket capacity reached"), domainerr.ResponseOptions{
			Status: http.StatusServiceUnavailable,
		})
		return
	}
	defer s.unregisterWSConnection(wsConn)

	conn.SetReadLimit(s.wsReadLimitBytes)
	_ = conn.SetReadDeadline(time.Now().Add(s.wsPingInterval * 2))
	conn.SetPongHandler(func(_ string) error {
		return conn.SetReadDeadline(time.Now().Add(s.wsPingInterval * 2))
	})

	go s.runWSWriter(ctx, wsConn)
	s.sendWSAck(wsConn)
	s.runWSReader(ctx, wsConn)
}

func (s *Server) registerWSConnection(conn *wsConnection) bool {
	if conn == nil || s.ws == nil {
		return false
	}
	next := s.ws.connectionCnt.Add(1)
	if int(next) > s.wsMaxConnections {
		s.ws.connectionCnt.Add(-1)
		if s.ws.metrics != nil {
			s.ws.metrics.RecordConnectionRejected()
		}
		return false
	}
	s.ws.connections.Store(conn.id, conn)

	// Record metrics
	if s.ws.metrics != nil {
		s.ws.metrics.RecordConnectionOpen()
	}

	// Register with router for cross-instance routing
	if s.ws.router != nil {
		_ = s.ws.router.Register(context.Background(), wsrouting.ConnectionInfo{
			ConnectionID: conn.id,
			DeviceID:     conn.deviceID,
			UserID:       conn.userID,
		})
	}

	return true
}

func (s *Server) unregisterWSConnection(conn *wsConnection) {
	if conn == nil || s.ws == nil {
		return
	}
	if conn.cancel != nil {
		conn.cancel()
	}
	s.ws.connections.Delete(conn.id)
	s.ws.connectionCnt.Add(-1)
	_ = conn.conn.Close()

	// Record metrics
	if s.ws.metrics != nil {
		s.ws.metrics.RecordConnectionClose()
	}

	// Unregister from router
	if s.ws.router != nil {
		_ = s.ws.router.Unregister(context.Background(), conn.id)
	}
}

func (s *Server) runWSReader(ctx context.Context, conn *wsConnection) {
	for {
		if !conn.isAuthenticated() && time.Since(conn.createdAt) > s.wsGuestIdleTimeout {
			s.sendWSDomainError(conn, domainerr.Unauthorized("ws_guest_timeout", "guest websocket expired"), "")
			return
		}
		messageType, payload, err := conn.conn.ReadMessage()
		if err != nil {
			return
		}
		if len(payload) == 0 {
			continue
		}

		// Record message received
		if s.ws != nil && s.ws.metrics != nil {
			s.ws.metrics.RecordMessageReceived(int64(len(payload)))
		}

		env, binaryFormat, err := s.decodeWSEnvelope(messageType, payload)
		if err != nil {
			s.sendWSDomainError(conn, domainerr.Validation("invalid_envelope", "invalid envelope"), "")
			continue
		}
		if binaryFormat {
			conn.setBinaryFormat(true)
		}
		env.Normalize()

		if !conn.isAuthenticated() {
			if s.wsAuthRequired && !s.isWSGuestAllowedEvent(env.EventType) {
				s.sendWSDomainError(conn, domainerr.Unauthorized("auth_required", "authentication required"), env.CorrelationID)
				continue
			}
			if !s.isWSGuestAllowedEvent(env.EventType) {
				s.sendWSDomainError(conn, domainerr.Forbidden("guest_event_not_allowed", "event not allowed for guest connection"), env.CorrelationID)
				continue
			}
		}

		// Track dispatch latency
		dispatchStart := time.Now()
		if err := s.dispatchWSRequest(ctx, conn, env); err != nil {
			s.log.Warn("websocket dispatch failed", "event_type", env.EventType, "error", err.Error())
		}
		if s.ws != nil && s.ws.metrics != nil {
			s.ws.metrics.RecordLatency(time.Since(dispatchStart))
		}
	}
}

func (s *Server) runWSWriter(ctx context.Context, conn *wsConnection) {
	ticker := time.NewTicker(s.wsPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case outbound, ok := <-conn.send:
			if !ok {
				return
			}
			_ = conn.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
			if err := conn.conn.WriteMessage(outbound.messageType, outbound.payload); err != nil {
				if s.ws != nil && s.ws.metrics != nil {
					s.ws.metrics.RecordMessageFailed()
				}
				return
			}
			// Record message sent
			if s.ws != nil && s.ws.metrics != nil {
				s.ws.metrics.RecordMessageSent(int64(len(outbound.payload)))
			}
		case <-ticker.C:
			_ = conn.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (s *Server) sendWSAck(conn *wsConnection) {
	if conn == nil {
		return
	}
	env := events.Envelope{
		EventType: "identity:connection_open:v1:ack",
		Payload: map[string]any{
			"connection_id": conn.id,
			"state":         "guest",
		},
		Metadata: map[string]any{
			"global_context": map[string]any{
				"device_id": conn.deviceID,
				"source":    "ws",
			},
		},
		CorrelationID: fmt.Sprintf("corr_%d", time.Now().UTC().UnixNano()),
		Timestamp:     time.Now().UTC(),
		SchemaVersion: events.EnvelopeSchemaVersion,
	}
	_ = s.enqueueWSEnvelope(conn, env)
}

func (s *Server) dispatchWSRequest(ctx context.Context, conn *wsConnection, env events.Envelope) error {
	if conn == nil {
		return errors.New("connection is required")
	}
	md := metadata.FromMap(env.Metadata)
	if md.GlobalContext == nil {
		md.GlobalContext = &metadata.GlobalContext{}
	}
	if md.GlobalContext.DeviceID == "" {
		md.GlobalContext.DeviceID = conn.deviceID
	}
	if md.GlobalContext.Source == "" {
		md.GlobalContext.Source = "ws"
	}
	auth := conn.authSnapshot()
	if auth.authenticated {
		md.GlobalContext.UserID = auth.userID
		md.GlobalContext.OrganizationID = auth.orgID
		md.GlobalContext.RoleID = auth.roleID
	}

	req := httpapi.DispatchRequest{
		EventType:        env.EventType,
		Payload:          env.Payload,
		PayloadBytes:     append([]byte(nil), env.PayloadBytes...),
		PayloadEncoding:  env.PayloadEncoding,
		ResponseEncoding: env.PayloadEncoding,
		Metadata:         md.ToMap(),
		CorrelationID:    env.CorrelationID,
		SchemaVersion:    env.SchemaVersion,
		Timestamp:        env.Timestamp.UTC().Format(time.RFC3339),
	}
	if req.Payload == nil {
		req.Payload = map[string]any{}
	}

	wsCtx := ctx
	if auth.authenticated {
		wsCtx = security.ContextWithUserID(wsCtx, auth.userID)
		wsCtx = security.ContextWithOrganizationID(wsCtx, auth.orgID)
		wsCtx = security.ContextWithRole(wsCtx, auth.roleID)
		wsCtx = security.ContextWithCapabilities(wsCtx, auth.capabilities)
	}

	request := (&http.Request{
		Method:     http.MethodPost,
		URL:        mustParseURL("/ws"),
		Header:     make(http.Header),
		RemoteAddr: conn.ip,
	}).WithContext(wsCtx)
	request.RemoteAddr = conn.ip

	execution, ok, err := s.performDispatch(request, req)
	if err != nil {
		responseEnvelope, buildErr := buildWSDispatchErrorEnvelope(env, md, err)
		if buildErr != nil {
			return buildErr
		}
		return s.enqueueWSEnvelope(conn, responseEnvelope)
	}
	if !ok {
		responseEnvelope, buildErr := buildWSDispatchErrorEnvelope(env, md, domainerr.NotFound("handler_not_found", "handler not found"))
		if buildErr != nil {
			return buildErr
		}
		return s.enqueueWSEnvelope(conn, responseEnvelope)
	}

	responseEnvelope, err := buildWSDispatchResponseEnvelope(env, md, execution.Response)
	if err != nil {
		return err
	}
	s.maybeUpgradeConnectionAuth(conn, env.EventType, execution.Response.Payload)
	if env.EventType == "identity:logout_connection:v1:requested" {
		conn.clearAuth()
	}
	if env.EventType == "system:websocket_subscribe:v1:requested" {
		s.handleWSSubscribe(conn, env)
		return nil
	}
	if env.EventType == "system:websocket_unsubscribe:v1:requested" {
		s.handleWSUnsubscribe(conn, env)
		return nil
	}
	return s.enqueueWSEnvelope(conn, responseEnvelope)
}

func (s *Server) handleWSSubscribe(conn *wsConnection, env events.Envelope) {
	pattern, _ := env.Payload["pattern"].(string)
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		s.sendWSDomainError(conn, domainerr.Validation("pattern_required", "subscription pattern is required"), env.CorrelationID)
		return
	}

	conn.mu.Lock()
	if conn.subscriptions == nil {
		conn.subscriptions = make(map[string]struct{})
	}
	conn.subscriptions[pattern] = struct{}{}
	conn.mu.Unlock()

	// Record subscription metrics
	if s.ws != nil && s.ws.metrics != nil {
		s.ws.metrics.RecordSubscription()
	}

	_ = s.enqueueWSEnvelope(conn, events.Envelope{
		EventType:     "system:websocket_subscribe:v1:success",
		Payload:       map[string]any{"pattern": pattern},
		CorrelationID: env.CorrelationID,
		SchemaVersion: events.EnvelopeSchemaVersion,
		Timestamp:     time.Now().UTC(),
	})
}

func (s *Server) handleWSUnsubscribe(conn *wsConnection, env events.Envelope) {
	pattern, _ := env.Payload["pattern"].(string)
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		s.sendWSDomainError(conn, domainerr.Validation("pattern_required", "unsubscription pattern is required"), env.CorrelationID)
		return
	}

	conn.mu.Lock()
	if conn.subscriptions != nil {
		delete(conn.subscriptions, pattern)
	}
	conn.mu.Unlock()

	// Record unsubscription metrics
	if s.ws != nil && s.ws.metrics != nil {
		s.ws.metrics.RecordUnsubscription()
	}

	_ = s.enqueueWSEnvelope(conn, events.Envelope{
		EventType:     "system:websocket_unsubscribe:v1:success",
		Payload:       map[string]any{"pattern": pattern},
		CorrelationID: env.CorrelationID,
		SchemaVersion: events.EnvelopeSchemaVersion,
		Timestamp:     time.Now().UTC(),
	})
}

func (s *Server) maybeUpgradeConnectionAuth(conn *wsConnection, eventType string, payload map[string]any) {
	switch eventType {
	case "identity:authenticate_connection:v1:requested", "identity:refresh_connection:v1:requested", "identity:bind_connection_token:v1:requested":
	default:
		return
	}
	if payload == nil {
		return
	}
	userID, _ := payload["user_id"].(string)
	orgID, _ := payload["organization_id"].(string)
	roleID, _ := payload["role_id"].(string)
	rawCaps, _ := payload["capabilities"].([]any)
	caps := make([]string, 0, len(rawCaps))
	for _, capability := range rawCaps {
		if text, ok := capability.(string); ok && strings.TrimSpace(text) != "" {
			caps = append(caps, strings.TrimSpace(text))
		}
	}
	if userID == "" {
		// Auth failed - no user ID in response
		if s.ws != nil && s.ws.metrics != nil {
			s.ws.metrics.RecordAuthFailure()
		}
		return
	}
	conn.setAuth(userID, orgID, roleID, caps)

	// Record auth success
	if s.ws != nil && s.ws.metrics != nil {
		s.ws.metrics.RecordAuthSuccess()
	}

	// Update router with authenticated user
	if s.ws != nil && s.ws.router != nil {
		_ = s.ws.router.UpdateAuth(context.Background(), conn.id, userID)
	}
}

func (s *Server) isWSGuestAllowedEvent(eventType string) bool {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return false
	}
	if _, ok := s.wsUnauthenticatedAllowset[eventType]; ok {
		return true
	}
	return strings.HasSuffix(eventType, ":ping:v1:requested")
}

func (s *Server) enqueueWSEnvelope(conn *wsConnection, envelope events.Envelope) error {
	if conn == nil {
		return errors.New("connection is required")
	}
	if conn.prefersBinaryFormat() {
		raw, err := envelope.ToBinary()
		if err != nil {
			return err
		}
		return s.enqueueWS(conn, websocket.BinaryMessage, raw)
	}
	raw, err := envelope.ToJSON()
	if err != nil {
		return err
	}
	return s.enqueueWS(conn, websocket.TextMessage, raw)
}

func (s *Server) enqueueWS(conn *wsConnection, messageType int, payload []byte) error {
	select {
	case conn.send <- wsOutbound{messageType: messageType, payload: payload}:
		return nil
	default:
		return errors.New("websocket outbound queue full")
	}
}

func (s *Server) sendWSDomainError(conn *wsConnection, err error, correlationID string) {
	if conn == nil {
		return
	}
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		correlationID = fmt.Sprintf("corr_%d", time.Now().UTC().UnixNano())
	}
	body := domainerr.Body(err, domainerr.ResponseOptions{
		CorrelationID: correlationID,
	})
	errorPayload := map[string]any{
		"kind":           body.Error.Kind,
		"code":           body.Error.Code,
		"message":        body.Error.Message,
		"status":         body.Error.Status,
		"correlation_id": body.Error.CorrelationID,
	}
	if body.Error.EventType != "" {
		errorPayload["event_type"] = body.Error.EventType
	}
	if len(body.Error.Details) > 0 {
		errorPayload["details"] = body.Error.Details
	}
	payload := map[string]any{
		"state": body.State,
		"error": errorPayload,
	}
	_ = s.enqueueWSEnvelope(conn, events.Envelope{
		EventType:     "system:websocket_error:v1:failed",
		Payload:       payload,
		Metadata:      map[string]any{},
		CorrelationID: correlationID,
		SchemaVersion: events.EnvelopeSchemaVersion,
		Timestamp:     time.Now().UTC(),
	})
}

func (s *Server) ensureEventSubscription() {
	if s == nil || s.ws == nil || s.handler == nil {
		return
	}

	var bus events.Bus
	switch e := s.handler.EventEmitter.(type) {
	case *graceful.InMemoryEventEmitter:
		bus = e.Bus
	case *graceful.RedisEventEmitter:
		bus = e.Bus
	}

	if bus == nil {
		return
	}

	s.ws.subscribeOnce.Do(func() {
		bus.Subscribe("*", func(ctx context.Context, envelope events.Envelope) {
			s.forwardEventToConnections(ctx, envelope)
		})
	})
}

func (s *Server) forwardEventToConnections(_ context.Context, envelope events.Envelope) {
	md := metadata.FromMap(envelope.Metadata)
	targetUser := ""
	targetDevice := ""
	if md.GlobalContext != nil {
		targetUser = strings.TrimSpace(md.GlobalContext.UserID)
		targetDevice = strings.TrimSpace(md.GlobalContext.DeviceID)
	}

	s.ws.connections.Range(func(_, value any) bool {
		conn, ok := value.(*wsConnection)
		if !ok || conn == nil {
			return true
		}
		auth := conn.authSnapshot()
		if targetDevice != "" && targetDevice != conn.deviceID && targetDevice != conn.id {
			return true
		}
		if targetUser != "" && targetUser != auth.userID {
			return true
		}

		conn.mu.RLock()
		subscribed := false
		for pattern := range conn.subscriptions {
			if events.Matches(pattern, envelope.EventType) {
				subscribed = true
				break
			}
		}
		conn.mu.RUnlock()

		if subscribed {
			_ = s.enqueueWSEnvelope(conn, envelope)
		}
		return true
	})
}

func (c *wsConnection) setAuth(userID, orgID, roleID string, capabilities []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.authenticated = true
	c.userID = strings.TrimSpace(userID)
	c.orgID = strings.TrimSpace(orgID)
	c.roleID = strings.TrimSpace(roleID)
	c.capabilities = append([]string(nil), capabilities...)
}

func (c *wsConnection) clearAuth() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.authenticated = false
	c.userID = ""
	c.orgID = ""
	c.roleID = ""
	c.capabilities = nil
}

func (c *wsConnection) isAuthenticated() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.authenticated
}

type wsAuthSnapshot struct {
	authenticated bool
	userID        string
	orgID         string
	roleID        string
	capabilities  []string
}

func (c *wsConnection) authSnapshot() wsAuthSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return wsAuthSnapshot{
		authenticated: c.authenticated,
		userID:        c.userID,
		orgID:         c.orgID,
		roleID:        c.roleID,
		capabilities:  append([]string(nil), c.capabilities...),
	}
}

func (c *wsConnection) setBinaryFormat(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.binaryFormat = enabled
}

func (c *wsConnection) prefersBinaryFormat() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.binaryFormat
}

func (s *Server) decodeWSEnvelope(messageType int, payload []byte) (events.Envelope, bool, error) {
	switch messageType {
	case websocket.BinaryMessage:
		if env, err := events.FromBinary(payload); err == nil {
			return env, true, nil
		}
		if s.wsCompressionEnabled {
			if decompressed, err := kitcompress.Decompress(payload); err == nil {
				if env, decodeErr := events.Decode(decompressed); decodeErr == nil {
					return env, true, nil
				}
			}
		}
		if env, err := events.Decode(payload); err == nil {
			return env, true, nil
		}
		return events.Envelope{}, true, errors.New("binary websocket payload is not a valid envelope")
	default:
		env, err := events.FromJSON(payload)
		return env, false, err
	}
}

func buildWSDispatchResponseEnvelope(request events.Envelope, md metadata.EnvelopeMetadata, result registry.DispatchResult) (events.Envelope, error) {
	meta := md.ToMap()
	meta["status"] = http.StatusOK
	envelope := events.Envelope{
		EventType:       terminalEventType(request.EventType, "success"),
		Payload:         result.Payload,
		PayloadBytes:    append([]byte(nil), result.PayloadBytes...),
		PayloadEncoding: result.PayloadEncoding,
		Metadata:        meta,
		CorrelationID:   request.CorrelationID,
		SchemaVersion:   events.EnvelopeSchemaVersion,
		Timestamp:       time.Now().UTC(),
	}
	envelope.Normalize()
	return envelope, nil
}

func buildWSDispatchErrorEnvelope(request events.Envelope, md metadata.EnvelopeMetadata, err error) (events.Envelope, error) {
	payload := map[string]any{}
	body := domainerr.Body(err, domainerr.ResponseOptions{
		CorrelationID: request.CorrelationID,
		EventType:     request.EventType,
	})
	raw, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		return events.Envelope{}, marshalErr
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return events.Envelope{}, err
	}
	meta := md.ToMap()
	meta["status"] = domainerr.HTTPStatus(err)
	return events.Envelope{
		EventType:     terminalEventType(request.EventType, "failed"),
		Payload:       payload,
		Metadata:      meta,
		CorrelationID: request.CorrelationID,
		SchemaVersion: events.EnvelopeSchemaVersion,
		Timestamp:     time.Now().UTC(),
	}, nil
}

func terminalEventType(eventType, terminal string) string {
	eventType = strings.TrimSpace(eventType)
	terminal = strings.TrimSpace(terminal)
	if eventType == "" || terminal == "" {
		return eventType
	}
	for _, suffix := range []string{":requested", ":ack", ":success", ":failed"} {
		if strings.HasSuffix(eventType, suffix) {
			return strings.TrimSuffix(eventType, suffix) + ":" + terminal
		}
	}
	return eventType + ":" + terminal
}

func mustParseURL(path string) *url.URL {
	parsed, err := url.Parse(path)
	if err != nil {
		panic(err)
	}
	return parsed
}
