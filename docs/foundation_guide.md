---
description: Ovasabi Foundation: Comprehensive Agent & Developer Guide
---

# Ovasabi Foundation: Comprehensive Agent & Developer Guide

The **Ovasabi Foundation** is the master template and infrastructure baseline for building independent, standalone applications in the ecosystem. It abstracts **communication mechanics**, **high-compute performance**, and **durable orchestration** so feature teams can iterate safely without creating shared production databases.

This guide is **self-contained** and designed to be copied into any application using the foundation.

Primary performance companions:

* `performance_practices.md`: cross-cutting Go, networking, PostgreSQL, Rust, benchmarking, and documentation-tracking rules.
* `tla_architecture_practices.md`: state-machine, invariant, liveness, real-time bound, composition, and refinement practices from `Specifying Systems`.
* `foundation_benchmarks.md`: benchmark commands, reference results, and allocation guardrails.
* `database_practices.md`: PostgreSQL schema, query, pool, migration, and operational standards.
* `testing_practices.md`: `TE-*` testing rules, adequacy expectations, contract checks, and test lint guidance.
* `delivery_metrics_practices.md`: DORA delivery signals, CI collection, and incident records.

---

## 1. Core Modules & Elements

### A. server-kit (Go)

* **Purpose**: Backend durable orchestration & decoupled messaging.
* **Core Services**:
  * **Event Bus**: Multi-driver (Redis/In-Memory) pattern matching for decoupled service communication. Highly focuses on `<domain>:<action>:requested/success/failed` lifecycle.
  * **Graceful Signalers**: Consistently formats error and success streams into conforming envelopes.
* **Scaffold Contract**: Generated backends must use server-kit as the runtime spine. Startup registers dependencies with `resilience`; server ingress uses `registry`, `httpapi`, `metadata`, `graceful`, `security`, `compress`, and `observability`; WebSockets use `wsrouting` and `wsmetrics`; workers use bounded queue defaults.

* **Extended Modules** (v1.0.0):

| Module | Package | Purpose |
| -------- | --------- | --------- |
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
* **Scaffold Contract**: Frontends consume this as `@ovasabi/runtime-transport` from `file:../foundation/runtime-transport/ts`; raw aliases into `foundation/runtime-transport/ts/src` are drift.

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
* Worker runtimes must inject the job correlation ID into `context.Context` before calling processors so tracing, graceful events, and metadata emission share one trusted request chain.
* Cascading jobs, failure persistence, audit writes, and other durable follow-up operations from inside a worker must use a bounded detached context that preserves metadata values but does not inherit an already-cancelled or nearly-expired worker deadline. This prevents a parent job timeout from silently dropping a required child job while still enforcing a small enqueue/write budget.
* Header-derived user, session, device, organization, role, forwarded IP, and real IP values are provisional metadata. Auth and proxy-trust middleware must overwrite them from trusted claims or trusted edge headers before authorization or domain logic relies on them.

### 2. Client Stateful Pipeline Boundaries

* To prevent flooding endpoints with duplicates: Use the deduplication hooks provided by `createEventStore`.
* Avoid declaring ad-hoc `.dispatch` payloads without verifying if the Active Metadata Context bundle addresses the variables implicitly.

### 3. Compute Locality Rule

* Calculations involving massive bytes or mathematical loops belong in **`runtime-sdk`** via WASM, not the browser logic streams.

### 3b. Performance Specification Rule

* Before optimizing, define the behavior boundary: tenant scope, state transition, payload size, concurrency budget, timeout, backpressure behavior, and failure semantics.
* For high-risk concurrent or performance-sensitive work, also define visible state, hidden/internal state, invariants, liveness/fairness, real-time bounds, and refinement/parity expectations. Use `tla_architecture_practices.md` as the granular guide.
* Establish a baseline with a benchmark, profile, load test, query plan, or production telemetry. Performance work without measurement belongs in docs as a hypothesis, not in code as a default.
* Update `performance_practices.md`, `tla_architecture_practices.md`, `foundation_benchmarks.md`, `database_practices.md`, `runtime_foundation.md`, `websocket_scaling.md`, or `optimization_points.md` when an optimization changes a default, budget, invariant, benchmark expectation, or operational runbook.

