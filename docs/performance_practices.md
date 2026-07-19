# Performance Practices

Status: baseline
Date: 2026-05-22
Owner: Platform Architecture

## Purpose

This document synthesizes the useful performance guidance from Go production patterns, Go networking practices, PostgreSQL tuning notes, Rust optimization notes, and the local `Specifying Systems` book context into practices that apply to Foundation projects. The TLA+ architecture details live in `foundation/docs/tla_architecture_practices.md`.

The rule is simple: specify the behavior and bounds first, measure the system, then optimize the proven bottleneck without weakening contracts, tenancy, security, or diagnostics.

Related docs:

- `foundation/docs/coding_practices.md`
- `foundation/docs/database_practices.md`
- `foundation/docs/foundation_benchmarks.md`
- `foundation/docs/game_runtime_practices.md`
- `foundation/docs/go_concurrency_bug_practices.md`
- `foundation/docs/performance_lab.md`
- `foundation/docs/runtime_foundation.md`
- `foundation/docs/rust_runtime_practices.md`
- `foundation/docs/tla_architecture_practices.md`
- `foundation/docs/websocket_scaling.md`

## Operating model

1. Define the system boundary before tuning: actor, tenant scope, visible state, hidden/internal state, state transition, payload size, concurrency bound, timeout, and failure behavior.
2. Keep invariants explicit. Performance work must not make idempotency, authorization, ordering, replay, cache coherence, epoch monotonicity, or backpressure implicit.
3. Separate safety from liveness: first define what must never happen, then define what must eventually happen under fair/healthy conditions.
4. Benchmark before and after. A change without a baseline is a hypothesis, not an optimization.
5. Optimize the cheapest correct layer first: query shape before more hardware, batching before fanout, typed frames before JSON maps, direct dispatch before RPC.
6. Treat each lower-level performance lane as a refinement of a higher-level contract. Faster implementations must preserve canonical metadata, payload semantics, terminal events, and error classes.
7. Track every adopted optimization in docs when it changes a project default, benchmark expectation, concurrency budget, invariant, or operational runbook.
8. Agent-suggested optimizations remain hypotheses until the owning lane has a
   baseline, before/after measurement, invariant mapping, and fallback path.

## Realtime and frame-budget posture

Use `game_runtime_practices.md` for interactive loops, canvas/WebGL/WebGPU,
media, maps, dashboards, native previews, and any user-visible runtime surface
where hitches matter.

1. Use frame time and deadline budgets, not average FPS or mean latency. Track
   first-use, p95, p99, and max hitch separately.
2. Treat every visible interaction as a bounded frame transaction: main thread,
   worker, GPU queue, decode, network, database, object-store, and compose work
   each need a visible budget.
3. Data reduction comes before acceleration. Cull by viewport, subscription,
   tenant, permission, time window, visibility, interest, and quality tier
   before decoding, ranking, rendering, or dispatching GPU work.
4. Complex interactive work should use a pass graph: declare pass inputs,
   outputs, resources, barriers, transient lifetimes, fallback lanes, and
   validation rules before optimizing.
5. Stable performance markers are mandatory for interactive hot paths. Marker
   names must be low-cardinality and hierarchical; entropy belongs in fields.
6. Avoid first-use hitches. Prewarm shader/pipeline state, WebGPU modules,
   WASM modules, FFI backends, prepared SQL, route handlers, and cache entries
   where the first interaction would otherwise pay the compile/load cost.
7. Use capability profiles and quality tiers. Degrade visual/detail quality,
   update cadence, resolution, sample count, and refinement frequency before
   violating tenant, auth, idempotency, or command truth.
8. Capture-backed evidence is required for serious rendering/media/GPU changes:
   include build SHA, browser/runtime, driver, device, quality profile,
   feature flags, input seed, and whether async overlap was enabled.

## TLA+ specification layer

Use `tla_architecture_practices.md` for granular modeling guidance. The short version:

1. Model important behavior as `Init`, `Next`, invariants, liveness/fairness, real-time bounds, and refinement.
2. Use lightweight specs for queues, cache coherence, runtime epochs, WebSocket lifecycle, transport fallback, DB idempotency, outbox flows, load shedding, and scheduler behavior.
3. Name hidden state: queues, caches, locks, epochs, buffers, retries, and pool counters.
4. Name visible behavior: accepted/rejected command, terminal event, emitted response, persisted record, diagnostics, and observable delivery semantics.
5. Use weak fairness for normal progress guarantees: if work remains enabled and capacity is healthy, it eventually runs or fails visibly.
6. Use real-time bounds for deadlines, TTLs, acquire timeouts, retry ceilings, ping intervals, and execution budgets.
7. Use refinement/parity tests to prove optimized lanes still implement the original command/event contract.
8. Keep worst-case behavior and statistical performance separate: hard bounds belong in architecture/spec docs; p95/p99, RPS, CPU, heap, and allocation trends belong in benchmark and telemetry docs.

### Worst-case vs statistical performance

Hard upper bounds are contract facts: deadline, queue depth, retry cap, acquire
timeout, frame size, ping interval, lease duration, and execution budget.
Statistical measurements are evidence facts: p50/p95/p99, RPS, allocs/op,
bytes/op, CPU counters, heap profiles, trace spans, and benchmark deltas. Do
not replace one with the other. A fast path that can hang violates the contract;
a bounded path that is too slow needs benchmark and telemetry evidence.

## Go service hot paths

Use these defaults for `server-kit`, app services, workers, registries, and WebSocket handlers.

### Measure before changing code

1. Use `pprof` for CPU, heap, goroutine, block, and mutex profiles on load-tested paths.
2. Use `go test -bench ... -benchmem` for micro-level decisions such as allocation shape, parser changes, frame codecs, batching helpers, and lock strategy.
3. Capture p50, p95, p99, error rate, pool acquire latency, queue depth, and rejection counts during networked tests.
4. Treat `runtime.convT2I`, `runtime.assertI2T`, high allocation counts, mutex wait, and goroutine growth as signals to inspect, not automatic refactor triggers.
5. For Go concurrency changes, capture active goroutines by component, queue depth/capacity, blocked or rejected channel sends, shutdown drain duration, work-after-cancel count, and block/mutex profile samples.
6. A passing race run or absent runtime deadlock is not enough performance evidence. Pair `go test -race` with explicit leak/blocking tests and metrics for the specific goroutine/channel/lock boundary.
7. Profile a representative active workload, not an idle process. An empty CPU
   profile is still evidence: inspect waiting, syscalls, scheduler activity,
   allocation churn, blocking, and external round trips before concluding that
   the path has no bottleneck.
8. Separate cumulative allocation from retained memory. Capture
   `alloc_space`/`alloc_objects` to find garbage and construction churn, and
   `inuse_space`/`inuse_objects` to find footprint and retention. Neither view
   substitutes for the other.
9. Inspect cumulative work as well as time per operation. Record the counters
   appropriate to the lane: candidates inspected, results produced, bytes
   copied/touched, sort comparisons, rows removed by filter, serialization
   bytes, database/network round trips, syscalls, host/device transfers, or
   queue hops.
10. State the expected complexity class for data-structure and scan changes.
    A normal live heap can hide quadratic copying or repeated linear scans;
    source-line allocation profiles and size-series benchmarks must distinguish
    `O(1)`, amortized `O(1)`, `O(log N + K)`, bounded `O(N)`, and accidental
    superlinear behavior.

### Allocation discipline

