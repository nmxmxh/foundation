package projectiongw

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
	"google.golang.org/protobuf/proto"
)

// projectionUpgrader upgrades projection subscription requests. Origin is
// enforced by the server's CORS middleware upstream of this handler. gorilla is
// the foundation's standard inbound-WebSocket library (matching the /ws server).
var projectionUpgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

// ErrUnauthenticated is returned when a projection request carries no
// authenticated identity. Projection access is identity-scoped: the tenant is
// the organization, and the organization is the only thing that authorizes a
// projection read. A request without it is rejected, not defaulted.
var ErrUnauthenticated = errors.New("projectiongw: unauthenticated projection request")

// ErrScopeForbidden is returned when a request names a scope outside the
// mount's ScopeAllowlist. Distinct from ErrScopeInvalid (malformed) and
// ErrUnauthenticated (no identity): the request is well-formed and identified,
// but this mount does not publish that scope.
var ErrScopeForbidden = errors.New("projectiongw: scope not published on this mount")

// TenantFunc derives the authenticated tenant (organization) for a request. It
// is the trust boundary: the scope tenant comes from authenticated context, not
// from client-supplied path, header, or query, so a client cannot read another
// tenant's projection (TenantScopeStable).
type TenantFunc func(r *http.Request) (string, error)

// PublicTenantFunc pins every request on a mount to one fixed public
// organization, regardless of identity. This is the anonymous read posture: a
// public mount (e.g. a discovery/landing surface) serves ONE tenant's
// deliberately-published projections to everyone, so no request can steer the
// tenant. Always pair it with a ScopeAllowlist — a public tenant without a
// scope allowlist would publish every collection in that organization.
func PublicTenantFunc(organization string) TenantFunc {
	org := strings.TrimSpace(organization)
	return func(*http.Request) (string, error) {
		if org == "" {
			// An unset public org disables the mount rather than defaulting.
			return "", ErrUnauthenticated
		}
		return org, nil
	}
}

// ScopeAllowlist names the {domain}/{collection} scopes a mount will serve,
// as domain -> collections. A nil map applies no restriction (the
// identity-scoped default); a non-nil map rejects any scope not listed with
// ErrScopeForbidden — including subscribe control frames on the multiplexed
// stream. Public mounts must be fail-closed: an empty (non-nil) map allows
// nothing.
type ScopeAllowlist map[string][]string

// Allows reports whether the allowlist publishes domain/collection.
func (a ScopeAllowlist) Allows(domain, collection string) bool {
	if a == nil {
		return true
	}
	return slices.Contains(a[domain], collection)
}

// SecurityTenantFunc is the default identity-scoped tenant resolver. It reads the
// authenticated organization the security middleware placed in request context
// from verified JWT claims. No authenticated organization means no projection —
// access is gated by identity by default. This is what makes projection generic
// across scaffolded projects: every project authenticates through the same
// security middleware, so every project scopes projections by the same identity.
func SecurityTenantFunc(r *http.Request) (string, error) {
	organization := strings.TrimSpace(security.GetOrganizationIDFromContext(r.Context()))
	if organization == "" {
		return "", ErrUnauthenticated
	}
	return organization, nil
}

// HandlerConfig configures the HTTP/WS handlers.
type HandlerConfig struct {
	// Tenant resolves the authenticated tenant. Defaults to SecurityTenantFunc
	// (the authenticated organization from request context) when nil.
	Tenant TenantFunc
	// PathPrefix is stripped before parsing {domain}/{collection}. Defaults to
	// "/v1/projections/". Used by both the snapshot and subscribe handlers.
	PathPrefix string
	// Allowlist restricts which scopes this mount serves (see ScopeAllowlist).
	// Nil applies no restriction.
	Allowlist ScopeAllowlist
}

func (c HandlerConfig) tenant() TenantFunc {
	if c.Tenant != nil {
		return c.Tenant
	}
	return SecurityTenantFunc
}

func (c HandlerConfig) prefix() string {
	if strings.TrimSpace(c.PathPrefix) != "" {
		return c.PathPrefix
	}
	return "/v1/projections/"
}

