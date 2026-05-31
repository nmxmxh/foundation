# TLA+ Architecture Practices for Ovasabi

Status: baseline
Date: 2026-05-05
Owner: Platform Architecture

## Purpose

This document translates the useful engineering techniques from Leslie Lamport's `Specifying Systems` into Ovasabi architecture practice. It is not a generic TLA+ tutorial. It identifies where TLA+ thinking should shape our performance, runtime, transport, cache, queue, WebSocket, worker, and database documentation.

Performance relevance: TLA+ does not optimize code directly. It prevents expensive wrong optimizations by forcing us to model state, bounds, fairness, hidden queues, cache coherence, timing, and refinement before implementation choices harden.

Related docs:

- `foundation/docs/performance_practices.md`
- `foundation/docs/runtime_foundation.md`
- `foundation/docs/foundation_benchmarks.md`
- `foundation/docs/database_practices.md`
- `foundation/docs/websocket_scaling.md`
- `foundation/docs/coding_practices.md`
- `foundation/docs/go_concurrency_bug_practices.md`

## Core translation

The book's core model is:

1. A behavior is a sequence of states.
2. A specification describes allowed behaviors.
3. `Init` describes valid initial states.
4. `Next` describes allowed state transitions.
5. Invariants describe what must always remain true.
6. Liveness/fairness describe what must eventually happen.
7. Real-time constraints describe bounded response windows.
8. Refinement proves that a lower-level implementation still satisfies a higher-level contract.

For Ovasabi, this maps to:

1. `Init`: bootstrap config, route registry, pool budgets, queue registration, runtime lane selection, auth/session/socket setup.
2. `Next`: request dispatch, event transition, queue enqueue/dequeue, WebSocket send/receive, runtime buffer epoch movement, DB transaction transitions.
3. Invariants: tenant scope, idempotency, bounded queue depth, frame size, cache coherence, connection ownership, monotonic epochs, no cross-org replay.
4. Liveness/fairness: queued work drains, authenticated sockets get terminal responses, runtime epochs advance, pending DB jobs either complete or fail visibly.
5. Real-time bounds: deadlines, acquire timeouts, p95/p99 budgets, ping intervals, TTLs, circuit-breaker windows, retry backoff ceilings.
6. Refinement: JSON compatibility adapters, protobuf frames, direct dispatch, `ffi`, `shm`, `stdio`, WebSocket, and HTTP must preserve the same domain contract.

## Go concurrency bug taxonomy

The Go concurrency study in `go_concurrency_bug_practices.md` adds a practical taxonomy for Foundation specs:

1. Behavior dimension: `blocking` means at least one required goroutine cannot make progress, even if the whole process is not deadlocked; `non-blocking` means all goroutines may finish but the visible state, order, or side effect is wrong.
2. Cause dimension: `shared memory` covers locks, RWMutex, atomics, WaitGroup, Cond, shared variables, and mutable values shared through contexts or channels; `message passing` covers channels, select, context cancellation channels, timers/tickers, Pipe-like streams, and mixed channel/lock paths.

Modeling implication: `DeadlockFree` is too weak for Foundation. We also need `NoPartialHang`, terminal-state reachability, goroutine ownership, channel ownership, cancellation observation, and shutdown-priority invariants for worker, runtime, registry, Redis, WebSocket, and event paths.

## First-principles performance reading

A broad grep pass over `Specifying Systems` for performance, optimization, safety, liveness, bounds, invariants, refinement, and model-checking terms produced these principles.