1. Preallocate slices and maps when the expected size is known.
2. Use `strings.Builder`, `bytes.Buffer`, or append-style byte builders for repeated accumulation.
3. Reuse temporary buffers with `sync.Pool` only for stateless, recreatable, high-churn objects. Always reset before reuse.
4. Copy small retained subslices out of large buffers so long-lived records do not keep large backing arrays alive.
5. Keep hot communication payloads as bytes until the owning handler validates and decodes them. Avoid `map[string]any` materialization in routing, observability, and registry dispatch.
6. Treat JSON encode/decode as a compatibility boundary cost. Product hot paths should use generated protobuf, Cap'n Proto, typed structs, borrowed binary frame views, raw JSON bytes where preservation is required, or shared-memory descriptors.
7. For open extension fields, prefer Foundation's typed extension value container or protobuf `Struct/Value` at protobuf interop boundaries. Do not use `any` as a performance escape hatch; in Go it is an alias for `interface{}` and still requires dynamic dispatch and runtime type checks.
7a. Typed open values improve structural integrity and can reduce dynamic-map pressure in indexed/query/list and frame-adapter lanes, but they can regress JSON ingress when the decoder materializes an owned extension tree too early. Measure typed/binary and JSON compatibility lanes separately.
7b. JSON-to-typed adapters should use token-level decoders, generated field walkers, protobuf reflection walkers, or lazy raw payload preservation. Avoid `json.Unmarshal` into `interface{}` followed by a second typed conversion on any benchmarked ingress, event, or projection path.
7c. For event envelopes and HTTP dispatch, preserve raw payload bytes until the owner requests a typed object. Eager object construction is only acceptable when the owner immediately validates or mutates the value and the benchmark budget covers the allocation.
8. Prefer borrowed frame views for synchronous inspection; use owned decoded values only when data must outlive the input frame.
9. Use concrete types on fixed hot paths. Interfaces are fine at boundaries, but hidden boxing and dynamic dispatch must not sit inside tight loops without measurement.
10. Review struct layout when many instances are stored or scanned. Put larger fields before smaller fields when it materially reduces padding and cache waste.
11. Use escape analysis (`go build -gcflags="-m"`) when a hot allocation is unexpected. Avoid contorting code unless the benchmark proves the heap move matters.
12. For bounded stream copies, use caller-owned buffers or exact-size bounded reads when the size is part of the contract. Avoid accidental scratch allocation in byte loops, and benchmark both the materialized API and any callback/borrowed-view API separately.
13. For fixed-size checksum and identifier encodings, prefer stack-backed `hex.Encode`/`hex.Decode` into fixed arrays before the final string conversion. Reserve `hex.EncodeToString`/`hex.DecodeString` for cold paths or tests where the extra allocation is irrelevant.
14. Validate offset/length arithmetic with checked addition before slicing, issuing range reads, composing manifests, or building object-store byte ranges. Integer wraparound in a hot path is both a correctness bug and a potential unbounded allocation trigger.
15. Return borrowed readers or views for immutable in-memory payloads when the caller consumes them synchronously. Make a defensive copy only when storing caller-provided bytes, exposing mutable data, or allowing the view to outlive the owner.
15c. Pooled runtime executors should offer a caller-owned destination API when
    output size is bounded. The compatibility API may return an owned copy;
    the reuse API must reject undersized destinations and must never expose the
    pooled backing buffer after release.
15a. De-serialize protobuf event envelope metadata lazily. Store raw metadata pointers and parse the metadata map only when explicitly requested (for example, via `MaterializeMetadata()`). Perform fast-path validations directly on the protobuf structure to bypass map allocations.
15b. Convert custom structs directly to generic extension containers (like `extension.Object` maps) using reflection (`reflect.Struct` kinds) instead of performing expensive `json.Marshal`/`json.Unmarshal` round-trips in hot paths.
16. Do not infer allocation churn from RSS or live heap alone. A path may retain
    little memory while repeatedly allocating and discarding large backing
    arrays. Compare cumulative and retained profiles under the same workload.
17. Before pooling or shaving individual allocations, determine whether the
    path is CPU/GC-bound or waiting on a database, network, disk, device, or
    lock. Allocation cuts on a wait-bound path are capacity/p99 improvements
    only when GC CPU, throughput, or tail-latency evidence confirms them.
18. Review prepend, insert, concatenate, clone, grow, and exact-fit slice
    patterns for repeated whole-buffer copying. Use a deque/ring, reserved head
    room, chunked representation, builder, or ownership transfer when the
    operation otherwise becomes quadratic; preserve bounds and ownership tests.

### CPU microarchitecture posture

Modern cores hide some latency with pipelining, speculation, out-of-order
execution, caches, and SIMD, but Foundation hot paths still need layouts the
hardware can feed predictably.

1. Count cache lines, not just fields or bytes. Use 64-byte cache lines as the
   default review budget unless target hardware proves otherwise.
2. Prefer contiguous typed slices, byte buffers, and structure-of-arrays layouts
   for scan-heavy or vector-capable loops. Avoid pointer-heavy object graphs,
   map iteration, interface boxing, and JSON object materialization inside tight
   loops unless a profile shows the cost is irrelevant.
