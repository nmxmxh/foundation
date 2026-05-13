# Foundation Benchmarks

Status: baseline  
Date: 2026-05-01  
Owner: Platform Architecture

## Purpose

Foundation performance work is not a single transport bet. The architecture uses a ladder:

1. same-process direct typed/frame dispatch
2. fixed binary frame codecs and borrowed frame views
3. generated protobuf for typed network payloads
4. gRPC for cross-host or polyglot process boundaries
5. JSON envelopes only as compatibility adapters
6. native runtime `ffi` or `shm` for trusted same-host hot units
7. browser worker + WASM + `SharedArrayBuffer` where the browser can support it

The benchmark suite exists to prove that ladder stays honest. The fastest lane should not pay network-stack or JSON costs, and the compatibility lane should remain visibly more expensive than the binary paths.

The benchmark suite does not replace architecture invariants. TLA-style rules live in `foundation/docs/tla_architecture_practices.md`: hard bounds and correctness properties must be tested as behavior; p95/p99, throughput, CPU, heap, and allocation shape are statistical evidence.

## 2026-05-11 cohesive substrate synthesis

The current direction is to make Foundation a small, coherent substrate rather than a broad collection of optional helpers. The sources reviewed point to the same rule from different angles:

1. Cerebras CS-2: large gains come from system-level co-design. For Foundation, the co-designed path is contract -> metadata -> dispatch -> event -> worker/cache/Redis -> realtime projection -> frontend store, with no layer inventing its own lifecycle.
2. Go concurrency: useful parallelism starts by partitioning work, doing local computation, and merging with bounded synchronization. For Foundation, this means tenant/key partitioning, exact fanout indexes, bounded worker queues, and channel/select timeouts rather than unbounded goroutines or sleeps.
3. Rust performance engineering: benchmark first, profile the actual hot path, optimize locality/allocation shape, then prove the delta. For Foundation, this means every new substrate claim gets a local test, a benchmark, and a doc entry before becoming a default.
4. The referenced Redis implementation: the useful shape is not the toy parser itself, but the command boundary, per-connection state, blocking waits via channels, TTLs, locks, and monotonic stream IDs. Foundation keeps Redis as ephemeral speed/coordination, but the local Redis memory driver must be real enough to test those contracts without external services.

Minimal plan:

1. Keep the canonical substrate narrow: typed/binary envelopes, correlation metadata, tenant scope, event lifecycle, bounded queues, Redis coordination, and realtime routing.
2. Prefer strengthening existing primitives over adding new packages. A generated app should inherit the correct lifecycle by default.
3. Use local proof harnesses before service-backed load tests. Memory Redis, in-memory event bus, MemoryDB, WebSocket routing, and worker queues should catch shape regressions quickly.
4. Promote external-service benchmarks only after the local shape is correct: real Redis Streams/pubsub lag, Postgres query plans, WebSocket slow-client pressure, and p95/p99 request budgets.
5. Treat generic wildcard/pattern matching as compatibility/observability unless a benchmark proves it is safe for product hot paths. Exact and colon-prefix routes remain the hot fanout shape.

## Measurement taxonomy

1. Correctness properties: invariants, allowed transitions, terminal states, metadata preservation, tenant isolation, and refinement/parity.
2. Worst-case operational properties: deadlines, queue caps, retry caps, acquire timeouts, payload limits, and overload behavior.
3. Statistical performance properties: ns/op, B/op, allocs/op, RPS, p50/p95/p99 latency, CPU profiles, heap profiles, and cache-hit ratios.

Benchmarks primarily cover the third category. Performance PRs that alter the runtime ladder must also include tests or contract checks for the first two categories.

## Go concurrency silver-lining metrics

The Go concurrency study in `docs/go_concurrency_bug_practices.md` gives Foundation a positive measurement checklist, not only a bug checklist.

Study signals to preserve:

1. Goroutines are shorter-lived and created more frequently than traditional threads. Use them for finite, owned work; measure active goroutines, start/stop counts, and shutdown drain time.
2. Mutexes are still the most common primitive in production Go, and channels are also heavily used. Benchmark the actual primitive boundary instead of assuming one is faster or safer.
3. Message passing caused more blocking bugs, while shared-memory misuse caused most non-blocking bugs. Measure both liveness and race/order safety.
4. Most blocking fixes were small synchronization changes. Keep code structured so the sync boundary is visible enough for review, tests, and future static checks.
5. Built-in detector coverage was incomplete. Race/deadlock runs are evidence, but leak tests, block/mutex profiles, queue metrics, and shutdown metrics are the performance guardrails.

Recommended benchmark and load-test additions for goroutine-owning code:

1. `steady`: active goroutines and queue depth stay bounded under target load.
2. `burst`: buffered channel or queue saturation produces measured reject/drop behavior, not unbounded heap growth.
3. `cancel`: request/job cancellation unblocks result goroutines and records cancellation propagation duration.
4. `shutdown`: long-lived listeners and workers drain or stop within the documented bound.
5. `profile`: goroutine, block, and mutex profiles are captured for hot fanout paths.
6. `race`: shared-memory packages run with `go test -race`, plus explicit tests for order/select/channel behavior that the race detector cannot see.

## How to run

From the repository root:

```bash
foundation/tooling/scripts/performance_check.sh
```

For the Go-only server-kit slice:

```bash
cd foundation/server-kit/go
go test -tags=perf ./grpcsvc ./chain
go test -bench='Benchmark(DispatchOverBufconn|DispatchFrameOverBufconn|DirectFrameClientDispatch|RouterDispatchFrameDirect|BinaryFrameCodecRoundTrip|BinaryFrameAppendRoundTrip|BinaryFrameAppendViewRoundTrip|GeneratedProtoMarshalAppendRoundTrip)$|BenchmarkRunParallel$' -benchmem ./grpcsvc ./chain
```

Set `PROFILE=1` when you need CPU and heap profiles under `/tmp/ovasabi-foundation-profiles`.

## Current reference run

Environment:

- Date: 2026-05-01
- OS/Arch: `darwin/arm64`
- CPU: Apple M1 Pro
- Command: server-kit Go benchmark command above

