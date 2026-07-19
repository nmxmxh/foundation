---
description: "Ovasabi Foundation: Exhaustive Agent Operations Workflow & Architecture Guide"
---

# Ovasabi Foundation: Detailed Agent Workflow & Architecture Guide

The **Ovasabi Foundation** is the strict platform infrastructure layer designed to be copied **as-is** across the standalone application ecosystem (for example, `reframe_v1`, `field_os`, `fintech_v1`). Its goal is absolute predictability: zero variance in how apps communicate, execute performance-bound tasks, connect to databases, and handle events.

As an AI Agent operating within an encompassing app that uses this foundation, **you must follow these exact instructions and constraints** whenever modifying communication, performance code, or data layers.

---

## 1. Holistic Architecture Overview

Before acting, verify your target against the structural boundaries of the Foundation.

| Layer | Responsibility | Elements | Agent Constraints & Nuances |
| :--- | :--- | :--- | :--- |
| **`server-kit`** | Durable Orchestration | Go, Postgres, Redis | State transitions MUST be idempotent. Never store primary records in Redis. Enforce `organization_id` on ALL routes. |
| **`runtime-sdk`** | High Compute (CPU Bound) | Rust, WASM, C++ | **STRICT ALLOCATION**: Uses a 4KB Fixed Shared Buffer. Zero-copy FFI. Do NOT use dynamic heap allocations in hot paths. |
| **`runtime-transport`** | Universal Client Wire | TS, Zustand, HTTP/WS | Handles stateless HTTP/WS routing. Stateful (`MetadataStore`, `EventStore`) controls deduplication. Protobuf preferred. |
| **`config-contracts`** | Boot & Environment | T-Schema, ENV variables | Separates `public` (frontend safe) and `server` config. Keep DB pools config-driven. |
| **`docs/*_practices`** | Universal Governance | CP-rules, DB/Redis rules | Enforces pragmatism. `Max 80-line functions`. 3-group database migrations. Zero warnings in CI. |

---

## 2. Foundation Documentation Topology (Required Reading)

Agents MUST navigate to these specific files for granular rules when operating within their respective domains:

* **Coding Practices**: [`docs/coding_practices.md`](../docs/coding_practices.md) - Contains CP-01 through CP-36 strict coding assertions.
* **Rust Unit Guide**: [`docs/rust_unit_guide.md`](../docs/rust_unit_guide.md) - End-to-end walkthrough for adding an app-owned Rust performance unit (stdio/FFI/shm/WASM lanes).
* **Database Practices**: [`docs/database_practices.md`](../docs/database_practices.md) - Fixed 3-group migration structure and connection budgets.
* **Redis Practices**: [`docs/redis_practices.md`](../docs/redis_practices.md) - Ephemeral data structures, key-naming conventions, and TTL configurations.
* **Runtime Architecture**: [`docs/runtime_foundation.md`](../docs/runtime_foundation.md) - Top-level isolation posture between control-plane (Go) and hot-path (WASM/Rust).
* **Runtime SDK Kernel**: [`runtime-sdk/README.md`](../runtime-sdk/README.md) - Explains the memory topology of the 4KB fixed execution buffer.
* **Runtime Transport (Universal)**: [`runtime-transport/README.md`](../runtime-transport/README.md) - The stateless routing matrix and event envelopes.
* **Config Contracts**: [`config-contracts/README.md`](../config-contracts/README.md) - Separation of public frontend-safe parameters from secure server connections.

---

## 3. Core Operational Workflows (Agent Actions)

### A. Implementing Frontend State & Communication (Using `runtime-transport`)

When you are asked to connect a new React component or Frontend module to the Backend, adhere to the following sequence:

1. **Initialize the Singletons in App-Space**:
   Do NOT build independent fetching hooks. Ensure the application initializes `createMetadataStore()` and `createEventStore()`.
2. **Metadata Rule - Implicit Tracking**:
   NEVER manually attach context metadata to your view payloads unless overriding context (for example, `organization_id`). The framework-agnostic `MetadataStore` state carries the correlation hashes invisibly.
3. **Dispatch Rule - Deduplication**:
   To prevent React re-render floods, route all mutations and data requests through the `EventStore`.
   `eventStore.emitEvent('domain:action:v1:requested', payload, { cacheDurationMs: 3000 })`
4. **Offline Resilience (WebSocket)**:
   The stateless `CommandBus` owns a **Re-Subscription Map**. When writing socket listeners (for example, `media:*`), know that on disconnect/reconnect, the foundation automatically resubscribes. Do not build redundant polling algorithms.
