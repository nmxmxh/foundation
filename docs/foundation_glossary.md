# Foundation Glossary & Agent Q&A Reference

Status: 0.0.1
Date: 2026-06-28
Owner: Platform Architecture

This is the lookup companion for every Foundation document. Use it to find
concept definitions, module purposes, invariant summaries, practice IDs, and
pre-answered questions without scanning multiple files.

---

## Part 1: Concept Glossary

| Term | Definition | Owning Doc |
| :--- | :--- | :--- |
| **appkit** | Planned `server-kit/go/appkit` package that extracts foundation-owned runtime wiring out of project-owned composition roots, preventing drift. | `foundation_project_standardization.md` |
| **Bounded work** | The invariant that all loops, retries, queue depths, Redis waits, request handling, and worker execution have finite caps or deadlines (CP-02). | `coding_practices.md` |
| **Bulk** | `server-kit/go/bulk` — bounded large-payload and resumable multipart upload helpers with idempotent part replay. | `transfer_lane.md` |
| **Cap'n Proto** | Zero-copy serialization format used for runtime contracts: SAB layouts, syscalls, compute capsules, and chunk descriptors. | `runtime_sab_capnp_contracts.md` |
| **Capability planner** | Runtime decision layer that selects an eligible execution lane from workload shape, trust, locality, platform capability, deadline, budgets, and fallback requirements while preserving the visible contract. | `runtime_foundation.md` |
| **Chain** | `server-kit/go/chain` — worker chain helpers for bounded multi-step job composition. | `coding_practices.md` |
| **Circuit breaker** | `server-kit/go/circuitbreaker` — fault tolerance pattern that stops calling a failing dependency after a threshold, auto-recovering after a timeout. | `foundation_guide.md` |
| **Command bus** | `createCommandBus` in `@ovasabi/runtime-transport` — resolves an event type to a route and dispatches it over HTTP or WebSocket. | `frontend_command_registry.md` |
| **Config-contracts** | Cross-language configuration schemas shared between frontend and backend, ensuring validated parity. | `foundation_architecture_contract.md` |
| **Correlation ID** | A unique identifier carried through every mutating command, all workers, events, logs, and traces. Required by the nervous system lifecycle. | `foundation_nervous_system.md` |
| **Create mode** | Scaffold manifest mode: file is seeded once during project generation, then project-owned. Existing files are preserved during updates. | `scaffold_manifest.md` |
| **Degradation** | `server-kit/go/degradation` — health monitoring with automatic state transitions (normal → degraded → unavailable) and configurable fallback. | `foundation_guide.md` |
| **Domain service** | Application-owned business logic under `internal/service/<domain>`. Foundation provides execution lanes; the domain owns meaning. | `foundation_architecture_contract.md` |
| **Envelope** | `RuntimeEnvelope` — the universal message wrapper carrying identity, metadata, tenant scope, correlation, and payload across all transport lanes. | `foundation_nervous_system.md` |
| **Event bus** | `server-kit/go/events` — multi-driver (Redis/in-memory) pattern-matching bus for decoupled domain event communication. | `foundation_guide.md` |
| **Event lifecycle** | The `<domain>:<action>[:vN]:requested → :success / :failed` pattern. Every mutating command must produce these terminal bookend events. | `foundation_nervous_system.md` |
| **Eventlog** | `server-kit/go/eventlog` — append-only lifecycle evidence store for traces, inspection, and contract verification. | `foundation_guide.md` |
| **Extension** | `server-kit/go/extension` — typed payload wrappers for event bus payloads, supporting direct struct serialization without JSON round-trips. | `foundation_guide.md` |
| **Feature flags** | `server-kit/go/featureflags` — structured feature toggles with percentage rollouts, user/org targeting, and environment overrides. | `foundation_guide.md` |
| **Force mode** | Scaffold manifest mode: foundation-managed baseline, overwritten only when `--force` is used or the file does not exist. | `scaffold_manifest.md` |
| **Foundation Core** | This repository — the shared infrastructure containing `server-kit`, `runtime-transport`, `runtime-sdk`, and related modules. | `AGENTS.md` |
| **Foundation Profile** | Configuration level (`core`, `lite`, `performance`, `regulated`) that controls which scaffold surfaces a generated project receives. | `foundation_architecture_contract.md` |
| **Foundation Project** | A specific application generated from Foundation templates (for example, Trader, Civic, Global). | `AGENTS.md` |
| **Foundation Reference** | The `/foundation` directory inside a generated project — a local copy/reference to Core modules. Read-only. | `AGENTS.md` |
| **Foundation Template** | The skeletal structure in `templates/` used to bootstrap new projects via `init-project.sh`. | `AGENTS.md` |
| **Ovasabi CLI** | Planned distribution CLI exposed as `@ovasabi/cli`; wraps scaffold init/update, package registry setup, license verification, agent config generation, and baseline checks. | `foundation_distribution.md` |
| **Agent config bundle** | Generated developer-environment files (`AGENTS.md`, `.cursorrules`, `.clauderules`, `CLAUDE.md`, `.agents/*`) that make AI coding tools follow Foundation ownership, read-order, and evidence rules. | `foundation_distribution.md` |
| **Frontend-kit** | `@ovasabi/frontend-kit` — operational frontend utilities: IndexedDB storage, metadata, runtime artifacts, transfer progress, and store helpers. | `foundation_guide.md` |
| **Graceful signaler** | `server-kit/go/graceful` — consistently formats error and success streams into conforming envelopes, with context cancellation awareness. | `foundation_guide.md` |
| **Hermes** | `server-kit/go/hermes` — bounded, node-local projection read cache. Not the source of truth. Falls back to Postgres when stale. **CRITICAL WARNING**: Hermes is a stale-read projection and must NEVER be used for writes, transactional integrity, or financial reconciliation. | `hermes_hotplane.md` |
| **Hotplane** | The Hermes node-local projection layer: bounded memory, freshness contracts, rebuild policies, and degradation fallback. | `hermes_hotplane.md` |
| **httpapi** | `server-kit/go/httpapi` — HTTP route generation from event types, transfer routes, and the route catalog projection for frontend command generation. | `frontend_command_registry.md` |
| **Idempotency key** | Token carried on mutating commands ensuring retries and duplicate deliveries do not duplicate durable side effects. | `foundation_nervous_system.md` |
| **Intelligence** | `server-kit/go/intelligence` — registry-level signal extraction: keyword hints, graph edges, entity references, and relevance vectors. | `ai_practices.md` |
| **Kernellane** | `server-kit/go/kernellane` — native Rust/FFI/SHM compute lane dispatch with descriptor management and schema validation. | `runtime_foundation.md` |
| **Lifecycle manifest** | Machine-readable JSON at `docs/references/lifecycle/lifecycle_contract.json` derived from protobuf request/response pairs. | `foundation_nervous_system.md` |
| **Managed scaffold** | Files generated and synchronized from `templates/scaffold.manifest.tsv` — Makefile, CI, Docker, config, checks, and baseline wiring. | `foundation_architecture_contract.md` |
| **Metadata** | `server-kit/go/metadata` — context-aware request metadata: correlation ID, tenant ID, request ID, user, session, tags, locale, and trace fields. | `foundation_guide.md` |
| **Money** | `server-kit/go/money` — integer minor-unit financial arithmetic with checked operations. Floats are forbidden for ledger balances. | `coding_practices.md` |
| **Nervous system** | The canonical lifecycle path connecting contracts, metadata, dispatch, workers, Redis, WebSocket, frontend stores, and observability. | `foundation_nervous_system.md` |
| **Object store** | `server-kit/go/objectstore` — object-storage helpers with tenant-scoped key derivation, streaming put/get, and presigned URLs. | `transfer_lane.md` |
| **Overwrite mode** | Scaffold manifest mode: foundation-owned file, always synchronized during updates. | `scaffold_manifest.md` |
| **Platform module** | Shared modules versioned, tested, and rarely edited inside applications: `server-kit`, `runtime-transport`, `runtime-sdk`, and related modules. | `foundation_architecture_contract.md` |
| **Policy engine** | `server-kit/go/policy` — Cedar-inspired policy-as-code authorization with principal/action/resource matching. | `foundation_guide.md` |
| **Post-quantum** | Crypto agility posture: hybrid TLS, configurable algorithms, artifact signing for long-lived data. No PQ crypto on hot render paths. | `post_quantum_security.md` |
| **Practice controls** | Machine-readable matrix at `tooling/practice_controls.psv` mapping every CP/TE rule to enforcement, evidence, and merge-gate posture. | `practice_controls.md` |
| **Projection freshness** | Contract governing staleness modes for Hermes, read models, search, materialized views, and Redis caches. | `projection_freshness_contract.md` |
| **Projection gateway** | `server-kit/go/projectiongw` — HTTP read surface for Hermes-backed projection queries. | `foundation_project_standardization.md` |
| **Range index** | Optional Hermes tenant-scoped ordered numeric candidate index declared through `RangeIndexedFields`; accelerates bounded range predicates while retaining full-scan fallback and explicit write/memory cost. | `hermes_hotplane.md` |
| **Progressive disclosure** | Documentation and agent interface principle: expose behavior and guarantees first, contracts and domain mechanics next, architectural trade-offs next, and low-level runtime internals only when the task or decision requires them. | `PHILOSOPHY.md` |
| **Protocol Buffers** | Primary contract format for app/domain/backend business services, Hermes events, durable API messages, and generated TypeScript types. | `foundation_architecture_contract.md` |
| **Registry** | `server-kit/go/registry` — central route registration, dispatch, metrics, and queue management for HTTP and WebSocket handlers. | `foundation_guide.md` |
| **Resilience** | `server-kit/go/resilience` — coordinated dependency model: health, circuit breakers, retry, degradation, and failure-drill behavior. | `foundation_guide.md` |
| **Retry policy** | `server-kit/go/retry` — exponential backoff with jitter, max attempts, context-aware cancellation, and preset policies. | `foundation_guide.md` |
| **River** | Go-based background job queue used for durable worker execution with bounded retries and dead-letter support. | `foundation_guide.md` |
| **Route catalog** | Machine-readable JSON projection of all registered server routes, used to generate `runtimeRoutes.ts` for the frontend. | `frontend_command_registry.md` |
| **Runtime envelope** | See *Envelope* above. | `foundation_nervous_system.md` |
| **Runtime-native** | `@ovasabi/runtime-native` — Tauri-backed native shell bridge: binary frames, secure storage, capability discovery, and native dispatch. | `runtime_native.md` |
| **Runtime-sdk** | WASM/Rust/Go runtime kernel with a 4KB control-buffer contract for high-performance JS/Rust communication. | `foundation_guide.md` |
| **Runtime-transport** | `@ovasabi/runtime-transport` — universal client wire: command bus, envelope creation, WebSocket/HTTP fallback, route registry, and metadata stores. | `foundation_guide.md` |
| **Scaffold manifest** | `templates/scaffold.manifest.tsv` — the contract declaring which files are managed, their destination, profile, feature gate, and ownership mode. | `scaffold_manifest.md` |
| **Seed ledger** | `.foundation-seeds.tsv` in a generated project — per create-mode file, the template hash and rendered hash at seed time. Update/refresh warn when the Foundation template evolves after seeding; user edits are never flagged and project-owned files are never rewritten. | `scaffold_manifest.md` |
| **Signed license file** | Offline `ovasabi.lic` JWT validated with Ovasabi's public key for air-gapped enterprise package/update authorization. | `foundation_distribution.md` |
| **Server-kit** | Go backend platform primitives: the largest Foundation module containing 50+ packages for events, workers, database, resilience, auth, and more. | `foundation_guide.md` |
| **Simplified Technical English (STE)** | ASD-STE100 controlled-language standard adapted for Foundation documentation and code comments. Governs sentence length, noun clusters, vocabulary, voice, and safety alerts. | `ste_documentation_practices.md` |
| **STE controlled vocabulary** | The set of approved words with single-meaning assignments and approved parts of speech. Technical names from the Foundation glossary and module API surface are always permitted as nouns. | `ste_documentation_practices.md` |
| **STE noun cluster** | A group of nouns or adjectives functioning as a single noun phrase. Foundation limits noun clusters to a maximum of 3 words and requires expansion with prepositions for longer names. | `ste_documentation_practices.md` |
| **Tenant isolation** | Organization scope derived from authenticated context, never from client-supplied data. Preserved through all lifecycle lanes. | `foundation_nervous_system.md` |
| **Tracing** | `server-kit/go/tracing` — OpenTelemetry integration with correlation ID bridging and HTTP middleware for automatic span creation. | `foundation_guide.md` |
| **Transfer lane** | `server-kit/go/transfer` — progress-bearing operation lifecycle: monotonic byte progress on an ephemeral lane, bracketed by durable bookend events. | `transfer_lane.md` |
| **UI-minimal** | `@ovasabi/ui-minimal` — shared structural UI primitives, semantic theme tokens, and motion helpers. App components should be thin wrappers. | `styling_design_practices.md` |
| **WASM control buffer** | The 4KB fixed shared buffer in `runtime-sdk` guaranteeing cache affinity and zero-copy pointer exchanges with zero allocation pressure. | `foundation_guide.md` |
| **Worker** | `server-kit/go/worker` — River-based background job handling with bounded queues, retry policies, and correlation metadata propagation. | `foundation_guide.md` |