3. Keep independent hot counters, queue cursors, and ring slots away from false
   sharing. Contended atomics and producer/consumer cursors should be sharded,
   padded, or otherwise isolated when benchmarks show cache-line bouncing.
4. Separate hot predictable paths from cold exceptional paths. Unpredictable
   branches, polymorphic dispatch, and repeated type switches belong outside
   per-record loops when a branch-miss profile shows pressure.
5. Treat branchless code, manual prefetch, alignment tricks, and SIMD as
   measured specializations, not style defaults. They require scalar parity
   tests, realistic payload benchmarks, and tail-handling coverage.
6. Batch sizes must fit the working set, not only network or API convenience.
   For runtime/database scans, benchmark powers of two around L1/L2-friendly
   windows and the product's real payload sizes.
7. More goroutines or workers do not automatically create useful memory-level
   parallelism. Parallel scans need independent memory streams, bounded fanout,
   and cache-miss/branch-miss evidence; otherwise they can amplify cache and
   scheduler pressure.
8. Native/runtime benchmark reports for CPU-bound claims should include, where
   available, cycles, instructions, IPC, cache misses, branch misses, TLB misses,
   allocation counts, and p95/p99 latency.

### Concurrency discipline

1. Use bounded worker pools for untrusted, bursty, or externally fed work. Do not create unbounded goroutines per event, socket, row, or upload.
2. Prefer `server-kit/go/chain` for independent I/O-bound operations that need shared cancellation and per-step diagnostics.
3. Use buffered channels to absorb small bursts, not to hide unbounded backlog. Buffer size is a budget and should be observable.
4. Use `sync.WaitGroup` or `WaitGroup.Go` where available for waiting on known finite goroutine sets. Do not use sleeps as synchronization.
5. Use `sync.Once` for expensive lazy initialization that is safe to share.
6. Reduce lock scope before replacing locks. In read-heavy shared state, consider `sync.RWMutex`; for counters and flags, consider `sync/atomic`; for maps under high contention, consider sharding (for example, 128/256 independent partitions using a fast hash).
6a. **Lock-Free Reads**: For in-memory projection caches (for example, Hermes), reads must be completely lock-free. Leverage Copy-On-Write (COW) index snapshot swaps and atomic cell pointers to avoid read-blocking locks, protecting read latency from write batch pressure.
7. Share immutable snapshots freely across goroutines. Mutable shared state needs explicit ownership, synchronization, or copy-on-write semantics.
8. Every goroutine spawned from a request, socket, worker job, or ingestion batch must receive cancellation through `context.Context` or an equivalent lifecycle boundary.
9. In containers, validate `GOMAXPROCS` against cgroup CPU limits. Prefer an automatic setting such as `automaxprocs` where deployment does not already enforce this.
10. Prefer the primitive that matches ownership: locks for short critical sections, channels for handoff/order/ownership transfer, and atomics for narrow counters or flags.
11. Use Foundation observability concurrency signals for long-lived owners: `RecordConcurrency`, `RecordConcurrencyGauge`, and `RecordConcurrencyDuration` with low-cardinality `component`, `primitive`, `operation`, and `state` values.
12. Treat channel close, timer/ticker lifecycle, and shutdown select priority as performance concerns. Leaks and partial hangs show up as tail latency, queue lag, and failed drain behavior before they show up as obvious crashes.
13. Ready-worker selection must include observable load when multiple workers
    are eligible. Use least-in-flight or another benchmarked bounded policy with
    deterministic tie-breaking, and test that completion, timeout, removal, and
    synchronous dispatch failure all release load accounting.

## Network and transport performance

Ovasabi uses a transport ladder. Pick the lowest layer that preserves the required process boundary.

