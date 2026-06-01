# Ovasabi Testing Practices

Status: v1.0
Date: 2026-05-22
Owner: Platform Architecture

## Purpose and scope

This document defines the Foundation testing standard for Go backend modules, TypeScript/React clients, Rust/WASM runtime units, PostgreSQL/Redis infrastructure, WebSocket/runtime-transport contracts, and River workers.

The standard is deliberately practical: tests must expose faults early, preserve architecture contracts, and stay cheap enough to run continuously. Coverage is required, but coverage alone is not evidence of adequacy.

Primary references used for synthesis:

- `/Users/okhai/Desktop/Software Testing and Analysis by Mauro Pezze,Michal Young (z-lib.org).pdf`
- `/Users/okhai/Desktop/testing.pdf`
- `docs/coding_practices.md`
- `docs/database_practices.md`
- `docs/tla_architecture_practices.md`
- `docs/runtime_foundation.md`

The source review used five passes over each testing document:

1. Overall test-process and quality-assurance posture.
2. Test case selection, equivalence partitioning, boundary values, and duplicates.
3. Adequacy criteria: functional, structural, data-flow, model-based, and regression coverage.
4. Test execution machinery: scaffolding, stubs, mocks, oracles, automation, and documentation.
5. Foundation-specific mapping to event lifecycles, tenant isolation, correlation IDs, runtime envelopes, workers, Postgres, Redis, WASM, frontend stores, and high-performance lanes.

## Rule levels

- `Mandatory`: required for merge unless a documented exception is approved.
- `Recommended`: strong default; deviations require rationale in PR notes.
- `Contextual`: apply when the condition is present.

## Testing vocabulary

| Term | Foundation meaning |
| :--- | :--- |
| **Test oracle** | Code or data that decides whether observed behavior is correct. Assertions, contract verifiers, invariant checks, golden frames, and query result checks are all oracles. |
| **Scaffolding** | Test-only harness code needed to isolate or drive a unit, component, service, worker, transport lane, browser view, or runtime guest. |
| **Adequacy** | Evidence that the test suite exercises relevant behavior classes, not just a raw line-coverage percentage. |
| **Functional test** | Black-box test derived from the public contract: API, event, proto, schema, command, UI workflow, or runtime unit spec. |
| **Structural test** | White-box test derived from code structure: branch, loop, error path, data-flow, race, or optimized-lane behavior. |
| **Regression test** | A durable test added or updated because a defect was found, a contract changed, or a risky interaction was identified. |
| **Contract test** | A test that verifies compatibility across a boundary: producer/consumer events, proto schemas, envelopes, DB migrations, frontend transport, worker payloads, WASM host/guest buffers, or generated scaffold behavior. |
| **Property test** | A generative test that checks an invariant over many valid and invalid generated inputs, with a recorded or fixed seed for reproduction. |
| **Acceptance mutation** | A fault-based acceptance adequacy check that mutates Gherkin example values in the JSON IR and expects generated acceptance tests to fail for meaningful changes. |

## Rules (`TE-*`)

### TE-01: Treat tests as part of the architecture contract

Level: `Mandatory`

Requirements:

1. Every changed production behavior must have a test at the same architectural level as the risk.
2. Tests must verify visible behavior, not only implementation details.
3. Boundary contracts must be tested from both sides when a producer and consumer can drift.
4. Test files are subject to the same bounded-operation, error-handling, and clarity rules as production code unless this document gives a narrower exception.

Enforcement:

- Reviewer gate on changed production code without tests or a documented exception.
- `make test`, `make lint`, and `tooling/scripts/testing_practices_check.sh`.

### TE-02: Use black-box tests before structural tests

Level: `Mandatory`

Requirements:

1. Public behavior must first be tested from the specification: command input/output, event lifecycle, API response, DB effect, UI behavior, or runtime frame.
2. Structural tests may then add branch, loop, error-path, race, and optimization coverage.
3. Do not replace a public contract test with a test that mirrors the current implementation.

