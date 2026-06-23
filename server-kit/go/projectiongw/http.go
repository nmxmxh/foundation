package projectiongw

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

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

// TenantFunc derives the authenticated tenant (organization) for a request. It
// is the trust boundary: the scope tenant comes from authenticated context, not
// from client-supplied path, header, or query, so a client cannot read another
// tenant's projection (TenantScopeStable).
type TenantFunc func(r *http.Request) (string, error)

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
	return scope, nil
}

// Handler returns a single http.Handler for {prefix}{domain}/{collection} that
// dispatches a WebSocket upgrade to the delta subscription and any other request
// to the snapshot reader. Mount it at the path prefix (e.g. "/v1/projections/").
func (g *Gateway) Handler(config HandlerConfig) http.Handler {
	snapshot := g.SnapshotHandler(config)
	subscribe := g.SubscribeHandler(config)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
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

// ControlType labels a projection control frame. Control frames are text frames
// (distinct from the binary delta frames) carrying out-of-band stream state.
const (
	// ControlResync tells the client its stream has gaps (frames were dropped
	// for a slow consumer) and it must reconcile from a fresh snapshot.
	ControlResync = "resync"
)

// ControlFrame is the JSON shape of a projection WebSocket control message.
type ControlFrame struct {
	Type    string `json:"type"`
	Reason  string `json:"reason,omitempty"`
	Epoch   uint64 `json:"epoch,omitempty"`
	Dropped uint64 `json:"dropped,omitempty"`
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
	case errors.Is(err, ErrScopeInvalid):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}
