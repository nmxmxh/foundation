# Ovasabi Performance Practices

Status: baseline
Date: 2026-05-05
Owner: Platform Architecture

## Purpose

This document synthesizes the useful performance guidance from Go production patterns, Go networking practices, PostgreSQL tuning notes, Rust optimization notes, and the local `Specifying Systems` book context into practices that apply to Ovasabi Foundation projects. The TLA+ architecture details live in `foundation/docs/tla_architecture_practices.md`.

The rule is simple: specify the behavior and bounds first, measure the system, then optimize the proven bottleneck without weakening contracts, tenancy, security, or diagnostics.

Related docs:

- `foundation/docs/coding_practices.md`
- `foundation/docs/database_practices.md`
- `foundation/docs/foundation_benchmarks.md`
- `foundation/docs/go_concurrency_bug_practices.md`
- `foundation/docs/runtime_foundation.md`
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

## Go service hot paths

Use these defaults for `server-kit`, app services, workers, registries, and WebSocket handlers.

### Measure before changing code

1. Use `pprof` for CPU, heap, goroutine, block, and mutex profiles on load-tested paths.
2. Use `go test -bench ... -benchmem` for micro-level decisions such as allocation shape, parser changes, frame codecs, batching helpers, and lock strategy.
3. Capture p50, p95, p99, error rate, pool acquire latency, queue depth, and rejection counts during networked tests.
4. Treat `runtime.convT2I`, `runtime.assertI2T`, high allocation counts, mutex wait, and goroutine growth as signals to inspect, not automatic refactor triggers.
5. For Go concurrency changes, capture active goroutines by component, queue depth/capacity, blocked or rejected channel sends, shutdown drain duration, work-after-cancel count, and block/mutex profile samples.
6. A passing race run or absent runtime deadlock is not enough performance evidence. Pair `go test -race` with explicit leak/blocking tests and metrics for the specific goroutine/channel/lock boundary.

### Allocation discipline

1. Preallocate slices and maps when the expected size is known.
2. Use `strings.Builder`, `bytes.Buffer`, or append-style byte builders for repeated accumulation.
3. Reuse temporary buffers with `sync.Pool` only for stateless, recreatable, high-churn objects. Always reset before reuse.
4. Copy small retained subslices out of large buffers so long-lived records do not keep large backing arrays alive.
5. Keep hot communication payloads as bytes until the owning handler validates and decodes them. Avoid `map[string]any` materialization in routing, observability, and registry dispatch.
6. Prefer borrowed frame views for synchronous inspection; use owned decoded values only when data must outlive the input frame.
7. Use concrete types on fixed hot paths. Interfaces are fine at boundaries, but hidden boxing and dynamic dispatch must not sit inside tight loops without measurement.
8. Review struct layout when many instances are stored or scanned. Put larger fields before smaller fields when it materially reduces padding and cache waste.
9. Use escape analysis (`go build -gcflags="-m"`) when a hot allocation is unexpected. Avoid contorting code unless the benchmark proves the heap move matters.

### Concurrency discipline

1. Use bounded worker pools for untrusted, bursty, or externally fed work. Do not create unbounded goroutines per event, socket, row, or upload.
2. Prefer `server-kit/go/chain` for independent I/O-bound operations that need shared cancellation and per-step diagnostics.
3. Use buffered channels to absorb small bursts, not to hide unbounded backlog. Buffer size is a budget and should be observable.
4. Use `sync.WaitGroup` or `WaitGroup.Go` where available for waiting on known finite goroutine sets. Do not use sleeps as synchronization.
5. Use `sync.Once` for expensive lazy initialization that is safe to share.
6. Reduce lock scope before replacing locks. In read-heavy shared state, consider `sync.RWMutex`; for counters and flags, consider `sync/atomic`; for maps under high contention, consider sharding.
7. Share immutable snapshots freely across goroutines. Mutable shared state needs explicit ownership, synchronization, or copy-on-write semantics.
8. Every goroutine spawned from a request, socket, worker job, or ingestion batch must receive cancellation through `context.Context` or an equivalent lifecycle boundary.
9. In containers, validate `GOMAXPROCS` against cgroup CPU limits. Prefer an automatic setting such as `automaxprocs` where deployment does not already enforce this.
10. Prefer the primitive that matches ownership: locks for short critical sections, channels for handoff/order/ownership transfer, and atomics for narrow counters or flags.
11. Use Foundation observability concurrency signals for long-lived owners: `RecordConcurrency`, `RecordConcurrencyGauge`, and `RecordConcurrencyDuration` with low-cardinality `component`, `primitive`, `operation`, and `state` values.
12. Treat channel close, timer/ticker lifecycle, and shutdown select priority as performance concerns. Leaks and partial hangs show up as tail latency, queue lag, and failed drain behavior before they show up as obvious crashes.

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
12. Tune socket options only after profiling shows the need. `TCP_NODELAY`, keepalive, receive/send buffers, backlog, and reuse settings are operational choices, not defaults to cargo-cult.
13. Optimize TLS with session resumption, ALPN correctness, modern cipher defaults, and cert-chain hygiene. Do not trade away security for small handshake gains.
14. Treat DNS as a latency source. Use resolver metrics, dialer instrumentation, and explicit caching/pre-resolution only when failure modes are understood.

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
11. Read replicas can protect the primary from read load, but consistency expectations must be explicit in the feature contract.

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

## Review checklist

- [ ] Is the behavior specified with bounds, timeouts, payload limits, and failure semantics?
- [ ] Are visible state, hidden state, invariants, liveness/fairness, and refinement/parity expectations named for high-risk concurrent paths?
- [ ] Is there a baseline benchmark, profile, query plan, or load-test result?
- [ ] Does the optimization preserve tenant scope, idempotency, authorization, replay safety, and diagnostics?
- [ ] Does it use the correct Ovasabi performance lane for the process/network boundary?
- [ ] Are allocation, copying, locking, and goroutine growth visible in tests or telemetry?
- [ ] Are database indexes and query predicates aligned?
- [ ] Is the documentation updated in the same change set?
