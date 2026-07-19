# Runtime Foundation

Date: 2026-05-22

This document records the runtime foundation posture for this scaffold.

The runtime is the execution depth behind Foundation's small external model.
Product code declares a stable input/output contract and required guarantees;
the planner may select direct Go, bounded workers, SIMD, WASM/SAB, transferable
buffers, FFI, shared memory, framed process, native shell/device, WebGPU/native
GPU, or network fallback according to workload, trust, locality, capability,
and deadline. Lane choice is hidden state unless it changes a consequential
trade-off. Every lane must preserve visible semantics and diagnostics.

For the end-to-end command/event/worker/realtime lifecycle, see `foundation/docs/foundation_nervous_system.md`. That document is the canonical substrate contract; this file describes the runtime lanes that must refine it. For the Hermes hotplane research posture, see `foundation/docs/hermes_hotplane.md`. For Go-specific concurrency bug taxonomy and watch points, see `foundation/docs/go_concurrency_bug_practices.md`. For Rust/WASM/native coding, async, performance, and error-handling rules, see `foundation/docs/rust_runtime_practices.md`.

## Control-plane foundations

1. Go owns HTTP ingress, orchestration, schedule/publish workflow state, queue registration, and OpenAPI generation.
2. PostgreSQL is the durable system of record and follows the fixed three-group migration structure.
3. Redis is ephemeral only: event fanout, coordination, and cache support.
4. Media artifacts are expected to live in S3-compatible object storage from day one, with private-by-default buckets and signed or mediated access.
5. Route metadata drives RBAC posture, OpenAPI generation, and the frontend route manifest.
6. Service-registry listeners should fan in Redis/pubsub traffic to blocking worker pools instead of using sleep-based polling loops.
7. Handler registration should apply bounded concurrency through a shared execution controller so saturation behavior is explicit and measurable.
8. Externally reachable handlers must fail closed on missing identity, organization scope, integrity metadata, or route contract validation.
9. Hermes hotplane projections are mandatory node-local read refinements for the scaffolded `database.RuntimeStore`. They are fed only by committed Postgres/outbox/River/Redis Stream observations, remain bounded, replayable, tenant-scoped, and disposable, and fall back to Postgres instead of serving partial state.

## Foundation log plane

The runtime splits observability into two lanes:

1. Operational logs explain behavior to operators. They use the Foundation logger facade, context enrichment, redaction, bounded value sizes, sampling/deduplication, async queues, colorized development output, JSON production output, or compact wire format (`cwf`) when log storage needs fast append/split/index behavior.
2. Durable event facts describe system truth. They are typed terminal events with correlation metadata and binary Foundation envelopes. The `server-kit/go/eventlog` package persists those envelopes in `foundation_event_log` and republishes pending rows into Redis Streams for short delivery windows. Pending eventlog drains first claim rows with a bounded Postgres lease (`FOR UPDATE SKIP LOCKED`, `publish_claim_token`, `publish_claim_expires_at`), then use direct bytea reads, Redis Stream batch append, and token-checked batched published-state updates. This keeps multi-publisher drains from publishing the same pending facts while still amortizing service round trips and preserving per-entry diagnostics. Hermes consumes these facts only after the Postgres/outbox/River/Redis Stream observation is committed; it must never consume arbitrary operational log text.

The expected hot projection flow is:

```text
Postgres transaction
-> durable outbox/event fact row
-> Redis Stream short delivery window
-> Hermes typed projection apply
-> drift checks and operational telemetry around every step
```

Operational logging failure is non-fatal and must degrade through drops,
sampling, and counters. Durable event fact failure inside a command boundary is
part of the truth boundary: the command must fail, retry, or remain unpublished.
Redis failure leaves the durable row available for replay, and Hermes failure
falls back to Postgres.

## Go concurrency posture

