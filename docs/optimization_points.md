# Optimization Points

Date: 2026-04-19

This document tracks the deliberate performance and architecture carryovers folded into the scaffold after reviewing `fintech_v1` history. For deep-dives into legendary computer science optimization tricks, see [Coding Magic](file:///Users/okhai/Desktop/OVASABI%20STUDIOS/reframe_v1/foundation/docs/coding_magic.md).

## Adopted immediately

1. Compact route metadata and generated route manifests reduce drift between backend and frontend.
2. Environment-shaped queue budgets avoid hardcoded throughput assumptions.
3. Shared event metadata keeps correlation, idempotency, and locale context visible early.
4. Runtime buffer offsets and epoch indices are generated once in the shared `runtime-sdk` instead of hand-coded in each app.
5. Rust/WASM reads an app-owned Cap'n Proto input message and writes an app-owned Cap'n Proto output message into the runtime buffer.
6. The browser decodes the output region with generated `capnp-es` readers rather than manual field extraction.
7. The frontend is organized around feature boundaries, lazy routes, and app-owned `Minimal*` primitives instead of page-local styling drift.
8. Request replay keys are now scoped by metadata context so cached read responses cannot bleed across session, user, or organization switches.
9. In-flight request coalescing now defaults to replay-safe read commands; mutating flows require explicit opt-in dedupe instead of being collapsed accidentally.
10. Loading state is tracked with reference-counted scoped keys instead of a single fragile boolean so concurrent actions do not hide each other.
11. Redis listener dispatch uses blocking fan-in workers instead of sleep-based polling loops.
12. Handler registration now uses an execution controller with bounded concurrency, acquire timeouts, and token-bucket rate limiting with burst support.
13. Public routes should keep the heavy application shell lazy-loaded and only warm the assets needed for the next likely route family.
14. Runtime bootstrap state that must survive HMR or code splitting should live on a process-level singleton (`window`/`globalThis` or equivalent module singleton), not component-local state.
15. Stale chunk/module failures should trigger one-shot cache and service-worker refresh plus reload, instead of leaving the app in a white-screen state.
16. Hot-path parsing should precompile regexes, centralize normalization, and use bounded caches when the same values recur heavily.

**Phase 2 Implementation (Binary-First & Zero-Copy)**:
- **Singleflight Cache**: `GetOrSet` prevents cache stampedes via concurrent request coalescing and double-check locking.
- **Adaptive Concurrency**: Worker engine dynamically scales goroutine pools based on queue depth (up to 64 per queue) to handle traffic spikes.
- **Vectorized Batching**: Support for bulk `EventBatch` envelopes reduces syscall and JS event-loop overhead for high-frequency streams.
- **SharedArrayBuffer Ring Buffer**: Zero-copy log streaming from WASM to the host without main-thread blocking.
- **Beautiful Diagnostics**: High-density, table-aligned logging for sub-millisecond anomaly detection.

## Deferred behind stubs

1. Native Rust render RPC for server-authoritative media execution
2. Live social connector execution
3. Production webhook reconciliation
4. Shared `runtime-transport`, `config-contracts`, and `ui-minimal` extraction after cross-app convergence is proven

## First optimization targets after the scaffold

1. Add bridge-aware queue and reconnect replay helpers so apps can reuse the same request lifecycle discipline without copying store logic.
2. Emit transport diagnostics for replay hits, coalesced requests, queue drain counts, and concurrency-limit rejects.
3. Add service-worker stale-build recovery helpers and tests to the foundation runtime package family.
4. Tighten Rust/Go media-engine boundaries around manifest and quality-report contracts.
5. Promote parser hot-path helpers for regex/date caching into shared utilities once two apps converge on the same fixtures.