1. Same-process hot dispatch uses direct typed calls, direct frame dispatch, worker channels, or runtime buffer dispatch. It must not use gRPC, HTTP, Redis, or JSON for convenience.
2. Same-host trusted native compute may use `ffi`; isolated same-host compute may use `shm`; portable process boundaries may use framed `stdio`.
3. Cross-host or polyglot service boundaries use generated protobuf or `grpcsvc.Frame`; `grpcsvc.Envelope` and JSON maps are compatibility paths.
4. A typed service binding must feed both registry protobuf dispatch and frame dispatch when the service has typed contracts. Use `bootstrap.RegisterTypedHandlers` for ingress/event/lifecycle dispatch and `bootstrap.RegisterTypedFrameHandlers` for synchronous internal calls.
5. Benchmark the raw lane and the adapter lane separately. Raw frame dispatch tracks router and binary codec cost; typed frame adapter benchmarks include protobuf decode/encode plus bounded handler execution.
6. Browser runtime lanes use `sab -> wasm -> transferable -> ws -> http -> postMessage` according to availability and observed failure.
7. Long-lived sockets must enforce read limits, deadlines, ping/pong health, write queue bounds, topic authorization, auth expiry handling, and cleanup on close.
8. Backpressure is part of the contract. Use queue depth limits, acquire timeouts, load shedding, circuit breakers, and graceful rejection before overload becomes memory growth.
9. Connection lifecycle observability should cover DNS, dial, TLS handshake, protocol negotiation, read/write latency, disconnect cause, and retry path for external dependencies.
10. Use buffered readers/writers for high-volume stream I/O where it reduces syscalls without delaying latency-sensitive flushes.
11. Batch small operations across network and storage boundaries when correctness allows it. Preserve per-item diagnostics inside the batch.
    Eventlog publication is the reference service-backed pattern: fetch pending
    Postgres bytea envelopes through a bounded claim lease, pipeline Redis
    Stream `XADD`, and mark published rows with one token-checked Postgres
    batch update rather than one Postgres/Redis/Postgres cycle per event.
    Multiple drainers must use the claim path, not a plain pending-row select.
12. Tune socket options only after profiling shows the need. `TCP_NODELAY`, keepalive, receive/send buffers, backlog, and reuse settings are operational choices, not defaults to cargo-cult.
13. Optimize TLS with session resumption, ALPN correctness, modern cipher defaults, and cert-chain hygiene. Do not trade away security for small handshake gains.
14. Treat DNS as a latency source. Use resolver metrics, dialer instrumentation, and explicit caching/pre-resolution only when failure modes are understood.
15. Keep native and TypeScript frame codecs in parity with Rust frame limits. Encoders must reject payloads whose declared lengths exceed the shared frame budget; decoders must validate declared field lengths against remaining bytes before slicing or creating views.
16. In TypeScript byte lanes, prefer `subarray` views for same-owner, same-lifetime reads and `slice` copies for retained chunks, worker transfer boundaries, or any path where later mutation would violate the contract.

## PostgreSQL performance

The database rules in `database_practices.md` remain authoritative. The synthesized performance posture is:

1. Start with `EXPLAIN (ANALYZE, BUFFERS)` for slow or important queries.
2. Enable and use `pg_stat_statements` to find the highest total-cost queries, not just the most visible slow request.
3. Use the narrowest index that matches the actual query shape: compound, partial, expression, covering, GIN, or BRIN as appropriate.
4. Avoid `SELECT *`, runtime casts/functions on indexed columns, and large `OFFSET` pagination in runtime paths.
5. Batch writes with `COPY`, `pgx.CopyFrom`, or driver-native batch primitives instead of per-row loops.
6. Keep pgx statement caching enabled for repeated repository SQL; Foundation `PostgresDB` forces `QueryExecModeCacheStatement` and exposes cache capacity through `PoolOptions`.
7. Use materialized views, summary tables, Redis counters, or background refresh for repeated heavy joins and dashboards.
8. Tune `work_mem` locally for specific heavy sorts/joins; do not globally inflate it without concurrency math.
9. Treat autovacuum and bloat as production concerns. Track dead tuples, table/index bloat, vacuum lag, and index usage.
10. Use PgBouncer transaction pooling before allowing app replicas to create broad direct connection fanout.
11. Partition only when it matches access patterns such as time, tenant, or append-only history, and confirm pruning with `EXPLAIN`.
12. Read replicas can protect the primary from read load, but consistency expectations must be explicit in the feature contract.
13. Optimize like a query engine: push projections, predicates, limits, and
    partition keys as close to the scan as possible; then materialize wide rows
    only after scope, filter, and order have reduced the candidate set.
