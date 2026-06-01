# Foundation Benchmarks

Status: active reference
Date: 2026-05-31
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

Metric meanings:

1. `ns/op`: average nanoseconds per operation in Go/Rust benchmark output. Lower means less CPU time, less waiting, or both.
2. `B/op`: heap bytes allocated per operation. Lower means less allocator and GC pressure.
3. `allocs/op`: heap allocation count per operation. Lower usually improves tail latency and cache locality.
4. `hz`: operations per second in Vitest benchmark output. Higher means more throughput.
5. `mean`, `p75`, `p99`, `p995`, `p999`: Vitest latency distribution converted to nanoseconds in this ledger. Lower tail values matter more for realtime/runtime paths than a tiny mean-only win.
6. `rme`: relative margin of error. Large values mean the result is noisy and should not be over-interpreted.
7. `samples`: number of benchmark samples collected. More samples usually gives a steadier distribution, but only for the same machine/load shape.

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
make test-bench-history
make test-bench
FOUNDATION_NATIVE_SKIP_BASELINE=1 tooling/scripts/native_benchmark.sh .
make test-service-backed
```

For the Go-only server-kit slice:

```bash
cd foundation/server-kit/go
go test -tags=perf ./grpcsvc ./chain
go test -bench='Benchmark(DispatchOverBufconn|DispatchFrameOverBufconn|DirectFrameClientDispatch|RouterDispatchFrameDirect|BinaryFrameCodecRoundTrip|BinaryFrameAppendRoundTrip|BinaryFrameAppendViewRoundTrip|GeneratedProtoMarshalAppendRoundTrip)$|BenchmarkRunParallel$' -benchmem ./grpcsvc ./chain
```

Set `PROFILE=1` when you need CPU and heap profiles under `/tmp/ovasabi-foundation-profiles`.

## 2026-05-31 full benchmark pass

This run is the current local reference for Foundation's performance story. It
combines the broad performance ladder, targeted objectstore/bulk checks, native
runtime flow simulation, runtime-native TypeScript frame benches, and live
Postgres/Redis service-backed benches.

Artifacts:

| Artifact | Contents |
| --- | --- |
| `benchmark-results/foundation_bench_20260531T223701Z.tsv` | Broad Go/Rust/TypeScript benchmark summary: 221 rows, with `benchmark`, `ns_per_op`, `bytes_per_op`, `allocs_per_op`, and `source`. |
| `benchmark-results/foundation_bench_20260531T223701Z.log` | Raw broad benchmark output, including Vitest `hz`, `mean`, p75/p99/p995/p999, RME, and sample counts. |
| `benchmark-results/test_bench_20260531T224419.log` | Targeted objectstore, bulk manager, and native flow simulation output from `make test-bench`. |
| `benchmark-results/native_bench_20260531T224409.log` | Runtime-native Rust report-only benches, native flow simulation, and runtime-native TypeScript frame benches. |
| `benchmark-results/service_backed_20260531T235252Z.tsv` | Live eventlog/Postgres/Redis service-backed summary after eventlog claim leases: 15 rows, with `unit_per_op` for batch rows. |
| `benchmark-results/service_backed_20260531T235252Z.log` | Raw service-backed benchmark output after Docker-backed race tests, including concurrent multi-drainer eventlog publication coverage. |

The TSV files are the exhaustive machine-readable ledgers. The tables below are
the architectural read of those ledgers: which Foundation lane is being measured,
what cost it represents, and where generated applications should inherit the
practice.

### Runtime ladder

| Benchmark | ns/op | B/op | allocs/op | Meaning |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkBoundFrameClientDispatchTrusted-8` | 10.67 | 0 | 0 | Bound trusted same-process handler, effectively the control-call floor. |
| `BenchmarkRouterDispatchFrameDirect-8` | 10.79 | 0 | 0 | Generic same-process frame router path. |
| `BenchmarkBinaryFrameAppendViewRoundTrip-8` | 19.75 | 0 | 0 | Binary append plus borrowed frame view; the preferred synchronous hot parser. |
| `BenchmarkBinaryFrameAppendRoundTrip-8` | 41.00 | 0 | 0 | Binary append plus owned frame decode. |
| `BenchmarkBinaryFrameCodecRoundTrip-8` | 81.71 | 144 | 2 | Codec-compatible binary owned path. |
| `BenchmarkGeneratedProtoMarshalAppendRoundTrip-8` | 371.5 | 152 | 6 | Generated protobuf contract path. |
| `BenchmarkClientDispatchFrameOverBufconn-8` | 20585 | 10991 | 181 | Binary frame through local gRPC client/server machinery. |
| `BenchmarkDispatchFrameOverBufconn-8` | 25372 | 10916 | 178 | Server-side binary frame over local gRPC. |
| `BenchmarkDispatchOverBufconn-8` | 30175 | 12624 | 213 | JSON envelope compatibility lane over local gRPC. |

Foundation's rule is visible: internal hot work should stay on same-process frame
dispatch or borrowed binary views. Protobuf and gRPC remain correct for typed
cross-process boundaries. JSON is a compatibility adapter, not a hot product
lane. Industry-wise, zero-allocation 10-80 ns in-process control paths are
strong local numbers; tens of microseconds for a gRPC boundary is normal because
it pays client/server call machinery, codecs, metadata, and framing even through
`bufconn`.

### App, safety, and orchestration lanes

| Benchmark | ns/op | B/op | allocs/op | Meaning |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkAppLane_DirectFrame_DomainCall-8` | 32.02 | 32 | 1 | App-shaped same-process domain call. |
| `BenchmarkAppLane_HTTPIngress_JSONToDispatchRequest-8` | 5399 | 9219 | 71 | HTTP JSON ingress and dispatch request shaping. |
| `BenchmarkAppLane_Auth_ValidateToken-8` | 3288 | 2104 | 27 | JWT validation only. |
| `BenchmarkAppLane_HTTPMiddleware_AuthSecurityRBAC-8` | 7203 | 11284 | 81 | Auth, security headers, validation, and RBAC middleware. |
| `BenchmarkAppLane_Cache_GetHit_JSONValue-8` | 54.03 | 0 | 0 | In-memory cache hit. |
| `BenchmarkAppLane_Retry_NoRetrySuccess-8` | 3.697 | 0 | 0 | No-retry success fast path. |
| `BenchmarkAppLane_CircuitBreaker_ClosedSuccess-8` | 68.35 | 0 | 0 | Healthy dependency circuit-breaker wrapper. |
| `BenchmarkAppLane_Worker_EnqueueWithBackpressureAndDrain-8` | 5500 | 1167 | 26 | Accepted bounded worker enqueue and drain. |
| `BenchmarkAppLane_Worker_RejectFullQueue-8` | 1523 | 738 | 17 | Explicit full-queue rejection. |
| `BenchmarkAppLane_Worker_DropNoProcessor-8` | 1291 | 706 | 14 | Explicit no-processor rejection. |
| `BenchmarkAppLane_Retry_CanceledWait-8` | 95.83 | 96 | 2 | Canceled retry wait path. |

These are the costs generated apps inherit when they use Foundation's safety
boundaries correctly. HTTP/auth/RBAC and worker enqueue are thousands of
nanoseconds because they do real safety work; the cache, retry, circuit breaker,
and direct domain path remain nanosecond-scale. The practice is to put expensive
safety boundaries at ingress and async edges, then keep already-authorized
same-process work on direct/binary lanes.

### Scale and fanout lanes

| Benchmark | ns/op | B/op | allocs/op | Meaning |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkScale_MemoryDBTenantCount100K-8` | 5173 | 0 | 0 | 100K tenant-scoped count. |
| `BenchmarkScale_MemoryDBTenantListFiltered100K-8` | 21586 | 33400 | 105 | 100K tenant/filter list with defensive response copies. |
| `BenchmarkScale1M_MemoryDBTenantCount-8` | 5079 | 0 | 0 | 1M tenant-scoped count remains indexed. |
| `BenchmarkScale1M_MemoryDBTenantListFiltered-8` | 21598 | 33400 | 105 | 1M tenant/filter list. |
| `BenchmarkScale1M_MemoryDBDenseTenantListFilteredLimit-8` | 16836 | 33400 | 105 | 1M dense-tenant indexed `LIMIT 50` path. |
| `BenchmarkScale_WebSocketBroadcastResolveInto100K-8` | 34464 | 0 | 0 | 100K owned broadcast target materialization. |
| `BenchmarkScale1M_WebSocketBroadcastResolveInto-8` | 489633 | 0 | 0 | 1M owned target materialization. |
| `BenchmarkScale1M_WebSocketBroadcastForEach-8` | 2400135 | 0 | 0 | 1M callback-per-target route. |
| `BenchmarkScale1M_WebSocketBroadcastBatch-8` | 747.9 | 0 | 0 | 1M adaptive borrowed batch route. |
| `BenchmarkScale_EventExactDispatch100KSubscriptions-8` | 405.8 | 0 | 0 | Exact event dispatch at 100K subscriptions. |
| `BenchmarkScale1M_EventExactDispatchSubscriptions-8` | 423.8 | 0 | 0 | Exact event dispatch at 1M subscriptions. |
| `BenchmarkScale_EventWildcardDispatch1KSubscriptions-8` | 544.2 | 64 | 1 | Generic wildcard compatibility fanout. |
| `BenchmarkScale_EventPrefixWildcardDispatch100KSubscriptions-8` | 530.8 | 64 | 1 | Colon-prefix wildcard fanout. |
| `BenchmarkScale_ConfigConvergence10K-8` | 173.8 | 0 | 0 | Runtime config validation/convergence. |

The scale story is data shape, not heroics. Tenant counts are indexed. Dense
tenant lists stop at indexed limits instead of sorting broad state. Exact event
fanout is stable at 100K and 1M subscription cardinality. WebSocket broadcast is
where API choice matters: owning a 1M target slice costs about 0.49 ms, a
per-target callback loop costs about 2.4 ms, while borrowed adaptive batches keep
routing below 1 microsecond before actual socket writes begin.

### Objectstore and bulk range gains

| Benchmark | Before | 2026-05-31 | Gain | Allocation gain | Practice |
| --- | ---: | ---: | ---: | ---: | --- |
| `BenchmarkMemoryStoreGetRange/64KB-8` | 3596 ns/op, 65696 B/op | 213.3 ns/op, 160 B/op | 16.9x faster | 410.6x fewer bytes | Borrow immutable range readers instead of copying the selected range. |
| `BenchmarkMemoryStoreGetRange/1024KB-8` | 36024 ns/op, 1048736 B/op | 210.3 ns/op, 160 B/op | 171.3x faster | 6554.6x fewer bytes | Keep range metadata small and payload bytes shared. |
| `BenchmarkManagerOpenRangeIdentity-8` | 46324 ns/op, 531474 B/op | 12044 ns/op, 5205 B/op | 3.85x faster | 102.1x fewer bytes | Compose bounded range readers instead of rebuilding the complete object. |
| `BenchmarkManagerForEachRangeIdentity-8` | 35882 ns/op, 530938 B/op | 2648 ns/op, 5072 B/op | 13.6x faster | 104.7x fewer bytes | Stream subranges through callbacks and keep offsets checked. |

This is the clearest recent Foundation gain. The old shape treated range access
like "copy a payload and then read it." The new shape treats range access as
"validate checked offsets, borrow immutable slices/readers, and stream only the
requested span." That is the same principle Rust, Go, and TypeScript should all
propagate into scaffolded apps: views for hot synchronous reads, ownership only
when data must outlive the source, and checked arithmetic at every length/offset
boundary.

### Runtime SDK, browser, and native payload lanes

| Benchmark | Result | Meaning |
| --- | ---: | --- |
| `BenchmarkBufferInputBytesView1KB-8` | 3.109 ns/op, 0 B/op, 0 allocs/op | Go runtime buffer borrowed input view. |
| `BenchmarkBufferInputBytesOwned1KB-8` | 143.4 ns/op, 1024 B/op, 1 alloc/op | Owned input copy. |
| `BenchmarkBufferOutputBytesView2KB-8` | 3.214 ns/op, 0 B/op, 0 allocs/op | Go runtime buffer borrowed output view. |
| `BenchmarkBufferOutputBytesOwned2KB-8` | 276.2 ns/op, 2048 B/op, 1 alloc/op | Owned output copy. |
| `BenchmarkBufferReadFrameInto4KB-8` | 73.68 ns/op, 4 B/op, 1 alloc/op | Read framed data into caller-provided storage. |
| `BenchmarkBufferReadFrameAllocCopy4KB-8` | 693.6 ns/op, 4100 B/op, 2 allocs/op | Allocating framed read. |
| `native output_bytes_view borrowed` | 3.93 ns/op | Rust SDK borrowed output view. |
| `native read_output_bytes_into reused Vec` | 20.06 ns/op | Rust SDK reused output buffer. |
| `native read_output_bytes owned Vec` | 63.34 ns/op | Rust SDK owned output copy. |
| `runtime-native TS decode native dispatch response` | 3,296,719 ops/sec | JS/native response frame decode. |
| `runtime-native TS encode native dispatch frame` | 1,087,686 ops/sec | JS/native request frame encode. |
| `runtime-transport TS decode identity binary frame` | 4,988,681 ops/sec | Browser/Node identity binary frame decode. |

The runtime lesson is consistent across Go, Rust, and TypeScript: borrowed views
are the hot path, caller-provided buffers are the next-best path, owned copies
are the compatibility path. Scaffolded apps should inherit these checks through
runtime docs and scripts: no unbounded frame lengths, no unchecked offset math,
no accidental payload cloning in loops, and explicit caps before JS/Rust/Go
cross-runtime handoff.

### Native descriptor vs full-payload control

| Lane | Payload represented | Mean | p50 | p95 | p99 | Copy model |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| `runtime-native dispatch frame` | 4KB | 517.51 ns | 500 ns | 583 ns | 666 ns | Report-only Rust frame encode/decode/echo. |
| `runtime-native dispatch frame` | 64KB | 6349.09 ns | 6625 ns | 7166 ns | 7625 ns | Linear with payload movement. |
| `runtime-native dispatch frame` | 1MB | 117019.64 ns | 115708 ns | 134000 ns | 169250 ns | Linear full-payload frame cost. |
| `descriptor-control` | 4KB external | 332.50-347.03 ns | ~333 ns | ~375 ns | ~417 ns | 96-byte descriptor, zero hot-payload copy. |
| `descriptor-control` | 64KB external | 326.37-340.00 ns | ~333 ns | ~375 ns | ~417 ns | Constant with represented payload size. |
| `descriptor-control` | 1MB external | 324.36-340.34 ns | ~333 ns | ~375 ns | ~417 ns | Constant with represented payload size. |
| `runtime-buffer-in-place` | 1KB input | 137.13-138.91 ns | 125 ns | 167 ns | 209-250 ns | Fixed buffer input view plus bounded output work. |

