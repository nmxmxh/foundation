# Foundation Benchmarks

Status: active reference
Date: 2026-09-22
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

Set `PROFILE=1` to write CPU and heap profiles.
History captures use a dated directory beside the log. Direct runs default to
`/tmp/ovasabi-foundation-profiles`. Set `PROFILE_DIR` to override either location.

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

### Implementation capture: 2026-09-22

This follow-up measures changes selected from the cleanup profile.
Machine: Apple M1 Pro, `darwin/arm64`, Go 1.26.6, `GOMAXPROCS=8`.
Hermes reads use ten samples per version with a 200 ms measurement target.
Times are benchstat medians. The comparison artifact includes confidence ranges and significance tests.

| Operation | Before | After | B/op before → after | allocs/op before → after |
| --- | ---: | ---: | ---: | ---: |
| Columnar assembly, 128 rows | 42.49 µs | 26.59 µs | 31,272 → 3,456 | 12 → 9 |
| Columnar assembly, 10K rows | 5.887 ms | 4.566 ms | 2,329,608 → 248,544 | 12 → 9 |
| Columnar assembly, 10K rows, limit 50 | 11.625 µs | 9.483 µs | 12,248 → 1,488 | 12 → 9 |
| Predicate assembly, 10K rows, limit 50 | 425.0 µs | 313.3 µs | 435,544 → 17,456 | 12 → 9 |
| Columnar assembly and bitmap reduction, 10K rows | 5.826 ms | 4.665 ms | 2,332,170 → 251,104 | 14 → 11 |
| View iteration, limit 50 | 12.57 µs | 10.52 µs | 20,024 → 9,264 | 12 → 9 |

Temporary selection now retains immutable entry pointers instead of copying complete records.
It adds no persistent projection cache or per-record metadata.
Canonical ordering, scope validation, expiry checks, and vector ownership remain enforced.
See the [Hermes memory model](hermes_hotplane.md#memory-model) for selection and fallback bounds.

The ordered limit benchmark does not represent out-of-order updates.
A separate fixture applies one decreasing timestamp after loading the records.
Correct limit-50 selection then measures 24.17 µs across 128 records and 1.674 ms across 10K records.
The 10K case allocates 428.1 KiB across 42 allocations, including existing delta-index reconciliation.
The heap retains only 50 pointers; the complete query still pays for index traversal.
The original early-stop behavior returns incorrect rows for this case, so its timing is not a valid correctness baseline.
This remains a measured improvement opportunity for update-heavy projections.

The two HTTP allocation failures are resolved through response-owned header storage.
Authenticated middleware falls from 82 to 74 allocations. The secured chain falls from 76 to 68.
Both ceilings are tightened; existing byte ceilings remain unchanged.
Header values, HSTS conditions, authorization, and correlation behavior remain unchanged.

An initial sequential capture suggested small write and HTTP timing regressions.
A controlled comparison alternated original and changed binaries across ten samples each, with a 400 ms target.
Write, batch, and rebuild timings showed no statistically significant change in that comparison.
Authenticated middleware measured 8.083 → 7.707 µs; the secured chain measured 7.625 → 7.258 µs.
Write allocation averages include capacity growth and are not stable gates.

Audience cache hashing now prefixes identifier lengths to prevent ambiguous membership keys.
Ten 200 ms samples retain the previous allocation costs: 64 bytes and two allocations for one identifier.
Three identifiers use 112 bytes and three allocations. The single-identifier timing is statistically unchanged.

Evidence: `benchmark-results/implementation_20260922_comparison.txt`,
`benchmark-results/implementation_20260922_metadata.json`, and
`benchmark-results/implementation_20260922_evidence.json`.
The original captures remain available, including the superseded timing comparison.
These fixtures do not establish application latency, service throughput, retained RSS, or cross-device behavior.

### Reference capture: 2026-09-22

This capture refreshes selected lanes. The remaining tables retain their
historical measurements; this pass did not rerun them.

Machine: Apple M1 Pro, `darwin/arm64`, Go 1.26.6, default `GOMAXPROCS=8`.
Each Go row has ten samples with a 200 ms measurement target.
Values below are benchstat medians with its reported confidence ranges.
These local measurements do not establish production latency or cross-machine speedups.

| Operation | Median time | B/op | allocs/op |
| :--- | ---: | ---: | ---: |
| Router frame dispatch | 10.32 ns ±1% | 0 | 0 |
| Direct frame client | 18.61 ns ±1% | 0 | 0 |
| Borrowed binary frame round trip | 20.81 ns ±1% | 0 | 0 |
| Generated protobuf append round trip | 374.6 ns ±2% | 152 | 6 |
| Correlated HTTP route | 2.232 µs ±1% | 3,756 | 22 |
| Secured HTTP chain | 7.172 µs ±1% | 6,896 | 76 |
| Hermes copied record | 286.7 ns ±4% | 224 | 2 |
| Hermes view iteration, limit 50 | 12.75 µs ±2% | 20,016 | 12 |
| Hermes columnar construction and filtering, 10K records | 5.803 ms ±5% | 2.224 MiB | 14 |
| Hermes record materialization and filtering, 10K records | 7.500 ms ±6% | 7.446 MiB | 10,010 |
| Hermes filtering of a resident columnar batch, 10K records | 30.44 µs ±3% | 2,560 | 2 |

The resident row excludes batch construction. Its ratio against record
materialization is not a complete request speedup.
The full columnar fixture is about 1.29 times faster than the record fixture here.
Neither fixture includes HTTP, database I/O, or production contention.

A separate profile identifies record collection and sorting as useful investigation targets.
Collection accounts for about 86% of sampled allocated space, including benchmark preparation.
Do not interpret that sample as an exact request allocation budget.

The corrected Canvas benchmark exercises both eligible frames and skipped frames.
It measures JavaScript bookkeeping with a stub context, not browser drawing or GPU work.
The WebGL fence capture proves completion and disposal only. Its two samples cannot establish p95 latency.

Evidence: capture metadata and replay commands (`benchmark-results/cleanup_20260922_metadata.json`),
Go summary (`benchmark-results/cleanup_20260922_go_summary.txt`),
Hermes summary (`benchmark-results/cleanup_20260922_hermes_summary.txt`),
resident batch summary (`benchmark-results/cleanup_20260922_hermes_resident_summary.txt`),
Canvas capture (`benchmark-results/cleanup_20260922_canvas.txt`), and
WebGL capture (`benchmark-results/cleanup_20260922_webgl.json`).

Two allocation gates failed in this initial capture: secured HTTP used 76 against 75;
authenticated middleware used 82 against 81. Both failures reproduce with the original checker.
The implementation capture above resolves both failures and tightens the allocation ceilings.
See the [cleanup review](foundation_cleanup_review_20260922.md).

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
