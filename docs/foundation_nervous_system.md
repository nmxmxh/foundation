# Foundation Nervous System

Status: 0.0.1  
Date: 2026-06-28  
Owner: Platform Architecture

## Purpose & The "Why"

The **Nervous System** is the unified lifecycle of a request as it travels across networks, databases, workers, and UI threads. Instead of developers inventing different ways to process commands, update state, and push events, every action follows a single, predictable lane.

### Why this lifecycle exists:
- **Tracing & Debugging**: With standard correlation IDs carried through every thread and network call, we can trace a user's action from a browser click down to Go workers, database transactions, and final UI rendering.
- **Data Safety (Tenant Isolation)**: The backend derives organization and user identities from authenticated session contexts, ensuring tenant data is never leaked across commands.
- **Fault Tolerance**: The system decouples processing: the client initiates a request, it is parsed and validated, then handed to worker queues so slow database operations or network glitches do not block the user interface.

This document defines the visible lifecycle every generated app inherits and every optimized performance lane refines.

Related docs:

- `docs/foundation_architecture_contract.md`
- `docs/runtime_foundation.md`
- `docs/tla_architecture_practices.md`
- `docs/coding_practices.md`
- `docs/foundation_benchmarks.md`
- `docs/redis_practices.md`
- `docs/websocket_scaling.md`
- `docs/transfer_lane.md`
- `docs/frontend_command_registry.md`
- `docs/foundation_glossary.md`

## Canonical Lifecycle

```text
client command
-> RuntimeEnvelope
-> auth, tenant, correlation, idempotency validation
-> domain command handler
-> <domain>:<action>:vN:requested
-> optional worker/job/cache/Redis coordination
-> <domain>:<action>:vN:success or :failed
-> realtime projection/store update
-> frontend state
```

The lifecycle is intentionally boring. It should be generated, tested, and observable by default.

## Required Invariants

1. `MetadataPreserved`: correlation ID, request ID, idempotency key, user, session, organization, schema version, locale, and trace fields survive every lane that handles the command.
2. `TenantScopeStable`: organization scope is derived from authenticated context and does not change between request, job, terminal event, and realtime projection.
3. `RequestedBeforeTerminal`: a mutating command has a requested event before success or failed is visible.
4. `ExactlyOneTerminalVisible`: an accepted command exposes one semantic terminal state to clients/operators. Duplicate terminal observations must share idempotency identity.
5. `IdempotentRetry`: retries and duplicate deliveries do not duplicate durable side effects.
6. `BoundedWork`: retries, queue depth, Redis waits, request handling, and worker execution all have finite caps or deadlines.
7. `FallbackRefinement`: direct, binary, WebSocket, HTTP, Redis, worker, WASM, FFI, SHM, and JSON compatibility lanes preserve the same command semantics.
8. `ProjectionAfterTerminal`: frontend-visible projection changes are causally tied to success/failed events or an explicitly documented optimistic UI state.

## Generated Contract Defaults

Generated mutating commands should include:

1. `correlation_id`
2. `idempotency_key`
3. trusted user/session/organization metadata from auth context
4. request deadline or timeout budget
5. bounded retry policy
6. typed request/response schema
7. generated event names for `requested`, `success`, and `failed`
8. event contract tests
9. tracing span and metric names derived from the contract

Generated read commands should include correlation metadata and identity-safe cache keys. They should opt into dedupe/coalescing only when the request is replay-safe and the cache key excludes volatile metadata.

`tooling/scripts/generate_lifecycle_manifest.mjs` is the machine-readable
source pass for this rule. It scans `api/protos`, derives mutating command
lifecycles from request/response message pairs, and emits
`docs/references/lifecycle/lifecycle_contract.json` plus a generated guide. The
manifest is deterministic and carries event names, worker job kinds, queues,
invariants, and agent review vectors.

`tooling/scripts/generate_lifecycle_contract_tests.mjs` is the test compiler
pass. It emits `tests/contract/generated_lifecycle_test.go`; the generated cases
call `server-kit/go/contracttest.VerifyCommandLifecycle` for both `:success`
and `:failed` terminal refinements.

The generator intentionally uses the scaffold example proto as the reference shape:

1. shared `foundation.v1.Metadata metadata = 1`
2. mutating pairs such as `CreateExampleRequest` and `CreateExampleResponse`
3. event names derived as `example:create:v1:requested`, `example:create:v1:success`, and `example:create:v1:failed`
4. worker metadata vectors that preserve correlation, idempotency, and organization scope

This is a contract-vector test, not a replacement for implementation tests. Domain integration tests should capture real envelopes/jobs from handlers and pass those observations to `VerifyCommandLifecycle`.

