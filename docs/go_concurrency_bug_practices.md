# Go Concurrency Bug Study Practices

Date: 2026-05-13

Source parsed: `/Users/okhai/Desktop/go-study.pdf`, the ASPLOS 2019 paper "Understanding Real-World Concurrency Bugs in Go" by Tengfei Tu, Xiaoyu Liu, Linhai Song, and Yiying Zhang.

This document extracts foundation practices from the study and maps them to Ovasabi's coding, TLA-style architecture, runtime, optimization, and review checks. The paper studied 171 real-world Go concurrency bugs across Docker, Kubernetes, etcd, gRPC-Go, CockroachDB, and BoltDB. Its most useful result for Foundation is the taxonomy: classify each issue by behavior and cause.

Behavior:

1. `Blocking`: one or more goroutines cannot make progress. This is broader than classic deadlock because it includes goroutine leaks and waits for resources that no goroutine will ever supply.
2. `Non-blocking`: goroutines complete, but interleavings produce wrong state, extra work, panic, stale observation, or order violation.

Cause:

1. `Shared memory`: mutexes, RWMutexes, atomics, WaitGroups, Cond, shared variables, context/test objects shared by reference.
2. `Message passing`: channels, select, context cancellation channels, timers/tickers, Pipe-like stream libraries, and combinations of channels with locks or waits.

The important Foundation conclusion is direct: channels and goroutines are not a safety proof. Message passing avoids some shared-memory races when used correctly, but it caused more blocking bugs in the study than shared memory misuse. Every goroutine, channel, timer, ticker, and context-derived cancellation path needs ownership, bounds, and an observable terminal condition.

## Silver Lining: Measurable Good Practice

The paper is a bug study, but its metrics point to a constructive performance model:

1. Goroutines are useful when work is partitioned, finite, and owned. In the gRPC comparison, Go created far more goroutines than C threads while goroutine lifetimes were generally shorter. That is a good fit for Foundation fan-in workers, direct operation chains, runtime workers, and request-scoped parallel I/O when lifecycle ownership is explicit.
2. Mutexes remain the most common primitive in real Go systems, while channels are still heavily used. The useful practice is not "locks bad, channels good"; it is "use the primitive that matches ownership." Use locks for short critical sections around shared state, channels for handoff/order/ownership transfer, and atomics for narrow counters/flags.
3. Message passing produced more blocking bugs, but fewer non-blocking bugs. The positive reading is that channels can reduce shared-memory race surfaces when they carry immutable values or transfer ownership, but only if close authority, buffer capacity, cancellation, and select priority are designed.
4. Most blocking fixes in the study were small, with an average patch size of 6.8 lines and around 90% fixed by adjusting synchronization primitives. This means review and tooling can catch many issues early if the code names the primitive boundary clearly.
5. The built-in runtime detectors were incomplete: deadlock detection found only 2 of 21 reproduced blocking bugs, and race detection found 10 of 20 reproduced non-blocking bugs. The useful implementation lesson is to measure ownership and liveness directly, not just hope detectors fire.

### Preferred implementation shapes

1. `Bounded fan-in`: external bursts enter bounded queues, then a fixed/adaptive worker set drains them. Metrics: queue depth, capacity, rejected sends, worker count, processing latency.
2. `Owned handoff`: a channel transfers immutable data or ownership to exactly one receiver, with one close owner. Metrics: sent, received, canceled, dropped/full, closed.
3. `Context-first result`: child goroutines returning to a timing-out parent use a buffered result channel of capacity one, or a send select on `ctx.Done()`. Metrics: result sent, result abandoned after cancel, shutdown drain duration.
4. `Short lock plus message outside`: mutate shared state under a short lock, then perform blocking send/receive after unlock. Metrics: mutex/block profile samples, channel send latency, blocked/rejected sends.
5. `Finite operation chain`: independent I/O steps use `server-kit/go/chain` or equivalent bounded fanout with shared cancellation and per-step diagnostics. Metrics: started, completed, canceled, critical failure, total chain duration.
6. `Observable shutdown`: long-lived loops record started/stopped workers, cancellation observed, drain duration, and work attempted after cancellation. Metrics: active goroutines, shutdown success/failure, work-after-cancel count.

### Metric model

