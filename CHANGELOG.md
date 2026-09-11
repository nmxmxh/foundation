# Changelog

All notable changes to Foundation will be documented in this file.

## [Unreleased]

### Added

- **docs**: `ui_render_performance_research.md` — research handoff for the DOM/WebView rendering gap, the one axis Foundation's zero-copy data lanes do not cover. It maps Chromium (Android WebView) and WebKit (WKWebView) rendering models, mobile tile-based GPUs, and AAA frame-budget practice onto Foundation's existing primitives (`frameClock`, render surface ladder, `renderMarks`, `ui-minimal` motion, the styling doc's Motion Design System), and proposes ten next-phase DOM-lane primitives and rules with budgets aligned to Android vitals. ChooseChow is recorded as the first-adopter baseline. Research only: nothing is promoted until its owning document adopts it.
- **templates/native**: fix `beforeDevCommand`/`beforeBuildCommand` — Tauri runs them from `native/`, not `src-tauri/`, so the template's `cd ../../frontend` left the project and `tauri dev`, `tauri build`, and every mobile build failed before compiling anything. Hooks now `cd ../frontend`; `frontendDist` (resolved from `src-tauri/`) stays `../../frontend/dist`. `project_scaffold_check.sh` fails on the old path.
- **templates/native**: add the `"tauri": "tauri"` npm script. The Xcode "Build Rust Code" phase and the Android Gradle `BuildTask` that `tauri ios/android init` generate both run `npm run -- tauri <platform> *-script`, so without it every mobile build failed inside Xcode/Gradle with `Missing script: "tauri"`. Guarded by the scaffold check and `tests/init_project_test.sh`.
- **templates/native**: mobile release baseline. Tauri pins move to `tauri =2.11.5` / `tauri-build =2.6.3` / CLI `2.11.4` (patch releases; no fork to rebase). The template gains `ios:*`/`android:*` scripts, a `[profile.release]` (LTO, one codegen unit, `opt-level = "s"`, `strip`, `panic = "abort"` so panics never unwind across the Swift/Kotlin boundary), `object-src 'none'; base-uri 'self'` in every CSP overlay, and a README covering current mobile prerequisites (JDK 17/21 for Gradle 8.14, SDK 36, build-tools 35, NDK r28+ for Play's 16 KB page-size rule), signing, and a post-`mobile:init` hardening checklist for the Tauri-generated `gen/` projects. See `docs/runtime_native.md` § Mobile Release Hardening.
- **tooling**: `project_scaffold_check.sh` native checks now accept any exact `=2.x.y` Tauri pin instead of hard-coding one version, match the capability list whitespace-insensitively (a formatter splitting `["main"]` across lines failed the check), forbid inline scripts in the release CSP outright, and allow release `style-src 'unsafe-inline'` only as an exception documented in `native/README.md` — CSS-in-JS apps (styled-components) cannot ship without it.

- **hermes**: `RuntimeStoreOptions.OnCommandWrite` — the seam a projection uses to learn that another process moved a scope, without asking the database whether anything happened. A projection is filled by the writes its own process applied, so the worker, a second replica, and a migration are all invisible to it; the alternative everybody reaches for is a trigger calling `pg_notify`, and it is measurably the wrong answer on a hot table. `tooling/scripts/record_store_notify_bench.sh` puts numbers on it: on PG18 the record store's real upsert sustains 27,199 tps at 64 clients, and 6,027 with a notify trigger — **78% of write throughput, and flat from 8 clients onward**, because committing a transaction that notified takes a global exclusive lock. Per-statement and per-row triggers cost the same, since the lock is taken per committing transaction and not per notification; the usual "batch your notifications" advice cannot help a trigger. The hook is called only for command writes — `MirrorSweeper` convergence writes are excluded, because a replica that rebroadcasts what it just converged makes fleet traffic grow with the square of the fleet. See `docs/database_practices.md`.
- **hermes**: `MirrorSweeper.SweepSource(ctx, name)` and `SourceNames()` — converge one named scope instead of polling every registered source. A caller holding a change signal already knows which scope moved; without this, one event costs one query per source. Sources sweep under their own lock, so a signal-driven sweep and a periodic `SweepOnce` cannot interleave on a cursor.
- **hermes**: `ScopeSignal` — the assembled change-notification lane, so a project wires one thing instead of three and gets the answers rather than the questions. `NewScopeSignal(ScopeSignalOptions{Bus: bus})` yields an `OnCommandWrite` hook for the store and a `Converge(sweeper)` call for the subscription. Each half stays off the goroutine that reached it: announcing hands a scope name to a buffered channel so no commit waits on Redis, and receiving flags a source so no bus dispatch waits on Postgres — a bus dispatches every subscription from one goroutine, so a subscriber that swept inline would put a database round trip in front of every other event the process was about to handle. The lane also closes four failure modes that each look like working code: the node identity is random rather than a clock (two processes started together read the same clock, and two nodes sharing an id each mistake the other's announcements for an echo of their own), the payload is materialized before it is read (a bus leaves the payload as bytes, and reading through the nil payload yields empty strings rather than an error, so the subscriber silently discards everything), announcements naming an unregistered source are counted as `Unroutable` rather than published, and `AddScopeSource`/`AddScopeDeleteRecordSource` register under the name the announcement derives, so the two halves cannot disagree about a name.
- **hermes**: `MirrorSweeper.Notify(name)` — non-blocking, coalescing signal intake, and the intake a change signal should use instead of `SweepSource`. It sets a flag and returns; `Run` converges the flagged sources on its next turn without restarting its reconcile interval. Signals arriving while a sweep runs collapse onto the flag it left, because the sweep that follows reads everything past the cursor anyway — so a burst of *k* writes costs one sweep rather than *k*, on each of *n* replicas, all of which receive every announcement. `MirrorSweepStats` gains `Signalled`, `Coalesced` and `Unroutable`: a converged write with `Signalled` still at zero means the timer did it and the signal lane is dead.
- **tooling**: `record_store_notify_bench.sh` — measures what change notification costs `governance_state_records` on your own hardware. Resets the table before every run and attaches a real listener, because bloat from a previous run and a trigger that silently failed to install both benchmark as "notification is free" — both of which happened while writing it.

- **httpserver**: `Server.OnConnectionClosed(func(ctx, ConnectionClosed))` — the application is told when a WebSocket connection ends, with the account and device that held it. Any domain that ties state to connection lifetime needed this and had no way to get it: presence in a room, a held lock, a live cursor, a subscription with a side effect. Without a disconnect signal such state can only be expired by a timer, so a roster is permanently a little wrong and a lock outlives the process holding it — and the timer has to be generous enough to survive a throttled background tab, which makes the window worse. The callback runs off the teardown path under `WSDisconnectBudget` (10s) on a context detached from the connection's own, because cleanup that runs on a cancelled context does not run at all. It fires only for a connection that was authenticated: an anonymous socket holds no application state to release. `ConnectionClosed` is a struct so a later field — a close reason, a last-seen timestamp — does not break every caller.

- **database**: `CachedStateStore` — shared, stampede-safe cache-aside point reads over any `StateStore` (Postgres included). Opaque `json.RawMessage` payloads parsed back through the extension lane preserve int64/raw-byte fidelity; version-guarded refreshes block out-of-order commit clobbering; negative caching, per-op timeout bounds, and full degradation to Postgres on any cache error. Includes allocation budget guard (`TestAllocationBudgetCachedHit`) plus hit/miss/envelope benchmarks.
- **database**: `AsPostgresDB` unwraps bounded wrapper chains (hermes, cache) to the concrete pool-bearing store without new constructor signatures.
- **hermes**: point-read and point-write benchmarks with allocation reporting (`BenchmarkHermesPointReadHotPartition` ~630 ns / 3 allocs), establishing the layering baseline that keeps warm partitions ahead of added cache layers.
- **templates/backend**: `RIVER_DIRECT_URL` routes both River pools through a session-mode endpoint bypassing PgBouncer transaction pooling, restoring native LISTEN/NOTIFY wakes (Option A of the read-pressure handoff); `STATE_STORE_CACHE` (`off|memory|redis` + TTL knobs) wires `CachedStateStore` for direct StateStore consumers with first-fallback degradation logging.
- **scripts**: `db_diagnostics.sh` — parameterized read-pressure evidence collector (pg_stat leaders, river_job state/queue backlog, oldest job age) with no hardcoded hosts or credentials.

### Changed

- **vitest 5**: `templates/frontend`, `runtime-transport/ts`, `runtime-sdk/ts/browser-host`, and `runtime-native/ts` move to `vitest`/`@vitest/coverage-v8` `^5.0.0`. With vitest 5 as `latest`, a `^4` manifest made npm resolve vitest 5 through optional peers (`@vitejs/devtools-vitest` peers `vitest@*`), and npm 11 crashed on the mixed-major peer set with `Cannot read properties of null (reading 'edgesOut')` — `./init.sh` aborted at the frontend lockfile sync. `frontend_manifest_sync.mjs` now pins `vitest` to the template range and moves `@vitest/coverage-v8` in lockstep when present, so `foundation-update` carries existing apps across the major. Vitest 5 dropped the top-level `bench` export: every `*.bench.ts` now registers through the `bench` test fixture (`test(name, async ({ bench }) => bench.compare(...))`), and bench invocations pass `--reporter=verbose`, since the default reporter no longer prints the results table.
- **scripts**: `init.sh --skip-deps` no longer runs `npm install --package-lock-only` after the frontend manifest sync; it warns that the lockfile needs a manual `npm install`.
- **tooling**: `generate_frontend_prototype_runtime.mjs` fails on entity-name and schema-key collisions instead of emitting a file that does not compile. Identifiers derive from the bare message name, so two domains declaring `Subscription` both emitted `subscriptionSchema`, `useSubscriptionSnapshot`, … (TS2451), and `make check-contract-drift` still passed, because `--check` only compared the committed file against a fresh, equally broken generation. The error names both proto files and their qualified messages, and covers the same entity in two versions of a domain and two messages mapping to one `domain.collection` key. Colliding names are not auto-namespaced, since that would rename identifiers an existing domain already exports; output for non-colliding protos is byte-identical. Guarded by `tests/frontend_prototype_generator_test.sh`.
- **server-kit/servicebacked**: the projection-convergence `idle` leg no longer sleeps 2s. It now runs last and asserts that every reader poll since its first pass was a signalled sweep (polls == `Stats().Signalled` delta). A timer or self-driven pass polls without being signalled, so the check covers the whole multi-second run, reconcile leg included, instead of a dedicated wall-clock window.
- **templates/backend**: startup now initializes the event bus before the database so the state-store cache can share its Redis client; cleanup ordering is preserved.
- **deps**: `riverqueue/river` v0.35.0 → v0.44.1 across `server-kit/go` (direct) and `runtime-sdk/go` (indirect). Builds and worker/database suites pass unchanged — no API surface we use moved. Deployment gate: run migration 7 (`river migrate-up`) during a brief worker stop window; gains PgBouncer simple-protocol JSON hardening (#1153), per-queue `FetchCooldown`/`FetchPollInterval`, full reindexer coverage for previously bloated `river_job` indexes, jittered poll loops, and `JobDeleteMany` backlog tooling.
- **events**: `RedisBus` node identity is now random rather than `time.Now().UnixNano()`. It is what decides whether an inbound message is this bus's own echo, so two buses that shared an id would each silently drop every message from the other — for every event type, not just the one being debugged. Two processes started together on one host can read the same clock, and the coarser the platform's timer, the likelier.
- **hermes**: `MirrorSweeper` registers sources in a name-indexed registry under a lock. Lookup by name is now O(1) rather than a scan, registration racing a lookup is no longer a data race, and a duplicate source name is rejected at registration instead of silently making the second source unreachable — a signal naming it would have converged the first instead, which looks exactly like convergence.
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

- **httpserver**: WebSocket subscription never worked for an application that had not been told two undocumented things. `system:websocket_subscribe:v1:requested` and its unsubscribe counterpart were handled *after* `performDispatch`, so a subscribe was first offered to the service registry — where an application has no handler for it, because connection control is not an application concern. Dispatch answered `handler_not_found`, the function returned on its error path, and the branch that records the subscription was unreachable. Separately, both events were gated on `wsUnauthenticatedAllowset`, so even with a handler registered a socket also had to have them allow-listed. Net effect: the connection stayed subscribed to nothing, `forwardEventToConnections` matched no pattern, and **no event was ever pushed to any client** — the entire push lane was silently dead, and applications that needed live state polled instead. This package's own tests did not catch it because `newWSTestServer` registers a no-op handler for both events *and* allow-lists everything it registers, so dispatch succeeded and execution fell through to the recording branch: the tests encoded the workaround rather than the contract. Control events are now answered by the socket before dispatch and are always permitted, for anyone. That permits subscribing, not receiving — delivery is filtered separately, and an event carrying a user id still reaches only that user's connections. `TestWSSubscribeWithoutApplicationHandlers` and `TestWSForwardingWorksWithoutApplicationHandlers` register nothing and fail on the old code.
- **httpserver**: a WebSocket connection ignored the identity the middleware had already verified. `security.JWTAuth` accepts the `access_token` query parameter on upgrades specifically — a browser cannot set an Authorization header on a handshake — and leaves validated claims on the request context, which the upgrade path never read. Every connection therefore began as a guest, and the only route to an identity was for the application to implement `identity:authenticate_connection:v1:requested` itself; until it did, any event addressed to an account could be delivered to nobody, which is every addressed event. Worse, that command is the sole auth path, so an application that implemented it carelessly — echoing a `user_id` from the client's own payload — authenticated the socket as anybody. The connection now seeds its identity from the request context, never from the query string: the middleware is the only thing that verifies a signature, and re-reading the raw parameter would be a second unverified path to the same decision. `maybeUpgradeConnectionAuth` still applies and still wins, which is what a token refresh on a live socket needs.

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
