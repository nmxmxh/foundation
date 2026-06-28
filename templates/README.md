# {{PROJECT_NAME}} (Work in Progress)

This application is scaffolded using the **Ovasabi Foundation {{FOUNDATION_VERSION}}**, currently a **Work in Progress (WIP)** baseline. It is structured to separate shared platform infrastructure from your custom domain and application logic.

---

## Quick Start

Initialize your local development stack and verify the scaffold:

```bash
# Set up dependencies and toolchains
make setup

# Start local backing services (PostgreSQL, Redis)
make docker-up

# Start the application in development mode
make dev
```

---

## Essence & Architecture of the Scaffold

A Foundation project separates the shared platform layer from your own application logic to prevent architectural drift and framework decay:

* **Platform Modules (`foundation/`)**: These contain core primitives (e.g. `server-kit`, `runtime-transport`, `ui-minimal`). They are **read-only references** and should not be modified directly.
* **Scaffold Updates (`make foundation-update`)**: If the upstream Ovasabi Foundation core is updated, you can synchronize your project by running `make foundation-update`. This purges old foundation references, copies updated modules, and applies non-breaking scaffold patches.
* **Project-Owned Space**: Your custom application logic belongs in the following places:
  * Domain services under `internal/service/`
  * Background event job handlers under `internal/worker/`
  * API schema definitions in `api/protos/` and `api/schemas/`
  * Frontend UI and stores under `frontend/src/features/`

---

## Day-One Capabilities

Your scaffolded project inherits the following production-grade capabilities from day one:

1. **Multi-Tenant Isolation**: Derives tenant context securely from JWT tokens (`auth.OrgIDFromContext(ctx)`) at the database gateway, ensuring strict tenant isolation.
2. **Event-Driven Nervous System**: Mutating actions propagate conforming events (`requested` -> `success` / `failed`) carrying a unique `CorrelationID` tracked across all logs and workers.
3. **Hermes Hotplane Projections**: Bounded, node-local in-memory read models that tail Postgres/Redis mutation events and serve dashboard reads in nanoseconds.
4. **Resumable Transfers**: Progress-tracking file upload and download streams with lifecycle hooks.
5. **Bounded Concurrency**: Background queues and worker pipelines powered by River to prevent thread-saturation and memory leaks.
6. **Unified Resilience**: OpenTelemetry tracing, circuit breakers, and exponential backoff retry patterns pre-wired.

---

## Agnosticism & Zero-Copy Primitives

Your custom code has access to the same hardware-aligned performance primitives that power the Foundation:
* **Decoupled Agnosticism**: The platform logic is decoupled from runtime processing, running identically in bare-metal Go/Rust environments, browser event loops, and native operating system containers.
* **Zero-Copy Serialization**: Build high-speed computing modules using Cap'n Proto control-buffers and SharedArrayBuffers (SAB) to share memory directly across threads without serialization overhead.
* **Performance Primitives**: Guidelines like zero-allocation hotpaths, Structure-of-Arrays (SoA) layout prefetching, and vectorized SIMD loops can be applied directly to custom financial arithmetic, visualization steps, or data pipelines.

---

## Verification checks & Linting

Your scaffold includes strict checking tools mapped under `scripts/checks/` to enforce code and documentation consistency:

* `make lint-foundation`: Runs the complete suite of linters and verification checks.
* `make check-agent-contract`: Asserts that agent guidelines and DOD evidence remain wired.
* `make check-practice-controls`: Asserts that your code conforms to the machine-readable practice controls matrix.
* `make check-runtime-performance-contracts`: Asserts that pprof, tracing, and CPU evidence hooks remain intact.
* `make check-formal-methods`: Verifies that TLA/PlusCal queue and projection specs are in place.
* `make check-operational-excellence`: Verifies that DORA, SPACE, and software provenance tracking are wired.