1. Worst-case performance is behavioral. If a system must react within a fixed time, that is a specification property and belongs with the architecture contract.
2. Average performance is not the same kind of property. It belongs in benchmark/load-test docs, not in a TLA-style correctness model.
3. Bounded resources are correctness boundaries. Real systems have finite queues, finite pools, finite socket buffers, finite retry budgets, and finite timeouts.
4. Some optimizations should stay out of the correctness spec. The cache example allows eviction at any time except where correctness forbids it; an implementation may evict only when space is needed.
5. Safety checks come first. Liveness/fairness is important, but the book explicitly treats liveness as subtler, slower to check, and usually less valuable for finding the first class of design errors.
6. A type invariant is not an assumption. In our docs, declaring `QueueBounded` or `TenantScopeStable` does not make it true; code, tests, and telemetry must prove it remains true.
7. Abstraction is a performance decision. Too fine-grained a model hides the useful system shape in noise; too coarse-grained a model hides races, backpressure, and latency.
8. Stuttering/no-op steps are real architecture facts: retries, suppressed duplicate requests, reconnect attempts, empty polls, cache hits that do not mutate state, and idempotent replays should be allowed without changing visible behavior.
9. Refinement is the formal version of "fast path must preserve semantics". A lower-level or optimized lane is acceptable only when it implements the higher-level contract under a clear state mapping.
10. Model-checking performance has its own discipline: finite model parameters, constraints, symmetry, views, simulation, and operator overrides are how we get useful checks without pretending to prove the whole production universe.

Ovasabi implication: docs should separate three layers that are often blurred:

1. Correctness properties: invariants, allowed transitions, forbidden states, visible semantics.
2. Worst-case operational properties: deadlines, queue bounds, retry caps, acquire timeouts, overload behavior.
3. Statistical performance properties: RPS, p95/p99 latency, allocation counts, CPU/memory profiles, benchmark deltas.

Only the first two belong in TLA-style specs. The third belongs in `foundation_benchmarks.md`, load-test reports, and production telemetry.

## When to write a lightweight spec

Use a lightweight TLA-style spec note before implementing or optimizing:

1. Bounded worker queues, retry loops, batch ingestion, or scheduler behavior.
2. WebSocket auth, topic subscription, reconnect, replay, backpressure, or connection routing.
3. Cache-aside, singleflight, write-through, invalidation, or cross-process consistency.
4. Runtime buffer, shared-memory, FFI, WASM, or epoch signaling behavior.
5. DB idempotency, uniqueness-sensitive mutations, advisory locks, and outbox/event flows.
6. Transport fallback ladders where several lower-level lanes claim to implement the same command.
7. Load shedding, circuit breaking, rate limiting, and degradation paths.

Do not write a formal spec for ordinary CRUD, static UI composition, or code whose behavior is already obvious and single-threaded.

## Specification template

Use this compact template in ADRs, design docs, or PR descriptions for high-risk performance/concurrency work:

```text
Visible state:
- What external state matters to users/operators?

Internal state:
- What queues, caches, locks, epochs, buffers, retries, or pool counters exist only to implement behavior?

Init:
- What must be true before the component starts accepting work?

Actions:
- ActionName: enabled when ..., changes ..., leaves ... unchanged.

Invariants:
- Always true properties.

Liveness/fairness:
- If work remains enabled, what eventually happens?

Real-time bounds:
- Upper/lower bounds, deadlines, TTLs, p99 budgets, retry ceilings.

Refinement:
- Which higher-level contract does this implementation preserve?

Model-check or test mapping:
- Unit/integration/load tests, benchmark, or TLC model that checks each property.
```

## Abstraction and atomicity

The book repeatedly emphasizes choosing the right abstraction and grain of atomicity. For us:

1. A WebSocket message send is usually one action; TCP packet movement is not.
2. A DB command handler may be one action only if the transaction is the behavior boundary.
3. Repository helpers should preserve one visible transition per operation: command, single-row query, bounded stream/list query, or transaction. Do not hide external network calls or unbounded waits inside database helper closures.
4. A batch ingest is not one action when per-record diagnostics, partial failure, or retry semantics matter.
5. Runtime buffer exchange should separate `write input`, `execute unit`, `write output`, and `advance epoch` when debugging parity or races.
6. Transport fallback should model `try lane`, `observe failure`, and `fallback` separately so cache/replay/idempotency behavior is visible.
7. Queue enqueue and dequeue are separate actions unless the system truly guarantees synchronous handoff.