| Benchmark | ns/op | B/op | allocs/op | Role |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkRouterDispatchFrameDirect` | 16.18 | 0 | 0 | Same-process router dispatch |
| `BenchmarkDirectFrameClientDispatch` | 24.62 | 0 | 0 | Same-process client facade with validation |
| `BenchmarkBinaryFrameAppendViewRoundTrip` | 24.16 | 0 | 0 | Append encode + borrowed decode view |
| `BenchmarkBinaryFrameAppendRoundTrip` | 63.54 | 34 | 3 | Append encode + owned frame decode |
| `BenchmarkBinaryFrameCodecRoundTrip` | 104.4 | 178 | 5 | gRPC codec-compatible owned round trip |
| `BenchmarkGeneratedProtoMarshalAppendRoundTrip` | 360.2 | 152 | 6 | Generated protobuf marshal/unmarshal |
| `BenchmarkRunParallel` | 1197 | 592 | 8 | Bounded parallel operation chain |
| `BenchmarkDispatchFrameOverBufconn` | 22227 | 11034 | 183 | Binary frame over in-memory gRPC |
| `BenchmarkDispatchOverBufconn` | 27218 | 12690 | 213 | JSON envelope over in-memory gRPC |

## Latest Local Check

Environment:

- Date: 2026-05-05
- OS/Arch: `darwin/arm64`
- CPU: Apple M1 Pro
- Command: targeted server-kit Go benchmark commands from this document

| Benchmark | ns/op | B/op | allocs/op | Delta vs 2026-05-01 | Interpretation |
| --- | ---: | ---: | ---: | ---: | --- |
| `BenchmarkRouterDispatchFrameDirect` | 18.59 | 0 | 0 | slower/noisy | Still same-process, zero-allocation routing |
| `BenchmarkDirectFrameClientDispatch` | 25.68 | 0 | 0 | stable/slower | Validation facade remains zero-allocation |
| `BenchmarkBinaryFrameAppendViewRoundTrip` | 22.70 | 0 | 0 | faster | Borrowed frame view remains the fastest codec lane |
| `BenchmarkBinaryFrameAppendRoundTrip` | 62.25 | 34 | 3 | stable/faster | Owned binary frame path is stable |
| `BenchmarkBinaryFrameCodecRoundTrip` | 113.4 | 178 | 5 | slower/noisy | Codec-compatible owned path remains below protobuf |
| `BenchmarkGeneratedProtoMarshalAppendRoundTrip` | 386.9 | 152 | 6 | slower/noisy | Typed network payload lane remains sub-microsecond |
| `BenchmarkRunParallel` | 1712 | 592 | 8 | slower/noisy | Orchestration overhead is still microsecond-class |
| `BenchmarkDispatchFrameOverBufconn` | 31645 | 10969 | 181 | slower/noisy | Binary gRPC boundary stays much cheaper than external network I/O |
| `BenchmarkDispatchOverBufconn` | 39989 | 12653 | 212 | slower/noisy | JSON compatibility lane remains most expensive |

The important signal is unchanged: same-process and borrowed binary lanes are nanosecond paths, while gRPC/JSON lanes are microsecond boundary paths. Use benchmark deltas here as local evidence only; laptops, battery state, scheduler noise, and dependency versions can move these numbers by double-digit percentages.

### 2026-05-11 Redis memory substrate check

This pass replaced placeholder behavior in the local Redis memory driver with deterministic semantics for `Set`/`Get`/`Del`, TTL expiry, token-checked locks, pattern pub/sub, exact HyperLogLog-style cardinality, and basic stream group read/ack. The goal is not to emulate every Redis edge; it is to make local Foundation tests catch coordination and ephemeral-state drift before real Redis enters the loop.

Correctness tests added:

1. Pattern subscriptions match qualified channels and reject unrelated channels.
2. `Set`/`Get` returns copies and honors TTL expiry.
3. Locks reject concurrent holders, require matching unlock tokens, and expire.
4. Streams produce unique monotonic IDs, advance per consumer group, and accept ack calls.

Command:

```bash
cd foundation/server-kit/go
go test -run=^$ -bench='BenchmarkMemoryClient' -benchmem ./redis
```

| Benchmark | ns/op | B/op | allocs/op | Interpretation |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkMemoryClientGetHit` | 215.0 | 56 | 4 | Local copied `Get` is sub-microsecond; allocation is intentional ownership protection. |
| `BenchmarkMemoryClientPublish1KSubscribers` | 3145 | 58 | 3 | Exact pub/sub fanout to 1k local subscribers is microsecond-class. |
| `BenchmarkMemoryClientPSubscribePrefix1K` | 28277 | 64 | 3 | Generic Redis-style pattern fanout scans patterns; use Foundation exact/prefix event routing for hot product fanout. |
| `BenchmarkMemoryClientStreamXAddReadAck` | 1136 | 1015 | 17 | Local stream add/read/ack is now measurable and useful for contract tests, not a replacement for real Redis Streams load tests. |
| `BenchmarkMemoryClientLockUnlock` | 515.0 | 120 | 8 | Token lock/unlock is cheap locally; real Redis lock budgets still need network timeout and fencing-token checks. |

Follow-up benchmark gap: add service-backed Redis checks for stream group lag, pub/sub fanout loss under slow consumers, lock contention with TTL expiry, and pipeline chunk sizing once the dev stack is available. The first Docker-backed lane was added on 2026-05-11; keep this local memory harness as the fast regression net before running it.

### 2026-05-11 Lifecycle generator check

This pass makes proto definitions a compiler input for the Foundation nervous system. `tooling/scripts/generate_lifecycle_contract_tests.mjs` scans mutating request/response pairs and emits `tests/contract/generated_lifecycle_test.go` cases that call `VerifyCommandLifecycle`.

The scaffold example proto is now the reference fixture:

1. `CreateExampleRequest`/`CreateExampleResponse`
2. `UpdateExampleRequest`/`UpdateExampleResponse`
3. `DeleteExampleRequest`/`DeleteExampleResponse`

Each pair generates `:requested -> :success` and `:requested -> :failed` contract vectors with preserved correlation ID, idempotency key, tenant metadata, and worker job metadata.

Correctness checks:

```bash
node --check tooling/scripts/generate_lifecycle_contract_tests.mjs
tests/lifecycle_contract_generator_test.sh
tooling/scripts/contract_drift_check.sh .
tests/init_project_test.sh
cd server-kit/go && go test ./...
cd server-kit/go && go test -race ./contracttest ./observability ./redis
```

Result: generator syntax passed, the example proto generated six lifecycle vectors, contract drift checks passed, scaffold init generated the lifecycle test file in a fresh project, and server-kit tests/race checks passed.

### 2026-05-11 observed lifecycle and pressure substrate

This pass adds the implementation-test half of the lifecycle compiler path:

1. `contracttest.LifecycleRecorder` wraps a real `events.Bus`, records real worker jobs, and produces `LifecycleObservation` for `VerifyCommandLifecycle`.
2. Generated lifecycle tests now expose `verifyGeneratedLifecycleObservation` so app tests can bind observed handler output to proto-derived contracts.
3. `observability.Collector` records event trace entries, worker enqueue/process trace entries, Redis operation latency/error counts, database operation latency, pgx pool pressure, and queue depth.
4. The scaffold exposes a local correlation trace endpoint at `/metricsz/trace?correlation_id=<id>`.
5. Redis and worker startup now use inherited pool/timeout budgets instead of silently ignoring shard and timeout config.

Scaffold boundary: no new service-backed benchmark compose files, daemons, or test processes were added to generated projects. Service-backed Redis/Postgres benchmark assets now live under root `tests/` only, with a scaffold manifest guard preventing accidental inheritance.

Trace-retention guardrail benchmark:

```bash
cd foundation/server-kit/go
GOCACHE=/private/tmp/ovasabi-go-build-cache go test -run=^$ -bench='BenchmarkInMemoryBus_Publish_(NoSubscribers|1Subscriber|10Subscribers)$' -benchmem ./events
```

