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
5. Payload routing is automatic by policy:
   - `<4KB`: 4KB control buffer
   - `4KB-1MB`: `RuntimeSharedArena` when SAB is available
   - `>1MB`: explicit async stream chunks with backpressure
6. The browser runtime now uses a generic role-based worker split:
   - `pulse` watches and drives runtime epochs
   - `compute` owns the preview execution unit
7. Rust/WASM reads and writes serialized Cap'n Proto messages inside the runtime buffer regions and increments epoch counters.
8. The UI reads the output region through generated `capnp-es` readers instead of manual offset mapping or ad hoc JS payloads.
9. Frontend request replay/coalescing must be scoped by runtime identity context (session/user/org) so read caches stay safe across auth and org switches.
10. Frontend loading state should use scoped reference counts rather than a single boolean when multiple concurrent commands can overlap.
11. `SharedArrayBuffer` deployments must intentionally pair `COOP` + `COEP` and compatible asset policies (`CORP`/CORS) so cross-origin isolation is stable and auditable.
12. Generated readers/writers and route contracts are the allowed parsing path for runtime payloads. Unknown or oversized frames must be rejected before render or storage flows.
13. DOM mutation watching is not a foundation data-flow primitive. Prefer explicit stores, route contracts, and worker/runtime messages over `MutationObserver`.
14. Main-thread code must not call blocking `Atomics.wait`; workers own blocking waits and main-thread code uses `Atomics.waitAsync` or message fallback when needed.
15. If DOM observation is unavoidable, keep it inside a narrow UI adapter and prefer `ResizeObserver` or `IntersectionObserver` before `MutationObserver`.

## Runtime state-machine invariants

The runtime ladder follows the TLA-derived rules in `foundation/docs/tla_architecture_practices.md`: each faster lane is a refinement of the same visible command/event contract.

1. Visible runtime behavior is input contract, output contract, status code, diagnostics, terminal event, and canonical metadata.
2. Hidden runtime state includes buffers, arena descriptors, stream chunks, retries, worker ownership, connection state, and lane-specific transport bookkeeping.
3. `EpochMonotonic`: runtime epoch counters must never move backwards.
4. `OutputAfterInput`: output and diagnostics epochs must correspond to a known input epoch.
5. `MetadataPreserved`: correlation ID, idempotency key, session, user, organization, schema version, and locale must survive lane changes.
6. `FallbackRefinement`: fallback from `sab`, WASM, transferable buffers, WebSocket, HTTP, `ffi`, `shm`, or `stdio` must produce the same accepted domain result or a controlled error class.
7. `OwnedDecodeLifetime`: borrowed views must not outlive the source frame or runtime buffer region.
8. `FrameSizeBound`: oversized frames and payloads must be rejected before decode, render, storage, or worker dispatch.
9. No-op/stuttering steps such as duplicate suppression, empty polls, reconnect attempts, cache hits, or retry waits must not change visible semantics.
10. Runtime parity tests must act as refinement checks, not just byte comparisons.

## Browser WASM build and binding flow

1. `make runtime-bindings` regenerates the shared runtime buffer constants from `foundation/runtime-sdk/protocols/system/v1/*` into the Rust, Go, and TypeScript runtime-sdk packages.
2. `make build-wasm` builds the scaffolded Go WASM compatibility shim from `wasm/`, copies the matching `wasm_exec.js`, optionally optimizes/compresses the artifact, and emits `frontend/public/main.wasm`.
3. `make build-rust-wasm` builds app-owned Rust compute modules from `rust/Cargo.toml` for `wasm32-unknown-unknown`, then copies emitted `.wasm` files into `frontend/public/modules/`. Foundation does not put app-domain compute crates in `runtime-sdk`.
4. `make wasm-manifest` writes `frontend/public/runtime/wasm-manifest.json` so frontend code can discover runtime artifacts through `@ovasabi/frontend-kit` instead of hard-coded paths.
5. Frontend code loads the manifest with `loadWasmManifest(...)`, selects the relevant kernel/module artifact, and instantiates compute units through `BrowserRuntimeHost.instantiate(...)` from `foundation/runtime-sdk/ts/browser-host`.
6. `BrowserRuntimeHost` provides the low-level `env` imports:
   - copy bytes from wasm linear memory into the shared runtime buffer
   - copy bytes back out of the shared runtime buffer
   - atomic epoch operations
   - logging and timing hooks
7. Workers run compute units off the UI thread. The main thread owns the `SharedArrayBuffer`, the worker writes the input contract, Rust/WASM executes the exported compute function, and the UI reads the output Cap'n Proto payload through generated readers.
8. Frontend production builds should consume already-emitted artifacts from `frontend/public`. Rust/WASM generation belongs in Makefile targets (`build-runtime`, `build-rust-wasm`, `wasm-manifest`) so CI and local dev use the same propagation path.

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
12. FFI diagnostics must remain C-compatible and UTF-8 safe. Truncated error buffers must end on a character boundary and always be null-terminated when capacity is non-zero.
13. Native host accounting must use RAII/symmetric cleanup guards for in-flight counters and other state that must be restored on every return path.
14. The Rust unit registry is shared through synchronized interior state and returns `Arc<dyn RuntimeUnit>` handles for concurrent reads. Runtime units themselves must remain `Send + Sync`; any mutable caches inside a unit must use explicit synchronization.

## Runtime parity posture

