# Ovasabi Foundation Project Intelligence

> Universal instruction file for AI coding assistants (Claude, Codex, Cursor, Copilot, Windsurf, etc.)

## Project Context

This is an **Ovasabi Foundation** project - a production-grade full-stack application using Go backend, TypeScript/React frontend, and Rust/WASM for high-performance compute. The foundation provides shared infrastructure for event-driven, tenant-isolated, realtime applications.

## Tech Stack

| Layer | Technology | Version |
| ------- | ------------ | --------- |
| Backend | Go | 1.24+ |
| Frontend | TypeScript, React | 5.x, 19.x |
| High-Performance | Rust, WASM | 1.87+ |
| Database | PostgreSQL | 16+ |
| Cache/Pubsub | Redis | 7+ |
| Queue | River (Go) | Latest |
| Protocol | Protocol Buffers, Cap'n Proto | 3.x |

## Architecture Overview

```text
project/
├── cmd/                    # Entry points (server, worker)
├── internal/               # Application logic
│   ├── service/           # Domain services
│   ├── worker/            # Background job handlers
│   └── startup/           # Initialization
├── api/                    # Protocol definitions
│   ├── protos/            # Protobuf schemas
│   └── schemas/           # Cap'n Proto schemas
├── frontend/              # React SPA
│   └── src/
│       ├── features/      # Feature modules
│       ├── components/    # Shared UI
│       ├── runtime/       # WASM bridge, transport
│       └── stores/        # State management
├── docs/
│   └── foundation/        # Foundation practices and guides (copied at init)
├── foundation/            # Shared infrastructure (READ-ONLY reference)
│   ├── server-kit/go/     # Backend modules
│   ├── runtime-transport/ # Client transport
│   └── runtime-sdk/       # WASM kernel
└── rust/                  # Native Rust crates
```

## Critical Rules (Mandatory)

### Foundation Metadata And Build Output

`.foundation` is tracked project metadata. Foundation tooling refreshes `LAST_UPDATED`, so a timestamp-only diff in that file is expected metadata churn, not an ignore-rule issue.

Vendored foundation modules must carry `foundation/.gitignore`. Rust build output under `foundation/runtime-sdk/rust/target/` is generated and must stay ignored.

### 1. Correlation ID Propagation

Every mutating command MUST carry a `correlationId`. Trace it through all workers, events, and logs.

```go
// CORRECT
ctx = metadata.WithCorrelationID(ctx, envelope.CorrelationID)
bus.Publish(ctx, "domain:action:requested", payload)

// WRONG - loses traceability
bus.Publish(context.Background(), "domain:action:requested", payload)
```

### 2. Event Contract Lifecycle

All domain events follow `<domain>:<action>:<state>` pattern:

- `:requested` - Command received, validation passed
- `:success` - Operation completed
- `:failed` - Operation failed with reason

Workers subscribe to `:requested`, emit `:success` or `:failed`.

### 3. Tenant Isolation

Never trust client-supplied `organization_id`. Always derive from authenticated context:

```go
// CORRECT
orgID := auth.OrgIDFromContext(ctx)

// WRONG - client can forge
orgID := req.OrganizationID
```

### 4. Error Handling

Use the foundation error taxonomy. Never panic in request handlers:

```go
import "foundation/server-kit/go/errors"

// Categorized errors with HTTP mapping
return errors.NotFound("user not found").WithField("user_id", id)
return errors.Validation("invalid email").WithField("field", "email")
return errors.Forbidden("insufficient permissions")
```

### 5. Bounded Operations

All loops, retries, and external calls MUST have explicit bounds:

```go
// Retries with policy
retry.HTTPRetry().Do(ctx, func() error {
    return externalAPI.Call()
})

// Context deadlines
ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
defer cancel()
```

## Commands

```bash
# Build
make build                    # Build all binaries
make frontend-build          # Build frontend

# Test
make test                    # Run all tests
make test-go                 # Go tests only
make foundation-test         # Foundation module tests

# Lint
make lint                    # All linters
make lint-go                 # Go lint (golangci-lint)
make lint-frontend           # ESLint + TypeScript

# Generate
make proto                   # Generate Go from .proto
make proto-ts                # Generate TypeScript from .proto
make generate-contracts      # All code generation

# Database
make migrate-up              # Run migrations
make migrate-reset           # Reset and re-run

# Docker
make docker-up               # Start dev containers
make docker-down             # Stop containers
make docker-logs             # Follow logs

# Verify (CI pipeline)
make verify                  # Full verification suite
```

## Server-Kit Modules

### Circuit Breaker

Protect external service calls from cascading failures:

```go
import "foundation/server-kit/go/circuitbreaker"

cb := circuitbreaker.New("payment-gateway", circuitbreaker.Config{
    FailureThreshold: 5,
    SuccessThreshold: 2,
    Timeout:          30 * time.Second,
})

result, err := cb.Execute(ctx, func() (interface{}, error) {
    return paymentClient.Charge(amount)
})
```

### Feature Flags

Control feature rollouts with targeting:

```go
import "foundation/server-kit/go/featureflags"

if flags.IsEnabled(ctx, "new-checkout",
    featureflags.WithUser(userID),
    featureflags.WithOrg(orgID),
) {
    // New flow
}
```

### Distributed Tracing

OpenTelemetry integration:

```go
import "foundation/server-kit/go/tracing"

ctx, span := tracing.Start(ctx, "operation-name")
defer span.End()
```

### Health Checks

Liveness and readiness probes:

```go
import "foundation/server-kit/go/healthcheck"

hc := healthcheck.New(healthcheck.Config{ServiceName: "my-service"})
hc.AddCheck("database", healthcheck.DatabaseCheck(db))
hc.AddCheck("redis", healthcheck.PingerCheck(redis, "redis"))
http.Handle("/health", hc.Handler())
```

