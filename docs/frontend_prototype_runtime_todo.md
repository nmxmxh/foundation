# Frontend Prototype Runtime TODO

Status: implementation checklist
Owner: Frontend Platform

## Phase 1: Frontend Workbench

- [x] Add generated dummy data factories in `frontend-kit`.
- [x] Add faker-compatible dummy value provider adapter without adding a
      network-bound dependency.
- [x] Add generated domain store helpers in `frontend-kit`.
- [x] Add tenant projection store with stale-update rejection.
- [x] Add direct keyed store reads, lazy immutable snapshots, and batched
      projection apply to avoid per-event snapshot allocation.
- [x] Add live Hermes projection binding with loading/error/closed state.
- [x] Add worker-backed projection normalization with bounded pending requests,
      timeout fallback, and local offline backup.
- [x] Add bounded live subscription batching so normalized bursts hit stores
      through `applyMany`, not one React-visible update per event.
- [x] Add tenant-scoped runtime cache for stores and deterministic dummy records.
- [x] Add runtime workbench adapter over transport/runtime capabilities.
- [x] Route frontend-kit compute through a planning adapter before dispatch.
- [x] Add tests for dummy/live/runtime mode parity.

## Phase 2: Local Runtime Prototype Lanes

- [x] Promote INOS-style bridge primitives into `runtime-sdk/ts/browser-host`.
- [x] Add worker pool, dispatcher, module loader, and registry reader.
- [x] Add Cap'n Proto syscall and compute descriptors for local runtime lanes.
- [x] Add Rust ring buffer primitive tests.
- [x] Add benchmark stubs for bridge, registry, dispatcher, and workbench paths.
- [x] Add generated workbench manifests from app-owned protobuf/domain schema.
- [x] Add Hermes projection-to-store adapter fixtures.
- [x] Add IndexedDB snapshot persistence tests for tenant reset/session reset.

## Phase 3: Prototype Generator

- [x] Generate `DummySchemaSpec` from app protobuf/domain schema.
- [x] Generate typed tenant stores and React hooks from schema metadata.
- [x] Generate fixture sets for happy path, empty state, invalid state, and
      high-volume list/table states.
- [x] Generate benchmark fixtures for store apply, reset, selector, and dummy
      data paths.
- [x] Generate selector fixtures through direct keyed store reads instead of
      forcing full snapshot materialization.
- [x] Generate contract notes that name tenant scope, visible state, hidden
      state, bounds, and fallback behavior.
- [x] Generate live-aware cached store and projection binding helpers.
- [x] Generate aggregate `prototypeDomains` and `createPrototypeTenantStores`
      helpers for scaffold/template auto-wiring.

## Phase 4: Scaffolded Project Integration

- [x] Add template `src/stores/prototype.ts` to create offline prototype runtime
      contexts from generated stores.
- [x] Add template persistence hooks for generated tenant stores.
- [x] Make frontend build/test/lint regenerate prototype runtime before running.
- [x] Update scaffold checks so generated stores are part of project init.
- [x] Make the template app render generated runtime/store status as a smoke
      test for the prototype state path.
- [x] Add scaffold smoke coverage for dependency-free prototype runtime
      generation in a fresh project.
- [x] Keep generated frontend build/test available through the scaffold smoke
      CI path or `SCAFFOLD_SMOKE_FRONTEND=1 SCAFFOLD_SMOKE_INSTALL=1`.
- [x] Store generated frontend build/test artifact logs and bundle-size output
      when scaffold smoke runs the dependency-backed frontend path.
- [x] Add a live Hermes projection integration fixture that proves initial load,
      buffered update, degraded fallback, and tenant reset behavior through a
      backend-shaped Hermes adapter.
- [x] Add worker-normalized Hermes subscription fixture with local fallback.
- [ ] Add a service-backed browser/backend replay that drives the same fixture
      through the managed backend test environment.

## Phase 5: Benchmarks And Evidence

- [x] Benchmark runtime bridge epoch/view paths.
- [x] Benchmark runtime dispatcher and exported compute ABI paths.
- [x] Benchmark registry scan path.
- [x] Benchmark frontend dummy factory, projection store, and planned compute
      paths.
- [x] Split frontend workbench benchmarks into hot apply, batched apply,
      render-coupled snapshot read, live apply, and live batched apply paths.
- [x] Add projection event pipeline normalization benchmark coverage.
- [x] Add benchmark history capture for frontend-kit workbench lanes.
- [x] Add bundle-size and cold-start checks for generated prototype packages
      when scaffold smoke runs frontend build/test.
- [ ] Add first-render and reset-hydration measurements for generated app
      fixtures.
- [ ] Add generated-app offline smoke benchmarks for store creation, first
      snapshot read, and schema fixture render paths.
- [x] Add allocation/heap profile monitoring for frontend workbench dummy,
      projection apply, and cache reset paths.
- [x] Tighten projection apply heap regression budget after removing per-event
      snapshot copies.
- [x] Add projection event pipeline allocation monitoring.
- [ ] Promote frontend workbench profile TSVs into a retained benchmark history
      report with before/after comparison thresholds.

## Non-Negotiable Rules

- [x] No P2P, mesh, peer delegation, active signaling, or browser WebRTC scope
      in this frontend prototype runtime path.
- [x] No raw `new WebSocket` in app or frontend-kit code.
- [x] No main-thread blocking `Atomics.wait`.
- [x] No bindgen-first public runtime API.
- [ ] No secrets in SAB, dummy data, runtime logs, stores, or fixtures.