1. Runtime, registry, Redis, WebSocket, worker, and event-bus goroutines must be owned by a component with a cancellation source, shutdown order, and terminal observation path.
2. Channels must document send ownership, receive ownership, close authority, capacity, and overflow behavior. Unbuffered channels are rendezvous points; buffered channels are bounded queues.
3. Do not hold `Mutex` or `RWMutex` locks across blocking channel operations, WaitGroup waits, Cond waits, context waits, external calls, or callbacks.
4. `WaitGroup.Add` must occur before launched goroutines can call `Done` and before a waiter can observe the group. Fanout loops should launch all intended work before waiting unless serial execution is deliberate.
5. Select loops must handle shutdown and cancellation explicitly. If a shutdown signal must win, give it a pre-check or priority structure instead of relying on random select choice when multiple cases are ready.
6. Timer and ticker lifecycles must be explicit. Avoid zero-duration placeholder timers, stop owned timers/tickers, and avoid retaining timer channels beyond their owning context.
7. Partial hangs are runtime failures even when the Go process is not globally deadlocked. Long-lived lanes should expose active worker/listener counts, queue depth, blocked/rejected sends, cancellation, and terminal shutdown signals where meaningful.

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
   The server-side primitive for this class is `server-kit/go/bulk`. It is a generic package, not a Foundation-owned `/bulk` route: scaffolded apps bind it into domain-specific functions such as media upload, dataset import, model artifact sync, archive export, or tenant backup after applying product auth, capability, quota, content-type, and retention rules. Those app-owned handlers must pass an `io.Reader` for each bounded part and must not route bulk bytes through generic dispatch paths that materialize request bodies.
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

## Hermes hotplane posture

Hermes is the Foundation node-local hotplane for live operational reads. The
implementation continues to evolve, but the read-mode behavior is stable in
`docs/hermes_read_modes.md`; generated projects may rely on that v1 contract
while keeping Postgres as the default correctness path. The server-kit slice
lives in `server-kit/go/hermes`: bounded projection specs,
tenant-scoped segmented indexes, copied public reads, borrowed internal views,
atomic epoch publication, `database.StateStore` rebuild, typed record batch
ingestion, trusted snapshot `BulkLoad`, generated
`foundation.v1.RecordMutationBatch` envelope ingestion, binary payload
ingestion, a worker processor bridge, and Redis Stream tailer
abstractions. It
uses the runtime principles in this document, but it does not replace the
durable command path. Postgres remains the system of record, River/outbox
records remain the durable follow-up path, and Redis remains the distributed
coordination substrate.

1. Hermes consumes committed observations only: terminal lifecycle events,
   outbox rows, River jobs, Redis Stream entries, or Postgres rebuild snapshots.
2. Mutating handlers must not update Hermes before the database transaction
   commits. The correct sequence is durable write -> terminal observation ->
   projector apply -> epoch publish.
3. Hermes partitions are keyed by organization, domain, collection, and
   optional shard. Reads must never cross organization scope.
4. Every partition owns an atomic epoch, source watermark, bounded apply queue,
   memory budget, tombstone budget, and declared index set.
5. Internal consumers may use borrowed views, descriptor IDs, caller-owned
   result slices, and epoch watchers. Public service APIs must return copied
   records or copied bytes.
6. Projection reads that require read-your-own-write semantics must provide a
   fence: event ID, source watermark, revision, updated-at bound, or equivalent
   freshness token. If the fence is not satisfied within a tiny bound, Hermes
   falls back or fails visibly.
7. Redis integration is for stream tailing, invalidation, watermarks, leases,
   and node heartbeats. A healthy Hermes read should not require a Redis round
   trip on every request.
8. Shared-memory or arena-backed Hermes payloads must use descriptor ownership,
   release discipline, and epoch signaling from `runtime-sdk`; broad JSON maps
   are compatibility only.
9. Generic Hermes contracts live in `runtime-transport/protos/foundation/v1`.
   App-owned schemas can remain protobuf or Cap'n Proto, but projector output to
   Hermes should use the generated Foundation projection contract.
