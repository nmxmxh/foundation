# Optimization Points

Date: 2026-05-22

This document tracks the deliberate performance and architecture carryovers folded into the scaffold after reviewing product histories and the current performance synthesis. For cross-cutting Go, networking, PostgreSQL, Rust, benchmarking, and documentation-tracking practices, see `docs/performance_practices.md`. For TLA+/`Specifying Systems` state-machine, invariant, liveness, real-time bound, composition, and refinement practices, see `docs/tla_architecture_practices.md`. For Go concurrency bug taxonomy and practices extracted from `go-study.pdf`, see `docs/go_concurrency_bug_practices.md`. For deep-dives into classic optimization techniques, see `docs/coding_magic.md`.

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
44. Columnar storage is now an explicit analytical lane, not a replacement for Postgres command truth. Use it for append-heavy facts, telemetry, reports, exports, and vector/batch compute where column pruning, late materialization, compression, and SIMD-friendly contiguous buffers are measurable wins.
45. Runtime arena payloads should evolve toward Arrow-compatible columnar batch descriptors for scan-heavy work: schema metadata, row count, validity bitmap, offsets buffer, and typed value buffers. Row contracts stay protobuf/Cap'n Proto; columnar batches are internal runtime/read-model payloads.
46. Virtual-memory behavior is part of hot native/runtime evidence. Cold/warm page-cache runs, minor/major page faults, RSS/PSS, mmap footprint, TLB/cache behavior, and NUMA placement belong in benchmarks when shared-memory, mmap, or large columnar payloads dominate.
47. FFI is a versioned calling-convention contract. ABI tests must cover version mismatch, null pointers, invalid lengths, UTF-8 diagnostics, error-buffer truncation, status codes, and parity against a non-FFI lane before product runtime units rely on it.
48. Query-engine thinking is now the default database review model: projection pushdown, predicate pushdown, limit pushdown, partition pruning, late materialization, and batch/stream execution must be visible in repository SQL and EXPLAIN evidence.
49. SSD performance work must reason about total write amplification, not just query latency. Review heap rewrites, index count, WAL bytes, full-page images, checkpoint cadence, autovacuum churn, HOT update ratio, TOAST-heavy row updates, and retention deletes before tuning hardware.
50. Postgres remains the row-store truth. Storage-engine ideas such as out-of-place page placement, hot/cold deathtime grouping, FDP/ZNS placement, and GC-unit alignment are useful mental models, but Foundation applies them through schema shape, append partitions, sidecar tables, retention drops, and measured WAL/vacuum/index pressure.
51. CPU microarchitecture is now a review layer for hot paths. Runtime arenas,
    database scans, websocket fanout, worker queues, and native kernels should
    count cache lines, preserve contiguous scan layouts, avoid pointer chasing,
    and isolate contended atomics before reaching for wider parallel lanes.
52. Search remains Postgres-owned by default. Weighted `tsvector`, `pg_trgm`,
    JSONB/expression indexes, tenant predicates, deterministic cursors, and
    projection-lag evidence come before any external search projection.
53. GPU is a throughput batch lane, not a control-plane shortcut. Promote work
    only after the bottleneck, transfer/readback cost, data layout,
    synchronization scope, fallback lane, and parity tests are explicit.
54. GPU tuning is empirical. Workgroup size, work per invocation, kernel
    fusion/fission, shared memory, register pressure, coalescing, occupancy,
    atomics, and auto-tuning parameters belong in benchmark notes when changed.
55. Branch predictability and memory-level parallelism are measured properties,
    not assumptions. Branchless rewrites, manual prefetch, SIMD, and higher
    fanout require profiles or benchmarks that include cache misses, branch
    misses, allocations, and p95/p99 behavior.
56. LSM/SSTable lessons translate into Foundation as append-only partitions,
    replayable outbox/audit/fact lanes, read-model refresh, VACUUM/analyze
    hygiene, and bounded worker pressure. They do not change Postgres being the
    durable row-store authority.
57. Database overload must surface as controlled pressure signals: pool acquire
    timeout, lock timeout, statement timeout, idle-transaction timeout, WAL
    growth, checkpoint pressure, autovacuum lag, and replica replay lag.
58. Native GPU acceleration has its own compatibility lane. Device capability,
    driver/runtime version, feature support, stream/queue ordering, graph
    support, memory-pool behavior, and fallback path must be declared before a
    kernel becomes a Foundation runtime option.
59. GPU correctness includes asynchronous failure and numeric drift. Launch
    status, synchronization status, device assertions, sanitizer gaps, ULP
    tolerance, NaN/Inf handling, reduction order, and host/device accuracy
    belong in tests for every non-trivial GPU lane.
60. GPU launch and transfer optimizations are edge-case heavy. Default-stream
    serialization, graph capture invalidation, prohibited operations, lazy
    loading, JIT/cache warmth, managed-memory migration, page oversubscription,
    peer access, and memory-pool reuse must appear in benchmark notes when
    those features are used.
61. Interactive runtime performance borrows from AAA game engines: optimize
    frame time, hitch count, first-use latency, pass boundaries, resource
    lifetime, and target-device captures before chasing average FPS or mean
    request latency.
62. Render-graph thinking applies outside rendering. Multi-stage media, canvas,
    GPU, native, streaming, and dashboard work should declare passes, resources,
    barriers, transient lifetimes, fallback lanes, and validation checks before
    low-level optimization.
63. Culling, LOD, instancing, batching, and streaming are data-reduction tools.
    Foundation should filter by visibility, tenant, permission, subscription,
    viewport, quality tier, and interest mask before decoding, ranking,
    uploading, dispatching, or rendering.