1. `ParityHarness` compares outputs for the same unit input and reports the first mismatch offset for faster drift diagnosis.
2. Stub runners may test the harness shape only. They do not prove runtime parity.
3. Production parity coverage must compare the lanes the product actually uses: native direct dispatch, FFI buffer mutation, stdio framed buffers, Linux shared-memory transport, and browser worker/WASM where available.
4. Runtime parity tests must compare full buffer state, not just returned payload bytes: status code, output bytes, diagnostics text, and epoch transitions (`IDX_INPUT_WRITTEN`, `IDX_OUTPUT_WRITTEN`, `IDX_PANIC_STATE`, `IDX_DIAGNOSTICS_WRITTEN`).
5. Browser `SharedArrayBuffer` and native shared-memory parity must use `u32`/4-byte-aligned atomic slots only. Go, Rust, and TypeScript hosts must use atomic load/store/add/CAS operations for epoch slots; plain byte-order reads are allowed only for non-shared header and payload regions. Blocking waits stay in workers or native host threads, never on the browser main thread.
6. A generic host runner such as Wasmtime may be useful for isolated tests, but it is not the foundation architecture by itself. The benchmark and parity target is the Ovasabi runtime ladder, not one embedding library.

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
7. Internal same-host performance lanes may use `runtime-sdk` `ffi` or `shm` transports with epoch signaling. gRPC is implemented in `server-kit/go/grpcsvc` for cross-host service-to-service calls and polyglot network boundaries; do not replace network RPC with shared memory unless processes share a host and lifecycle.
8. The default posture is performance-demanding. Managed communication must prefer typed/binary/shared-memory lanes even for ordinary app features; JSON maps are compatibility adapters, not foundation runtime primitives.
9. `grpcsvc.Envelope` is a JSON compatibility path only. New hot service-to-service calls should use generated protobuf messages or `grpcsvc.Frame`, which carries typed event metadata plus raw payload bytes through a compact binary codec.
10. Dynamic `map[string]any` JSON decoding is treated as a boundary adapter cost. Domain code may still accept JSON bytes for compatibility, but runtime transport should keep payloads as bytes until the owning handler validates and decodes them.
11. Registry dispatch keeps protobuf request bytes through typed handlers and defaults typed protobuf responses back to protobuf bytes. HTTP protobuf requests default to protobuf responses when `Accept` is absent.
12. Frontend route dispatch defaults to the performance ladder: `sab -> wasm -> transferable -> ws -> http -> postMessage`, skipping unavailable strategies and falling back only after observable failure.
13. Same-process hot dispatch must use direct frame dispatch (`grpcsvc.NewDirectFrameClient` or equivalent typed in-process call), not gRPC. Current guardrail target is zero allocations for direct dispatch; gRPC is a network/polyglot boundary with materially higher stack cost.
14. Frame codecs expose owned decode and borrowed `FrameView` decode. Use borrowed views for synchronous hot paths that do not retain frame data; use owned `Frame` decode when values escape the incoming buffer lifetime.
15. Parallel operation chains should use `server-kit/go/chain`: independent operations run concurrently, non-critical failures do not block movement, and critical failures cancel the operation context for the rest of the chain.
16. The frontend runtime client uses an authenticated websocket upgrade path that fits the existing allowset model:

    - guest socket opens and receives `identity:connection_open:v1:ack`
    - if a session access token exists, the client sends `identity:authenticate_connection:v1:requested` over that socket
    - once the socket is authenticated, route transport preference can safely switch to `ws -> http` for mutation paths without opening broad guest access

17. Socket authentication is not sufficient on its own. Privileged subscriptions, commands, and topic joins must re-authorize against current session, user, and organization state after connect.
18. Websocket upgrades must validate allowed origins and close or downgrade sessions when auth state changes or expires.
19. Event envelopes and payload bodies must enforce schema validation, size limits, and replay/idempotency windows before handler dispatch.

## Borrowed patterns

1. From `field_os`: route metadata, docgen, route manifest generation, low-cost observability, and strict migration checks.
2. From `fintech_v1`: environment-driven concurrency defaults, queue budget discipline, lazy shell boot, request replay boundaries, HMR-safe runtime bootstrap, stale-build recovery, and dispatch-worker posture.
3. From `inos_v1`: raw host ABI imports, shared-memory contract thinking, epoch-style signaling vocabulary, worker isolation, and typed buffer layouts.

## Shared foundation posture

1. `runtime-sdk` is extracted now into `foundation` for this scaffold (copy of the canonical `ovasabi_foundation` repo family); it remains the upstream lane for browser/native performance use.
2. `server-kit` is now consumed from `foundation/server-kit` for current builds; the canonical source remains `ovasabi_foundation/server-kit` and should be synced into this copy intentionally.
3. The encompassing app keeps app-specific composition and services under `internal/`, while the canonical backend runtime scaffolding lives in foundation.
4. `runtime-transport`, `frontend-kit`, `config-contracts`, and `ui-minimal` are real shared package families. Scaffolded frontends must consume them through local package dependencies, not raw source aliases.
5. App domain schemas remain app-owned under `api/protos`; generated TypeScript contracts live under `frontend/src/types/protos` and are adapted into runtime transport stores/routes by app code.
6. The backend posture is hybrid:
   - browser uses workers + WASM
   - backend uses Go orchestration plus native Rust host lanes where performance paths need them

## Explicit non-goals in this cut

1. No mesh or P2P runtime.
2. No live connector integrations.
3. No full server-side transcode or mux pipeline yet. Current native Rust coverage is probe, quality, preview-prepare, and packaged render artifacts.
4. No full nonlinear editor behavior.
