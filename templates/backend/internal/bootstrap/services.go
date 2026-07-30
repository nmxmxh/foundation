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
//
// This map *declares* the surface: its keys become the HTTP route table, the
// OpenAPI document, and the generated frontend command list. Declaring an event
// here does not install a dispatch handler for it — that is a separate call to
// ServiceRegistry.Register or RegisterTypedWithOptions — so the two must agree.
//
// They are reconciled for you: httpserver.Server.Run refuses to start when an
// advertised route has no registered handler, and names the events. Two failures
// this closes, both of which have happened in practice:
//
//   - An event declared here with no handler registered. The route is published
//     in the catalogue and the docs, and answers handler_not_found on a URL the
//     server itself advertised.
//   - The same event name claimed twice on the registry. Registration is
//     first-claim-wins and returns registry.ErrDuplicateEventType; do not discard
//     that error, because the alternative is selecting a handler by registration
//     order. One service shipped a ranked and an unranked read path under one
//     event name this way — the unranked one won, and the ranking subsystem was
//     unreachable while all of its own tests passed.
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
