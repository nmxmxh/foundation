# Foundation: Philosophy & Design
 
**Status**: 0.0.1 | **Date**: 2026-06-30 | **Audience**: Architects, full-stack teams, AI coding partners

This document explains *why* Foundation exists, what gap it fills, and who it's designed for.

---

## The Software Deficit

Modern hardware is astonishingly fast. A 3.0 GHz CPU core completes a clock cycle every 0.33 nanoseconds. A single instruction executes in less than 1 nanosecond. Light travels 30 centimeters in 1 nanosecond—roughly the width of a motherboard. This is the tempo at which hardware operates.

Typical software stacks suffer from a massive *software deficit*. A simple JSON payload parse or router dispatch often spends **50–1,000 microseconds** (50,000–1,000,000 nanoseconds). That wastes millions of CPU cycles on indirection, heap allocation, serialization, and unnecessary copying.

Foundation is engineered to bridge this deficit. It provides:

- A **performance ladder** with seven planes, from zero-alloc direct dispatch (10–30 ns/op) to compatibility JSON (30 µs/op)
- **Zero-allocation hotpaths** where the only work happening is the work you intended
- **Hardware-aligned memory interfaces** that keep operations in the nanosecond-to-microsecond domain
- **Bounded work guarantees** on every queue, cache, retry, and worker to prevent surprise latency cliffs

This is not premature optimization. When you can keep a dashboard read in sub-microseconds instead of milliseconds, you enable product experiences that feel instant. When you eliminate allocation pressure from critical paths, you gain predictability under load. When you measure this carefully and enforce it, you build systems that stay fast as they grow.

---

## One Programming Model: Everything Is a State Event

The deeper reason most software is slow *and* buggy is not bad code — it's **fragmentation**. In a normal stack, the same piece of state is represented differently at every layer, and the developer pays a tax to translate it across each boundary:

```
Database (SQL rows)
   │  ORM mapping
Backend (Go/Java objects)
   │  JSON serialization
Network (HTTP payloads)
   │  API hydration
Frontend (React/Zustand state)
   │  storage mapping
Browser (IndexedDB/LocalStorage)
```

Each arrow is hand-written translation code: ORMs, JSON encoders, REST controllers, DTOs, state synchronizers. A large fraction of a team's time goes into those arrows — and they are where most bugs, security leaks, and wasted CPU live. The business logic is a thin sliver; the translation tower is the bulk.

