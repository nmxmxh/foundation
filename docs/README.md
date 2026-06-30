# Foundation Documentation Map

**Status**: current as of 2026-06-30

This directory contains the source documentation for Foundation Core and the reference copy distributed to generated projects. Keep files short enough for humans to scan and precise enough for coding agents to enforce.

---

## Getting Started (Pick Your Path)

### I'm New Here—Show Me the Essentials (15 minutes)

Read in order:

1. [`foundation_glossary.md`](foundation_glossary.md) — concept lookup; start here for definitions
2. [`foundation_quick_start.md`](foundation_quick_start.md) — minimum viable path, critical first questions, common mistakes
3. [`foundation_tour.md`](foundation_tour.md) — one product action through Foundation end-to-end
4. [`PHILOSOPHY.md`](PHILOSOPHY.md) — why Foundation exists, what gap it fills

### I'm a Developer—What Do I Need to Know?

Read in order:

1. [`foundation_quick_start.md`](foundation_quick_start.md) — 15-minute baseline
2. [`foundation_tour.md`](foundation_tour.md) — one complete flow
3. [`foundation_architecture_contract.md`](foundation_architecture_contract.md) — platform/project ownership
4. [`foundation_nervous_system.md`](foundation_nervous_system.md) — canonical lifecycle and invariants

Then read only the lane-specific docs for code you're changing:

- Backend/domain logic: `coding_practices.md`, `database_practices.md`, `security_practices.md`, `testing_practices.md`
- Real-time/WebSocket: `websocket_scaling.md`, `hermes_read_modes.md`
- Workers/async: `go_concurrency_bug_practices.md`, `coding_practices.md` (bounded work rules)
- Frontend: `frontend_scaffold_sync.md`, `styling_design_practices.md`

### I'm an Architect—Show Me the Full Picture

Read in order:

1. [`PHILOSOPHY.md`](PHILOSOPHY.md) — the thesis and motivation
2. [`foundation_architecture_contract.md`](foundation_architecture_contract.md) — platform, scaffold, project ownership
3. [`foundation_nervous_system.md`](foundation_nervous_system.md) — canonical lifecycle
4. [`foundation_guide.md`](foundation_guide.md) — comprehensive module reference
5. [`practice_controls.md`](practice_controls.md) — which rule is enforced by which check

Then read deeply into domains that matter for your product:

- **Data**: `database_practices.md`, `redis_practices.md`, `hermes_hotplane.md`, `projection_freshness_contract.md`
- **Observability**: `delivery_metrics_practices.md`, `performance_practices.md`
- **Security**: `security_practices.md`, `ai_threat_model.md`, `post_quantum_security.md`
- **Performance**: `performance_lab.md`, `foundation_benchmarks.md`, `optimization_points.md`

### I'm Working With AI Agents or Partners

Read in order:

1. [`../AGENTS.md`](../AGENTS.md) — agent workflows and role definitions
2. [`foundation_glossary.md`](foundation_glossary.md) — concept lookup
3. [`foundation_quick_start.md`](foundation_quick_start.md) — minimum viable path
4. [`foundation_architecture_contract.md`](foundation_architecture_contract.md) — ownership boundaries
5. [`agent_operating_contract.md`](agent_operating_contract.md) — definition of done, evidence requirements
6. [`practice_controls.md`](practice_controls.md) — control matrix, enforcement gates
7. [`ai_threat_model.md`](ai_threat_model.md) — threat classes, validation requirements
8. [`../agent_memory/CONDUCTOR.md`](../agent_memory/CONDUCTOR.md) — autonomous build loop, memory patterns

---

## Core Architecture & Design