Use low-cardinality dimensions only: `component`, `primitive`, `operation`, and `state`. Never include request IDs, correlation IDs, tenant IDs, raw channel names, or dynamic goroutine IDs as metric labels.

Foundation's observability collector exposes a generic concurrency signal surface:

1. `RecordConcurrency(component, primitive, state)` for counts such as `registry|goroutine|started`, `worker|channel|send_rejected_full`, or `runtime|select|cancel_won`.
2. `RecordConcurrencyGauge(component, name, value)` for active workers, active goroutines, channel depth, channel capacity, blocked senders, or pending shutdown count.
3. `RecordConcurrencyDuration(component, operation, state, duration)` for shutdown drain time, channel send wait, worker acquire wait, result handoff latency, or cancellation propagation time.

Suggested states:

1. Goroutine: `started`, `stopped`, `panic_recovered`, `leaked_suspected`.
2. Channel: `sent`, `received`, `send_canceled`, `send_rejected_full`, `dropped`, `closed`.
3. Select/shutdown: `cancel_won`, `work_after_cancel`, `timeout_won`, `default_taken`.
4. WaitGroup/chain: `add_registered`, `wait_started`, `wait_completed`, `wait_timeout`.
5. Lock/message boundary: `lock_wait`, `block_wait`, `send_under_lock_rejected`.

Primary dashboards should show:

1. active goroutines by component
2. queue depth divided by capacity
3. rejected/dropped channel sends
4. shutdown drain p95/p99 and timeout count
5. work-after-cancel count
6. race test coverage for shared-memory packages
7. block and mutex profile samples for hot paths
8. goroutine profile growth across load-test start/steady-state/shutdown

### Scaffolded review check

Foundation ships `tooling/scripts/go_concurrency_practices_check.sh` into generated projects as `scripts/checks/go_concurrency_practices_check.sh`.

The check intentionally distinguishes two classes:

1. Hard failures stay in `coding_practices_check.sh` for low-noise known-bad patterns.
2. Broader channel/lock/select/lifecycle risks are emitted as `[REVIEW]` findings by `go_concurrency_practices_check.sh`.

The broad check reviews:

1. lock scopes that appear to contain channel/context/select work
2. `select` statements with `default`
3. loop-launched anonymous goroutines with implicit closure inputs
4. timer/ticker construction sites
5. channel close sites

Default behavior is report-only so generated projects do not fail on legitimate nonblocking sends, explicit drops, or owned close paths. Set `GO_CONCURRENCY_STRICT=1` to fail on review findings during a hardening pass.

Use `GO_CONCURRENCY_MAX_FINDINGS=<n>` to control the number of sample matches printed per review category.

## Pass 1: Performance and Lifecycle Pressure

Go makes goroutines cheap enough that codebases create them freely. That is useful for Foundation workers, runtime lanes, registry listeners, Redis subscribers, and WebSocket routing, but it also creates lifecycle pressure:

1. Treat each goroutine as an owned resource. It must have a parent context, shutdown path, bounded send/receive behavior, and a place where failure is observed.
2. Prefer bounded worker pools or `server-kit/go/chain` over per-item goroutine fanout on hot paths.
3. Channels in hot paths need explicit capacity rationale. Unbuffered channels are rendezvous points, not default queues. Buffered channels are bounded queues and need overflow policy.
4. A child goroutine that sends a result after the parent may time out must use a buffer, context-aware select, or an explicit cancellation contract so it cannot leak.
5. Timers and tickers are hidden goroutine/channel machinery. Do not create placeholder timers such as `time.NewTimer(0)` just to fill a select case. Create timeout channels only when enabled, and stop tickers/timers on every exit path.
6. Runtime deadlock and race detectors are useful but incomplete. Deadlock detection may miss partial hangs when other goroutines keep running; race detection misses non-race order bugs, channel panics, select nondeterminism, and some races that do not manifest under the sampled interleaving.

Watch points for checks and review:

1. `time.NewTimer(0)` and similar placeholder timers in production Go.
2. Unbounded goroutine creation in event fanout, queue processing, registry dispatch, or per-record ingestion.
3. Sends/receives that can block after caller timeout or shutdown.
4. Lack of goroutine leak tests for long-lived listeners and runtime/worker loops.

## Pass 2: Concurrency Correctness Pain Points