Native frame dispatch is a useful desktop/mobile control boundary, but it is not
the highest-performance payload lane. Full-payload native frames copy roughly
five payload-equivalents in the current simulation, so 1MB frames land in the
117-189 microsecond range depending on the exact run. Descriptor control remains
around 325-347 ns because only the control descriptor moves. The Foundation
runtime principle is therefore strict: camera/audio/GPU/market-data style
payloads move through descriptors, arenas, packet rings, shared memory, or fixed
runtime buffers; control frames carry ownership, epoch, schema, and bounds.

### Service-backed live substrate

| Benchmark | ns/op | B/op | allocs/op | Unit | Meaning |
| --- | ---: | ---: | ---: | --- | --- |
| `BenchmarkServiceBackedEventLogPublishPending64-8` | 4910472 | 178327 | 1975 | 64 events/op | Claim 64 durable eventlog rows with `FOR UPDATE SKIP LOCKED`, append them to Redis Streams with one pipeline, and mark them published with one token-checked Postgres batch update. |
| `BenchmarkServiceBackedHermesRebuild512-8` | 3206715 | 1703672 | 19202 | 512 records/op | Rebuild 512 Hermes records from live substrate state. |
| `BenchmarkServiceBackedHermesApplyBatch512-8` | 899371 | 956955 | 4836 | 512 records/op | Apply 512-record Hermes batch. |
| `BenchmarkServiceBackedRedisSetGet-8` | 454364 | 888 | 26 | | Two live Redis round trips. |
| `BenchmarkServiceBackedRedisSet-8` | 223987 | 464 | 13 | | One live Redis `SET`. |
| `BenchmarkServiceBackedRedisGet-8` | 248782 | 408 | 12 | | One live Redis `GET`. |
| `BenchmarkServiceBackedRedisSetGetParallel-8` | 117197 | 1006 | 29 | | Parallel Redis set/get under pool concurrency. |
| `BenchmarkServiceBackedRedisSetManyGetMany64-8` | 766354 | 51521 | 1053 | 64 keys/op | Two 64-key Redis batch phases. |
| `BenchmarkServiceBackedRedisSetGetMany64-8` | 561730 | 49482 | 793 | 64 keys/op | Combined 64-key pipelined cache lane. |
| `BenchmarkServiceBackedRedisRawPipelineSetGet64-8` | 636014 | 31768 | 657 | 64 keys/op | Raw go-redis pipeline baseline. |
| `BenchmarkServiceBackedPostgresUpsert-8` | 286893 | 3063 | 49 | | Full state-store tenant-scoped JSONB upsert. |
| `BenchmarkServiceBackedPostgresUpsertRawJSON-8` | 309598 | 2201 | 40 | | Byte-preserving raw JSONB upsert path. |
| `BenchmarkServiceBackedPostgresUpsertParallel-8` | 76576 | 3085 | 49 | | Parallel independent tenant-scoped upserts. |
| `BenchmarkServiceBackedPostgresSendBatchUpsert64-8` | 3061392 | 73374 | 937 | 64 rows/op | Batched upsert with per-row semantics. |
| `BenchmarkServiceBackedPostgresCopyFrom64-8` | 698505 | 36700 | 378 | 64 rows/op | `COPY` ingest lane for append/import workloads. |

Live eventlog, Redis, and Postgres rows are in the hundreds of microseconds to
low milliseconds locally because they cross Docker, socket, and database
boundaries. That is expected and industry-normal for localhost service-backed
tests. The Foundation rule is not to wish these into nanoseconds; it is to
batch, pipeline, pool, cap acquire waits, keep query budgets explicit, and use
local memory harnesses for fast contract regression before paying
service-backed costs. The eventlog row is the durable fact-lane expression of
that rule: claim pending Postgres bytea envelopes with a lease, pipeline Redis
`XADD`, then batch the token-checked published-state update instead of doing
one full Postgres/Redis/Postgres cycle per event. The claim lease moved the
64-event local service-backed benchmark from 2.97ms to 4.91ms per batch
(roughly 46us/event to 77us/event) while keeping allocation shape essentially
flat. That is an intentional safety trade: multi-drainer duplicate prevention
is now part of the measured live substrate contract.

### Delta from the prior history run

Compared with `benchmark-results/foundation_bench_20260529T130319Z.tsv`, the
largest architectural improvements in the broad ledger were:

| Benchmark | Previous | Current | Gain | Note |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkScale1M_MemoryDBDenseTenantListFilteredLimit-8` | 35861 ns/op | 16836 ns/op | 2.13x | Dense tenant read path benefits from the indexed/limited shape. |
| `can dispatch write via admin fallback` | ~200 ns | ~100 ns | 2.00x | TypeScript capability fallback check got cheaper in this run. |
| `webgpu fake dispatch resident-to-resident 4KB x1` | ~1700 ns | ~1100 ns | 1.55x | CPU-side WebGPU helper path improved/noise-favored. |
| `decode protobuf envelope bytes` | ~1900 ns | ~1600 ns | 1.19x | Runtime-transport TS protobuf decode improved. |
| `packet-ring enqueue/dequeue/complete/release x128` | ~51400 ns | ~43900 ns | 1.17x | Browser packet-ring batch lifecycle improved. |

Several service-backed rows moved slower by 5-20% against the prior Docker run,
while allocations stayed effectively unchanged. Treat those as environment and
service jitter unless they repeat across multiple runs. The important current
signal is that structural allocation shape stayed stable, `COPY` remains much
cheaper than semantic batch upsert per row, raw/pipelined Redis remains the
right multi-key lane, and the local in-process/runtime improvements are orders
of magnitude below networked service boundaries.

## Historical reference run (2026-05-01)

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

## Historical local check (2026-05-05)

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
| `BenchmarkGeneratedProtoMarshalAppendRoundTrip` | 386.9 | 152 | 6 | slower/noisy | Typed network payload lane remains below 1000 ns |
| `BenchmarkRunParallel` | 1712 | 592 | 8 | slower/noisy | Orchestration overhead sits in the low-thousands-of-ns range |
| `BenchmarkDispatchFrameOverBufconn` | 31645 | 10969 | 181 | slower/noisy | Binary gRPC boundary stays much cheaper than external network I/O |
| `BenchmarkDispatchOverBufconn` | 39989 | 12653 | 212 | slower/noisy | JSON compatibility lane remains most expensive |

The important signal is unchanged: same-process and borrowed binary lanes are nanosecond paths, while gRPC/JSON lanes are tens-of-thousands-of-ns boundary paths. Use benchmark deltas here as local evidence only; laptops, battery state, scheduler noise, and dependency versions can move these numbers by double-digit percentages.

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
| `BenchmarkMemoryClientGetHit` | 179.7 | 56 | 4 | Local copied `Get` stays below 1000 ns; allocation is intentional ownership protection. |
| `BenchmarkMemoryClientSetManyGetMany64` | 21319 | 12637 | 516 | Separate set-many/get-many batches still pay per-key/value ownership costs. |
| `BenchmarkMemoryClientSetGetMany64` | 12570 | 9563 | 324 | Combined set/get-many reduces round-trip orchestration but still copies returned values. |
| `BenchmarkMemoryClientPublish1KSubscribers` | 56012 | 47689 | 991 | Exact pub/sub fanout to 1k local subscribers is allocation-heavy; use budgeted fanout and slow-consumer controls. |
| `BenchmarkMemoryClientPSubscribePrefix1K` | 28848 | 111 | 4 | Generic Redis-style pattern fanout scans patterns; use Foundation exact/prefix event routing for hot product fanout. |
| `BenchmarkMemoryClientStreamXAddReadAck` | 1108 | 1015 | 17 | Local stream add/read/ack is now measurable and useful for contract tests, not a replacement for real Redis Streams load tests. |
| `BenchmarkMemoryClientLockUnlock` | 449.4 | 120 | 8 | Token lock/unlock is cheap locally; real Redis lock budgets still need network timeout and fencing-token checks. |

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
| `BenchmarkInMemoryBus_Publish_NoSubscribers` | 307.0 | 0 | 0 | Context-free publish now skips empty metadata map construction and remains allocation-free. |
| `BenchmarkInMemoryBus_Publish_1Subscriber` | 309.1 | 0 | 0 | One exact subscriber adds little over trace/event validation. |
| `BenchmarkInMemoryBus_Publish_10Subscribers` | 340.7 | 0 | 0 | Synchronous local fanout remains below 1000 ns for small exact sets. |

2026-05-21 propagation note: the bulk checksum/copy audit exposed that
`Publish(context.Background(), normalizedEnvelope)` was constructing empty
metadata maps before discovering there was no context metadata to merge.
`metadata.FromContextOK` now lets event publish stay zero-allocation on the
context-free hot path while preserving metadata injection when the context
actually carries Foundation metadata.

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
| `BenchmarkServiceBackedRedisSetManyGetMany64` | 933156 | 51521 | 1053 | Two batch round trips for 64 keys, about 14600 ns/key. |
| `BenchmarkServiceBackedRedisSetGetMany64` | 685292 | 49479 | 793 | One pipelined write/read batch for 64 keys, about 10700 ns/key. |
| `BenchmarkServiceBackedRedisRawPipelineSetGet64` | 619035 | 31768 | 657 | Raw go-redis pipeline baseline, about 9700 ns/key in this Docker run. |
| `BenchmarkServiceBackedPostgresUpsert` | 250843 | 3149 | 51 | Full `StateStore` semantics: JSONB payload, unique identity, timestamps, acquire budget, query budget, pool pressure. |
| `BenchmarkServiceBackedPostgresUpsertRawJSON` | 250273 | 2188 | 40 | Byte-preserving JSON write path for handlers that do not need map mutation; tenant key is stamped in JSONB by SQL. |
| `BenchmarkServiceBackedPostgresUpsertParallel` | 67960 | 3184 | 51 | Pool concurrency amortizes latency for independent tenant-scoped writes. |
| `BenchmarkServiceBackedPostgresSendBatchUpsert64` | 2639219 | 73451 | 938 | Batched upsert is about 41200 ns/row. Keep diagnostics per row when using this lane. |
| `BenchmarkServiceBackedPostgresCopyFrom64` | 630576 | 39487 | 378 | COPY ingest is about 9900 ns/row for append/import workloads. |

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
| `BenchmarkDispatchFrameOverBufconn` | 22751 | 10932 | 178 | gRPC boundary remains tens-of-thousands-of-ns; binary codec saves allocations but not the gRPC stack cost |
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
| `BenchmarkScale_LocalOperationMixLatency` | 10183 ns mean, 24084 ns p99 | 7874 ns mean, 15959 ns p99 | 3794 B / 34 allocs -> 1984 B / 21 allocs | Mixed local DB count + WS user route + cache hit + event publish + config validation stays below 20000 ns p99 locally |

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
| `BenchmarkScale_WebSocketBroadcastResolveInto100K` | 39578 | 0 | 0 | 100k materialization is still below 100000 ns, but copies target IDs |
| `BenchmarkScale_WebSocketBroadcastBatch100K` | 336.2 | 0 | 0 | 100k broadcast routes as adaptive borrowed batches |
| `BenchmarkScale1M_WebSocketBroadcastResolveInto` | 556400 | 0 | 0 | 1M materialization is dominated by copying the target slice |
| `BenchmarkScale1M_WebSocketBroadcastForEach` | 2096710 | 0 | 0 | 1M per-connection callbacks are too expensive for routing alone |
| `BenchmarkScale1M_WebSocketBroadcastBatch` | 753.8 | 0 | 0 | 1M adaptive batches keep routing overhead below 1000 ns in this run |

Interpretation: 1M local scale is not breaking the foundation data structures, but the API and container choice matters. Use `ResolveTargetsInto` only when the caller needs an owned/stable target list. Use adaptive `ForEachTargetBatch` for broadcast fanout so the router hands write queues borrowed chunks instead of copying a huge slice or invoking a million callbacks. Event wildcards should be exact or colon-prefix shaped for product traffic; complex wildcard patterns remain compatibility/observability tools. Returning 50 DB records allocates because public records are defensive copies. Dense tenant reads must stop at indexed `LIMIT`, not sort broad state. Broadcast to 1M live sockets is still a product-level write-pressure problem: it requires bounded per-connection queues, slow-client shedding, and node-level fanout budgets.

### Historical app-lane check

| Benchmark | ns/op | B/op | allocs/op | Role |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkAppLane_DirectFrame_DomainCall` | 36.18 | 32 | 1 | App-shaped direct frame domain call |
| `BenchmarkAppLane_HTTPIngress_JSONToDispatchRequest` | 5329 | 9219 | 71 | HTTP JSON ingress to dispatch request |
| `BenchmarkAppLane_Auth_ValidateToken` | 3415 | 2104 | 27 | JWT validation only |
| `BenchmarkAppLane_HTTPMiddleware_AuthSecurityRBAC` | 7751 | 11284 | 81 | HTTP middleware with auth, headers, validation, RBAC |
| `BenchmarkAppLane_Cache_GetHit_JSONValue` | 53.34 | 0 | 0 | In-memory cache hit |
| `BenchmarkAppLane_Retry_NoRetrySuccess` | 3.231 | 0 | 0 | No-retry success fast path |
| `BenchmarkAppLane_CircuitBreaker_ClosedSuccess` | 61.66 | 0 | 0 | Healthy dependency safety wrapper |
| `BenchmarkAppLane_Worker_EnqueueWithBackpressureAndDrain` | 4499 | 1267 | 24 | Accepted worker enqueue and drain |
| `BenchmarkAppLane_Worker_RejectFullQueue` | 1566 | 734 | 17 | Bounded queue rejection path |
| `BenchmarkAppLane_Worker_DropNoProcessor` | 1316 | 707 | 14 | Missing processor rejection path |
| `BenchmarkAppLane_Retry_CanceledWait` | 94.77 | 96 | 2 | Canceled retry wait path |

