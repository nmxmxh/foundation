# Ovasabi Foundation Intelligence

> Universal instruction file for AI coding assistants (Claude, Codex, Cursor, Copilot, Windsurf, etc.)

## Terminology

| Term | Definition |
| :--- | :--- |
| **Foundation Core** | The shared infrastructure repository containing `server-kit`, `runtime-transport`, `runtime-sdk`, etc. |
| **Foundation Template** | The skeletal structure in `templates/` used to bootstrap new projects. |
| **Foundation Project** | A specific application (e.g., `trader_os`, `fintech_v1`) generated from the template. |
| **Foundation Reference** | The `/foundation` directory inside a **Project**, which is a local copy/reference to Core modules. |

## Project Context

This is an **Ovasabi Foundation** project - a production-grade full-stack application using Go backend, TypeScript/React frontend, and Rust/WASM for high-performance compute. The foundation provides shared infrastructure for event-driven, tenant-isolated, realtime applications.

## Tech Stack (2026 Standards)

| Layer | Technology | Version |
| ------- | ------------ | --------- |
| Backend | Go | 1.25+ |
| Frontend | TypeScript, React | 5.9+, 19.2+ |
| High-Performance | Rust, WASM | 1.95+ |
| Database | PostgreSQL | 18+ |
| Cache/Pubsub | Redis | 8+ |
| Queue | River (Go) | Latest |
| Protocol | Protocol Buffers, Cap'n Proto | 3.x |

## Architecture Overview

A **Foundation Project** is structured to separate shared infrastructure from application logic:

```text
project/
├── cmd/                    # Entry points (server, worker)
├── internal/               # Application logic
│   ├── service/           # Domain services (e.g., service/order, service/user)
│   ├── worker/            # Background job handlers
│   └── startup/           # App-specific initialization
├── api/                    # Protocol definitions
│   ├── protos/            # App-specific Protobuf schemas
│   └── schemas/           # Cap'n Proto schemas
├── frontend/              # React SPA
│   └── src/
│       ├── features/      # Feature modules
│       ├── components/    # Shared UI (wrapping foundation primitives)
│       ├── runtime/       # WASM bridge, transport adapters
│       └── stores/        # State management (Zustand + transport)
├── docs/
│   └── foundation/        # Foundation practices (copied from Core)
├── foundation/            # Shared infrastructure (READ-ONLY REFERENCE)
│   ├── server-kit/go/     # Backend modules
│   ├── runtime-transport/ # Client transport
│   ├── runtime-sdk/       # WASM kernel
│   ├── ui-minimal/        # UI Primitives
│   └── frontend-kit/      # Frontend operational utilities
└── rust/                  # Native Rust crates
```

## Critical Rules (Mandatory)

### 1. Foundation Dependency Boundary
**NEVER** import or alias raw source files from `foundation/*/ts/src` or internal Go packages.
- **Frontend**: Consume foundation logic via package boundaries: `@ovasabi/runtime-transport`, `@ovasabi/frontend-kit`, `@ovasabi/ui-minimal`.
- **Backend**: Use the `server-kit` module exports.

### 2. Correlation ID Propagation
Every mutating command MUST carry a `correlationId`. Trace it through all workers, events, and logs.
```go
// CORRECT
ctx = metadata.WithCorrelationID(ctx, envelope.CorrelationID)
bus.Publish(ctx, "domain:action:requested", payload)
```

### 3. Event Contract Lifecycle
All domain events follow `<domain>:<action>:<state>` pattern:
- `:requested` - Command received, validation passed
- `:success` - Operation completed
- `:failed` - Operation failed with reason

### 4. Tenant Isolation
Never trust client-supplied `organization_id`. Always derive from authenticated context via `auth.OrgIDFromContext(ctx)`.

### 5. Error Handling
Use the foundation error taxonomy (`foundation/server-kit/go/errors`). Never panic in request handlers.

### 6. Bounded Operations
All loops, retries, and external calls MUST have explicit bounds (timeouts, max attempts).

## Commands

```bash
# Build & Generation
make generate-contracts      # Full code gen (Protos -> Go/TS)
make build                   # Build all binaries
make frontend-build          # Build frontend production bundle

# Testing & Linting
make test                    # Run all tests (Go, TS, Rust)
make lint                    # Run all linters
make verify                  # Full CI verification suite

# Infrastructure
make docker-up               # Start dev stack (PG, Redis, etc.)
make migrate-up              # Run DB migrations
```

## Server-Kit Modules (Key Primitives)

| Module | Purpose |
| :--- | :--- |
| `errors` | Categorized error taxonomy with HTTP mapping. |
| `metadata` | Context-aware metadata (CorrelationID, TenantID, RequestID). |
| `auth` | RBAC and JWT context helpers. |
| `policy` | Cedar-inspired policy-as-code authorization. |
| `retry` | Standardized retry policies with exponential backoff + jitter. |
| `circuitbreaker` | Fault tolerance for external service calls. |
| `featureflags` | Targeted rollouts and environment-based overrides. |
| `tracing` | OpenTelemetry integration for distributed tracing. |
| `cache` | Cache-aside patterns with tag-based invalidation. |
| `healthcheck` | Liveness/Readiness probes for all dependencies. |
| `worker` | River-based background job handling. |

