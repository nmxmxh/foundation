# Runtime Foundation

Date: 2026-04-24

This document records the runtime foundation posture for this scaffold.

## Control-plane foundations

1. Go owns HTTP ingress, orchestration, schedule/publish workflow state, queue registration, and OpenAPI generation.
2. PostgreSQL is the durable system of record and follows the fixed three-group migration structure.
3. Redis is ephemeral only: event fanout, coordination, and cache support.
4. Media artifacts are expected to live in S3-compatible object storage from day one, with private-by-default buckets and signed or mediated access.
5. Route metadata drives RBAC posture, OpenAPI generation, and the frontend route manifest.
6. Service-registry listeners should fan in Redis/pubsub traffic to blocking worker pools instead of using sleep-based polling loops.
7. Handler registration should apply bounded concurrency through a shared execution controller so saturation behavior is explicit and measurable.
8. Externally reachable handlers must fail closed on missing identity, organization scope, integrity metadata, or route contract validation.

## Hostile-environment security posture

1. Assume anonymous internet users, authenticated users, cross-tenant adversaries, malicious webhook/API callers, compromised browsers, and limited insiders.
2. Trust boundaries include browser <-> API, websocket upgrade/auth, worker <-> main thread, Redis/pubsub, object storage/CDN, third-party webhooks, and native runtime transports (`ffi`, `stdio`, `shm`).
3. Sensitive assets include session tokens, organization-scoped data, admin/publish/billing actions, signed URLs, object keys, queue commands, and audit trails.
4. Each boundary must validate identity, tenant scope, payload size, and contract integrity before dispatch.

## Hot-path foundations

1. Browser worker execution is isolated under `frontend/src/runtime/workers`.
2. `SharedArrayBuffer` requires cross-origin isolation headers in dev and preview serving.
3. The shared runtime buffer contract is a 4KB hot control plane defined in `foundation/runtime-sdk/protocols/system/v1/runtime_buffer.capnp`.
4. Large payloads use transferable buffers, binary transport envelopes, or the optional `RuntimeSharedArena` defined in `foundation/runtime-sdk/protocols/system/v1/runtime_shared_arena.capnp`.
5. The browser runtime now uses a generic role-based worker split:
   - `pulse` watches and drives runtime epochs
   - `compute` owns the preview execution unit
6. Rust/WASM reads and writes serialized Cap'n Proto messages inside the runtime buffer regions and increments epoch counters.
7. The UI reads the output region through generated `capnp-es` readers instead of manual offset mapping or ad hoc JS payloads.
8. Frontend request replay/coalescing must be scoped by runtime identity context (session/user/org) so read caches stay safe across auth and org switches.
9. Frontend loading state should use scoped reference counts rather than a single boolean when multiple concurrent commands can overlap.
10. `SharedArrayBuffer` deployments must intentionally pair `COOP` + `COEP` and compatible asset policies (`CORP`/CORS) so cross-origin isolation is stable and auditable.
11. Generated readers/writers and route contracts are the allowed parsing path for runtime payloads. Unknown or oversized frames must be rejected before render or storage flows.
12. DOM mutation watching is not a foundation data-flow primitive. Prefer explicit stores, route contracts, and worker/runtime messages over `MutationObserver`.
13. Main-thread code must not call blocking `Atomics.wait`; workers own blocking waits and main-thread code uses `Atomics.waitAsync` or message fallback when needed.
14. If DOM observation is unavoidable, keep it inside a narrow UI adapter and prefer `ResizeObserver` or `IntersectionObserver` before `MutationObserver`.

## Browser WASM build and binding flow

1. `scripts/wasm_codegen.sh` builds `rust/crates/reframe-preview-wasm` for `wasm32-unknown-unknown` in release mode.
2. That script copies the raw artifact into `frontend/src/runtime/wasm/generated/reframe_preview_wasm.wasm`.
3. `frontend/src/runtime/wasm/reframePreview.ts` imports the wasm file with Vite's `?url` loader and instantiates it through `BrowserRuntimeHost.instantiate(...)`.
4. `BrowserRuntimeHost` provides the low-level `env` imports:
   - copy bytes from wasm linear memory into the shared runtime buffer
   - copy bytes back out of the shared runtime buffer
   - atomic epoch operations
   - logging and timing hooks
