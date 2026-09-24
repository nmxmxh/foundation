# Runtime Layer Optimization and Go WASM Retirement

Date: 2026-09-24
Scope: Foundation Core, native runtime boundaries, and twelve local managed projects.

## Delivered changes

| Layer | Change | Preserved invariant |
| --- | --- | --- |
| Go runtimehost | Pool complete buffer wrappers. Return fixed placement tables. | Reset pooled bytes and retain buffers until native work finishes. |
| Go FFI lifecycle | Stop admission, wait for active calls, then unload the library. | An executing native call retains its host and function pointers. |
| ovrt-core | Reuse diagnostic source string capacity. | Diagnostic text and epochs retain their existing meaning. |
| ovrt-dispatch | Replace descriptor and statistics vectors with fixed arrays. | Generated layout, lane eligibility, and publication ordering remain unchanged. |
| ovrt-unit | Store validated registration metadata and expose pinned handles. | Accepted work retains its selected implementation across replacement. |
| ovrt-native | Bound role queues and use one response slot per request. | Saturation returns an error; response waits retain their timeout. |
| ovrt-ffi | Remove diagnostic allocation through its native dispatch path. | C ABI version 1 and pointer validation remain unchanged. |
| ovrt-network | Avoid futile atomic exchanges and delay frame allocation until path admission. | Probe count, frame limits, and sequence checks remain bounded. |

Successful network completion now publishes path availability before the result.
Previously, immediate subsequent work could observe false backpressure.
A blocked test exposed this race. Its cleanup now has a deadline.
The regression performs 1,000 consecutive races while another path remains blocked.
A Loom model checks availability publication before completion reception.

These changes are the default implementation. No performance feature flag selects the previous implementation.

## Measurement method

The host was an Apple M1 Pro with eight logical CPUs, Go 1.26.6, and Rust 1.92.0.
Measurements used optimized Rust binaries and ordinary Go benchmark builds.
They do not establish results for Rust 1.95, x86, Linux, or production traffic.

Rust measurements used three alternating baseline and modified process runs.
Each row warmed 1,000 operations, then measured seven batches.
The table reports the median of the three batch medians.
Allocation counting ran separately from timing, over 1,000 operations.
Parallel rows warmed 200 operations per thread, then timed 20,000 operations per thread.
Their nanoseconds measure elapsed time divided by total completions, rather than individual request latency.

The baseline used a private SDK copy with the affected production files restored from the recorded Git revision.
Both copies used the same benchmark workload and valid FFI handle lifecycle.
The baseline retained its vector snapshots and omitted the new allocation ceilings.
Existing browser ABI changes were preserved in the working tree.

Go measurements used three runs of 200 milliseconds per row.
Separate profiles captured CPU, blocking, mutex contention, and heap allocations at sampling rate one.
Profile timing includes instrumentation cost and is excluded from the latency comparison.
Hardware counters were not captured. Portable timing, allocator counters, and Go profiles provide the evidence fallback.

Raw captures, metadata, hashes, and comparisons reside in `benchmark-results/runtime_layers_20260924/`.
Use `rust_final_before_*.tsv`, `rust_final_after_*.tsv`, `go_before_valid.txt`, and `go_after.txt` for the measured comparison.

## Results

| Operation | Before ns/op | After ns/op | Before allocations | After allocations |
| --- | ---: | ---: | ---: | ---: |
| Rust region validation | 1.32 | 1.32 | 0 | 0 |
| Rust unit lookup | 13.89 | 14.72 | 0 | 0 |
| Native empty direct dispatch | 33.21 | 22.04 | 1 | 0 |
| Native direct dispatch, 1 KiB | 83.91 | 63.27 | 2 | 1 |
| Native direct dispatch, 64 KiB | 1,448.94 | 1,474.04 | 2 | 1 |
| Native direct dispatch, 1 MiB | 18,131.24 | 20,667.12 | 2 | 1 |
| Native queued dispatch, 1 KiB | 6,341.88 | 6,486.54 | about 10 | about 6 |
| Rust FFI processing, 1 KiB | 138.45 | 114.00 | 2 | 1 |
| Native direct dispatch, four threads | 243.96 | 224.90 | about 2 | about 1 |
| Native direct dispatch, eight threads | 410.34 | 339.47 | about 2 | about 1 |
| Rust snapshot and placement, 32 lanes | 592.67 | 443.57 | 5 | 0 |
| Deduplication under slot pressure | 14.33 | 6.66 | 0 | 0 |
| Busy-path rejection, 4 KiB frame | 140.50 | 51.50 | 1 | 0 |
| Go snapshot and placement, 32 lanes | 660.2 | 505.1 | 2 | 0 |
| Go process dispatch into destination, 1 KiB | 1,642 | 1,671 | 5 | 4 |
| Go process dispatch with owned output, 1 KiB | 1,743 | 1,807 | 6 | 5 |

