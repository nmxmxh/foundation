# Low-Level Performance Lab

Status: recommended for performance-sensitive changes
Owner: Runtime Performance

## Purpose

Performance claims need repeatable evidence. This lab contract defines the
extra artifacts required when Foundation work depends on CPU, memory, syscall,
I/O, WebGPU, native GPU, WASM, Rust, or transport-lane performance.

Do not use these requirements to slow down ordinary product work. Use them when
the change claims a performance win, changes a hot path, or introduces a new
runtime lane.

## Measurement Lanes

| Lane | Minimum evidence |
| --- | --- |
| CPU hot path | `go test -bench`, Criterion, or Vitest bench with allocation counts and repeated runs. |
| Scheduler/concurrency | `runtime/trace`, pprof block/mutex profile, goroutine leak evidence, or bounded load test. |
| Allocator/memory | bytes/op, allocs/op, `alloc_space`/`alloc_objects`, `inuse_space`/`inuse_objects`, RSS/PSS where relevant, and object lifetime explanation. |
| Cache/TLB/branch | CPU-counter capture where available, or explicit fallback when counters are unavailable. |
| Syscall/I/O | syscall count, copy path, sendfile/splice/io_uring decision, and fallback path. |
| Database/Redis | query plan, WAL/rows/bytes, pool acquire timing, Redis command mix, and p95/p99 under load. |
| Algorithmic work | Size-series benchmark plus candidates/rows/bytes/comparisons/copies/round trips appropriate to the operation and an expected complexity class. |
| WASM/FFI/native | host/guest boundary cost, ABI version, pointer/length validation, and scalar fallback parity. |
| GPU/WebGPU | device matrix, dispatch timing, device-loss behavior, layout conformance, and CPU fallback. |

## CPU Counter Taxonomy

Native/runtime claims should collect this taxonomy where the host allows it:

1. cycles, instructions, and IPC
2. cache references, cache misses, L1/L2/L3 misses where available
3. TLB misses, branch instructions, and branch misses
4. allocator churn, bytes/op, allocs/op, heap live bytes, and GC pressure
5. syscall count, context switches, page faults, and kernel time
6. NUMA locality, remote memory, thermal throttling, and CPU frequency state

Intel and AMD captures should use the current vendor toolchain where available:
Linux `perf`, Intel VTune/pmu-tools, AMD uProf/IBS/L3PMC/DFPMC, or platform
equivalents. If counters are unavailable, record that explicitly and keep the
benchmark valid through ordinary timing, allocation, and trace evidence.

## Tool Lanes

Go pprof/trace:

- Use CPU, heap, goroutine, block, and mutex profiles for request/runtime paths.
- Use `runtime/trace` when scheduler, network poller, GC, goroutine lifecycle,
  or blocking behavior is part of the claim.
- Foundation's benchmark runner supports `PROFILE=1`, `TRACE=1`,
  `PROFILE_DIR=...`, and `PERF_COUNTERS=1` for local evidence capture.
- Capture profiles while the measured workload is active. For allocation work,
  retain both cumulative (`alloc_space`, `alloc_objects`) and live
  (`inuse_space`, `inuse_objects`) views and use source-line attribution before
  changing a data structure.
- Treat profiles as complementary evidence: CPU samples can be dominated by
  scheduler/syscall waiting while allocation profiles reveal the application
  work, and a small live heap can coexist with extreme cumulative churn.

## Cumulative-Work Record

Every core profile should record the smallest set of counters that explains
where work scales:

| Operation shape | Required work counters |
| --- | --- |
| Filter/list/projection | candidates inspected, rows selected, rows materialized, sort comparisons where practical |
| Codec/frame/snapshot | input bytes, output bytes, bytes copied/touched, encode/decode allocations |
| Database/Redis | logical operations, statements/commands, round trips, returned rows, WAL/bytes when relevant |
| Worker/queue | jobs accepted, queue hops, batches, retries, rejected work, time queued |
| File/kernel lane | logical bytes, userspace bytes, syscalls, page-cache posture, checksum bytes |
| GPU/native | host/device bytes, dispatches, synchronizations, materializations, fallback count |