Enforcement:

- Reviewer gate on tests that assert private implementation details while leaving the public behavior unverified.

### TE-03: Define an oracle for every test

Level: `Mandatory`

Requirements:

1. Every test must have explicit expected behavior.
2. Tests must not only assert "no panic" or "no error" when visible state, emitted event, response body, metric, persisted row, or terminal failure class can be checked.
3. Error tests must check error class, message shape, or sentinel wrapping where the contract promises it.
4. Worker and event tests must check terminal `:success` or `:failed` outcomes where the command lifecycle reaches them.

Enforcement:

- Review of assertions and failure checks.
- Contract test helpers for lifecycle events.

### TE-04: Select cases by equivalence classes, boundaries, and duplicates

Level: `Mandatory`

Requirements:

1. For each changed contract, identify meaningful input classes and test at least one representative per class.
2. Test minimum, maximum, empty, nil/null, single-item, many-item, and just-over-limit cases where the domain has boundaries.
3. Test duplicate, replay, aliasing, and repeated-call behavior when the system accepts identifiers, slices, maps, object references, idempotency keys, jobs, messages, files, or external callbacks.
4. Boundary tests must include both accepted and rejected values.

Enforcement:

- Reviewer gate for validators, parsers, pagination, retry, rate-limit, queue, migration, and runtime-buffer changes.

### TE-05: Cross-product only where interactions matter

Level: `Recommended`

Requirements:

1. Combine partitions when behavior depends on interaction between variables.
2. Use pairwise/combinatorial cases for config matrices, feature flags, transport lanes, auth modes, queue options, DB drivers, Redis drivers, and browser/native targets.
3. Avoid exhaustive cross-products when independent factors can be tested separately.

Enforcement:

- Test plan review for feature flags, multi-lane runtime code, and infra matrix changes.

### TE-06: Coverage is a floor, not proof

Level: `Mandatory`

Requirements:

1. New or changed production code targets at least 95% line coverage.
2. Coverage reports must not be used as the only adequacy argument.
3. Missing coverage in error handling, tenant denial, retry exhaustion, cancellation, and timeout paths is a correctness gap.
4. Coverage drops in touched legacy modules require an approved exception.

Enforcement:

- `scripts/coverage-go.sh` and package-level TypeScript/Rust coverage where configured.
- Reviewer gate on untested critical branches.

### TE-07: Test loops and retries at zero, one, two, many, and exhausted

Level: `Mandatory`

Requirements:

1. Loop behavior must cover zero iterations, one iteration, multiple iterations, and the configured maximum where practical.
2. Retry behavior must cover immediate success, success-after-retry, permanent failure, cancellation, timeout, and max-attempt exhaustion.
3. Tests must not rely on unbounded sleeps to observe eventual behavior.

Enforcement:

- Static check for extreme sleeps in test files.
- Reviewer gate for retry, worker, poller, reconnect, and backoff changes.

### TE-08: Every mutating test command carries correlation and idempotency evidence

Level: `Mandatory`

Requirements:

1. Tests for commands, events, workers, and transport frames must set or verify `correlationId`.
2. Retryable mutation tests must include idempotency keys or deterministic duplicate prevention.
3. Tests must verify correlation propagation into emitted events, worker metadata, logs, traces, or response envelopes when that surface is available.

Enforcement:

- Contract tests for lifecycle envelopes.
- Reviewer gate on mutating command tests.

### TE-09: Tenant isolation tests must be negative as well as positive

Level: `Mandatory`

Requirements:

1. Tests for org-scoped reads and writes must include same-tenant success and cross-tenant denial/not-found behavior.
2. Tests must derive organization identity through authenticated context or server-side metadata helpers.
3. Tests must not normalize a client-supplied `organization_id` into trusted server identity.

Enforcement:

- Integration tests for repositories, handlers, workers, and policies.
- Security review for auth and object-access changes.

