// Package bootstrap wires project-owned domain services into foundation dispatch.
package bootstrap

import (
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/httpapi"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
)

// Services is the project-owned domain service container — the single place a
// project declares its HTTP surface. Foundation keeps infrastructure wiring in
// server-kit (httpserver, startup); the project exposes its handlers and routes
// here, and both the server (SetHTTPRoutes) and docgen (OpenAPI) consume them.
// Application domains extend this type using Foundation handler/route types; do
// not add local wrapper or adapter layers.
type Services struct{}

// AllHandlers returns the event/route handlers owned by the project.
func (s *Services) AllHandlers() map[string]any {
	return map[string]any{}
}

// HTTPRoutes returns the project's HTTP route catalogue — the single source of
// truth for both the running server and generated OpenAPI. The default derives
// routes generically from AllHandlers; domains with explicit per-service routes
// override this by aggregating their service GetHTTPRoutes() (richer methods,
// paths, and schemas). Keeping it here means a project's HTTP surface lives in
// one package, not split across an internal/server route file.
func (s *Services) HTTPRoutes() []registry.HTTPRoute {
	return httpapi.RoutesFromHandlerMap(s.AllHandlers())
}

// RouteCatalog is the instance-free route catalogue for docgen (same routes as
// Services.HTTPRoutes, without constructing live dependencies). Domains that
// make Services stateful keep this arg-free by sourcing handlers from a
// dependency-free constructor.
func RouteCatalog() []registry.HTTPRoute {
	return (&Services{}).HTTPRoutes()
}
