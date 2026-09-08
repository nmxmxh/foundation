# Changelog

All notable changes to Foundation will be documented in this file.

## [Unreleased]

### Added

- **database**: `CachedStateStore` — shared, stampede-safe cache-aside point reads over any `StateStore` (Postgres included). Opaque `json.RawMessage` payloads parsed back through the extension lane preserve int64/raw-byte fidelity; version-guarded refreshes block out-of-order commit clobbering; negative caching, per-op timeout bounds, and full degradation to Postgres on any cache error. Includes allocation budget guard (`TestAllocationBudgetCachedHit`) plus hit/miss/envelope benchmarks.
- **database**: `AsPostgresDB` unwraps bounded wrapper chains (hermes, cache) to the concrete pool-bearing store without new constructor signatures.
- **hermes**: point-read and point-write benchmarks with allocation reporting (`BenchmarkHermesPointReadHotPartition` ~630 ns / 3 allocs), establishing the layering baseline that keeps warm partitions ahead of added cache layers.
- **templates/backend**: `RIVER_DIRECT_URL` routes both River pools through a session-mode endpoint bypassing PgBouncer transaction pooling, restoring native LISTEN/NOTIFY wakes (Option A of the read-pressure handoff); `STATE_STORE_CACHE` (`off|memory|redis` + TTL knobs) wires `CachedStateStore` for direct StateStore consumers with first-fallback degradation logging.
- **scripts**: `db_diagnostics.sh` — parameterized read-pressure evidence collector (pg_stat leaders, river_job state/queue backlog, oldest job age) with no hardcoded hosts or credentials.

### Changed

