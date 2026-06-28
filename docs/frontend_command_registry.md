# Frontend Command Registry

Status: v1.0
Date: 2026-06-24
Owner: Platform Architecture

## Purpose

The client dispatch path — `client → RuntimeEnvelope → createCommandBus →
resolveRoute → :requested → terminal → projection` — needs a per-app
`RuntimeRoute[]` to resolve an `eventType` to a `{method, path, requiredCapability,
permission}` and to gate dispatch with `canDispatch`. The action hook itself
(`createCommandBus`, `createRouteRegistry`, `createHTTPTransport`, the WS
strategy) already ships in `@ovasabi/runtime-transport`. The only missing input
is the route list, and it must not drift from the server.

## Single source of truth

`RuntimeRoute` maps 1:1 to the backend `registry.HTTPRoute`. The derivation of
method/path/capability/permission lives authoritatively in Go
(`server-kit/go/httpapi/catalog.go` + `security.CapabilityFromEvent` /
`PermissionFromEvent`). Re-deriving it in JavaScript would duplicate that logic
and, worse, would **miss hand-registered non-event routes** — for example the
transfer/upload routes, whose custom paths and `HEAD`/`PATCH` verbs are not
derivable from a proto event pair.

So the registry is generated from the routes the server actually registers:

```
bootstrap.RouteCatalog() []registry.HTTPRoute        (app: the authoritative set)
        │  httpapi.MarshalRouteCatalog(...)            (server-kit: stable JSON projection)
        ▼
docs/references/lifecycle/route_catalog.json          (machine artifact, committed)
        │  generate_frontend_commands.mjs              (tooling: pure JSON → TS transform)
        ▼
frontend/src/generated/runtimeRoutes.ts               (RuntimeRoute[] + createAppRouteRegistry)
```

`route_catalog.json` is emitted by `docgen route-catalog` (which reuses the same
`bootstrap.RouteCatalog()` the OpenAPI generator and the live server use), so
there is exactly one place that knows the truth.

## Generated output

`runtimeRoutes.ts` exports:

- `runtimeRoutes: readonly RuntimeRoute[]` — the registry data.
- `createAppRouteRegistry(): RouteRegistry` — wraps `createRouteRegistry`.
- `AppEventType` — a string-literal union of every registered event type, so a
  command's `eventType` is checked at compile time.

The app wires it into the bus (matching the "generate data, app wires the bus"
split used by the prototype runtime generator):

Use `createAppRuntime` — the thin facade that wires the registry, transports,
and command bus into one ergonomic `dispatch(eventType, payload)`. It replaces
hand-rolled runtime seams that re-derive method/path from the eventType in TS
(which drifts from `catalog.go`): the bus resolves the real route — custom paths
included — from the generated registry.

```ts
import { createAppRuntime, createHTTPTransport, createWebSocketTransport } from "@ovasabi/runtime-transport";
import { createAppRouteRegistry } from "@/generated/runtimeRoutes";

export const runtime = createAppRuntime({
  registry: createAppRouteRegistry(),
  strategies: [
    // Actor scope (auth, tenant/edition, role) rides the transport's getHeaders —
    // configured once here, never per command.
    createHTTPTransport({ baseUrl, getHeaders }),
    createWebSocketTransport({ url: wsUrl }),
  ],
  grantedCapabilities, // client UX gate only; the server (CP-20) is the authority
  hasPolicyAccess,
});

// App command namespaces become one-line typed helpers:
export const commands = {
  menu: {
    publishItem: (payload: PublishItemInput) =>
      runtime.dispatch("menu:publish_item:v1:requested", payload),
  },
};
```

The lower-level `createCommandBus` / `createRouteRegistry` remain available for
advanced cases; `createAppRuntime` is the recommended default.

**Templated routes need no call-site plumbing.** When a registered route has
`{param}` segments (e.g. `marketplace:get_order` → `/v1/marketplace/orders/{order_id}`),
the HTTP transport resolves each `{param}` from the payload field of the same
name, percent-encodes it, and drops that key from the JSON body / query string so
it is never sent twice — the server re-derives path params from the URL anyway. A
missing or non-scalar path-param value fails fast rather than emitting a broken
URL. So the call site is simply `commands.marketplace.getOrder({ order_id })`.

## Timing and enforcement

Generation runs at contract-generation time, alongside the proto-derived
generators:

- App: `make communication-contracts` runs `frontend-commands` (emit catalog →
  generate `runtimeRoutes.ts`).
- Core: `make generate-contracts` runs `generate_frontend_commands.mjs`, which
  cleanly skips when no catalog is present (Foundation core has no app routes).

The generator supports `--check` (byte-stale detection), wired into the
centralized lint suite as `check-frontend-commands-generator`, so a stale
`runtimeRoutes.ts` fails CI with "run: make generate-contracts" — the same
staleness contract as `lifecycle_contract.json` and `prototypeRuntime.ts`.

## Testing posture

- `server-kit/go/httpapi` `route_catalog_test.go`: projection + sort + dedup,
  incomplete-route skipping, custom non-event route capture (the transfer route),
  and stable/round-trippable marshaling.
- `tests/frontend_commands_generator_test.sh`: generate + idempotent `--check` +
  stale detection, custom-route presence, empty catalog (`AppEventType = never`),
  missing-catalog skip, and invalid-permission rejection.

See also: `foundation_nervous_system.md` (the canonical dispatch path) and
`transfer_lane.md` (a custom route source the proto path cannot derive).
