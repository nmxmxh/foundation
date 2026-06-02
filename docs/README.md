# Foundation Documentation Map

Status: current as of 2026-05-26

This directory is the source documentation set for Foundation Core and the
`docs/foundation/` reference copied into generated projects. Keep these files
short enough for humans to scan and precise enough for coding agents to enforce.

## First Reads

Read these in order when onboarding, reviewing architecture, or changing a
generated scaffold:

1. `foundation_architecture_contract.md`: ownership boundaries between platform
   modules, managed scaffold, and project-owned code.
2. `foundation_nervous_system.md`: canonical command, event, worker, projection,
   and realtime lifecycle.
3. `foundation_guide.md`: broad agent/developer guide and module overview.
4. `foundation_tour.md`: one request through Foundation end to end.
5. `agent_operating_contract.md`: required workflow for one architect and
   multiple AI agents working from the same Foundation contracts.
6. `practice_controls.md`: machine-readable mapping from rules to scripts,
   evidence, and merge-gate posture.
7. `ai_threat_model.md`: AI/tool/agent threat classes and required validation
   evidence.
8. `coding_practices.md`: enforceable CP rules used by review and tooling.
9. `testing_practices.md`: test adequacy rules, generated lifecycle tests, and
   CI expectations.
10. `security_practices.md`: trust boundaries, auth posture, secrets, audit, and
   ingress controls.

## Architecture And Runtime

| File | Use |
| --- | --- |
| `runtime_foundation.md` | Runtime ladder, WASM/native lanes, binary transport, and control-buffer discipline. |
| `runtime_native.md` | Tauri/native shell device access, native byte lanes, secure storage, and GPU handle policy. |
| `rust_runtime_practices.md` | Rust/WASM/native runtime coding, async, performance, error-handling, and check automation rules. |
| `foundation_tour.md` | One product action through Foundation from ingress to durable state, workers, Hermes, and observability. |
| `foundation_nervous_system.md` | Canonical lifecycle and invariants for envelopes, events, workers, stores, and projections. |
| `hermes_hotplane.md` | Hermes node-local projection contract, scaffold wrapper, consistency modes, and operational watch points. |
| `foundation_architecture_contract.md` | Platform/scaffold/project ownership split and extension rules. |
| `tla_architecture_practices.md` | State-machine and invariant practice for high-risk changes. |
| `specs/tla/` | Starter TLA modules for worker retry queues, projection freshness, and WebSocket backpressure. |
| `scaffold_manifest.md` | Scaffold manifest columns, update modes, and safe maintenance workflow. |
| `agent_operating_contract.md` | Architect/agent role split, definition of done, evidence ledger, handoff format, and AI-specific safety rules. |
| `practice_controls.md` | Control matrix maintenance rules for CP, TE, and cross-cutting agent/security/performance controls. |
| `projection_freshness_contract.md` | Freshness modes and evidence requirements for Hermes, read models, search, materialized views, and Redis caches. |
| `future_practices_research.md` | June 2026 research gap ledger mapped back to each Foundation document. |

## Backend And Data

| File | Use |
| --- | --- |
| `database_practices.md` | PostgreSQL schema, query, pool, migration, and operational rules. |
| `migration_practices.md` | Migration structure, reversibility, and generated-project checks. |
| `redis_practices.md` | Redis coordination, cache, stream, and rate-limit rules. |
| `websocket_scaling.md` | Socket routing, metrics, fanout, and scaling budgets. |
| `go_concurrency_bug_practices.md` | Bounded concurrency and known Go failure patterns. |
| `delivery_metrics_practices.md` | DORA, SPACE/DevEx, OpenTelemetry linkage, SLSA/SBOM/provenance, and incident-linked delivery metrics. |
| `ai_threat_model.md` | Prompt/tool/memory poisoning, generated-code provenance, and AI/tool security review vocabulary. |

## Performance And Compute

| File | Use |
| --- | --- |
| `performance_practices.md` | Cross-cutting performance posture and measurement workflow. |
| `performance_lab.md` | Low-level performance evidence contract for CPU, allocator, syscall, I/O, WASM, FFI, native, and GPU lanes. |
| `rust_runtime_practices.md` | Rust runtime measurement, clone/borrow, bounds, unsafe, async, and parity rules. |
| `foundation_benchmarks.md` | Benchmark commands, reference runs, and interpretation notes. |
| `optimization_points.md` | Adopted optimization decisions and future targets. |
| `gpu_practices.md` | WebGPU/native GPU batching, memory, and verification rules. |
| `game_runtime_practices.md` | Frame-budgeted runtime practices for visual and interactive loops. |

## Frontend And Product UI

| File | Use |
| --- | --- |
| `frontend_scaffold_sync.md` | Frontend package boundaries, generated types, and scaffold sync contract. |
| `styling_design_practices.md` | UI primitive, theme, animation, and visual design practice. |
| `coding_magic.md` | Product-quality interaction and presentation inspiration. |
| `references/README.md` | Focused UI animation and component reference index. |

## Tooling And Enforcement

Use `tooling/docs/enforcement.md` for lint strictness, communication lane
enforcement, ownership checks, and operational gates. The source scripts live in
`tooling/scripts/`; generated projects receive them under `scripts/checks/`.
`agent_contract_check.sh` verifies that generated projects keep the agent
operating contract, research ledger, and agent-facing templates wired into the
scaffold.
`practice_controls_check.sh` verifies that `tooling/practice_controls.psv`
contains every `CP-*` and `TE-*` rule plus cross-cutting controls, and that
referenced docs/scripts exist in both Foundation and generated-project layouts.
`runtime_performance_contract_check.sh`, `formal_methods_check.sh`, and
`operational_excellence_check.sh` verify that low-level runtime evidence,
formal-method templates, and DORA/SPACE/SLSA/OpenTelemetry delivery hooks remain
wired into Foundation and generated projects.

Core repository commands:

```bash
make lint
make test
make verify
make check-runtime-performance-contracts
make check-formal-methods
make check-operational-excellence
```

Generated project commands:

```bash
make communication-contracts
make lint-foundation
make test-all
```

## Maintenance Rules

1. Do not duplicate canonical contracts. Link to the owning document instead.
2. Update this map when adding or retiring a document.
3. When code changes an invariant, benchmark expectation, generated scaffold
   shape, or enforcement rule, update the owning doc in the same change.
4. Keep generated-project paths distinct from Foundation Core paths:
   `tooling/scripts` is the source, `scripts/checks` is the generated copy.