These app-lane results explain the practical architecture boundary: the foundation communication core remains far cheaper than real HTTP auth, route building, worker rejection, or domain persistence logic. Optimize product code by keeping hot internal calls on direct/binary lanes, then budgeting auth, DB, worker, and cache costs explicitly.

### Historical foundation hardening pass

After reducing avoidable allocations in JWT bearer parsing, HTTP path parameter extraction, and worker job normalization:

| Benchmark | Before | After | Allocation change | Interpretation |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkAppLane_HTTPIngress_JSONToDispatchRequest` | 5570 ns | 5141 ns | 74 -> 71 | Path extraction no longer uses regex match allocation |
| `BenchmarkAppLane_Auth_ValidateToken` | 3380 ns | 3312 ns | 28 -> 27 | Token parsing avoids split allocation |
| `BenchmarkAppLane_HTTPMiddleware_AuthSecurityRBAC` | 7600 ns | 7455 ns | 83 -> 81 | Auth + middleware path inherits parsing improvement |
| `BenchmarkAppLane_Worker_EnqueueWithBackpressureAndDrain` | 5373 ns | 5294 ns | 26 -> 24 | Metadata-free raw jobs avoid empty map allocation |
| `BenchmarkAppLane_Worker_RejectFullQueue` | 1734 ns | 1647 ns | 19 -> 17 | Bounded rejection path is cheaper |
| `BenchmarkAppLane_Worker_DropNoProcessor` | 1543 ns | 1479 ns | 17 -> 15 | Missing processor rejection path is cheaper |

These are small but useful foundation-wide improvements because every scaffold inherits them. The bigger lesson is unchanged: auth, HTTP shaping, and worker queues are thousands-of-ns safety boundaries. They are appropriate at ingress and async boundaries, but same-process hot domain calls should stay on direct frame or typed call lanes.

### Post-Quantum TLS Check

Local TLS 1.3 handshake benchmark:

| Benchmark | ns/op | B/op | allocs/op | Role |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkTLSHandshake_ClassicalX25519` | 420556 | 72599 | 817 | Classical TLS 1.3 local handshake |
| `BenchmarkTLSHandshake_HybridX25519MLKEM768` | 576051 | 116436 | 838 | Hybrid post-quantum TLS 1.3 local handshake |
| `BenchmarkApplyPostQuantumTLSAuto` | 217.6 | 964 | 3 | Config posture application |

Hybrid post-quantum TLS adds about 155000 ns in this local handshake benchmark. That cost belongs at connection/session establishment or the edge terminator, not inside per-request JWT validation, render loops, domain handlers, or worker hot loops. The foundation posture remains: use standardized hybrid TLS where supported, keep signatures for durable artifacts and compliance workflows, and benchmark before moving post-quantum signatures into any request path.

### Phase 2 Core Utilities (Server-Kit)

| Benchmark | ns/op | B/op | allocs/op | Role |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkMemoryBackend_Get` | 53.71 | 0 | 0 | Ultra-low latency cache read |
| `BenchmarkJob_Normalize` | 4.850 | 0 | 0 | Minimum worker job normalization overhead |
| `BenchmarkCircuitBreaker_Execute_Closed` | 61.24 | 0 | 0 | Safety overhead per healthy call |
| `BenchmarkInMemoryBus_Publish_10Subscribers` | 289.2 | 0 | 0 | Trace-enabled in-process delivery latency |

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
| `runtime-native` dispatch frame | 64KB | 10180 ns | 8040 ns | 12830 ns | 91670 ns | One tail outlier in this run; still report-only |
| `runtime-native` dispatch frame | 1MB | 111990 ns | 114380 ns | 134830 ns | 171920 ns | Copies payload through frame encode/decode and echo unit |
| native TS frame encode | ~1KB envelope | 1,000,386 ops/s | 1000 ns mean | 3500 ns p99 | 4100 ns p99.5 | Browser/JS frame construction cost |
| native TS response decode | ~1KB envelope | 3,248,841 ops/s | 300 ns mean | 400 ns p99 | 900 ns p99.5 | Header validation and payload view |
| runtime-sdk Rust buffer output borrowed view | 2KB | 3.65 ns/op | n/a | n/a | n/a | Hot lane reference: no owned output copy |
| runtime-sdk Rust buffer fast output write | 2KB | 16.33 ns/op | n/a | n/a | n/a | Hot lane reference: trusted copy-only write |
| direct Go frame dispatch | control frame | 17.49-22.07 ns/op | n/a | n/a | n/a | Hot same-process control reference |
| bufconn dispatch | control frame | 20330-24830 ns/op | n/a | n/a | n/a | Local RPC-style boundary reference |

Interpretation: `runtime-native` frame dispatch is viable as a local native control lane when the shell needs device access, platform lifecycle, secure storage, or mobile/desktop packaging. It is not the top of the performance ladder. Direct same-process frame dispatch, `runtime-sdk` fixed-buffer views, FFI, shared memory, and WASM/SAB remain the hot compute lanes when payloads are frequent or latency budgets are below 1000 ns.

2026-05-13 communication-flow simulation:

| Simulated lane | Represented payload | Mean | p50 | p95 | p99 | Modeled copy budget |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| full-payload native frame | 4KB | 659.63 ns | 625 ns | 750 ns | 833 ns | ~20KB/call, 5x payload |
| full-payload native frame | 64KB | 15420 ns | 15040 ns | 19580 ns | 28330 ns | ~320KB/call, 5x payload |
| full-payload native frame | 1MB | 149200 ns | 144540 ns | 174170 ns | 253290 ns | ~5MB/call, 5x payload |
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

Go SIMD note: Go 1.26 exposes experimental `simd/archsimd` behind
`GOEXPERIMENT=simd`. Treat Foundation Go SIMD benchmarks as opt-in architecture
lane checks. They should report scalar Go, Go SIMD, Rust FFI/native, and
WASM/SAB comparisons for the same input contract before a Go SIMD path becomes
eligible for default lane planning.

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

| Benchmark | hz | mean ns | p99 ns | Role |
| --- | ---: | ---: | ---: | --- |
| 4KB slab write/read | 736596.92 | 1400 | 4400 | Control-plane-sized payload movement |
| 64KB slab write/read | 102084.09 | 9800 | 46200 | Medium slab payload movement |
| 1024KB slab write/read | 8422.95 | 118700 | 681500 | Large slab payload movement |
| sustained descriptor-ready ring traffic | 922.79 | 1083700 | 1270200 | Descriptor queue pressure path |

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

### 2026-05-23 WebGPU CPU-Side Helper Run

This pass added a focused `RuntimeWebGpuHost` benchmark for the CPU-side work
around WebGPU dispatch: strict layout planning, arena descriptor packing into a
contiguous upload buffer, and readback writeback into arena descriptors. It does
not measure physical GPU kernel time; browser/device WebGPU measurements must
add adapter acquisition, pipeline warmup, queue submit, dispatch, and readback.

Command:

```bash
cd foundation/runtime-sdk/ts/browser-host
npm run bench -- src/webgpuHost.bench.ts --run --reporter=verbose
```

Tuning changed the dispatch helper to avoid duplicate internal validation after
the layout has already been checked, and `writeGpuOutputToArena` now writes
slices directly to arena descriptors instead of building a transient writes
array.

The follow-up nanosecond pass added caller-owned layout and pack targets plus
descriptor-snapshot arena copy/write helpers. This keeps checked public
semantics available while giving hot paths an explicit reuse lane.

The second follow-up applied the WebGPU host-interaction rule from
`gpu_practices.md`: direct arena uploads now use `GPUQueue.writeBuffer` with
data offsets and sizes against the stable arena typed-array view when all
regions satisfy WebGPU's 4-byte write validation. Unaligned regions fall back to
packed upload. `RuntimeWebGpuHost` also gained a bounded GPU buffer pool keyed
by size and usage, so transient input/readback buffers and explicitly destroyed
resource receipts can be reused without unbounded device allocation churn.

| Benchmark | First tuned mean | Reuse-path mean | Meaning |
| --- | ---: | ---: | --- |
| `gpu layout strict u32 4KB x1` | `100 ns` | `100 ns` | Layout planning is already nanosecond-class for single-region batches. |
| `gpu layout strict u32 into 1KB x128` | not tracked | `1100 ns` | Caller-owned layout storage trims multi-region planning churn. |
| `gpu pack arena descriptors 4KB x1` | `1100 ns` | `1000 ns` | Owned pack still pays output allocation. |
| `gpu pack arena descriptors into 4KB x1` | not tracked | `300 ns` | Caller-owned target avoids pack allocation; about 3.3x faster than owned pack. |
| `gpu writeback arena descriptors 4KB x1` | `300 ns` | `300 ns` | Descriptor-snapshot writeback remains nanosecond-class. |
| `gpu pack arena descriptors 64KB x1` | `8000 ns` | `6800 ns` | Owned medium pack improved modestly from descriptor-snapshot reads. |
| `gpu pack arena descriptors into 64KB x1` | not tracked | `2800 ns` | Caller-owned medium pack is near the arena writeback cost. |
| `gpu writeback arena descriptors 64KB x1` | `2900 ns` | `2800 ns` | Medium writeback is now mostly copy bandwidth. |
| `gpu pack arena descriptors 1024KB x1` | `77800 ns` | `76500 ns` | Owned large pack is still dominated by allocation plus 1MB movement. |
| `gpu pack arena descriptors into 1024KB x1` | not tracked | `42700 ns` | Caller-owned large pack cuts the allocation side, leaving memory movement. |
| `gpu writeback arena descriptors 1024KB x1` | `43200 ns` | `42700 ns` | Large writeback is memory bandwidth shaped. |
| `gpu pack arena descriptors 1KB x128` | `26200 ns` | `22500 ns` | Many-region owned pack improved, but still allocates the packed buffer. |
| `gpu pack arena descriptors into 1KB x128` | not tracked | `15400 ns` | Caller-owned target still reduces many-region pack overhead versus owned pack. |
| `gpu writeback arena descriptors 1KB x128` | `21300 ns` | `21700 ns` | Many-region writeback is mostly per-descriptor state updates and copies; this run is scheduler-noisy. |

Interpretation: the browser GPU CPU-side helper overhead is nanosecond-recorded
and ranges from hundreds of ns for tiny control-sized helpers to tens of
thousands of ns for 1MB or 128-region
batches. That is cheap enough for GPU candidates that already need wide
data-parallel work, but still too expensive for scalar control, auth, routing,
or UI deadlines below 1,000,000 ns. The next GPU benchmark gap is a real
browser/device run that separates pipeline warmup, upload, dispatch, readback,
and arena writeback using the timing fields returned from `dispatchArenaBatch`.

Resident-lane correction: `RuntimeWebGpuHost` now defaults dispatch output to a
GPU-resident resource receipt. Arena materialization is explicit through
`materializeResourceToArena`, and resident resources can feed subsequent GPU
passes through `dispatchResidentBatch` without re-uploading arena bytes.

The policy benchmark uses a fake WebGPU device, so it measures host-side JS
dispatch/resource bookkeeping rather than physical GPU work:

| Benchmark | Mean | Meaning |
| --- | ---: | --- |
| `webgpu fake dispatch gpu-resident 4KB x1` | `1900 ns` | Arena input is uploaded once through direct offset write and output remains GPU-resident. |
| `webgpu fake dispatch materialize-readback 4KB x1` | `3700 ns` | Compatibility mode pays explicit readback/writeback. |
| `webgpu fake dispatch resident-to-resident 4KB x1` | `1100 ns` | No arena upload and no readback; output stays GPU-resident for the next pass. |

Interpretation: resident-to-resident dispatch is about 1.65x faster than
arena-to-resident and about 3.27x faster than materialize-readback in this
host-side benchmark. This is the Foundation GPU target shape: Cap'n Proto and
arena descriptors carry canonical contract metadata, while GPU buffers/textures
remain resident across GPU pass graphs until a CPU-visible boundary explicitly
requests materialization.

`runRuntimeWebGpuPhysicalProbe` is now the browser probe wrapper for physical
adapter runs. It calls `measureRuntimeWebGpuDeviceRoundTrip` and reports
adapter acquisition, device acquisition, pipeline warmup, dispatch,
queue-drain, materialization, and total wall time in nanoseconds. Node on this
machine reports no `navigator.gpu`, so no physical browser/device adapter run
is recorded in this ledger yet.

### 2026-05-24 Native GPU Descriptor Contract Run

This pass added the canonical native GPU descriptor receipt contract:
`runtime-sdk/protocols/system/v1/runtime_native_gpu.capnp`. TypeScript, Go, and
Rust expose typed validators and map public descriptor receipts into the Cap'n
Proto numeric contract. `runtime-native` reuses the `runtime-sdk` TypeScript
contract and adds native command names; raw platform handles remain in
plugin-owned side tables.

Commands:

```bash
cd foundation/runtime-sdk/ts/browser-host
npm run bench -- src/nativeGpu.bench.ts --run --reporter=verbose

cd foundation/runtime-native/ts
npm run bench -- src/nativeTransport.bench.ts --run --reporter=verbose

cd foundation/runtime-sdk/go
go test ./runtimehost -bench=RuntimeNativeGPUDescriptorValidate -benchmem