| Benchmark | ns/op | B/op | allocs/op | Interpretation |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkInMemoryBus_Publish_NoSubscribers` | 305.9 | 48 | 1 | Bounded per-correlation trace ring avoids post-cap slice copying. |
| `BenchmarkInMemoryBus_Publish_1Subscriber` | 312.4 | 48 | 1 | One exact subscriber adds little over trace/event validation. |
| `BenchmarkInMemoryBus_Publish_10Subscribers` | 362.0 | 48 | 1 | Synchronous local fanout remains sub-microsecond for small exact sets. |

### 2026-05-11 service-backed Docker substrate check

This pass adds `tests/service_backed_foundation_test.sh`, a Foundation-only live harness that starts isolated Redis 8 and Postgres 18 containers, waits for bounded health checks, runs tagged Go race tests against real services, then runs live benchmarks and tears the stack down.

The harness deliberately stays outside `templates/` and `tooling/scripts` so scaffolded projects do not inherit core benchmark processes or compose files. `tests/scaffold_manifest_test.sh` now fails if service-backed assets appear in the scaffold manifest, template tree, or scaffold-copied tooling script directory.

Live correctness covered:

1. Redis `Set`/`Get` ownership, TTL expiry, `Incr`/`Expire`, token locks, exact pub/sub, pattern pub/sub, stream group read/ack, HyperLogLog cardinality, and Redis operation metrics.
2. Postgres 18 state-store schema compatibility, scoped upsert/get/list/count, query-budget enforcement with `pg_sleep`, transaction rollback, `ExecResult`, pgx pool pressure, and database operation metrics.
3. Postgres pool saturation with `MaxConns=1` and eight concurrent callers, verifying bounded acquire wait, `ErrPoolAcquireTimeout`, pgx pool pressure visibility, and no unbounded queueing.
4. Postgres raw JSON state-store writes, verifying byte preservation at the handler boundary and server-side tenant stamping in stored JSONB.
5. Redis-backed `events.Bus` lifecycle flow using `LifecycleRecorder` and `VerifyCommandLifecycle`, including correlation ID, tenant metadata, idempotency key, worker job metadata, and trace capture.

Commands:

```bash
bash tests/service_backed_foundation_test.sh
cd foundation/server-kit/go
go test -tags=servicebacked -race -count=1 -timeout 5m ./servicebacked
go test -tags=servicebacked -run '^$' -bench=BenchmarkServiceBacked -benchmem -benchtime 1s -count 1 ./servicebacked
```

Research alignment:

1. Redis pipelining exists to amortize request/response RTT and socket syscall overhead. A sequential `SET` followed by `GET` is intentionally the slow comparison point; use `BatchClient.SetMany`, `GetMany`, or `SetGetMany` for multi-key cache hydration/write-through lanes.
2. PostgreSQL bulk guidance favors one transaction, prepared/batched statements, and `COPY` over repeated independent inserts. Foundation exposes those lanes through `PostgresDB.SendBatch` and `CopyFromRows`.
3. Docker volumes/tmpfs avoid the writable-layer penalty for database-like state. The service-backed harness uses tmpfs for Postgres and disables Redis persistence because Redis is not Foundation's durable recovery lane.

Local result:

| Benchmark | ns/op | B/op | allocs/op | Interpretation |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkServiceBackedRedisSetGet` | 512362 | 888 | 26 | Two sequential host-to-container round trips; this is the pessimistic baseline, not the target hot lane. |
| `BenchmarkServiceBackedRedisSet` | 230824 | 464 | 13 | One Redis round trip on Docker Desktop/localhost. |
| `BenchmarkServiceBackedRedisGet` | 319656 | 408 | 12 | One Redis round trip plus copied ownership boundary. |
| `BenchmarkServiceBackedRedisSetGetParallel` | 135454 | 1022 | 29 | Pool/concurrency hides some RTT; still one command pair per operation. |
| `BenchmarkServiceBackedRedisSetManyGetMany64` | 933156 | 51521 | 1053 | Two batch round trips for 64 keys, about 14.6us/key. |
| `BenchmarkServiceBackedRedisSetGetMany64` | 685292 | 49479 | 793 | One pipelined write/read batch for 64 keys, about 10.7us/key. |
| `BenchmarkServiceBackedRedisRawPipelineSetGet64` | 619035 | 31768 | 657 | Raw go-redis pipeline baseline, about 9.7us/key in this Docker run. |
| `BenchmarkServiceBackedPostgresUpsert` | 250843 | 3149 | 51 | Full `StateStore` semantics: JSONB payload, unique identity, timestamps, acquire budget, query budget, pool pressure. |
| `BenchmarkServiceBackedPostgresUpsertRawJSON` | 250273 | 2188 | 40 | Byte-preserving JSON write path for handlers that do not need map mutation; tenant key is stamped in JSONB by SQL. |
| `BenchmarkServiceBackedPostgresUpsertParallel` | 67960 | 3184 | 51 | Pool concurrency amortizes latency for independent tenant-scoped writes. |
| `BenchmarkServiceBackedPostgresSendBatchUpsert64` | 2639219 | 73451 | 938 | Batched upsert is about 41.2us/row. Keep diagnostics per row when using this lane. |
| `BenchmarkServiceBackedPostgresCopyFrom64` | 630576 | 39487 | 378 | COPY ingest is about 9.9us/row for append/import workloads. |

Implementation delta from live parity:

- Real Redis `GET` now returns `(nil, nil)` for missing keys to match the memory driver contract.
- Real Redis stream group reads now auto-create missing groups with `XGROUP CREATE ... MKSTREAM` and retry once on `NOGROUP`.
- The Postgres 18 service-backed compose file mounts tmpfs at `/var/lib/postgresql`, matching the official image layout for major-version-specific data directories.
- Redis `BatchClient` adds `SetMany`, `GetMany`, and `SetGetMany` so Foundation projects can use pipelined/cache-batch lanes without importing raw go-redis.
- Postgres `UpsertRecord` no longer round-trips and reparses the JSONB payload it just wrote; it returns timestamps only and keeps the normalized in-memory payload.
- Postgres state-store and bulk lanes now acquire explicit connections under `AcquireTimeout`, so `MaxConns` saturation produces bounded `ErrPoolAcquireTimeout` failures instead of silent pgxpool queueing.
- `RawStateStore.UpsertRecordJSON` adds a byte-preserving JSON path for handlers that already own canonical JSON. In this run it reduced state-store write allocation from 3149 B/51 allocs to 2188 B/40 allocs while keeping tenant stamping server-side in JSONB.
- Service-backed concurrent smoke tests now enforce p95 and p99 latency budgets for Redis and Postgres instead of p95 alone.
- Scaffold Redis now defaults to Redis 8 and disables RDB/AOF persistence because Redis is an ephemeral speed/coordination substrate; durable recovery belongs to Postgres/River/outbox.

### 2026-05-09 Core Tuning Check

This pass added an opt-in bound frame client for same-process hot routes. The default router and direct client behavior remains unchanged; the bound client resolves the frame handler once at startup and avoids the per-dispatch route-map lookup. Use it only for stable internal lanes where the route is known and registered during initialization.

The follow-up data-shape change keeps the binary frame wire format unchanged but interns low-cardinality control strings during owned decode. `EventType` and `SchemaVersion` are treated as bounded vocabularies; `CorrelationID` remains owned per message because it is high-cardinality. Borrowed `FrameView` remains the zero-copy lane for synchronous hot paths.

