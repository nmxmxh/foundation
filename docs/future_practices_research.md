# Future Practices Research Ledger

Status: baseline
Date: 2026-06-01
Owner: Platform Architecture

## Purpose

This ledger tracks current research and industry practice gaps that should move
Foundation from a strong present-day scaffold into an agent-native future
runtime. It is intentionally mapped back to the existing docs so one architect
can assign targeted work to multiple agents without losing the architecture
thread.

Use this file for research-backed deltas. Once a delta becomes an adopted rule,
move it into the owning practice document and, where possible, into tooling.

## Research Lanes To Track

1. Agentic software engineering: task localization, patch validation, agent
   handoff, reproducible evals, contamination-resistant benchmarks, and
   multi-agent coordination.
2. AI and tool security: prompt injection, tool-output poisoning, MCP/tool
   permission boundaries, memory poisoning, generated-code provenance, and
   unsafe autonomous execution.
3. Low-level performance: CPU counters, allocator pressure, cache/TLB/branch
   behavior, virtual memory, syscall shape, I/O zero-copy, WebGPU/CUDA/native
   GPU timing, and thermal/cold-start profiles.
4. Formal and model-based practice: TLA+, PlusCal, Alloy, P-style state
   machines, model-based tests, and invariant-to-test mapping.
5. Data-plane evolution: PostgreSQL 18+ async I/O and observability, Redis 8+
   streams/cache behavior, columnar exports, vector/ANN recall gates, and
   projection freshness contracts.
6. Operational excellence: OpenTelemetry semantic conventions, DORA, SPACE,
   DevEx, SLSA/SBOM/provenance, incident-linked delivery records, and flaky-test
   budgets.
7. Near-data and movement-avoidance compute (added 2026-07-02, source: the
   Processing-using-Memory literature — RowClone, Ambit, HBM-PIM, CXL memory):
   the governing question is "why is this component involved in this byte's
   journey at all?" Software-level deltas to track and promote:
   - kernel zero-copy primitives as measured lanes for artifact/snapshot
     movement: `copy_file_range`, reflink clones, `sendfile`/`splice`,
     `io_uring`, mmap page-cache reads (candidate: hermessnapshot artifact
     serving and snapshot-tier warm paths);
   - bitmap-predicate merges in Hermes columnar reads: evaluate multi-filter
     queries as bulk AND/OR over packed validity/index bitmaps (POPCNT lane)
     before touching record memory — the software form of Ambit's in-place
     bulk bitwise operations;
   - an explicit bytes-moved-per-op budget next to B/op in benchmark rows, so
     movement (not just allocation) is a gated regression class;
   - lane-planner readiness for near-memory hardware (PIM DIMMs, CXL pooled
     memory, GPU near-data kernels): compute location is already a planned,
     benchmarked decision in Foundation, so a near-data device is a new lane,
     not a rearchitecture.

## Per-Document Gap Map