cd foundation/runtime-native/rust
cargo run --release --bin native_flow_sim
```

| Benchmark | Mean | Notes |
| --- | ---: | --- |
| `validate native GPU descriptor` | `500 ns` mean, `1400 ns` p99 | TypeScript public receipt validation and raw-handle field rejection after removing transient `TextEncoder`, `Object.keys`, and lowercase-string allocation. |
| `plan native GPU lane` | `700 ns` mean, `1500 ns` p99 | TypeScript planner with descriptor validation and platform capability check. |
| `validate native GPU descriptor receipt` | `500 ns` mean, `600 ns` p99 | `runtime-native` TS command-side receipt validation through the shared `runtime-sdk` contract. |
| `BenchmarkRuntimeNativeGPUDescriptorValidate` | `26.28 ns/op`, `0 B/op`, `0 allocs/op` | Go descriptor validation and enum contract path. |
| `native-gpu-descriptor-contract` | `26.17 ns` mean, `41 ns` p50, `42 ns` p99 | Rust validation plus borrowed Cap'n Proto contract mapping in the native flow simulation. |
| `native-gpu-registry-lifecycle` | `992.62 ns` mean, `709 ns` p50, `3750 ns` p99 | Rust private registry register + acquire + release + final release with a bounded side table and no hot payload copy. |
| `native-gpu-plugin-opaque-lifecycle` | `546.45 ns` mean, `542 ns` p50, `625 ns` p99 | Rust private registry register + release for an opaque plugin-owned IOSurface/AHB/CUDA/Vulkan-style handle. |
| `native-gpu-unix-fd-lifecycle` | `8639.15 ns` mean, `8000 ns` p50, `26542 ns` p99 | Unix owned-fd register + release path, including `/dev/null` fd open cost in this portability simulation. |

Allocation evidence: Go reports `0 B/op` and `0 allocs/op` with `-benchmem`.
Rust contract mapping returns borrowed string fields instead of cloning descriptor
text. TypeScript benchmarks do not expose `B/op` or `allocs/op`, so the
validator avoids allocation-prone helpers such as `TextEncoder`,
`Object.keys`, and lowercasing transient strings in the hot path.

Native flow simulation also shows why the descriptor lane matters:

| Flow | 4KB mean | 64KB mean | 1MB mean | p99 range | Modeled hot payload copy |
| --- | ---: | ---: | ---: | ---: | ---: |
| full-payload native frame | `728.49 ns` | `12452.88 ns` | `171911.47 ns` | `833-405416 ns` | `5x payload` |
| descriptor-control frame | `357.20 ns` | `332.24 ns` | `333.21 ns` | `417-459 ns` | `0B` |

Interpretation: the contract itself is not the expensive part. Descriptor
validation is nanosecond-class across Go, Rust, and TypeScript in these local
runs. The real win is architectural: native GPU/device producers pass a
small Cap'n Proto receipt while the platform handle and payload stay resident in
the native plugin/device side table. Browser WebGPU can still copy from that
receipt when required, but the serious native path avoids full payload frame
movement by default.

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

2026-05-17 microarchitecture follow-up: Go runtimehost now exposes explicit
fast input/output setters for trusted pre-cleared buffers, and `ProcessPool` /
`FFIPool` use borrowed output views so pooled-buffer responses copy active bytes
only once. The stdio process transport now reads the returned 4KB control frame
directly into the pooled buffer instead of allocating a temporary frame and
copying it back.

| Benchmark | ns/op | B/op | allocs/op | Interpretation |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkBufferSetInputBytes1KB` | 29.46 | 0 | 0 | Defensive clear + copy input path |
| `BenchmarkBufferSetInputBytesFast1KB` | 16.62 | 0 | 0 | Trusted pre-cleared input path, about 1.8x faster |
| `BenchmarkBufferSetOutputBytes2KB` | 61.41 | 0 | 0 | Defensive clear + copy output path |
| `BenchmarkBufferSetOutputBytesFast2KB` | 30.66 | 0 | 0 | Trusted output path, about 2.0x faster |
| `BenchmarkBufferReadFrameAllocCopy4KB` | 712.6 | 4100 | 2 | Old stdio return shape: allocate frame, then copy |
| `BenchmarkBufferReadFrameInto4KB` | 74.07 | 4 | 1 | New stdio return shape: read directly into pooled buffer |

Browser shared-arena update:

| Benchmark | mean ns | p99 ns | Interpretation |
| --- | ---: | ---: | --- |
| 4KB slab write/read | 1400 | 4400 | Owned read copy path remains around control-plane scale |
| 4KB slab write/read view | 500 | 600 | Borrowed view stays below 1000 ns |
| 4KB slab fast write/read view | 400 | 500 | Fast prevalidated path remains the browser hot lane |
| 64KB slab write/read view | 3000 | 3700 | Medium borrowed payloads stay near memory-copy cost |
| 1024KB slab write/read view | 43200 | 64400 | Large borrowed payloads avoid a second copy |
| descriptor-ready fast batch traffic x128 | 484000 | 665000 | Fast batch queueing remains about 2.26x faster than old x128 batch |
| preallocated write/enqueue/dequeue batch x128 | 61600 | 95500 | Best full arena lane when descriptors are pre-owned |
| packet-ring enqueue/dequeue/complete/release x128 | 43900 | 66200 | Packet-like lifecycle is cheaper than general arena descriptor orchestration |

Rust native buffer update:

| Benchmark | ns/op | Interpretation |
| --- | ---: | --- |
| native read_output_bytes owned Vec | 75.67 | Owned read copy pays allocation and payload copy |
| native read_output_bytes_into reused Vec | 17.71 | Caller-owned `Vec` reuse removes repeated allocation |
| native output_bytes_view borrowed | 3.74 | Borrowed native view remains the fastest runtime lane |
| native write_output_bytes clear+copy | 39.86 | Defensive clear + copy remains allocation-free |
| native write_output_bytes_fast copy only | 16.96 | Fast write is the trusted hot path when stale bytes outside length are irrelevant |
| native process_runtime_buffer_in_place | 132.65 | Native in-place 4KB control-plane processing avoids cloned buffer traffic |

2026-05-09 follow-up: `process_runtime_buffer_in_place` now operates directly on the caller-provided 4KB buffer instead of cloning the entire control plane into a temporary `Vec` and copying it back. The release benchmark reports `native process_runtime_buffer_in_place` at 143.44 ns/op for a 1KB echo unit on this machine. Public Go `ProcessResponse.Output` remains owned; FFI/process pools copy the active output before returning so pooled buffers cannot leak into caller-owned responses.

WebSocket routing local-load results:

| Benchmark | ns/op | B/op | allocs/op | Interpretation |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkRouterRegisterLocalOnly` | 534.9 | 228 | 4 | Local connection registration stays below 1000 ns |
| `BenchmarkRouterResolveTargetsUserLocal` | 18758 | 59760 | 12 | Resolves 1024 local user targets without per-connection copy allocation |
| `BenchmarkRouterForEachLocal1024` | 37044 | 98304 | 1024 | Public copy-safe iterator intentionally allocates per connection |

2026-05-17 microarchitecture follow-up: `ForEachLocalValue` adds a value-copy
iterator over the router's contiguous local order for read-only hot scans. The
existing pointer iterator remains for compatibility and still returns isolated
copies.

| Benchmark | ns/op | B/op | allocs/op | Interpretation |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkRouterForEachLocal1024` | 38738 | 98304 | 1024 | Pointer-copy compatibility iterator; each callback pointer escapes |
| `BenchmarkRouterForEachLocalValue1024` | 17573 | 0 | 0 | Value-copy hot iterator; about 2.2x faster with zero allocation |

The WebSocket routing improvement is behavioral-neutral: public read helpers still return copies, but `ResolveTargets` now resolves local user/broadcast targets under the router read lock and appends connection IDs directly. That keeps tenant/session safety semantics while removing avoidable per-connection object copies from realtime fanout.

### 2026-05-17 Forced Microarchitecture Refinement Pass

TLA-style refinement note:

1. Visible state preserved: runtime `ProcessResponse`, arena queue progress,
   transport allow/deny decisions, MemoryDB query results, and WebSocket
   connection snapshots.
2. Hidden state changed: fewer temporary frame buffers, fewer escaped
   connection copies, direct arena descriptor-ID drains, reused Rust output
   vectors, tighter capability scans, and no retained read-filter copies.
3. Invariants preserved: `FrameSizeBound`, `OutputAfterInput`,
   `OwnedDecodeLifetime`, `QueryBounded`, `ConnectionOwned`, and
   `FallbackRefinement`.
4. Liveness/bounds preserved: existing runtime exchange timeout, queue drain,
   DB context checks, and router callback-stop behavior are unchanged.
5. Test mapping: Go runtimehost, Go database, Go/TS transport, Rust native, and
   TS arena tests cover the refined paths before benchmarks are accepted.

Forced comparison summary:

| Lane | Baseline metric | Refined metric | Meaning |
| --- | ---: | ---: | --- |
| Go runtime input write | `29.46 ns/op`, `0 B/op`, `0 allocs/op` | `16.62 ns/op`, `0 B/op`, `0 allocs/op` | Fast setter skips a redundant full-region clear after buffer reset; about 1.8x faster with the same declared-length contract. |
| Go runtime output write | `61.41 ns/op`, `0 B/op`, `0 allocs/op` | `30.66 ns/op`, `0 B/op`, `0 allocs/op` | Trusted output setter preserves length semantics while avoiding defensive tail clearing; about 2.0x faster. |
| Go stdio runtime return | `712.6 ns/op`, `4100 B/op`, `2 allocs/op` | `74.07 ns/op`, `4 B/op`, `1 alloc/op` | Reads the 4KB control frame directly into the pooled buffer instead of allocating a temporary frame and copying it back. |
| WebSocket local scan | `38738 ns/op`, `98304 B/op`, `1024 allocs/op` | `17573 ns/op`, `0 B/op`, `0 allocs/op` | Value-copy iterator keeps copy safety while preventing one pointer escape per connection. |
| Event publish tracing | `294.7 ns/op`, `48 B/op`, `1 alloc/op` | `253.5 ns/op`, `0 B/op`, `0 allocs/op` | Terminal-state extraction no longer splits the event type while recording traces; publish semantics and trace visibility are unchanged. |
| Rust native output read | `75.67 ns/op` owned `Vec` | `17.71 ns/op` reused `Vec` | Caller-owned reuse avoids repeated allocation while still returning owned bytes. |
| TS arena drain x8 | `514500 ns mean`, `650900 ns p99` | `497400 ns mean`, `611500 ns p99` | Descriptor-ID-only drain avoids queue-entry object construction when only IDs are needed. |
| TS arena drain x32 | `466900 ns mean`, `586900 ns p99` | `428700 ns mean`, `537800 ns p99` | ID-only drain improves larger batch locality and object churn. |
| TS arena drain x128 | `448300 ns mean`, `574100 ns p99` | `409300 ns mean`, `511300 ns p99` | ID-only drain is about 9% faster on the 128-descriptor batch path. |
| TS transport admin fallback | `~5.39M hz` previous local run | `6.64M hz` | Capability check avoids transient array/callback paths; throughput improved while exact allow/deny semantics are unchanged. |
| MemoryDB 100K filtered list | `29708 ns/op`, `33400 B/op`, `105 allocs/op` | `20598 ns/op`, `33400 B/op`, `105 allocs/op` | Read filters are no longer defensively copied and same-type scalar comparisons avoid formatting; response-copy cost remains intentional. |

Expanded one-command coverage:

`tooling/scripts/performance_check.sh` now includes the previously separate
scale benchmarks, in-memory cache/circuit/compress/events/metrics/redis/retry
and worker benchmarks, TLS/PQ handshake benchmarks, and service-backed
Redis/Postgres benchmarks when `SERVICE_BACKED_DATABASE_URL` and
`SERVICE_BACKED_REDIS_URL` are set. The 2026-05-17 run skipped only the
service-backed Redis/Postgres lane because those URLs were not set.

Final expanded-script highlights:

| Area | Benchmark | Metric | Meaning |
| --- | --- | ---: | --- |
| Same-process dispatch | `BenchmarkBoundFrameClientDispatchTrusted` | `10.89 ns/op`, `0 B/op`, `0 allocs/op` | Lower bound for trusted in-process frame dispatch. |
| gRPC boundary | `BenchmarkDispatchFrameOverBufconn` | `21645 ns/op`, `10933 B/op`, `179 allocs/op` | Binary frame saves work versus JSON but still pays gRPC stack cost. |
| HTTP ingress | `BenchmarkAppLane_HTTPIngress_JSONToDispatchRequest` | `5329 ns/op`, `9219 B/op`, `71 allocs/op` | JSON compatibility ingress is thousands-of-ns and allocation-heavy relative to frame lanes. |
| Auth middleware | `BenchmarkAppLane_HTTPMiddleware_AuthSecurityRBAC` | `7751 ns/op`, `11284 B/op`, `81 allocs/op` | Full auth/security/RBAC middleware is a request-boundary cost, not a runtime hot-loop cost. |
| Scale DB count | `BenchmarkScale_MemoryDBTenantCount100K` | `4899 ns/op`, `0 B/op`, `0 allocs/op` | Tenant count stays index-shaped. |
| Scale DB list | `BenchmarkScale_MemoryDBTenantListFiltered100K` | `20598 ns/op`, `33400 B/op`, `105 allocs/op` | Filtered list now pays mostly intentional defensive record copies. |
| Scale DB dense 1M | `BenchmarkScale1M_MemoryDBDenseTenantListFilteredLimit` | `18380 ns/op`, `33400 B/op`, `105 allocs/op` | Dense tenant list stops at indexed `LIMIT`; no broad sort/materialization. |
| WebSocket broadcast copy | `BenchmarkScale1M_WebSocketBroadcastResolveInto` | `509151 ns/op`, `0 B/op`, `0 allocs/op` | Materializing 1M IDs is dominated by slice header copy. |
| WebSocket broadcast batch | `BenchmarkScale1M_WebSocketBroadcastBatch` | `772.1 ns/op`, `0 B/op`, `0 allocs/op` | Borrowed batch routing keeps broadcast routing effectively constant by batch count. |
| Event exact fanout | `BenchmarkScale_EventExactDispatch100KSubscriptions` | `357.5 ns/op`, `0 B/op`, `0 allocs/op` | Exact fanout remains allocation-free after trace terminal-state fix. |
| Event wildcard fanout | `BenchmarkScale_EventPrefixWildcardDispatch100KSubscriptions` | `472.9 ns/op`, `64 B/op`, `1 alloc/op` | Prefix wildcard is bucketed; remaining allocation is the matching subscriber slice. |
| Local operation mix | `BenchmarkScale_LocalOperationMixLatency` | `8399 ns/op`, `p99 12750 ns/op`, `1986 B/op`, `21 allocs/op` | Mixed local DB count, WS route, cache hit, event publish, and config validation stays below 13000 ns p99 locally. |
| Cache hit | `BenchmarkMemoryBackend_Get` | `53.71 ns/op`, `0 B/op`, `0 allocs/op` | In-memory cache hit is not a bottleneck. |
| Cache pattern delete | `BenchmarkMemoryBackend_DeletePattern` | `3705 ns/op`, `0 B/op`, `0 allocs/op` | Pattern deletion is scan-shaped but still allocation-free in this fixture. |
| Circuit breaker | `BenchmarkCircuitBreaker_Execute_Closed` | `61.24 ns/op`, `0 B/op`, `0 allocs/op` | Closed-circuit guard is negligible next to network or DB work. |
| Compression | `BenchmarkCompressLargeBatch/Brotli-Q4` | `6247026 ns/op`, `16246706 B/op`, `25 allocs/op` | Brotli is a batch/offline lane, not a realtime hot path. |
| Compression | `BenchmarkCompressLargeBatch/Zstd-Fastest` | `186545 ns/op`, `1048576 B/op`, `1 alloc/op` | Zstd-fastest is the practical large-payload realtime candidate. |
| Event bus | `BenchmarkInMemoryBus_Publish_1Subscriber` | `253.5 ns/op`, `0 B/op`, `0 allocs/op` | Trace-enabled exact publish is now allocation-free. |
| Event envelope JSON | `BenchmarkEnvelope_ToJSON` | `3345 ns/op`, `2681 B/op`, `49 allocs/op` | JSON envelope conversion is compatibility, not hot internal dispatch. |
| Event envelope binary | `BenchmarkEnvelope_ToBinary` | `2437 ns/op`, `2208 B/op`, `20 allocs/op` | Binary envelope is materially cheaper than JSON but still owned. |
| Metrics counter | `BenchmarkRegistryCounterPrecomputedKey` | `21.04 ns/op`, `0 B/op`, `0 allocs/op` | Hot metrics should use precomputed keys. |
| Metrics snapshot | `BenchmarkRegistrySnapshotPrometheus1024` | `171318 ns/op`, `230080 B/op`, `19 allocs/op` | Prometheus export is scrape-path work; do not put it in hot request loops. |
| Redis memory batch | `BenchmarkMemoryClientSetGetMany64` | `12570 ns/op`, `9563 B/op`, `324 allocs/op` | Current in-memory batch API still allocates per-key/value ownership. |
| Redis memory pubsub | `BenchmarkMemoryClientPublish1KSubscribers` | `56012 ns/op`, `47689 B/op`, `991 allocs/op` | Per-subscriber fanout remains allocation-heavy and should stay behind budgets. |
| Retry success | `BenchmarkPolicy_Do_Success` | `3.554 ns/op`, `0 B/op`, `0 allocs/op` | No-retry success path is effectively free. |
| Retry with retry | `BenchmarkPolicy_Do_Retry` | `298.6 ns/op`, `264 B/op`, `4 allocs/op` | Retry path pays timer/error bookkeeping and must stay bounded. |
| Worker enqueue | `BenchmarkEngine_Enqueue_InMemory` | `2311 ns/op`, `940 B/op`, `19 allocs/op` | Worker enqueue remains a thousands-of-ns boundary with explicit ownership copies. |
| TLS classical | `BenchmarkTLSHandshake_ClassicalX25519` | `420556 ns/op`, `72599 B/op`, `817 allocs/op` | Local TLS handshakes are hundreds-of-thousands-of-ns; connection reuse matters. |
| TLS hybrid PQ | `BenchmarkTLSHandshake_HybridX25519MLKEM768` | `576051 ns/op`, `116436 B/op`, `838 allocs/op` | Hybrid KEM is about 1.37x slower in this run; use it deliberately at ingress boundaries. |
| Runtime buffer read | `BenchmarkBufferInputBytesView1KB` | `3.060 ns/op`, `0 B/op`, `0 allocs/op` | Borrowed views are the correct internal runtime read lane. |
| Runtime stdio frame | `BenchmarkBufferReadFrameInto4KB` | `74.07 ns/op`, `4 B/op`, `1 alloc/op` | Direct frame read into pooled control buffer removes the temporary 4KB allocation. |
| Transport route index | `BenchmarkRouteIndexResolve1024` | `8.053 ns/op`, `0 B/op`, `0 allocs/op` | Generated/indexed route tables are the hot path for large route sets. |
| Transport fallback | `BenchmarkCanDispatchWriteViaAdminFallback` | `65.34 ns/op`, `0 B/op`, `0 allocs/op` | Admin fallback is allocation-free but still slower than exact capability match. |

Current database guard metrics after this pass:

| Benchmark | ns/op | B/op | allocs/op | Meaning |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkMemoryDBCountRecordsTenantScoped` | 12528 | 0 | 0 | Count remains index-shaped and allocation-free. |
| `BenchmarkMemoryDBListRecordsTenantScopedFiltered` | 13713 | 25304 | 69 | Small filtered list pays only typed response-copy cost. |
| `BenchmarkMemoryDBUpsertTenantScopedParallel` | 3360 | 2030 | 12 | Parallel upsert still pays record/key/map ownership costs. |
| `BenchmarkQueryAllFakeRows100` | 3376 | 4512 | 210 | Retained typed slice path; use streaming for broad reads. |
| `BenchmarkExecCommandMemoryDB` | 32.37 | 24 | 1 | Command executor overhead is tiny relative to DB work. |
| `BenchmarkExecRowsAffectedFake` | 32.69 | 24 | 1 | Rows-affected wrapper is a small typed-result cost. |
| `BenchmarkQueryEachFakeRows100` | 2731 | 2472 | 202 | Streaming helper avoids retained result slice. |

### 2026-05-25 Hermes hotplane substrate baseline

This first pass captured the local substrate that Hermes would refine: current
`MemoryDB` read/index behavior, websocket borrowed-batch routing, exact event
dispatch, worker enqueue, and bounded parallel orchestration. The result
supports the Hermes API split proposed in `docs/hermes_hotplane.md`: internal
borrowed/descriptor reads for local hotplane consumers, and copied public reads
at service boundaries.

Environment:

- Date: 2026-05-25
- OS/Arch: `darwin/arm64`
- CPU: Apple M1 Pro
- Command:

```bash
cd foundation/server-kit/go
go test -run=^$ -bench='Benchmark(MemoryDB|Scale_|Scale1M_|ExecCommandMemoryDB|Engine_Enqueue|Job_Normalize|RunParallel)' -benchmem ./database ./appbench ./worker ./chain
```

| Area | Benchmark | ns/op | B/op | allocs/op | Interpretation |
| --- | --- | ---: | ---: | ---: | --- |
| MemoryDB small count | `BenchmarkMemoryDBCountRecordsTenantScoped` | 12561 | 0 | 0 | Small tenant count is index-shaped and allocation-free. |
| MemoryDB small list | `BenchmarkMemoryDBListRecordsTenantScopedFiltered` | 13300 | 25304 | 69 | Copy-safe public list pays defensive record-copy cost. |
| MemoryDB parallel upsert | `BenchmarkMemoryDBUpsertTenantScopedParallel` | 3248 | 1990 | 12 | Upsert still owns record/key/map state; Hermes should consume committed batches, not act as a write authority. |
| Executor command | `BenchmarkExecCommandMemoryDB` | 31.71 | 24 | 1 | Executor helper overhead is tiny compared with real storage and projection work. |
| Scale DB count 100K | `BenchmarkScale_MemoryDBTenantCount100K` | 4731 | 0 | 0 | Tenant count remains local-index work. |
| Scale DB list 100K | `BenchmarkScale_MemoryDBTenantListFiltered100K` | 20860 | 33400 | 105 | Limit/filter shape is good; copied public output dominates allocations. |
| Scale DB count 1M | `BenchmarkScale1M_MemoryDBTenantCount` | 4794 | 0 | 0 | Count is stable at 1M records because it is index-shaped. |
| Scale DB list 1M | `BenchmarkScale1M_MemoryDBTenantListFiltered` | 30891 | 33400 | 105 | 1M filtered list still stays bounded, but public copies remain visible. |
| Scale DB dense 1M | `BenchmarkScale1M_MemoryDBDenseTenantListFilteredLimit` | 22913 | 33400 | 105 | Dense tenant list stops at indexed `LIMIT`; no broad sort/materialization. |
| WebSocket route 100K | `BenchmarkScale_WebSocketBroadcastResolveInto100K` | 25670 | 0 | 0 | Materializing 100K IDs is a copy cost but allocation-free. |
| WebSocket batch 1K | `BenchmarkScale_WebSocketBroadcastBatch1K` | 29.08 | 0 | 0 | Borrowed batch routing is the model for Hermes internal consumers. |
| WebSocket batch 100K | `BenchmarkScale_WebSocketBroadcastBatch100K` | 313.5 | 0 | 0 | Batch routing scales by batch count, not target count. |
| WebSocket batch 1M | `BenchmarkScale1M_WebSocketBroadcastBatch` | 740.3 | 0 | 0 | Borrowed batches keep 1M route resolution below 1000 ns locally. |
| Event exact fanout | `BenchmarkScale_EventExactDispatch100KSubscriptions` | 351.6 | 0 | 0 | Exact event dispatch remains suitable as the local projection notification shape. |
| Event exact fanout 1M | `BenchmarkScale1M_EventExactDispatchSubscriptions` | 354.6 | 0 | 0 | Exact dispatch remains stable at 1M subscriptions. |
| Event prefix wildcard | `BenchmarkScale_EventPrefixWildcardDispatch100KSubscriptions` | 441.8 | 64 | 1 | Prefix buckets are acceptable; complex wildcard scans remain off hot product paths. |
| Local operation mix | `BenchmarkScale_LocalOperationMixLatency` | 7688 | 1984 | 21 | Mixed local DB count, websocket route, cache hit, event publish, and config validation reports p99 `17084 ns`. |
| Worker enqueue | `BenchmarkEngine_Enqueue_InMemory` | 2404 | 957 | 19 | Worker enqueue is a bounded ownership boundary, not a nanosecond hot read. |
| Worker raw payload enqueue | `BenchmarkEngine_Enqueue_RawPayload` | 2324 | 939 | 19 | Raw payload path is similar; River/Postgres path remains the production durability lane. |
| Job normalize | `BenchmarkJob_Normalize` | 4.849 | 0 | 0 | Job metadata normalization is not a bottleneck. |
| Chain orchestration | `BenchmarkRunParallel` | 1173 | 576 | 7 | Parallel orchestration is useful for independent I/O but not for local hot reads. |
| Chain orchestration into | `BenchmarkRunParallelInto` | 1084 | 448 | 6 | Caller-owned result storage reduces allocation modestly. |

Hermes implication:

1. Current `MemoryDB` indexes are already shaped well for scoped reads.
2. The next improvement is API shape: borrowed internal views and caller-owned
   result buffers for websocket/realtime/runtime consumers.
3. Public APIs should keep copy safety, so Hermes benchmarks must report
   borrowed/internal and copied/public lanes separately.
4. Projection apply must be batched and idempotent; per-command direct writes
   into Hermes before Postgres commit are explicitly out of scope.
5. Epoch publication and borrowed batch routing should use the same
   no-allocation discipline as websocket batch routing and event exact dispatch.

### 2026-05-26 Hermes mandatory runtime-store baseline

The `server-kit/go/hermes` implementation now adds bounded projection specs,
tenant-scoped partitions, segmented snapshot indexes, idempotent source-event
apply, tombstones, atomic epoch publication, `database.StateStore` rebuild,
typed record batch ingestion, generated `foundation.v1.RecordMutationBatch`
envelope ingestion, binary payload batch ingestion, a `worker.Processor` bridge,
Redis Stream source/tailer abstractions, and a mandatory
`hermes.ProjectedRuntimeStore` wrapper for scaffolded `database.RuntimeStore`
reads. Public reads return copied `DomainRecord` values; internal reads use
callback-lifetime borrowed `RecordView` values. JSON is not a Hermes transport
lane.

Command:

```bash
cd foundation/server-kit/go
go test -run=^$ -bench='BenchmarkHermes' -benchmem ./hermes
```

| Benchmark | ns/op | B/op | allocs/op | Interpretation |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkHermesGetRecordCopied` | 333.9 | 368 | 3 | Single copied hot record read is sub-microsecond; allocation is defensive ownership. |
| `BenchmarkHermesForEachViewLimit50` | 7469 | 0 | 0 | Borrowed internal filtered reads remain allocation-free after segmented snapshot publication. |
| `BenchmarkHermesCountIndexed` | 143.2 | 0 | 0 | Exact single-index count reads from projection cardinality instead of scanning records. |
| `BenchmarkHermesListRecordsCopiedLimit50` | 21426 | 33400 | 105 | Copy-safe public list still pays the known defensive-copy shape. |
| `BenchmarkHermesApplyEventUpsert` | 18362 | 15817 | 44 | Single-event RCU publication is slower than direct mutable-map apply; projectors should batch. |
| `BenchmarkHermesApplyBatch64` | 212200 | 146845 | 1148 | Event batch apply amortizes index publication to about 3.32 us per record. |
| `BenchmarkHermesApplyRecords64` | 29757 | 29809 | 358 | Typed record batch ingestion avoids per-event construction and is the preferred projector path after decode. |
| `BenchmarkHermesApplyRecordPayloads64` | 249655 | 199750 | 1243 | Compatibility binary payload ingestion with per-payload decoder calls. |
| `BenchmarkHermesApplyRecordPayloadEvents64` | 247463 | 195413 | 1242 | Generated batch decoder hook builds ready-to-apply events directly; larger wins require schema-specific typed/columnar record construction. |
| `BenchmarkHermesProjectedRuntimeStoreHotGet` | 429.2 | 368 | 3 | Mandatory scaffold wrapper caches registered projection scopes while preserving copied-record ownership. |
| `BenchmarkHermesProjectedRuntimeStoreWarmCount` | 297.2 | 48 | 1 | Warm StateStore counts use cached projection names and Hermes indexes. |
| `BenchmarkHermesDriftCheckMerkle` | 17448326 | 12619680 | 24939 | Bounded 10K-record production safety check; Hermes side uses borrowed views and witnesses are emitted only for sampled records. |

Implementation implication:

1. Hermes improves the internal read lane immediately: borrowed filtered reads
   avoid the `33 KB / 105 allocs` public copy shape.
2. Public safety remains intentionally expensive enough to be visible in
   benchmarks.
3. Exact count now exploits declared indexes; multi-filter counts still scan the
   smallest candidate set until intersection counters are justified.
4. Apply performance is now shape-dependent: single-event updates pay for RCU
   publication, while typed record batches are already below 500 ns per record.
5. Segmented snapshot indexes remove the reader/writer lock boundary. A bounded
   atomic publish gate prevents readers from observing partial record/index
   publication.
6. Generated typed decoders now have a batch event hook:
   `ApplyRecordPayloadEvents`. It avoids per-payload decoder callbacks and lets
   app-generated Cap'n Proto/protobuf decoders preserve operation/source/version
   metadata directly. The measured improvement is modest with the synthetic map
   decoder; bigger wins require generated decoders to avoid generic maps or feed
   `ApplyRecords` when the mutation set is pure upsert.