// scopeFromRequest builds an authenticated scope: tenant from auth context,
// domain/collection from the trailing path segments after the prefix.
func (c HandlerConfig) scopeFromRequest(r *http.Request) (*foundationpb.ProjectionScope, error) {
	tenant, err := c.tenant()(r)
	if err != nil {
		return nil, err
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, c.prefix()), "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, errors.New("projectiongw: path must be {prefix}{domain}/{collection}")
	}
	scope := &foundationpb.ProjectionScope{TenantId: tenant, Domain: parts[0], Collection: parts[1]}
	if err := validateScope(scope); err != nil {
		return nil, err
	}
	if !c.Allowlist.Allows(scope.GetDomain(), scope.GetCollection()) {
		return nil, ErrScopeForbidden
	}
	return scope, nil
}

// Handler returns a single http.Handler mounted at the path prefix (e.g.
// "/v1/projections/"):
//
//   - a WebSocket upgrade at exactly the prefix root is the multiplexed delta
//     stream — one connection carries every scope the client subscribes to via
//     control frames (see SubscribeMultiplexHandler);
//   - a WebSocket upgrade at {prefix}{domain}/{collection} is the single-scope
//     delta stream, kept for older clients;
//   - any other request is the snapshot reader.
func (g *Gateway) Handler(config HandlerConfig) http.Handler {
	snapshot := g.SnapshotHandler(config)
	subscribe := g.SubscribeHandler(config)
	multiplex := g.SubscribeMultiplexHandler(config)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			if strings.Trim(strings.TrimPrefix(r.URL.Path, config.prefix()), "/") == "" {
				multiplex.ServeHTTP(w, r)
				return
			}
			subscribe.ServeHTTP(w, r)
			return
		}
		snapshot.ServeHTTP(w, r)
	})
}

// SnapshotHandler serves GET {prefix}{domain}/{collection} as a binary
// ProjectionSnapshot proto. The optional `since` query parameter carries a
// resume watermark and `limit` bounds the record count.
func (g *Gateway) SnapshotHandler(config HandlerConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		scope, err := g.snapshotScope(config, r, w)
		if err != nil {
			return
		}
		req := &foundationpb.ProjectionSnapshotRequest{
			Scope:          scope,
			SinceWatermark: strings.TrimSpace(r.URL.Query().Get("since")),
		}
		if limit, err := strconv.ParseUint(strings.TrimSpace(r.URL.Query().Get("limit")), 10, 32); err == nil {
			req.Limit = uint32(limit)
		}
		snapshot, err := g.Snapshot(r.Context(), req)
		if err != nil {
			writeError(w, err)
			return
		}
		raw, err := proto.Marshal(snapshot)
		if err != nil {
			http.Error(w, "encode error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.Header().Set("X-Projection-Epoch", strconv.FormatUint(snapshot.GetEpoch(), 10))
		w.Header().Set("X-Projection-Watermark", snapshot.GetWatermark())
		_, _ = w.Write(raw)
	})
}

func (g *Gateway) snapshotScope(config HandlerConfig, r *http.Request, w http.ResponseWriter) (*foundationpb.ProjectionScope, error) {
	scope, err := config.scopeFromRequest(r)
	if err != nil {
		writeError(w, err)
		return nil, err
	}
	return scope, nil
}