### TE-10: Event lifecycle tests are required for domain flows

Level: `Mandatory`

Requirements:

1. Domain command flows must test `<domain>:<action>:requested`, `:success`, and `:failed` where the service owns the lifecycle.
2. Tests must verify event type, version when present, payload schema, correlation ID, idempotency identity, organization scope, and failure reason shape.
3. Consumers must reject malformed, unknown, wrong-version, and cross-tenant events deterministically.

Enforcement:

- `contracttest` helpers and generated lifecycle tests.
- `tooling/scripts/generate_lifecycle_contract_tests.mjs --check`.

### TE-11: Runtime envelope and binary-frame parity must be tested

Level: `Mandatory`

Requirements:

1. HTTP, WebSocket, Redis, gRPC, direct frame, WASM/native, and GPU/WebGPU lanes must preserve canonical metadata and payload semantics.
2. Optimized lanes must have parity/refinement tests against the canonical behavior.
3. Compatibility JSON lanes must not become the only tested path for performance-sensitive communication.
4. GPU/WebGPU parity tests must include buffer layout, fallback behavior, device/capability absence, boundary sizes, and deterministic tolerance for numeric output.
5. Native GPU tests must check asynchronous error surfaces: launch-time status, synchronization-time status, stream/event status, device assertion behavior, and fallback replay after a failed dispatch.
6. GPU numeric tests must name tolerance and ULP budgets, then cover fused multiply-add drift, reduction-order changes, NaN/Inf behavior, subnormal handling, and host/device accuracy differences.
7. GPU edge tests must include default-stream or implicit-queue serialization, graph capture invalidation, unsupported operations, memory-pool reuse, page migration or oversubscription where supported, and sanitizer/debug-tool gaps.
8. Interactive rendering, media, canvas, WebGPU, and native preview tests must
   include a frame-budget case, a first-use cold-start case, a warmed steady
   state case, and a reduced-capability fallback case.
9. Capture-backed tests must preserve enough metadata to replay or compare the
   run: build SHA, browser/runtime, driver version, GPU/CPU model, quality
   profile, feature flags, input seed, async overlap state, and capture tool.

Enforcement:

- Runtime transport and server-kit contract tests.
- Reviewer gate for transport/router/runtime changes.
- GPU adapter changes must link to parity, edge-case, and benchmark evidence from `docs/gpu_practices.md`.
- Interactive GPU/media/rendering changes must link to `docs/game_runtime_practices.md` evidence when user-visible hitches or visual regressions are possible.

### TE-12: Database tests must prove constraints, not only app prechecks

Level: `Mandatory`

Requirements:

1. Repository tests must verify unique constraints, tenant predicates, row-count semantics, transaction rollback, and not-found/conflict behavior.
2. Migration tests must verify schema shape, indexes, seed idempotency, down migrations, and Foundation state-store availability.
3. Tests for security-critical uniqueness must include concurrent or repeated attempts.
4. Tests must execute queries under bounded context budgets.

Enforcement:

- Integration tests against Postgres for migration and repository contracts.
- `database_practices_check.sh` and `migration_structure_check.sh`.

### TE-13: Redis and cache tests must distinguish ephemeral state from truth

Level: `Mandatory`

Requirements:

1. Cache tests must verify miss, hit, stale/expired, invalidated, and source-of-truth fallback behavior.
2. Redis lock tests must verify token ownership, TTL expiry, unlock failure for wrong token, and bounded wait.
3. Pub/sub and stream tests must verify delivery, ack, redelivery, lag, cancellation, and duplicate handling where used.

Enforcement:

- Memory and service-backed Redis tests.
- Reviewer gate for cache, lock, pub/sub, and stream changes.

### TE-14: Worker tests must prove idempotency and bounded progress

Level: `Mandatory`

Requirements:

1. Worker tests must cover enqueue, execution, retry, terminal success, terminal failure, cancellation, and metadata persistence.
2. Job handlers must be idempotent under duplicate delivery and retry.
3. Queue concurrency, max attempts, backoff, and timeout behavior must be tested when changed.
4. Worker tests must verify correlation and tenant metadata propagation.

Enforcement:

- Unit tests with in-memory stores.
- Integration tests for River-backed critical workers.

### TE-15: Frontend tests must exercise user-visible behavior and transport state

Level: `Mandatory`

Requirements:

1. React tests must prefer semantic queries and user events over implementation selectors.
2. Store tests must verify transport envelopes, deduplication, error states, offline queue behavior, and subscription cleanup.
3. UI tests must include loading, empty, success, error, permission-denied, and reconnect states when the component can render them.
4. Tests must not import raw `foundation/*/ts/src` internals from generated projects.

Enforcement:

- ESLint, Vitest, Testing Library, and scaffold checks.

### TE-16: WASM/Rust runtime tests must prove host/guest contract safety

Level: `Mandatory`

Requirements:

1. Runtime tests must verify 4KB control-buffer layout, payload routing, bounds checks, deterministic errors, and fallback behavior.
2. Rust tests must reject `unwrap`, `expect`, `todo!`, and panic-driven correctness in runtime paths.
3. Host tests must verify unsupported shared-memory or FFI lanes degrade predictably.
4. Financial or scoring kernels must test integer/checked arithmetic, overflow rejection, and deterministic serialization.
5. Frame/buffer tests must include empty, exact-limit, just-over-limit, malformed length, negative declared length, timeout, and panic-conversion cases.

Enforcement:

- `make check-rust`, Go runtimehost tests, TypeScript browser-host tests.

### TE-17: Concurrency tests must make ownership and termination observable

Level: `Mandatory`

Requirements:

1. Tests for goroutines, channels, timers, tickers, queues, sockets, workers, and subscriptions must verify cancellation and cleanup.
2. Race-prone code must have `go test -race` coverage where feasible.
3. Concurrency tests must use bounded contexts, channels, fake clocks, or polling helpers instead of long fixed sleeps.
4. Tests must verify that senders, receivers, closers, and cancellers have clear ownership.

Enforcement:

- `go_concurrency_practices_check.sh`.
- Race test lane for concurrency-sensitive packages.

### TE-18: Performance tests must separate hard bounds from statistical targets

Level: `Mandatory`

Requirements:

1. Hard correctness bounds such as max attempts, queue capacity, acquire timeout, payload size, and page limit must be asserted as behavior.
2. Statistical targets such as p95/p99 latency, RPS, heap, CPU, and allocation counts belong in benchmarks/load tests.
3. Benchmarks must include representative fixtures and must not silently skip the hot path they claim to measure.
4. Load tests must define request mix, duration, concurrency, think time, error budget, and pass/fail threshold.

Enforcement:

- `foundation_benchmarks.md`, `performance_check.sh`, and load-test targets.

### TE-19: Security tests must target trust boundaries

Level: `Mandatory`

Requirements:

1. Tests must cover unauthenticated, unauthorized, wrong-org, malformed, replayed, oversized, and expired inputs at exposed boundaries.
2. Authorization must be tested on the target object or aggregate, not only the route.
3. Tests must verify secrets are redacted from logs, errors, traces, and frontend state.
4. Rate limiting, CORS, CSRF/session, signed URL, upload, and webhook surfaces require negative tests when touched.

Enforcement:

- Security middleware, policy, auth, objectstore, and frontend guard tests.

### TE-20: Regression tests are mandatory for repaired defects

Level: `Mandatory`

Requirements:

1. Every bug fix must include a regression test that fails before the fix unless an exception is documented.
2. The regression test should encode the smallest stable reproduction at the correct boundary.
3. Do not delete regression tests because implementation changed; rewrite them at the preserved contract level.

Enforcement:

- Reviewer gate on bug-fix PRs.

