# Movement Accounting Evidence

Date: 2026-10-13
Scope: Foundation Core measurement and regression guards.
Base revision: `b06f99fafd99e32bae973ec90b7fc056e1ccb324`.

Follow-up: [Runtime and gaming confirmation, 2026-10-14](#runtime-and-gaming-confirmation-2026-10-14).

The [numeric validity improvement](../evidence/hermes_validity_words_2026-10-14.md) supersedes the original construction bitmap counts below.
The [fleet adoption](../evidence/hermes_validity_fleet_2026-10-14.md) records subsequent application synchronization.
The original measurements remain historical evidence.

## Objective

Observe explicit payload movement through complete operations before changing execution selection or query algorithms.
Keep allocation, logical access, declared copy, and physical traffic evidence separate.

Research source: [A Modern Primer on Processing-in-Memory, version 5](https://arxiv.org/abs/2012.03112v5).
Sections 4 and 8 motivate movement accounting and complete operation measurements.
The paper does not establish a Foundation speedup or energy reduction.

## Contract and ownership

This work changes the Core measurement contract and its merge guard.
Public APIs, event schemas, runtime buffer layouts, and binding receipts retain their existing semantics.
Planner decisions and Hermes query algorithms remain unchanged.

The edited modules and tooling belong to Foundation Core.
The scaffold manifest contains no individual ownership rows for these module files.
The existing synchronizer propagates Core documentation and checker scripts.
Generated projects do not automatically acquire Core benchmark fixtures or budgets.

## Accounting boundary

`foundation_movement` enables diagnostic probes. Ordinary builds use empty probes that the Go compiler can inline.
Diagnostic counts describe executed source operations within the audited boundary.
Diagnostic timings include instrumentation overhead and must not support latency claims.

Hermes validation exercises the public read, vector construction, predicates, bitmap merge, and selected reduction.
Producer initialization precedes the resident read and remains outside that read budget.
Candidate visits, field probes, and ordering comparisons are work counts.
They are not converted into speculative memory sizes.

FFI validation exercises pool preparation, input publication, the native ABI call, output validation, and caller-owned output.
Host input copies, host output copies, and buffer clearing are separate counters.
Binding receipts remain adapter declarations. They do not become observations of arbitrary native code.

Neither lane measures allocator internals, garbage collection, cache lines, prefetching, or physical DRAM transfers.
The FFI counters do not inspect arbitrary native unit instructions or driver internals.
The complete operation is validated, but byte accounting has explicit coverage limits.

Hermes also excludes record pointers, map storage, string headers, and parser internals.
Parse bytes describe supplied text length, not every parser access.
Explicit copy bytes overlap their bitmap read and write counters. Do not add these counters together.
Each installed bitmap hook increments an unobserved-call counter, including hooks that decline execution.
The scalar benchmark requires zero unobserved calls.

Collectors require isolated, serial diagnostic workloads.
These private collectors do not provide general production telemetry.
The FFI budget covers `ExecuteInto` with caller storage. It excludes the allocated-output `Execute` path.

## Observed movement

The Hermes fixture contains 10,000 resident records and two numeric fields.
Every fifth price is absent. Bucket values repeat from zero through fifteen.
The query selects prices above 7,500 and buckets at most seven, then sums selected prices.
Both paths return the same independent fixture result.

| Counter per operation | Build and query | Resident query |
| --- | ---: | ---: |
| Numeric reads, bytes | 175,968 | 175,968 |
| Numeric writes, bytes | 144,000 | 0 |
| Bitmap reads, bytes | 152,792 | 8,792 |
| Bitmap writes, bytes | 150,280 | 6,280 |
| Explicit copies, bytes | 0 | 0 |
| Parse input, bytes | 55,822 | 0 |
| Candidates visited | 10,000 | 0 |
| Field probes | 40,007 | 0 |
| Sort comparisons | 252,684 | 0 |
| Rows materialized | 10,000 | 0 |
| Unobserved kernel calls | 0 | 0 |

Zero explicit copies describe the audited bitmap copy sites. They do not establish a zero-copy request.
The older `bytes_touched/op` formula is now named `kernel_model_bytes/op`.
Its formula remains unchanged and excludes construction and repeated accesses.

| FFI payload bytes | Input copied | Output copied | Control cleared | Error buffer cleared |
| ---: | ---: | ---: | ---: | ---: |
| 8 | 8 | 8 | 4,096 | 4,096 |
| 64 | 64 | 64 | 4,096 | 4,096 |
| 256 | 256 | 256 | 4,096 | 4,096 |
| 1,024 | 1,024 | 1,024 | 4,096 | 4,096 |

The FFI fixture loads a compiled native library and verifies transformed output through the actual ABI call.
The binding test reconciles declared input copies with observed host copies.
It also verifies rejection before execution when the input-copy budget is zero.

## Invariants and fallback

Preserve tenant scope, correlation, bounds, cancellation, canonical ordering, null behavior, and caller ownership.
Preserve existing controlled errors and fallback behavior.
Instrumentation must not alter results, admission decisions, or buffer layouts.
Ordinary builds must retain their existing allocation budgets.

Missing metrics, failed commands, and changed audited source must fail the movement guard.
Unknown work must not become a zero measurement.

## Regression guard

`make check-benchmark-ratchet` runs the ordinary allocation guard, then the separate movement guard.
The movement guard requires 38 integer ceilings across six benchmark rows and two packages.
It also requires successful completion of 12 audited accounting tests.
Missing, skipped, duplicate, malformed, excessive, and failed measurements reject the run.
Commands have explicit outer and package timeouts.

The source manifest covers 86 normalized function declarations, including signatures, bodies, fixtures, capture helpers, and selected accessors.
An additional unmetered copy invalidates this audit even when allocation counts remain unchanged.
Comments and formatting do not invalidate normalized declarations.
The audit excludes globals, type definitions, imports, native preambles, and unaudited callees.
Review those boundaries before accepting a source refresh.

Source refreshes require `HUMAN_SUPERVISED_CHECK_UPDATE=1` and preserve budgets.
The integrity manifest protects the checker, configurations, fixtures, source manifest, and Makefile integration.
The checker never raises movement ceilings automatically.

## Validation record

Host: Apple M1 Pro, macOS 26.5.2, Go 1.26.6, `darwin/arm64`, with cgo enabled.
The original checkout was clean at the stated base revision.
An independent archived checkout supplied the original comparison binaries.
These results establish local behavior for the specified fixtures.
They do not establish production latency, Linux behavior, physical traffic, or energy savings.

| Validation | Result |
| --- | --- |
| Complete ordinary Hermes suite | Passed; coverage increased from 85.8% to 85.9%. |
| Complete ordinary runtimehost suite | Passed; coverage remained 88.3%. |
| Complete tagged suites | Passed; Hermes coverage 86.0%, runtimehost coverage 88.5%. |
| New accounting helpers | 100% statement coverage in both packages. |
| Ordinary race suites | Passed for both complete packages. |
| Tagged accounting race suites | Passed for both packages. |
| Tagged `go vet` | Passed for both packages. |
| `make check-core-validation-contract` | Passed, including existing guard fixtures and 61 movement cases. |
| Checker coverage | 96.4% statement coverage. |
| `make check-benchmark-ratchet` | Passed ordinary allocation budgets and all movement ceilings. |
| Enforcement integrity | Passed after reviewed manifest refresh. |
| Coding, concurrency, practice, runtime, and documentation checks | Passed. |

The accounting tests cover bitmap boundaries at 1, 63, 64, 65, and 1,000 rows.
They also cover absent values, empty results, malformed numbers, cancellation, tenant boundaries, fences, and accelerator fallback.
FFI tests cover empty through maximum payloads, caller ownership, rejected budgets, invalid destinations, closed pools, and runtime failures.

The existing testing-practice check rejects a 5,000 ms timeout in `frontend-lab/src/browser/browserAbi.browser.test.ts:12`.
The same line exists at the base revision. This task leaves that unrelated file unchanged.
Full repository verification is not claimed.

Raw records:

- Environment and changed-source hashes: `benchmark-results/movement_20261013/metadata.txt`
- Package suites and coverage: `benchmark-results/movement_20261013/package_tests.txt`
- Allocation and movement gate: `benchmark-results/movement_20261013/ratchet.txt`
- Core validation and checker coverage: `benchmark-results/movement_20261013/core_validation.txt`
- Initial static checks and existing failure: `benchmark-results/movement_20261013/static_initial.txt`
- Remaining static checks: `benchmark-results/movement_20261013/static_remaining.txt`
- Integrity verification: `benchmark-results/movement_20261013/integrity.txt`

## Ordinary-build comparison

Both binaries received a discarded warmup of 200 operations.
Four measured pairs alternated execution order, with 200 operations per sample.

| Fixture | Original median | Revised median | Allocation result |
| --- | ---: | ---: | --- |
| Hermes build and filter | 3.588 ms | 3.618 ms | Both 11 allocations; approximately 251 KB per operation. |
| RuntimeHost placement | 536.9 ns | 566.0 ns | Both zero allocations and zero allocated bytes. |

Timing ranges overlap. The short RuntimeHost samples are too noisy to establish a latency change.
These samples confirm allocation consistency, without supporting a speedup claim.
Instrumented timings remain excluded from this comparison.
See the warm alternating samples: `benchmark-results/movement_20261013/ordinary_comparison.txt`.

## Mutation proof

An isolated copy received two deliberate mutations. The working checkout retained its original implementation.
The extra copy was injected to test detection. No duplicate input copy required removal from the working implementation.

1. Add an unmetered input copy inside `Buffer.SetInputBytesFast`.
   The source audit rejects the changed declaration before accepting measurements.
2. Meter the additional copy and refresh only the isolated source manifest.
   The actual movement tests reject doubled input copies at every nonempty test size.

The mutated binding still returns `RESIDENT` successfully.
Its receipt declares eight input bytes, while observation counts sixteen.
The existing output and zero-allocation test still passes on that mutated implementation.
This demonstrates a regression that allocation and output checks alone can miss.
Required tests and ceilings remain unchanged throughout the experiment.
See the mutation commands, diffs, and failures: `benchmark-results/movement_20261013/mutation.txt`.

## Reproduction

Run the integrated gates from the Core root:

```sh
make check-core-validation-contract
make check-benchmark-ratchet
make check-enforcement-integrity
```

Run each package suite from its module directory:

```sh
# server-kit/go
go test ./hermes -count=1 -cover -timeout=180s
go test -tags=foundation_movement ./hermes -count=1 -cover -timeout=180s
go test -race ./hermes -timeout=180s
go test -tags=foundation_movement -race ./hermes -run '^TestMovement' -count=1 -timeout=180s
go vet -tags=foundation_movement ./hermes

# runtime-sdk/go
go test ./runtimehost -count=1 -cover -timeout=180s
go test -tags=foundation_movement ./runtimehost -count=1 -cover -timeout=180s
go test -race ./runtimehost -timeout=180s
go test -tags=foundation_movement -race ./runtimehost -run '^TestMovement' -count=1 -timeout=180s
go vet -tags=foundation_movement ./runtimehost
```

## Follow-up boundary

Review the evidence before proposing planner calibration or Hermes selection changes.
Those changes need separate workload comparisons, parity checks, and fallback evidence.
The next reviewer should cover performance measurement and runtime ownership.

## Runtime and gaming confirmation: 2026-10-14

The user requested confirmation of the mutation result, runtime checks, and gaming practices.
Inspection confirms one input copy in `Buffer.SetInputBytesFast`.
`FFIPool.executeInto` copies output into caller storage before returning the pooled buffer.
Removing that output copy would violate caller ownership.
The working source still matches the preserved hash from the mutation experiment.
Fresh measurements retain every original movement ceiling.

### Fresh validation

| Scope | Result | Evidence path |
| --- | --- | --- |
| Complete ordinary Go runtimehost suite | Passed. | `benchmark-results/movement_confirmation_20261014/runtimehost_tests.txt` |
| Tagged runtime movement tests with race detector | Passed. | `benchmark-results/movement_confirmation_20261014/runtimehost_movement_race.txt` |
| Allocation and movement gates | Passed all existing ceilings and source audit. | `benchmark-results/movement_confirmation_20261014/ratchet.txt` |
| Browser runtime unit suites | 293 tests passed across 34 files. | `benchmark-results/movement_confirmation_20261014/browser_runtime_tests.txt` |
| Rebuilt scalar and shared Rust/WASM ABI | Nine integration tests passed. | `benchmark-results/movement_confirmation_20261014/browser_abi.txt` |
| Rust core, FFI, browser, and unit crates | 41 tests passed under Rust 1.95. | `benchmark-results/movement_confirmation_20261014/rust_runtime_tests.txt` |

Browser unit tests include render channels, worker ownership, arena bounds, device fallback, and planner behavior.
The real ABI tests compare direct and legacy access through compiled scalar and shared guests.
They exercise copy imports, output parity, invalid lengths, memory growth, released handles, and region budgets.
Zero direct copy-import calls do not account for input publication or owned output reads.
The render-channel tests verify borrowed slots, generation stability, and reuse of cached views.

This confirmation does not include a deployed game journey or a physical GPU capture.
Node tests verify contracts and mocked device behavior. They do not establish device traffic or frame latency.
The allocation-profile suite was excluded from this behavioral run.
Its timing and heap experiments require a separate controlled capture.

### Practice integration

RUNTIME-02 now requires lane-specific movement evidence through contextual review.
The existing automated guard remains limited to its audited Hermes and Go FFI paths.
The new coverage table names the browser, WASM, process, render, and GPU measurements still required.

Updated owning documents:

- [Runtime foundations](../runtime_foundation.md): host copies, clearing, ownership, and adapter declarations.
- [Game runtime practices](../game_runtime_practices.md): executed frames, producer writes, uploads, readbacks, and residency.
- [GPU practices](../gpu_practices.md): staging, device transfers, completion, and unobserved driver work.
- [Rust practices](../rust_runtime_practices.md): borrowing, output construction, parity, and safe ownership.
- [SAB contracts](../runtime_sab_capnp_contracts.md): direct ABI boundaries and complete operation accounting.
- [Native bindings](../native_resource_bindings.md): declared receipts and independent measurements.
- [Render surfaces](../render_surface_lane.md): shared-slot lifetime, executed frames, and observer costs.
- [Performance lab](../performance_lab.md) and [performance practices](../performance_practices.md): scoped guards and RUNTIME-02.
- [Practice controls](../practice_controls.md): automated fixture scope and contextual review.
- [Glossary](../foundation_glossary.md), [research](../future_practices_research.md), and [philosophy](../PHILOSOPHY.md): consistent allocation and movement terminology.

The Core guide and both READMEs also describe borrowed views and explicit payload ownership.
The SDK README now matches the generated epoch, route, header, and reserved byte ranges.
The practice matrix records RUNTIME-02 as contextual review, without claiming automated coverage for uninstrumented lanes.
The normal scaffold synchronizer distributes these Core documents.
This confirmation does not synchronize or modify consumer applications.

### Confirmation contract

Public APIs, event schemas, buffer layouts, planner policy, and query algorithms retain their existing contracts.
This follow-up changes documentation and the evidence required during runtime review.
Ownership, tenant-safe clearing, bounded work, and fallback remain required invariants.
Malformed input, missing capability, runtime errors, and device loss retain their existing failure paths.
Existing tests and the movement guard remain the executable regression evidence.
Each future lane needs its own guard before promotion to automatic enforcement.

Additional reproduction commands from the Core root:

```sh
FOUNDATION_REQUIRE_TS_DEPS=1 tooling/scripts/run_vitest.sh runtime-sdk/ts/browser-host run --exclude '**/*.profile.test.ts'
rustup run 1.95 cargo test --locked --manifest-path runtime-sdk/rust/Cargo.toml -p ovrt-core -p ovrt-ffi -p ovrt-browser -p ovrt-unit
RUSTUP_TOOLCHAIN=1.95 bash runtime-sdk/scripts/build_browser_wasm.sh
FOUNDATION_REQUIRE_TS_DEPS=1 tooling/scripts/run_vitest.sh runtime-sdk/ts/browser-host run --config vitest.abi.config.ts
```

The shared build uses the installed nightly toolchain and its experimental atomics feature.
The raw build record retains the corresponding warning.
The confirmation metadata records toolchain versions, commands, and source hashes.
See `benchmark-results/movement_confirmation_20261014/metadata.txt`.
Practice validation records appear in `benchmark-results/movement_confirmation_20261014/practice_validation.txt`.
The render-surface validation appears in `benchmark-results/movement_confirmation_20261014/frontend_surface_validation.txt`.
Final documentation and integrity results appear in `benchmark-results/movement_confirmation_20261014/final_validation.txt`.
