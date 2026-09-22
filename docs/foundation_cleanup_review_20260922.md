# Foundation Cleanup Review

Date: 2026-09-22
Owner: Platform Architecture
Status: implemented measurement repairs and runtime improvements; consumer research remains open

## Scope and conclusion

The owner reports more than ten consuming applications, including three production applications.
This review covers Core, scaffold checks, benchmark evidence, documentation, and the frontend lab.
It does not claim an audit of those applications or production workloads.

The strongest immediate opportunity is trustworthy measurement and consistent consumer wiring.
Several checks could accept missing evidence. One updater could increase supposedly permanent allocation ceilings.
One graphics experiment used an invalid constant. Another benchmark mostly measured skipped frames.
These defects were repaired before the runtime implementation follow-up below.

The fresh Hermes profile points toward record collection and sorting.
Resident bitmap operations are already much cheaper than assembling their input.
The implementation follow-up reduces that assembly cost while preserving ordering, freshness, ownership, and tenant isolation.

## Implementation follow-up

The authorized implementation pass delivers these changes in Core:

| Change | Result | Regression evidence |
| --- | --- | --- |
| Retain immutable entry pointers during columnar assembly. Use typed sorting. | Full 10K assembly allocates about 89% fewer temporary bytes. Tested reads improve by 18–37%. | Size and selectivity benchmarks; tenant, expiry, cancellation, and concurrent replacement tests. |
| Prove ordered traversal before applying an early limit. Use bounded selection when the proof fails. | Out-of-order timestamps and repeated versions return the correct canonical rows. | The original implementation fails the new ordering test. Concurrent invalidation forces one bounded rescan. |
| Store security header values in one response-owned array. | Eight fewer allocations per secured request. Both failing allocation gates now pass with tighter ceilings. | Exact header values, HSTS conditions, mutation isolation, and an allocation budget. |
| Prefix audience identifier lengths before cache hashing. | Distinct membership sets cannot share bytes through ambiguous delimiter placement. | The original HTTP handler returns another audience's cached record in the new regression fixture. |
| Reject failed coverage commands and absent recorded packages. | Partial test output cannot pass the gate or delete coverage floors. | Failure, missing package, missing module, floor regression, and update fixtures. |
| Consolidate three deadline wrappers and bound updater dependency commands. | One tested helper enforces deadlines. `--skip-deps` supports synchronization without dependency resolution. | Exit status, diagnostics, descendant termination, missing Perl, and dependency-spy tests. |

The audience defect requires an enabled snapshot cache and identifiers containing a zero byte.
The fix changes internal cache key encoding. Wire messages, public signatures, and persistent data remain unchanged.
Caches are process-local, so this change requires no data migration.
An unused private scope-invalidation helper was removed. Epoch validation remains the active cache invalidation path.

The original sequential timing comparison suggested small write and HTTP regressions.
Alternating original and changed binaries removed the apparent write regression.
The controlled comparison measured HTTP requests about 5% faster, with unchanged byte allocation totals.
The raw sequential results remain available alongside the controlled comparison.
Unordered limit-50 reads inspect all candidates and can allocate temporary keys during existing index reconciliation.
The 10K unordered fixture measures 1.674 ms and 428.1 KiB. The ordered fixture does not represent this workload.