---

## Part 2: Module Reference Cards

### server-kit (Go backend)

| Package | What | Why | Contract / Invariant | Key Types | Practice Rules |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `auth` | RBAC and JWT context | Derive tenant/user from trusted claims | Tenant never from client | `OrgIDFromContext` | CP-20 |
| `bulk` | Resumable multipart uploads | Survive dropped connections | Idempotent part replay | `Manager`, `Plan` | CP-02, CP-18 |
| `cache` | Cache-aside with TTL | Reduce DB pressure | Tag-based invalidation | `GetOrSet`, `Invalidator` | CP-07 |
| `chain` | Worker job composition | Bounded multi-step workflows | Each step bounded | `Chain`, `Step` | CP-02, CP-24 |
| `circuitbreaker` | Fault tolerance | Stop cascading failures | Half-open recovery | `Execute`, `ExecuteWithFallback` | CP-02 |
| `compress` | Response compression | Reduce bandwidth | Disabled with secrets | `Middleware` | CP-33 |
| `contracttest` | Lifecycle verification | Prove event contracts | Requested before terminal | `VerifyCommandLifecycle` | TE-10 |
| `database` | PostgreSQL helpers | Timeouts, pools, tx discipline | Source of truth | `Executor` | CP-04 |
| `degradation` | Graceful fallback | Handle dependency loss | Auto state transitions | `Manager`, `Sentinel` | CP-02 |
| `domainerr` | Domain error helpers | Typed business errors | Maps to HTTP status | `DomainError` | CP-04 |
| `errors` | Error taxonomy | Categorized error codes | HTTP mapping | `New`, `CodeNotFound` | CP-04 |
| `eventlog` | Lifecycle evidence | Append-only audit | Never mutable | `Append`, `Query` | CP-12 |
| `events` | Event bus | Decoupled communication | Pattern matching | `Bus`, `Envelope` | CP-10 |
| `extension` | Typed payloads | Zero-copy event data | Direct struct serialization | `Value`, `Object` | CP-07 |
| `featureflags` | Feature toggles | Safe rollouts | Percentage + targeting | `IsEnabled` | CP-22 |
| `graceful` | Response formatting | Consistent envelopes | Context cancellation aware | `Handler`, `Emit` | CP-04 |
| `healthcheck` | Health probes | Dependency readiness | Concurrent checks | `AddCheck`, `Handler` | CP-22 |
| `hermes` | Hot projection reads | Reduce DB load | Bounded, falls back to PG | `Store`, `Partition` | CP-02 |
| `httpapi` | Route generation | Event→HTTP mapping | Route catalog projection | `MakeEventRoute` | CP-18 |
| `intelligence` | Signal extraction | Graph/relevance hints | Bounded, drop under pressure | `Signal` | CP-02 |
| `kernellane` | Native compute dispatch | Rust/FFI/SHM | Descriptor validation | `Dispatch` | CP-09 |
| `metadata` | Request context | Identity propagation | Survives all lanes | `WithCorrelationID` | CP-12 |
| `money` | Financial arithmetic | Integer minor units | No floats for money | `Amount`, `Add` | CP-05 |
| `objectstore` | Object storage | Tenant-scoped keys | Streaming put/get | `PutStream`, `GetRange` | CP-23 |
| `observability` | Metrics/traces | System inspection | Bounded ring collector | `RecordTrace` | CP-34 |
| `policy` | Authorization | Policy-as-code | Default-deny | `Evaluate`, `Policy` | CP-20 |
| `projectiongw` | Projection HTTP gateway | Read surface for Hermes | Tenant-scoped queries | `Handler` | CP-20 |
| `redis` | Redis client | Coordination/cache | Not durable truth | `Client` | TE-13 |
| `registry` | Route dispatch | Central HTTP routing | Validates method/path/event | `Register`, `HTTPRoute` | CP-18 |
| `resilience` | Dependency coordination | Unified health model | All deps registered | `Register`, `Status` | CP-22 |
| `retry` | Retry policies | Handle transients | Bounded attempts + jitter | `Do`, `NewPolicy` | CP-02 |
| `security` | Auth middleware | Trust boundaries | PQ TLS helpers | `ApplyPostQuantumTLS` | CP-20, CP-33 |
| `transfer` | Transfer lifecycle | Progress + bookends | Monotonic, bounded | `Tracker`, `Manager` | CP-02, CP-10 |
| `versioning` | API versioning | Backward compat | Deprecation headers | `HandleVersion` | CP-12 |
| `worker` | Background jobs | Bounded async work | River-backed, correlation | `Register`, `Enqueue` | CP-02, CP-35 |
| `wsmetrics` | WebSocket metrics | Socket observability | Bounded counters | `Collector` | CP-34 |
| `wsrouting` | WebSocket routing | Topic-based fanout | Exact/prefix topics | `Route`, `Fanout` | CP-17 |