10. Memory pressure degrades Hermes before the process. Projections must cap
   records, bytes, indexes, tombstones, descriptors, queue depth, and watcher
   count.
11. Dropping a Hermes partition must be safe. The partition either rebuilds from
   Postgres plus stream tail or routes reads to Redis/Postgres according to its
   projection policy.
12. Ingestion shape matters. `ApplyBatch` is the durable event/mixed-mutation
    lane, `ApplyRecords` is the incremental pure-upsert projector lane, and
    `BulkLoad` is the trusted snapshot replacement lane used by rebuild/repair.
    Routine refresh loops should not rebuild from Postgres when a bounded
    changelog can feed `ApplyRecords`.

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
   - `runtime-native`: Tauri-backed binary dispatch for desktop/mobile shell control, measured separately from the hot runtime lanes
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
   - `runtime-native` for desktop/mobile shell access, secure storage, capability discovery, and device plugin control
10. `ffi` is a trusted-only lane. Do not load arbitrary runtime libraries or allow user-controlled module/unit selection.
11. `shm` and `stdio` lanes must enforce frame-size limits, same-host permissions, and explicit allowlists for callable units.
12. FFI diagnostics must remain C-compatible and UTF-8 safe. Truncated error buffers must end on a character boundary and always be null-terminated when capacity is non-zero.
13. Native host accounting must use RAII/symmetric cleanup guards for in-flight counters and other state that must be restored on every return path.
14. The Rust unit registry is shared through synchronized interior state and returns `Arc<dyn RuntimeUnit>` handles for concurrent reads. Runtime units themselves must remain `Send + Sync`; any mutable caches inside a unit must use explicit synchronization.
15. App Rust crates must expose a `RuntimeUnitDescriptor` before they are treated as integrated runtime work. A crate that only has Rust functions is a library; a crate with a descriptor, stable input/output schema names, and native/WASM capability flags is selectable by the runtime planner.
16. Do not route scalar checks through FFI or WASM just because a Rust implementation exists. The planner must compare boundary cost against work size. Direct Go validation remains the right lane for nanosecond-scale request checks; Rust runtime units are for deterministic batched math, simulations, scoring, and browser/native parity.
17. Financial runtime units must use integer minor units, checked arithmetic, stable text/binary schemas, and explicit rejection of ambiguous decimal inputs. Float semantics are not permitted for ledger, settlement, fee, or stablecoin accounting paths.
18. Backend projects that add app Rust compute must include a runtimehost integration test for at least one native lane. FFI is the preferred proof for trusted same-process kernels; stdio is the portability/isolation proof. A Rust crate without a backend runtimehost test is not yet operationally integrated.
19. Native worker dispatch must use bounded response waits and record timeout diagnostics. Framed stdio and TypeScript/native codecs must reject declared frame lengths before allocating payload memory or creating byte views.
20. Foundation Rust changes should pass `make check-rust`, which runs fmt,
    Clippy with unsafe-documentation lints, runtime-practice checks, and Rust
    tests. Scaffolded applications receive the same scripts under
    `scripts/checks/`; their `lint-foundation` target checks app-owned `rust/`,
    `native/src-tauri/`, and vendored `foundation/runtime-*` Rust manifests.

## Go SIMD posture

Go 1.26 adds experimental `simd/archsimd` access through
`GOEXPERIMENT=simd`. Foundation treats it as an opt-in CPU lane, not a new
default substrate.

1. It is architecture-specific and currently practical only for amd64 builds;
   Foundation must keep portable scalar or Rust/WASM/native fallbacks.
2. The API is experimental and not covered by the Go 1 compatibility promise,
   so `archsimd` types must not appear in public Foundation APIs or generated
   app contracts.
3. SIMD candidates are contiguous, batched, arithmetic or byte-processing loops:
   scoring vectors, signal windows, image/audio kernels, checksums/hashing
   helpers where allowed, telemetry compression primitives, or bounded
   normalization passes.