| Document | Future optimization to add or research |
| --- | --- |
| `README.md` | Add architect, agent, and reviewer reading paths plus enforceability level per doc. |
| `agent_operating_contract.md` | Keep current with AI-agent benchmarks, tool security, multi-agent handoff practice, and succession continuity when the primary architect is unavailable. |
| `practice_controls.md` | Keep the machine-readable controls matrix aligned with every CP/TE rule, cross-cutting agent/security/performance control, and scaffolded enforcement path. |
| `ai_threat_model.md` | Track OWASP LLM/agentic/MCP threat classes, tool sandbox research, provenance scoring, and contamination-resistant agent evaluations. |
| `ai_practices.md` | Add prompt-injection, tool poisoning, memory poisoning, agent identity, generated-code provenance, model/tool eval gates, and MCP permission review. |
| `info/coding_magic.md` | Add agent-era design intuition: proof-carrying patches, contract compression, representation design for agents, and evidence-led automation. |
| `coding_practices.md` | Split every rule into machine-enforced, review-enforced, and evidence-required. Add agent patch evidence, Go 1.25+ concurrency updates, TS package-boundary rules, and Rust 2024 unsafe posture. |
| `testing_practices.md` | Add oracle-strength scoring, mutation score thresholds where feasible, flaky-test quarantine, property seed ledgers, model-based protocol tests, and agent-generated-test review rules. |
| `security_practices.md` | Add AI/tool/MCP threat modeling, SLSA/SBOM/provenance, secrets-in-context policy, package install-script review, and memory-safe-roadmap tracking. |
| `tla_architecture_practices.md` | Add reusable lightweight spec templates for queues, workers, Redis Streams, Hermes, websocket routing, idempotent commands, and cache freshness. |
| `go_concurrency_bug_practices.md` | Track Go `WaitGroup.Go`, container-aware `GOMAXPROCS`, runtime trace, pprof block/mutex profiles, leak harnesses, and structured concurrency candidates. |
| `performance_practices.md` | Add CPU-counter taxonomy, allocator-trace posture, syscall/I/O copy-budget review, thermal/cold-start budgets, and "do not optimize" criteria. |
| `performance_lab.md` | Track repeatable evidence bundles for CPU counters, allocator traces, syscall/I/O shape, cold/warm cache, WASM/FFI, native, WebGPU, and GPU timings. |
| `foundation_benchmarks.md` | Convert benchmark notes into a registry with owner, machine class, variance, regression threshold, last valid SHA, replay command, and linked guard test. |
| `database_practices.md` | Track PostgreSQL 18/19 async I/O, skip-scan caveats, virtual generated columns, `pg_stat_io`, WAL bytes/op, RLS tests, vector recall, and projection-lag fences. |
| `redis_practices.md` | Track Redis 8 behavior, client-side cache invalidation, Streams pending recovery, shard policy, script safety, big-key automation, and eviction simulation. |
| `websocket_scaling.md` | Add reconnect storm modeling, browser backpressure, slow-client fairness, auth-expiry mid-socket tests, QUIC/WebTransport research, and topic fanout complexity budgets. |
| `runtime_foundation.md` | Add lane-selection proof tables: direct Go, Rust, WASM/SAB, FFI, shared memory, stdio, native GPU, WebGPU, WebSocket, HTTP, registry dispatch, graceful event emission, and JSON fallback. |
| `runtime_native.md` | Add native plugin provenance, OS permission matrices, fd/handle leak checks, sensor/camera/audio latency budgets, and mobile store/privacy review. |
| `rust_runtime_practices.md` | Track Rust 2024 unsafe discipline, Miri eligibility, Loom concurrency tests, `cargo-semver-checks`, panic strategy, Criterion profiles, and FFI fuzzing. |
| `gpu_practices.md` | Add WebGPU compatibility matrix, WGSL layout generator, device-loss chaos tests, CUDA graph invalidation, occupancy-vs-latency guidance, and capture bundle schema. |
| `game_runtime_practices.md` | Add hitch ledger, browser trace bundle, input latency vs frame latency, quality-tier contracts, and streaming priority scheduler rules. |
| `hermes_hotplane.md` | Add freshness taxonomy: monotonic, read-your-write, bounded-stale, stale-while-revalidate, and fallback-required. Add Merkle/count/watermark drift repair. |
| `projection_freshness_contract.md` | Keep projection, cache, search, materialized-view, and Hermes freshness modes synchronized with tests and metrics. |
| `foundation_nervous_system.md` | Promoted 2026-06-08: `docs/references/lifecycle/lifecycle_contract.json` is now the machine-readable lifecycle source for event names, worker metadata, and review vectors. Next research: generated handler skeletons and richer implementation-test scaffolds from the same manifest. |
| `foundation_architecture_contract.md` | Track profile adoption evidence for Core, Lite, Performance, and Regulated scaffold modes without weakening mandatory lifecycle/security invariants. |
| `foundation_guide.md` | Split into architect, agent, and operator paths. Keep examples current with runtime and scaffold contracts. |
| `foundation_tour.md` | Add failure tours for duplicate command, tenant mismatch, stale Hermes, Redis down, worker timeout, partial batch failure, and typed-payload JSON compatibility regressions. |
| `optimization_points.md` | Convert future targets into hypothesis cards: bottleneck, expected win, invariant, benchmark, rollout, and rollback. |
| `delivery_metrics_practices.md` | Add DevEx/SPACE metrics: cognitive load, review latency, agent rework, escaped defects, flaky-test rate, and local setup time. |
| `frontend_scaffold_sync.md` | Add UI agent protocol: read `DESIGN.md`, inspect primitives, avoid raw aliases, run screenshots, check responsive text fit and reduced motion. |
| `styling_design_practices.md` | Add automated visual QA: Playwright screenshots, contrast checks, layout-overlap detection, reduced-motion tests, and motion snapshot review. |
| `migration_practices.md` | Add production transition mode: expand/contract migrations, online backfills, lock estimation, backup verification, and restartable data movement. |
| `scaffold_manifest.md` | Add agent conflict policy for generated updates and mode changes. |
| `info/scaffolded_projects_executive_summary.md` | Add Foundation-as-control-plane framing for IP, governance, and multi-product operational value. |
| `post_quantum_security.md` | Add crypto inventory schema, artifact-signing policy, hybrid TLS compatibility tests, and PQ latency benchmark gates. |
| `references/*` | Add an agent checklist to each note: use when, do not use when, required verification, and accessibility/performance proof. |

## Promotion Rules

Research becomes Foundation practice only when all are true:

1. The source is current and relevant to the affected Foundation lane.
2. The recommendation maps to a contract, invariant, test, benchmark, scaffold
   default, or review checklist.
3. The owning document is updated.
4. Enforcement is added when the rule is low-noise and machine-detectable.
5. An exception path is documented when the rule is contextual.

## Source Classes

Prefer primary or near-primary sources:

1. Official docs and standards: Go, Rust, PostgreSQL, Redis, WebGPU, WGSL,
   CUDA, Tauri, OpenTelemetry, NIST, OWASP, CISA, W3C, Khronos.
2. Research papers with a clear method and limitation.
3. Vendor performance guides when results are verified against Foundation
   benchmarks instead of copied as assumptions.
4. Incident reports and benchmark ledgers from Foundation projects.

Avoid promoting advice from posts, model memory, or summaries unless it is
verified against a primary source or local evidence.

## Review Checklist

- [ ] Research item has a date and source class.
- [ ] The affected Foundation document is named.
- [ ] The proposed rule states whether it is mandatory, recommended, or
      contextual.
- [ ] The evidence type is clear: test, benchmark, trace, capture, query plan,
      spec note, or review-only.
- [ ] The update avoids duplicating canonical contracts owned by another doc.