### TE-21: Integration tests must own their environment

Level: `Mandatory`

Requirements:

1. Integration tests must declare required services and ports through test config, not hidden developer machine state.
2. Setup and teardown must be deterministic and idempotent.
3. Tests must isolate DB schemas, Redis keys, queues, object-store keys, tenants, and correlation IDs.
4. External network dependencies must be faked, recorded with approval, or gated behind explicit opt-in.

Enforcement:

- `make docker-up`, `make test-env-up`, test fixtures, and service-backed test helpers.

### TE-22: Stubs, mocks, and fakes must preserve the contract they replace

Level: `Mandatory`

Requirements:

1. Fakes must implement failure, cancellation, timeout, duplicate, and boundary behavior needed by the tested code.
2. Mocks must not assert incidental call order unless order is the contract.
3. Shared fakes for Foundation modules must be contract-tested against the real implementation when feasible.
4. Test doubles must not create behavior that production cannot exhibit.

Enforcement:

- Contract tests for MemoryDB/Postgres, memory/Redis, transport adapters, and worker stores.

### TE-23: Test data must be explicit, minimal, and domain-shaped

Level: `Recommended`

Requirements:

1. Fixtures should use meaningful IDs such as `org_1`, `corr_1`, `idem_1`, and domain-specific refs.
2. Avoid anonymous blobs unless the test is specifically about binary payload shape.
3. Use builders for repeated domain setup, but keep expected values visible in each test.
4. Golden files must be stable, small, and reviewed as contract artifacts.

Enforcement:

- Reviewer gate on opaque fixtures and broad snapshots.

### TE-24: Tests must not hide failures behind broad skips

Level: `Mandatory`

Requirements:

1. Skips must state the missing capability or explicit opt-in variable.
2. Required CI tests must not skip because a local service is absent unless that lane is explicitly optional.
3. Long, load, destructive, or external tests must use explicit `RUN_*` gates.

Enforcement:

- Reviewer gate on `t.Skip`, `test.skip`, and environment-gated tests.

### TE-25: Generated contracts must be checked for drift

Level: `Mandatory`

Requirements:

1. Generated proto, Cap'n Proto, lifecycle, runtime, and frontend manifest artifacts must be either committed or verified stale-free by CI.
2. Contract generators must support `--check` or equivalent dry-run comparison.
3. Tests must fail when generated producer/consumer contracts are missing or stale.

Enforcement:

- `contract_drift_check.sh`.
- `generate_lifecycle_contract_tests.mjs --check`.

### TE-26: Test documentation must explain risk, not restate code

Level: `Recommended`

Requirements:

1. Complex tests should briefly name the invariant, bug class, or boundary being protected.
2. PR notes should list intentionally untested risk when coverage is incomplete.
3. Test names should read as behavior claims.

Enforcement:

- Review of test names and comments.

### TE-27: Test files must stay deterministic

Level: `Mandatory`

Requirements:

1. Avoid wall-clock, random, unordered map, network, filesystem, and scheduler assumptions unless explicitly controlled.
2. Random/property tests must log or fix seeds on failure.
3. Time-dependent tests should use fake clocks or bounded polling helpers where available.
4. Tests must be safe to run in parallel unless they explicitly own isolated global resources.

Enforcement:

- Static checks for focused tests and extreme sleeps.
- Reviewer gate for nondeterministic tests.

### TE-28: Acceptance and E2E tests must cover core journeys

Level: `Mandatory`

Requirements:

1. Auth guards and core user journeys require E2E coverage.
2. E2E tests must verify visible UI behavior plus the backend state or event effect when feasible.
3. E2E tests should avoid brittle visual-only assertions unless the change is visual.
4. Browser tests must run against a known local server or file target and capture diagnostics on failure.

Enforcement:

- Frontend and E2E CI lanes.

### TE-29: Model-based tests are required for stateful protocols

Level: `Contextual`

Requirements:

1. Stateful flows such as workers, retries, websocket reconnects, offline queues, runtime lane selection, and lifecycle events should have transition-model tests.
2. Tests must cover illegal transitions and terminal states.
3. Optimized implementations must refine the same model.

Enforcement:

- Reviewer gate for protocol/state-machine changes.
- `tla_architecture_practices.md` for high-risk models.

### TE-30: Fault-based tests must target likely Foundation bug classes

Level: `Contextual`

Requirements:

1. Tests should inject malformed envelopes, missing metadata, wrong tenant, duplicate events, stale cache, Redis timeout, DB conflict, serialization failure, worker retry, and cancelled contexts.
2. Mutation-style thinking should be used for validators and security checks: each important guard should have at least one test that fails when the guard is removed.
3. Chaos/fault injection belongs behind bounded and repeatable test harnesses.

Enforcement:

- Review of critical validation and resilience changes.

### TE-31: Use property tests for invariant-heavy code

Level: `Contextual`

Requirements:

1. Property tests are expected for parsers, serializers, validators, canonicalizers, permission predicates, routing/deduplication logic, idempotency keys, retry/backoff math, pagination cursors, money/count arithmetic, state transitions, and runtime buffer/frame transforms when the behavior has a stable invariant.
2. Each property test must name the invariant and constrain the generator domain to realistic Foundation inputs: bounded sizes, valid tenant/correlation shapes, explicit invalid classes, and edge values near limits.
3. Property tests must include negative domains when the contract rejects malformed, cross-tenant, oversized, stale, replayed, missing, or semantically invalid values.
4. Generated examples must be bounded. Do not use unbounded recursion, unbounded collection sizes, wall-clock randomness, or external services inside a property.
5. Failures must be reproducible by logging or fixing the seed and by printing the shrunk counterexample when the framework supports shrinking.
6. Property tests complement table tests. Keep small table tests for named boundaries and regressions, then use property tests to explore the surrounding space.

Enforcement:

- Reviewer gate for invariant-heavy changes that only test hand-picked happy paths.
- Static check for uncontrolled randomness in test files.

### TE-32: Acceptance mutation hardens generated acceptance tests

Level: `Contextual`

Requirements:

1. Projects that generate acceptance tests from Gherkin or a JSON acceptance IR must provide a normal acceptance command that parses the feature, writes the JSON IR, generates executable tests from that IR, and runs the project test runner.
2. Those projects must also provide an explicit acceptance mutation command that mutates only example-cell values in the IR, regenerates tests per mutation, runs them, and reports `killed`, `survived`, and `error` outcomes.
3. The mutator must be deterministic: stable mutation IDs, stable paths, bounded workers, bounded timeout, and no in-place mutation of the base IR.
4. Survived mutations are test-quality failures unless a documented equivalent-mutation filter explains why the change is semantically identical for the project.
5. Differential acceptance mutation may reuse previous clean scenario results only when feature identity, scenario content, background content, and the selected implementation-hash policy prove reuse is valid.
6. Normal acceptance belongs in regular verification. Acceptance mutation belongs in an explicit quality target because it may be slower.

Enforcement:

- Reviewer gate for generated acceptance-test pipelines.
- Static check for scaffold or project targets/scripts when `features/*.feature` files are present.

### TE-33: Test suites must be organized by speed and dependency

Level: `Mandatory`

Requirements:

1. Unit tests must run without Docker or network dependencies.
2. Integration tests may depend on local Postgres/Redis/object-store services and must be under explicit targets.
3. Load and benchmark tests must be separate from default unit tests unless intentionally lightweight.
4. CI should run fast tests first, then integration/load/benchmark lanes.

Enforcement:

- `make test-unit`, `make test-integration`, `make test-frontend`, `make test-load`, `make test-bench`, and `make test-all`.

### TE-34: Test failures must preserve diagnostics

Level: `Mandatory`

Requirements:

1. Test failures must identify the operation, record, stage, event type, correlation ID, organization ID, or fixture where practical.
2. Batch tests must report per-record failures rather than collapsing all diagnostics into one boolean.
3. Load tests must emit request counts, error rates, latency summaries, and first failure samples.

Enforcement:

- Reviewer gate on low-signal assertions and swallowed errors.

### TE-35: Test automation must be reproducible locally

Level: `Mandatory`

Requirements:

1. Every CI test/lint lane must have a local make target or script.
2. Scripts must use explicit roots and avoid hidden global state.
3. Test scripts must be copied into generated projects through scaffold sync.

Enforcement:

- Makefile targets and scaffold manifest checks.

### TE-36: Testing checks are linted as part of Foundation

Level: `Mandatory`

Requirements:

1. `tooling/scripts/testing_practices_check.sh` must run from `make lint`.
2. Generated projects must receive the same script as `scripts/checks/testing_practices_check.sh`.
3. The check must remain conservative: fail on high-confidence test hazards and leave nuanced adequacy judgments to review.

Enforcement:

- `make lint`.
- Scaffold sync.

### TE-37: Update this document when test strategy changes

Level: `Mandatory`

Requirements:

1. Add or revise `TE-*` rules when the Foundation introduces a new runtime lane, storage adapter, protocol boundary, test target, or defect class.
2. Update checks and templates in the same change when a rule becomes mechanically enforceable.
3. Record intentionally deferred automation as a test gap.

Enforcement:

- Reviewer gate for new testing infrastructure and critical defect postmortems.

### TE-38: Service-backed pressure tests prove substrate claims

Level: `Mandatory`

Requirements:

1. Claims about Postgres pool behavior must include live pgxpool saturation tests that prove acquire timeouts are bounded and observable.
2. Claims about Redis coordination must include live Redis tests for stream lag, pending windows, pub/sub slow-consumer behavior, and multi-key batching.
3. Claims about Hermes as a hot-plane must include live Postgres/Redis tests for rebuild latency, Redis stream tailing, drift checks, and hot indexed reads.
4. Mixed workflow tests must report p95/p99 latency across Postgres, Redis, and Hermes together, not only isolated microbenchmarks.
5. Service-backed tests must remain Foundation-only. Generated projects inherit the APIs and checks, not the Foundation benchmark harness.
6. Service-backed Docker ports should be ephemeral by default and discovered at runtime. Fixed host ports are allowed only through explicit override environment variables.
7. Generated app integration tests must write Docker-assigned test ports into a runtime env file and feed those resolved URLs into Go tests. This keeps manual parallel app runs from fighting over `5433`/`6380`.
8. Catalogue-wide app test runners must use isolated Compose project names per app/run so parallel execution does not share containers, networks, volumes, or runtime env files.

Enforcement:

- `make test-service-backed`.
- `tests/service_backed_foundation_test.sh`.
- `server-kit/go/servicebacked` with the `servicebacked` build tag.

### TE-39: Scaffold smoke belongs in verify, not fast lint

Level: `Mandatory`

Requirements:

1. `make lint` must remain structural and fast enough for frequent local use.
2. `make verify` and CI must run stronger generated-scaffold smoke checks.
3. Generated-scaffold smoke must compile the Go WASM shim when present.
4. CI must install generated frontend dependencies and run generated frontend build/test when a frontend exists.
5. Local scaffold smoke may skip frontend install/build/test unless dependencies already exist or the caller opts in.

Enforcement:

- `make verify`.
- `tests/scaffold_smoke_test.sh`.
- Generated CI workflow frontend/WASM steps.

### TE-40: Benchmark and latency statistics must be sound

Level: `Mandatory`

Requirements:

1. Percentile calculations must use one conservative rule across Foundation:
   nearest-rank over a sorted sample set, clamped to the valid sample range.
   Do not mix floor-based, average-based, and nearest-rank formulas across
   packages.