Performance rule: coarser atomicity makes docs and code simpler, but can hide race conditions, backpressure, and latency. Choose the coarsest step that still exposes the failure modes we care about.

### Commutativity test for safe coarsening

The book uses action commutativity to reason about atomicity. Translate that into a practical review question:

1. Can two operations run in either order and produce the same visible state?
2. Does either operation enable, disable, or change the deadline of the other?
3. Does either operation mutate a field the other reads for authorization, idempotency, routing, or capacity?
4. Does moving the operations together hide an intermediate state that operators or retry logic rely on?

If the answer to 1 is yes and 2-4 are no, coarsening may be safe. If not, keep the actions separate in docs and tests.

Examples:

1. Independent object-storage uploads in a batch may commute if diagnostics preserve record identity.
2. DB transaction commit and outbox publish do not commute; the outbox record must be durable before publication.
3. WebSocket authentication and privileged subscription do not commute; subscription authorization depends on auth state.
4. Runtime output epoch advance and diagnostics write do not commute if readers may observe output before diagnostics.

## Invariants we should name

Every high-risk component should name invariants explicitly. These are examples to reuse.

### Runtime and transport

1. `FrameSizeBound`: no frame or payload exceeds the lane's documented max.
2. `MetadataPreserved`: correlation ID, idempotency key, session, user, organization, schema version, and locale survive dispatch.
3. `TenantScopeStable`: replay/cache keys cannot cross session, user, or organization changes.
4. `EpochMonotonic`: runtime epoch counters never move backwards.
5. `OutputAfterInput`: output epoch cannot advance for a unit before its matching input epoch.
6. `OwnedDecodeLifetime`: borrowed frame views are not retained after the source buffer lifetime.
7. `FallbackRefinement`: fallback lane produces the same domain result or a controlled error, not a different semantic event.

### Queues and workers

1. `QueueBounded`: queued items never exceed configured depth.
2. `NoUnboundedFanout`: each input event creates at most a bounded number of goroutines/jobs.
3. `AtLeastTerminal`: every accepted job reaches success, failed, cancelled, quarantined, or expired.
4. `RetryBudgetBounded`: retries never exceed `max_attempts` or deadline.
5. `IdempotentRetry`: retrying a job does not duplicate durable side effects.
6. `PerRecordDiagnosis`: batch failures retain record key/index and stage.
7. `CascadingDurability`: a worker that accepts a required child job must persist that child enqueue with a bounded detached context so parent cancellation does not erase the next enabled action.
8. `GoroutineOwned`: every goroutine has an owner, cancellation source, bounded wait/send behavior, and a terminal observation path.
9. `NoPartialHang`: a component is unhealthy if required goroutines are blocked, even when unrelated goroutines still run.
10. `ChannelOwnerKnown`: send ownership, receive ownership, close authority, buffer capacity, and overflow policy are defined for every channel.
11. `WaitGroupAddBeforeWait`: all intended `Add` transitions occur before a waiter can observe completion, unless a skipped add is the documented behavior.
12. `NoLockAcrossBlockingMessage`: lock ownership cannot depend on a channel operation, Cond wait, WaitGroup wait, network call, or unbounded callback.
13. `ContextCancelObserved`: cancel/timeout eventually unblocks owned listener, worker, runtime, and transport goroutines.
14. `SelectShutdownWins`: terminal shutdown/cancel actions cannot indefinitely lose to ready work actions.

### WebSocket

1. `ConnectionOwned`: each connection belongs to exactly one server instance at a time.
2. `AuthStateCurrent`: privileged actions require current session/user/org authorization, not only a successful socket upgrade.
3. `TopicAuthorized`: every subscription maps to an authorized topic scope.
4. `WriteQueueBounded`: outbound queue length is finite and saturation has a defined rejection/close policy.
5. `DeadlineMaintained`: read/write deadlines and ping intervals are refreshed or the connection closes.
6. `DisconnectCleansState`: local and Redis routing state is removed or expires after disconnect.