### Frontend packages (TypeScript)

| Package | What | Why | Key Exports |
| :--- | :--- | :--- | :--- |
| `@ovasabi/runtime-transport` | Client wire | Command bus, envelopes, WS/HTTP, route registry | `createCommandBus`, `createAppRuntime`, `createRouteRegistry` |
| `@ovasabi/frontend-kit` | Operational utilities | IndexedDB, metadata, stores, transfer progress | `useTransfer`, `createTenantProjectionStore` |
| `@ovasabi/ui-minimal` | UI primitives | Theme, components, motion | `MinimalButton`, `MinimalAppShell`, `useMinimalMotion` |
| `@ovasabi/runtime-native` | Native shell bridge | Tauri IPC, secure storage, capability detection | `dispatchNativeFrame` |

---

## Part 3: Invariant Quick Reference

The 8 canonical invariants from `foundation_nervous_system.md`:

| # | Invariant | Definition | What Breaks If Violated | Verification |
| :--- | :--- | :--- | :--- | :--- |
| 1 | **MetadataPreserved** | Correlation ID, request ID, idempotency key, user, session, org, schema version, locale, and trace fields survive every lane. | Lost audit trail, broken tracing, orphaned events. | `contracttest.VerifyCommandLifecycle` |
| 2 | **TenantScopeStable** | Organization scope derived from auth context does not change between request, job, terminal event, and projection. | Cross-tenant data leakage. | TE-09 tenant-negative tests |
| 3 | **RequestedBeforeTerminal** | A mutating command has a `:requested` event before `:success` or `:failed` is visible. | Consumers miss the start of a lifecycle. | TE-10 lifecycle tests |
| 4 | **ExactlyOneTerminalVisible** | An accepted command exposes one semantic terminal state. Duplicates share idempotency identity. | Double-processing, phantom success. | Idempotency integration tests |
| 5 | **IdempotentRetry** | Retries and duplicate deliveries do not duplicate durable side effects. | Double charges, duplicate records. | TE-14 worker idempotency tests |
| 6 | **BoundedWork** | Retries, queue depth, Redis waits, request handling, and worker execution all have finite caps or deadlines. | Resource exhaustion, unbounded loops. | CP-02 enforcement checks |
| 7 | **FallbackRefinement** | All transport lanes (direct, binary, WS, HTTP, Redis, WASM, FFI, JSON) preserve the same command semantics. | Silent behavior change on lane switch. | TE-11 parity tests |
| 8 | **ProjectionAfterTerminal** | Frontend-visible projection changes are causally tied to success/failed events or explicitly documented optimistic UI. | Phantom UI state, stale reads. | Frontend transport tests |