| Document | Audience | Purpose |
| --- | --- | --- |
| [`PHILOSOPHY.md`](PHILOSOPHY.md) | Everyone | Why Foundation exists, what gap it fills, the software deficit, Hermes principle, metadata carrier |
| [`foundation_glossary.md`](foundation_glossary.md) | Lookup reference | Concept definitions, module cards, invariant summaries, agent Q&A |
| [`foundation_quick_start.md`](foundation_quick_start.md) | First-time readers | 15-minute path, critical questions, common mistakes, minimum evidence |
| [`foundation_tour.md`](foundation_tour.md) | Learners | Walk-through one product action from ingress to workers to observability |
| [`foundation_guide.md`](foundation_guide.md) | Architects, deep-dive | Comprehensive agent & developer guide, module reference, extended services |
| [`foundation_architecture_contract.md`](foundation_architecture_contract.md) | Architects, reviewers | Platform/scaffold/project ownership boundaries, extension rules |
| [`foundation_nervous_system.md`](foundation_nervous_system.md) | Architects, developers | Canonical lifecycle, envelope contract, invariants, correlation flow |

---

## Backend & Data Layer

| Document | Purpose |
| --- | --- |
| `database_practices.md` | PostgreSQL schema, query, pool, migration, and operational rules |
| `migration_practices.md` | Migration structure, reversibility, safety checks |
| `redis_practices.md` | Redis coordination, cache, stream, rate-limit rules |
| `hermes_hotplane.md` | Hermes node-local projection contract, scaffold wrapper, consistency modes |
| `hermes_read_modes.md` | Stable v1 read modes: `fenced`, `live`, `stale_while_revalidate`, `postgres_required` |
| `projection_freshness_contract.md` | Freshness modes and evidence for Hermes, read models, search, views, caches |
| `transfer_lane.md` | Progress-bearing transfer operations: upload/download lifecycle, resumability |
| `websocket_scaling.md` | Socket routing, metrics, fanout, scaling budgets |

---

## Coding Practices & Quality

| Document | Purpose |
| --- | --- |
| `coding_practices.md` | Core reliability, performance, and security rules (CP-* rules) |
| `testing_practices.md` | Test adequacy, generated lifecycle tests, TE-* rules, CI expectations |
| `security_practices.md` | Trust boundaries, auth posture, secrets, audit, ingress controls |
| `go_concurrency_bug_practices.md` | Bounded concurrency patterns, known Go failure modes |
| `practice_controls.md` | Machine-readable mapping from rules to scripts, evidence, merge-gate posture |
| `ai_threat_model.md` | Prompt/tool/memory poisoning, generated-code provenance, validation vocabulary |

---

## Performance & Runtime

| Document | Purpose |
| --- | --- |
| `performance_practices.md` | Cross-cutting Go, networking, PostgreSQL, Rust benchmarking rules |
| `performance_lab.md` | Low-level evidence contract for CPU, allocator, syscall, I/O, WASM, FFI, GPU lanes |
| `foundation_benchmarks.md` | Benchmark commands, reference runs, interpretation, performance ladder |
| `rust_runtime_practices.md` | Rust/WASM/native runtime coding, async, performance, error-handling |
| `runtime_foundation.md` | Runtime ladder, WASM/native lanes, binary transport, control-buffer discipline |
| `runtime_native.md` | Tauri/native shell device access, native byte lanes, secure storage, GPU policy |
| `gpu_practices.md` | WebGPU/native GPU batching, memory, verification |
| `game_runtime_practices.md` | Frame-budgeted runtime practices for visual and interactive loops |
| `mathematical_practices.md` | Numerical analysis, floating-point, probability, statistics, CRDT convergence |
| `tla_architecture_practices.md` | State-machine, invariant, liveness, real-time bound practices from Specifying Systems |

---

## Frontend & Product

| Document | Purpose |
| --- | --- |
| `frontend_scaffold_sync.md` | Frontend package boundaries, generated types, scaffold sync contract |
| `frontend_command_registry.md` | Generated route registry, `createAppRuntime`, dispatch path |
| `frontend_runtime_workbench.md` | Frontend workbench: dummy data, tenant stores, live projections |
| `styling_design_practices.md` | UI primitive, theme, animation, visual design practice |
| `runtime_sab_capnp_contracts.md` | SharedArrayBuffer and Cap'n Proto runtime contracts |
| `references/README.md` | References index (UI Animation, Lifecycle Manifests, Security Profiles) |