### Cache and database

1. `CacheCoherence`: cached copies for the same semantic key cannot disagree beyond the documented staleness window.
2. `SingleflightUniqueness`: concurrent cache misses for one key run one producer.
3. `InvalidationCoversWrite`: writes that change cached projections invalidate or refresh all affected keys.
4. `DBUniquenessAuthoritative`: app prechecks never replace database constraints for critical uniqueness.
5. `TransactionScopeBounded`: no external network call occurs inside an open DB transaction.
6. `QueryBounded`: runtime queries have finite limits, explicit order, and tenant predicates.

## Liveness and fairness

The book distinguishes safety from liveness and recommends expressing liveness through fairness on subactions of `Next`.

Use weak fairness language for normal systems:

1. If a queued job remains enabled and capacity is available, it is eventually attempted.
2. If a response remains sendable and the connection remains open, it is eventually flushed or the connection is closed with a controlled error.
3. If a runtime unit has valid input and a worker is healthy, output eventually advances or diagnostics report failure.
4. If a DB outbox event remains pending and the worker is healthy, it is eventually published or marked failed/quarantined.

Use strong fairness only when repeated intermittent enablement must still progress. This is rarer and should be documented explicitly. Example: a scheduler that should eventually process a tenant even if global capacity flickers.

Avoid ad hoc "eventually" claims that also restrict allowed steps. This is the book's machine-closure lesson: liveness should not secretly change safety. In code terms, a progress rule must not silently forbid valid intermediate states unless that restriction is intentional and documented.

## Real-time bounds

The real-time chapter models time with a `now` variable and defines lower/upper bounds on actions. Our practical equivalent is to document time budgets as part of the spec, not as incidental constants.

Use this shape:

1. `EnabledAt`: when work becomes possible or required.
2. `MinDelay`: lower bound before action may execute, usually `0` except rate limits, debounce, backoff, and scheduled jobs.
3. `MaxDelay`: upper bound before action must execute, reject, timeout, shed, or mark failed.
4. `ClockSource`: monotonic clock for durations; wall clock only for calendar semantics.
5. `ZenoGuard`: prevent infinite tiny progress that never reaches completion, usually by deadline, attempt cap, or monotonic budget burn.

Apply it to:

1. DB pool acquire timeout.
2. HTTP request deadline.
3. WebSocket ping/pong and idle timeout.
4. Worker lease and retry backoff.
5. Cache TTL and stale-while-revalidate window.
6. Circuit breaker open/half-open windows.
7. Runtime unit execution timeout.
8. Frontend replay/coalescing expiry.

### Worst-case vs statistical performance

Use TLA-style real-time thinking for hard or contractual upper bounds:

1. accepted HTTP command reaches terminal response/event within `MaxDelay` or fails visibly
2. DB pool acquire waits at most `AcquireTimeout`
3. WebSocket write waits at most `WriteDeadline` before close/reject
4. worker lease expires or renews within a bounded interval
5. runtime unit execution terminates, times out, or emits diagnostics within a fixed budget

Use benchmarks and telemetry for statistical performance:

1. p50/p95/p99 latency
2. throughput under load
3. allocations/op and bytes/op
4. CPU/heap profiles
5. cache hit ratios
6. pool utilization distributions

Statistical measurements still need an oracle. A p99 claim is only useful when
the benchmark names the workload shape, uses a consistent percentile formula,
collects enough samples for that percentile, and keeps fixture setup out of the
measured hot path unless setup is the behavior under test. If a fast smoke lane
uses a tiny fixed iteration count, treat its ns/op and allocation results as a
regression signal, not as evidence of tail latency. Tail latency belongs in a
separate benchmark or load test with enough duration, sample count, and variance
diagnostics to explain scheduler, GC, lock, pool, network, or filesystem noise.