---

## Part 4: Practice Control Quick Reference

### Coding Practices (CP-01 through CP-36)

| Rule | Summary |
| :--- | :--- |
| CP-01 | No goto, no uncontrolled recursion |
| CP-02 | All loops, retries, and time-consuming operations must be bounded |
| CP-03 | Functions ≤80 lines, cyclomatic complexity ≤15 |
| CP-04 | Check return values and propagate errors intentionally |
| CP-05 | Use assertions/invariants at boundaries |
| CP-06 | Minimize mutable shared state and scope data tightly |
| CP-07 | Apply allocation discipline in hot paths |
| CP-07b | Specify hot-path behavior before optimizing |
| CP-08 | Zero-warning mindset and static analysis in CI |
| CP-09 | Restrict unsafe and reflection-heavy patterns |
| CP-10 | Keep event contracts deterministic and idempotent |
| CP-11 | Code for testability-first behavior |
| CP-11A | Use cleanup and unlock patterns deliberately |
| CP-12 | Keep documentation and traceability current |
| CP-13 | Prefer styled-component architecture and shared UI primitives |
| CP-14 | Form state should default to a single object model |
| CP-15 | Use lodash intentionally to reduce code bloat |
| CP-16 | Prefer adaptive concurrency over fixed internal request pacing |
| CP-17 | Frontend realtime architecture must stay contract-first and minimal |
| CP-18 | Ingress edge security, abuse resistance, and origin controls |
| CP-19 | Frontend token and secret lifecycle safety |
| CP-20 | Defence in depth: validation, authorization, and state safety |
| CP-21 | Frontend resilience and error isolation |
| CP-22 | Operational monitoring and startup safety |
| CP-23 | Safe asset management and storage |
| CP-24 | Offload slow context operations to background workers |
| CP-25 | Frontend request replay, dedupe, and loading state must be scoped |
| CP-26 | Frontend boot, runtime singleton, and stale-build recovery |
| CP-27 | Browser boundary, headers, and cache control must be explicit |
| CP-28 | Dependency, third-party integration, and secret supply chain hygiene |
| CP-29 | Adversarial threat modeling for exposed features |
| CP-30 | Use coverage plus complexity to prioritize change risk |
| CP-31 | MutationObserver is exception-only architecture |
| CP-32 | Runtime communication must use foundation transport contracts |
| CP-33 | Post-quantum readiness must be crypto-agile and hot-path safe |
| CP-34 | Observability, SLOs, and fault tests are foundation requirements |
| CP-35 | River / background job reliability and scaling |
| CP-36 | Agent-authored changes must carry evidence |