Run scale points that can falsify the claimed complexity. A collection change
should normally include at least four increasing sizes and report normalized
work such as candidates per result or bytes copied per input byte. Benchmark
fixtures must not allocate or rebuild inside the timed region unless construction
is explicitly the subject.

Rust Miri/Loom:

- Use Miri for unsafe, FFI, pointer, alignment, endian, aliasing, and buffer
  lifetime changes when the crate can run under Miri's host model.
- Use Loom for Rust concurrency primitives, atomics, queues, and cancellation
  paths where a small state space can expose interleavings.
- Foundation's Rust runtime check exposes opt-in `RUST_RUNTIME_MIRI=1` and
  `RUST_RUNTIME_LOOM=1` lanes.

WebGPU/WGSL:

- Capture adapter/device limits, WGSL layout conformance, dispatch dimensions,
  buffer sizes, device-loss behavior, pipeline warmup, upload/readback cost,
  and fallback lane.
- Shader claims must include scalar or WASM/SAB parity tests.

CUDA/Nsight:

- CUDA-specific lanes should include Nsight Systems for host/device timeline
  questions and Nsight Compute for kernel-level occupancy, memory, scheduler,
  and instruction analysis.
- CUDA Graphs, streams, async copies, pinned memory, shared memory, and tensor
  paths require a capture bundle and fallback/parity evidence.

## Variance Rules

1. A single benchmark run is a smoke test, not proof.
2. Report sample shape, duration/count, allocs/op, and max latency where
   available.
3. If p99 moves more than the expected regression threshold, rerun with higher
   count or longer duration before calling it noise.
4. Investigate fixture allocation, timer placement, GC, scheduler pressure,
   lock contention, thermal state, cold/warm cache, and hidden filesystem or
   network work.

## Staged Load Research

Use `make test-load-research` when the question is scale shape rather than a
single microbenchmark. The default ramp is:

```text
1k -> 10k -> 50k -> 100k -> 250k -> 500k -> 1M
```

This harness measures local cardinality lanes: MemoryDB tenant predicates,
Hermes hotplane count/get/view/apply, exact event dispatch, and WebSocket
broadcast routing before socket writes. It records setup time separately from
steady operation latency because setup memory and warmup pressure are different
questions from hot-path lookup cost.

Interpretation rules:

1. Treat `setup_ns` and `heap_alloc_delta` as capacity-planning evidence.
2. Treat p50/p95/p99/max as steady hot-path evidence only for the named lane.
3. Do not convert local million-cardinality results into a claim about one
   million live network clients. Live-client claims need distributed load
   generators, socket/kernel tuning, and service telemetry.
4. Compare borrowed/batched lanes against owned/materialized lanes. If owned
   materialization dominates, tune API shape before tuning CPU.
5. For Hermes, compare prebuilt `ApplyRecords` against build+apply. Record
   construction in the hot path is a product-shape cost, not a projector cost.

## Service-Backed Staged Saturation

Use `make test-service-backed-load` when a claim crosses live Postgres, Redis,
Hermes, or WebSocket-routing coordination. The default ramp is the same:

```text
1k -> 10k -> 50k -> 100k -> 250k -> 500k -> 1M
```

This is not a "spawn one million goroutines" harness. Foundation's scalable
shape is bounded workers plus batched lanes. The runner therefore treats each
step as a target unit count and chooses bounded worker concurrency from the
CPU-aware scaling config unless `SERVICE_BACKED_LOAD_RESEARCH_MAX_WORKERS`
overrides it.

Interpretation rules:

1. `postgres_send_batch64` describes semantic write throughput when many
   independent mutations must cross the database boundary.
2. `postgres_copy_from1024` describes append/import throughput. Do not use its
   number to justify replacing semantic command writes with COPY.
