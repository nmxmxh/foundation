# Foundation (Work in Progress — Version 0.0.1)

**I built Foundation to bridge the massive deficit between raw hardware speed and typical software stacks, collapse translation layers across database, network, browser, native, and accelerated runtimes, and make sophisticated, high-performance, reliable software externally programmable through a small, teachable model.**

Every modern system wastes millions of CPU cycles translating the same piece of state: SQL rows get parsed into Go structs, serialized to JSON, decoded over HTTP, mapped to TypeScript interfaces, and synchronized with React state. That's a massive "software deficit"—spending microseconds or milliseconds on work that raw hardware could complete in nanoseconds. It is also where most bugs, memory leaks, and architectural drift live.

And when you bring AI agents into a loose, unstructured codebase, the friction multiplies. Without strict boundary contracts and deterministic checks, agents write sloppy loops, hallucinate APIs, and compromise performance under load.

So I built **Foundation**.

Foundation concentrates systems complexity into infrastructure. A person or
agent describes domain intent, contracts, transitions, projections, and
guarantees; the substrate supplies authenticated execution, durability,
realtime state, capability-aware runtime selection, observability, verification,
and safe fallback. Its deeper planes remain available through progressive
disclosure rather than becoming prerequisites for ordinary feature work.

It is a full-stack substrate that compounds the strengths of Go, Rust/WASM, and TypeScript into a single, cohesive "nervous system":

