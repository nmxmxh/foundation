---
description: Ovasabi Foundation: Comprehensive Agent & Developer Guide
---

# Ovasabi Foundation: Comprehensive Agent & Developer Guide

The **Ovasabi Foundation** is the master template and infrastructure baseline for building independent, standalone applications in the ecosystem. It abstracts **communication mechanics**, **high-compute performance**, and **durable orchestration** so feature teams can iterate safely without creating shared production databases.

This guide is **self-contained** and designed to be copied into any application using the foundation.

---

## 1. Core Modules & Elements

### A. server-kit (Go)

* **Purpose**: Backend durable orchestration & decoupled messaging.
* **Core Services**:
  * **Event Bus**: Multi-driver (Redis/In-Memory) pattern matching for decoupled service communication. Highly focuses on `<domain>:<action>:requested/success/failed` lifecycle.
  * **Graceful Signalers**: Consistently formats error and success streams into conforming envelopes.

* **Extended Modules** (v1.0.0):

| Module | Package | Purpose |
|--------|---------|---------|
| **Circuit Breaker** | `circuitbreaker` | Fault tolerance for external service calls with configurable thresholds |
| **Feature Flags** | `featureflags` | Structured feature toggles with percentage rollouts and user targeting |
| **Distributed Tracing** | `tracing` | OpenTelemetry integration with correlation ID bridging |
| **Policy Engine** | `policy` | Cedar-inspired policy-as-code authorization |
| **Retry Policies** | `retry` | Exponential backoff with jitter and preset policies |
| **Health Checks** | `healthcheck` | Liveness/readiness probes with dependency checks |
| **Error Taxonomy** | `errors` | Categorized error codes with HTTP mapping |
| **Cache Patterns** | `cache` | Cache-aside with TTL policies and invalidation |
| **Graceful Degradation** | `degradation` | Health monitoring with automatic fallback behaviors |
| **API Versioning** | `versioning` | Header/path/query versioning with deprecation support |

### B. runtime-transport (TypeScript)

* **Purpose**: The universal client wire. Abstracts "How" we dispatch.
* **Key Services**:
  * **Stateless Bus**: `createEnvelope`, `createCommandBus`. Automatically manages falling back between WebSocket setups and HTTP streams.
  * **Stateful SDK (New)**: `createMetadataStore`, `createEventStore` (Zustand/Vanilla). Offers framework-agnostic singletons for request deduplication and implicit metadata carrying.

### C. runtime-sdk (Rust/WASM)

* **Purpose**: High-performance kernel for CPU-bound tasks.
* **Performance Model**: 4KB Fixed Shared Buffer to guarantee cache affinity and zero-copy pointer exchanges with zero Allocation Pressure.

### D. ui-minimal (TypeScript)

* **Purpose**: Shared structural UI primitives, semantic theme tokens, and reusable motion helpers.
* **Key Services**:
  * **Theme Layer**: `theme.tsx` defines semantic tokens, theme merging, and CSS variable export.
  * **Primitive Layer**: `primitives.tsx` defines reusable structural components that apps should wrap instead of re-implementing.
  * **Motion Layer**: `motion.ts` centralizes reduced-motion and shared micro/spring defaults.
* **Implementation Posture**:
  * New app and feature code should prefer grouped styled-component declarations in `const Style = { ... }`.
  * Loading shells, empty states, and route hydration boundaries should remain separate from business rendering.
  * See `styling_design_practices.md` and `docs/references/` for the detailed frontend standard.

---

## 2. Nuances & Guidelines for Development

### 1. Mandatory Correlation Propagation

* Every mutating command **must** carry a `correlationId`.
* Incoming streams reconcile by carrying this identifier through sub-routes or workers.

### 2. Client Stateful Pipeline Boundaries

* To prevent flooding endpoints with duplicates: Use the deduplication hooks provided by `createEventStore`.
* Avoid declaring ad-hoc `.dispatch` payloads without verifying if the Active Metadata Context bundle addresses the variables implicitly.

### 3. Compute Locality Rule

* Calculations involving massive bytes or mathematical loops belong in **`runtime-sdk`** via WASM, not the browser logic streams.

### 4. Hostile-Environment Security Rule

* Treat browser state, route params, websocket frames, uploaded files, webhook payloads, queue messages, and third-party responses as untrusted input.
* Model at least these attacker classes: anonymous user, authenticated user, tenant adversary, malicious integration/API consumer, and insider with partial infrastructure access.
* Every new exposed capability should define its trust boundary, sensitive assets, abuse controls, and audit expectations before implementation.