Full rules: `coding_practices.md`

### Testing Practices (TE-01 through TE-41)

| Rule | Summary |
| :--- | :--- |
| TE-01 | Tests are part of the architecture contract |
| TE-02 | Use black-box tests before structural tests |
| TE-03 | Define an oracle for every test |
| TE-04 | Select cases by equivalence classes, boundaries, and duplicates |
| TE-05 | Cross-product only where interactions matter |
| TE-06 | Coverage is a floor, not proof |
| TE-07 | Test loops and retries at zero, one, two, many, and exhausted |
| TE-08 | Every mutating test command carries correlation and idempotency evidence |
| TE-09 | Tenant isolation tests must be negative as well as positive |
| TE-10 | Event lifecycle tests are required for domain flows |
| TE-11 | Runtime envelope and binary-frame parity must be tested |
| TE-12 | Database tests must prove constraints, not only app prechecks |
| TE-13 | Redis and cache tests must distinguish ephemeral state from truth |
| TE-14 | Worker tests must prove idempotency and bounded progress |
| TE-15 | Frontend tests must exercise user-visible behavior and transport state |
| TE-16 | WASM/Rust runtime tests must prove host/guest contract safety |
| TE-17 | Concurrency tests must make ownership and termination observable |
| TE-18 | Performance tests must separate hard bounds from statistical targets |
| TE-19 | Security tests must target trust boundaries |
| TE-20 | Regression tests are mandatory for repaired defects |
| TE-21 | Integration tests must own their environment |
| TE-22 | Stubs, mocks, and fakes must preserve the contract they replace |
| TE-23 | Test data must be explicit, minimal, and domain-shaped |
| TE-24 | Tests must not hide failures behind broad skips |
| TE-25 | Generated contracts must be checked for drift |
| TE-26 | Test documentation must explain risk, not restate code |
| TE-27 | Test files must stay deterministic |
| TE-28 | Acceptance and E2E tests must cover core journeys |
| TE-29 | Model-based tests are required for stateful protocols |
| TE-30 | Fault-based tests must target likely Foundation bug classes |
| TE-31 | Use property tests for invariant-heavy code |
| TE-32 | Acceptance mutation hardens generated acceptance tests |
| TE-33 | Test suites must be organized by speed and dependency |
| TE-34 | Test failures must preserve diagnostics |
| TE-35 | Test automation must be reproducible locally |
| TE-36 | Testing checks are linted as part of Foundation |
| TE-37 | Update this document when test strategy changes |
| TE-38 | Service-backed pressure tests prove substrate claims |
| TE-39 | Scaffold smoke belongs in verify, not fast lint |
| TE-40 | Benchmark and latency statistics must be sound |
| TE-41 | Agent-generated test changes must prove oracle strength |