- **templates/backend**: startup now initializes the event bus before the database so the state-store cache can share its Redis client; cleanup ordering is preserved.
- **deps**: `riverqueue/river` v0.35.0 → v0.44.1 across `server-kit/go` (direct) and `runtime-sdk/go` (indirect). Builds and worker/database suites pass unchanged — no API surface we use moved. Deployment gate: run migration 7 (`river migrate-up`) during a brief worker stop window; gains PgBouncer simple-protocol JSON hardening (#1153), per-queue `FetchCooldown`/`FetchPollInterval`, full reindexer coverage for previously bloated `river_job` indexes, jittered poll loops, and `JobDeleteMany` backlog tooling.
- **database**: flattened cache envelope embeds `RecordData` directly — hit path drops from 23 to 20 allocs/op (−17% ns/op) by eliminating the second parse pass; single extension-backed unmarshal now produces envelope fields and typed data together.

- **wsrouting**: `Router.UpdateAuthState(ctx, connID, AuthState)` to atomically rotate a connection's full security context (user, organization, role, session) — e.g. after a context switch — plus `Authenticated`/`OrganizationID`/`Role`/`SessionID` fields on `ConnectionInfo`. `UpdateAuth` is retained as a back-compat wrapper. Prevents the gateway from authorizing frames against a stale org context.
- **auth**: `ClockSkewLeeway` (60s) applied symmetrically to JWT `exp` and `iat` validation. Skew is not directional, so tolerating it in only one direction leaves the other failing for the same reason. `iat` was previously minted and never validated at all, which also let a post-dated token extend its useful life past what the issuer intended; it is now rejected beyond the leeway (RFC 8725 §3.9).
- **runtime-transport**: `createOfflineQueue` now strips the auth credential from queued envelopes on enqueue and re-stamps a fresh one on drain (via `resolveAuthToken`), so requests replayed after a reconnect/token rotation never carry a stale token. New `authTokenExtraKeys`/`resolveAuthToken` options.
- **runtime-transport**: `createWebSocketTransport` gains an `authenticate(session)` re-authentication hook that runs on every (re)connect before readiness, re-subscribes, and queue drain — closing the window where a freshly reconnected socket serves frames unauthenticated.
- **templates/frontend**: reference `useIdleLogout` hook (keyed on session presence, not token, to survive token rotation), shared `sessionActivity` helpers (cleared on logout), and a reference service worker (`public/sw.js`) + `registerServiceWorker` that serves content-hashed immutables cache-first and the navigation shell network-first. Idle logout now ends the session in every open tab via the `storage` event: shared activity already kept sibling tabs alive, but a tab that timed out logged only itself out and left the others authenticated — the exact state idle logout exists to prevent.
- **runtime-sdk**: native↔WASM parity gate in `ovrt-wasm-host`. `ParityHarness::compare_units` runs one input through the native lane and a wasmtime-instantiated guest build, then diffs full control-buffer state — header integers, every named epoch slot, payload regions, diagnostics — turning the §8 evidence requirement of `rust_unit_guide.md` from documented to enforced. Guests target a defined ABI (cdylib exporting `ovrt_unit_run(handle) -> i32` over the `ovrt_browser` import surface); `WasmGuest` holds a compiled guest warm across exchanges so steady-state cost is measurable instead of paying instantiation per call. Bounds are engine-enforced (fuel, memory limiter, epoch-watchdog deadline) behind `--features wasm-runtime`, keeping ordinary builds dependency-free. Reference dual-target guest lives in `crates/ovrt-parity-fixture`; lane costs are recorded by `benches/parity_bench.rs` (native ≈1.1µs vs warm WASM ≈32µs per exchange, instantiation ≈44ms amortised) and wired into `performance_check.sh`.
- **runtime-sdk**: volatility story for parity. Time-reading units pin one instant via `ParityOptions::fixed_now_ms` — guests see it through the pinned `ovrt_get_now` import, the native side injects the same value through its own clock (pattern demonstrated by the fixture's clock-marker mode). Per-lane counters are excluded by name through `DiffPolicy`; payload regions are never excludable. Clock drift between lanes is proven to surface as an output divergence rather than pass silently.
- **runtime-sdk**: `WasmGuest::compile`/`exchange` API for reuse of an instantiated guest across bounded exchanges, with per-exchange buffer reset, refuel, and deadline re-arm; warm results are verified byte-identical to the one-shot path.

### Fixed

- **projectiongw**: audience-partitioned scopes — the read path authorized by tenant and nothing else, so in a deployment where many end users share one organization every signed-in client received every row of the scopes it bound (profiles, orders, carts, wallet movements, message bodies), on the snapshot and the delta stream alike. A scope may now declare `AudiencePolicy{Mode: AudiencePerRecord, Fields: [...]}`: the fan-out partitions on the audience ids a record names (`tenant:domain:collection:@audience`, encode-once preserved — cost scales with distinct audiences per batch, not subscriber count), a subscriber is registered only under the audiences its verified identity resolves to (`HandlerConfig.Audience`, a trust boundary like `Tenant`), and the snapshot filters on the same declared fields then re-checks every record before answering. An unresolvable audience is 403 on both halves — never a fall back to tenant scope. `AudienceConfig.Strict` makes an undeclared scope a refusal rather than an accidental broadcast. The same missing term made the newest-1024 snapshot window organization-wide, so a user's own rows silently fell out of it once the org got busy; a per-audience window fixes that with the disclosure. Default is unchanged (`AudienceTenant`), so upgrading changes nothing until a policy is declared — a deployment that has not declared them is still broadcasting. Residual: a tombstone carrying no audience field reaches nobody and is counted by `Gateway.AudienceDrops()` rather than broadcast; emit deletes carrying the audience fields to converge deletions live. Scope components may no longer contain `:`, which keeps the audience term unforgeable through a crafted collection name. New invariant `AudienceScopeStable` in `FrontendLiveProjection.tla` with its own negative control; audience/enforcement-point/field-allowlist are now required fields of a projection note.
- **tooling**: `projection_audience_check.sh` (control `PROJFRESH-02`) carries the projection read-path requirement into every generated project, wired as `make check-projection-audience` and included in `lint-foundation`. Documentation alone could not reach app code; a check does, because `tooling/scripts/*` is copied into each project's `scripts/checks/`. It WARNS when a project uses the gateway with no audience policy declared, because tenant-wide delivery is a legitimate B2B posture and a hard failure would break every existing project on upgrade. It FAILS only on wiring that cannot work: an `AudiencePerRecord` scope with no `HandlerConfig.Audience` resolver answers 403 to every read. It also warns when `Strict` is unset, and when a per-record scope deletes without carrying audience fields.
- **hermes**: the mirror sweeper's delete lanes now batch like its changed lane. `ProjectedRuntimeStore.DeleteRecords` is the symmetric shape of `UpsertRecords` — base deletes per record (optional `batchRecordDeleter` seam for a base store that can batch round trips), then ONE `ApplyBatch` per scope group — and both delete sources share one `deleteBatcher` flushing at `BatchSize`. Deleting one at a time meant every row took the partition lock, ran a full apply cycle, published indexes and notified observers on its own, and made the projection gateway encode one fan-out frame per deletion. At 256 records in one scope: −35% time, −27% allocations, and 256 → 1 fan-out frames. The first grouping attempt cost 33% MORE bytes (`Event` embeds a `DomainRecord`, so append growth copies a large value log(n) times); fixed by sizing each group exactly and taking a single-scope fast path, landing at +2% bytes — the materialized batch itself. See `docs/foundation_benchmarks.md` (2026-09-08).
- **database**: `PostgresDB.DeleteRecordsBatch` removes many domain records in ONE statement (`DELETE ... USING unnest`), the delete counterpart of `UpsertRecordsBatch`. It satisfies the optional capability hermes probes for, so a Postgres-backed projected store now pays one round trip per delete batch instead of one per record; a base store without the method still falls back to sequential deletes. Identities are trimmed and deduplicated client-side, because a DELETE cannot match one row twice and the sequential lane's second delete of an identity is a no-op either way. An empty identity component is kept rather than rejected, matching `DeleteRecord`, which trims and then matches nothing. Refinement is covered by a service-backed parity test asserting same-rows-removed, absent-row tolerance, duplicate handling, and — the property that needs a live database — that the four-column identity join never reaches another organization's rows.
- **hermes**: addressed deletes, so a per-record audience scope can converge deletions live instead of permanently. `ProjectedRuntimeStore.DeleteRecordWithFields(ctx, rec)` deletes by identity and projects the delete carrying `rec.Data`; `MirrorSweeper.AddDeleteRecordSource(name, DeletedRecordsSince)` streams a `database.DomainRecord` rather than four strings, so a tombstone source can attach the fields the audience is derived from. Both are additive — `DeleteRecord` delegates with empty data and `AddDeleteSource` is untouched, so tenant-wide scopes are byte-for-byte unchanged. Before this, `projectiongw`'s per-record scopes had no way to address a tombstone at all, which made "fail closed and count it" a standing condition rather than an edge: every deletion was withheld and `Gateway.AudienceDrops()` tracked delete traffic instead of flagging a real gap. Carry the audience fields and nothing else — a tombstone is not a second place to keep the deleted row.
- **runtime-network**: `the_race_collapses_the_tail` failed under parallel machine load. The pass/fail threshold sat only 15× above a 100µs sleep, so scheduler wake latency could push raced samples across it; the seeded stall pattern was never the cause. Stall, inter-frame gap, and threshold rescaled to 10ms/12ms/5ms, putting fifty-fold noise headroom below the threshold and a 2× stall margin above it. Verified passing under full-core CPU saturation.
- **repo**: removed the git-tracked 20MB Go test binary `server-kit/go/projectiongw.test` and added `**/*.test` to `.gitignore`, closing the artifact-class gap the enforcement sweep surfaced.

## [1.2.0-dev] - 2026-06-13

### Added (1.2.0)

- Direct reflection-based serialization/deserialization for custom structs in `extension.Value` and `extension.FromJSON` to remove the costly JSON marshal/unmarshal round-trip.
- Queue capacity and queue current length tracking fields in `registry.MetricsSnapshot`.
- High-level test coverage in `graceful_test.go` and `registry_test.go` for struct payloads, context cancellation, and queue metrics.

### Changed

- Refactored `graceful.Handler` to perform early check of `ctx.Err()` to abort event emission on cancelled contexts.
- Removed duplicate correlation extraction from metadata inside `InMemoryEventEmitter` and `RedisEventEmitter`.
- Skipped redundant cloning of decoded metadata and payload envelopes in `registry.dispatchEnvelope`.
- Swapped sort-and-copy iteration in observability `cloneMap` and `cloneDatabasePoolMap` for optimized `maps.Copy`.
- Cleaned up dead `cloneObject` code from `events/bus.go`.

### Fixed

- Fixed timer leak in worker retry backoff loops in `worker/engine.go` by stopping the timer before returning or looping.

## [1.1.0] - 2026-05-03

### Added (1.1)

- **policy**: Policy-as-code authorization engine (Cedar-inspired)
- **redis**: Native Redis client integration for server-kit
- **worker**: River-based background job handling infrastructure
- **docgen**: Automated documentation generation for generated projects

### Changed (1.1)

- Updated tech stack standards to Go 1.26, React 19.2, TypeScript 5.9+, Rust 1.95, PostgreSQL 18, Redis 8
- Refined **AGENTS.md** with clearer terminology (Core vs Project vs Template)
- Formalized Foundation Dependency Boundary rules

### Fixed (1.1)

- Sync issues between template scaffold and foundation core package boundaries

## [1.0.0] - 2026-04-21

### Added (2)

#### Server-Kit Modules

- **circuitbreaker**: Fault tolerance for external service calls
  - Configurable failure/success thresholds
  - Half-open state with limited request testing
  - Global registry for managing multiple breakers
  - Fallback function support

- **featureflags**: Structured feature flag system
  - Percentage-based rollouts
  - User and organization targeting
  - Environment-based overrides
  - Time-based activation windows
  - Multiple sources (env, JSON, memory)
  - HTTP middleware support

- **tracing**: Distributed tracing with OpenTelemetry
  - OTLP exporter support
  - Correlation ID bridging
  - HTTP middleware for automatic span creation
  - Context propagation helpers
  - Configurable sampling rates

- **policy**: Policy-as-code authorization
  - Cedar-inspired policy syntax
  - Principal, action, and resource matching
  - Condition evaluation
  - Priority-based policy ordering
  - Default-deny security model

- **retry**: Standardized retry policies
  - Exponential backoff with jitter
  - Configurable max attempts and delays
  - Context-aware cancellation
  - Preset policies (aggressive, gentle, HTTP, database)
  - Retryable/NonRetryable error wrappers

- **healthcheck**: Reusable health check builder
  - Liveness and readiness probes
  - Database, Redis, HTTP, TCP checks
  - Concurrent check execution
  - Result caching
  - Critical vs non-critical checks

- **errors**: Formalized error taxonomy
  - Categorized error codes (client, server, domain)
  - HTTP status mapping
  - Error wrapping with context
  - Stack trace capture
  - API-safe response formatting

- **cache**: Standardized cache-aside patterns
  - Pluggable backends (memory, Redis)
  - TTL policies
  - Tag-based invalidation
  - GetOrSet helper with generics

- **degradation**: Graceful degradation modes
  - Health monitoring for dependencies
  - Automatic state transitions (normal → degraded → unavailable)
  - Configurable fallback behaviors
  - Recovery detection

- **versioning**: HTTP API versioning
  - Header-based versioning
  - Path-based versioning
  - Query parameter versioning
  - Accept header versioning
  - Deprecation headers
  - Sunset support

#### Project Bootstrapper

- `init.sh` script for creating new projects
- Profile support: full, backend, frontend, minimal
- Automatic Go module and npm initialization
- Docker configuration generation
- Makefile with standard targets
- CLAUDE.md generation for AI assistance

#### Update Mechanism

- `update-project.sh` for updating existing projects
- Tooling synchronization
- Documentation linking
- Version tracking
## [0.0.1] - 2026-06-28

Foundation reset to version 0.0.1. This marks the first clean documentation
baseline with structural integrity, complete cross-references, and the agent
glossary.

### Documentation

- Added `docs/foundation_glossary.md`: agent Q&A reference, concept glossary,
  module cards, invariant reference, and practice summaries.
- Added `docs/info/` directory for informational documents that are not
  directly Foundation practices or contracts.
- Moved `coding_magic.md`, `columnar_projection_lane.md`, and
  `scaffolded_projects_executive_summary.md` to `docs/info/`.
- Removed `handover_note_codex.md`, `inos_runtime_reuse_plan.md`, and
  `frontend_prototype_runtime_todo.md`.
- Refreshed `docs/README.md` documentation map with all missing entries.
- Updated all version references to 0.0.1.
- Fixed stale dates across all documentation files.
- Added missing server-kit modules to `AGENTS.md` module table.

### Previous Development History

The following entries record the development history prior to the 0.0.1 reset.

#### Pre-reset: 1.2.0-dev

- Direct reflection-based serialization/deserialization for custom structs in
  `extension.Value` and `extension.FromJSON`.
- Queue capacity and queue current length tracking in `registry.MetricsSnapshot`.
- Transfer lane: progress-bearing upload/download lifecycle with bookend events,
  monotonic progress, and resumable multipart surface.
- Frontend command registry: generated route catalog, `createAppRuntime`,
  and typed dispatch with custom route support.
- Projection gateway: HTTP read surface for Hermes-backed projections.
- Frontend runtime workbench completion: dummy data, tenant stores, live
  projections, runtime adapters, and prototype generator.
- Fixed timer leak in worker retry backoff loops.

#### Pre-reset: 1.1.0

- **policy**: Policy-as-code authorization engine (Cedar-inspired).
- **redis**: Native Redis client integration for server-kit.
- **worker**: River-based background job handling infrastructure.
- **docgen**: Automated documentation generation for generated projects.
- Updated tech stack standards to Go 1.26, React 19.2, TypeScript 5.9+,
  Rust 1.95, PostgreSQL 18, Redis 8.

#### Pre-reset: 1.0.0

- Initial release with server-kit modules: `circuitbreaker`, `featureflags`,
  `tracing`, `policy`, `retry`, `healthcheck`, `errors`, `cache`,
  `degradation`, `versioning`.
- Project bootstrapper (`init.sh`) with profile support.
- Update mechanism (`update-project.sh`).
- Foundation documentation set.
