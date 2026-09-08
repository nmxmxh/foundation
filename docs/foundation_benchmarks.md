# Foundation Benchmarks

Status: active reference
Date: 2026-09-08
Owner: Platform Architecture

This document holds the current benchmark results for the main Foundation
lanes. It states what each lane costs today and what the number proves.

The full history is in `info/benchmarks_archive.md`. That archive holds every
superseded run, every rejected approach, and the reasoning behind each result.
Read the archive when you need to know why a lane has its present shape.

## Purpose

Foundation performance work is not one transport bet. The architecture uses a
ladder of lanes, from cheapest to most general:

1. same-process direct typed dispatch
2. fixed binary frame codecs and borrowed frame views
3. generated protobuf for typed network payloads
4. gRPC for cross-host or polyglot process boundaries
5. JSON envelopes as compatibility adapters only
6. native `ffi` or `shm` for trusted same-host hot units
7. browser worker with WASM and `SharedArrayBuffer`

The benchmarks keep that ladder honest. The fastest lane must not pay network
or JSON costs. The compatibility lane must stay visibly more expensive than the
binary lanes.

Benchmarks do not replace architecture invariants. Hard bounds and correctness
properties are behavior, and they need tests. See `tla_architecture_practices.md`.

## How to run

From the repository root:

```bash
make test-bench-history
make test-bench
FOUNDATION_NATIVE_SKIP_BASELINE=1 tooling/scripts/native_benchmark.sh .
make test-service-backed
make test-load-research
make test-service-backed-load
```

Set `PROFILE=1` to write CPU and heap profiles to
`/tmp/ovasabi-foundation-profiles`.

## What the numbers mean

| Metric | Meaning | Better |
| :--- | :--- | :--- |
| `ns/op` | Average nanoseconds per operation. | Lower |
| `B/op` | Heap bytes allocated per operation. | Lower |
| `allocs/op` | Heap allocation count per operation. | Lower |
| `hz` | Operations per second, from Vitest. | Higher |
| `p50`, `p95`, `p99` | Latency distribution in nanoseconds. | Lower |
| `rme` | Relative margin of error. Large values mean a noisy result. | Lower |

Allocation count usually matters more than byte count for tail latency.
Allocation count drives allocator and garbage-collector pressure.

### Statistical rules

These rules come from `mathematical_practices.md` (control `MATH-01`). Results
that break them are not comparable.

1. All percentiles use the nearest-rank method, rank `⌈p/100 · n⌉`. Do not mix
   nearest-rank and interpolated percentiles in one table.
2. Sample floors: p95 needs `n ≥ 100`. p99 needs `n ≥ 1000`. p999 needs
   `n ≥ 10000`. Below the floor, the tail value is the maximum in disguise.
3. Report variance with any gating tail metric. Compare distributions, not
   single tail points.
4. Validate float reductions against a scalar reference with a tolerance that
   scales with `n`. Never use bit-equality, because float addition is not
   associative.

## Results by lane

### Hermes hot plane

Measured on an Apple M1 Pro, `darwin/arm64`, 10,000 records per scope.

| Operation | Before | After | Result |
| :--- | ---: | ---: | :--- |
| Facet manifest, 3 dimensions | 765,183 ns | 549.6 ns | 1,392x faster |
| Numeric metric summary | 3,252,103 ns | 29.58 ns | 0 B/op, 0 allocs/op |
| Multi-attribute filter, 3 predicates | 150,465 ns | 5,280 ns | 28.5x faster |

Streaming accumulators make aggregates O(1) instead of O(N). Inverted roaring
bitmaps replace the iterative predicate scan. Both trade memory for a bounded
read cost.

### Projection read path

The gateway serves scoped snapshots and a live delta stream.

| Operation | Cost | What it proves |
| :--- | ---: | :--- |
| Full snapshot, 10K records | ~5.5 ms | Cost is building 100K mutations, not the scan. |
| Bounded snapshot, limit 1024 | ~0.49 ms | A positive limit engages early-stop, so the read is O(limit). |
| Incremental snapshot, since watermark | ~5.4 µs | The reconnect path builds only the changed tail. |
| Keyset page, cursor at 5000 | ~0.50 ms | Pagination stays bounded at any depth. |
| Encode one delta frame | ~3.0 µs | The frame is encoded once and the fan-out reuses the bytes. |
| Broadcast to 1 subscriber | ~92 ns | Zero allocation. |
| Broadcast to 1000 subscribers | ~0.27 ms | One slice allocation, with non-blocking sends. |

The bounded read is the load-bearing property. An unbounded scan of the same
scope costs ~200 ms, so a client must never be able to request one.

### Delete lane

Measured with 256 records in one scope.

| Lane | ns/op | B/op | allocs/op | Fan-out frames |
| :--- | ---: | ---: | ---: | ---: |
| Per record | 618,899 | 233,330 | 2,823 | 256 |
| Batched | 400,742 | 238,261 | 2,060 | 1 |