Full rules: `testing_practices.md`

---

## Part 5: Common Agent Questions

### Architecture & Structure

**Q: What are the three ownership layers?**
Platform modules (server-kit, runtime-*, frontend-kit, ui-minimal, config-contracts) → Managed scaffold (generated from templates, synchronized by update) → Project-owned code (domain services, routes, product UI). See `foundation_architecture_contract.md`.

**Q: Which layer owns this file?**
Check `templates/scaffold.manifest.tsv`. If the file has mode `overwrite` or `force`, Foundation owns it. If mode `create`, the project owns it after generation. If the file is not in the manifest, it is project-owned.

**Q: What is the difference between `overwrite`, `force`, and `create` modes?**
`overwrite` = Foundation replaces it every sync. `force` = Foundation replaces only when `--force` is used or file is missing. `create` = seeded once, project-owned forever. See `scaffold_manifest.md`.

**Q: What are Foundation profiles?**
`core` (default, full backend), `lite` (minimal, no GPU/native/formal), `performance` (WASM/GPU/Hermes-heavy), `regulated` (security/audit emphasis). Profiles remove optional surfaces but never weaken invariants. See `foundation_architecture_contract.md`.

### Lifecycle & Events

**Q: What is the event naming pattern?**
`<domain>:<action>[:vN]:requested`, `<domain>:<action>[:vN]:success`, `<domain>:<action>[:vN]:failed`. The versioned form (`:vN:`) is preferred for new contracts. See `foundation_nervous_system.md`.