The Rust FFI row excludes Go cgo entry overhead.
The Go process rows use a deterministic exchange fixture and exclude operating-system transport latency.
Each Go fixture exchange allocates one wrapper. Cancellation accounts for the other three allocations in the destination case.

Rust placement removes 2,944 allocated bytes per decision. Go placement removes 1,792 bytes.
Queued Rust dispatch reduces allocation volume from about 4,079 to 2,886 bytes per request.
Busy-path rejection removes the 4,112-byte frame allocation.
Go process dispatch removes one 24-byte wrapper allocation.

Large payloads have no proven latency improvement. The final short 1 MiB capture was 14% slower.
This triggered an isolated comparison with 50,000 operations per batch and three alternating runs.
The longer 1 MiB medians were 18.90 microseconds before and 19.74 microseconds after, a 4.5% slowdown.
Run medians ranged from 18.36–18.95 microseconds before and 18.44–20.09 microseconds after.
The 64 KiB isolated medians were 1.46 and 1.29 microseconds, with overlapping run ranges.
The captures establish allocation savings; they leave large-output latency as an unresolved performance limit.
See `rust_large_*.tsv`. Repeat this longer comparison with the benchmark argument `--large`.
Queued timing also varies; the allocation reduction is the stronger result.
The Go process rows show allocation savings without a latency improvement.
Concurrent direct dispatch improves, but shared registry and diagnostic synchronization still prevent linear scaling.
These short measurements do not establish p95 or p99 latency.

## Profile interpretation

Before the change, buffer wrappers accounted for approximately 40% of Go process benchmark allocation objects.
Half belonged to the host. Half belonged to the exchange fixture.
After the change, only the fixture wrapper remains. Cancellation contributes approximately 75% of allocation objects.
The retained cumulative and live heap summaries distinguish temporary allocation churn from retained memory.

Cancellation was preserved because request timeout must terminate isolated work without recycling a buffer still used by a worker.
The owned Rust result remains necessary under the current `RuntimeUnit::run` return contract.
An echo operation still copies its entire output once, regardless of the removed diagnostic allocation.

## Go browser WASM retirement

The legacy Go browser shim and its build targets were removed from the template.
Core no longer builds, compiles, or advertises its `main.wasm` artifact.
Rust modules and their scalar/shared build remain supported.
Browser communication uses `@ovasabi/runtime-transport` directly.

The managed retirement patch recognizes known Foundation sources, recipes, and Go artifacts before making changes.
Custom sources, custom recipes, remaining frontend consumers, and non-Go artifacts require review without partial edits.
The patch also removes obsolete CI, workspace, manifest, and validation wiring.
Normal Foundation updates apply this patch before the Rust build patch.

Retirement was applied to these twelve managed projects:

- chowdash_rider_v1
- civic_watch_ng_v1
- docuos_v1
- forest_v1
- global_value_exchange_net_v1
- marketer_v1
- metered_v1
- ovasabi_v1
- pronto_v1
- reframe_v1
- trader_v1
- trotters_v1

The rollout removed 26,349,593 bytes, approximately 25.1 MiB, across source and generated artifact copies.
This includes public and distribution copies. It is not a measured browser download or startup improvement.
No active frontend shim consumers were found in these projects.
Every second patch run made no changes. Every project received a patch ledger entry.
The audit records file hashes, removed bytes, command output, and the temporary backup location.
See `benchmark-results/runtime_layers_20260924/go_wasm_retirement.json`.

This focused project rollout retired the Go shim only.
Projects receive the modified Rust and Go SDK sources through their next normal Foundation update.

## Public contract and migration