5. `probeWorker.ts` runs the compute unit off the UI thread. The main thread owns the `SharedArrayBuffer`, the worker writes the input contract, Rust/WASM executes `ovrt_process_preview`, and the UI reads the output Cap'n Proto payload.
6. Frontend production build does not compile Rust itself. `yarn --cwd frontend build` first runs `scripts/wasm_codegen.sh`, then Vite bundles the emitted wasm asset.

## Frontend boot and recovery posture

1. Public/landing routes should keep the full authenticated app shell lazy-loaded so marketing entry points avoid paying for dashboard-only stores and runtime wiring.
2. Asset warmup should be route-family aware and should honor offline status and browser save-data preferences before prefetching.
3. Runtime bridge state that must survive HMR, code splitting, or shell remounts should live on `window`/`globalThis` or an equivalent module singleton.
4. Chunk-load and dynamic-import failures should be treated as stale-build recovery signals:
   - refresh service workers
   - clear app-shell caches
   - reload once with a guarded cooldown
5. Read response replay is the default only for replay-safe request families; mutation dedupe must be explicit.
6. Sensitive bootstrap or authenticated responses should avoid shared-cache reuse and prefer `no-store` unless an identity-safe cache strategy is explicitly documented.

## Native host binding and zero-copy posture

1. Backend does not use browser `SharedArrayBuffer` directly. Go and Rust communicate through the vendored `runtime-sdk` native host lane.
2. `foundation/runtime-sdk/go/runtimehost` allocates a fixed runtime buffer with the same region layout concepts used in the browser:
   - epoch slots
   - header ints
   - input region
   - output region
   - diagnostics region
3. Go writes Cap'n Proto input bytes into the input region and dispatches the buffer through a foundation-owned runtime transport.
4. Rust reads the same buffer layout in `ovrt-native`, runs the registered unit, writes the output bytes back into the output region, and advances the output epoch.
5. This is "SAB discipline on native", not literal shared browser memory:
   - inside each host process the runtime uses fixed offsets and no JSON mapping on the hot path
   - cross-process transports still copy at transport boundaries, while in-process FFI can mutate the runtime buffer directly
6. Native transport now has three lanes:
   - `ffi`: trusted in-process ABI for the fastest host path
   - `stdio`: portable framed-stdio buffer exchange
   - `shm`: Linux-first shared-file transport under `/dev/shm`, with control frames over stdio and the runtime buffer living in shared memory
7. The shared-memory lane is foundation-owned and app-agnostic:
   - Go chooses it through runtimehost transport options
   - Rust host selects it through `OVRT_RUNTIME_TRANSPORT=shm`
   - app code still dispatches through the same `MediaRuntime` boundary
8. The FFI lane is also foundation-owned and app-agnostic:
   - Go loads a runtime library through `runtimehost.NewFFIPool`
   - app Rust crates expose the generic ABI through `ovrt-ffi`
   - app code still dispatches through the same `MediaRuntime` boundary
9. This gives a hybrid native posture:
   - `stdio` for safest portability
   - `shm` for isolated Linux-first throughput
   - `ffi` for trusted zero-copy control-buffer execution and maximum per-core throughput
10. `ffi` is a trusted-only lane. Do not load arbitrary runtime libraries or allow user-controlled module/unit selection.
11. `shm` and `stdio` lanes must enforce frame-size limits, same-host permissions, and explicit allowlists for callable units.

## Compression posture