3. `redis_set_get_many64` describes multi-key cache pressure. Sequential
   per-key Redis paths should be treated as a different and usually worse lane.
4. `redis_xadd_many64` and `redis_stream_drain64` describe projector/event
   relay pressure and lag. Watch p99 batch latency and memory growth together.
5. `hermes_rebuild_postgres_snapshot` is control-plane warmup/repair. It should
   be fast enough for startup or recovery, but live updates should normally use
   `ApplyRecords` or Redis Stream tailing. Rebuild must consume the default
   `StateStore.ForEachRecord` streaming lane rather than materializing a full
   snapshot before projection.
6. `hermes_redis_tailer64` is the live hotplane bridge: Redis coordinates,
   Hermes applies, and ack happens only after apply.
7. `hermes_hot_count` proves repeated scoped reads after a live rebuild. It is
   a read-plane number, not a durable-write number.
8. `mixed_pg_redis_hermes64` is the closest Foundation substrate shape to a
   product workflow: durable batch, cache batch, projection batch.
9. `pipeline_pg_redis_hermes` is the live cooperation lane: Postgres writes
   durable records, Redis relays batched projection envelopes, Hermes tails and
   applies before ack, and hot reads continue while writes are flowing.
10. `wsroute_register_redis` measures live route registration coordination.
11. `wsroute_broadcast_after_register` measures borrowed fanout planning after
    registration; it still excludes actual socket writes and slow-client queues.

Hermes mutation consistency lanes:

- `UPSERT` is a complete hot-record replacement. Use it when the durable state
  snapshot is already materialized or the whole record changed.
- `PATCH` is a partial hot-record merge. It updates only supplied fields,
  preserves unchanged fields, moves affected indexes atomically, and ignores
  patches for records that are missing or already deleted. Use it for status,
  state, archive, bucket, assignment, and other small projection updates.
- `DELETE` is hard invalidation. It removes the hot record and its indexes, then
  records a tombstone so stale replay cannot reappear as a live read.

Archive is normally a `PATCH` to a domain status/state field, not a hard delete.
That keeps the record discoverable through an archived index while removing it
from active views. Hard delete is for erasure, revocation, or retention expiry.

Resource knobs for serious runs:

```bash
SERVICE_BACKED_POSTGRES_TMPFS_SIZE=8g
SERVICE_BACKED_REDIS_MAXMEMORY=4gb
SERVICE_BACKED_POSTGRES_MAX_CONNECTIONS=120
SERVICE_BACKED_POSTGRES_MAX_WAL_SIZE=4GB
SERVICE_BACKED_POSTGRES_MIN_WAL_SIZE=1GB
SERVICE_BACKED_LOAD_RESEARCH_DB_RESERVED_CONNS=24
SERVICE_BACKED_LOAD_RESEARCH_DB_ACQUIRE_TIMEOUT=2s
SERVICE_BACKED_LOAD_RESEARCH_DB_QUERY_TIMEOUT=10s
SERVICE_BACKED_LOAD_RESEARCH_LANES=pipeline_pg_redis_hermes
SERVICE_BACKED_LOAD_RESEARCH_PIPELINE_BATCH=512
SERVICE_BACKED_LOAD_RESEARCH_PIPELINE_TAILER_BATCH=256
SERVICE_BACKED_LOAD_RESEARCH_PIPELINE_MAX_LAG=32768
SERVICE_BACKED_LOAD_RESEARCH_WS_REGISTER_BATCH=256
make test-service-backed-load
```

The runner adapts Postgres scratch space to the largest requested step: 2GB
below 250K, 4GB at 250K, 6GB at 500K, and 8GB at 1M. It also reserves database
headroom by default. With the local service set to 120 max connections, the
load harness budgets 96 application connections and leaves 24 for health checks,
setup, cleanup, and control-plane work. Do not size benchmark pools directly to
the database hard cap unless the experiment is explicitly about starvation.