4. SIMD is not appropriate for request orchestration, auth, tenant checks,
   database calls, event lifecycle logic, or small scalar validation.
5. Promotion requires scalar parity tests, architecture-gated build tags,
   `GOEXPERIMENT=simd` CI coverage, benchmarks versus scalar Go and Rust/native
   lanes, and documented fallback behavior.
6. The lane planner may classify SIMD-capable Go kernels as `cpu-simd`, but the
   visible command/event contract must remain unchanged.

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
2. New and changed production code should target line coverage >= 95%, branch coverage >= 90%, and hotspot scores below the high-risk threshold where the stack can calculate them.
3. Hot-path changes are not complete until both regression tests and hotspot review show the code is safe to modify.

## Binary event transport posture

1. Foundation event transport now follows the Phase A shape from `fintech_v1/docs/transition_full_binary_pipeline.md`.
2. The canonical internal event envelope and Hermes projection contract live under `foundation/runtime-transport/protos/foundation/v1`.
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
12. Frontend route dispatch defaults to the performance ladder: `sab -> wasm -> native -> transferable -> ws -> http -> postMessage`, skipping unavailable strategies and falling back only after observable failure. `native` is a measured local control lane; direct FFI, shared-memory, and WASM/SAB remain the hot compute lanes.
13. Same-process hot dispatch must use direct frame dispatch (`grpcsvc.NewDirectFrameClient` or equivalent typed in-process call), not gRPC. Current guardrail target is zero allocations for direct dispatch; gRPC is a network/polyglot boundary with materially higher stack cost.
14. Frame codecs expose owned decode and borrowed `FrameView` decode. Use borrowed views for synchronous hot paths that do not retain frame data; use owned `Frame` decode when values escape the incoming buffer lifetime.
15. A typed service binding has two first-class projections: registry protobuf dispatch for ingress/event/lifecycle paths, and `bootstrap.RegisterTypedFrameHandlers` for internal synchronous `grpcsvc.Frame` dispatch. App startup must register the same binding map into both when typed contracts exist.
16. Frame adapter benchmarks are separate from raw frame benchmarks. Raw frame dispatch measures router/codec mechanics; typed frame adapter benchmarks include protobuf marshal/unmarshal and bounded handler execution, so regressions are interpreted against the correct lane.
17. Runtime lane planning is a foundation concern. `runtime-sdk` classifies work by payload size, workload class, trust, locality, batchability, deadline, and runtime capabilities before selecting direct, scalar CPU, SIMD/FFI, shared-memory, native GPU, WebGPU, WASM/SAB, transfer, stream, or HTTP lanes. Plans must expose copy budget, allocation budget, expected latency class, deadline risk, cross-origin-isolation requirements, and fallback order so frontend/backend callers can explain why a lane was chosen.
18. GPU/WebGPU lanes must follow `docs/gpu_practices.md`. They are batch lanes, not control lanes. GPU-bound batches must use storage-buffer-friendly packing, explicit alignment/stride metadata, and enough items/bytes to amortize transfer, dispatch, and readback; small trusted control work stays on direct Go, FFI, or WASM/SAB lanes.
19. GPU lane plans must name the bottleneck they address: memory bandwidth, memory latency, uncoalesced access, branch divergence, load imbalance, synchronization, atomics, host-device transfer, kernel launch throughput, or CPU hot loop. Native GPU plans must also declare device capability, driver/runtime version, stream/queue ordering, graph or memory-pool reuse, async error-drain points, numeric tolerance, frame/pass budget, quality profile, and first-use warmup state. A plan that only says "use GPU" is incomplete.
20. `RuntimeWebGpuHost` is the browser compute bridge: it packs arena descriptors into GPU buffers, uses async pipeline creation to avoid compile stalls, dispatches workgroups, and now keeps outputs GPU-resident unless arena materialization is explicit. Real browser/device probes use `measureRuntimeWebGpuDeviceRoundTrip` to separate adapter, device, warmup, upload, dispatch, queue-drain, materialization, and total timings. WebGPU must remain optional and capability-gated because availability, adapter lifetime, and device loss can change at runtime.
21. Native GPU adapters follow the Cap'n Proto `runtime_native_gpu.capnp` descriptor/fallback contract and store raw platform handles in the private `runtime-native` registry. Registry records own the hidden DMA-BUF, IOSurface, Android hardware-buffer, CUDA/Vulkan, Metal, stream, event, fence, owner-scope, and reference-count state. Unix fd-backed handles are accepted as owned private records; IOSurface, Android hardware-buffer, CUDA, and Vulkan SDK objects remain opaque plugin records until the platform plugin owns the unsafe SDK boundary. Public APIs expose only descriptor receipts, snapshots without raw handles, and explicit materialization plans.
22. Interactive runtime work should follow `docs/game_runtime_practices.md`: pass graphs for multi-stage work, stable performance markers, target-hardware capture bundles, culling/LOD/interest masks before expensive passes, pipeline warmup before first-use deadlines, and quality tiers for reduced-power or reduced-capability profiles.
23. Arena descriptors have a lifecycle. Producers must consume or explicitly force-release ready descriptors before reuse; released descriptors return through the free-list and keep their page-aligned slab region for future allocations that fit, preventing long-running processes from turning descriptor tables or arena pages into hidden pressure.
24. Kernel-bypass-inspired lanes are modeled as optional packet rings, not default NIC ownership. The foundation primitive is fixed-size descriptor slots, burst enqueue/dequeue, explicit ownership states, low-overhead monotonic timestamps, drops, high-water depth, and release discipline. A future DPDK, Onload, AF_XDP, or FPGA adapter must refine this same packet-ring contract rather than introducing app-specific packet ownership.
25. Timestamping is a lane diagnostic, not visible domain behavior. Software monotonic timestamps are always available; hardware/NIC timestamps may be attached by an adapter when supported, but fallback lanes must preserve command/event semantics even when timestamp precision changes.
26. Garbage-collector avoidance means keeping hot payload movement out of runtime heap object creation. Direct, packet-ring, FFI, and shared-memory lanes should reuse slabs/descriptors/views, return borrowed views for synchronous work, and avoid JSON/map/object materialization until the owning domain boundary needs it. This reduces allocator cost, GC scan work, cache churn, and tail-latency spikes; it does not remove GC from the whole application.
27. Deadline-sensitive frontend work must call the lane planner before choosing WebGPU, workers, HTTP, or direct SAB/WASM access. Browser operations with deadlines below `1,000,000 ns` should prefer SAB/WASM or transferable workers; WebGPU is reserved for batches large enough to amortize adapter, pipeline, dispatch, and readback costs.
28. Device streams follow the same rule. WebView media APIs are compatibility lanes. Native camera frames, microphone PCM, and sensor samples must enter through Swift/Kotlin plugins, Rust validation, and Foundation binary buffers/descriptors before reaching FFI, shared-memory, WASM/SAB, or GPU lanes.
29. Bulk transfer adapters are app-owned. Foundation supplies `server-kit/go/bulk` for plans, streamed part acceptance, per-part hashing/compression, manifest completion, range reads, Redis-compatible progress caches, and lifecycle events. Scaffolded apps decide whether the ingress is HTTP multipart, signed object-store URLs, native FFI/shared-memory, internal service calls, or worker-driven import/export. The visible contract must remain domain-specific (`media:asset_upload:*`, `dataset:import:*`, and similar), while the hidden data plane uses bulk transfer receipts and manifests.
30. Bulk integration uses the generic `server-kit/go/bulk.Pipeline` adapter ladder:
    - `runtime-transport` adapter: carries the transfer plan, part receipts, resume tokens, progress events, signed-part grants, and manifest references through typed envelopes; it does not push large byte slices through generic JSON or request-body dispatch.
    - resumable protocol handshake: maps domain commands to `init -> accept part -> receipt -> complete -> committed/failed`, with idempotency keys, correlation IDs, part offsets, checksum verification, status/missing-part discovery, and bounded retry windows.
    - distributed state backend: stores in-flight plans, received part receipts, ownership leases, and completion state outside one process when uploads may cross workers, pods, or restarts. Redis is acceptable for ephemeral progress and leases; durable recovery must use the product's durable store or object-store manifest.
    - object-store data plane: persists each bounded part or composed object through the `objectstore` interface. App handlers should pass readers through to bulk/objectstore and avoid materializing a full logical file in Go heap.
    - HTTP/app streaming lane: app routes may call `Pipeline.AcceptHTTPPart` with an app-owned `io.Reader` after route auth, quota, content-type, and rate-limit checks. This is the simplest server-mediated lane and preserves the same receipt/manifest state.
    - signed object-store lane: apps may call `Pipeline.GrantSignedPart` so clients upload a bounded part directly to object storage, then call `Pipeline.AcceptSignedPart` to stream-verify the stored object hash and issue the Foundation receipt. This removes the app server from the byte proxy path while keeping checksum, idempotency, tenant, and manifest invariants.
    - same-host acceleration: trusted native callers may use FFI/shared-memory descriptors through `Pipeline.AcceptDescriptorPart`; the descriptor source opens a bounded reader and the public contract remains receipts/manifests. Borrowed views must not outlive their source descriptor.
    - kernel/network acceleration: Linux `sendfile`, `splice`, `MSG_ZEROCOPY`, `io_uring`, `TCP_NOTSENT_LOWAT`, pacing, BBR/fq, MPTCP, QUIC, or future packet-ring adapters are optional lower lanes. `DetectPlatformCapabilities` reports conservative availability; a real adapter must still manage socket setup, pinned-buffer lifetime, completion notifications, fallback, and metrics. They must refine the same transfer manifest and receipt contract rather than creating app-specific transport semantics.