14. Read important plans as physical execution, not just SQL text. Identify
    scan type, join algorithm, sort/materialization nodes, rows removed by
    filter, heap fetches, temp files, WAL records, and buffer read/write/hit
    counts before changing indexes or code.
15. Treat SSD write amplification as a cross-layer metric. Minimize needless
    heap rewrites, index maintenance, WAL full-page images, checkpoint bursts,
    vacuum churn, row-by-row deletes, and wide JSONB updates before tuning the
    storage device.
16. HOT-update eligibility, table `fillfactor`, TOAST behavior, WAL compression,
    checkpoint sizing, and insert-triggered autovacuum are performance tools,
    but each changes a different bottleneck and must be validated with
    service-backed measurements.
17. Backpressure is a performance feature. Track acquire timeouts, lock
    timeouts, statement timeouts, idle transaction kills, worker queue depth,
    WAL growth, and replica replay lag together during load tests.
18. Separate storage-engine lessons by lane: Postgres owns durable row truth;
    append partitions, read models, materialized views, and columnar exports
    are where immutable-file and compaction ideas become Foundation practice.
19. Search performance is a Postgres read-model problem by default. Use
    weighted `tsvector`, `pg_trgm`, JSONB/expression indexes, tenant predicates,
    deterministic cursors, and `EXPLAIN` evidence before considering an external
    search projection.

## Columnar analytics performance

Columnar storage and column-shaped runtime buffers are optimization lanes for
wide scans, analytical grouping, reports, telemetry, and batch compute. They are
not replacements for Postgres transactional truth.

1. Start from query shape: columnar wins when the workload reads a few columns
   across many rows, filters whole chunks/partitions, or runs vectorized math.
2. Keep mutations and uniqueness in the row store. Project append-only facts
   into columnar/read-model lanes through bounded workers with replayable
   fingerprints.
3. Prefer compact read models or materialized views for hot dashboards before
   introducing a separate OLAP engine.
4. Use Parquet/Arrow/object-storage exports, DuckDB, ClickHouse, or warehouse
   jobs for report/export workloads that would otherwise scan transactional
   tables under product traffic.
5. Runtime columnar batches should use structure-of-arrays layouts: contiguous
   typed value buffers, optional validity bitmaps, offsets for variable-width
   data, and small metadata descriptors.
6. Benchmark row-oriented, materialized-view, and columnar/export paths against
   the same semantic result. Include bytes read, allocations, page-cache state,
   p95/p99, and projection lag.
7. Late materialization is a review principle: filter/prune on compact columns
   before constructing wide row objects, JSON maps, React state, or response
   DTOs.
8. Report `candidates_inspected`, `rows_selected`, and their ratio for filtered
   scans. Predicate pushdown that avoids materialization but still examines an
   entire large scope is an intermediate improvement, not an indexed query.
9. The planner should begin with the most selective eligible equality, ordered,
   range, bitmap, or composite index, then evaluate residual predicates over
   the reduced candidate set. Index memory, mutation cost, and tenant/scope
   bounds are part of the decision.
10. Benchmark scale series, not one cardinality, for candidate collection and
    data-structure changes. Include enough sizes to expose linear, logarithmic,
    and superlinear growth and report work per returned row.

## GPU and WebGPU performance

The detailed rules in `gpu_practices.md` are authoritative. The synthesized
performance posture is:

1. GPU lanes are batch lanes. Use them for wide homogeneous numeric, vector,
   media, signal, model, simulation, or columnar workloads that amortize
   transfer, dispatch, and readback.
2. Start from a bottleneck label: memory bandwidth, memory latency,
   uncoalesced access, branch divergence, load imbalance, synchronization,
   atomics, host-device transfer, kernel launch throughput, or CPU hot loop.
3. Prefer structure-of-arrays, typed columnar buffers, contiguous arena
   descriptors, and storage-buffer-friendly packing before changing algorithms.
4. Include host-device transfer bytes, queue submit, dispatch count, pipeline
   creation, readback, and fallback cost in benchmarks. Kernel-only timings are
   not enough for a Foundation lane decision.
5. Treat workgroup size, work per invocation, fusion/fission, shared memory,
   register pressure, and auto-tuning parameters as measured choices, not
   defaults.