### 5. Change-Risk Hotspot Rule

* Treat complexity plus low coverage as change risk. If a touched method is a hotspot candidate, add tests or simplify it before layering more behavior onto it.
* New code should aim for line coverage >= 80%, branch coverage >= 60%, and CRAP-style hotspot scores below the high-risk threshold where the stack can calculate them.

### 6. DOM Observation Rule

* `MutationObserver` is exception-only. Prefer explicit props, stores, runtime events, `ResizeObserver`, or `IntersectionObserver` before watching DOM mutations.
* If observation is unavoidable, isolate it behind a small UI adapter, watch the smallest possible subtree, and disconnect it reliably on cleanup.

### 7. Frontend Styling And Motion Rule

* Follow the theme -> CSS variable -> primitive -> feature wrapper layering model.
* Prefer grouped styled-component declarations (`const Style = { ... }`) over long flat lists of standalone styled constants in app code.
* Keep route loaders, keyed loading state, and skeletons separate from domain rendering instead of collapsing them into one component.
* Read `styling_design_practices.md` and `docs/references/README.md` before introducing new interaction motion.

---

## 3. Standard Interaction Workflow

When adding endpoints or solving route crashes, follow these steps:

1. **Check Capability Scope**: Verify RBAC mapped through the `RouteRegistry` (`view`, `write`, `admin`).
2. **Model the Threat Boundary**: Identify actor type, sensitive assets, entry points, and trust boundaries before changing handler or client flow behavior.
3. **Attach Tenancy Safely**: Ensure context injection contains `organization_id` directly in the underlying dispatcher payload where necessary.
4. **Validate Contract Endpoints**: Standard state machines rely on `:requested` to trigger workers. Ensure correctly-spelled terminal flags match backend bindings.
5. **Re-check Object Access**: Enforce server-side authorization on the target object or aggregate, not just on the route name. Never trust client-supplied ownership or org IDs.
6. **Constrain Inputs and Side Effects**: Validate payloads, file uploads, outbound URLs, and background side effects with allowlists, size limits, timeouts, and idempotency where appropriate.
7. **Apply Edge Controls**: Attach rate limits, origin/CSRF policy, cache/replay boundaries, and webhook verification rules for any exposed route or socket flow.
8. **Record Security Signals**: Ensure privilege changes, token flows, upload decisions, and external callbacks have logs/audit trails and negative tests.
9. **Check Hotspot Risk**: Before changing complex code, inspect coverage plus complexity risk. Hotspot-style scores above the high-risk threshold require tests or simplification, not optimism.
10. **Avoid DOM-Driven State**: Do not use `MutationObserver` to infer auth, routing, or business state. If a third-party widget forces DOM observation, isolate it and prove cleanup behavior in tests.

---

## 4. Extended Server-Kit Module Usage

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

// With fallback
result, err := cb.ExecuteWithFallback(ctx, primaryFn, fallbackFn)
```

### Feature Flags

Control feature rollouts with targeting:

```go
import "foundation/server-kit/go/featureflags"

flags := featureflags.New(featureflags.Config{
    Source:         featureflags.NewEnvSource(),
    DefaultEnvironment: "production",
})

if flags.IsEnabled(ctx, "new-checkout",
    featureflags.WithUser(userID),
    featureflags.WithOrg(orgID),
) {
    // New checkout flow
}
```

### Distributed Tracing

Integrate with OpenTelemetry:

```go
import "foundation/server-kit/go/tracing"

tp, _ := tracing.NewProvider(tracing.Config{
    ServiceName: "my-service",
    Endpoint:    "localhost:4317",
    SampleRate:  0.1,
})
defer tp.Shutdown(ctx)

// Start spans that chain with correlationId
ctx, span := tracing.Start(ctx, "operation-name")
defer span.End()

// Bridge with existing correlation system
ctx = tracing.WithCorrelationID(ctx, correlationID)
```

### Policy Engine

Define authorization policies:

```go
import "foundation/server-kit/go/policy"

engine := policy.NewEngine()
engine.AddPolicy(policy.Policy{
    ID:      "admin-access",
    Effect:  policy.Allow,
    Principal: &policy.PrincipalMatcher{Roles: []string{"admin"}},
    Actions: []string{"*"},
    Resource: &policy.ResourceMatcher{Type: "Document"},
})

result := engine.Evaluate(ctx, policy.Request{
    Principal: policy.Principal{ID: userID, Roles: userRoles},
    Action:    "read",
    Resource:  policy.Resource{Type: "Document", ID: docID},
})