31. Bulk lane selection must be adaptive and explicit. `Pipeline.PlanLane` ranks descriptor, signed object-store, kernel zero-copy, MPTCP, QUIC, and HTTP-stream candidates from locality, trust, direct object-store availability, adapter availability, compression policy, and detected platform capabilities. `Pipeline.Diagnostics` provides the generic shape; `DetectPlatformCapabilities` can seed it with local OS capability hints, and app adapters may enrich it with platform-specific discovery. Missing acceleration is not a correctness failure; silent full-file materialization is.
32. Parallel operation chains should use `server-kit/go/chain`: independent operations run concurrently, non-critical failures do not block movement, and critical failures cancel the operation context for the rest of the chain.
33. The frontend runtime client uses an authenticated websocket upgrade path that fits the existing allowset model:

    - guest socket opens and receives `identity:connection_open:v1:ack`
    - if a session access token exists, the client sends `identity:authenticate_connection:v1:requested` over that socket
    - once the socket is authenticated, route transport preference can safely switch to `ws -> http` for mutation paths without opening broad guest access

34. Socket authentication is not sufficient on its own. Privileged subscriptions, commands, and topic joins must re-authorize against current session, user, and organization state after connect.
35. Websocket upgrades must validate allowed origins and close or downgrade sessions when auth state changes or expires.
36. Event envelopes and payload bodies must enforce schema validation, size limits, and replay/idempotency windows before handler dispatch.