See the [implementation capture](foundation_benchmarks.md#implementation-capture-2026-09-22)
and `benchmark-results/implementation_20260922_evidence.json` for commands, coverage, and limits.

The scope boundary is Core runtime internals, projection cache isolation, and Core-owned scaffold tooling.
The columnar fallback scans candidates with bounded retained storage when publication order cannot prove canonical order.
Uncacheable projection requests retain the existing uncached path. Header values retain response-local ownership.
Dependency failures retain diagnostics and the documented manual synchronization path.

This pass does not migrate production applications, change module boundaries, or claim service-backed performance.

## Completed repairs

| Finding | Change | Regression evidence |
| --- | --- | --- |
| Failed benchmark commands could disappear through process substitution. | Capture command status and diagnostics before parsing. | A failing fixture with complete rows must fail. |
| Missing recorded benchmarks produced warnings. Updates could discard their ceilings. | Require every recorded benchmark before checking or updating. | Missing rows, missing modules, and incomplete updates fail. |
| Zsh quoted associative keys prevented lookup of old ceilings. | Use one consistent key representation. | An attempted increase preserves both old ceilings. |
| The same quoting error disabled duplicate control detection. | Normalize control keys and remove quote stripping. | Duplicate and unmapped control fixtures fail. |
| History parsing omitted Vitest 5 rows. | Parse both table forms and optional ranking labels. | Legacy, current, fastest, and slowest rows remain present. |
| History lacked provenance and reused profile locations. | Add metadata, status, and capture-specific profile directories. | Failure artifacts and relative output directories are checked. |
| Vitest 5 inherited plugins twice. Obsolete options broke lab type checking. | Keep plugins at the root and remove unsupported options. | Lab type checking and 48 contract tests pass. |
| Two shadow assertions described superseded styling. | Match current low-power and balanced tier tokens. | Chromium evaluates the actual computed styles. |
| WebGL used `SYNC_OBJECT_TYPE` as a fence condition. | Use the specified enum, flush, bound polling, and clean up on disposal. | A real worker produces completed samples and stops after disposal. |
| GPU summaries used the wrong percentile index. | Use nearest rank and mark p95 below its sample floor. | Five statistical tests cover boundaries and invalid values. |
| Canvas timing reused one timestamp and skipped subsequent frames. | Measure actual stage bookkeeping in distinct draw and skip cases. | Benchmark callbacks assert the expected branch and consume results. |
| Glossary control tables duplicated stale practice lists. | Link the canonical documents and control matrix. | Documentation and control checks validate references. |
| Generated failure screenshots were committed. | Remove 32 attachments, ignore new captures, retain CI diagnostics. | Rendering contracts pass; source fixtures remain available. |

The Core CI workflow now runs DOM, SSR, and Chromium contracts through
`make test-frontend-lab-contracts`. Hardware GPU measurements remain a separate lane.
The new workflow has local validation; its hosted execution remains unverified.

## Fresh measurements and their meaning

The [benchmark reference](foundation_benchmarks.md#reference-capture-2026-09-22)
contains the measured table and links to raw samples.
The metadata artifact (`benchmark-results/cleanup_20260922_metadata.json`)
records commands, versions, source hashes, and limitations.

Ten samples per Go benchmark establish a local reference, not a production service guarantee.
The machine used Go 1.26.6 on an Apple M1 Pro with eight logical CPUs.
No runtime implementation changed between those samples and the initial checkout.

| Work boundary | Measured median | Interpretation |
| --- | ---: | --- |
| Resident columnar filter | 30.44 µs | Batch construction is outside the timed operation. |
| Columnar construction and filter | 5.803 ms | Includes candidate collection and batch construction. |
| Record materialization and filter | 7.500 ms | Includes copied record materialization. |
| Secured HTTP chain | 7.172 µs | In-memory fixture; no production network or database. |

The former README claim compared resident filtering with materialization.
That boundary obscured the cost an application pays when it cannot reuse the batch.
The revised README names both costs. The complete columnar fixture has about a 1.29 ratio against record filtering.

The CPU profile (`benchmark-results/cleanup_20260922_hermes_cpu.txt`)
shows record lookup, collection, and sorting among the leading costs.
The allocation profile (`benchmark-results/cleanup_20260922_hermes_alloc.txt`)
attributes about 86% of sampled allocated space to collection.
These profiles include benchmark preparation. Their percentages identify candidates and do not replace per-operation measurements.

The current `bytes_touched/op` metric estimates filtering traffic. It does not describe all traffic during batch construction.
Keep allocation, retained memory, copied bytes, and estimated scan traffic as separate measurements.

The initial audit found two allocation failures, now resolved by the implementation follow-up:

- `BenchmarkAppLane_HTTPMiddleware_AuthSecurityRBAC`: 82 allocations against a ceiling of 81.
- `BenchmarkNullLane_06_HTTPSecuredChain`: 76 allocations against a ceiling of 75.

Both failures reproduce with the original checker. No allocation ceiling was increased.
Allocation output is an averaged count under a specific compiler and workload.
Investigate the implementation and toolchain before attributing this difference to a particular change.
See the [Go testing contract](https://pkg.go.dev/testing#BenchmarkResult.AllocsPerOp).

## Prioritized implementation work

The assembly, HTTP allocation, and dependency deadline items are implemented above.
The remaining proposals require their own measured acceptance evidence.

| Priority | Scope and owner role | Concrete next experiment | Acceptance and fallback |
| --- | --- | --- | --- |
| Done | Hermes maintainer; `server-kit/go/hermes/columnar.go` | Reduce temporary record copies and correct limited ordering. | Read, write, and rebuild comparisons; scope, ownership, and concurrency regression tests. Retained RSS remains unmeasured. |
| Done | HTTP/runtime maintainer; `server-kit/go/appbench` | Reduce response-header allocations without changing header values. | Eight fewer allocations; stricter ceilings; header and race tests. The original compiler-specific extra allocation was not attributed. |
| P1 | Hermes maintainer; delta-index traversal | Reduce reconciliation memory for unordered reads after bulk loading. | Preserve replacement and deletion semantics. Compare compact indexes, delta chains, read limits, and write costs without adding a cache. |
| P1 | Platform architect; scaffold composition | Continue the existing `appkit` extraction proposal after inventorying actual application seams. | Demonstrate one non-production migration, then one production canary. Preserve project registration hooks and the previous composition path. |
| P1 | Release owner; consuming applications | Record Core revision, package versions, scaffold generation, runtime contract, and update status for each consumer. | Generate compatibility evidence from actual consumers. In-repository references alone cannot justify deleting exported modules. |
| P2 | Frontend maintainer; lab and package manifests | Test supported React versions and the Vite integration independently. | The lab uses React 18 with React 19 types. `frontend-kit` declares Vite 6/7 peers while the lab resolves Vite 8. Validate before widening peers. |
| P2 | Runtime maintainer; `renderMarks.ts` | Measure diagnostic retention during a long transition test. Define mark retention and pass-name cardinality. | Preserve observer behavior and stable public names. Test bounded retention before adding automatic clearing or sampling. |
| P2 | Benchmark owner; history and device scripts | Add structured Vitest output and align remaining device percentile helpers with the mathematical contract. | Preserve raw samples, units, workload identity, and sample floors. Criterion results currently remain in raw history output. |
| P2 | Database owner; service-backed lane | Capture representative reads, writes, queue pressure, and projection rebuilds with query plans. | Measure pool waits, I/O, WAL, projection lag, and failure recovery. Keep CPU work outside SQL. |
| P2 | Tooling owner; scaffold distribution | Classify copied scripts by Core-only and project responsibilities. | Prove every generated profile retains its required checks before shrinking the copied set. Preserve the existing updater. |
| Done | Tooling owner; dependency maintenance | Add deadlines and `--skip-deps` for updater dependency commands. | Preserve diagnostics. Verify skipped commands with spies and process-group termination with a descendant test. |

The `appkit` proposal already exists in
[`foundation_project_standardization.md`](foundation_project_standardization.md).
The package is not implemented. Creating another design would add repetition.
The useful next step is a tested extraction of common runtime assembly.

Do not add a columnar cache solely because resident filtering is fast.
The existing cache rule requires measured layering evidence and an allocation guard.
A useful experiment must include mutation frequency, invalidation, eviction, retention, and stale-read behavior.

## Research mapped to Foundation

### Go and evidence quality

Foundation already uses `B.Loop` extensively. A mechanical repository rewrite has little value.
Audit setup boundaries and observable work when editing each benchmark.
The [Go benchmark guidance](https://go.dev/blog/testing-b-loop) explains the timer and compiler protections.

Use repeated samples and workload identity for comparisons.
Keep timing thresholds separate from deterministic behavioral gates.
The [benchstat documentation](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat)
supports statistical comparison; it cannot correct mismatched workloads.

Go 1.25 introduced container-aware `GOMAXPROCS` handling on Linux.
An explicit override disables automatic updates.
The performance guide now reflects this behavior instead of recommending another controller unconditionally.
See the [runtime release notes](https://go.dev/doc/go1.25#runtime).

Consider `testing/synctest` for isolated retry and cancellation tests with fake time.
Keep real browser, socket, and service tests where external scheduling is the subject.
Use bounded flight recordings for incident evidence, with retention and sensitive-data controls.
Evaluate PGO only with representative application profiles and a reproducible fallback build.
Sources: [testing time](https://go.dev/blog/testing-time),
[Go runtime notes](https://go.dev/doc/go1.25#runtime), and
[PGO guidance](https://go.dev/doc/pgo).

### Graphics and frontend measurement

WebGL fence creation requires `SYNC_GPU_COMMANDS_COMPLETE`.
Queue completion includes submission and polling delays; it is not shader execution time.
The corrected experiment supports a future production fence adapter, subject to context-loss and watchdog tests.
Source: [WebGL 2 synchronization](https://registry.khronos.org/webgl/specs/latest/2.0/#3.7.14).

Worker animation frames are available under the HTML worker ownership conditions.
Do not assume every worker lacks `requestAnimationFrame`, or that every worker supports it.
Capability detection and fallback remain required.
Source: [HTML animation frames](https://html.spec.whatwg.org/multipage/imagebitmap-and-animations.html#animation-frames).

WebGPU timestamp queries are optional. Queue completion and kernel timestamps answer different questions.
Feature detection must retain the existing fallback paths.
Source: [WebGPU specification](https://www.w3.org/TR/webgpu/).

Long Animation Frames reports frames above 50 ms.
It cannot by itself identify every missed frame on a 120 Hz display.
React Profiler measures React work; it does not establish paint, raster, or presentation cost.
Use frame gaps, browser traces, queue pressure, and device observations together.
Sources: [Long Animation Frames](https://www.w3.org/TR/long-animation-frames/)
and [React Profiler](https://react.dev/reference/react/Profiler).

The desktop Chromium fence test proves fixture correctness on this machine.
It does not prove Safari, Android WebView, mobile power consumption, or thermal behavior.
Reuse the existing frontend device lab before introducing another performance harness.

Vitest 5 changed project inheritance and benchmark configuration.
The lab repair removes duplicate plugin application and obsolete transform options.
Migration changes require contract checks alongside throughput measurements.
Source: [Vitest migration guide](https://vitest.dev/guide/migration/).

### Database and operations

Memory fixtures cannot establish PostgreSQL performance.
Use representative service-backed tests and `EXPLAIN (ANALYZE, BUFFERS, WAL)` where appropriate.
`ANALYZE` executes the statement; capture plans in controlled fixtures.
Network delivery and application queueing need separate measurement.
Source: [PostgreSQL EXPLAIN](https://www.postgresql.org/docs/18/sql-explain.html).

Measure observability queue saturation and dropped telemetry alongside request latency.
A bounded queue protects resources, but saturation can remove the evidence needed during overload.
Source: [OpenTelemetry resiliency](https://opentelemetry.io/docs/collector/resiliency/).

## Consolidation and documentation

The initial inventory contained 1,299 tracked files.
Large local lab storage was mostly ignored dependencies and generated results.
Deleting that storage would not simplify the shipped platform.
The removed tracked screenshots occupied about 165 KiB.

A byte comparison found one exact duplicate configuration pair among non-generated source files larger than 200 bytes.
The two small Vitest configurations remain local. Extracting a shared package for them would introduce another dependency boundary.
Semantic duplication needs consumer and ownership analysis; byte equality cannot establish safe deletion.

The glossary now links canonical controls instead of maintaining a second abbreviated table.
The agent instructions no longer claim an obsolete fixed practice count.
Performance guidance now names maintenance costs and measured cache criteria instead of universal timing promises.
Historical research remains available, with corrections linked from its owning documents.

Use one canonical rule, one enforcement mapping, and linked dated evidence.
Separate shipped behavior, experiments, and proposals explicitly in future documentation updates.
Archive evidence when superseded; do not silently relabel an old machine run as current.

## Validation, limitations, and handoff

The evidence ledger (`benchmark-results/cleanup_20260922_evidence.json`)
records commands, observed failures, coverage scope, and remaining gaps.

The local Rust compiler is 1.92.0; CI specifies 1.95.0.
This pass does not claim full `make verify`, native release benchmarks, service-backed results, or consumer application compatibility.
The lab still emits Vite plugin lifecycle warnings. Investigate these separately from rendering correctness.

The scaffold update test passed with `SKIP_DEPS=true`, `GOPROXY=off`, and `GOSUMDB=off`.
Its first run stalled during npm lockfile resolution and was stopped after eight minutes.
The passing run validates synchronization and checks, not dependency installation.
The implementation now gives dependency commands explicit deadlines and tests the supported `--skip-deps` flag.

The enforcement manifest now protects the three new regression scripts.
It also corrects two pre-existing stale hashes after source review and generator/scaffold validation:
`generate_frontend_prototype_runtime.mjs` and `project_scaffold_check.sh`.

Public runtime APIs, events, database schemas, and production styling remain unchanged.
The follow-up corrects limited columnar ordering and internal audience cache key encoding.
Operational checks now reject missing evidence, duplicate controls, and attempted ceiling increases.
History summaries identify estimated TypeScript timing and include provenance metadata.

All changes are in Foundation Core or Core-owned templates.
The template agent file has `create` ownership; existing application copies are not silently replaced.
The new frontend CI lane and lab files remain Foundation-only.

The fallback for a failed benchmark is a nonzero result with retained diagnostics and an unchanged baseline.
The WebGL fixture stops polling on timeout or disposal. Production rendering fallbacks remain intact.

Next reviewer roles: a tooling reviewer validates the negative fixtures;
a rendering reviewer validates the Chromium experiment;
a performance reviewer evaluates workload boundaries, ordering fallback costs, and application rollout evidence.
