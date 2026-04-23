# Server-Kit (Go)

The `server-kit` module provides the core primitives for Ovasabi backend services. It is designed for high-throughput, distributed event processing and hardened security.

## Core Components

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
- **Redaction Helpers**: Reusable helpers sanitize session identifiers and secret-bearing telemetry fields before analytics or logs persist them.

### 5. Bootstrap (`/bootstrap`)
- **HandlerExecutionController**: Applies bounded concurrency, acquire timeouts, and optional token-bucket pacing to registered handlers.
- **Explicit Saturation Errors**: Acquire-timeout failures surface as `concurrency limit reached` rather than disappearing into a generic timeout path.

## LLM Agent Patterns: Implementing a New Route

When adding a new capability (e.g., `media:process`):
1. Define the route in the `registry`.
2. Determine if it requires **Streaming**. If so, return a channel.
3. Use the `EventEmitter` to signal progress.
4. Apply the `RateLimit` middleware if the operation is compute-expensive.
