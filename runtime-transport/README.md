# runtime-transport

`runtime-transport` is the shared client-side transport layer planned for extraction from converged app implementations.

It owns:

1. event envelope creation
2. binary event envelope contracts for internal and realtime transport
3. route-manifest lookup and RBAC-aware resolution
4. transport strategy composition (`ws -> http`, `wasm -> http`, `http only`)
5. runtime diagnostics hooks
6. context-scoped request replay/coalescing helpers and loading-state utilities for frontend command stores
7. runtime session-id helpers that keep bearer-looking values out of replay keys and shared metadata context

Current transport contract posture:

1. foundation-owned protobuf transport envelopes now live under [foundation/runtime-transport/protos/transport/v1](/Users/okhai/Desktop/OVASABI%20STUDIOS/reframe_v1/foundation/runtime-transport/protos/transport/v1)
2. `server-kit` uses that binary envelope for internal Redis/pubsub event traffic
3. payloads remain `JSON-in-bytes` by default in the current phase
4. websocket/client traffic now supports protobuf-binary envelopes end to end
5. authenticated apps can prefer `ws -> http` without widening guest allowsets by using a narrow connection-auth upgrade on the same socket
6. typed protobuf request/response dispatch is available in the shared Go registry/binding layer and the shared frontend command bus

Planned public surface:

1. `createEnvelope`
2. `createCommandBus`
3. `createRouteRegistry`
4. `canDispatch`
5. `resolveRoute`
6. envelope binary codecs
7. transport diagnostics adapters
8. event-store helpers for replay-safe reads, mutation opt-in dedupe, and scoped loading keys
9. metadata/session helpers for identity-safe runtime context

It does not own:

1. app store state
2. auth policy decisions
3. guest/public route allowlists
4. domain event names

Extraction gate:

1. `field_os` and `reframe_v1` must share the same route-registry and command-bus interface.
2. `fintech_v1` must be adapted to the same transport contract.