6. Occupancy is a diagnostic, not the goal. Throughput, p95/p99, memory
   throughput, transfer cost, and parity against the fallback lane decide
   whether GPU promotion stays.
7. Browser WebGPU remains optional and worker-owned. React render paths receive
   state and results; they do not create devices, compile pipelines, dispatch
   workgroups, or map readback buffers.
8. GPU optimization must preserve Foundation invariants: metadata, tenant
   scope, result semantics, bounded work, fallback refinement, and controlled
   error classes.
9. Native GPU performance reviews must separate cold compile/JIT, first launch,
   warmed cache, steady-state dispatch, and lazy-loading behavior. Benchmark
   notes should include driver/runtime versions and relevant environment flags.
10. Streams, events, async copies, CUDA Graphs or command graphs, and memory
    pools are launch/transfer optimizations. Use them when they reduce measured
    queue, copy, allocation, or launch overhead, and document capture
    invalidation, prohibited operations, reuse rules, and fallback lanes.
11. Unified/managed memory needs page-migration evidence. Prefetch, usage
    hints, direct host access, CPU writes to GPU-resident memory, and
    oversubscription can dominate kernel time and must be measured as part of
    the lane.
12. Launch bounds, max-register controls, `__restrict__`, inline decisions,
    LTO, cooperative groups, warp shuffle/reduce primitives, and async barriers
    require before/after benchmarks plus parity tests. They are not cleanup.
13. Default-stream or implicit-queue serialization is a performance smell.
    Foundation adapters should use explicit streams/queues/events and expose
    ordering edges in diagnostics.

## Virtual-memory-aware profiling

For native runtime, shared-memory, ingest, and analytical paths, process memory
behavior is part of the performance contract.

1. Measure cold-cache and warm-cache runs separately when page cache materially
   changes latency.
2. Track minor and major page faults for native/shared-memory benchmarks where
   available.
3. Inspect RSS/PSS and mmap regions for long-running processes that reuse
   arenas, shared segments, memory-mapped files, or large object buffers.
4. Watch TLB/cache behavior with platform tools when a supposedly contiguous
   loop underperforms despite low allocation counts.
5. On multi-socket Linux hosts, treat NUMA placement as evidence for large
   native/columnar workloads. Thread and memory locality can dominate the
   algorithmic improvement.
6. Prefer page-aligned slabs and descriptor reuse for hot arenas. Repeatedly
   allocating and discarding large backing regions can trade heap pressure for
   page-fault pressure.

## Rust and native compute

Rust/WASM/native paths are reserved for compute locality and runtime parity, not as a blanket rewrite target.

1. Avoid unnecessary cloning; borrow with `&T`/`&str` where ownership is not needed.
2. Prefer contiguous collections such as `Vec` for iteration and cache locality.
3. Use static dispatch in hot generic paths where dynamic trait objects are measurable overhead.
4. Keep `unsafe` exception-only and documented. Techniques such as `MaybeUninit` require a clear benchmark, narrow scope, and correctness tests.
5. Use struct layout discipline for dense data and FFI contracts. Do not use packed layouts unless unaligned-access risks are handled.
6. Add `#[inline]` only for small, frequently called functions after measurement or clear compiler-boundary reasoning.
7. Preserve runtime parity across direct, `ffi`, `stdio`, `shm`, and browser/WASM lanes where the product uses those lanes.
8. Use PGO only after representative profiles exist. It is a release optimization, not a substitute for better algorithms or boundaries.
9. Validate lengths before indexing or allocating. Runtime frame readers must reject oversized declarations before allocating payload buffers.
10. Keep worker waits bounded and observable. A fast lane that can hang is not a valid refinement of the runtime contract.
11. Treat clone-to-borrow changes as first-class performance fixes: add tests for lifetime/ownership behavior and benchmarks for hot-path payloads.
12. Use explicit Cargo profile changes only with benchmark evidence. Profile
    settings are read from the workspace-root `Cargo.toml`, so app-owned Rust
    and vendored Foundation Rust need the owning workspace to record any
    `opt-level`, `lto`, `codegen-units`, `panic`, or `overflow-checks` decision.