The frame count is the result that matters. The gateway encodes once per scope
per batch instead of once per deletion. Bytes rise by 2 percent, because a
batch must materialize the slice it applies.

`PostgresDB.DeleteRecordsBatch` removes a whole batch in one statement, so a
Postgres-backed deployment also pays one round trip per batch instead of one per
record. A base store without that method falls back to sequential deletes.

The Postgres statement has not been measured yet. Measuring it needs the
service-backed lane, and the number belongs here once it runs.

### Shared-memory transport

Measured on an Apple M1 Pro, release build.

| Exchange body | ns/op |
| :--- | ---: |
| Control buffer, positional read and write | 1,903 |
| Control buffer, mapped in place | 130 |
| Arena slab read, 64 KiB, positional | 4,781 |
| Arena slab read, 64 KiB, mapped | 10 |

The arena rows are the larger finding. A positional slab read issued four
separate four-byte reads to assemble one table entry, and each read allocated.
Reading a columnar batch cost one syscall and one allocation per column.

A mapped read is an offset into memory the host already wrote. It does not
scale with slab size, because it borrows instead of copying.

Map both ends. A mapped writer against a positional reader drifts, and on
darwin a freshly published descriptor reads back as free. That failure looks
like a protocol race and is not one.

### Epoch exchange

| Benchmark | ns/op | B/op | allocs/op |
| :--- | ---: | ---: | ---: |
| `BenchmarkProcessPoolEpochExchange` | ~1.24-1.32 ms | ~1,010 | 15 |

The kernel child's own round trip dominates this cost. The host doorbell adds
only the wake delivery.

Two contracts came out of this lane. First, arm a waited-slot baseline before
the action that triggers the counterpart's publish. If you arm it after, the
counterpart's store lands inside an already-armed baseline and both sides park.
Second, do not allocate a timer per parked wait. Reuse one timer with stop,
drain, and reset.

### Shared render surface

One worker serves several surfaces with one device.

| Surfaces | Devices, unshared | Devices, shared | Dispatch, unshared | Dispatch, shared |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 1 | 1 | 16 ns | 21 ns |
| 3 | 3 | 1 | 40 ns | 37 ns |
| 8 | 8 | 1 | 71 ns | 27 ns |

The device count is exact and structural. Each avoided device is an adapter, a
pipeline set, and driver state that the page requested by accident.

The dispatch trend is the second result. Unshared dispatch grows with surface
count, because every message wakes every listener. Shared dispatch stays flat.

At one surface, sharing is marginally slower. The arrangement is for pages with
several figures, and those pages were the ones paying for several devices.

A shared worker outlives every surface in it, so the release is
reference-counted. Chain the release onto the acquisition. If the last surface
leaves while acquisition is still in flight, dropping the promise leaks a device
that no profiler can show you.

### HTTP ingress

Parsing `{}` into a dispatch request cost 28 allocations and 7,080 bytes.
Materializing the same body into the extension container costs 1 allocation and
48 bytes. Ingress was spending about 20 times the parse it wraps.

The profile named the causes, which reasoning had not:

| Cause | Share |
| :--- | :--- |
| `net/textproto` header canonicalization | 33 percent of allocations |
| Global context object append and clone | 84 percent of bytes |
| Four eager empty metadata containers | 13 percent of allocations |

## Guardrails

1. Same-process hot dispatch must stay allocation-free.
2. Binary frame paths must allocate less than JSON compatibility paths.
3. Borrowed views must not retain data beyond the source frame lifetime.
4. Any new high-volume ingestion path must benchmark batch primitives against
   per-record writes.
5. Any benchmark improvement that changes behavior must land with correctness
   tests. Cover malformed input, cancellation, oversized frames, and diagnostics.
6. Any optimized lane must prove refinement against the lane it replaces. Prove
   the same metadata, the same payload semantics, the same terminal event, and
   the same error class.
7. Hard bounds are behavioral contracts, not benchmark targets. Queue depth,
   acquire timeout, write deadline, retry cap, and frame size need direct tests.
8. Key and shard routing functions must stay allocation-free, because they sit
   on every routed operation. A change to the routing hash needs a parity oracle
   proving that no key changes shard. A routing-hash change is a silent data
   remap, not a performance change.

## Method rules learned from these runs

1. Measure allocation as a slope across two duration windows for parked, wait,
   or daemon loops. Fixed per-call costs are identical between windows and
   cancel out. An absolute ceiling breaks whenever the runtime shifts a fixed
   cost.
2. Profile before you attribute a cost. Three lanes in this document had a cause
   that reasoning had named wrongly.
3. Never benchmark a transport against a trivial unit. The unit's own cost hides
   the transport's.
4. Report enough cardinalities to expose the growth curve. One data point cannot
   separate a constant cost from a linear one.