1. HTTP egress now prefers Brotli when the client advertises `br`, then falls back to gzip, then identity.
2. HTTP ingress now accepts Brotli, gzip, and deflate request bodies and normalizes them before handler dispatch.
3. WebSocket binary frames keep identity protobuf as the compatibility path and use explicit `OVRT` binary compression frames only when compression is enabled and the payload shrinks.
4. Compression is transport-level only:
   - durable artifacts in object storage are stored as app artifacts, not automatically recompressed network blobs
   - the server decides response compression from `Accept-Encoding`
5. Frontend production assets already emit `.br` and `.gz` variants during Vite build, but serving those variants still depends on the deployment edge or static file server honoring them.
6. Responses that reflect attacker-controlled input together with secrets or one-time tokens should avoid compression when side-channel exposure is plausible.

## Change-risk posture

1. Complexity limits are necessary but insufficient; app CI should pair complexity with coverage to identify CRAP-style hotspots before merges.
2. New code should target line coverage >= 80%, branch coverage >= 60%, and hotspot scores below the high-risk threshold where the stack can calculate them.
3. Hot-path changes are not complete until both regression tests and hotspot review show the code is safe to modify.

## Binary event transport posture

1. Foundation event transport now follows the Phase A shape from `fintech_v1/docs/transition_full_binary_pipeline.md`.
2. The canonical internal event envelope lives under `foundation/runtime-transport/protos/transport/v1`.
3. `server-kit/events` now publishes Redis/pubsub traffic as protobuf-binary envelopes, not JSON text.
4. Payload strategy is still `JSON-in-bytes` by default:
   - transport envelope is binary protobuf
   - event payload body remains JSON bytes for compatibility with existing service handlers
5. Consumers use dual decode for the transition:
   - try protobuf envelope first
   - fall back to legacy JSON envelope
6. WebSocket/client traffic now uses the same protobuf envelope family as the internal bus.
7. The frontend runtime client uses an authenticated websocket upgrade path that fits the existing allowset model:
   - guest socket opens and receives `identity:connection_open:v1:ack`
   - if a session access token exists, the client sends `identity:authenticate_connection:v1:requested` over that socket
   - once the socket is authenticated, route transport preference can safely switch to `ws -> http` for mutation paths without opening broad guest access
8. Socket authentication is not sufficient on its own. Privileged subscriptions, commands, and topic joins must re-authorize against current session, user, and organization state after connect.
9. Websocket upgrades must validate allowed origins and close or downgrade sessions when auth state changes or expires.
10. Event envelopes and payload bodies must enforce schema validation, size limits, and replay/idempotency windows before handler dispatch.

## Borrowed patterns

1. From `field_os`: route metadata, docgen, route manifest generation, low-cost observability, and strict migration checks.
2. From `fintech_v1`: environment-driven concurrency defaults, queue budget discipline, lazy shell boot, request replay boundaries, HMR-safe runtime bootstrap, stale-build recovery, and dispatch-worker posture.
3. From `inos_v1`: raw host ABI imports, shared-memory contract thinking, epoch-style signaling vocabulary, worker isolation, and typed buffer layouts.

## Shared foundation posture

1. `runtime-sdk` is extracted now into `foundation` for this scaffold (copy of the canonical `ovasabi_foundation` repo family); it remains the upstream lane for browser/native performance use.
2. `server-kit` is now consumed from `foundation/server-kit` for current builds; the canonical source remains `ovasabi_foundation/server-kit` and should be synced into this copy intentionally.
3. The encompassing app keeps app-specific composition and services under `internal/`, while the canonical backend runtime scaffolding lives in foundation.
4. `runtime-transport`, `config-contracts`, and `ui-minimal` are real shared package families upstream, but apps treat them as convergence targets rather than hard runtime dependencies.
5. The backend posture is hybrid:
   - browser uses workers + WASM
   - backend uses Go orchestration plus native Rust host lanes where performance paths need them

## Explicit non-goals in this cut

1. No mesh or P2P runtime.
2. No live connector integrations.
3. No full server-side transcode or mux pipeline yet. Current native Rust coverage is probe, quality, preview-prepare, and packaged render artifacts.
4. No full nonlinear editor behavior.