| Benchmark | ns/op | B/op | allocs/op | Interpretation |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkRouterDispatchFrameDirect` | 17.44 | 0 | 0 | Generic same-process router remains map lookup + handler call |
| `BenchmarkDirectFrameClientDispatch` | 22.49 | 0 | 0 | Validated client facade is modestly faster |
| `BenchmarkBoundFrameClientDispatch` | 11.11 | 0 | 0 | Bound same-process route nearly halves the direct-client facade |
| `BenchmarkBoundFrameClientDispatchTrusted` | 10.80 | 0 | 0 | Remaining cost is mostly the handler function call |
| `BenchmarkBinaryFrameAppendViewRoundTrip` | 22.10 | 0 | 0 | Borrowed binary view remains stable |
| `BenchmarkBinaryFrameAppendRoundTrip` | 51.41 | 8 | 1 | Owned decode now only owns the high-cardinality correlation string on warm control vocabularies |
| `BenchmarkBinaryFrameCodecRoundTrip` | 102.4 | 152 | 3 | Codec-compatible owned path keeps marshal allocation but avoids repeated control-string ownership |
| `BenchmarkDispatchFrameOverBufconn` | 22751 | 10932 | 178 | gRPC boundary remains microsecond-class; binary codec saves allocations but not the gRPC stack cost |
| `BenchmarkDispatchOverBufconn` | 28187 | 12688 | 213 | JSON envelope remains compatibility lane |

The practical performance target is not to force every lane under the same nanosecond budget. The route-bound path is the right tool for trusted in-process hot dispatch. Borrowed frame views are the right shape when data does not escape the source buffer; owned decode is now cheaper for compatibility paths that need durable strings. gRPC, JSON, queue, DB, and external Redis/Postgres lanes must be optimized with batching, pooling, backpressure, and tail-latency controls instead of pretending they are equivalent to a direct function call.

### 2026-05-09 Local Scale Pressure Check

This pass added `server-kit/go/appbench/scale_paths_test.go`, a no-external-service pressure harness for the scale questions that local nanosecond dispatch benchmarks cannot answer by themselves. It pushes tenant predicates, cache stampede coalescing, in-memory Redis fanout, WebSocket churn/routing, worker queue saturation, exact/wildcard event fanout, config convergence, and mixed p50/p95/p99 latency with local substitutes.

Environment:

- Date: 2026-05-09
- OS/Arch: `darwin/arm64`
- CPU: Apple M1 Pro
- Command: `cd foundation/server-kit/go && go test -run=^$ -bench='BenchmarkScale_' -benchmem ./appbench`

| Benchmark | First pressure run | Tuned run | Allocation change | Meaning |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkScale_MemoryDBTenantCount100K` | 5191 ns | 5177 ns | 0 -> 0 | Tenant count is index-shaped and stable |
| `BenchmarkScale_MemoryDBTenantListFiltered100K` | 36164 ns | 29708 ns | 41592 B / 105 allocs -> 33400 B / 105 allocs | Scalar `Data` filter index narrows candidates before defensive record copies |
| `BenchmarkScale_WebSocketBroadcastResolveInto100K` | 2495856 ns | 27382 ns | 0 -> 0 | Broadcast routing now copies from a contiguous connection index instead of walking the connection map |
| `BenchmarkScale_WebSocketUserResolve100K` | 231.4 ns | 227.8 ns | 0 -> 0 | User routing remains direct indexed lookup |
| `BenchmarkScale_EventExactDispatch100KSubscriptions` | 2179 ns | 194.1 ns | 1811 B / 13 allocs -> 0 B / 0 allocs | Exact event fanout is now map lookup + ring-buffer record + callback |
| `BenchmarkScale_EventWildcardDispatch1KSubscriptions` | 26050 ns | 23209 ns | 1790 B / 13 allocs -> 64 B / 1 alloc | Wildcard fanout still scans wildcard patterns; use exact tenant topics for hot paths |
| `BenchmarkScale_ConfigConvergence10K` | 159.3 ns | 158.2 ns | 0 -> 0 | Runtime config validation is not a deploy-time bottleneck in-process |
| `BenchmarkScale_LocalOperationMixLatency` | 10183 ns mean, 24084 ns p99 | 7874 ns mean, 15959 ns p99 | 3794 B / 34 allocs -> 1984 B / 21 allocs | Mixed local DB count + WS user route + cache hit + event publish + config validation stays sub-20 us p99 locally |

Correctness pressure covered by the local test:

- 100 tenants x 100 records with tenant predicates and filtered list checks.
- 1000 users x 10 WebSocket connections with unregister/register churn and broadcast resolution.
- 512 concurrent cache misses coalesced to one computation.
- 1000 exact subscribers per tenant with no cross-tenant delivery.
- 1024 in-memory Redis subscribers receiving one fanout payload.
- Worker queue fills to the bounded 1024 capacity and rejects overflow.
- 2048 concurrent runtime config validations converge without mutation.

What this proves: the foundation's local data structures are now shaped correctly for the distributed bottlenecks. Exact topics avoid broad fanout scans, WebSocket broadcast has a contiguous local index, user/device routes are indexed, cache stampedes coalesce, queue overflow is explicit, config validation is cheap, and p99 for the mixed local lane is tracked.

What this does not prove: real Postgres query plans, real Redis cluster fanout behavior, TLS/network jitter, kernel socket buffers, browser slow-client write queues, cross-region routing, or deploy orchestration behavior. Those need service-backed load tests with `EXPLAIN (ANALYZE, BUFFERS)`, Redis/pubsub metrics, WebSocket slow-consumer injection, and p95/p99 dashboards. The local harness is the fast regression net before those external proofs.

### 2026-05-09 1M Local Scale Check

This pass extends the local proof to 1 million records/connections/subscriptions and reconciles the in-memory store with the Postgres state-store contract. The scaffold migration now creates `governance_state_records` with the same identity key the adapter uses, scoped/order indexes for tenant queries, and a JSONB GIN index for app-specific containment queries. The Postgres adapter now pushes scalar JSONB filters into SQL before `LIMIT`, applies query budgets to state-store methods, and still rechecks filters in Go to preserve MemoryDB semantics.

Command:

```bash
cd foundation/server-kit/go
go test -run=^$ -bench='BenchmarkScale1M_' -benchmem -benchtime=100x ./appbench
```

| Benchmark | ns/op | B/op | allocs/op | Meaning |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkScale1M_MemoryDBTenantCount` | 5544 | 0 | 0 | Tenant count stays indexed at 1M records |
| `BenchmarkScale1M_MemoryDBTenantListFiltered` | 18360 | 33400 | 105 | Filtered list uses scoped/filter indexes, then pays intentional response-copy cost |
| `BenchmarkScale1M_MemoryDBDenseTenantListFilteredLimit` | 19051 | 33400 | 105 | Dense single-tenant filtered list uses order-aware field indexes and stops at `LIMIT 50` |
| `BenchmarkScale1M_WebSocketBroadcastResolveInto` | 556400 | 0 | 0 | Materializing 1M local connection IDs is a contiguous 16 MB string-header slice copy |
| `BenchmarkScale1M_WebSocketBroadcastForEach` | 2096710 | 0 | 0 | Per-connection streaming avoids materialization but costs one callback per connection |
| `BenchmarkScale1M_WebSocketBroadcastBatch` | 753.8 | 0 | 0 | Adaptive chunked broadcast routing uses borrowed slices and scales with batch count |
| `BenchmarkScale1M_WebSocketUserResolve` | 271.7 | 0 | 0 | User routing remains direct indexed lookup even with 1M local connections |
| `BenchmarkScale1M_EventExactDispatchSubscriptions` | 244.2 | 0 | 0 | Exact event dispatch remains constant-time at 1M subscription cardinality |

Data-shape check:

| Benchmark | Before | After | Allocation change | Meaning |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkScale1M_MemoryDBDenseTenantListFilteredLimit` | 603816285 ns | 19051 ns | 72032888 B / 105 allocs -> 33400 B / 105 allocs | Dense tenant reads must use order-aware scoped/filter indexes; sorting/materializing all candidates is the wrong shape for `LIMIT` |
| `BenchmarkScale_EventWildcardDispatch1KSubscriptions` | 23209 ns | 286.7 ns | 64 B / 1 alloc -> 64 B / 1 alloc | Colon-prefix wildcard subscriptions now route by prefix bucket instead of scanning all wildcard patterns |
| `BenchmarkScale_EventPrefixWildcardDispatch100KSubscriptions` | 2542881 ns | 333.8 ns | 64 B / 1 alloc -> 64 B / 1 alloc | Tenant prefix fanout scales by event depth and matching bucket, not total wildcard subscriptions |

Broadcast strategy check:

| Benchmark | ns/op | B/op | allocs/op | Meaning |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkScale_WebSocketBroadcastResolveInto1K` | 327.5 | 0 | 0 | Materializing 1k IDs is cheap when a stable slice is needed |
| `BenchmarkScale_WebSocketBroadcastBatch1K` | 32.08 | 0 | 0 | 1k broadcast routes as one borrowed batch |
| `BenchmarkScale_WebSocketBroadcastResolveInto100K` | 39578 | 0 | 0 | 100k materialization is still sub-0.1 ms, but copies target IDs |
| `BenchmarkScale_WebSocketBroadcastBatch100K` | 336.2 | 0 | 0 | 100k broadcast routes as adaptive borrowed batches |
| `BenchmarkScale1M_WebSocketBroadcastResolveInto` | 556400 | 0 | 0 | 1M materialization is dominated by copying the target slice |
| `BenchmarkScale1M_WebSocketBroadcastForEach` | 2096710 | 0 | 0 | 1M per-connection callbacks are too expensive for routing alone |
| `BenchmarkScale1M_WebSocketBroadcastBatch` | 753.8 | 0 | 0 | 1M adaptive batches keep routing overhead sub-microsecond in this run |

Interpretation: 1M local scale is not breaking the foundation data structures, but the API and container choice matters. Use `ResolveTargetsInto` only when the caller needs an owned/stable target list. Use adaptive `ForEachTargetBatch` for broadcast fanout so the router hands write queues borrowed chunks instead of copying a huge slice or invoking a million callbacks. Event wildcards should be exact or colon-prefix shaped for product traffic; complex wildcard patterns remain compatibility/observability tools. Returning 50 DB records allocates because public records are defensive copies. Dense tenant reads must stop at indexed `LIMIT`, not sort broad state. Broadcast to 1M live sockets is still a product-level write-pressure problem: it requires bounded per-connection queues, slow-client shedding, and node-level fanout budgets.

### Latest App-Lane Check

| Benchmark | ns/op | B/op | allocs/op | Role |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkAppLane_DirectFrame_DomainCall` | 42.09 | 32 | 1 | App-shaped direct frame domain call |
| `BenchmarkAppLane_HTTPIngress_JSONToDispatchRequest` | 6865 | 9652 | 74 | HTTP JSON ingress to dispatch request |
| `BenchmarkAppLane_Auth_ValidateToken` | 3860 | 2152 | 28 | JWT validation only |
| `BenchmarkAppLane_HTTPMiddleware_AuthSecurityRBAC` | 8335 | 11364 | 83 | HTTP middleware with auth, headers, validation, RBAC |
| `BenchmarkAppLane_Cache_GetHit_JSONValue` | 58.11 | 0 | 0 | In-memory cache hit |
| `BenchmarkAppLane_Retry_NoRetrySuccess` | 4.530 | 0 | 0 | No-retry success fast path |
| `BenchmarkAppLane_CircuitBreaker_ClosedSuccess` | 62.21 | 0 | 0 | Healthy dependency safety wrapper |
| `BenchmarkAppLane_Worker_EnqueueWithBackpressureAndDrain` | 5941 | 1368 | 26 | Accepted worker enqueue and drain |
| `BenchmarkAppLane_Worker_RejectFullQueue` | 1734 | 803 | 19 | Bounded queue rejection path |
| `BenchmarkAppLane_Worker_DropNoProcessor` | 1537 | 772 | 17 | Missing processor rejection path |
| `BenchmarkAppLane_Retry_CanceledWait` | 87.83 | 96 | 2 | Canceled retry wait path |

These app-lane results explain the practical architecture boundary: the foundation communication core remains far cheaper than real HTTP auth, route building, worker rejection, or domain persistence logic. Optimize product code by keeping hot internal calls on direct/binary lanes, then budgeting auth, DB, worker, and cache costs explicitly.

### Latest Foundation Hardening Pass

After reducing avoidable allocations in JWT bearer parsing, HTTP path parameter extraction, and worker job normalization:

| Benchmark | Before | After | Allocation change | Interpretation |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkAppLane_HTTPIngress_JSONToDispatchRequest` | 5570 ns | 5141 ns | 74 -> 71 | Path extraction no longer uses regex match allocation |
| `BenchmarkAppLane_Auth_ValidateToken` | 3380 ns | 3312 ns | 28 -> 27 | Token parsing avoids split allocation |
| `BenchmarkAppLane_HTTPMiddleware_AuthSecurityRBAC` | 7600 ns | 7455 ns | 83 -> 81 | Auth + middleware path inherits parsing improvement |
| `BenchmarkAppLane_Worker_EnqueueWithBackpressureAndDrain` | 5373 ns | 5294 ns | 26 -> 24 | Metadata-free raw jobs avoid empty map allocation |
| `BenchmarkAppLane_Worker_RejectFullQueue` | 1734 ns | 1647 ns | 19 -> 17 | Bounded rejection path is cheaper |
| `BenchmarkAppLane_Worker_DropNoProcessor` | 1543 ns | 1479 ns | 17 -> 15 | Missing processor rejection path is cheaper |

These are small but useful foundation-wide improvements because every scaffold inherits them. The bigger lesson is unchanged: auth, HTTP shaping, and worker queues are microsecond-scale safety boundaries. They are appropriate at ingress and async boundaries, but same-process hot domain calls should stay on direct frame or typed call lanes.

### Post-Quantum TLS Check

Local TLS 1.3 handshake benchmark:

| Benchmark | ns/op | B/op | allocs/op | Role |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkTLSHandshake_ClassicalX25519` | 406751 | 72159 | 813 | Classical TLS 1.3 local handshake |
| `BenchmarkTLSHandshake_HybridX25519MLKEM768` | 576089 | 102632 | 829 | Hybrid post-quantum TLS 1.3 local handshake |
| `BenchmarkApplyPostQuantumTLSAuto` | 196.6 | 964 | 3 | Config posture application |

Hybrid post-quantum TLS adds about 169 microseconds in this local handshake benchmark. That cost belongs at connection/session establishment or the edge terminator, not inside per-request JWT validation, render loops, domain handlers, or worker hot loops. The foundation posture remains: use standardized hybrid TLS where supported, keep signatures for durable artifacts and compliance workflows, and benchmark before moving post-quantum signatures into any request path.

### Phase 2 Core Utilities (Server-Kit)

| Benchmark | ns/op | B/op | allocs/op | Role |
| --- | ---: | ---: | ---: | --- |
| `Cache.Get` (Memory) | 53.4 | 0 | 0 | Ultra-low latency cache read |
| `Worker.MapJob` | 5.72 | 0 | 0 | Minimum engine overhead |
| `CircuitBreaker.Execute` (Healthy) | 35.1 | 0 | 0 | Safety overhead per call |
| `Events.Fanout` (3 Subs) | 284.1 | 64 | 2 | In-process delivery latency |

These numbers are local references, not universal budgets. CI and developer laptops vary. The important signal is the ordering and allocation shape.

## Comparative interpretation

Direct frame dispatch is the baseline for same-process hot communication. It validates the frame and calls the registered handler without gRPC, Redis, HTTP, JSON, or heap allocation. This is the lane to use when caller and handler share a process and lifecycle.

Borrowed binary frame views are the right parser shape for synchronous hot paths. `UnmarshalFrameView` returns slices into the original frame bytes, so routing and validation can inspect event type, correlation ID, schema version, and payload without copying. Owned decode remains available when data needs to escape the input buffer lifetime.

Generated protobuf remains the default for typed cross-process contracts. It is not zero-allocation in this benchmark, but it preserves schema discipline and avoids `map[string]any` materialization. Use it for service-to-service contracts where the payload semantics are stable and versioned.

gRPC is a boundary tool, not the default internal hot path. Even with `bufconn`, it is orders of magnitude slower than direct dispatch because it exercises client/server call machinery, interceptors, metadata, codec invocation, and message framing. That cost is acceptable for cross-host/polyglot boundaries and unacceptable for same-process routing.

JSON envelopes are compatibility-only. In the current run, binary gRPC frames use fewer allocations and fewer bytes than JSON envelopes over the same in-memory gRPC path. New hot communication APIs must provide typed or binary paths first and keep JSON as an explicit fallback.

The operation chain benchmark measures orchestration overhead, not external I/O speed. `chain.RunParallel` is intended for independent I/O-bound work where latency is dominated by storage, network, or service calls. It must preserve cancellation semantics: critical failures cancel the chain, non-critical failures are reported without blocking unrelated work.

### Phase 2 Performance Multipliers

**Singleflight Coalescing**: By integrating `singleflight` into the `cache` package, we eliminate the 100x latency spike typically seen during cache stampedes. Concurrent requests for the same missing key now wait for a single computation, converting a potential system-wide slowdown into a predictable sub-millisecond wait.

**Vectorized Batching**: Bulk processing of `EventBatch` envelopes reduces the frequency of JS event loop ticks and Go scheduler wakeups. For high-throughput streams (8kHz+), batching provides a 30-50% reduction in total system CPU consumption compared to single-event dispatch.