7. Scaffolded apps now wrap `database.RuntimeStore` with
   `hermes.ProjectedRuntimeStore` by default. Oversized scopes fall back to
   Postgres instead of serving partial hot state, and health/resilience checks
   expose degraded projection scopes.
8. Drift checks are deliberately outside the hot read path. The 10K-record
   Merkle run improved from about 56 ms and 1.62M allocs to about 17 ms and
   25K allocs by hashing borrowed Hermes views and avoiding per-record hex
   strings. It is suitable for scheduled parity checks and promotion gates, not
   per-request validation.
9. The service-backed Redis/Postgres run caught a real Redis stream edge case:
   empty `XREADGROUP` reads must omit `BLOCK`; `BLOCK 0` waits forever in Redis
   even though the memory client returned immediately.
10. Hermes Redis stream sources now drain pending entries for the same consumer
   before reading `>` messages. This preserves apply-before-ack safety after an
   ack failure or restart with the same consumer identity.

### 2026-05-29 Hermes bulk-load and byte-estimator refinement

Follow-up review of the Hermes hotplane showed that full rebuild was paying
event-path costs that belong to durable mutation replay, not trusted snapshot
replacement. The implementation now exposes `Store.BulkLoad` and routes
`Rebuild` through that snapshot path. `BulkLoad` still normalizes records,
validates projection scope, enforces record/byte bounds, builds indexes, and
publishes a new epoch atomically, but it skips per-event source de-duplication,
tombstone checks, delete semantics, and synthetic rebuild event bookkeeping.

The record byte estimator was also changed from `fmt.Sprintf("%v", value)` per
field to typed approximate sizing. `Stats.ApproxBytes` remains a guardrail, not
an exact heap meter; the important property is that every apply path enforces a
bounded byte budget without formatting arbitrary values in the hot loop.

Commands:

```bash
cd foundation/server-kit/go
go test -run='^$' -bench='BenchmarkHermes' -benchmem ./hermes
cd ../..
SERVICE_BACKED_BENCHTIME=1s tests/service_backed_foundation_test.sh
```

Artifacts:

- `benchmark-results/hermes_bench_20260529T0141_after_bulkload.log`
- `benchmark-results/service_backed_20260529T014205Z.log`
- `benchmark-results/service_backed_20260529T014205Z.tsv`

| Benchmark | ns/op | B/op | allocs/op | Per record | Interpretation |
| --- | ---: | ---: | ---: | ---: | --- |
| `BenchmarkHermesApplyBatch64` | 222475 | 153467 | 993 | 3476 ns | Durable event path for mixed operations, source IDs, deletes, and idempotency. |
| `BenchmarkHermesApplyRecords64` | 28482 | 29646 | 351 | 445 ns | Preferred incremental projector path when records are already materialized pure upserts. |
| `BenchmarkHermesBulkLoad512` | 857575 | 850629 | 4805 | 1675 ns | Trusted snapshot replacement path; faster and simpler than synthetic event rebuild. |
| `BenchmarkServiceBackedHermesRebuild512` | 2776989 | 1711877 | 19202 | 5424 ns | Live Postgres snapshot plus Hermes bulk-load. Still a control-plane repair path because source reads dominate. |
| `BenchmarkServiceBackedHermesApplyBatch512` | 928888 | 956837 | 4835 | 1814 ns | Live in-memory hotplane apply after mutation events have already reached the process. |

Practice update:

1. Use `ApplyRecords` for changelog/projector batches that are already decoded
   into `database.DomainRecord` and contain only upserts.
2. Use `BulkLoad` for trusted initial seeding, rebuild, and repair snapshots.
3. Use `ApplyBatch` when the batch carries durable event semantics: deletes,
   source/correlation IDs, idempotency, or mixed operations.
4. Do not use `Rebuild` as a routine refresh loop if a bounded changelog can
   feed `ApplyRecords`; full rebuild is a parity/control-plane tool.

Runtime-transport Go results:

| Benchmark | ns/op | B/op | allocs/op | Interpretation |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkCreateEnvelopeJSON` | 304.5 | 96 | 2 | Creates correlation/request/idempotency metadata with crypto randomness |
| `BenchmarkResolveRouteLinear16` | 33.03 | 0 | 0 | Small generated route tables are cheap even with linear lookup |
| `BenchmarkResolveRouteLinear1024` | 454.0 | 0 | 0 | Linear route lookup scales with route count; use `RouteIndex` for hot route tables |
| `BenchmarkRouteIndexResolve1024` | 8.053 | 0 | 0 | Indexed route lookup is the hot generated route-table lane |
| `BenchmarkCanDispatchExactCapability` | 4.849 | 0 | 0 | Capability guard is effectively free on exact match |
| `BenchmarkCanDispatchWriteViaAdminFallback` | 65.34 | 0 | 0 | Admin fallback is allocation-free after the tightened scan |
| `BenchmarkSchemaRegistryNegotiate` | 31.73 | 0 | 0 | Schema negotiation is allocation-free for small accepted-version sets |

Runtime-transport TypeScript binary-envelope results:

| Benchmark | mean ns | p99 ns | Interpretation |
| --- | ---: | ---: | --- |
| encode JSON envelope to protobuf bytes | 7100 | 14400 | JSON payload materialization dominates binary envelope encode |
| decode JSON envelope from protobuf bytes | 2900 | 5600 | JSON payload decode is still thousands-of-ns |
| encode protobuf envelope bytes | 3900 | 9400 | Typed/binary payloads avoid JSON payload stringify |
| decode protobuf envelope bytes | 1700 | 2200 | Typed/binary decode is about 1.7x faster than JSON decode |
| encode JSON compatibility envelope | 1400 | 2400 | Compatibility JSON string path is fast for already-object payloads, but less typed |
| decode identity binary frame | 200 | 300 | Identity frame detection is effectively a header check |

Runtime-transport TypeScript routing results:

| Benchmark | mean ns | p99 ns | Interpretation |
| --- | ---: | ---: | --- |
| parse event type | 200 | 300 | Event contract validation is below 1000 ns |
| create JSON envelope | 700 | 800 | Browser envelope creation is cheap when IDs are provided |
| resolve route by event type | 100 | 100 | Precomputed route map lookup is effectively free |
| resolve route by path | 200 | 200 | Method normalization + path map lookup remains below 1000 ns |
| can dispatch exact capability | <100 | <100 | Exact capability fast path is extremely cheap |
| can dispatch write via admin fallback | 100 | 200 | Admin fallback remains below 1000 ns after removing transient arrays |

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

## 2026-05-17 Server-Kit Table Refresh

This section refreshes the older 2026-05-05 "Latest Local Check" server-kit
table without rewriting its historical values. The old table remains useful as
a baseline, but current update runs must append the new evidence here or in a
new dated section after running the relevant benchmark lane.

Command:

```bash
cd foundation/server-kit/go
go test -bench='Benchmark(DispatchOverBufconn|DispatchFrameOverBufconn|ClientDispatchFrameOverBufconn|RouterDispatchFrameDirect|DirectFrameClientDispatch|BoundFrameClientDispatch|BoundFrameClientDispatchTrusted|BinaryFrameCodecRoundTrip|BinaryFrameAppendRoundTrip|BinaryFrameAppendViewRoundTrip|GeneratedProtoMarshalAppendRoundTrip)$|BenchmarkRunParallel$' -benchmem -run='^$' ./grpcsvc ./chain
```

Current comparison against the 2026-05-05 table:

| Benchmark | 2026-05-05 | 2026-05-17 | Allocation delta | Status | Meaning |
| --- | ---: | ---: | ---: | --- | --- |
| `BenchmarkRouterDispatchFrameDirect` | `18.59 ns/op`, `0 B/op`, `0 allocs/op` | `14.44 ns/op`, `0 B/op`, `0 allocs/op` | unchanged | improved | Same-process router dispatch remains zero-allocation and is about 22% faster in this run. |
| `BenchmarkDirectFrameClientDispatch` | `25.68 ns/op`, `0 B/op`, `0 allocs/op` | `22.35 ns/op`, `0 B/op`, `0 allocs/op` | unchanged | improved | Validation facade remains zero-allocation; direct hot paths should use it instead of gRPC when process-local. |
| `BenchmarkBoundFrameClientDispatch` | not tracked | `11.97 ns/op`, `0 B/op`, `0 allocs/op` | new row | best safe direct lane | Binding the route once removes map lookup from the hot call while preserving event-type validation. |
| `BenchmarkBoundFrameClientDispatchTrusted` | not tracked | `10.78 ns/op`, `0 B/op`, `0 allocs/op` | new row | lower bound | Trusted bound dispatch is the minimum handler-call lane; use only after the caller has already validated the frame boundary. |
| `BenchmarkBinaryFrameAppendViewRoundTrip` | `22.70 ns/op`, `0 B/op`, `0 allocs/op` | `19.55 ns/op`, `0 B/op`, `0 allocs/op` | unchanged | improved | Borrowed frame views are still the right parser shape for synchronous routing and validation. |
| `BenchmarkBinaryFrameAppendRoundTrip` | `62.25 ns/op`, `34 B/op`, `3 allocs/op` | `50.34 ns/op`, `8 B/op`, `1 alloc/op` | `-26 B/op`, `-2 allocs/op` | improved | Append marshal plus bounded control-string interning reduced owned binary decode cost. |
| `BenchmarkBinaryFrameCodecRoundTrip` | `113.4 ns/op`, `178 B/op`, `5 allocs/op` | `100.0 ns/op`, `152 B/op`, `3 allocs/op` | `-26 B/op`, `-2 allocs/op` | improved | Codec-compatible owned path improved, but borrowed views remain the preferred hot lane. |
| `BenchmarkGeneratedProtoMarshalAppendRoundTrip` | `386.9 ns/op`, `152 B/op`, `6 allocs/op` | `392.7 ns/op`, `152 B/op`, `6 allocs/op` | unchanged | noisy/slight regression | Generated protobuf is stable but owned; no action without a targeted protobuf decode change and parity tests. |
| `BenchmarkRunParallel` | `1712 ns/op`, `592 B/op`, `8 allocs/op` | `1207 ns/op`, `640 B/op`, `11 allocs/op` | `+48 B/op`, `+3 allocs/op` | mixed | Faster wall time, but allocation count is higher than the older table. Current perf guard allows `<=12` allocs; optimize only with a new API shape such as caller-owned result storage. |
| `BenchmarkDispatchFrameOverBufconn` | `31645 ns/op`, `10969 B/op`, `181 allocs/op` | `21482 ns/op`, `10937 B/op`, `179 allocs/op` | `-32 B/op`, `-2 allocs/op` | improved | Binary gRPC boundary is about 32% faster, but still a tens-of-thousands-of-ns boundary compared with direct frame dispatch. |
| `BenchmarkClientDispatchFrameOverBufconn` | not tracked | `21691 ns/op`, `11013 B/op`, `181 allocs/op` | new row | boundary reference | Cached client call options do not remove the gRPC stack cost; use direct clients for same-process hot paths. |
| `BenchmarkDispatchOverBufconn` | `39989 ns/op`, `12653 B/op`, `212 allocs/op` | `26831 ns/op`, `12697 B/op`, `213 allocs/op` | `+44 B/op`, `+1 alloc/op` | mixed/noisy | JSON gRPC is faster in wall time but still the highest-allocation compatibility lane. Do not optimize product hot paths around it. |

Regression interpretation:

1. No zero-allocation same-process lane regressed. `RouterDispatchFrameDirect`,
   `DirectFrameClientDispatch`, `BoundFrameClientDispatch`,
   `BoundFrameClientDispatchTrusted`, and `BinaryFrameAppendViewRoundTrip` all
   remain allocation-free.
2. Owned binary decode improved materially. The learning is the same as the
   runtimehost and arena passes: append into caller-owned buffers, borrow for
   synchronous inspection, and only own the bytes/strings that must outlive the
   source frame.
3. `GeneratedProtoMarshalAppendRoundTrip` is effectively unchanged. This is a
   schema-owned compatibility lane; improving it requires protobuf-specific
   work, not generic loop rewrites.
4. `RunParallel` is faster but allocates more than the old table. That is an
   orchestration boundary with goroutines, `context.WithCancel`, and a returned
   result slice. Do not hide allocation by pooling returned results unless the
   API changes to make caller ownership explicit.
5. `DispatchOverBufconn` improved in time but still allocates slightly more
   than the old table. This remains acceptable as a JSON/gRPC compatibility
   boundary, not as a same-process hot path.

Recent implementation techniques that produced the improvements:

1. Separate safe public APIs from trusted hot APIs. Examples:
   `SetInputBytes` keeps defensive clearing; `SetInputBytesFast` is used only
   after reset/preclear. `DispatchFrame` validates; `DispatchFrameTrusted` is
   only for already-bound callers.
2. Prefer borrowed views for synchronous inspection. Examples:
   `UnmarshalFrameView`, runtimehost `InputBytesView`/`OutputBytesView`, and TS
   arena descriptor-ID drains.
3. Use caller-owned storage for repeated work. Examples:
   `AppendMarshalFrame`, `readFrameInto`, Rust `read_output_bytes_into`, and
   WebSocket `ForEachLocalValue`.
4. Avoid transient arrays/callback helpers in hot authorization/routing paths.
   The Go/TS capability fallback changes use explicit loops so the semantic
   fallback remains allocation-free.
5. Treat docs as benchmark ledgers, not prose rewrites. Every future update to
   this file should identify the code path touched, the benchmark command, the
   before/after metrics, and the semantic invariant that stayed true.

Future update rule:

1. Add or select the benchmark before changing code.
2. Run the targeted benchmark before and after the change.
3. Run `tooling/scripts/performance_check.sh` before closing the pass.
4. Append a dated note with metric meaning, regression checks, and the learned
   technique.
5. If a zero-allocation lane gains an allocation, fix it or document the
   behavior reason and add a guard test.
6. If an optimized lane bypasses a safer lane, map it to the safer lane's
   visible contract using the TLA refinement notes: same input contract, same
   output semantics, same bounds, same controlled errors.

## 2026-05-17 Server-Kit Lane Refinement Follow-Up

After the table refresh above, the four highlighted lanes were checked against
the recent implementation techniques from the runtimehost, arena, transport,
database, event, and WebSocket passes:

1. Avoid repeated map lookup when a route has a hot bound handler.
2. Decode into caller-owned output structs instead of replacing the whole
   result.
3. Reuse already-owned low-cardinality/control strings when the next frame
   carries the same bytes.
4. Keep borrowed payload/view semantics explicit and covered by tests.
5. Add perf guard tests when a zero-allocation lane is established.

Applied changes:

1. `Router.RegisterFrame` now records a hot frame-handler slot, and
   `Router.DispatchFrame` / gRPC frame dispatch check it before the handler
   map. This preserves the same missing-handler behavior and only changes
   hidden lookup state.
2. `binaryFrameCodec.Unmarshal` now decodes into the caller-provided `Frame`
   and reuses an existing owned `CorrelationID` string when the incoming bytes
   are identical. If the bytes differ, it still allocates a fresh owned string.
3. Perf-tag tests now guard the binary append roundtrip at zero allocations and
   the codec-compatible roundtrip at no more than two allocations.

Command:

```bash
cd foundation/server-kit/go
go test -bench='Benchmark(DispatchOverBufconn|DispatchFrameOverBufconn|ClientDispatchFrameOverBufconn|RouterDispatchFrameDirect|DirectFrameClientDispatch|BoundFrameClientDispatch|BoundFrameClientDispatchTrusted|BinaryFrameCodecRoundTrip|BinaryFrameAppendRoundTrip|BinaryFrameAppendViewRoundTrip|GeneratedProtoMarshalAppendRoundTrip)$|BenchmarkRunParallel$' -benchmem -run='^$' ./grpcsvc ./chain
```

Follow-up deltas versus the first 2026-05-17 refresh:

| Benchmark | First 2026-05-17 refresh | After lane refinement | Allocation delta | Interpretation |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkRouterDispatchFrameDirect` | `14.44 ns/op`, `0 B/op`, `0 allocs/op` | `11.02 ns/op`, `0 B/op`, `0 allocs/op` | unchanged | Hot frame-handler slot removes the repeated map lookup for the common route. |
| `BenchmarkDirectFrameClientDispatch` | `22.35 ns/op`, `0 B/op`, `0 allocs/op` | `18.84 ns/op`, `0 B/op`, `0 allocs/op` | unchanged | Direct client inherits the router hot-handler lookup. |
| `BenchmarkBoundFrameClientDispatch` | `11.97 ns/op`, `0 B/op`, `0 allocs/op` | `12.15 ns/op`, `0 B/op`, `0 allocs/op` | unchanged | Bound safe dispatch is effectively flat/noisy; it was already near the handler-call floor. |
| `BenchmarkBoundFrameClientDispatchTrusted` | `10.78 ns/op`, `0 B/op`, `0 allocs/op` | `10.97 ns/op`, `0 B/op`, `0 allocs/op` | unchanged | Trusted bound dispatch is unchanged, as expected. |
| `BenchmarkBinaryFrameAppendRoundTrip` | `50.34 ns/op`, `8 B/op`, `1 alloc/op` | `41.82 ns/op`, `0 B/op`, `0 allocs/op` | `-8 B/op`, `-1 alloc/op` | Caller-owned decode plus stable correlation reuse makes the append roundtrip zero-allocation. |
| `BenchmarkBinaryFrameCodecRoundTrip` | `100.0 ns/op`, `152 B/op`, `3 allocs/op` | `85.97 ns/op`, `144 B/op`, `2 allocs/op` | `-8 B/op`, `-1 alloc/op` | Codec path still allocates its marshaled frame, but no longer reallocates stable correlation on decode. |
| `BenchmarkBinaryFrameAppendViewRoundTrip` | `19.55 ns/op`, `0 B/op`, `0 allocs/op` | `19.98 ns/op`, `0 B/op`, `0 allocs/op` | unchanged | Borrowed view path was already the floor for synchronous inspection. |
| `BenchmarkDispatchFrameOverBufconn` | `21482 ns/op`, `10937 B/op`, `179 allocs/op` | `21528 ns/op`, `10926 B/op`, `179 allocs/op` | `-11 B/op`, unchanged allocs | gRPC/server stack dominates wall time; handler lookup changes are mostly hidden under gRPC overhead. |
| `BenchmarkClientDispatchFrameOverBufconn` | `21691 ns/op`, `11013 B/op`, `181 allocs/op` | `21606 ns/op`, `10991 B/op`, `181 allocs/op` | `-22 B/op`, unchanged allocs | Client wrapper follows the same gRPC boundary pattern: tiny allocation-byte win, noisy wall time. |
| `BenchmarkDispatchOverBufconn` | `26831 ns/op`, `12697 B/op`, `213 allocs/op` | `26764 ns/op`, `12645 B/op`, `213 allocs/op` | `-52 B/op`, unchanged allocs | JSON compatibility path is mostly unaffected; recent binary-frame techniques do not apply to JSON map materialization. |
| `BenchmarkGeneratedProtoMarshalAppendRoundTrip` | `392.7 ns/op`, `152 B/op`, `6 allocs/op` | `398.0 ns/op`, `151 B/op`, `6 allocs/op` | effectively unchanged | Generated protobuf remains stable/noisy; future work here must be protobuf-specific. |
| `BenchmarkRunParallel` | `1207 ns/op`, `640 B/op`, `11 allocs/op` | `1227 ns/op`, `640 B/op`, `11 allocs/op` | unchanged | Parallel chain is unchanged; improving it requires an explicit caller-owned result API or different orchestration contract. |