## Virtual-memory and columnar data-plane posture

The runtime arena is intentionally shaped like a virtual-memory-aware data
plane: a small fixed control buffer, page-aligned slabs for larger payloads,
borrowed views for synchronous work, and explicit descriptor ownership. Treat
page faults, page-cache behavior, TLB/cache locality, NUMA placement, and
copy-on-write effects as measurable runtime behavior for native/shared-memory
lanes.

1. Keep the 4KB control buffer for command/status/epoch metadata. Do not expand
   it to carry report, media, telemetry, or model batches.
2. Use arena descriptors for payloads that need page-aligned slabs, ownership
   transfer, or reuse across workers/native lanes.
3. Prefer column-shaped payloads for scan-heavy batches: one descriptor for
   schema/metadata, then separate descriptors for validity, offsets, and typed
   value buffers when the workload benefits from contiguous column access.
4. Align future columnar batch descriptors with Apache Arrow concepts where
   practical: record batch, field arrays, validity bitmap, offsets buffer,
   values buffer, row count, null count, and dictionary references. Full Arrow
   IPC support is optional; layout compatibility and zero-copy interop are the
   design target.
5. Row-oriented protobuf/Cap'n Proto messages remain the command/event
   contract. Columnar batches are internal analytical/runtime payloads and must
   refine the same visible command/event semantics.