64. First-use hitches are production defects. Shader variants, PSOs, WebGPU
    pipelines, WASM modules, FFI backends, prepared SQL, route handlers, and
    hot caches need prewarm or explicit cold-path budgets when they touch the
    first viewport or first interaction.
65. Stable performance markers are now a Foundation review primitive. Marker
    names must be hierarchical and low-cardinality; correlation IDs, tenant IDs,
    hashes, and timestamps belong in fields so traces and captures can be
    compared across runs.
66. Hermes rebuild performance depends on using the right ingestion lane.
    Trusted snapshot refreshes use `BulkLoad`, incremental pure-upsert
    projector batches use `ApplyRecords`, and durable mixed mutation streams use
    `ApplyBatch`. Byte-bound estimation must stay approximate and allocation
    light; do not format every record field just to maintain a guardrail.
67. Typed payload contracts and JSON compatibility performance are separate
    optimization lanes. The 2026-06-02 typed refactor improved indexed/query/list
    and frame adapter paths by removing dynamic maps and conversion churn, but
    regressed HTTP/event JSON decode where compatibility adapters still build
    owned typed object trees early.
68. JSON ingress optimization now means direct decode, not map removal. Hot
    adapters should walk JSON tokens, generated schema fields, or protobuf
    reflection directly into `extension.Object`, domain structs, or preserved raw
    payload bytes. A `json.Unmarshal` to `interface{}` followed by typed
    conversion is an explicit compatibility fallback.
69. Binary/event envelopes should preserve raw payload bytes until the owner
    needs a typed object. Lazy object decode, borrowed views, and schema-guided
    validation are the preferred way to keep binary lanes healthy while retaining
    external JSON support.
70. Hermes service-backed projector performance depends on avoiding per-field
    object churn in trusted batch lanes. `BulkLoad` and `ApplyRecords` should keep
    growing toward borrowed or builder-backed `RecordData` flows, while
    `ApplyBatch` remains the durable mixed-mutation path.
71. Route-planned HTTP dispatch is now an opt-in generated/scaffolded lane.
    Compile path params and included headers once with `CompileDispatchRoute`,
    but do not copy an already-decoded payload merely to pre-size it; the copy
    can cost more than ordinary map growth.
72. Event JSON compatibility should parse envelope control fields separately from
    payload ownership. Single-pass top-level parsing plus lazy payload
    materialization gives the best current balance: time below the old baseline,
    old allocation count retained, and typed payload safety preserved.
73. JSON encoders now have two lanes: deterministic `MarshalJSON` for canonical
    output, drift, signing, and stable logs; unordered `MarshalJSONFast` for
    non-canonical compatibility responses where key order is irrelevant.
74. Hermes rebuild now supports an optional normalized snapshot interface. Keep
    `StateStore` canonical, and let high-performance sources opt into
    `NormalizedSnapshotStore` only when they can hand over already-normalized
    `RecordData` without JSONB re-materialization.
75. HTTP JSON dispatch should not retain raw request bytes unless the route
    explicitly asks for `IncludeRawBody`. Protobuf request bytes are the payload
    contract and remain retained; JSON routes should carry typed payload objects
    and preserve raw body only for audit/replay/streaming compatibility.
76. Redis Stream eventlog drains should avoid per-entry map adapters. Durable
    relay bursts use an ordered field/value append path for the common
    single-envelope field shape, while generic `XAddMany` remains available for
    multi-field stream entries.
77. Binary frame regressions must be split before optimization. Keep separate
    append-only, view-read-only, and full append/view benchmarks so nanosecond
    hot-lane movement can be attributed to write growth, field validation, or
    benchmark noise before changing the codec.

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
- **EventEmitter Struct Payload Optimization**: removed `json.Marshal` round-trip from the event publication path by implementing direct reflection-based conversion for custom structs within `extension.FromJSON`.
- **Deduplicated Correlation Extraction**: extracted correlation ID once from prepared metadata context to avoid redundant map lookups.
- **Registry Envelope Ownership**: skipped redundant envelope metadata cloning after decoding fresh payloads in the service registry dispatch.
- **Extended Registry Metrics**: added queue capacity and current length fields to `MetricsSnapshot` to monitor backpressure.
- **Handler Context Cancellation Check**: added context error checks in `graceful.Handler` to prune event emission on aborted requests.
- **Worker Backoff Timer Leak Fix**: ensured `timer.Stop` is always invoked when retrying worker jobs to prevent memory leaks.
- **Memory Client Batch Allocation Reduction**: reused buffer allocations within the Redis memory client when retrieving key arrays.
- **Lazy Metadata Deserialization**: Binary envelope decoders (`FromBinary`/`FromBatchBinary`) now defer metadata map creation by utilizing a `lazyMetadata` field, materializing the map only when `MaterializeMetadata()` is explicitly called. A zero-allocation fast-path validation is executed directly on the un-materialized protobuf structure.
- **Direct Struct Reflection Mapping**: Added direct `reflect.Struct` mapping support in `extension.valueFromReflect` to dynamically convert Go structs to `extension.Object` maps without a heavy `json.Marshal`/`json.Unmarshal` round-trip fallback.

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
9. Build a direct streaming JSON decoder for `extension.Object`/`extension.Value`, then route HTTP JSON dispatch, `events.FromJSON`, and JSON payload handling through it without `any` staging.
10. Add benchmark guards for `BenchmarkAppLane_HTTPIngress_JSONToDispatchRequest`, `BenchmarkEnvelope_FromJSON`, `BenchmarkEnvelope_FromBinary`, and service-backed Hermes rebuild/apply allocation budgets so typed-contract work cannot silently move cost into compatibility lanes.
11. Add a Hermes trusted-batch builder that can populate `RecordData` from validated projector rows with fewer per-field owned conversions, then compare it against the current `ApplyRecords` and `ApplyBatch` lanes.