**Adaptive Worker Pools**: The worker engine's ability to scale based on queue depth ensures that throughput (hz) remains high even under sudden pressure, while keeping idle memory overhead near zero on quiet nodes.

## Runtime SDK comparison

The runtime SDK has a separate performance shape:

- `ffi`: trusted in-process mutation of the fixed 4KB runtime control buffer.
- `shm`: same-host process isolation with shared-file runtime buffer under `/dev/shm`.
- `stdio`: portable framed buffer exchange.
- browser worker/WASM: worker-owned execution with `SharedArrayBuffer` when cross-origin isolation allows it.

Do not reduce runtime parity to a single Wasm-host implementation strategy. The correct parity question is whether a unit produces identical buffer state across the lanes the product actually uses: status code, output bytes, diagnostics, and epoch transitions.

The next runtime benchmark target should compare:

1. native direct unit dispatch
2. FFI `process_buffer`
3. stdio framed runtime buffer
4. shared-memory transport on Linux
5. browser worker/WASM where available
6. Tauri-backed `runtime-native` IPC frame dispatch as a measured control boundary

Each run should report latency, bytes copied at transport boundaries, allocations where the language runtime exposes them, and failure-path behavior.

`runtime-native` benchmark entrypoint:

```bash
foundation/tooling/scripts/native_benchmark.sh .
```

The native benchmark is report-only until at least three stable local baselines exist. Its result must be compared against the existing same-process, FFI, shared-memory, WASM/SAB, WebSocket, and HTTP ladder before any native IPC path becomes a default.

The same script also runs `native_flow_sim`, which models communication-flow
copy budgets without requiring Tauri, Android, or iOS SDKs. It compares:

1. full-payload native control frames,
2. descriptor-only control frames that represent external native payloads,
3. the `runtime-sdk` fixed-buffer in-place path.

This simulation is part of the Foundation communication contract. It proves the
shape before platform SDKs enter the loop: control messages may copy small
binary frames, but hot payloads must stay in native buffers, shared arenas,
packet rings, or fixed runtime buffers.

2026-05-12 local Apple M1 Pro run:

| Lane slice | Payload | Mean | p50 | p95 | p99 | Notes |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| `runtime-native` dispatch frame | 4KB | 639.73 ns | 583 ns | 625 ns | 667 ns | In-process Rust bridge benchmark; Tauri IPC not included |
| `runtime-native` dispatch frame | 64KB | 10.18 us | 8.04 us | 12.83 us | 91.67 us | One tail outlier in this run; still report-only |
| `runtime-native` dispatch frame | 1MB | 111.99 us | 114.38 us | 134.83 us | 171.92 us | Copies payload through frame encode/decode and echo unit |
| native TS frame encode | ~1KB envelope | 1,000,386 ops/s | 1.0 us mean | 3.5 us p99 | 4.1 us p99.5 | Browser/JS frame construction cost |
| native TS response decode | ~1KB envelope | 3,248,841 ops/s | 0.3 us mean | 0.4 us p99 | 0.9 us p99.5 | Header validation and payload view |
| runtime-sdk Rust buffer output borrowed view | 2KB | 3.65 ns/op | n/a | n/a | n/a | Hot lane reference: no owned output copy |
| runtime-sdk Rust buffer fast output write | 2KB | 16.33 ns/op | n/a | n/a | n/a | Hot lane reference: trusted copy-only write |
| direct Go frame dispatch | control frame | 17.49-22.07 ns/op | n/a | n/a | n/a | Hot same-process control reference |
| bufconn dispatch | control frame | 20.33-24.83 us/op | n/a | n/a | n/a | Local RPC-style boundary reference |

Interpretation: `runtime-native` frame dispatch is viable as a local native control lane when the shell needs device access, platform lifecycle, secure storage, or mobile/desktop packaging. It is not the top of the performance ladder. Direct same-process frame dispatch, `runtime-sdk` fixed-buffer views, FFI, shared memory, and WASM/SAB remain the hot compute lanes when payloads are frequent or latency budgets are sub-microsecond.

2026-05-13 communication-flow simulation:

| Simulated lane | Represented payload | Mean | p50 | p95 | p99 | Modeled copy budget |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| full-payload native frame | 4KB | 659.63 ns | 625 ns | 750 ns | 833 ns | ~20KB/call, 5x payload |
| full-payload native frame | 64KB | 15.42 us | 15.04 us | 19.58 us | 28.33 us | ~320KB/call, 5x payload |
| full-payload native frame | 1MB | 149.20 us | 144.54 us | 174.17 us | 253.29 us | ~5MB/call, 5x payload |
| descriptor control frame | 4KB external | 381.21 ns | 333 ns | 375 ns | 458 ns | 0 hot-payload bytes; ~480B control |
| descriptor control frame | 64KB external | 323.30 ns | 292 ns | 334 ns | 416 ns | 0 hot-payload bytes; ~480B control |
| descriptor control frame | 1MB external | 323.16 ns | 292 ns | 334 ns | 375 ns | 0 hot-payload bytes; ~480B control |
| runtime buffer in-place | 1KB input | 144.52 ns | 125 ns | 167 ns | 167 ns | input view is zero-copy; current echo copies output and clears output region |

Interpretation: full-payload native control frames are linear in payload size and
should stay out of device hot streams. Descriptor control frames are effectively
constant with respect to represented payload size, which is the desired
Foundation shape for camera frames, microphone PCM chunks, sensor samples,
market ticks, and other packet-like streams. The control plane moves ownership,
schema, epoch, and buffer descriptors; the data plane stays in fixed buffers,
arena slabs, shared memory, WASM/SAB, or native packet rings.

Runtime lane planning now has explicit scheduling inputs: payload size, workload class, trust, locality, batch size, deadline, unit capabilities, and available hardware/runtime features. The planner must preserve the runtime contract while selecting the cheapest physical lane:

1. direct/same-process for trusted control payloads,
2. Rust FFI or CPU SIMD for trusted vector-sized work,
3. shared-memory or WASM/SAB for bounded same-host/browser payloads,
4. WebGPU for wide data-parallel batches large enough to amortize dispatch,
5. transfer or stream fallbacks when SAB/GPU lanes are unavailable.

For financial applications, Rust is the deterministic math lane, not the database orchestration lane. Use Rust for exact minor-unit conversion, checked integer arithmetic, fee/basis-point kernels, route scoring, settlement simulation, canonical payload hashing, and proof-adjacent state machines. Keep provider calls, Postgres transactions, policy lookups, audit writes, and request orchestration in Go/server-kit. A Rust boundary is justified when it removes floating-point ambiguity, batch-computes enough work to amortize the call, or needs parity across native/WASM/browser lanes.

GPU batch layouts should be benchmarked separately from descriptor-ring traffic. The browser-host helper packs batch regions on 256-byte boundaries by default so storage-buffer style workloads can move through the arena without per-item ad hoc layout decisions.

`RuntimeWebGpuHost` is intentionally split into testable pieces:

1. pack arena descriptors into aligned GPU input buffers,
2. create/cache compute pipelines asynchronously,
3. dispatch workgroups,
4. copy GPU output to a readback buffer,
5. write output slices back to the source arena descriptors.

Node tests validate the deterministic packing/writeback helpers without requiring a physical GPU. Browser/device benchmarks should add real `GPUDevice` dispatch metrics separately because adapter choice, driver, browser, and power state dominate those numbers.

## Browser shared-arena reference

The browser-host benchmark measures payload movement inside `RuntimeSharedArena`. Current local reference:

| Benchmark | hz | mean ms | p99 ms | Role |
| --- | ---: | ---: | ---: | --- |
| 4KB slab write/read | 736596.92 | 0.0014 | 0.0044 | Control-plane-sized payload movement |
| 64KB slab write/read | 102084.09 | 0.0098 | 0.0462 | Medium slab payload movement |
| 1024KB slab write/read | 8422.95 | 0.1187 | 0.6815 | Large slab payload movement |
| sustained descriptor-ready ring traffic | 922.79 | 1.0837 | 1.2702 | Descriptor queue pressure path |

