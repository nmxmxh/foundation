# Runtime Output and Concurrent Dispatch Finalization

Date: 2026-09-24
Scope: Foundation Core and the managed project updater.

## Required output contract

`RuntimeUnit::execute(input, output)` replaces the required `run` implementation.
Every unit receives a checked `RuntimeOutput` destination.
FFI and control exchanges write directly into their output region.
`ArenaBlobUnit` writes directly into its assigned slab after validating both regions and rejecting overlap.
There is no feature flag or alternative unit implementation path.

The provided `run` method returns owned bytes when a caller needs ownership.
`write_owned` transfers an existing allocation into an empty owned destination.
Algorithms that already construct vectors retain those allocations after mechanical migration.
Streaming encoders can write chunks directly into the destination.

Input and output regions must remain valid, disjoint, and exclusively assigned until completion.
Mapped reads borrow only their requested region, without creating references over adjacent writable slabs.
Writes reject overflow before copying. Failed direct calls clear their complete destination.
Successful calls return the valid prefix length; bytes outside that prefix are not result data.
Arena consumers must wait for successful control publication before reading any result bytes.
A failed arena unit can leave unpublished partial bytes in its assigned slab.
The host must retain slab ownership through failure cleanup and reuse.

## Concurrent dispatch

The unit registry publishes immutable maps through pinned `arc-swap` 1.9.2 snapshots.
Direct execution pins a registry generation without cloning the selected unit or taking a registry read lock.
Registration serializes writers and clones the map. Registration cost is O(n) in registered units.
A request retains its selected implementation across replacement.

Successful diagnostics update 64 bounded counter shards, each aligned to 128 bytes.
These shards require 8 KiB per host. Threads beyond 64 can share a shard safely.
Repeated success from the same runtime source avoids the diagnostic mutex.
Source changes, errors, and diagnostic snapshots still acquire that mutex.
Counters saturate. Snapshots aggregate live counters and do not provide a transaction across ongoing calls.
A scope guard releases direct-call accounting during error or panic unwinding.
Sampled units also release their placement claim during unwinding.