if result.Decision == policy.DecisionDeny {
    // Access denied
}
```

### Retry Policies

Handle transient failures:

```go
import "foundation/server-kit/go/retry"

// Custom policy
policy := retry.NewPolicy(retry.Config{
    MaxAttempts:  3,
    InitialDelay: 100 * time.Millisecond,
    MaxDelay:     5 * time.Second,
    Multiplier:   2.0,
    Jitter:       0.1,
})

err := policy.Do(ctx, func() error {
    return externalAPI.Call()
})

// Preset policies
retry.HTTPRetry().Do(ctx, httpCall)
retry.DatabaseRetry().Do(ctx, dbOperation)
retry.AggressiveRetry().Do(ctx, criticalOperation)
```

### Health Checks

Build comprehensive health probes:

```go
import "foundation/server-kit/go/healthcheck"

hc := healthcheck.New(healthcheck.Config{
    ServiceName: "my-service",
})

hc.AddCheck("database", healthcheck.DatabaseCheck(db))
hc.AddCheck("redis", healthcheck.PingerCheck(redis, "redis"))
hc.AddCheck("external-api", healthcheck.HTTPCheck("https://api.example.com/health"))

// Mount handlers
http.Handle("/health", hc.Handler())
http.Handle("/health/live", hc.LivenessHandler())
http.Handle("/health/ready", hc.ReadinessHandler())
```

### Error Taxonomy

Use categorized errors:

```go
import "foundation/server-kit/go/errors"

// Create errors with context
err := errors.New(errors.CodeNotFound, "user not found").
    WithField("user_id", userID).
    WithRequestID(requestID)

// Check error types
if errors.Is(err, errors.CodeNotFound) {
    // Handle not found
}

// Convenience constructors
errors.BadRequest("invalid input")
errors.Unauthorized("token expired")
errors.Forbidden("insufficient permissions")
errors.Validation("email format invalid").WithField("field", "email")
```

### Cache Patterns

Implement cache-aside:

```go
import "foundation/server-kit/go/cache"

c := cache.New(cache.Config{
    Backend:    cache.NewMemoryBackend(),
    DefaultTTL: 5 * time.Minute,
    Prefix:     "myapp:",
})

// Get or compute
user, err := cache.GetOrSet(ctx, c, "user:123", func() (*User, error) {
    return db.GetUser(ctx, 123)
}, cache.DefaultTTLPolicy().Medium)

// Tag-based invalidation
invalidator := cache.NewInvalidator(c)
invalidator.Tag("user:123", "user-data", "profile")
invalidator.InvalidateTag(ctx, "user-data")
```

### Graceful Degradation

Handle dependency failures:

```go
import "foundation/server-kit/go/degradation"

dm := degradation.NewManager()
dm.Register("redis", degradation.Config{
    HealthCheck: func(ctx context.Context) error {
        return redis.Ping(ctx).Err()
    },
    CheckInterval:     10 * time.Second,
    FailureThreshold:  3,
    FallbackBehavior:  degradation.FallbackFailOpen,
})

// Use sentinel pattern
sentinel := dm.Sentinel("redis")
if sentinel.Guard() {
    // Use Redis
} else {
    // Fallback to database or default
}
```

### API Versioning

Version your HTTP APIs:

```go
import "foundation/server-kit/go/versioning"

v := versioning.New(versioning.Config{
    Strategy:       versioning.StrategyHeader,
    HeaderName:     "X-API-Version",
    DefaultVersion: "v1",
})

v.HandleVersion("v1", v1Router)
v.HandleVersion("v2", v2Router)

// Mark deprecated versions
sunset := time.Now().Add(90 * 24 * time.Hour)
v.DeprecateVersion("v1", &sunset)

http.Handle("/api/", v.Middleware(v.Router()))
http.Handle("/api/versions", v.VersionsHandler())
```

---

## 5. Project Bootstrapping

### Create New Projects

```bash
# From OVASABI STUDIOS root
./foundation/init.sh my-app full      # Full-stack (Go + React + WASM)
./foundation/init.sh api-service backend   # Backend only
./foundation/init.sh web-client frontend   # Frontend only
./foundation/init.sh utility minimal       # Tooling only
```

### Update Existing Projects

```bash
./foundation/scripts/update-project.sh /path/to/project

# Or from within a project
make foundation-update
```

---

## 6. Version History

See [CHANGELOG.md](../CHANGELOG.md) for detailed release notes.

Current foundation version: **1.0.0**