The 4KB control-plane-sized path is roughly 7.22x faster than the 64KB slab path and 87.45x faster than the 1024KB slab path in this run. That supports the current design split: keep the hot control plane fixed and small, move larger payloads through arena descriptors or explicit streams, and benchmark descriptor-ring pressure separately from raw slab copies.

### Browser Shared-Arena Hardening Run

Environment:

- Date: 2026-05-07
- OS/Arch: `darwin/arm64`
- CPU: Apple M1 Pro
- Command: `cd foundation/runtime-sdk/ts/browser-host && npm run bench -- --reporter=verbose`

| Benchmark | mean ns | p99 ns | Interpretation |
| --- | ---: | ---: | --- |
| 4KB slab write/read | 1500 | 4000 | Owned read copy path |
| 4KB slab write/read view | 500 | 600 | Borrowed view avoids output copy |
| 4KB slab fast write/read view | 400 | 500 | Thin hot path for prevalidated descriptor use |
| 64KB slab write/read | 10700 | 55500 | Owned read cost grows with payload size |
| 64KB slab write/read view | 3000 | 3200 | View path stays near memory-copy cost |
| 64KB slab fast write/read view | 2900 | 3200 | Fast path is limited by slab copy |
| 1024KB slab write/read | 121400 | 762200 | Large owned copy path |
| 1024KB slab write/read view | 43100 | 62200 | Large view path avoids the second copy |
| 1024KB slab fast write/read view | 43100 | 60700 | Same limit: moving 1MB dominates |
| sustained descriptor-ready ring traffic | 1079900 | 1255700 | Single-entry queue CAS loop baseline |
| descriptor-ready batch traffic x128 | 1090900 | 1243800 | Old batched API still pays per-entry queue work |
| descriptor-ready fast batch traffic x128 | 487800 | 607600 | One tail CAS plus one head CAS per batch |
| preallocated write/enqueue/dequeue batch x128 | 57200 | 80600 | Best full hot lane when descriptors are already owned |
| descriptor release/reallocate free-list x1 | 313 | 400 | Reuse path is sub-4KB-copy cost |
| descriptor release/reallocate free-list x128 | 35100 | 55700 | Lifecycle churn stays below preallocated write/enqueue/dequeue x128 |

The major improvement is descriptor orchestration, not raw memory bandwidth. `descriptor-ready fast batch traffic x128` is about 2.24x faster than the old x128 batch path in this run. The new release/reallocate path also proves long-running processes can recycle descriptor IDs and their page-aligned slab regions without advancing the arena allocation head when the next request fits.

Operational rule: allocate descriptors for stable flows, reuse them aggressively, and use fast queue reservations only for batches. For x1, the old single-entry path remains competitive because the fast batch path has extra setup without amortization.

### Rust Native Buffer Hardening Run

Environment:

- Date: 2026-05-07
- OS/Arch: `darwin/arm64`
- CPU: Apple M1 Pro
- Command: `cd foundation/runtime-sdk/rust && cargo run --release -p ovrt-native --bin buffer_bench`

| Benchmark | ns/op | Interpretation |
| --- | ---: | --- |
| native read_output_bytes owned Vec | 68.58 | Copies output into a new owned allocation |
| native output_bytes_view borrowed | 4.05 | Borrows from the fixed control buffer |
| native write_output_bytes clear+copy | 55.37 | Clears the full output region, then copies payload |
| native write_output_bytes_fast copy only | 33.98 | Copies payload and updates length only |

The Rust-side optimization space is real but specific: prefer borrowed views when bytes do not need to outlive the control buffer, and use the explicit fast write path only when all readers honor the length field. The default clearing write remains available for defensive hygiene when stale bytes outside the active length must be erased.

Research notes:

- Rust `copy_nonoverlapping` is the `memcpy`-equivalent primitive for proven non-overlapping regions, but it is unsafe and requires strict validity/alignment guarantees. Safe `copy_from_slice` remains the default until a benchmark proves the unsafe path matters.
- Rust `std::hint::black_box` is appropriate for this local benchmark because it asks the compiler to avoid optimizing away the measured operation, but the standard library documents it as best-effort rather than a correctness mechanism.
- Crates such as `bytes` and `zerocopy` are relevant future options for cross-boundary owned/shared byte views, but the current 4KB control buffer is already simpler and faster with direct borrowed slices.

### Cross-Runtime Benchmark Expansion Run

Environment:

- Date: 2026-05-08
- OS/Arch: `darwin/arm64`
- CPU: Apple M1 Pro
- Commands:
  - `cd foundation/runtime-sdk/go && go test -bench='BenchmarkBuffer' -benchmem -run='^$' ./runtimehost`
  - `cd foundation/runtime-sdk/ts/browser-host && npm run bench -- --run`
  - `cd foundation/runtime-sdk/rust && cargo run -p ovrt-native --bin buffer_bench --release`
  - `cd foundation/server-kit/go && go test ./wsrouting -run 'TestResolveTargets|TestNilClient' -bench 'BenchmarkRouter' -benchmem`

Go runtimehost fixed-buffer results:

| Benchmark | ns/op | B/op | allocs/op | Interpretation |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkBufferSetInputBytes1KB` | 31.45 | 0 | 0 | Full input-region clear + copy remains allocation-free |
| `BenchmarkBufferInputBytesOwned1KB` | 184.6 | 1024 | 1 | Owned read cost is exactly the output copy |
| `BenchmarkBufferSetOutputBytes2KB` | 79.81 | 0 | 0 | Full output-region clear + copy remains allocation-free |
| `BenchmarkBufferOutputBytesOwned2KB` | 326.8 | 2048 | 1 | Owned output read allocates one copied payload |
| `BenchmarkBufferEpochAdd` | 7.165 | 0 | 0 | Atomic epoch movement is nanosecond-class |
| `BenchmarkBufferDiagnosticsText` | 345.9 | 768 | 1 | Current diagnostics read materializes a bounded string |

2026-05-09 follow-up: `BenchmarkBufferDiagnosticsText` is now 267.2 ns/op and 48 B/op after trimming the diagnostic byte region before string materialization. Borrowed input/output views remain about 3 ns/op and allocation-free; owned reads still allocate exactly the copied payload size.

The Go runtimehost hot write paths now clear fixed regions with `clear(...)` instead of allocating temporary zero slices. That preserves the security hygiene of clearing stale bytes while restoring the intended allocation-free control-plane write behavior.

Browser shared-arena update:

| Benchmark | mean ns | p99 ns | Interpretation |
| --- | ---: | ---: | --- |
| 4KB slab write/read | 1400 | 4400 | Owned read copy path remains around control-plane scale |
| 4KB slab write/read view | 500 | 600 | Borrowed view stays sub-microsecond |
| 4KB slab fast write/read view | 400 | 500 | Fast prevalidated path remains the browser hot lane |
| 64KB slab write/read view | 3000 | 3700 | Medium borrowed payloads stay near memory-copy cost |
| 1024KB slab write/read view | 43200 | 64400 | Large borrowed payloads avoid a second copy |
| descriptor-ready fast batch traffic x128 | 484000 | 665000 | Fast batch queueing remains about 2.26x faster than old x128 batch |
| preallocated write/enqueue/dequeue batch x128 | 61600 | 95500 | Best full arena lane when descriptors are pre-owned |
| packet-ring enqueue/dequeue/complete/release x128 | 43900 | 66200 | Packet-like lifecycle is cheaper than general arena descriptor orchestration |

Rust native buffer update:

| Benchmark | ns/op | Interpretation |
| --- | ---: | --- |
| native read_output_bytes owned Vec | 53.11 | Owned read copy is faster than the prior local run |
| native output_bytes_view borrowed | 3.73 | Borrowed native view remains the fastest runtime lane |
| native write_output_bytes clear+copy | 39.59 | Defensive clear + copy improved |
| native write_output_bytes_fast copy only | 16.65 | Fast write is the trusted hot path when stale bytes outside length are irrelevant |

2026-05-09 follow-up: `process_runtime_buffer_in_place` now operates directly on the caller-provided 4KB buffer instead of cloning the entire control plane into a temporary `Vec` and copying it back. The release benchmark reports `native process_runtime_buffer_in_place` at 143.44 ns/op for a 1KB echo unit on this machine. Public Go `ProcessResponse.Output` remains owned; FFI/process pools copy the active output before returning so pooled buffers cannot leak into caller-owned responses.

WebSocket routing local-load results:

| Benchmark | ns/op | B/op | allocs/op | Interpretation |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkRouterRegisterLocalOnly` | 534.9 | 228 | 4 | Local connection registration is sub-microsecond |
| `BenchmarkRouterResolveTargetsUserLocal` | 18758 | 59760 | 12 | Resolves 1024 local user targets without per-connection copy allocation |
| `BenchmarkRouterForEachLocal1024` | 37044 | 98304 | 1024 | Public copy-safe iterator intentionally allocates per connection |

