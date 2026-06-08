# Frontend Runtime Workbench

Status: implemented foundation guide
Owner: Frontend Platform

## Purpose

The frontend runtime workbench is the app-facing layer that lets generated
Foundation frontends prototype, run offline, consume live Hermes projections,
and opt into local runtime compute without mixing UI state with networking
internals.

It exists to answer prototype questions safely:

- role: what the screen or workflow does for the user,
- look and feel: how the interaction behaves,
- implementation: whether runtime, store, and communication paths are viable,
- integration: whether those paths compose without hidden coupling.

## Module Boundaries

`frontend-kit` owns:

- dummy data factories,
- generated domain stores,
- tenant projection stores,
- projection worker normalization pipelines,
- live projection bindings and loading state,
- tenant-scoped runtime store/dummy caches,
- reset/session coordination,
- IndexedDB persistence,
- React hooks around external stores,
- runtime snapshot adapters.

`frontend-kit` does not own:

- concrete WebSocket connections,
- backend authorization,
- runtime lane planning,
- Rust/WASM module loading internals,
- visual primitives.

Those stay in `runtime-transport`, `runtime-sdk`, server code, and
`ui-minimal`.

## Workbench Shape

Generated application code should create a workbench from three inputs:

1. generated schema/domain metadata,
2. an optional runtime adapter,
3. tenant/session context supplied by the app.

The workbench can run in three modes:

- `dummy`: generated records and UI flows only, no network required.
- `live`: backend commands and Hermes projection subscriptions.
- `runtime`: live mode plus local Rust/WASM compute, bulk, or stream
  acceleration through explicit runtime adapters.

Runtime mode is a prototype acceleration mode, not a separate source of truth.

## State Contract

Generated domain stores should expose:

- `getSnapshot()`,
- `get(id)`,
- `subscribe(listener)`,
- `useSnapshot()`,
- `upsert(record)`,
- `remove(id)`,
- `replace(records)`,
- `reset()`,
- `batch(run)`.

Tenant projection stores should reject stale or cross-tenant updates and keep
projection metadata with every record:

- tenant id,
- domain,
- collection,
- record id,
- version,
- source watermark,
- epoch,
- deletion state.

Store snapshots are stable between mutations so generated React hooks can use
`useSyncExternalStore` without render loops or stale reads. Snapshots are also
lazy: keyed store operations use `get(id)` and mutation batches use `applyMany`
so hot live-ingest paths do not rebuild the public `records` array or snapshot
map for every event. Snapshot materialization belongs at UI subscription/render
boundaries, selector fixtures, persistence flushes, and explicit diagnostics.

This distinction is required for low-latency live projections. A burst of Hermes
mutations may be cheap to ingest, while a component that reads a full snapshot
after every mutation can still exceed a frame budget. Generated templates should
therefore subscribe once, batch external events, and derive visible state from
cached snapshots rather than polling `getSnapshot()` inside transport callbacks.

## Live Projection Contract

Generated frontend code connects to Hermes through `RuntimeWorkbenchAdapter`,
`createHermesProjectionAdapter`, and `createLiveProjectionBinding`.
`frontend-kit` does not open sockets directly. Runtime transport owns the actual
HTTP/WebSocket connection, reconnect policy, compression, and command bus.

The live binding automatically manages:

- initial loading state,
- explicit `idle`, `loading`, `live`, `degraded`, `closed`, and `error` states,
- buffered live mutations while the initial snapshot is loading,
- monotonic version application,
- batched mutation application with accepted/rejected counts and accepted
  watermark/version reporting,
- bounded live mutation queues with visible degradation when saturated,
- cross-tenant/domain/collection rejection,
- disconnect cleanup,
- reset of tenant-owned cached stores.

The adapter may load an initial Hermes projection snapshot and subscribe to
projection mutations. If only snapshot loading is available, status becomes
`degraded`; if no live adapter is configured, the binding fails closed and does
not mutate the store.

## Worker Normalization Pipeline

Hermes events often arrive as backend-shaped envelopes, not store-ready
mutations. `createProjectionWorkerNormalizer` and
`installProjectionWorkerHandler` provide a worker protocol for normalizing raw
projection events away from the main thread:

- raw backend/Hermes envelopes enter a bounded event queue,
- batches are posted to a worker for schema/scope normalization,
- worker replies contain typed `ProjectionMutation` arrays or normalized load
  results,