- **Single-Contract Architecture**: Define mutations once (Protobuf/Cap'n Proto), and the schema generates the Go routes, TypeScript types, and zero-copy binary layouts across all boundaries.
- **Extreme Performance**: Serves node-local read models via **Hermes** in microseconds. Predicate filter benchmarks show Hermes columnar bitmap merges executing in ~34 µs with 2 allocations, compared to ~7.8 ms and 10,000+ allocations on standard record-chasing paths (a 229× speedup).
- **Agent & Human Synergy**: The codebase is designed as an agent-executable environment. With 40+ automated enforcement checks—**Practice Controls**, contract drift, concurrency safety—running on every commit, both humans and agents can refactor and add features with confidence.

---

## Components at a Glance

| Component | Tech Stack | Purpose |
| --- | --- | --- |
| **server-kit** | Go | Backend: event bus, workers, Hermes, database, resilience, observability |
| **runtime-transport** | TypeScript | Client wire: command bus, envelope creation, metadata stores, WebSocket/HTTP fallback |
| **runtime-sdk** | Rust/WASM | High-performance kernel: 4KB control buffer, zero-copy communication |
| **ui-minimal** | TypeScript/React | Shared UI primitives, semantic theme tokens, motion helpers |
| **frontend-kit** | TypeScript | IndexedDB storage, metadata helpers, runtime adapters, transfer progress |
| **runtime-native** | Tauri/Rust | Native shell bridge: secure storage, GPU handles, device access |
| **config-contracts** | Go/TypeScript | Cross-language configuration schemas |

**Data Layer**: PostgreSQL (durable truth), Redis (coordination), Protocol Buffers (contracts), Cap'n Proto (zero-copy boundaries)

The execution surface extends from ordinary HTTP and worker flows through
Hermes columnar reads, Rust/WASM and SharedArrayBuffer, Go SIMD, native
FFI/shared memory, Tauri device byte lanes, WebGPU/native GPU resources, and
portable compatibility fallbacks. These are capability-selected refinements of
one visible contract, not separate programming models.

---

## The Performance Ladder

Foundation uses seven performance planes. Each plane has its cost measured and enforced:

```text
1. Direct dispatch        10–30 ns/op     (same-process, zero-alloc)
2. Binary frames          20–80 ns/op     (borrowed views)
3. Generated protobuf     ~370 ns/op      (typed cross-process)
4. gRPC                   20–30 µs/op     (network machinery)
5. JSON                   ~30 µs/op       (compatibility)
6. Native FFI/SHM         (varies)        (trusted compute)
7. Browser + WASM + SAB   (platform)      (where supported)
```

**Key rule**: The fastest lane must not pay the cost of the compatibility lane. This is measured automatically; regressions are caught before they land.

Read more: [`docs/foundation_benchmarks.md`](docs/foundation_benchmarks.md)

---

## Day-One Capabilities

Every project generated from Foundation receives:

1. **Multi-Tenant Isolation** — organization scope derived from authenticated context, never from client data
2. **Event-Driven Nervous System** — canonical `requested → success / failed` lifecycle with correlation metadata
3. **Hermes Hotplane** — node-local, memory-bounded, indexed read models that project database mutations in real-time
4. **Resumable File Transfers** — progress-bearing, chunk-based upload/download with resumability
5. **Bounded Worker Processing** — background jobs with exponential backoff, retry policies, and bounded queues
6. **Unified Observability** — OpenTelemetry tracing, structured logs, circuit breakers, error taxonomy

---

## Quick Paths

### For Developers

Start here → [`docs/foundation_quick_start.md`](docs/foundation_quick_start.md) (15 min) → [`docs/foundation_tour.md`](docs/foundation_tour.md) (walk-through) → [`docs/foundation_architecture_contract.md`](docs/foundation_architecture_contract.md) (platform/project split)

### For Architects & Reviewers

Start here → [`docs/PHILOSOPHY.md`](docs/PHILOSOPHY.md) (why Foundation exists) → [`docs/foundation_architecture_contract.md`](docs/foundation_architecture_contract.md) → [`docs/foundation_nervous_system.md`](docs/foundation_nervous_system.md) → [`docs/practice_controls.md`](docs/practice_controls.md)

### For AI Agents & Partners (Agent-Native Workflow)

Start here → [`AGENTS.md`](AGENTS.md) → [`docs/foundation_glossary.md`](docs/foundation_glossary.md) → [`docs/agent_operating_contract.md`](docs/agent_operating_contract.md) → [`docs/ai_threat_model.md`](docs/ai_threat_model.md)

---

## Repository Map

| Path | Purpose |
| --- | --- |
| `server-kit/` | Go backend: registry, metadata, events, workers, resilience, observability, Hermes, eventlog, Redis, database, transfer, projection gateway, object storage, bulk operations, intelligence signals |
| `runtime-transport/` | Protocol contracts, command bus, route registry, binary codecs, Hermes projection schemas |
| `runtime-sdk/` | WASM/Rust/Go kernel, 4KB control-buffer, shared arena, runtime lane helpers |
| `runtime-native/` | Tauri shell, secure storage, native frames, device dispatch |
| `frontend-kit/` | IndexedDB storage, metadata, runtime artifacts, transfer progress |
| `ui-minimal/` | Shared UI primitives, theme tokens, motion helpers |
| `config-contracts/` | Generated configuration schemas |
| `templates/` | Scaffold templates copied into generated projects |
| `docs/` | Architecture, practices, guides, security, performance, testing |
| `tooling/` | Enforcement scripts, manifests, lint configs |

---

## Core Commands

```bash
make generate-contracts      # Code gen (Protos → Go/TS)
make lint                    # All linters
make test                    # All tests
make check-rust              # Rust fmt, clippy, tests
make verify                  # Full CI suite
make check-practice-controls # Practice matrix
make check-doc-references    # Link validation

make docker-up               # Start local infra
make test-service-backed     # Tests with live DB/Redis
```

---

## Scaffolding & Development Velocity

Bootstrapping a new project should not require reinventing the wheel. Foundation's scaffolding tool creates a fully configured, production-ready workspace in seconds, designed specifically to be simple for human developers to navigate and deterministic for AI agents to write code in.

### Why the Scaffold Facilitates Modern Development

- **Instant Production Stack**: Humans get a ready-to-run environment with PostgreSQL, Redis, DORA telemetry, and a Go-React-Rust runtime pre-wired.
- **Deterministic AI Context**: Agents get strict `.cursorrules` / `.clauderules`, a clear `AGENTS.md` operating contract, and automated checks in `tooling/` that act as compilation gates. They don't have to guess or hallucinate APIs.
- **Clean Sync Boundaries**: Upstream updates to core foundation modules (like `server-kit` or `runtime-transport`) can be merged into existing projects cleanly using the `update` command (shown below) without overwriting custom application code.

### Bootstrapping a New Project

From the parent directory of `foundation`:

```bash
# Initialize a new project with a performance-oriented stack
node foundation/cmd/ovasabi/bin/ovasabi.js init --profile=performance --name=my-app --foundation-dir foundation --skip-license

# Update an existing project to sync with core updates
node foundation/cmd/ovasabi/bin/ovasabi.js update --project-dir=/path/to/project --foundation-dir foundation --skip-license
```

*Note: Generated projects consume Foundation through package boundaries. Do not import raw `foundation/*/ts/src` or `foundation/*/go` directly.*

---

## Philosophy & Motivation

Modern hardware is insanely fast, yet modern software stacks feel heavy and slow. A 3.0 GHz CPU core completes a clock cycle every 0.33 nanoseconds, but typical web apps spend milliseconds on router dispatch, JSON serialization, and garbage collection.

I built Foundation to bridge this software deficit. By collapsing the translation tower and defining every operation as a single, immutable **state event** from day one, we achieve:

- **Zero-Allocation Hotpaths**: The fastest lanes pass memory references (binary frames, SharedArrayBuffers), avoiding the latency cliffs of GC pressure.
- **Hermes Hotplane**: Local memory-bounded read models project Postgres mutations in real-time, handling query loads in microsecond domains.
- **Continuous Architectural Governance**: We shift code quality from manual reviews to automated compilation gates. Practice controls check loop boundaries, tenant isolation, and complexity thresholds on every commit.

This structural discipline is what makes high-performance full-stack applications buildable and maintainable at scale.

Read [`docs/PHILOSOPHY.md`](docs/PHILOSOPHY.md) to understand the design.

---

## Documentation

**Start here**: [`docs/README.md`](docs/README.md) is the documentation map.

**Key reads** (in order):

1. [`docs/foundation_glossary.md`](docs/foundation_glossary.md) — concept lookup
2. [`docs/foundation_quick_start.md`](docs/foundation_quick_start.md) — 15-minute path
3. [`docs/foundation_tour.md`](docs/foundation_tour.md) — walk-through one action
4. [`docs/foundation_architecture_contract.md`](docs/foundation_architecture_contract.md) — ownership split
5. [`docs/foundation_nervous_system.md`](docs/foundation_nervous_system.md) — lifecycle contract

**If you're using AI tools**: [`AGENTS.md`](AGENTS.md) — agent workflows and evidence requirements

**To understand why**: [`docs/PHILOSOPHY.md`](docs/PHILOSOPHY.md) — the motivation and design principles

---

## Work in Progress

Foundation is actively evolving. The entire repository represents a work-in-progress baseline for production applications. Expect:

- Continued refinement of contracts and practices
- New performance planes (GPU compute, distributed tracing refinement)
- Agentic coding patterns still being proven
- Documentation expanding as usage patterns emerge

This repository is designed for agent-assisted development. While I review and sign off on all modifications, many of the underlying modules, tests, and reference documents are co-authored by AI agents verifying their work via strict, machine-decidable evidence gates.

Read [`docs/agent_operating_contract.md`](docs/agent_operating_contract.md) for evidence and handoff expectations.

---

## License

See [`LICENSE`](LICENSE).