## Hidden state

The FIFO and cache examples use internal variables such as queues and caches, then hide them when specifying external behavior. This is directly relevant to Ovasabi.

Visible behavior should be stable even when internal implementation changes:

1. A command's visible behavior is request accepted/rejected plus terminal event, not the exact queue internals.
2. Runtime visible behavior is input contract to output contract plus diagnostics, not whether the lane was `ffi`, `shm`, `stdio`, WASM, WebSocket, or HTTP.
3. Cache visible behavior is correctness and documented staleness, not the local data structure or Redis key shape.
4. WebSocket visible behavior is authorized delivery and lifecycle semantics, not local routing map layout.

Documentation rule: when internal state affects correctness or performance, document it in the implementation section, but keep the public contract expressed in visible-state terms.

## Refinement and transport ladder

The book's implementation idea is implication/refinement: a lower-level system implements a higher-level spec if every lower-level behavior is allowed by the higher-level spec, possibly after hiding internal variables or mapping lower-level state to higher-level state.

For Ovasabi, each runtime lane must refine the same command/event contract:

1. Direct typed dispatch refines the command contract.
2. Binary frame dispatch refines direct dispatch by preserving metadata and payload bytes.
3. Generated protobuf refines binary semantic contracts with schema.
4. Typed frame registration refines the same typed protobuf contract into the
   internal synchronous plane. One service binding must be sufficient to prove
   registry dispatch and frame dispatch are behaviorally equivalent after hiding
   transport variables such as frame payload bytes and router lookup state.
5. gRPC refines cross-host delivery with network failure modes.
6. WebSocket refines realtime client delivery with auth/topic/lifecycle constraints.
7. HTTP fallback refines request/response semantics with explicit retry/replay boundaries.
8. JSON compatibility refines the same contract only at boundaries and must not add hidden semantics.

For each lane, document:

1. State mapping: how lane-specific metadata maps to canonical metadata.
2. Hidden state: buffers, queues, retries, connection state, stream IDs.
3. Stuttering/no-op behavior: retries, reconnects, duplicate suppressed requests, empty poll results.
4. Error mapping: transport errors to graceful domain errors.
5. Tests: parity tests proving same input yields same accepted output/error class.

## Composition

The composition chapter maps cleanly to our architecture.

### Disjoint-state composition

Use when components own separate state:

1. Feature store plus UI rendering.
2. Independent worker queues.
3. Separate runtime units with no shared memory.

Each component gets its own `Init`, `Next`, invariants, and metrics.

### Shared-state composition

Use when components mutate a shared resource:

1. DB-backed workers sharing job rows.
2. WebSocket local map plus Redis routing keys.
3. Shared runtime buffer between host and unit.
4. Cache entries touched by HTTP handlers and background refreshers.

Document which component may change which field and which actions are allowed from other components.

### Joint actions

Use when a state transition requires both sides:

1. WebSocket authenticated upgrade and session binding.
2. Runtime `input epoch -> execute -> output epoch` handoff.
3. DB transaction plus outbox write.
4. Worker lease acquisition.
5. Cross-host request where client and server both update lifecycle state.

Joint actions need explicit ownership: who enables the action, who chooses the payload, who records success/failure, and who cleans up.

## TLC/model-checking workflow

We do not need to formalize everything, but TLC-style checking is useful for small finite models.

Good candidates:

1. Queue capacity, worker retry, cancellation, and terminal-state behavior.
2. WebSocket auth/subscription state machine.
3. Singleflight cache and invalidation races.
4. Runtime epoch protocol.
5. Outbox exactly-once/idempotent publication model.
6. Transport fallback and replay safety.
7. Go channel/lock handoff where one action owns shared state and another action owns message delivery.
8. WaitGroup producer/waiter ordering for worker pools and fanout chains.
9. Select shutdown priority for tickers, registry listeners, and long-lived pub/sub loops.

