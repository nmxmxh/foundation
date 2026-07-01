# The State Event Model: Commands, Events, Projections, and Stores

Status: 0.0.1
Date: 2026-07-01
Owner: Platform Architecture

## Purpose

This document explains how Foundation handles **state** end to end — from a user
action in the browser to durable truth and back to the screen — under one model:
**everything is a state event.** It ties together four concepts that are often
separate frameworks in other stacks:

- **Commands** — a request to change state.
- **Events** — the immutable record that state changed.
- **Projections** — read-optimized views derived from events (Hermes).
- **Stores** — the frontend's local, visual projection (Zustand).

For the philosophy behind this model, see [`PHILOSOPHY.md`](PHILOSOPHY.md). For
the lifecycle invariants, see
[`foundation_nervous_system.md`](foundation_nervous_system.md). For projection
internals, see [`hermes_hotplane.md`](hermes_hotplane.md). For how the frontend
route registry is generated, see
[`frontend_command_registry.md`](frontend_command_registry.md).

## The one atom: a state event

A mutation is defined **once** as a Protobuf/Cap'n Proto contract, not as a
database row, a JSON body, and a frontend type that must be kept in sync by hand.
That single contract becomes:

| Layer | What the event becomes | Owner module |
| --- | --- | --- |
| Command dispatch | a `RuntimeEnvelope` carrying `eventType` + payload + metadata | `runtime-transport` |
| Durable truth | a row written through the database executor lane | `server-kit/go/database` |
| History | a lifecycle event `<domain>:<action>:vN:requested → :success/:failed` | `server-kit/go/events`, `eventlog` |
| Projection | an applied mutation in a node-local read model | `server-kit/go/hermes` |
| Frontend store | a snapshot the UI subscribes to | `runtime-transport` + `frontend-kit` |

The same envelope shape carries through every layer. There is no DTO tier and no
hand-written translation between these representations.

## The round trip

```text
1. UI calls dispatch(eventType, payload)
       │  runtime-transport wraps it in a RuntimeEnvelope (+ metadata)
2. Route registry resolves eventType -> {method, path, capability, permission}
       │  generated from the server's real routes (no client re-derivation)
3. Backend validates auth, tenant, correlation, idempotency
4. Domain handler writes durable truth to Postgres   ← source of truth
5. Lifecycle events emitted: :requested -> :success / :failed
6. Worker / Redis Stream projects the change
7. Hermes applies it to a node-local projection (atomic pointer swap)
8. WebSocket / epoch signal notifies subscribers
9. Frontend store updates; useSyncExternalStore re-renders only on change
```

Each numbered step is owned by the substrate. The developer writes steps **3–4**
(the transition) and step **9**'s view binding (the projection). Everything else
is generated or inherited.

## Commands: how a change is requested

A command is a typed dispatch, not a bespoke REST call:

```ts
import { createAppRuntime } from "@ovasabi/runtime-transport";
import { createAppRouteRegistry } from "@/generated/runtimeRoutes";

export const runtime = createAppRuntime({
  registry: createAppRouteRegistry(),
  strategies: [/* HTTP + WebSocket transports */],
});

// eventType is compile-time checked against AppEventType (generated).
await runtime.dispatch("order:create:v1", { sku, quantity });
```

- `eventType` is checked at compile time against the generated `AppEventType`
  union, so a typo or a route that does not exist on the server fails the build.
- The route — including hand-registered non-event routes like transfer/upload —
  comes from the generated registry, which is derived from
  `server-kit/go/httpapi/catalog.go`. The client never re-derives method/path.
- Actor scope (auth, tenant, role) rides the transport headers, configured once,
  never per command. The backend derives tenant from the authenticated context
  (`auth.OrgIDFromContext`), never from the payload.

## Events: the immutable record

Every mutating command produces a lifecycle bookended by terminal events:

```text
<domain>:<action>:vN:requested   →   :success   (or)   :failed
```

These events are the system's history. They are append-only, carry the
correlation ID and tenant scope, and are the causal source of every projection
update (`ProjectionAfterTerminal` invariant). Because state is a function of this
stream, the event log is also the reproduction: replay a tenant's events to
reproduce a bug with full fidelity.

## Projections: read models without database pressure

A projection is a view **derived** from events, never a second source of truth.
Hermes is the node-local projection layer:

- It applies committed mutations into bounded, indexed, in-memory read models.
- Reads are lock-free: a reader loads an immutable snapshot via an atomic pointer
  and never blocks a writer (see [`hermes_hotplane.md`](hermes_hotplane.md) and
  the model-checked spec `specs/tla/HermesProjectionPublish.tla`).
- It is **not** the source of truth. Postgres decides; Hermes remembers what this
  node needs right now. If a projection is stale or degraded, the read falls back
  to Postgres and records the fallback.

Read modes (`fenced`, `live`, `stale_while_revalidate`, `postgres_required`) let
a caller choose its freshness/latency trade-off explicitly — for example a
read-your-write after a command uses a `fenced` read against the watermark it
just advanced. See [`hermes_read_modes.md`](hermes_read_modes.md).

**Never project for writes, transactional integrity, or financial
reconciliation.** Those stay on the SQL layer.

## Stores: the frontend's visual projection

The frontend store is just another projection — the *visual* one. It is fed by
the same events over WebSocket/HTTP and exposes a snapshot the UI subscribes to:

- Subscribe to epoch/runtime signals rather than polling component timers.
- Expose snapshots through `useSyncExternalStore` so React re-renders only when
  the snapshot actually changes.
- Compare the local watermark against the backend epoch to know whether the view
  is stale — temporal coordination comes from the contract, not ad-hoc logic.
- Degrade cleanly when IndexedDB, SharedArrayBuffer, or workers are unavailable.

See [`frontend_runtime_workbench.md`](frontend_runtime_workbench.md) and
[`frontend-kit/README.md`](../frontend-kit/README.md) for the store/runtime
adapters.

## What the developer actually writes

Three things; the substrate owns the rest:

1. **The schema** — the event contract (Protobuf/Cap'n Proto).
2. **The transition** — the domain handler that validates and writes durable
   truth, emitting the lifecycle events.
3. **The projection/view** — how the UI store renders the resulting state.

Routing, transport, serialization, caching, multi-tenancy, projection, and
persistence are inherited. If you find yourself writing a translation layer — a
DTO, a bespoke JSON encoder, a client-side route table — stop: that is a sign you
are working against the model rather than with it.

## Invariants this model relies on

From [`foundation_nervous_system.md`](foundation_nervous_system.md):

- `MetadataPreserved` — correlation, tenant, idempotency survive every lane.
- `TenantScopeStable` — tenant derived from auth, stable across command, job,
  event, and projection.
- `RequestedBeforeTerminal` / `ExactlyOneTerminalVisible` — clean lifecycle.
- `ProjectionAfterTerminal` — projections are causally tied to terminal events
  (or an explicitly documented optimistic UI state).
- `FallbackRefinement` — every optimized lane (binary, WS, Redis, WASM, JSON
  compatibility) preserves the same command semantics.

## Common mistakes

- Re-deriving a route's method/path in TypeScript instead of using the generated
  registry — it drifts from `catalog.go`.
- Reading from Hermes on a path that needs transactional truth.
- Updating a frontend store from a raw API response instead of the projected
  event stream, so the store and the backend diverge under reconnect/replay.
- Trusting a client-supplied tenant/org field instead of the authenticated
  context.
- Adding an optimistic UI update with no documented reconciliation against the
  terminal event.