**Foundation collapses the tower.** The unit of the system is not a row, an object, or a payload — it is a single, immutable **state event**. A mutation is defined *once* as a schema (Protocol Buffers / Cap'n Proto), and the system behaves as a state machine:

```
State(n+1) = Transition(State(n), Event)
```

The same event flows through every layer without being re-encoded:

- The **event log** (Postgres, Redis Streams) is the *history* of events.
- The **network** (`runtime-transport` over WebSocket/HTTP) is the *carriage* of events.
- The **hotplane** (Hermes) is the *node-local projection* of events.
- The **frontend store** (Zustand via `runtime-transport`) is the *visual projection* of events.

The `RuntimeEnvelope` that represents a mutation in Go is the same envelope transmitted over the wire, tailer-applied from Redis, and read by JavaScript and WASM in the browser. The contract is the program. This is the practical realization of a **replicated state machine** — the same idea that underpins consensus systems like Raft, applied as the everyday programming model.

### Why contracts and protos are the foundation, not boilerplate

In most stacks, the schema is an afterthought — a serialization detail bolted on at the network edge. Here it is inverted: **the proto/Cap'n Proto contract is the single source of truth, and every layer is generated from or validated against it.** That one decision is what makes the rest possible:

- **No translation drift.** Backend route, network shape, generated TypeScript types, and frontend command registry all derive from the same contract (`server-kit/go/httpapi/catalog.go` → `route_catalog.json` → generated `runtimeRoutes.ts`). When the contract changes, every consumer changes with it or fails a check. There is no place for a hand-maintained DTO to silently disagree with the database.
- **Zero-copy where it counts.** Because the byte layout is fixed by the contract, the hot lanes pass *references*, not re-serialized copies. A binary frame arriving in the browser is read directly from its offsets through the SharedArrayBuffer control plane — no JSON parse, no allocation, no GC pressure (`runtime-sdk`'s `layout.rs`).
- **Determinism and replay.** State is a pure function of the event stream, so the event log *is* the reproduction. A production bug for one tenant can be downloaded as its event stream and replayed locally with full fidelity — which is also why agents debug well here: they replay the stream and watch exactly where an invariant breaks, instead of guessing.
- **Temporal coordination for free.** Events are ordered by epochs and watermarks, so every layer knows *when* it is. The frontend can tell its view is stale by comparing its local watermark to the backend epoch — the classic out-of-order / staleness problem is handled by the contract, not by ad-hoc cache-busting.

### The mindset shift

In a conventional stack you **manipulate variables and call APIs**. In Foundation you **define transitions**:

1. Define the **schema** (the event).
2. Write the **transition** (how the event updates durable state).
3. Write the **projection** (how the UI displays the state).

Routing, transport, serialization, caching, multi-tenancy, and persistence are handled by the substrate. That is the real abstraction: the developer (or agent) writes at the level of the **"what"** — declarative domain logic — while the substrate compiles it down to the **"how"** — zero-alloc frames, columnar scans, GPU dispatches, SharedArrayBuffer atomics. Developer expressiveness is decoupled from execution efficiency: simple code on top, hardware-limit physics underneath.

A fair caveat: this is the model Foundation is built *toward*, and large parts of it are real and measured today (the shared envelope, the generated registry, Hermes projections, the zero-copy runtime lanes). Some lanes still pass through compatibility adapters (JSON ingress, not-yet-binary paths). The contract makes those adapters *visible and bounded* rather than load-bearing — and the direction of travel is always toward fewer translations, not more.

For the concrete end-to-end mechanics of this model — command to event to projection to store — see [`state_event_model.md`](state_event_model.md).

---

## Three Enablers: Hermes, Metadata, and Performance Planes

### Hermes: The Hotplane

Hermes is the bounded, node-local projection layer that answers operational reads in sub-microseconds—without hitting the database for every query.

**The principle**: Postgres decides (it's the source of truth). Redis coordinates (across nodes). Hermes remembers what this node needs right now.

Hermes is not a cache you query. It's an automatically-updated read model that:

- Stays bounded in memory (you set limits)
- Projects database mutations into local indices in real-time
- Falls back to Postgres if it's stale or overwhelmed
- Measures its own freshness and degrades gracefully

A dashboard that reads 100 million customer records can use Hermes to serve scoped, filtered views in microseconds instead of hammering Postgres with millions of queries per second. And when Hermes is stale, it tells you—it doesn't silently serve wrong data.

**Why it matters**: Responsiveness at scale. The difference between a dashboard that loads instantly and one that lags is often just sub-microsecond reads. Hermes makes that the default, not the exception.

### Metadata: The Context Carrier

Every request carries metadata: who asked, what organization it belongs to, correlation ID, request ID, security context, and tags. This metadata flows through every lane—HTTP handlers, workers, databases, logs, traces, websockets, and Hermes projections.

This is not logging decoration. Metadata is how Foundation preserves the *circumstance* of every operation, enabling:

- **Observability**: All logs, metrics, and traces linked by correlation ID
- **Tenant isolation**: Organization scope never derived from client input, always from auth context
- **Incident diagnosis**: When something breaks, you can see exactly which actor, tenant, and request caused it
- **Security audit**: Every mutation is recorded with who changed what, when, and why

**Why it matters**: You can diagnose production issues without guessing. You maintain tenant isolation and security at every boundary, not through heroic effort but by design.

### Performance Planes

Foundation uses a seven-tier performance ladder:

1. **Direct dispatch** (10–30 ns/op) — same-process, zero-copy, typed
2. **Binary frames** (20–80 ns/op) — borrowed views, no allocation
3. **Generated protobuf** (370 ns/op) — typed cross-process boundary
4. **gRPC** (20–30 µs/op) — polyglot, network machinery
5. **JSON** (30 µs/op) — compatibility adapter only
6. **Native FFI/SHM** (varies) — trusted same-host compute
7. **Browser worker + WASM + SharedArrayBuffer** — where supported

The design rule: **the fastest lane must not pay the cost of the compatibility lane**. Direct dispatch should not suffer network overhead. JSON should remain visibly more expensive than binary paths. Benchmarks measure this automatically; regressions are caught before they land.

---

## Swiss Army Knife Philosophy

Foundation is not one monolithic framework. It's a carefully integrated toolkit of layers that can be used independently or together:

- **server-kit** (Go): Event bus, worker orchestration, Hermes projections, circuit breakers, database helpers, transfer coordination
- **runtime-transport** (TypeScript): Universal client wire, command bus, WebSocket fallback, metadata stores
- **runtime-sdk** (Rust/WASM): High-performance kernel with 4KB fixed control buffer for zero-copy communication
- **ui-minimal** (TypeScript): Shared UI primitives and theme tokens, not an opinionated design system
- **frontend-kit** (TypeScript): IndexedDB storage, runtime artifacts, state helpers (not domain logic)
- **runtime-native** (Tauri/Rust): Native shell bridge for secure storage, GPU handles, and hardware-accelerated lanes

Each component can be adopted partially. You can use server-kit without Hermes, or Hermes without runtime-sdk. You can build a backend-only service or a full-stack application. Foundation doesn't lock you into a single architecture.

**But**: When you integrate all the layers, something powerful emerges. Commands flow through a single nervous system. Metadata propagates everywhere. Performance is measured end-to-end. Tenant isolation is preserved at every boundary. This is why Foundation exists—not to constrain you, but to let you build something ambitious without starting from scratch.

---

## Three Pillars

### Performance

- Multi-plane architecture from nanosecond direct dispatch to microsecond JSON compatibility
- Hermes hotplane enables sub-microsecond operational reads at any scale
- Zero-allocation critical paths prevent latency cliffs under load
- Benchmarks measure and enforce performance contracts automatically
- Throughput and tail latency matter equally (p99 drives decisions, not p50)

### Reliability

- Event-driven nervous system with requested → success/failed lifecycle
- Correlation ID carriers observability through all layers
- Tenant isolation derived from auth context, never from client input
- Graceful degradation (Hermes falls back, circuit breakers auto-recover, workers retry with backoff)
- Bounded work everywhere (queues, caches, retries, workers)—no surprise latency cliffs

### Agility

- Agentic coding with deterministic contracts and machine-decidable gates
- 40+ practice controls enforce reliability/security/performance automatically
- Conductor pattern: persistent agent memory, closed-loop verification, handoff records
- Scaffold generation bootstraps new projects with production baseline
- Fleet synchronization keeps multiple projects in sync with Foundation changes

---

## What Foundation Is NOT

**Not a no-code platform.** You're writing code, not building with visual blocks. You need to understand the nervous system, the performance ladder, how Hermes works, and where metadata flows.

**Not a zero-DevOps platform.** You're running Postgres, Redis, and Go services. You need operational understanding: query plans, connection pools, index strategy, deployment discipline, observability wiring.

**Not a constraint on your code.** Foundation provides execution lanes (database, workers, cache, Hermes). You own the domain meaning. You can extend, replace, or ignore Foundation patterns where it makes sense for your product.

**Not a substitute for thinking about your domain.** Foundation doesn't tell you what to build or how to architect your business logic. It tells you how to build it fast, safely, and at scale once you know what you're building.

**Not for teams that want to move fast by cutting corners.** Foundation is demanding. It requires discipline: reviewing performance impacts, writing tests, understanding trade-offs, thinking through failure modes. If you want to "just ship," pick a simpler stack.

---

## Who This Is For

Foundation is designed for:

**Teams that want to evolve code.** Not teams that follow a template and call it done, but teams that expect their codebase to grow, change, and accumulate hard-won knowledge. Foundation gives you the infrastructure to do that without constantly fighting technical debt.

**Architects and full-stack engineers.** People who understand distributed systems, performance, and the gap between local machine code and production at scale. People who like to think in layers. People who appreciate building on solid foundations.

**AI coding partners and human agents.** Foundation works well with AI-assisted development because:

- Contracts are deterministic—AI can learn and follow the rules
- Gate verdicts are machine-decidable—no ambiguous "is this good?"
- Evidence is concrete—benchmarks, test output, contract checks, not opinions
- Memory is persistent—agents learn across sessions
- Handoff records are clear—next agent (human or AI) has context

**Organizations building systems that matter.** If latency, scale, observability, or tenant isolation matter for your product—if you're building for financial trading, distributed games, multi-tenant SaaS, or AI-intensive workloads—Foundation gives you the tools to do it right.

---

## The Foundation as Practiced

When everything works together, here's what you get:

1. A user clicks a button in the UI
2. Request enters with metadata: who, which organization, correlation ID, security context
3. Domain handler runs in server-kit, writes durable state to Postgres
4. Event is emitted: `domain:action:requested → :success`
5. Workers process follow-up work without blocking the user
6. Hermes projects the change into a local index
7. WebSocket notifies the frontend in sub-microseconds
8. Observability sees the full path: logs linked by correlation ID, metrics by tenant, traces from entry to completion

This entire flow costs microseconds on the hot path, not milliseconds. Tenant isolation is automatic. Observability is wired in. Workers are bounded. Failure modes are graceful.

That's Foundation.

---

## Next Steps

- Read [`README.md`](../README.md) for an overview of components and quick-start paths
- Read [`foundation_quick_start.md`](foundation_quick_start.md) for the 15-minute technical path
- Read [`foundation_tour.md`](foundation_tour.md) to walk through one complete product action
- Read [`AGENTS.md`](../AGENTS.md) if you're working with AI partners or planning to use agents
