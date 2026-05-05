# Server-Kit (Go)

The `server-kit` module provides the core primitives for Ovasabi backend services. It is designed for high-throughput, distributed event processing and hardened security.

## Core Components

## Scaffold Runtime Contract

Generated backends must use `server-kit` as the runtime spine, not as a copied
library shelf. The scaffold wires these surfaces by default:

1. `registry`, `httpapi`, `events`, `metadata`, and `graceful` shape all command,
   HTTP, WebSocket, event, and error communication.
2. `security`, `compress`, `observability`, `wsrouting`, and `wsmetrics` wrap
   ingress so cross-origin controls, compression, telemetry, routing, and socket
   counters stay uniform.
3. `resilience` binds `healthcheck`, `circuitbreaker`, `degradation`, `retry`,
   cache, and tracing into one initialized runtime. `chaos` remains the failure
   injection tool used by tests and drills against that same dependency model.
4. `worker`, `chain`, `metrics`, `slo`, `profiling`, and `contracttest` provide
   queue execution, bounded parallelism, observability, SLO evidence, runtime
   profiling, and event-contract verification.

The generated `scripts/checks/server_kit_usage_check.sh` fails when these
runtime bindings are present but not wired through startup/server paths.

## Communication Lane Policy

Foundation enforces the fastest correct lane by boundary:

1. same-process hot communication: `NewDirectFrameClient`, `Router.DispatchFrame`, `RegisterFrame`, and borrowed `UnmarshalFrameView`
2. Go service-to-service boundaries: binary `DispatchFrame` or typed protobuf handlers
3. external HTTP/WebSocket ingress: JSON or protobuf according to negotiated content type
4. compatibility/admin/debug paths: JSON envelopes only with explicit boundary ownership

Generated app checks reject app-internal `grpcsvc.Dispatch(...)` and
`grpcsvc.Envelope` usage. That keeps JSON compatibility from becoming the
default internal fabric while preserving HTTP/WebSocket ingress compatibility.

### 1. Events (`/events`)
The event system is the nervous system of the application.
- **Bus Interface**: A generic interface for pub/sub.
- **Redis Bus**: Distributed event routing across multiple nodes.
- **In-Memory Bus**: Local event routing for development and single-node deployments.
- **EventEmitter**: A refined wrapper that ensures all events conform to the `RuntimeEnvelope` schema, providing consistent metadata (correlation IDs, timestamps).
- **Dispatch Workers**: Redis listeners fan in traffic to blocking workers instead of polling loops, reducing idle CPU churn.

### 2. Redis (`/redis`)
A unified client for Redis operations.
- **Atomic Counters**: Used for distributed locking and rate limiting.
- **In-Memory Mock**: `NewMemoryClient` allows for full testing of distributed logic without a real Redis instance.

### 3. Compression (`/compress`)
Handles transport-level optimization.
- **Priority**: Brotli (Encoding `br`) is the primary target for modern clients, with GZIP as fallback.
- **Streaming**: The `StreamingCompressor` enables low-memory footprint response delivery. Use it when returning channels (`<-chan`).

### 4. Security (`/security`)
- **RedisRateLimiter**: Implements sliding-window rate limiting.
- **Middleware**: Injects stricter default security headers, exact-origin CORS handling, hashed credential-aware rate-limit fingerprints, and JWT validation with RBAC (Role-Based Access Control) support.
- **Post-Quantum TLS Helper**: `ApplyPostQuantumTLS` prefers hybrid TLS key exchange at connection setup while keeping app request handlers on the same hot path.
- **Redaction Helpers**: Reusable helpers sanitize session identifiers and secret-bearing telemetry fields before analytics or logs persist them.

### 5. Bootstrap (`/bootstrap`)
- **HandlerExecutionController**: Applies bounded concurrency, acquire timeouts, and optional token-bucket pacing to registered handlers.
- **Explicit Saturation Errors**: Acquire-timeout failures surface as `concurrency limit reached` rather than disappearing into a generic timeout path.

### 6. Resilience Runtime (`/resilience`)
- **Single startup binding**: Generated projects register database and Redis
  dependencies with the resilience runtime during startup.
- **Circuit and degradation together**: A registered dependency gets health
  status, circuit breaker state, retry-backed execution, and degradation mode
  from the same runtime object.
- **Failure drills**: Use `/chaos` to inject latency, failure, and partition
  behavior against dependency names already registered with resilience.

### 7. Performance Evidence
- Use `go test ./...` for correctness.
- Use `go test -bench=. ./chain ./compress ./grpcsvc ./metrics ./profiling ./slo`
  from `foundation/server-kit/go` when changing hot runtime primitives.
- Use scaffold `make lint-foundation` to run CP, database, Redis, River,
  contract drift, server-kit usage, and project scaffold checks together.

## LLM Agent Patterns: Implementing a New Route

When adding a new capability (e.g., `media:process`):
1. Define the route in the `registry`.
2. Determine if it requires **Streaming**. If so, return a channel.
3. Use the `EventEmitter` to signal progress.
4. Apply the `RateLimit` middleware if the operation is compute-expensive.