// SubscribeHandler serves a WebSocket delta stream for {prefix}{domain}/{collection}.
// Each frame is a binary events.Envelope carrying a RecordMutationBatch. A
// client resumes by sending its last watermark as the first text frame; the
// gateway does not replay history, so a gap (or any dropped frame) must be
// reconciled with a fresh snapshot.
func (g *Gateway) SubscribeHandler(config HandlerConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Scope and identity are resolved before the upgrade, so an unauthorized
		// or malformed request gets a proper HTTP status (e.g. 401) instead of a
		// silently-closed socket.
		scope, err := config.scopeFromRequest(r)
		if err != nil {
			writeError(w, err)
			return
		}
		sub, err := g.Subscribe(scope)
		if err != nil {
			writeError(w, err)
			return
		}
		conn, err := projectionUpgrader.Upgrade(w, r, nil)
		if err != nil {
			sub.Cancel()
			return
		}
		defer conn.Close()
		defer sub.Cancel()

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		// Reader pump: drain inbound frames (advisory resume token + control)
		// and detect client close so the writer loop exits promptly.
		go func() {
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					cancel()
					return
				}
			}
		}()

		var lastDropped, lastEpoch uint64
		// emitResyncIfDropped sends a resync control frame when the hub has shed
		// frames since the last notice, so the client reconciles from a fresh
		// snapshot rather than silently presenting a gapped stream.
		emitResyncIfDropped := func() error {
			if dropped := sub.Dropped(); dropped > lastDropped {
				lastDropped = dropped
				return sendResync(conn, lastEpoch, dropped)
			}
			return nil
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-sub.Drops():
				// A drop may arrive with no subsequent deliverable frame (e.g. a
				// burst that overflows then goes quiet); emit the resync now
				// rather than waiting for a frame that may never come.
				if err := emitResyncIfDropped(); err != nil {
					return
				}
			case frame, ok := <-sub.Frames:
				if !ok {
					return
				}
				lastEpoch = frame.Epoch
				if err := emitResyncIfDropped(); err != nil {
					return
				}
				if err := conn.WriteMessage(websocket.BinaryMessage, frame.Envelope); err != nil {
					return
				}
			}
		}
	})
}

// MultiplexScope names one projection topic inside a multiplexed subscription
// command. The tenant is never client-supplied — it comes from the
// authenticated request context, exactly like the single-scope handlers.
type MultiplexScope struct {
	Domain     string `json:"domain"`
	Collection string `json:"collection"`
	// Since is an advisory resume watermark, mirroring the single-scope
	// stream's first text frame. The hub does not replay history; a gap is
	// reconciled with a fresh snapshot.
	Since string `json:"since,omitempty"`
}

// MultiplexCommand is a client→server text frame on the multiplexed stream.
type MultiplexCommand struct {
	// Type is "subscribe" or "unsubscribe".
	Type   string           `json:"type"`
	Scopes []MultiplexScope `json:"scopes"`
}