Model with small finite sets:

1. `Proc = {p1, p2}` or `Workers = {w1, w2}`.
2. `Org = {o1, o2}` to catch tenant bleed.
3. Queue depth `N = 2` or `3`.
4. Payload values as symbolic tokens, not real data.
5. Time budgets as small integers.

Check:

1. Type invariants.
2. Safety invariants.
3. Deadlock absence where appropriate.
4. Terminal-state reachability.
5. No cross-tenant state.
6. Refinement/parity between two implementations of the same contract.

Use symmetry when actors are interchangeable, such as workers or processors, to reduce state explosion. Use views only when omitted state is genuinely debug-only and not part of the property being checked.

### TLC performance discipline

The book's model-checking chapter is itself a performance guide. Apply these techniques when we create small formal models:

1. Keep production specs separate from TLC helper modules. Put finite bounds and checker-only constants in `MC*` modules or model configs.
2. Bound queues, actors, payload sets, and time values aggressively. A queue depth of `2` or `3` is often enough to expose ordering bugs.
3. Check type/safety invariants first. Liveness checking is slower and should come after obvious safety mistakes are removed.
4. Use coverage to find actions that are never enabled; use liveness to find actions that are sometimes incorrectly disabled.
5. Be suspicious when a model passes. A safety property can pass because the model does nothing; add progress or coverage checks that prove meaningful actions occur.
6. Use `CONSTRAINT` to limit reachable states for checking. Do not confuse model constraints with production correctness rules unless they are intentionally part of the real spec.
7. Use `VIEW` only for debug-only state. A bad view can skip states and break temporal checking.
8. Use `SYMMETRY` for interchangeable workers, tenants in synthetic models, processors, sockets, or queue consumers, but avoid liveness checking with symmetry unless the model is understood deeply.
9. Use simulation mode after exhaustive checking on the largest feasible small model. Random simulation is not proof, but it may find large-model bugs.
10. Override expensive mathematical operators with checker-efficient equivalents only in model-checking modules, preserving the same meaning on the finite model.

### First finite models to add

1. `MCWorkerQueue`: two workers, queue depth two, retry cap two, cancellation, terminal states.
2. `MCSocketAuth`: guest connect, authenticate, subscribe, auth expiry, disconnect cleanup.
3. `MCRuntimeEpoch`: input epoch, output epoch, diagnostics epoch, timeout, duplicate dispatch.
4. `MCSingleflightCache`: two callers, one key, producer success/failure, invalidation during in-flight work.
5. `MCTransportFallback`: direct/protobuf/ws/http lanes, retry/stutter steps, metadata preservation, controlled failure.
6. `MCOutbox`: DB write, outbox enqueue, publish, retry, idempotent terminal state.
7. `MCGoChannelShutdown`: sender, receiver, close owner, buffer capacity one, cancellation, and overflow/drop policy.
8. `MCGoWaitGroupFanout`: producer loop, two child goroutines, waiter, Add/Done ordering, and cancellation.

## Documentation tracking

When TLA-style analysis changes the architecture:

1. Add or update invariants in this document.
2. Add enforceable coding rules to `coding_practices.md`.
3. Add performance budgets or benchmark expectations to `foundation_benchmarks.md`.
4. Add transport/runtime implications to `runtime_foundation.md`.
5. Add DB implications to `database_practices.md`.
6. Add WebSocket implications to `websocket_scaling.md`.
7. Add adopted decisions and future targets to `optimization_points.md`.

Every high-risk optimization PR should answer:

1. What is the visible behavior?
2. What internal state is hidden?
3. Which invariant is being preserved?
4. Which liveness/fairness rule guarantees progress?
5. Which real-time bound prevents indefinite waiting?
6. Which refinement/parity test proves the optimized path still implements the original path?