5. **Envelopes**:
   Every payload traverses the wire inside a `RuntimeEnvelope` (preferring binary protobuf). The stateless command bus automatically falls back from `WS -> HTTP` on failure.

### B. Building High-Compute Operations (Using `runtime-sdk`)

When handling large arrays, media blobs, or heavy mathematical reductions, do NOT write heavy execution in TypeScript/Browser/Go.

1. **The 4KB Fixed Buffer**:
   Execution happens inside the `runtime-sdk` Native (FFI/SHM) or WASM lanes.
   The memory is strictly partitioned:
   * `0-128`: Epoch Sync Counters
   * `128-256`: Control Headers
   * `256-1280`: Input Blob
   * `1280-3328`: Output Blob
2. **Zero-Copy Discipline**:
   You must read linearly from the pointers. Do not parse JSON inside these hot-paths. Rely on typed Cap'n Proto / Protobuf messages serialized directly into these buffer offsets.
3. **Generation Discipline**:
   Use the scaffold Makefile runtime targets instead of hand-copying browser artifacts. `make runtime-bindings` regenerates runtime-sdk buffer constants, `make build-rust-wasm` publishes app-owned Rust modules into `frontend/public/modules`, and `make wasm-manifest` exposes the resulting artifacts to frontend-kit. Frontend code should discover WASM through the manifest and instantiate through the runtime-sdk browser host.

### C. Backend Database Operations (PostgreSQL)

When adding schemas or DB features inside the app:

1. **The 3-Group Migration Fixation**:
   During active development, there are strictly 3 groups: `0001_schema`, `0002_seed_system_data`, `0003_seed_demo_data`. You DO NOT add a `0004+` migration. You edit `0001` or `0002` directly. Seeds must be idempotent (`ON CONFLICT DO UPDATE`).
2. **Tenant Isolation**:
   Every org-scoped table MUST feature `organization_id`. Ensure compound indices always include this ID.
3. **No Network in Transactions**:
   Transactions must be tightly scoped to mutations. Do not await Stripe/S3/external networks while an SQL `Tx` is open.

### D. Backend Ephemeral Operations (Redis)

When adding caches or locks:

1. **Strict Key Naming**:
   Keys must look like `<app>:<env>:<domain>:<entity>:<purpose>[:<id>]` (for example, `app:prod:billing:sub:cache:999`).
2. **Strict Expiration**:
   Never default to indefinite TTLs.
3. **Graceful Degradation**:
   If Redis fails, the code must still execute (albeit slower) using the DB as the source of truth if it was a cache, or fail safely if it was an active idempotency fence.

---

## 4. Strict Coding Conventions (CP Rules)

Before completing a task or marking code as "Done," run an internal check against Ovasabi Coding Practices:

* **CP-01**: No hidden control flows. Avoid clever `goto` or tricky recursions.
* **CP-02**: All loops processing variable-size input must have practical boundaries, guards, or timeouts.
* **CP-03**: Function constraint: Target <= 80 lines. Hard cap <= 120. Cyclomatic complexity <= 15. If it gets larger, split orchestrators from processors.
* **CP-04**: ALWAYS handle and check return variables from functions/promises.
* **CP-08**: Zero-warning mindset. `npm run typecheck` or `go test` must return Exit 0. Fix lints; do not ignore them.
* **CP-10**: Event contracts are deterministically idempotent. Side-effects must tolerate duplicate deliveries via the Event bus.
* **CP-16**: Concurrency MUST be bounded by ENV configurations, not tiny hard-coded integers.

## 5. Final Agent Directives

1. When asked to fix an issue in the foundation: **READ THIS ENTIRE FILE.**
2. When extending a service: Look at the generated schemas/manifests in `api/schemas` or route registry.
3. When refactoring: If the files live inside `foundation/`, verify that your edits retain generic (`<TMetadata>`) portability so other apps pulling the same foundation folder do not break. DO NOT ADD APP-SPECIFIC DOMAIN IMPORTS in `foundation`.
4. Before editing any scaffolded file: check its sync mode in `templates/scaffold.manifest.tsv`. `overwrite`/`force` files are Foundation-owned and will be wiped on the next `ovasabi update`; `create`-mode files are tracked in the seed ledger, and drift is surfaced by `ovasabi refresh` (acknowledge intentional drift with `--acknowledge-seed-drift`).