The WebSocket routing improvement is behavioral-neutral: public read helpers still return copies, but `ResolveTargets` now resolves local user/broadcast targets under the router read lock and appends connection IDs directly. That keeps tenant/session safety semantics while removing avoidable per-connection object copies from realtime fanout.

Runtime-transport Go results:

| Benchmark | ns/op | B/op | allocs/op | Interpretation |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkCreateEnvelopeJSON` | 356.7 | 96 | 2 | Creates correlation/request/idempotency metadata with crypto randomness |
| `BenchmarkResolveRouteLinear16` | 33.37 | 0 | 0 | Small generated route tables are cheap even with linear lookup |
| `BenchmarkCanDispatchExactCapability` | 5.353 | 0 | 0 | Capability guard is effectively free on exact match |
| `BenchmarkSchemaRegistryNegotiate` | 31.15 | 0 | 0 | Schema negotiation is allocation-free for small accepted-version sets |

Runtime-transport TypeScript binary-envelope results:

| Benchmark | mean ns | p99 ns | Interpretation |
| --- | ---: | ---: | --- |
| encode JSON envelope to protobuf bytes | 7100 | 13000 | JSON payload materialization dominates binary envelope encode |
| decode JSON envelope from protobuf bytes | 2900 | 4300 | JSON payload decode is still microsecond-class |
| encode protobuf envelope bytes | 3900 | 8900 | Typed/binary payloads avoid JSON payload stringify |
| decode protobuf envelope bytes | 1700 | 2200 | Typed/binary decode is about 1.7x faster than JSON decode |
| encode JSON compatibility envelope | 1400 | 1900 | Compatibility JSON string path is fast for already-object payloads, but less typed |
| decode identity binary frame | 200 | 300 | Identity frame detection is effectively a header check |

Runtime-transport TypeScript routing results:

| Benchmark | mean ns | p99 ns | Interpretation |
| --- | ---: | ---: | --- |
| parse event type | 200 | 300 | Event contract validation is sub-microsecond |
| create JSON envelope | 700 | 800 | Browser envelope creation is cheap when IDs are provided |
| resolve route by event type | 100 | 100 | Precomputed route map lookup is effectively free |
| resolve route by path | 200 | 300 | Method normalization + path map lookup remains sub-microsecond |
| can dispatch exact capability | <100 | 100 | Exact capability fast path is extremely cheap |
| can dispatch write via admin fallback | 200 | 300 | Admin fallback remains sub-microsecond after removing transient arrays |

Transport improvement notes:

- Runtime binary frame decode now uses `subarray` for framed compressed payloads, avoiding a copy before decompression.
- Runtime metadata extras encoding reuses a constant `{}` byte sequence for empty extras and strips reserved JSON metadata keys lazily, avoiding unnecessary object copies for normal envelopes.
- Go correlation ID construction now uses a stack buffer before the final string conversion, reducing `CreateEnvelope` from 3 allocations to 2 in the local run.
- TypeScript event parsing now reuses precompiled regex objects, and capability fallback checks avoid transient arrays.
- Malformed binary frames now have explicit tests for unsupported version, unsupported encoding id, and truncated payload. These are security-relevant parser boundaries, not just benchmark fixtures.

### Packet-Ring Exploration

Foundation now carries a DPDK-shaped browser-host primitive for optional packet-like lanes: fixed descriptor slots, burst enqueue/dequeue, explicit ownership states, monotonic timestamps, and drop/high-water counters. This is not a DPDK dependency. It is the contract a future native packet adapter must refine.

Research alignment:

- DPDK ring guidance emphasizes fixed-size FIFO rings, lockless producer/consumer modes, and bulk/burst enqueue/dequeue. Foundation mirrors those mechanics at the runtime contract level.
- Linux timestamping separates ordinary software timestamps from hardware/NIC timestamps. Foundation treats timestamp precision as diagnostics and keeps domain-visible behavior independent of timestamp source.
- Solarflare/Onload-style acceleration is valuable because it can preserve socket-shaped application code while moving packet handling closer to hardware. Foundation follows the same compatibility principle: app code keeps server-kit/runtime contracts, while optional adapters can use lower-level lanes underneath.

Packet-ring benchmarks should be compared against descriptor-ring and preallocated arena paths, not against HTTP. HTTP pays for identity, middleware, routing, and compatibility; packet rings measure low-level lane mechanics.

Local exploratory run:

- Date: 2026-05-07
- Command: `cd foundation/runtime-sdk/ts/browser-host && npm run bench -- --reporter=verbose`

| Benchmark | mean ns | p99 ns | Comparison |
| --- | ---: | ---: | --- |
| packet-ring enqueue/dequeue/complete/release x1 | 400 | 500 | Similar class as 4KB fast arena view, with lifecycle timestamps |
| packet-ring enqueue/dequeue/complete/release x8 | 2800 | 3500 | Faster than preallocated arena x8 in this run |
| packet-ring enqueue/dequeue/complete/release x32 | 10900 | 20200 | Lower than preallocated arena x32, but with p99 noise from timestamp/lifecycle work |
| packet-ring enqueue/dequeue/complete/release x128 | 47900 | 109700 | Faster mean than preallocated arena x128, noisier tail |
| descriptor-ready fast batch traffic x128 | 495200 | 610800 | Packet ring is roughly 10.3x cheaper for packet-like local lifecycle work |
| descriptor-ready batch traffic x128 | 1097500 | 1289900 | Packet ring is roughly 22.9x cheaper than old descriptor queue orchestration |

Interpretation: packet-ring mechanics are useful for packet-like streams where descriptors are owned by a tight worker/runtime lane. They are not a replacement for the shared arena descriptor ring, which carries larger cross-lane payload ownership and WebGPU/WASM interop semantics. The current packet-ring tail is dominated by JavaScript object/lifecycle/timestamp work; a native/Rust version should use structure-of-arrays descriptor storage and monotonic timestamp sampling only at configured boundaries.

## Guardrails

1. Same-process hot dispatch must stay allocation-free.
2. Binary frame paths must allocate less than JSON compatibility paths.
3. Borrowed views must not retain data beyond the source frame lifetime.
4. Any new high-volume ingestion path must benchmark batch primitives against per-record writes.
5. Any benchmark improvement that changes behavior must land with correctness tests for malformed input, cancellation, oversized frames, and diagnostics.
6. Any optimized lane must prove refinement against the higher-level lane it bypasses or replaces: same canonical metadata, same accepted payload semantics, same terminal event, and same controlled error class.
7. Hard bounds such as queue depth, acquire timeout, write deadline, retry cap, and frame size are not benchmark targets; they are behavioral contracts and must have direct tests.