## Frontend & High-Performance

### Transport Layer
Use `@ovasabi/runtime-transport` for all backend communication. It handles binary envelopes, WebSocket routing, and automatic request deduplication.

### UI Primitives (`ui-minimal`)
Check `foundation/ui-minimal` before creating local components. Use `MinimalButton`, `MinimalInput`, `MinimalAppShell`, etc. App-level components should be thin wrappers.

### Runtime SDK (`runtime-sdk`)
The bridge for Rust/WASM execution. It uses a 4KB control-buffer contract for high-performance communication between the JS event loop and the WASM guest.

### Cognitive Wire (`cw`)
A stealth extension for shared AI compute, providing binary-optimized CWF (Cognitive Wire Format) transport and edge-native state replication.

## Communicative Connections

The Foundation modules are linked through a unified "Nervous System":

1.  **Envelopes**: Every message (HTTP, WS, Redis) is wrapped in a `RuntimeEnvelope` (`runtime-transport`).
2.  **Resilience Runtime**: The `server-kit/go/resilience` module coordinates health, circuits, and degradation across all backend modules.
3.  **WASM Kernel**: `runtime-sdk` provides the 4KB high-speed buffer for JS/Rust communication.
4.  **Config Sync**: `config-contracts` ensures frontend and backend use the same validated schema.

## Development Context

| Context | Purpose | Key Commands |
| :--- | :--- | :--- |
| **Core Dev** | Developing the Foundation modules themselves in this repo. | `make test`, `make lint` |
| **Template Dev** | Updating the blueprints in `templates/` and `scaffold.manifest.tsv`. | `make lint-foundation` |
| **Project Dev** | Building a specific app *using* a generated scaffold. | `make dev`, `make foundation-update` |

## Next Steps (Core Roadmap)

1.  **Stability**: Finalize `policy` and `redis` modules for 1.1.0 release.
2.  **Observability**: Integrate Prometheus metrics and OTel trace exporters by default.
3.  **Performance**: Optimize the 4KB WASM control plane for high-frequency signal processing.
4.  **Security**: Standardize Post-Quantum TLS helpers across all ingress points.

## Gotchas and Anti-Patterns

1. **Don't ignore context deadlines**: Always pass `ctx` to DB and external calls.
2. **Don't use MutationObserver for business state**: Use Zustand stores.
3. **Don't store secrets in frontend**: Use environment-injected config or secure storage.
4. **Don't skip error wrapping**: Use `errors.Wrap(err, "context")` to preserve stack traces.

## Testing Requirements
- **Unit tests**: ≥80% Line Coverage, ≥60% Branch Coverage.
- **Integration**: Mandatory for event contracts and critical flows.
- **E2E**: Required for auth guards and core user journeys.

## Coding Practice Rules (CP-*)

The foundation enforces 31 coding practices. Key ones to remember:

| Rule | Summary |
| ------ | --------- |
| CP-01 | No goto, no uncontrolled recursion |
| CP-02 | All loops/retries must be bounded |
| CP-03 | Functions ≤80 lines, complexity ≤15 |
| CP-04 | Never ignore error returns |
| CP-06 | Minimize mutable shared state |
| CP-10 | Events must be idempotent |
| CP-11 | All behavior needs tests |
| CP-16 | Adaptive concurrency, not fixed throttling |
| CP-18 | Rate limit all ingress APIs |
| CP-20 | Server-side authorization on every object |

Full reference: `docs/foundation/coding_practices.md` in generated apps, or `docs/coding_practices.md` in the foundation source repo.

## File References

For deeper context, read these files (`docs/foundation/` in generated apps, `docs/` in the foundation source repo):

| Topic | File |
| ------- | ------ |
| Coding rules | `docs/foundation/coding_practices.md` or `docs/coding_practices.md` |
| Database patterns | `docs/foundation/database_practices.md` or `docs/database_practices.md` |
| Redis patterns | `docs/foundation/redis_practices.md` or `docs/redis_practices.md` |
| Migration rules | `docs/foundation/migration_practices.md` or `docs/migration_practices.md` |
| Performance | `docs/foundation/optimization_points.md` or `docs/optimization_points.md` |
| Runtime architecture | `docs/foundation/runtime_foundation.md` or `docs/runtime_foundation.md` |
| Foundation guide | `docs/foundation/foundation_guide.md` or `docs/foundation_guide.md` |
| Styling and motion | `docs/foundation/styling_design_practices.md` or `docs/styling_design_practices.md` |
| Animation references | `docs/foundation/references/README.md` or `docs/references/README.md` |
| Design inspiration | `docs/foundation/coding_magic.md` or `docs/coding_magic.md` |

## Security Checklist

Before merging any PR, verify:
- [ ] No secrets in code or logs
- [ ] Input validation at all boundaries
- [ ] Authorization checks on target objects (not just routes)
- [ ] Rate limiting on exposed endpoints
- [ ] CORS configured with explicit origins
- [ ] Correlation IDs propagated for audit trails

---
*Version: 1.1.0 | Last updated: 2026-05-03*
