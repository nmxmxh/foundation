# Changelog

All notable changes to Foundation will be documented in this file.

## [Unreleased]

### Added

- **wsrouting**: `Router.UpdateAuthState(ctx, connID, AuthState)` to atomically rotate a connection's full security context (user, organization, role, session) — e.g. after a context switch — plus `Authenticated`/`OrganizationID`/`Role`/`SessionID` fields on `ConnectionInfo`. `UpdateAuth` is retained as a back-compat wrapper. Prevents the gateway from authorizing frames against a stale org context.
- **auth**: `ClockSkewLeeway` (60s) applied to JWT expiry validation so a freshly minted token is not rejected as expired by a validator whose clock runs slightly ahead — avoids spurious unauthorized/logout in multi-pod deployments.
- **runtime-transport**: `createOfflineQueue` now strips the auth credential from queued envelopes on enqueue and re-stamps a fresh one on drain (via `resolveAuthToken`), so requests replayed after a reconnect/token rotation never carry a stale token. New `authTokenExtraKeys`/`resolveAuthToken` options.
- **runtime-transport**: `createWebSocketTransport` gains an `authenticate(session)` re-authentication hook that runs on every (re)connect before readiness, re-subscribes, and queue drain — closing the window where a freshly reconnected socket serves frames unauthenticated.
- **templates/frontend**: reference `useIdleLogout` hook (keyed on session presence, not token, to survive token rotation), shared `sessionActivity` helpers (cleared on logout), and a reference service worker (`public/sw.js`) + `registerServiceWorker` that serves content-hashed immutables cache-first and the navigation shell network-first.

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