**Q: Where does tenant scope come from?**
From authenticated context via `auth.OrgIDFromContext(ctx)`. Never from client-supplied `organization_id`. The scope must remain stable through request → worker → event → projection. See invariant #2 `TenantScopeStable`.

**Q: When should I use Hermes vs direct DB?**
Hermes is for hot operational reads where bounded staleness is acceptable (dashboards, fanout, repeated lookups). Direct DB is for durable truth, writes, and reads requiring strict consistency. Hermes falls back to DB when stale. **WARNING**: Do NOT use Hermes for writes, strict transaction consistency, or financial reconciliation. Writes must go directly to the relational database. See `hermes_hotplane.md` and `hermes_read_modes.md`.

**Q: What is the transfer lane for?**
Operations with user-visible progress: uploads, downloads, exports, transcodes. Progress is ephemeral (never in the event log). Lifecycle bookends (`:requested`/`:success`/`:failed`) are durable. See `transfer_lane.md`.

### Adding New Functionality

**Q: How do I add a new domain service?**
Create `internal/service/<domain>/` with handler, repository, and tests. Register routes through the server bootstrap. Add protobuf schemas in `api/protos/`. Run `make generate-contracts`. Add lifecycle and integration tests. See `foundation_guide.md` §3.

**Q: How do I add a new server-kit module?**
Create `server-kit/go/<package>/` with implementation and tests. Add to `go.mod`. Update `foundation_guide.md` extended modules table, `AGENTS.md` modules table, and `tooling/server_kit_module_manifest.tsv` (checked by `make check-server-kit-module-parity`). Add practice control entries if the module introduces new rules. Run `make test`.

**Q: How do I add a Rust performance unit?**
Create a crate under the project's `rust/crates/` implementing `ovrt_unit::RuntimeUnit` (a validated `RuntimeUnitDescriptor` plus `run`). Expose the lanes: a stdio bin via `ovrt_native::serve_stdio`, FFI via `ovrt_ffi::export_runtime_ffi!`, WASM via `make build-rust-wasm` + `wasm-manifest`. Integrate from Go through `runtimehost.NewProcessPool`/`NewFFIPool` and add a runtimehost integration test for at least one native lane. Full walkthrough: `rust_unit_guide.md`.

**Q: How do I add a new transfer/upload route?**
Use `httpapi.MakeTransferRoute` for simple streaming uploads or `httpapi.MakeResumableTransferRoutes` for resumable multipart. Both require a `transfer.Manager` and `objectstore.Store`. See `transfer_lane.md`.

**Q: How do I generate the frontend route registry?**
Run `make communication-contracts` in the app or `make generate-contracts` in Foundation core. This runs `docgen route-catalog` → `generate_frontend_commands.mjs` → `runtimeRoutes.ts`. See `frontend_command_registry.md`.

**Q: Where do generated types live?**
Protobuf Go types: `api/protos/gen/`. Protobuf TypeScript types: `frontend/src/types/protos/`. Runtime routes: `frontend/src/generated/runtimeRoutes.ts`. Lifecycle manifest: `docs/references/lifecycle/lifecycle_contract.json`.

### Evidence & Compliance

**Q: What is the definition of done for an agent change?**
Answer 7 questions: (1) contract changed, (2) invariant preserved, (3) evidence added, (4) fallback path, (5) scope boundary, (6) regression guard, (7) docs updated. See `agent_operating_contract.md`.