13. Clippy's unsafe-documentation lints are part of the performance gate
    because unchecked pointer assumptions are often introduced during hot-path
    optimization. Unsafe optimization is not accepted without a local safety
    invariant and parity tests.

Use `rust_runtime_practices.md` for the detailed Rust runtime checklist and
`make check-rust` before merging Rust/WASM/native runtime changes.

### FFI ABI discipline

1. Treat FFI as a versioned protocol with a calling convention, not as ordinary
   in-process function calls.
2. Keep exported ABI surfaces C-compatible: scalar integers, lengths, raw byte
   buffers, opaque handles, and explicit error buffers.
3. Never pass language-owned strings, slices, trait objects, Go interfaces,
   Cap'n Proto builders, Arrow objects, or allocator-owned ownership across the
   raw ABI.
4. Validate pointers, lengths, UTF-8, schema versions, and writable ranges
   before use on the callee side.
5. Add conformance and parity tests for every product FFI runtime unit before
   treating the lane as production-capable.

## Go SIMD

Go 1.26's experimental `simd/archsimd` package is useful for carefully bounded
CPU kernels, but it is not a general replacement for Go orchestration or Rust
runtime lanes.

1. Build with `GOEXPERIMENT=simd` only in explicit benchmark/test jobs.
2. Keep SIMD code behind internal packages, build tags, and scalar fallbacks.
3. Benchmark before and after with realistic payload sizes; SIMD startup,
   alignment, masking, and tail-handling costs can erase gains on small inputs.
4. Prefer structure-of-arrays or contiguous typed slices so the compiler and
   hardware can amortize vector loads/stores.
5. Do not use experimental SIMD for public APIs, generated contracts, tenant
   isolation, authorization, database semantics, or event lifecycle behavior.
6. Compare against existing Foundation lanes: direct scalar Go, Rust FFI,
   shared-memory Rust, WASM/SAB, and WebGPU where the workload is batch-wide.

## Documentation and tracking workflow

When a performance practice becomes a project default, update the relevant doc in the same change set.

1. `foundation/docs/performance_practices.md`: cross-cutting synthesis and decision rules.
2. `foundation/docs/tla_architecture_practices.md`: state, invariants, liveness, real-time bounds, composition, and refinement mapping.
3. `foundation/docs/coding_practices.md`: enforceable code-review or CI rules.
4. `foundation/docs/database_practices.md`: schema, query, pool, migration, and operational database standards.
5. `foundation/docs/foundation_benchmarks.md`: benchmark commands, reference numbers, allocation guardrails, and interpretation.
6. `foundation/docs/runtime_foundation.md`: runtime ladder, binary transport, WASM/native, compression, and WebSocket posture.
7. `foundation/docs/optimization_points.md`: adopted optimization decisions and future targets.
8. `foundation/docs/websocket_scaling.md`: socket budgets, routing, metrics, and operational scaling notes.

Each tracked change should include:

1. The bottleneck or risk being addressed.
2. The baseline measurement or incident evidence.
3. The adopted practice.
4. The guardrail that prevents regression.
5. The doc and test/benchmark location that will stay updated.
6. The agent or reviewer evidence note when the change was AI-authored or
   AI-assisted, using `agent_operating_contract.md`.
7. The cumulative-work counters and expected complexity class when the change
   affects a loop, collection, index, codec, copy, batch, or projection scan.

## Review checklist

- [ ] Is the behavior specified with bounds, timeouts, payload limits, and failure semantics?
- [ ] Are visible state, hidden state, invariants, liveness/fairness, and refinement/parity expectations named for high-risk concurrent paths?
- [ ] Is there a baseline benchmark, profile, query plan, or load-test result?
- [ ] Does the optimization preserve tenant scope, idempotency, authorization, replay safety, and diagnostics?
- [ ] Does it use the correct Ovasabi performance lane for the process/network boundary?
- [ ] Are allocation, copying, locking, and goroutine growth visible in tests or telemetry?
- [ ] Are cumulative allocation and retained memory distinguished where memory behavior is part of the claim?
- [ ] Are algorithmic work, scale behavior, and work-per-result visible for scans and collection operations?
- [ ] Are database indexes and query predicates aligned?
- [ ] Is the documentation updated in the same change set?