- timeouts, saturation, closed workers, or missing workers fall back to local
  normalization when configured with `fallback: "local"`,
- `createLiveProjectionBinding` still applies accepted store mutations on the
  main thread through `applyMany`, preserving React external-store semantics.

This is a refinement lane, not a different contract. Worker-normalized events
must produce the same accepted/rejected tenant mutations as local normalization.
The queue is bounded by `maxPendingRequests`, `maxQueuedEvents`, and
`maxQueuedMutations`; saturation must degrade visibly rather than growing memory
or blocking the main thread.

## Cache Contract

`createPrototypeRuntimeCache` keeps generated stores and deterministic dummy
records cached by tenant/domain/collection and schema constants. Cached dummy
records are not used when a custom provider is supplied because provider output
may be stateful. Tenant reset must clear tenant-owned stores and dummy caches.

This follows the current frontend direction: server/live state has an explicit
cache, loading state, error state, subscription lifecycle, and deduplication
boundary instead of ad hoc fetches inside rendered components.

## Persistence Contract

`createTenantSnapshotPersistence` binds a tenant projection store to an
`AsyncKeyValueStorage` implementation, and `createIndexedDBTenantSnapshotPersistence`
creates the same binding over `createIndexedDBStorage`.

Persistence is tenant/domain/collection scoped. Hydration rejects corrupted or
cross-scope snapshots, session reset clears both the in-memory store and the
persisted snapshot, and persisted records are shallow-redacted for sensitive
fields by default. Persistence is bounded by `maxRecords`; over-budget snapshots
fail closed instead of truncating state.

This path is for prototype/offline continuity and fast fixture reloads. Durable
tenant truth remains backend/Hermes-owned. Do not persist bearer tokens,
session identifiers, privileged role claims, audit internals, or raw private
identifiers in frontend-readable storage.

## Dummy Data Contract

Dummy data factories should derive records from generated schema metadata:

- scalar fields get deterministic values from field kind and index,
- apps may pass a faker-compatible provider through `DummyValueProvider`,
- enum fields use the first allowed value unless a dummy value is supplied,
- repeated fields produce bounded sample arrays,
- tenant/ownership/security/audit fields remain omitted unless allowlisted.

Generated dummy data must not contain secrets, bearer tokens, privileged role
claims, or real tenant identifiers.

## Prototype Generator

`tooling/scripts/generate_frontend_prototype_runtime.mjs` is the descriptor-based
frontend generator for app-owned protobuf schemas. It reads `protoc`
descriptors and emits:

- `DummySchemaSpec` constants,
- schema runtime constants,
- cached dummy record helpers,
- tenant-scoped store factories,
- live projection binding helpers,
- generated hook registry accessors,
- empty, happy-path, invalid, and high-volume fixture states,
- benchmark fixtures for dummy generation and store replacement paths,
- `prototypeDomains` and `createPrototypeTenantStores` aggregate helpers for
  scaffolded apps.

The generator accepts `--proto-root`, `--out`, `--package-import`,
`--include-template`, and `--check`. Its default output is
`frontend/src/generated/prototypeRuntime.ts` so generated applications can keep
prototype state code separate from handwritten app code.

Generated artifacts must import `frontend-kit` APIs instead of constructing raw
transport or runtime lanes. The generator uses protobuf descriptors instead of
text parsing so field kinds, repeated fields, enum values, map fields, and
well-known timestamp messages come from the schema contract.

## Scaffold Integration And Offline Operation

Scaffolded frontend projects include `src/stores/prototype.ts`. That file
imports `frontend/src/generated/prototypeRuntime.ts`, creates an
`offlinePrototypeRuntime`, seeds tenant stores with schema-derived dummy records,
and exposes a single `createPrototypeRuntimeContext` entry point for UI code.
When an `AsyncKeyValueStorage` is supplied, the context also exposes
`hydratePersistence`, `startPersistence`, per-store persistence bindings, and
an async `resetTenant` that clears both in-memory and persisted tenant state.

This gives new projects an offline frontend state path with no backend, no
Hermes connection, and no WASM module required. When a Hermes projection source
is provided, the same context creates live projection bindings that load the
initial projection, buffer incoming mutations during load, reject cross-tenant
or cross-domain events, and apply accepted updates into the same tenant stores.