---

## Operations & Distribution

| Document | Purpose |
| --- | --- |
| `foundation_distribution.md` | CLI bootstrap, registry boundaries, agent bundle, license verification |
| `scaffold_manifest.md` | Scaffold manifest columns, update modes, safe maintenance workflow |
| `foundation_project_standardization.md` | Project drift measurement, `appkit` extraction plan |
| `foundation_tooling.md` | Protocol compiler targets, route generators, verification matrix, fleet updates |
| `delivery_metrics_practices.md` | DORA, SPACE/DevEx, OpenTelemetry linkage, SBOM, provenance |
| `enforcement.md` (in `tooling/docs/`) | Lint strictness, communication lane enforcement, ownership checks, operational gates |

---

## AI & Agentic Development

| Document | Purpose |
| --- | --- |
| [`../AGENTS.md`](../AGENTS.md) | Agent terminology, operating baseline, tech stack, architecture overview |
| `agent_operating_contract.md` | Role split, definition of done, evidence ledger, handoff format |
| `ai_practices.md` | AI runtime as product capability, trust boundary, bounded work, verification |
| `ai_threat_model.md` | AI-specific threats: tool poisoning, memory poisoning, provenance, secrets |
| [`../agent_memory/CONDUCTOR.md`](../agent_memory/CONDUCTOR.md) | Closed-loop autonomous build loop, verification gates, memory patterns |
| [`../agent_memory/README.md`](../agent_memory/README.md) | Persistent memory in CWF format, sessions, index, validation rules |

---

## Informational & Research

Non-practice reference documents:

| File | Purpose |
| --- | --- |
| `info/scaffolded_projects_executive_summary.md` | Non-technical overview of products built on Foundation |
| `info/columnar_projection_lane.md` | Design spec for Arrow-compatible columnar Hermes projections |
| `info/coding_magic.md` | Product-quality interaction and presentation inspiration |
| `future_practices_research.md` | Research gap ledger mapped to each Foundation document |

---

## How to Navigate

1. **If you need a concept definition**: Jump to `foundation_glossary.md`
2. **If you're new to Foundation**: Start with `foundation_quick_start.md` (15 min), then `foundation_tour.md`
3. **If you're reviewing architecture**: Read `foundation_architecture_contract.md` and `practice_controls.md`
4. **If you're changing code**: Read the lane-specific practice doc (see "Coding Practices" above)
5. **If performance matters**: Read `foundation_benchmarks.md` and the relevant performance practice doc
6. **If you're working with agents**: Read `AGENTS.md`, then `agent_operating_contract.md`, then lane-specific docs
7. **If you want to understand why**: Read `PHILOSOPHY.md`

---

## Maintenance Rules

1. Do not duplicate canonical contracts. Link to the owning document instead.
2. Update this map when adding or retiring a document.
3. When code changes an invariant, benchmark expectation, or enforcement rule, update the owning doc in the same change.
4. Keep generated-project paths distinct from Foundation Core paths: `tooling/scripts` is source, `scripts/checks` is the generated copy.
5. Non-practice informational documents belong in `docs/info/`, not alongside contracts and practice docs.

---

## Core Repository Commands

For Foundation Core:

```bash
make generate-contracts             # Full code gen
make lint                           # All linters
make test                           # All tests
make check-rust                     # Rust verification
make verify                         # Full CI suite
make check-practice-controls        # Practice matrix
make check-doc-references           # Link validation
make lifecycle-manifest             # Regenerate lifecycle
```

For service-backed verification (requires local infra):

```bash
make docker-up
make test-service-backed
```

For generated projects:

```bash
make communication-contracts        # Verify contract parity
make lint-foundation               # Foundation-specific lints
make test-all                      # All project tests
```
