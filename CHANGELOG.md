# Changelog

All notable changes to the Ovasabi Foundation will be documented in this file.

## [1.1.0] - 2026-05-03

### Added
- **policy**: Policy-as-code authorization engine (Cedar-inspired)
- **redis**: Native Redis client integration for server-kit
- **worker**: River-based background job handling infrastructure
- **cognitive-wire**: Stealth extension for AI compute and CWF transport
- **docgen**: Automated documentation generation for generated projects

### Changed
- Updated tech stack standards to Go 1.25, React 19.2, TypeScript 6.0, Rust 1.95, PostgreSQL 18, Redis 8
- Refined **AGENTS.md** with clearer terminology (Core vs Project vs Template)
- Formalized Foundation Dependency Boundary rules

### Fixed
- Sync issues between template scaffold and foundation core package boundaries

## [1.0.0] - 2026-04-21

### Added

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

### Documentation
- Foundation guide with quick start
- Module usage examples
- Coding practices reference
- Database practices reference
- Redis practices reference
- Migration practices reference

---

## Future Roadmap

### Planned for 1.1.0
- Redis cache backend implementation
- Remote feature flag source (HTTP)
- OPA policy engine adapter
- Prometheus metrics integration

### Planned for 1.2.0
- gRPC interceptors for all modules
- Kubernetes health probe integration
- Distributed cache invalidation
- A/B testing infrastructure
