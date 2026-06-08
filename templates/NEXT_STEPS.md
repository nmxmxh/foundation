# Next Steps for PROJECT_NAME

Foundation gave you production-grade infrastructure. Now define what makes your app unique.

## Quick Start

```bash
make setup        # Install dependencies
make docker-up    # Start Postgres + Redis
make dev          # Start development server
```

## What Foundation Provides

- [x] Event-driven WebSocket server with compressed binary envelopes
- [x] JWT authentication with capability-based access control
- [x] Redis-backed event bus for async processing
- [x] River workers for background jobs
- [x] Database migrations with tenant isolation
- [x] 31 coding practices for AI assistants (AGENTS.md)

## What You Need to Define

### 1. Domain Contracts (First Priority)

Your app needs domain-specific protos. Foundation provides transport; you define business logic.

```bash
# Create your first domain
mkdir -p api/protos/user/v1
# Copy template
cp api/protos/_template/v1/example.proto api/protos/user/v1/user.proto
# Edit and customize, then generate app + foundation communication contracts
make communication-contracts
```

**Identify your 3-5 core domains.** Examples:
- Fintech: `user`, `business`, `billing`, `tax`, `compliance`
- Media: `workspace`, `media`, `publish`, `identity`
- Civic: `incident`, `report`, `evidence`, `verification`, `geo`

See `.agents/DOMAIN_GUIDE.md` for patterns and naming conventions.

### 2. Service Implementation

Once protos exist, create service handlers:

```
internal/service/<domain>/
├── service.go        # Business logic
├── registration.go   # Event handler registration
└── repository.go     # Data access (optional)
```

### 3. Frontend Integration

Your frontend gets two generated paths from the same app protos:

- `frontend/src/types/protos` contains protobuf TypeScript payloads for backend
  service calls.
- `frontend/src/generated/prototypeRuntime.ts` contains schema-derived dummy
  data, tenant stores, live projection bindings, hooks, fixture states, and
  benchmark fixtures.

Run all communication generation together:

```bash
make communication-contracts
```

The scaffolded `frontend/src/stores/prototype.ts` creates an offline
`offlinePrototypeRuntime` from generated stores. Use that path for UI
prototypes, fixture-driven tests, and offline workflows before backend handlers
are complete. When Hermes/live projection wiring is ready, pass a projection
source to `createPrototypeRuntimeContext`; generated UI code should still read
the same tenant stores.

Transport calls go through `@ovasabi/runtime-transport`, which owns WebSocket,
HTTP fallback, binary envelopes, compression, and route metadata. Domain
payloads are generated from your app protos into `frontend/src/types/protos`.

```typescript
import { createEnvelope } from '@ovasabi/runtime-transport'
import { LoginRequest } from './types/protos/user/v1/user'

const payload: LoginRequest = { email, password }
const envelope = createEnvelope({
  eventType: 'user:login:v1:requested',
  payload,
})
```

See `foundation/runtime-transport/ts/` for TypeScript envelope utilities.

The browser WASM template is a compatibility shim for legacy globals. Shared
memory and low-latency compute should use the Rust `foundation/runtime-sdk`
path, generated Cap'n Proto runtime contracts, workers, and benchmarks. Treat
performance as measured evidence from your generated fixtures and target
browsers, not a hard guarantee from the scaffold.

Frontend prototype evidence commands:

```bash
make test-frontend
make test-bench-frontend
SCAFFOLD_SMOKE_FRONTEND=1 SCAFFOLD_SMOKE_INSTALL=1 make -C foundation check-scaffold-smoke
```

The scaffold smoke frontend path captures install/build/test logs, build/test
timings, and bundle-size metrics under `foundation/test-results/` unless
`SCAFFOLD_SMOKE_ARTIFACT_DIR` is provided.

### 4. Local Full Test Path

Use the inherited local path when you need to prove the app still works across
generated contracts, backend code, frontend prototype stores, runtime/WASM
artifacts, Hermes, Redis, and Postgres:

```bash
make test-local-full
```

That command regenerates communication contracts, runs Go unit tests, rebuilds
runtime/WASM artifacts and the manifest, runs frontend tests through the
Foundation runner, starts the scaffolded Docker test services, applies
migrations, and runs integration tests with infrastructure marked as required.
If the frontend declares an `e2e` or `test:e2e` script, it also runs that
browser path; otherwise the e2e hook is skipped explicitly.

Keep heavier service evidence opt-in:

```bash
make test-local-services
```

This extends the local full path with load tests and benchmark/allocation
evidence. Use it before large runtime, transport, store, worker, or database
changes, and capture logs in your normal artifact directory when running it in
CI or a release branch.

## Detailed Checklist

For a comprehensive checklist, see:
- `.agents/POST_INIT.md` - Step-by-step initialization guide
- `.agents/DOMAIN_GUIDE.md` - Domain definition patterns
- `docs/operations/README.md` - Production readiness, DORA delivery metrics, and incident records

## Operational Readiness

Before production, keep the scaffold defaults aligned with the Foundation security and delivery model:

- Set `APP_ENV=production`, keep `REQUIRE_AUTH=true`, and keep `PROTECT_OPERATIONAL_ENDPOINTS=true`.
- Set `ALLOWED_ORIGINS` to exact production origins only. Do not use wildcard CORS for authenticated routes.
- Keep `/metricsz`, `/metricsz/trace`, and operational event views behind authenticated operator/admin access.
- Preserve the CI `delivery-metrics` artifact and forward it to your observability or warehouse layer once deployment is wired.
- Record production incidents, failed deployments, rollbacks, and hotfixes with `docs/operations/incident_record_template.md`.

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                     Your Application                         │
├─────────────────────────────────────────────────────────────┤
│  api/protos/         │  internal/service/  │  frontend/     │
│  (Your Contracts)    │  (Your Logic)       │  (Your UI)     │
├─────────────────────────────────────────────────────────────┤
│                     Foundation Layer                         │
├─────────────────────────────────────────────────────────────┤
│  server-kit/         │  runtime-transport/ │  config-       │
│  (Server, Events,    │  (Envelopes,        │   contracts/   │
│   Compress, Auth)    │   Metadata)         │  (Validation)  │
└─────────────────────────────────────────────────────────────┘
```

## Communication Pattern

Foundation uses **envelope-based runtime transport**:

1. **Client sends**: generated protobuf payload through a runtime envelope
2. **Server dispatches**: Routes to registered handler by event_type
3. **Handler processes**: Business logic, database, external calls
4. **Server responds**: Same correlation_id, success/failed state

The transport layer chooses the available lane (`sab`, `wasm`, `transferable`, `ws`, `http`, `postMessage`) and applies compression/binary framing where supported.

## Key Files

| File | Purpose |
|------|---------|
| `AGENTS.md` | AI assistant instructions (31 coding practices) |
| `CLAUDE.md` | Claude-specific project context |
| `.agents/POST_INIT.md` | Detailed initialization checklist |
| `.agents/DOMAIN_GUIDE.md` | How to define domains and protos |
| `api/README.md` | API boundary documentation |
| `api/protos/README.md` | Proto contract rules |
| `docs/foundation/` | Full coding practices documentation |

---

*Generated by Foundation v{{FOUNDATION_VERSION}}*
*Delete this file once you've completed initial setup.*