// SubscribeMultiplexHandler serves ONE WebSocket connection carrying delta
// streams for many scopes. Browsers cap and tax per-connection fan-out — a
// screen binding N collections must not cost N sockets — so the client
// subscribes and unsubscribes topics with JSON text frames and the gateway
// fans every subscribed scope's binary delta frames into the single
// connection. Frames need no extra envelope: every RecordMutation already
// carries its organization/domain/collection, so the client routes by scope
// from the payload itself. Resync notices are tagged with the scope they
// belong to.
func (g *Gateway) SubscribeMultiplexHandler(config HandlerConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant, err := config.tenant()(r)
		if err != nil {
			writeError(w, err)
			return
		}
		conn, err := projectionUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		// gorilla connections allow one concurrent writer; every scope pump and
		// the reader-driven error notices serialize through writeFrame.
		var writeMu sync.Mutex
		writeFrame := func(messageType int, payload []byte) error {
			writeMu.Lock()
			defer writeMu.Unlock()
			return conn.WriteMessage(messageType, payload)
		}
		writeControl := func(frame ControlFrame) error {
			payload, err := json.Marshal(frame)
			if err != nil {
				return err
			}
			return writeFrame(websocket.TextMessage, payload)
		}

		var subsMu sync.Mutex
		subs := map[string]*Subscription{}
		defer func() {
			subsMu.Lock()
			for _, sub := range subs {
				sub.Cancel()
			}
			subsMu.Unlock()
		}()

		startScope := func(domain, collection string) {
			// The allowlist gates subscribe frames exactly like path scopes: a
			// public mount must not stream a scope it does not publish.
			if !config.Allowlist.Allows(domain, collection) {
				_ = writeControl(ControlFrame{Type: ControlError, Reason: ErrScopeForbidden.Error(), Domain: domain, Collection: collection})
				return
			}
			scope := &foundationpb.ProjectionScope{TenantId: tenant, Domain: domain, Collection: collection}
			key := domain + "/" + collection
			subsMu.Lock()
			if _, exists := subs[key]; exists {
				subsMu.Unlock()
				return
			}
			// g.Subscribe validates the scope; a bad scope (e.g. an empty
			// collection) is answered with a scoped ControlError rather than
			// tearing the connection down.
			sub, err := g.Subscribe(scope)
			if err != nil {
				subsMu.Unlock()
				_ = writeControl(ControlFrame{Type: ControlError, Reason: err.Error(), Domain: domain, Collection: collection})
				return
			}
			subs[key] = sub
			subsMu.Unlock()

			go func() {
				var lastDropped, lastEpoch uint64
				emitResyncIfDropped := func() error {
					if dropped := sub.Dropped(); dropped > lastDropped {
						lastDropped = dropped
						return writeControl(ControlFrame{
							Type:       ControlResync,
							Reason:     "slow-consumer",
							Epoch:      lastEpoch,
							Dropped:    dropped,
							Domain:     domain,
							Collection: collection,
						})
					}
					return nil
				}
				for {
					select {
					case <-ctx.Done():
						return
					case <-sub.Drops():
						if err := emitResyncIfDropped(); err != nil {
							cancel()
							return
						}
					case frame, ok := <-sub.Frames:
						if !ok {
							return
						}
						lastEpoch = frame.Epoch
						if err := emitResyncIfDropped(); err != nil {
							cancel()
							return
						}
						if err := writeFrame(websocket.BinaryMessage, frame.Envelope); err != nil {
							cancel()
							return
						}
					}
				}
			}()
		}
		stopScope := func(domain, collection string) {
			key := domain + "/" + collection
			subsMu.Lock()
			if sub, ok := subs[key]; ok {
				sub.Cancel()
				delete(subs, key)
			}
			subsMu.Unlock()
		}

		// Reader loop: control commands in, connection liveness out. Any read
		// error (including client close) tears the whole stream down.
		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				cancel()
				return
			}
			if messageType != websocket.TextMessage {
				continue
			}
			var cmd MultiplexCommand
			if err := json.Unmarshal(payload, &cmd); err != nil {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(cmd.Type)) {
			case "subscribe":
				for _, s := range cmd.Scopes {
					startScope(strings.TrimSpace(s.Domain), strings.TrimSpace(s.Collection))
				}
			case "unsubscribe":
				for _, s := range cmd.Scopes {
					stopScope(strings.TrimSpace(s.Domain), strings.TrimSpace(s.Collection))
				}
			}
		}
	})
}

// ControlType labels a projection control frame. Control frames are text frames
// (distinct from the binary delta frames) carrying out-of-band stream state.
const (
	// ControlResync tells the client its stream has gaps (frames were dropped
	// for a slow consumer) and it must reconcile from a fresh snapshot.
	ControlResync = "resync"
	// ControlError reports a rejected control command (e.g. an invalid scope
	// in a multiplexed subscribe) without tearing the connection down.
	ControlError = "error"
)

// ControlFrame is the JSON shape of a projection WebSocket control message.
// Domain/Collection scope the notice on the multiplexed stream; they are empty
// on the single-scope stream, where the connection itself names the scope.
type ControlFrame struct {
	Type       string `json:"type"`
	Reason     string `json:"reason,omitempty"`
	Epoch      uint64 `json:"epoch,omitempty"`
	Dropped    uint64 `json:"dropped,omitempty"`
	Domain     string `json:"domain,omitempty"`
	Collection string `json:"collection,omitempty"`
}

// sendResync emits a resync control frame as a WebSocket text message. The
// client distinguishes control frames (text/string) from delta frames (binary).
func sendResync(conn *websocket.Conn, epoch, dropped uint64) error {
	payload, err := json.Marshal(ControlFrame{
		Type:    ControlResync,
		Reason:  "slow-consumer",
		Epoch:   epoch,
		Dropped: dropped,
	})
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, payload)
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUnauthenticated):
		http.Error(w, err.Error(), http.StatusUnauthorized)
	case errors.Is(err, ErrScopeForbidden):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrScopeInvalid):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}
