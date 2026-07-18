// Package discovergw mounts a public, anonymous, real-time read surface over a
// projectiongw.Gateway: the "protected yet public" pattern. It is a thin,
// named wrapper — the mechanism is entirely projectiongw (PublicTenantFunc +
// ScopeAllowlist over the same gateway/hub/hermes read path that serves
// authenticated projections); this package exists so scaffolded projects
// discover the pattern and cannot mis-assemble it.
//
// The safety model is fail-closed by construction:
//
//   - One fixed public organization. Every request resolves to it; no request
//     input can steer the tenant. An empty organization disables the mount.
//   - A mandatory scope allowlist. Only explicitly published
//     {domain}/{collection} scopes are served — path reads, single-scope
//     subscriptions, and multiplexed subscribe frames alike. NewHandler
//     rejects an empty allowlist rather than defaulting open.
//   - Field policy lives in the projector, not here. Publish dedicated public
//     collections materialized with only allowlisted fields; what was never
//     materialized cannot leak. (The same philosophy as an explicit SELECT
//     list: the projection definition is the allowlist.)
//
// Everything else — snapshots, the multiplexed WebSocket delta stream, resync
// on drop, lazy scope warming, bounded per-scope memory — is inherited from
// the gateway, so anonymous crowds get the same live machinery as signed-in
// users.
package discovergw

import (
	"errors"
	"net/http"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/projectiongw"
)

// DefaultPathPrefix is where the public discovery surface mounts.
const DefaultPathPrefix = "/v1/discover/"

var (
	// ErrNilGateway is returned when no gateway is supplied.
	ErrNilGateway = errors.New("discovergw: gateway is required")
	// ErrNoOrganization is returned when the public organization is empty. A
	// public mount with no organization is disabled, not defaulted.
	ErrNoOrganization = errors.New("discovergw: public organization is required")
	// ErrEmptyAllowlist is returned when no scopes are published. A public
	// mount is fail-closed: it serves exactly what is listed, so an empty
	// list is a wiring error, not "allow everything".
	ErrEmptyAllowlist = errors.New("discovergw: at least one published scope is required")
)

// Config wires the public discovery mount.
type Config struct {
	// PublicOrganizationID is the one tenant whose published projections this
	// mount serves to everyone. Required.
	PublicOrganizationID string
	// Scopes is the published {domain}/{collection} allowlist. Required and
	// non-empty; scopes outside it are rejected with 403 / a scoped control
	// error on the multiplexed stream.
	Scopes projectiongw.ScopeAllowlist
	// PathPrefix defaults to DefaultPathPrefix.
	PathPrefix string
}

func (c Config) prefix() string {
	if strings.TrimSpace(c.PathPrefix) != "" {
		return c.PathPrefix
	}
	return DefaultPathPrefix
}

// NewHandler returns the public discovery handler for gateway, to be mounted
// at Config.PathPrefix (register with a trailing-slash pattern so subpaths
// route here). It is the projectiongw Handler — snapshot reads, single-scope
// WS at {prefix}{domain}/{collection}, multiplexed WS at the prefix root —
// scoped to the public organization and allowlist.
func NewHandler(gateway *projectiongw.Gateway, cfg Config) (http.Handler, error) {
	if gateway == nil {
		return nil, ErrNilGateway
	}
	if strings.TrimSpace(cfg.PublicOrganizationID) == "" {
		return nil, ErrNoOrganization
	}
	if len(cfg.Scopes) == 0 {
		return nil, ErrEmptyAllowlist
	}
	return gateway.Handler(projectiongw.HandlerConfig{
		Tenant:     projectiongw.PublicTenantFunc(cfg.PublicOrganizationID),
		PathPrefix: cfg.prefix(),
		Allowlist:  cfg.Scopes,
	}), nil
}