### Cache Patterns

Cache-aside with invalidation:

```go
import "foundation/server-kit/go/cache"

user, err := cache.GetOrSet(ctx, c, "user:123", func() (*User, error) {
    return db.GetUser(ctx, 123)
}, cache.DefaultTTLPolicy().Medium)
```

### Graceful Degradation

Handle dependency failures:

```go
import "foundation/server-kit/go/degradation"

sentinel := dm.Sentinel("redis")
if sentinel.Guard() {
    // Use Redis
} else {
    // Fallback
}
```

## Frontend Patterns

### Transport Layer

Use the command bus for all backend communication:

```typescript
import { createCommandBus, createEnvelope } from '@ovasabi/runtime-transport'

const bus = createCommandBus({ baseUrl: '/api' })
const envelope = createEnvelope('media:upload:requested', payload, {
    correlationId: generateId()
})
await bus.dispatch(envelope)
```

### State Stores

Use Zustand stores with deduplication:

```typescript
import { createEventStore } from '@ovasabi/runtime-transport'

const eventStore = createEventStore()
// Automatic request deduplication
eventStore.subscribe('media:upload:success', handler)
```

### UI Components

Always use shared primitives from `foundation/ui-minimal`:

```typescript
// CORRECT - use foundation primitives
import { MinimalButton, MinimalInput } from '@ovasabi/ui-minimal'

// WRONG - page-local styled components for common patterns
const MyButton = styled.button`...`
```

App-level `src/components/ui/*` files should wrap `Minimal*` primitives with product naming, icons, and brand defaults. They must not reimplement button, input, card, table, modal, skeleton, dropdown, or header structure from scratch.

Consume `ui-minimal` only through the package boundary:

```typescript
import { MinimalThemeProvider } from '@ovasabi/ui-minimal'
```

Do not alias or import raw files from `foundation/ui-minimal/ts/src/*`. Frontend Vite, Vitest, and TypeScript configs must set `preserveSymlinks: true` so the local `file:../foundation/ui-minimal/ts` dependency resolves React, styled-components, and framer-motion through the frontend package boundary.

### Frontend Contract Types

Use generated protobuf TypeScript as the domain contract source:

```bash
make proto-ts
```

Generated types live under `frontend/src/types/protos/`. Do not hand-write request, response, event, route, geo, safety, report, or identity contract types in `frontend/src/types/*` when a protobuf source exists under `api/protos`.

### Frontend Operations Kit

Use `@ovasabi/frontend-kit` for app operational state:

```typescript
import {
  createFoundationMetadata,
  createIndexedDBStorage,
  createRuntimeExternalStore,
  createStoreResetRegistry,
} from '@ovasabi/frontend-kit'
```

The kit is the baseline for IndexedDB-backed persistence, metadata normalization, store reset handles, and runtime/WASM snapshot hooks. Do not copy app-local versions of these utilities from another product into a new app; wrap the kit with app-specific generated contract types instead.

## Gotchas and Anti-Patterns

### Never Do These

1. **Don't ignore context deadlines**

   ```go
   // WRONG
   go func() {
       heavyOperation() // No context, no timeout
   }()
   ```

2. **Don't use MutationObserver for state**

   ```typescript
   // WRONG - DOM observation for business logic
   new MutationObserver(() => updateAuthState())
   ```

3. **Don't store secrets in frontend**

   ```typescript
   // WRONG
   const API_KEY = 'sk-...'  // Never in frontend bundle
   ```

4. **Don't trust client input**

   ```go
   // WRONG - SQL injection risk
   db.Query("SELECT * FROM users WHERE id = " + req.ID)
   ```

5. **Don't skip error wrapping**

   ```go
   // WRONG - loses context
   return err

   // CORRECT
   return errors.Wrap(err, "failed to process order").WithField("order_id", id)
   ```

### Always Do These

1. **Use structured logging with correlation**

   ```go
   logger.Info("processing order",
       "correlation_id", ctx.Value("correlation_id"),
       "order_id", orderID)
   ```

2. **Validate at boundaries**

   ```go
   if err := validateRequest(req); err != nil {
       return errors.Validation(err.Error())
   }
   ```

3. **Use idempotency keys for mutations**

   ```go
   job := worker.Job{
       Kind:           "payment:process",
       IdempotencyKey: fmt.Sprintf("payment:%s", paymentID),
   }
   ```

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

## Testing Requirements

- **Unit tests**: All new logic, including failure paths
- **Integration tests**: Critical flows and event contracts
- **E2E tests**: User journeys and auth guards
- **Coverage targets**: Line ≥80%, Branch ≥60%

Run tests before every commit:

```bash
make test && make lint
```

## Security Checklist

Before merging any PR, verify:

- [ ] No secrets in code or logs
- [ ] Input validation at all boundaries
- [ ] Authorization checks on target objects (not just routes)
- [ ] Rate limiting on exposed endpoints
- [ ] CORS configured with explicit origins
- [ ] Correlation IDs propagated for audit trails

## When Uncertain

1. Check `docs/foundation/` for established patterns
2. Look at similar implementations in `internal/service/`
3. Follow the principle: explicit > implicit, bounded > unbounded
4. When adding external calls, wrap with circuit breaker + retry
5. When adding UI, check `foundation/ui-minimal`, `docs/foundation/styling_design_practices.md`, and `docs/foundation/references/README.md`
6. Before adding frontend API types, run `make proto-ts` and import from `frontend/src/types/protos`
7. Before adding frontend IndexedDB, metadata, store reset, or runtime hook utilities, check `foundation/frontend-kit`

---

*This file is auto-generated by foundation bootstrap. Keep it updated when practices change.*
*Version: 1.0.0 | Last updated: 2026-04-21*