The C ABI, shared control layout, and dispatch region format did not change.
Go `SnapshotDescriptors` and `SnapshotStats` now return fixed arrays.
Pass `descriptors[:]` and `stats[:]` to existing slice consumers.
Rust `snapshot_descriptors` now returns an array; `snapshot_stats` also returns an array.
Convert to a vector only when a consumer requires owned vector storage.
No external snapshot call sites were found in the twelve inspected projects.

Registration metadata remains fixed until replacement registration.
Queued requests retain their selected registration across replacement.
Each role permits one pending request per configured worker, in addition to active work.
Excess requests return `native runtime queue is saturated` immediately.
The existing response timeout does not interrupt a running Rust unit.

Go `FFIPool.Close` stops admission and waits for synchronous native calls.
Native units must bound their own execution. Isolated process transports retain timeout and termination support.
Portable scalar execution, direct Go processing, and existing stdio/shared-memory lanes remain available.
The retired Go browser shim has no runtime fallback or opt-in path.

## Benchmark corrections and regression guards

The original Rust placement benchmark allowed stale lanes and discarded the decision result.
The corrected harness keeps lanes eligible and consumes each decision with `black_box`.
Its historical pure-decision number is not directly comparable with the corrected workload.

Two Go atomic benchmarks could reject valid one-iteration calibration.
Their final shared counters now establish completed work.
An initial full test run also exposed an outdated pool fixture, which now uses the complete buffer pool.
The existing timer allocation guard passed five focused repetitions after one noisy instrumented failure.

`make test-bench-runtime-layers` enforces Rust allocation ceilings and Go allocation guards.
Core CI and `make test-bench` invoke this target.
The placement benchmark baseline now permits zero allocations and zero allocated bytes.
Lifecycle, queue saturation, replacement, malformed region, and network publication tests protect the changed behavior.

## Limits recorded before finalization

The [subsequent finalization](runtime_finalize_20260924.md) resolves the first two items through required destinations and immutable snapshots.

1. Large native outputs still allocate and copy. A caller-provided result region would require a unit API and ownership refactor.
2. Concurrent direct dispatch still contends on shared diagnostics and registry state. The thread sweep records this remaining limit.
3. Go process cancellation still allocates. Removing it requires equivalent timeout, termination, and buffer-lifetime evidence.
4. Explicit descriptor cloning still allocates three strings. Dispatch now reads stored metadata without that cloning cost.
5. Region validation already costs about one nanosecond. No bounds checks were weakened for this optimization pass.

## Completed validation

- Rust workspace tests passed with all features: 154 tests passed and one documentation example remained ignored.
- Clippy passed for the complete workspace, all features, and all targets, with warnings and unsafe documentation violations denied.
- Go runtimehost tests, race tests, and `go vet` passed.
- Go package coverage increased from the recorded 87.9% floor to 88.3%.
- New FFI admission and closure paths, pooled buffer construction, and the complete statistics snapshot reached 100% function coverage.
- Descriptor snapshots reached 94.1%; their remaining branch guards a decoder error after an exact row length check.
- Four retirement tests and six existing browser delivery tests passed.
- Normal project initialization, update integration, manifest checks, managed patch hygiene, and documentation checks passed.
- Runtime allocation gates, corrected placement timing gates, runtime practice checks, and profiling contract checks passed.
- Rust formatting and `git diff --check` passed.

Miri was unavailable in the installed toolchain. Real FFI lifecycle tests, Go race tests, and Loom models supplied the applicable concurrency evidence.
The focused project rollout did not execute every application test suite.

## Agent evidence ledger

- Public contract: fixed snapshot return types, pinned registration semantics, bounded queue admission, and retired Go browser build targets.
- Invariant: valid buffer lifetimes, unchanged wire layouts, bounded work, stable unit selection, and safe native library lifetime.
- Evidence: repeated benchmarks, allocation profiles, race tests, Loom models, boundary tests, and scaffold integration tests.
- Fallback: existing portable runtime lanes remain. Custom retirement inputs receive a review message before mutation.
- Boundary: Core owns runtime code and retirement tooling. The focused project edits remove recognized Foundation shim infrastructure.
- Guard: allocation ceilings, lifecycle tests, publication tests, and managed patch idempotency run through maintained targets.
- Documentation: this report, runtime practices, current benchmarks, and scaffold delivery describe the implementation and migration.