The runner also adapts WAL headroom. A 1M all-lane write ramp uses a 4GB
`max_wal_size` and 1GB `min_wal_size` by default. If Postgres logs "consider
increasing max_wal_size", the write lane is checkpointing from WAL pressure
instead of time-based cadence; raise WAL headroom before raising DB workers.
Autovacuum cancellation during these runs is a pressure signal on the mutable
state table, not an automatic failure. Keep autovacuum enabled and logged, then
track bloat, dead tuples, vacuum lag, and index churn before changing table or
index shape.

For 10M research, run focused lanes before running `all`. A 10M Hermes hotplane
question is primarily about `MaxRecords`, `MaxBytes`, indexed field count,
tombstone policy, and fallback. A 10M WebSocket question is primarily about
file descriptors, kernel TCP limits, write queue bounds, Redis routing TTLs,
slow-consumer policy, and load-generator distribution. A 10M Postgres question
is primarily about batch semantics, WAL volume, index write amplification,
partitioning, and pool acquire budgets.

Adaptive tuning rule:

```text
single user: keep safety boundaries explicit; avoid unnecessary cold setup
10k: batch cross-service work; keep exact indexes and per-tenant scopes
1M: borrowed/batched fanout, bounded projections, pool acquire budgets
10M: shard by tenant/scope, promote only proven hot scopes into Hermes, and use
     fallback/repair as a first-class operational mode
```

StateStore/Hermes rule:

1. `StateStore.ForEachRecord` is the default record-snapshot contract.
2. `ListRecords` is a convenience wrapper for callers that truly need a slice.
3. Hermes rebuild uses streaming snapshot iteration and swaps the rebuilt
   projection only after the replacement partition is ready.
4. Do not tune 1M rebuilds by relaxing normal hot-path DB budgets. Use explicit
   control-plane rebuild budgets and keep request lanes strict.

WebSocket route-registration tuning rule:

1. Use `Router.Register` for ordinary single connection lifecycle operations.
2. Use `Router.RegisterMany` for reconnect bursts, resume storms, fixture
   hydration, or any path already holding multiple connection descriptors.
3. Keep `SERVICE_BACKED_LOAD_RESEARCH_WS_REGISTER_BATCH=256` as the local
   default until a new service-backed run proves a better value for the host.
   The 2026-06-03 run showed 256 beating 64, 128, and 512; 512 regressed because
   the larger Redis pipeline increased tail cost instead of reducing it.

Postgres/Redis/Hermes pipeline tuning rule:

1. Isolated lane throughput is not the final product claim. Use
   `pipeline_pg_redis_hermes` after isolated lanes to measure cooperation.
2. Tune Postgres workers and pool pressure separately from Redis stream batch
   grouping. More DB workers can reduce throughput and worsen p99 when WAL,
   indexes, or pool scheduling are already saturated.
3. The pipeline default is intentionally not "use every worker." The default
   durable batch is 512 records, the default tailer batch is 256, and the
   default DB writer count is `min(cpu-2, 6)`. On the measured 8-core host,
   that means six DB writers by default because higher DB concurrency made the
   cooperative pipeline worse.
4. Prefer batched Hermes projection envelopes for committed outbox/projector
   relay. One Redis stream message may carry many record mutations, and Hermes
   still preserves apply-before-ack semantics.
5. Track published-vs-applied lag. Redis should not outrun Hermes indefinitely;
   bounded lag keeps the hotplane fresh while preserving durable-write
   throughput.
6. Hot reads should run while writes flow. If Hermes indexed reads regress under
   concurrent tailing, inspect owned materialization, index count, tombstone
   pressure, and projection byte caps before tuning Postgres.

## Do-Not-Optimize Gate

Do not add a faster lane unless all are true:

1. the bottleneck is measured
2. the visible contract is preserved
3. the fallback is documented and tested
4. the new lane has a rollback plan
5. the owning practice doc and controls matrix still match the evidence
6. a cheaper algorithm, data structure, index, batch, or round-trip removal was
   considered before allocation shaving, pooling, SIMD, FFI, or GPU promotion