2. p95/p99 metrics require enough samples to mean something. Short smoke
   benchmark lanes may use bounded iteration counts for speed, but percentile
   lanes must run with a duration or sample count large enough to avoid one
   scheduler hiccup dominating the result.
3. Benchmarks must separate fixture construction from the measured hot path
   unless the benchmark name explicitly claims to measure construction,
   parsing, ingress allocation, or cold-start setup.
4. Benchmarks that report p95/p99 should also preserve enough diagnostics to
   interpret variance: at minimum the workload shape, sample count or
   benchmark duration, allocation count, and max observed latency where the
   harness supports it.
5. Benchmark-history tooling must not silently drop a runtime lane. Go, Rust,
   TypeScript, browser-host, native, and service-backed results should either
   appear in the saved artifact or produce an explicit skip reason.
6. A benchmark result is evidence for the lane it measures, not for adjacent
   lanes. Local memory benchmarks do not prove Postgres/Redis behavior;
   service-backed benchmarks do not prove browser slow-consumer behavior;
   fake GPU dispatch does not prove physical device execution.
7. When variance appears, rerun with `-count`, increase sample duration, and
   inspect fixture allocation, timer placement, GC pressure, scheduler
   pressure, lock contention, and hidden network or filesystem work before
   calling it noise.

Enforcement:

- `tooling/scripts/performance_check.sh`.
- `tooling/scripts/benchmark_history.sh`.
- Package-level percentile helper tests.
- Reviewer gate for benchmark or load-test changes.

## Foundation test checks

The conservative automated check set is:

1. No focused TypeScript tests: `.only(`, `describe.only`, `it.only`, or `test.only`.
2. No disabled TypeScript tests without review: `describe.skip`, `it.skip`, or `test.skip`.
3. No extreme fixed sleeps in Go tests: `time.Sleep` with seconds or minutes outside load tests.
4. No long fixed waits in TypeScript tests: `setTimeout`/promise sleeps above one second outside explicit E2E/load paths.
5. Domain lifecycle tests must exist when generated lifecycle contract tests are present.
6. Frontend scaffold tests must keep Testing Library, jsdom, and user-event dependencies.
7. Frontend scaffold tests must include a property-testing library.
8. Tests must avoid uncontrolled random sources that cannot reproduce failures.
9. Acceptance feature files must have normal acceptance and acceptance-mutation script or target entry points.
10. Generated project lint must run the testing-practices check.
11. Benchmark and load-test helpers must use conservative percentile math and
    must not report p95/p99 from intentionally tiny smoke samples.

These checks intentionally do not try to infer whether every test has a strong oracle or adequate partitions. Those are review obligations under `TE-02` through `TE-06`.

## PR testing checklist

- [ ] Changed behavior has functional tests from the public contract.
- [ ] Structural/error tests cover branch, timeout, cancellation, retry, and failure paths that matter.
- [ ] Boundary, empty, duplicate/replay, and just-over-limit cases are included.
- [ ] Mutating flows verify correlation ID, idempotency, and terminal lifecycle events.
- [ ] Tenant-scoped flows include cross-tenant negative tests.
- [ ] DB changes include migration, constraint, rollback, and query-budget evidence.
- [ ] Redis/cache/queue changes include expiry, duplicate, cancellation, and failure tests.
- [ ] Frontend changes include loading, empty, success, error, permission, and reconnect states where applicable.
- [ ] Runtime/WASM/native changes include parity/refinement tests across lanes.
- [ ] Bug fixes include regression tests.
- [ ] Invariant-heavy logic has property tests with bounded generators and reproducible seeds.
- [ ] Generated acceptance pipelines include acceptance mutation, or the absence is documented as a test gap.
- [ ] Long, service-backed, load, or external tests are under explicit targets/gates.
- [ ] Benchmarks separate fixture setup from measured hot paths, use enough
      samples for p95/p99, and preserve variance diagnostics.