**Q: What evidence do I need for a performance claim?**
Benchmark with: command, machine class, payload shape, variance, allocation/copy budget. Before-and-after numbers. All active lanes benchmarked, not just the optimized one. See `performance_practices.md` and `performance_lab.md`.

**Q: What evidence do I need for a security change?**
Negative tests for malformed input, privilege escalation, tenant bleed, replay, timeout, and failure logging. Evidence ledger entry. See `security_practices.md` and `agent_operating_contract.md`.

**Q: What is the practice controls matrix?**
`tooling/practice_controls.psv` — machine-readable mapping from every CP/TE rule to its owning doc, risk class, automation strength, enforcement script, required evidence, and merge-gate posture. Checked by `make check-practice-controls`. See `practice_controls.md`.

### Frontend & Runtime

**Q: How do I consume Foundation packages in a frontend?**
Import from package boundaries: `@ovasabi/runtime-transport`, `@ovasabi/frontend-kit`, `@ovasabi/ui-minimal`. Never alias raw `foundation/*/ts/src` paths. Keep `preserveSymlinks` enabled in Vite/Vitest/TS config. See `frontend_scaffold_sync.md`.

**Q: What UI primitives exist?**
Check `ui-minimal` for: `MinimalAppShell`, `MinimalScrollMain`, `MinimalSkipLink`, `MinimalSidebar`, `MinimalButton`, `MinimalCard`, `MinimalInput`, `MinimalTable`, `MinimalCalendar`, `MinimalActionModal`, `MinimalSkeleton`. App components should wrap these, not replace them. See `styling_design_practices.md`.

**Q: When should I use WASM vs Go vs direct JS?**
Use the runtime lane planner. Small bounded payloads → SAB/WASM or transferable workers. Large batches → WebGPU (if transfer amortized). Orchestration/auth/DB → Go. UI thread → control and render only. See `foundation_guide.md` §8.

**Q: What is `createAppRuntime`?**
The recommended facade that wires the generated route registry, HTTP/WS transports, and command bus into one `dispatch(eventType, payload)` call. Replaces hand-rolled runtime seams. See `frontend_command_registry.md`.

---

## Part 6: Document Decision Map

**If you are changing…** → **Read these docs:**

| Change Area | Required Reading |
| :--- | :--- |
| Domain service / handler | `coding_practices.md`, `foundation_nervous_system.md`, `testing_practices.md` |
| Database schema / query | `database_practices.md`, `migration_practices.md` |
| Event contracts / lifecycle | `foundation_nervous_system.md`, `coding_practices.md` (CP-10), `testing_practices.md` (TE-10) |
| Worker / background job | `coding_practices.md` (CP-02, CP-24, CP-35), `testing_practices.md` (TE-14) |
| Redis usage | `redis_practices.md`, `testing_practices.md` (TE-13) |
| WebSocket routing | `websocket_scaling.md`, `coding_practices.md` (CP-17) |
| Frontend component | `styling_design_practices.md`, `frontend_scaffold_sync.md`, `references/README.md` |
| Frontend transport | `frontend_command_registry.md`, `frontend_scaffold_sync.md` |
| Runtime / WASM / Rust | `runtime_foundation.md`, `rust_runtime_practices.md`, `rust_unit_guide.md`, `performance_lab.md` |
| Native / Tauri | `runtime_native.md`, `rust_runtime_practices.md` |
| GPU / WebGPU | `gpu_practices.md`, `game_runtime_practices.md` |
| Security / auth | `security_practices.md`, `coding_practices.md` (CP-18, CP-20, CP-29), `ai_threat_model.md` |
| Performance optimization | `performance_practices.md`, `performance_lab.md`, `foundation_benchmarks.md` |
| Scaffold / template | `scaffold_manifest.md`, `foundation_architecture_contract.md` |
| AI / model integration | `ai_practices.md`, `ai_threat_model.md` |
| Hermes / projections | `hermes_hotplane.md`, `hermes_read_modes.md`, `projection_freshness_contract.md` |
| Upload / transfer | `transfer_lane.md` |
| Financial arithmetic | `mathematical_practices.md`, `coding_practices.md` (CP-05) |
| Post-quantum / crypto | `post_quantum_security.md`, `coding_practices.md` (CP-33) |
| Delivery metrics / DORA | `delivery_metrics_practices.md` |
| Formal methods / TLA | `tla_architecture_practices.md`, `specs/tla/` |
| Agent workflow / evidence | `agent_operating_contract.md`, `practice_controls.md` |