`server-kit/go/contracttest.LifecycleRecorder` is the implementation-test harness for that second step. It can wrap an `events.Bus`, record worker jobs, and build a `LifecycleObservation` from observed `requested` and terminal event types. Generated tests provide `verifyGeneratedLifecycleObservation` so app-owned tests can reuse the proto-derived contract names while checking real handler output.

## Hot-Path Routing Rules

1. Same-process domain calls use direct typed or direct frame dispatch.
2. Cross-host calls use generated protobuf or binary frames.
3. Redis pub/sub is transient notification and coordination; Redis Streams are for ephemeral or coordination-heavy at-least-once lanes, not durable business truth.
4. Worker queues carry the same correlation and idempotency metadata as the command that created them.
5. WebSocket fanout uses exact topics or indexed colon-prefix topics for product traffic. Complex wildcard scans stay in compatibility and observability paths.
6. Frontend stores should suppress no-op updates when envelope version, payload hash, or projection revision has not changed.

## Introspection Surface

The substrate should expose these views locally before production dashboards exist:

1. Correlation trace: all envelopes, jobs, cache/Redis actions, and WebSocket sends for one correlation ID.
2. Event lifecycle: requested -> success/failed timing, payload sizes, and terminal error class.
3. Tenant audit: organization/user/session observed at each boundary.
4. Worker timeline: enqueue, attempt, retry, dead-letter/quarantine, terminal event.
5. Realtime inspector: topic resolution, subscriber count, fanout count, slow-client drops.
6. Contract coverage: generated events with producer/consumer/lifecycle tests.

`server-kit/go/observability.Collector` provides the initial bounded trace substrate through `RecordTrace` and `Trace`. It is intentionally local and ring-like: it proves the shape for debug endpoints and future UI without creating an unbounded production event store.

The scaffold exposes this local surface at `/metricsz/trace?correlation_id=<id>`. It also records event publish/receive, worker enqueue/process, Redis operation latency, database operation latency, DB pool pressure, and queue depth in the same bounded collector. Service-backed benchmark processes stay outside the scaffold; generated projects inherit the minimal observability surface and runtime budgets, not benchmark orchestration.

Production scaffolds must protect `/metricsz`, `/metricsz/trace`, and operational event views behind authenticated operator/admin access. Development can keep the local endpoints open, but the generated config defaults to protected operational endpoints when `APP_ENV=production`.

## Machine-Readable Lifecycle Source

Agents and generators must not invent lifecycle event names from memory. The
contract source order is:

1. `docs/references/lifecycle/lifecycle_contract.json`
2. `docs/references/lifecycle/lifecycle_contract_guide.md`
3. proto request/response definitions under `api/protos`
4. generated lifecycle tests under `tests/contract/`

When a new mutating proto command is added, run:

```bash
make lifecycle-manifest
make lifecycle-contracts
```

Then use the manifest review vectors as the implementation checklist for tenant
isolation, correlation propagation, idempotency, requested-before-terminal
ordering, bounded work, and failure fallback.

## Delivery Metrics

Foundation runtime telemetry does not replace delivery telemetry. Generated projects inherit a lightweight CI event collector for DORA-ready signals:

1. change lead time
2. deployment frequency
3. failed deployment recovery time
4. change fail rate
5. deployment rework rate

The collector writes CI artifacts only. App deployment platforms decide where to aggregate them and how to join them with incident records.

## Verification Mapping

Use lightweight TLA-style notes for high-risk changes, then map each property to tests:

| Property | Test or benchmark |
| --- | --- |
| Metadata and tenant preservation | `server-kit/go/contracttest` lifecycle checks |
| Event naming and terminal state | `server-kit/go/events` contract tests |
| Queue bounds and retry caps | `server-kit/go/worker` tests and appbench queue saturation |
| Redis coordination semantics | `server-kit/go/redis` memory-driver tests plus service-backed Redis load tests |
| Exact/prefix fanout shape | `server-kit/go/events`, `wsrouting`, and `appbench` scale tests |
| Binary/direct lane refinement | `grpcsvc`, `runtime-transport`, and `runtime-sdk` parity tests |
| Statistical performance | `docs/foundation_benchmarks.md` reference runs |

## Minimal Roadmap

1. Keep this lifecycle as the scaffold’s one official substrate contract.
2. Generate app-domain test skeletons that wire real service handlers into `LifecycleRecorder`.
3. Add frontend transport/store no-op suppression around stable semantic cache keys and projection revisions.
4. Add service-backed Redis/Postgres/WebSocket load tests after local proof harnesses pass, but keep their configs and processes foundation-only.