Generated React components and templates must consume store snapshots with
`useSyncExternalStore` semantics. They must not fetch in render, open raw
WebSocket connections, or call runtime lanes directly. Network, loading,
retry/degrade state, and transport fallback remain adapter responsibilities.

The project Makefile runs `frontend-prototype-runtime` before frontend
build/test/lint, and `make communication-contracts` regenerates protobuf
TypeScript, frontend prototype stores, runtime bindings, and foundation
transport contracts together.

Fully offline operation is defined as:

- frontend dummy fixtures and tenant stores work without server access;
- generated stores and benchmark fixtures are deterministic and cached;
- runtime/WASM modules are optional for the prototype state path;
- live Hermes projections require backend/runtime-transport availability and
  fail closed into degraded/error state when unavailable;
- backend integration tests still require the project test environment
  configured by Docker/Postgres/Redis.

The scaffolded Go WASM module is only a compatibility shim that forwards to
runtime-transport. Low-latency shared-memory compute is the Rust
`runtime-sdk` path: generated Cap'n Proto contracts, host-managed SAB, worker
ownership, exported entrypoints, and runtime benchmarks. Performance claims are
evidence-based, not absolute guarantees: projects should use the generated
benchmarks for their schemas and device/browser mix before setting SLOs.

## Benchmark And Profile Evidence

Frontend workbench evidence now has three layers:

- `runtimeWorkbench.bench.ts` measures dummy generation, tenant projection
  apply, batched projection apply, render-coupled snapshot reads, live binding
  apply, live binding batched apply, projection event pipeline normalization,
  and planned compute dispatch.
- `runtimeWorkbench.profile.test.ts` records retained heap deltas for generated
  dummy, projection apply, batched projection apply, projection event pipeline,
  and cache reset paths. The wrapper `tooling/scripts/frontend_workbench_profile.sh` runs this with
  `FOUNDATION_VITEST_EXPOSE_GC=1` and writes log/TSV artifacts.
- `tests/scaffold_smoke_test.sh` captures generated frontend install, build,
  test, bundle-size, and cold-start timing artifacts when the dependency-backed
  scaffold frontend path is enabled.

Interpretation:

- Dummy generation around 1 ms per 1k records is acceptable for fixture creation,
  scaffold smoke, and offline reset. It should not run during render.
- Projection apply without snapshot reads is the live-ingest path. It should stay
  sub-millisecond per 1k simple mutations on local development hardware.
- Projection apply with a snapshot read after every mutation is intentionally
  measured as the expensive anti-pattern. If this benchmark dominates, generated
  components or callbacks are reading snapshots too often.
- Live binding batched apply is the preferred path for initial loads, buffered
  subscription events, and Hermes replay bursts.
- Worker projection normalization is the preferred path for large raw Hermes
  envelopes, high-frequency subscription bursts, or schema adapters that would
  otherwise parse/validate on the main thread.
- Planned compute measures workbench planning overhead only. It is not a claim
  about the Rust/WASM compute unit itself.
- Heap profile gates are regression budgets. Projection apply is bounded below
  16 MiB for 5k simple mutations so accidental per-event snapshot copies are
  caught before they reach scaffolded apps.

## Runtime Adapter Contract

The workbench sees runtime and network through an abstract adapter:

```ts
type RuntimeWorkbenchAdapter = {
  dispatch?(command): Promise<unknown>;
  query?(query): Promise<unknown>;
  loadProjection?(scope, request): Promise<ProjectionLoadResult>;
  subscribeProjection?(scope, listener): () => void;
  planCompute?(request): RuntimeComputePlan;
  dispatchCompute?(request, plan): Promise<unknown>;
  transfer?(request): Promise<unknown>;
  stream?(request): AsyncIterable<unknown>;
};
```

This keeps stores and UI hooks independent from WebSocket and WASM
implementation details.

## Acceptance Gates

- The same generated store works in dummy, live, and runtime modes.
- Tenant reset clears all tenant-scoped stores and persisted snapshots.
- Live bindings expose loading/error/network state without generated components
  opening transport lanes directly.
- Hermes projection updates preserve monotonic version/source ordering.
- Runtime compute results enter stores through the same typed update path as
  backend projection updates.
- Runtime compute dispatch goes through `planCompute` before `dispatchCompute`;
  generated UI code does not call raw runtime or transport lanes directly.
- Runtime data never bypasses backend-derived tenant authority.