Regression checks:

1. Zero-allocation direct lanes stayed zero-allocation.
2. Owned binary append roundtrip moved from one allocation to zero.
3. gRPC binary frame paths gained a small allocated-byte improvement, but not a
   reliable allocation-count or wall-time win. Treat gRPC as a boundary lane,
   not the target for same-process hot dispatch.
4. JSON gRPC, generated protobuf, and `RunParallel` did not benefit from the
   binary-frame techniques. Their next improvements require separate benchmark
   hypotheses: JSON payload shaping, protobuf decode strategy, or a new
   caller-owned chain result API.

Additional validation:

```bash
cd foundation/server-kit/go
go test ./grpcsvc
go test -tags=perf ./grpcsvc
cd foundation
tooling/scripts/performance_check.sh
```

## 2026-05-18 Open Lane Extension

This section extends the recent optimization work to the bounded open lanes
identified after the 2026-05-17 pass. These are not vague backlog items: each
lane has either an implementation, a benchmark-only proof, or an explicit
report-only status.

### RunParallel Caller-Owned Results

Applied implementation:

1. `chain.RunParallelInto` lets callers provide reusable result storage.
2. `chain.RunParallel` keeps the existing allocating API and behavior.
3. The goroutine body moved to a helper function so both APIs avoid per-loop
   closure allocations.
4. Perf-tag tests now guard `RunParallel` at `<=8 allocs/run` and
   `RunParallelInto` at `<=7 allocs/run`.
5. `chain.HasCriticalFailureOrdered` provides an indexed hot-path check for
   direct `RunParallel`/`RunParallelInto` results without building a name
   lookup. `HasCriticalFailure` remains the compatibility helper for filtered,
   merged, or reordered results.

Command:

```bash
cd foundation/server-kit/go
GOCACHE=/tmp/ovasabi-foundation-go-build go test -bench='BenchmarkRunParallel' -benchmem -run='^$' ./chain
```

| Benchmark | Before | After | Meaning |
| --- | ---: | ---: | --- |
| `BenchmarkRunParallel` | `1227 ns/op`, `640 B/op`, `11 allocs/op` | `1172 ns/op`, `576 B/op`, `7 allocs/op` | Compatibility API improves from helper extraction; returned result slice is still owned by caller. |
| `BenchmarkRunParallelInto` | not available | `1108 ns/op`, `448 B/op`, `6 allocs/op` | Caller-owned result storage avoids the result-slice allocation and is the preferred hot orchestration API. |
| `BenchmarkHasCriticalFailure` | name lookup only | `~36 ns/op`, `0 B/op`, `0 allocs/op` | Compatibility helper is still cheap, but it pays lookup work that ordered chain results do not need. |
| `BenchmarkHasCriticalFailureOrdered` | not available | `~1.27 ns/op`, `0 B/op`, `0 allocs/op` | Indexed critical-failure detection matches direct chain result order and is the preferred hot-path check. |

Refinement note: visible result order, critical-failure cancellation, nil-run
error behavior, and nil-context fallback are unchanged. Only result storage and
goroutine/helper/check shape changed. The remaining `RunParallelInto`
allocations come from per-call goroutine fanout, `sync.WaitGroup`,
`context.WithCancel`, and goroutine argument escape. Removing those cleanly
requires a new reusable runner/lane API that owns worker state across calls; the
current function API cannot become allocation-free without changing its
execution model.

### Generated Protobuf Decode Strategy

Applied implementation and benchmark proof:

1. The existing roundtrip benchmark remains the compatibility baseline.
2. A split reset+unmarshal benchmark isolates protobuf decode cost.
3. A merge-into-reused-message benchmark measures the protobuf-specific local
   win.
4. `protoapi.Binding` now exposes an explicit
   `ProtobufDecodeReuseCompleteMessages` opt-in.
5. The typed registry and typed frame adapter wire that opt-in into
   caller-owned request pooling. Default decode behavior remains
   reset+unmarshal.

Command:

```bash
cd foundation/server-kit/go
GOCACHE=/tmp/ovasabi-foundation-go-build go test -bench='BenchmarkGeneratedProto' -benchmem -run='^$' ./grpcsvc
GOCACHE=/tmp/ovasabi-foundation-go-build go test -bench='BenchmarkDecodeRequestBytesIntoCompleteReuse$' -benchmem -run='^$' ./protoapi
GOCACHE=/tmp/ovasabi-foundation-go-build go test -bench='BenchmarkTypedFrameAdapterDispatch(NoMetadata|Reuse)?$' -benchmem -run='^$' ./bootstrap
```

| Benchmark | Metric | Meaning |
| --- | ---: | --- |
| `BenchmarkGeneratedProtoMarshalAppendRoundTrip` | `397.1 ns/op`, `152 B/op`, `6 allocs/op` | Full generated protobuf compatibility roundtrip is stable. |
| `BenchmarkGeneratedProtoUnmarshalReset` | `242.4 ns/op`, `152 B/op`, `6 allocs/op` | Reset+unmarshal pays nested message/string ownership on every decode. |
| `BenchmarkGeneratedProtoUnmarshalMergeReuse` | `167.7 ns/op`, `56 B/op`, `5 allocs/op` | Raw merge into caller-owned nested storage is faster, but only valid for explicitly reused complete-message decode lanes. |
| `BenchmarkDecodeRequestBytesIntoCompleteReuse` | `251.1 ns/op`, `72 B/op`, `8 allocs/op` | Public protoapi helper exposes the valid reuse lane with binding/target checks. |
| `BenchmarkTypedFrameAdapterDispatchNoMetadata` | `586.8 ns/op`, `536 B/op`, `10 allocs/op` | Product frame path with ordinary reset decode and no metadata overlay. |
| `BenchmarkTypedFrameAdapterDispatchReuse` | `535.5 ns/op`, `424 B/op`, `9 allocs/op` | Product frame path with opt-in complete-message request pooling. |
| `BenchmarkTypedFrameAdapterDispatch` | `6282 ns/op`, `3493 B/op`, `78 allocs/op` | Metadata overlay still dominates when frame correlation metadata must be merged into the request message. |

Behavior warning: protobuf `Merge` preserves fields that are absent from later
messages. Do not silently replace ordinary `Reset`+`Unmarshal` with merge reuse
on partial/update messages. A product path may use merge reuse only when the
contract says every decoded frame is complete or when the caller clears fields
that may be absent. The pooled request object is returned after the typed
handler returns, so opt-in handlers must not retain request pointers for async
use.

### Browser Shared-Arena And Packet-Ring

Implementation already exists in `runtime-sdk/ts/browser-host`:

1. `RuntimeSharedArena` owns aligned slabs, descriptor tables, fast batch queue
   reservation, descriptor-ID drains, release/reallocate free-list reuse, and
   columnar batch descriptors.
2. `RuntimePacketRing` owns fixed packet descriptors, burst enqueue/dequeue,
   view/complete/release lifecycle, and high-water/drop counters.
3. `lanePlanner` can select `packet-ring` for packet-like same-host batches
   when available.
4. Packet-ring enqueue now mutates the preallocated timestamp record instead of
   replacing it per packet.
5. Packet-ring hot loops can drain descriptor IDs into caller-owned scratch
   storage with `dequeueIdsBurstInto`.

Reference numbers from the browser-host benchmark suite:

| Path | Mean | p99 | Meaning |
| --- | ---: | ---: | --- |
| 4KB slab write/read | `1400 ns` | `4700 ns` | Owned control-sized slab copy. |
| 4KB slab fast write/read view | `400 ns` | `500 ns` | Best browser-side control-buffer lane when borrowed views are acceptable. |
| 64KB slab write/read | `10100 ns` | `48200 ns` | Medium payload copy; use views/descriptors for hot paths. |
| 64KB slab fast write/read view | `2900 ns` | `3300 ns` | Descriptor/view route avoids copying the read side. |
| 1024KB slab write/read | `121400 ns` | `758500 ns` | Linear payload movement; descriptor routing should avoid hot-payload copies. |
| 1024KB slab fast write/read view | `43300 ns` | `63600 ns` | Large-payload view path removes the read copy but still pays write cost. |
| descriptor-ready id batch x128 | `434500 ns` | `546400 ns` | ID-only control drain avoids queue-entry object construction. |
| packet-ring x128 | `43600 ns` | `65700 ns` | Packet-like lifecycle is much cheaper than general descriptor orchestration for fixed packet flows. |
| packet-ring id x128 | `42600 ns` | `63600 ns` | ID-only packet drain is a small mean and p99 win in this JS run; tail remains runtime-noise sensitive. |

Implementation rule: use shared-arena slabs for owned payload movement, borrowed
views for synchronous inspection, descriptor-ID drains for scheduling, and
packet rings for fixed packet-like streams. Do not compare packet rings to HTTP
or gRPC; compare them to descriptor-ring and arena lifecycle costs.

### Native Runtime Lane

Status: report-only until three stable local baselines exist.

Current reference:

| Lane | Payload | Mean | p99 | Notes |
| --- | ---: | ---: | ---: | --- |
| `runtime-native` dispatch frame | 4KB | `639 ns` | `667 ns` | In-process Rust bridge; Tauri IPC not included. |
| `runtime-native` dispatch frame | 64KB | `10180 ns` | `91670 ns` | Tail noise; report-only. |
| `runtime-native` dispatch frame | 1MB | `112000 ns` | `172000 ns` | Linear in payload size. |
| descriptor control frame | represents 4KB | `381 ns` | `458 ns` | Copies zero hot-payload bytes. |
| descriptor control frame | represents 64KB | `323 ns` | `416 ns` | Constant with represented payload size. |
| descriptor control frame | represents 1MB | `323 ns` | `375 ns` | Desired native control/data split. |

Implementation rule: native control frames may copy small control payloads, but
hot camera/audio/sensor/market payloads should move by descriptor, shared
memory, fixed runtime buffer, SAB, arena slab, or packet ring. Full-payload
native frames are linear in payload size and must not become the default for
device hot streams.

## 2026-05-20 Bulk Transfer And Object Store

This pass added a bounded benchmark lane for the new `server-kit/go/bulk`
primitive and the object store streaming surface. The first draft accidentally
stored every benchmark object under a unique key, which turned the benchmark
into an unbounded memory-retention test. The accepted benchmark shape now reuses
a small key ring for object-store writes and sets up per-iteration bulk state
outside timed sections so memory stays bounded.

The implementation follows the external shape used by large object stores:
bounded parts, explicit per-part receipts/checksums, a completion manifest, and
part-aligned range reads where possible. Go hot paths use caller-sized buffers
or exact-size bounded reads rather than accidental `io.Copy` scratch allocation,
and checksum hex conversion stays stack-backed so manifest completion does not
allocate per decoded part hash.

Two lessons were promoted into `performance_practices.md` and
`coding_practices.md`: bounded copy paths should make scratch-buffer ownership
explicit, and fixed-size checksum/identifier hex work should use stack-backed
`hex.Encode`/`hex.Decode` when the path is hot. The same hex pattern was applied
to metadata correlation suffixes and security redaction hashes so bulk is not a
one-off optimization island.

Command:

```bash
make test-bench
```

Equivalent direct command:

```bash
cd foundation/server-kit/go
go test -run=^$ -bench='Benchmark(MemoryStore|Manager)' -benchmem -benchtime=100000000ns -count=1 ./objectstore ./bulk
```

Current local reference on Apple M1 Pro:

| Benchmark | Result | Allocation shape | Interpretation |
| --- | ---: | ---: | --- |
| `BenchmarkMemoryStorePutStream/64KB` | `6316 ns/op`, `10375 MB/s` | `65753 B/op`, `8 allocs/op` | Exact-size stream read removes the earlier 3x heap growth and brings memory-store stream writes near `PutBytes`. |
| `BenchmarkMemoryStorePutStream/1024KB` | `41486 ns/op`, `25276 MB/s` | `1048835 B/op`, `8 allocs/op` | Stream storage now retains roughly one payload copy for the memory test backend. |
| `BenchmarkMemoryStorePutStream/4096KB` | `135078 ns/op`, `31051 MB/s` | `4194588 B/op`, `8 allocs/op` | Larger stream writes stay bounded to roughly one payload copy. |
| `BenchmarkMemoryStorePutBytes/64KB` | `6546 ns/op`, `10012 MB/s` | `65696 B/op`, `6 allocs/op` | Existing-slice path is the baseline because the caller already materialized the object. |
| `BenchmarkMemoryStorePutBytes/1024KB` | `32459 ns/op`, `32304 MB/s` | `1048772 B/op`, `6 allocs/op` | Existing-slice path remains faster because the caller already materialized the whole object. |
| `BenchmarkMemoryStorePutBytes/4096KB` | `96705 ns/op`, `43372 MB/s` | `4194520 B/op`, `6 allocs/op` | Large in-memory writes show the store copy cost without stream-reader overhead. |
| `BenchmarkMemoryStoreGetRange/64KB` | `4043 ns/op`, `16209 MB/s` | `65696 B/op`, `6 allocs/op` | Small range reads copy only the requested range. |
| `BenchmarkMemoryStoreGetRange/1024KB` | `38441 ns/op`, `27277 MB/s` | `1048737 B/op`, `6 allocs/op` | Memory range reads copy only the requested range. |
| `BenchmarkManagerAcceptPartIdentity/64KB` | `41984 ns/op`, `1561 MB/s` | `68434 B/op`, `27 allocs/op` | The no-event path no longer builds event payload maps when no event bus is configured. |
| `BenchmarkManagerAcceptPartIdentity/1024KB` | `531366 ns/op`, `1973 MB/s` | `1051475 B/op`, `27 allocs/op` | Large identity chunks scale linearly and retain about one payload copy in the memory backend. |
| `BenchmarkManagerAcceptPartIdentity/4096KB` | `2021854 ns/op`, `2074 MB/s` | `4197208 B/op`, `27 allocs/op` | The 4MB lane stays bounded and shows hashing plus store-copy throughput. |
| `BenchmarkManagerAcceptPartWithCacheAndEvents` | `169111 ns/op`, `1550 MB/s` | `281380 B/op`, `156 allocs/op` | Evented/cache operation is explicitly a control-plane lane; the no-event manager path stays cheaper. |
| `BenchmarkManagerAcceptPartDuplicateReplay` | `331 ns/op`, `791940 MB/s` | `48 B/op`, `1 alloc/op` | Duplicate replay is a receipt/control-path check, not a payload-copy lane. |
| `BenchmarkManagerAcceptPartGzipCompressible` | `1546966 ns/op`, `678 MB/s` | `1256036 B/op`, `65 allocs/op` | Gzip at best-speed is the throughput-oriented stored-compression lane. |
| `BenchmarkManagerAcceptPartZstdCompressible` | `1081163 ns/op`, `970 MB/s` | `9505612 B/op`, `144 allocs/op` | Zstd is fastest on this compressible sample but currently carries high encoder allocation overhead. |
| `BenchmarkManagerAcceptPartBrotliCompressible` | `1696120 ns/op`, `618 MB/s` | `389923 B/op`, `47 allocs/op` | Brotli is allocation-light here but slower; reserve it for policy-driven size wins. |
| `BenchmarkManagerAcceptPartAutoCompressible` | `1523000 ns/op`, `688 MB/s` | `2281314 B/op`, `54 allocs/op` | Auto uses exact-size bounded reads and stack-backed checksum hex conversion. |
| `BenchmarkManagerAcceptPartAutoIncompressible` | `758418 ns/op`, `1383 MB/s` | `2099887 B/op`, `25 allocs/op` | Auto avoids codec work on likely incompressible data and pays roughly one decision buffer plus one store copy. |
| `BenchmarkManagerCompleteManifest/128Parts` | `161825 ns/op`, `51838 MB/s represented` | `236669 B/op`, `167 allocs/op` | Stack-backed hex decode/encode removes per-part digest allocations from manifest root construction. |
| `BenchmarkManagerCompleteManifest/1024Parts` | `1178808 ns/op`, `56929 MB/s represented` | `2065479 B/op`, `1074 allocs/op` | Manifest completion is now dominated by receipt sorting and JSON manifest construction. |
| `BenchmarkManagerCompleteManifestSparseMissing` | `204068 ns/op`, `328855 MB/s represented` | `201687 B/op`, `42 allocs/op` | Sparse missing-part detection stays bounded by manifest metadata, not payload bytes. |
| `BenchmarkManagerOpenRangeIdentity` | `48093 ns/op`, `10901 MB/s` | `531299 B/op`, `35 allocs/op` | Materialized range reads copy the requested range and compose chunk readers. |
| `BenchmarkManagerForEachRangeIdentity` | `34111 ns/op`, `15370 MB/s` | `531264 B/op`, `28 allocs/op` | Callback range walking follows the `wsrouting` split technique: avoid the aggregate reader and slice materialization for hot fanout reads. |

Behavior invariants held:

1. No benchmark path routes bulk bytes through generic dispatch or `io.ReadAll`
   on a full logical transfer.
2. Each accepted part remains bounded by its declared part size and memory
   budget; overrun readers fail before extra bytes become accepted payload.
3. Redis/cache/event work remains control-plane metadata only.
4. Identity remains the default. Gzip, Brotli, and Zstd are explicit stored
   artifact policies; `auto` is also explicit because it buffers one bounded
   chunk to decide whether compression is worth storing.

Adapter gaps to measure next:

1. `runtime-transport` adapter cost: `server-kit/go/bulk.Pipeline` now carries
   typed transfer-plan, part receipt, resume-token, status, progress, and
   manifest envelopes separately from byte movement. It also exposes
   `AcceptHTTPPart` for server-mediated streams and signed object-store grants
   through `GrantSignedPart`/`AcceptSignedPart` for direct-to-object-store
   uploads. Same-host producers can bind descriptor readers through
   `AcceptDescriptorPart`, while `DetectPlatformCapabilities` reports
   conservative Linux acceleration hints for future zero-copy/MPTCP/QUIC
   adapters. `Pipeline.PlanLane` ranks descriptor, signed object-store, kernel
   zero-copy, MPTCP, QUIC, and HTTP stream candidates without changing the
   receipt/manifest contract. `BenchmarkPipelinePlanLane` reports about
   `443 ns/op`, `696 B/op`, and `8 allocs/op`; `BenchmarkPipelineHandleStatus`
   reports about `1700-2000 ns/op`, `1816 B/op`, and `26 allocs/op`. Future work
   should reduce envelope allocation without moving bulk bytes into envelopes.
2. Resumable protocol recovery: benchmark idempotent duplicate part acceptance,
   missing-part discovery, offset retry, and manifest completion after process
   restart or worker handoff.
3. Distributed state backend: compare in-memory state, Redis lease/progress
   state, and durable manifest recovery under bounded concurrency. Redis must
   remain ephemeral coordination unless the product explicitly promotes state to
   a durable store.
4. Data-plane adapters: add filesystem/object-store streaming benchmarks that
   do not retain the whole object in memory. Memory-store numbers are a fast
   regression net, not proof of production storage throughput.
5. Kernel/network acceleration: when implemented, benchmark Linux `sendfile`,
   `splice`, `MSG_ZEROCOPY`, `io_uring`, pacing, MPTCP, QUIC, and packet-ring
   lanes as refinements of the same receipt/manifest contract. The benchmark
   result must report fallback behavior and copy budget, not only MB/s.

## 2026-05-26 service-backed substrate pressure

Command:

```bash
make test-service-backed
```

Environment:

- OS/Arch: `darwin/arm64`
- CPU: Apple M1 Pro
- Services: Docker-backed `postgres:18-alpine` and `redis:8-alpine`
- Artifacts:
  - `benchmark-results/service_backed_20260526T152527Z.log`
  - `benchmark-results/service_backed_20260526T152527Z.tsv`

Correctness and pressure tests added:

1. Postgres pool saturation proves bounded acquire timeout and records pool pressure.
2. Redis stream pressure proves pending-window visibility and read/ack latency budgets.
3. Redis slow-subscriber pressure proves publish latency remains bounded when subscribers do not drain.
4. Hermes projection pressure proves Postgres rebuild, Redis stream tailing, hot indexed counts, and drift checks.
5. Mixed workflow pressure measures p95/p99 across Postgres raw writes, Redis batch `SetGetMany`, and Hermes hot-plane apply.

| Benchmark | ns/op | B/op | allocs/op | Unit | Interpretation |
| --- | ---: | ---: | ---: | --- | --- |
| `BenchmarkServiceBackedHermesRebuild512` | `2914560` | `1879425` | `21278` | `512 records/op` | Rebuild from canonical Postgres into Hermes is millisecond-scale and should be treated as repair/recovery/control-plane, not per-request hot path. |
| `BenchmarkServiceBackedHermesApplyBatch512` | `962877` | `1002751` | `6375` | `512 records/op` | In-memory hot-plane batch apply is about 3x faster than rebuild because it avoids Postgres snapshot reads. |
| `BenchmarkServiceBackedRedisSetGet` | `443338` | `888` | `26` | | Single Redis round-trip pair is network/service-bound. |
| `BenchmarkServiceBackedRedisSetGetParallel` | `113453` | `1017` | `29` | | Parallelism hides service latency; pool sizing and timeouts matter. |
| `BenchmarkServiceBackedRedisSetManyGetMany64` | `796525` | `51522` | `1053` | `64 keys/op` | Separate set-many/get-many is slower and more allocation-heavy than combined batch paths. |
| `BenchmarkServiceBackedRedisSetGetMany64` | `693280` | `49482` | `793` | `64 keys/op` | Foundation batch client is the preferred project API for hot multi-key Redis work. |
| `BenchmarkServiceBackedRedisRawPipelineSetGet64` | `702681` | `31768` | `657` | `64 keys/op` | Raw pipeline is close in latency but bypasses Foundation semantics; use only inside server-kit. |
| `BenchmarkServiceBackedPostgresUpsert` | `342599` | `3054` | `49` | | Semantic single writes are service-bound but predictable. |
| `BenchmarkServiceBackedPostgresUpsertRawJSON` | `409281` | `2204` | `40` | | Raw JSON preserves bytes and lowers allocations, but live latency is similar because Postgres dominates. |
| `BenchmarkServiceBackedPostgresUpsertParallel` | `77136` | `3091` | `49` | | Parallel pool use improves throughput substantially when pool budgets are explicit. |
| `BenchmarkServiceBackedPostgresSendBatchUpsert64` | `2811726` | `73373` | `937` | `64 rows/op` | Batched independent statements amortize round trips but still execute per-row upsert logic. |
| `BenchmarkServiceBackedPostgresCopyFrom64` | `601597` | `37899` | `378` | `64 rows/op` | `CopyFromRows` is the correct append/import lane and is much faster than per-row upsert batches. |

Operational interpretation:

1. Hermes is now proved as a bounded hot-plane over live Postgres/Redis, not only an in-memory unit.
2. Postgres remains canonical truth; pool saturation must fail fast with `ErrPoolAcquireTimeout`.
3. Redis remains ephemeral coordination/cache; batch APIs are materially better than sequential calls.
4. Rebuild and drift checks are control-plane operations. Hot reads should use Hermes indexed queries after projection.
5. Mixed p95/p99 service-backed tests are now the next truth layer after local microbenchmarks.
