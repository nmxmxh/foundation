# Optimization Points

Date: 2026-05-11

This document tracks the deliberate performance and architecture carryovers folded into the scaffold after reviewing `fintech_v1` history and the current performance synthesis. For cross-cutting Go, networking, PostgreSQL, Rust, benchmarking, and documentation-tracking practices, see `foundation/docs/performance_practices.md`. For TLA+/`Specifying Systems` state-machine, invariant, liveness, real-time bound, composition, and refinement practices, see `foundation/docs/tla_architecture_practices.md`. For Go concurrency bug taxonomy and practices extracted from `go-study.pdf`, see `foundation/docs/go_concurrency_bug_practices.md`. For deep-dives into legendary computer science optimization tricks, see [Coding Magic](file:///Users/okhai/Desktop/OVASABI%20STUDIOS/reframe_v1/foundation/docs/coding_magic.md).

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
17. Performance work must start with a behavior boundary and a baseline measurement: profile, benchmark, load test, query plan, or production telemetry.
18. Go hot paths should preallocate predictable collections, preserve payload bytes until owner decode, use borrowed frame views where lifetime-safe, and keep `sync.Pool` limited to stateless temporary objects.
19. Concurrency defaults must be bounded and observable: worker pools, buffered channels, queue depth, acquire timeouts, context cancellation, and rejection metrics are part of the contract.
20. Network tuning follows the runtime ladder: direct/frame dispatch for same-process hot paths, `ffi`/`shm`/`stdio` for native runtime boundaries, generated protobuf or `grpcsvc.Frame` for cross-host service calls, and JSON only as a compatibility adapter.
21. PostgreSQL tuning starts with `EXPLAIN (ANALYZE, BUFFERS)` and `pg_stat_statements`; indexes must match actual predicates, and high-volume writes should use `COPY`, `pgx.CopyFrom`, or driver-native batching.
22. Long-lived connection paths must document deadlines, write queue bounds, topic authorization, lifecycle cleanup, backpressure, and overload shedding.
23. Significant performance decisions must update the relevant doc in the same change set: coding, database, benchmark, runtime, WebSocket, or this optimization tracker.
24. High-risk concurrency and transport optimizations must name visible state, hidden state, invariants, liveness/fairness, real-time bounds, and refinement/parity tests before becoming defaults.
25. Scale proof must include load-shaped local regression tests before service-backed load tests: tenant predicates, fanout, reconnect/churn, stampede coalescing, queue saturation, config convergence, and p95/p99 latency.
26. Hot fanout paths should prefer exact topics plus pre-indexed target sets. Wildcards remain useful for observability and broad subscriptions, but product hot paths should not scan broad pattern state per event.
27. Bounded observability buffers should use fixed rings instead of append-and-slice retention. Sustained event pressure must not allocate just to keep the latest `N` records.
28. WebSocket broadcast pressure should copy from a contiguous local connection index; map scans are acceptable for maintenance paths, not for high-frequency fanout.
29. In-memory test stores should model real query shape: tenant/scope indexes first, then scalar filter indexes, with defensive copies preserved at public API boundaries.
30. The scaffolded Postgres schema must include the same state-store table, uniqueness constraints, and scoped indexes that `server-kit/go/database.PostgresDB` expects. A generated app should not boot with `STATE_STORE_DRIVER=postgres` against a schema that only exists in the adapter.
31. At 1M local scale, avoid confusing target lookup with delivery. Resolving a user/device target should stay indexed; broadcast routing should use adaptive borrowed batches, while delivery remains bounded by per-connection write queues and slow-client policy.
32. Colon-delimited event names are not arbitrary strings in hot paths. Exact subscriptions should use direct maps, prefix wildcards such as `tenant:org_0042:*` should use prefix buckets, and complex wildcard scans should stay off product fanout lanes.
33. Store indexes must match both predicate and result order. For bounded list reads, the right shape is scoped/filter candidate selection plus order-aware early stop at `LIMIT`, not "scan broad state, sort everything, then trim".
34. Binary frame control fields have different cardinality. `EventType` and `SchemaVersion` are bounded vocabularies and can be interned in owned compatibility decode; `CorrelationID` and payload bytes are per-message data and must remain owned or borrowed according to lifetime.
35. The local Redis memory driver must behave like a real coordination substrate for tests: copied values, TTL expiry, token-checked locks, pattern pub/sub, approximate-cardinality API shape, and monotonic stream group read/ack semantics. Placeholder success paths hide contract drift.
36. Correlation tracing should start as a bounded local substrate: record compact per-correlation lifecycle events in a ring-like collector, then build debug endpoints/UI on top. Do not make trace introspection an unbounded event store.
37. Proto definitions are now a compiler input for nervous-system checks. Mutating request/response pairs should generate lifecycle contract vectors and call `VerifyCommandLifecycle`, so event naming, terminal refinement, idempotency, tenant scope, and worker metadata stay generated behavior.
38. Runtime pressure needs first-class local signals before service-backed load tests: event publish/receive trace, worker enqueue/process trace, Redis op latency/error counts, database op latency, pgx pool pressure, acquire-timeout errors, and queue depth. Service-backed benchmark configs and processes stay foundation-only; scaffolded projects inherit only minimal runtime budgets and debug endpoints.
39. Goroutine creation is a performance and liveness budget, not just syntax. Hot fanout, registry dispatch, Redis listeners, runtime workers, and batch ingestion must prefer bounded owners over unbounded per-item launches.
40. Message passing is not automatically safer than shared memory. Channel ownership, close authority, buffer capacity, select shutdown priority, and send/receive cancellation are part of the hot-path contract.
41. Timer/ticker usage belongs in the same lifecycle discipline as goroutines and channels. Avoid zero-duration placeholder timers, stop owned timers/tickers, and test timeout/cancel paths under load-shaped shutdown.
42. Go runtime deadlock and race detectors are evidence, not proof. Performance-sensitive concurrency work needs explicit leak/blocking tests, race runs where shared memory exists, and targeted select/channel/WaitGroup negative tests.
43. Mixed lock/channel/wait paths are high-risk optimization zones. Do not hold mutexes across blocking message operations, waits, or callbacks; when a fast path needs both shared state and messages, model the ownership boundary first.

**Phase 2 Implementation (Binary-First & Zero-Copy)**:

- **Singleflight Cache**: `GetOrSet` prevents cache stampedes via concurrent request coalescing and double-check locking.
- **Adaptive Concurrency**: Worker engine dynamically scales goroutine pools based on queue depth (up to 64 per queue) to handle traffic spikes.
- **Vectorized Batching**: Support for bulk `EventBatch` envelopes reduces syscall and JS event-loop overhead for high-frequency streams.
- **SharedArrayBuffer Ring Buffer**: Zero-copy log streaming from WASM to the host without main-thread blocking.
- **Beautiful Diagnostics**: High-density, table-aligned logging for sub-millisecond anomaly detection.

**Scale Pressure Pass (2026-05-09)**:

- **Exact Event Fanout**: Exact subscribers are separated from wildcard subscribers; hot tenant events avoid wildcard scans.
- **Prefix Event Fanout**: colon-prefix wildcards are bucketed by prefix so tenant wildcard routes scale with event depth and matching subscribers instead of total wildcard subscriptions.
- **Recent Event Ring**: In-memory and Redis event buses keep bounded recent history with a fixed ring buffer.
- **No-Alloc Event Validation**: common normalized envelopes validate event type and simple metadata without `strings.Split` or metadata map materialization.
- **WebSocket Route Index**: local broadcast resolves from a contiguous connection order index; user and device routes stay map-indexed.
- **Adaptive Broadcast Batches**: broad WebSocket fanout can iterate borrowed target chunks so routing scales by batch count instead of copying a huge slice or invoking one callback per connection.
- **Frame Control String Interning**: owned binary frame decode reuses low-cardinality event/schema strings while keeping correlation IDs owned per message.
- **MemoryDB Filter/Order Index**: scalar `Data` filters narrow tenant-list candidates and order-aware index logs let bounded reads stop at `LIMIT` before broad materialization.
- **Scale Harness**: `appbench` now exercises DB pressure, Redis fanout, WebSocket churn, cache stampedes, queue saturation, config convergence, and mixed p95/p99 latency without external services.
- **1M Scale Slice**: `BenchmarkScale1M_*` validates 1M record/connection/subscription shapes with fixed benchmark iteration counts.
- **Postgres State Store Alignment**: scalar JSONB filters push down before `LIMIT`, state-store methods use acquire/query budgets, raw JSON writes preserve bytes when map mutation is unnecessary, and the scaffold migration creates `governance_state_records` plus scoped indexes.

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
6. Add a recurring benchmark/profile review note for runtime lanes, database hot queries, WebSocket saturation paths, and worker queues.
7. Add a docs-tracking check in PR review templates for optimization changes that alter defaults, budgets, or benchmark expectations.
8. Add a Go concurrency review helper that flags risky `WaitGroup`, channel-close, timer, select, and lock/message combinations, then promote only low-noise patterns into hard CP checks.