6. Large analytical or media batches must expose copy budget, allocation budget,
   descriptor count, byte count, and fallback behavior in lane-plan diagnostics.
7. Native/shared-memory benchmark runs should record cold and warm page-cache
   behavior separately. For Linux hosts, include minor/major page faults and
   RSS/PSS where available; NUMA placement belongs in production evidence for
   multi-socket deployments.
8. Treat cache-line locality as part of the arena contract. Descriptor slots,
   ring cursors, producer/consumer ownership words, and columnar field
   descriptors should remain fixed-width, contiguous, and naturally aligned so
   hot loops count touched cache lines rather than chase pointers.
9. Avoid false sharing in future runtime queues and packet rings. Contended
   epoch slots, write cursors, read cursors, and per-worker counters must not be
   packed next to unrelated hot atomics without a benchmark that proves the
   layout is harmless.
10. Runtime batch planners should choose batch sizes that fit useful L1/L2
   working sets before escalating to SIMD, FFI, shared memory, or WebGPU. A
   wider lane that repeatedly stalls on cache misses is not a better lane.

## FFI ABI conformance posture

The FFI boundary is a calling-convention contract, not just a function pointer.
Any Rust, C, or Go participant must agree on exported symbol names, integer
widths, pointer lifetimes, buffer mutability, error-buffer semantics, alignment,
schema version, and ownership of host handles.

1. ABI version mismatch must fail closed before host creation.
2. Public FFI functions must use C-compatible scalar types and opaque pointers
   only. Do not expose Go, Rust, Cap'n Proto, Arrow, slice, string, trait, or
   interface types across the raw ABI.
3. FFI callees must validate null pointers, lengths, UTF-8 unit IDs, writable
   buffers, and error-buffer capacity before dereference.
4. Error buffers must be null-terminated when capacity is non-zero and must not
   split UTF-8 code points.
5. The host must treat runtime buffers as borrowed for the call duration unless
   the ABI explicitly transfers ownership.
6. Conformance tests should exercise ABI version mismatch, nil host, nil unit
   ID pointer, nil buffer pointer, oversized/invalid lengths, invalid UTF-8,
   diagnostic truncation, non-zero status, and concurrent calls.
7. Parity tests must compare FFI output against at least one non-FFI lane for
   product runtime units before the lane is considered operational.

## Borrowed patterns

1. From `field_os`: route metadata, docgen, route manifest generation, low-cost observability, and strict migration checks.
2. From `fintech_v1`: environment-driven concurrency defaults, queue budget discipline, lazy shell boot, request replay boundaries, HMR-safe runtime bootstrap, stale-build recovery, and dispatch-worker posture.
3. From `inos_v1`: raw host ABI imports, shared-memory contract thinking, epoch-style signaling vocabulary, worker isolation, typed buffer layouts, and the Hermes rule that memory-as-communication only works when ownership, snapshot lifetime, and epoch publication are explicit.

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