The dependency replaces an existing registry lock; it does not introduce another cache.
The [ArcSwap documentation](https://docs.rs/arc-swap/1.9.2/arc_swap/) describes this immutable snapshot pattern.
The pinned release includes the memory-order corrections introduced in the 1.9 series.

## Benchmark method

Machine: Apple M1 Pro, eight logical CPUs. Rust: 1.92.0. Go: 1.26.6.
These results do not establish production traffic, Linux, x86, or Rust 1.95 performance.
The baseline preserved all changes from the preceding runtime optimization report.
It therefore includes the implementation associated with the earlier 4.5% large-output slowdown.

Three process comparisons alternated baseline and revised order.
Each row warmed 1,000 operations and measured seven batches.
The tables report the median of three run medians.
Long payload batches contained 50,000 operations each.
Allocation counting ran separately over 1,000 operations.
Parallel batches used 20,000 operations per thread after 200 warmup operations.
Parallel nanoseconds describe elapsed time per aggregate completion, not individual request latency.

Owned-output rows use the same public dispatch call in both versions.
Destination rows compare the previous owned-result-plus-copy flow with direct destination execution.
The baseline harness explicitly implements that previous copy; the old SDK had no destination method.
These rows isolate dispatch and memory movement. They exclude application encoding and process transport.
The Rust FFI row excludes Go cgo entry overhead.

| Operation | Before | After | Allocations before → after |
| --- | ---: | ---: | ---: |
| Destination output, 1 KiB | 77.87 ns | 34.31 ns | 1 → 0 |
| Destination output, 64 KiB, long run | 3.506 µs | 1.307 µs | 1 → 0 |
| Destination output, 1 MiB, long run | 44.734 µs | 19.384 µs | 1 → 0 |
| Owned output, 1 MiB, long run | 20.067 µs | 19.266 µs | 1 → 1 |
| Rust FFI processing, 1 KiB | 116.12 ns | 68.39 ns | 1 → 0 |
| Four concurrent callers, owned 1 KiB | 343.20 ns | 39.39 ns | 1 → 1 |
| Eight concurrent callers, owned 1 KiB | 395.79 ns | 41.02 ns | 1 → 1 |
| Owned output, 1 KiB | 63.39 ns | 75.26 ns | 1 → 1 |
| Owned output, 64 KiB, long run | 1.312 µs | 1.410 µs | 1 → 1 |

The 1 MiB destination flow improves 56.7% and removes 1 MiB of temporary allocation per request.
Its run medians ranged from 43.82–45.72 µs before and 18.14–19.64 µs after.
Owned 1 MiB output improves 4.0% against the immediate baseline.
Its ranges were 19.93–20.34 µs before and 18.31–19.40 µs after.
This comparison improves on the immediate baseline. It does not repeat the original Git baseline measurement.
Small owned results remain slower: 18.7% at 1 KiB and 7.5% at 64 KiB.
The required destination API adds checked writer dispatch to those allocation-bearing calls.
Queued dispatch retains approximately six allocations; its timing remains noisy.

Raw samples, comparison JSON, source hashes, and baseline details reside in `benchmark-results/runtime_finalize_20260924/`.
Repeat the long workload with `cargo bench --manifest-path runtime-sdk/rust/Cargo.toml -p ovrt-ffi --bench runtime_layers -- --large`.

## Managed migration

`rust_unit_output_patch.py` converts concrete project `RuntimeUnit` implementations to the required method.
It preserves early returns, fallible operations, comments, literals, and existing algorithm bodies.
It plans and formats all changes before writing and refuses custom signatures or conflicting methods.
Traversal excludes build directories and has a fixed file-count bound.
Every migrated file enters `.foundation-patches.tsv`.

The ordinary updater also resolves stale Rust locks with `cargo update -p ovrt-unit`.
It attempts the local cache first and uses the existing dependency timeout for each attempt.
`--skip-deps` preserves locks and reports the required command.
No update requires `--force` for this migration.

## Evidence ledger

- Contract: Required Rust unit method changed. C ABI version 1 and shared layouts remain unchanged.
- Invariant: Region bounds, disjoint slabs, pinned registration, publication ordering, and request lifetime remain enforced.
- Evidence: Alternating allocation benchmarks, Rust tests, arena integration, browser ABI parity, Go race checks, and Clippy.
- Fallback: Owned callers use the provided adapter. Unsupported or oversized destinations return controlled errors.
- Scope: Core owns the SDK and updater. Managed migration narrowly adapts project unit methods and dependency locks.
- Regression guards: Zero-allocation destination/FFI ceilings, concurrent registry replacement, unwind cleanup, and diagnostic Loom models.
- Documentation: This report, runtime practices, benchmark reference, Rust unit guide, and scaffold ownership guidance.

## Rollout

The first fleet pass synchronized all eleven indexed projects. Two scaffold validations failed.
Pronto placed a telemetry collector in a directory reserved for domain services with protobuf contracts.
The unchanged collector moved to `internal/edgeanalytics`; its sole startup import now uses that path.
Reframe omitted linked configuration and browser-runtime packages from its Docker frontend stage.
The managed Docker patch now inserts their manifest and source copies before the corresponding build steps.
A regression verifies insertion order and idempotency.

The final fleet report resides in `test-results/fleet-runtime-finalize-20260924-final/` in Core.
The preceding report preserves the initial validation failures for comparison.


The final updater pass completed successfully for all eleven indexed projects.
Every project passed scaffold validation and the idempotence check, without `--force`.
An audit compared 104 SDK files per project and found no mismatches, pending unit migrations, or stale Rust locks.
Twenty unit methods migrated across four projects.

Downstream tests exposed an unrelated Reframe rendering defect: its image and video filters referenced `levels`.
The installed FFmpeg rejected that filter before producing media.
Both expressions now use [documented colorlevels parameters](https://ffmpeg.org/ffmpeg-filters.html#colorlevels), preserving the requested RGB input thresholds.
Existing real-media tests provide the regression guard.
This changes no media request schema or runtime ABI.

The corrected video integration passed with real FFmpeg output.
WebP integration remains unavailable on this host: its FFmpeg build has no `libwebp` encoder.
The full run recorded that failure. A separate run explicitly excludes only that unavailable-codec test.
No test assertions or production fallback rules were weakened.
The source fix retains the documented SVG fallback when native image encoding is unavailable.

Final checks: 165 Core Rust tests, 25 native shell tests, nine browser ABI tests, and the arena integration passed.
The later mapping check passed all six mapping tests and the concurrent arena integration.
Go runtimehost race tests and vet passed, with 88.5% statement coverage.
Downstream Rust tests passed: financial 5, Ovasabi 54, Pronto 92, and Reframe 17, with the stated WebP exclusion.
All four project Rust workspaces passed Clippy with warnings denied.
Pronto collector tests and startup compilation passed after the package move.