### 4. Hostile-Environment Security Rule

* Treat browser state, route params, websocket frames, uploaded files, webhook payloads, queue messages, and third-party responses as untrusted input.
* Model at least these attacker classes: anonymous user, authenticated user, tenant adversary, malicious integration/API consumer, and insider with partial infrastructure access.
* Every new exposed capability should define its trust boundary, sensitive assets, abuse controls, and audit expectations before implementation.
* Production scaffolds must keep authentication enabled, use exact CORS origins, and protect operational endpoints such as `/metricsz` and `/metricsz/trace`.

### 5. Change-Risk Hotspot Rule

* Treat complexity plus low coverage as change risk. If a touched method is a hotspot candidate, add tests or simplify it before layering more behavior onto it.
* New and changed production code should aim for line coverage >= 95%, branch coverage >= 90%, and CRAP-style hotspot scores below the high-risk threshold where the stack can calculate them.

### 6. DOM Observation Rule

* `MutationObserver` is exception-only. Prefer explicit props, stores, runtime events, `ResizeObserver`, or `IntersectionObserver` before watching DOM mutations.
* If observation is unavoidable, isolate it behind a small UI adapter, watch the smallest possible subtree, and disconnect it reliably on cleanup.

### 7. Frontend Styling And Motion Rule

* Follow the theme -> CSS variable -> primitive -> feature wrapper layering model.
* Install and use `@ovasabi/ui-minimal` from the vendored `foundation/ui-minimal/ts` package; app `components/ui` modules should be wrappers around `Minimal*` primitives, not standalone replacements.
* Recognize primitives before writing app-local UI: check `MinimalAppShell`, `MinimalScrollMain`, `MinimalSkipLink`, `MinimalSidebar`, `MinimalButton`, `MinimalCard`, `MinimalInput`, `MinimalTable`, `MinimalCalendar`, `MinimalActionModal`, `MinimalSkeleton`, and related surfaces first.
* Use `useMinimalMotion` and `useMinimalScrollFeedback` for layout entrances, contextual panel movement, and subtle scroll response. App-specific auth, role gating, route lists, and product copy stay outside `ui-minimal`.
* Install and use `@ovasabi/runtime-transport` and `@ovasabi/frontend-kit` from the vendored foundation TypeScript packages. Do not import or alias raw files from `foundation/ui-minimal/ts/src/*` or `foundation/runtime-transport/ts/src/*`.
* Keep Vite, Vitest, and TypeScript `preserveSymlinks` enabled so local file dependencies resolve peers from the frontend package graph.
* Run `make proto-ts` after protobuf changes and import domain contracts from `frontend/src/types/protos`.
* See `frontend_scaffold_sync.md` for the full package, generated-type, Docker context, and communication-layer sync contract.
* Prefer grouped styled-component declarations (`const Style = { ... }`) over long flat lists of standalone styled constants in app code.
* Keep route loaders, keyed loading state, and skeletons separate from domain rendering instead of collapsing them into one component.
* Read `styling_design_practices.md` and `docs/references/README.md` before introducing new interaction motion.

### 8. Frontend Operations Rule

