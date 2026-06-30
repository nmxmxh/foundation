# Ovasabi Foundation (Work in Progress — Version 0.0.1)

**A full-stack application substrate for high-performance, event-driven systems.**

Ovasabi Foundation is an integrated toolkit for teams that want to evolve code. It provides the platform modules, scaffolds, enforcement checks, and documentation to bootstrap and maintain production systems with:

- **Tenant-isolated, event-driven architecture** — every operation carries metadata: who asked, what organization, correlation ID
- **Performance ladder** — seven planes from nanosecond direct dispatch to microsecond JSON compatibility
- **Hermes hotplane** — bounded, node-local projections for sub-microsecond operational reads
- **Worker orchestration** — bounded background processing with retry policies and progress tracking
- **Built-in observability** — logs, metrics, and traces automatically linked by correlation ID

Not a no-code platform. Not zero-DevOps. Not for teams that want to move fast by cutting corners. Foundation is for teams that embrace managed infrastructure, understand performance, and expect their codebase to evolve.

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

### For AI Agents & Partners

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

## Project Bootstrap

From the parent directory of `foundation`:

```bash
# New project
node foundation/cmd/ovasabi/bin/ovasabi.js init --profile=performance --name=my-app --foundation-dir foundation --skip-license

# Or via shell script (legacy)
./foundation/scripts/init-project.sh my-app full

# Update existing project
node foundation/cmd/ovasabi/bin/ovasabi.js update --project-dir=/path/to/project --foundation-dir foundation --skip-license
```

Generated projects consume Foundation through package boundaries. Do not import raw `foundation/*/ts/src` or `foundation/*/go` directly.

---

## Philosophy & Motivation

Foundation bridges the **software deficit**: the gap between hardware performance (nanoseconds) and typical software stacks (milliseconds). It provides proven patterns for:

- **Responding instantly** — sub-microsecond operational reads via Hermes
- **Scaling safely** — bounded work, tenant isolation, circuit breakers
- **Observing clearly** — correlation IDs flowing through all layers
- **Evolving confidently** — enforced practices, contract verification, performance measurement

Foundation is not for everyone. It's demanding. It requires discipline: thinking about performance trade-offs, writing adequate tests, understanding failure modes, reviewing gate verdicts. It's for teams that expect to build something ambitious.

Read [`docs/PHILOSOPHY.md`](docs/PHILOSOPHY.md) for the full story.

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

Contributions via research, agents, and human reviewers are how Foundation improves. Read [`docs/agent_operating_contract.md`](docs/agent_operating_contract.md) for evidence and handoff expectations.

---

## License

See [`LICENSE`](LICENSE).