Shared memory and message passing both need explicit rules.

Shared-memory practices:

1. Use `defer Unlock`, `defer RUnlock`, `defer Done`, and `defer cancel` immediately after acquiring a resource unless a measured hot loop requires explicit cleanup.
2. Do not hold locks across channel sends/receives, WaitGroup waits, Cond waits, network calls, or callbacks into user-controlled code.
3. Avoid re-entering the same `sync.RWMutex` with `RLock` when a writer may be pending. Go's writer-preference behavior can make this block even if similar C pthread code would not.
4. `WaitGroup.Add` must happen before the goroutine can call `Done` and before another goroutine can observe `Wait`. Do not call `Wait` inside the goroutine production loop unless serial execution is explicitly intended.
5. Goroutine closures must copy loop/request values that can change before execution. Go 1.22+ improves loop-variable semantics, but Foundation code should still pass values explicitly when the value is part of the concurrency contract.
6. Values passed through channels or stored inside contexts can still point to mutable shared state. Passing a pointer through a channel is not data privatization.

Message-passing practices:

1. Channel ownership must be single-writer for close. If several paths can close a channel, guard with `sync.Once` or move close responsibility to a single owner.
2. Do not use `select { default: close(ch) }` as a safe close guard. It is a check-then-act race and can still double-close.
3. `select` is intentionally nondeterministic when multiple cases are ready. Shutdown and cancellation paths that must win need a pre-check, priority structure, or state flag.
4. Channel operations inside critical sections are high risk. If a send/receive can block, it can also block the lock holder and prevent the receiver/sender from making progress.
5. Nil channels are legitimate select controls only when intentional and documented. Accidental nil send/receive is an infinite block.

Watch points for checks and review:

1. `WaitGroup.Add` inside goroutine bodies.
2. `Wait` inside the same loop that launches goroutines.
3. double-close-prone channel shutdown paths.
4. channel sends/receives under `Mutex` or `RWMutex`.
5. select loops where shutdown can lose to a ready work case.

## Pass 3: Distributed and Runtime Liveness Pain Points

The study focused on Go process bugs, but the same patterns become distributed-system incidents in Foundation:

1. A leaked goroutine in a registry listener becomes stale routing state, missed shutdown, or duplicate event dispatch.
2. A blocked channel send in a worker becomes queue lag, missing terminal events, and user-visible timeout.
3. A missed context cancel becomes retained timers, stalled runtime workers, or orphaned downstream work.
4. A channel close panic in pub/sub code becomes event bus outage.
5. A select loop that processes one extra tick after shutdown can publish an extra event, double-send a response, or mutate state after cancellation.
6. A shared mutable pointer passed by channel can cross tenant/request/correlation boundaries if ownership is not explicit.

Foundation invariants to carry into specs and tests:

1. `GoroutineOwned`: every goroutine has an owner, cancellation source, and terminal observation path.
2. `NoPartialHang`: a component is unhealthy if required goroutines are blocked even when unrelated goroutines are still running.
3. `ChannelOwnerKnown`: one component owns send, receive, close, buffer capacity, and overflow policy for each channel.
4. `WaitGroupAddBeforeWait`: no waiter can observe the group before all intended adds are registered or intentionally skipped.
5. `NoLockAcrossBlockingMessage`: lock ownership cannot depend on a channel operation, Cond wait, WaitGroup wait, network call, or unbounded callback.
6. `ContextCancelObserved`: timeout/cancel paths eventually unblock worker, runtime, transport, and listener goroutines.
7. `SelectShutdownWins`: when shutdown/cancel is terminal for a loop, it cannot indefinitely lose to ready work cases.

Minimum test posture:

1. Use `go test -race` for concurrent shared-memory paths, but do not treat a clean race run as proof.
2. Add explicit leak/blocking tests for goroutine-owning components using bounded timeouts and terminal-state assertions.
3. Add table-driven tests for select tie behavior where shutdown, cancellation, and timeout races matter.
4. Add negative tests for double close, missing close, missed cancel, and `WaitGroup` Add/Wait ordering when code owns those primitives.
5. For queues, workers, runtime epochs, outbox publish, WebSocket lifecycle, and transport fallback, map these tests back to the TLA-style invariants in `docs/tla_architecture_practices.md`.