* Use `@ovasabi/frontend-kit` for IndexedDB storage, metadata normalization, store reset registries, and runtime/WASM snapshot hooks.
* Use `@ovasabi/runtime-transport` for WebSocket, HTTP fallback, binary envelopes, compression, offline queueing, route registries, and metadata/event stores.
* Keep generated protobuf contracts in app space and pass them into app-specific stores/hooks; do not put app domain contracts inside `frontend-kit`.
* Runtime and WASM views should expose React state through `useSyncExternalStore`-style handles. Avoid page-local polling loops for SAB epochs or worker diagnostics.
* Use Makefile runtime targets for WASM propagation: `make runtime-bindings`, `make build-rust-wasm`, and `make wasm-manifest`. Frontend code should load `frontend/public/runtime/wasm-manifest.json` through `@ovasabi/frontend-kit` and instantiate modules through the runtime-sdk browser host.
* Performance-sensitive frontend code must call the runtime lane planner before choosing a transport/compute path. Treat the planner output as the contract: `copyBudget`, `allocationBudget`, `expectedLatencyClass`, `deadlineRisk`, and `requiresCrossOriginIsolation` decide whether a feature uses SAB/WASM, transferable workers, WebGPU, WebSocket, or HTTP.
* Keep UI thread work on control and render duties. Do not decode large JSON maps, spin on SAB epochs, or dispatch WebGPU compute from React render paths. Workers own compute, SAB waits, transfer fallback, and GPU dispatch orchestration.
* Use WebGPU only for wide homogeneous batches where dispatch/readback is amortized. Use SAB/WASM or transferable workers for small bounded payloads, and keep hot domain payloads as typed bytes/views until the owning adapter must materialize objects.
* Use `@ovasabi/runtime-native` only for native shell dispatch and capability detection. Tauri IPC is a measured control boundary; hot compute stays on runtime-sdk lanes.
* Device APIs split into two lanes. WebView APIs such as `getUserMedia` are compatibility lanes for preview/simple capture. Performance-sensitive camera, microphone, and sensor streams must use native Swift/Kotlin plugins that route bytes or typed slots through `runtime-native` into `runtime-sdk`.
* Official Tauri device plugins such as geolocation, biometric auth, NFC, haptics, barcode scanning, notifications, filesystem, and shell access are enabled per app through explicit dependencies, capabilities, and platform privacy/permission entries. Foundation scaffold defaults stay minimal.

### 9. Backend Runtime Binding Rule

* Use `server-kit` as a bound runtime layer, not as copied reference code.
* Register database, Redis, object storage, and other critical dependencies with `resilience` during startup so health, circuit, retry, degradation, and failure-drill behavior share one dependency model.
* Route HTTP and WebSocket ingress through `registry`, `httpapi`, `metadata`, `graceful`, `security`, `compress`, `observability`, `wsrouting`, and `wsmetrics` before app handlers receive payloads.
* Keep scalar request validation in-process when it is already allocation-free and nanosecond-scale. Use `runtimehost`/Rust only when deterministic compute is large enough to amortize the FFI, stdio, shm, or WASM boundary.
* App Rust compute must expose a Foundation runtime descriptor and stable schema names before services depend on it. The service contract should remain the same whether the selected lane is direct Go, native Rust, shared-memory Rust, or WASM.
* Native shells must use `foundation/runtime-native` for binary native frames and secure storage surfaces. Do not store native session tokens in frontend `localStorage`.
* Custom native plugins must preserve Foundation frame discipline: bounded buffers, descriptor allowlists, schema/version validation, correlation metadata where commands mutate state, and controlled errors for permission-denied, canceled, malformed, stale, oversized, and backpressured frames.
* Financial kernels must use integer minor units and checked arithmetic only. Floats are not allowed for ledger balances, merchant settlement, fee calculation, stablecoin reserve math, or compliance thresholds.
* Keep worker throughput bounded through server-kit queue configuration and chain helpers instead of ad-hoc goroutine fan-out.
* Run `make lint-foundation` or `scripts/checks/server_kit_usage_check.sh .` after scaffold sync. `.foundation` projects receive deep wiring checks; intentionally custom apps should either adopt the scaffold profile or remain explicitly outside that contract.

### 10. Delivery Metrics Rule

* Runtime health and delivery health are separate signals. Keep DORA-style delivery events for change lead time, deployment frequency, failed deployment recovery time, change fail rate, and deployment rework rate.
* Generated projects inherit `make delivery-metrics` and CI artifact capture. Treat those records as collection events; app deployment platforms own aggregation, dashboards, and alert policies.
* Production incidents, failed deployments, rollbacks, and hotfixes should have incident records that tie CI run, deployment run, commit SHA, correlation IDs, and remediation follow-up together.

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
