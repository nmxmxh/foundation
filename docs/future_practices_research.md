# Future Practices Research Ledger

Status: baseline
Date: 2026-09-24
Owner: Platform Architecture

## Purpose

This ledger tracks current research and industry practice gaps that should move
Foundation from a strong present-day scaffold into an agent-native future
runtime. It is intentionally mapped back to the existing docs so one architect
can assign targeted work to multiple agents without losing the architecture
thread.

Use this file for research-backed deltas. Once a delta becomes an adopted rule,
move it into the owning practice document and, where possible, into tooling.

## Adopted native dispatch primitives: 2026-09-24

Foundation now uses required output destinations and immutable registry snapshots for native dispatch.
[ArcSwap 1.9.2](https://docs.rs/arc-swap/1.9.2/arc_swap/) provides the snapshot primitive and its memory-order corrections.
The registry replaces its read lock with this pinned dependency.
Bounded diagnostic shards remove the shared counter from steady-state successful dispatch.
The [measured report](info/runtime_finalize_20260924.md) records large-output gains, concurrency gains, allocation budgets, and small owned-result regressions.
The owning rules reside in `rust_runtime_practices.md`.

## Research Lanes To Track

The [graphics FPS refinement](graphics_fps_refinement.md) applies current worker and WebGPU guidance to measured frame throughput.
Completed frames improve under GPU pressure without changing shader detail or backing resolution.
Mobile endurance and full-page presentation remain separate evidence requirements.

The [2026-09-22 cleanup review](foundation_cleanup_review_20260922.md) maps
current research to measured Foundation costs and remaining work.
Adopted changes repair benchmark gates, measurement boundaries, and frontend validation.
Performance guidance now distinguishes resident filtering from batch construction.
WebGL guidance corrects an invalid fence experiment. Worker animation guidance follows the HTML worker contract.
The implementation follow-up reduces columnar assembly memory and secured HTTP allocations, with regression budgets.
Updater dependency commands now have deadlines and an explicit skip option.
Open priorities include consumer compatibility, bounded diagnostics, and representative service measurements.

1. Agentic software engineering: task localization, patch validation, agent
   handoff, reproducible evals, contamination-resistant benchmarks, and
   multi-agent coordination.
2. AI and tool security: prompt injection, tool-output poisoning, MCP/tool
   permission boundaries, memory poisoning, generated-code provenance, and
   unsafe autonomous execution.
3. Low-level performance: CPU counters, allocator pressure, cache/TLB/branch
   behavior, virtual memory, syscall shape, I/O zero-copy, WebGPU/CUDA/native
   GPU timing, CUDA Rust native kernel compilation (cutile-rs Tile, cuda-oxide
   SIMT), and thermal/cold-start profiles.
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
     movement — promoted 2026-07-02: `hermessnapshot.FileStore` ships
     reflink → `copy_file_range` → userspace clone lanes (`PromoteLatest`)
     and sendfile-path serving (`OpenArtifact`), correctness-proven on Linux
     via `make bench-zerocopy-linux`; still open: ledger-grade numbers on a
     real ext4/XFS host, `io_uring`, and mmap page-cache read lanes;
   - bitmap-predicate merges in Hermes columnar reads: evaluate multi-filter
     queries as bulk AND/OR over packed validity/index bitmaps (POPCNT lane)
     before touching record memory — promoted 2026-08-02: 4-way interleaved
     accumulators for scalar sum reductions (`sumFloat64sScalar`), validity
     counts (`nullCount`), and bitmap merges (`And`/`Or`/`Count`) achieve up
     to 4.13x throughput gain (75.8% reduction in latency) via CPU Memory-Level
     Parallelism (MLP, source: Lemire 2026);
   - an explicit bytes-moved-per-op budget next to B/op in benchmark rows, so
     movement (not just allocation) is a gated regression class;
   - lane-planner readiness for near-memory hardware (PIM DIMMs, CXL pooled
     memory, GPU near-data kernels): compute location is already a planned,
     benchmarked decision in Foundation, so a near-data device is a new lane,
     not a rearchitecture.
8. Durable-queue scheduling boundaries (added 2026-08-23, source class 4:
   Pronto connector CPU runaway incident): a transactional queue is a
   crash-safety mechanism. It must not become a scheduler clock. Promoted
   2026-08-25 into `database_practices.md`, "The Queue Is Not A Scheduler
   Clock"; the ByState rule is enforced by
   `tooling/scripts/river_practices_check.sh` and by the scaffolded TE-14 guard
   `internal/worker/periodic_jobs_test.go`. The lane stays open for the
   remaining deltas below:
   - Cadence ownership: a recurring producer keeps due-times in cheap local
     state, for example a `nextDue` map seeded by one bulk read at startup.
     A sweep then submits only due work, and most sweeps submit zero jobs.
     Promotion target: `database_practices.md`.
   - Uniqueness scoping: when queue uniqueness drops from primary clock to
     safety net, set `UniqueOpts.ByState` explicitly. Verified against pinned
     rivertype v0.44.1, `river_type.go:633`: the default state set includes
     `completed`, so default-scoped uniqueness stops recurrence after the
     first completed job and ingestion dies without errors. Recurring args
     must exclude the terminal states `completed`, `cancelled`, `discarded`.
   - Backlog bound invariant: every recurring producer enforces at most one
     in-flight job per key. A `ByPeriod` lock that expires while consumers
     lag turns backlog into amplification; observed volume was 3.1 million
     duplicate rows from one degraded window.
   - Restart seeding and jitter: an empty in-memory due map makes every
     batch due at once after a restart or failover. Seed once from durable
     state and stagger sweep phases across replicas. Promotion targets:
     `database_practices.md` River lane and TE-14 worker regression tests.
9. CUDA Rust native GPU kernel programming (added 2026-09-09, source class 1:
   NVIDIA official announcement and documentation, September 2026; source
   class 2: Elibol, Koundinyan, Bentz, "Fearless Concurrency on the GPU",
   arXiv:2606.15991, RustConf 2026): NVIDIA released two projects for writing
   CUDA GPU kernels in native Rust. The governing question is whether
   Foundation should add a PTX kernel lane alongside WebGPU, WASM, and FFI for
   server-side and native GPU workloads. Software-level deltas to track:
   - **cutile-rs Tile track** (`NVlabs/cutile-rs`, crates.io `cutile`):
     tile-based GPU programming on stable Rust 1.89+, CUDA 13.3+, Linux,
     compute capability 8.0+. No nightly toolchain, no custom LLVM. The
     `#[cutile::module]` macro captures kernel AST into the host binary and
     JIT-compiles through CUDA Tile IR at first launch. Ownership model:
     mutable output tensors must be `.partition()`-ed before launch, which
     gives each tile block exclusive ownership of its sub-tensor. The
     compiler manages thread mapping and shared memory. Already used outside
     NVIDIA in HuggingFace Grout inference engine and mistral.rs. Promotion
     candidate for `gpu_practices.md` as a tracked optional compute lane
     under the `performance` Foundation profile.
   - **cuda-oxide SIMT track** (`NVlabs/cuda-oxide`, early alpha): custom
     `rustc` codegen backend that compiles `#[kernel]` Rust functions to PTX
     through Rust MIR, the Pliron IR framework, and LLVM. Requires pinned
     nightly toolchain (`nightly-2026-04-03`), LLVM, and `clang` headers.
     Safety model: `DisjointSlice<T>` gives each thread exclusive access to
     its element; `ThreadIndex` is `!Send + !Sync + !Copy + !Clone` and
     `'kernel`-scoped. `#[launch_contract]` plus `PreparedLaunch` validate
     grid geometry against kernel declarations and device limits at prepare
     time. Shared memory access still requires `unsafe`. Three-tier safety
     model: Tier 1 (safe by default), Tier 2 (scoped `unsafe` with
     contracts), Tier 3 (raw hardware intrinsics). Track only; do not adopt
     until the project ships on stable Rust.
   - **Compile-time aliasing prevention**: both projects catch the classic
     GPU data race (two threads alias the same write target) at compile time
     through Rust's ownership system. cuda-oxide uses borrow checker rules
     at each launch call (`&c_dev` and `&mut c_dev` in the same call fails
     `E0502`). cutile-rs uses move semantics across the launch boundary
     (`use of moved value` fails `E0382`). This directly supports CP-06
     (minimize mutable shared state) and CP-09 (restrict unsafe patterns)
     for GPU code. Promotion target: `gpu_practices.md` testing section.
   - **Inter-language interop**: NVIDIA plans CUDA Rust, CUDA C++, and CUDA
     Python interop so that the choice of kernel language does not lock out
     other ecosystems. Track readiness for Foundation's `kernellane`
     descriptor model: a Go orchestrator dispatches to a Rust unit that
     launches a PTX kernel, with the same `RuntimeUnitDescriptor` registry
     and capability planner that today selects WASM, FFI, or SHM.
   - **Existing Rust GPU ecosystem context**: Rust-GPU (Embark/Traverse),
     rust-cuda, CubeCL, cudarc. The cuda-oxide book maintains an ecosystem
     appendix at `nvlabs.github.io/cuda-oxide/appendix/ecosystem.html`.
     Track convergence and community library maturation.
   - **Revisit trigger**: re-evaluate cutile-rs readiness when Foundation
     projects require server-side NVIDIA GPU compute (inference, signal
     processing, batch scoring, columnar transforms). Re-evaluate cuda-oxide
     when it ships on stable Rust or when Foundation needs SIMT-level
     control (shared memory, warp-level programming, TMA).

## 2026-09-24 Performance Candidates

These items track performance research and promotion evidence.

1. Browser WASM memory: promoted into browser buffer ABI version 2 on 2026-09-24.
   Guest-owned regions now replace control-buffer copy imports by default.
   Shared builds use imported memory. Scalar builds also support direct access in their owning thread.
   The build emits both artifacts. The loader selects shared memory automatically and preserves legacy artifact fallback.
   Tests cover full-buffer parity, region bounds, lifetime, memory growth, and two independent Chromium workers.
   Benchmarks compare identical Rust scans from 4 KiB through 4 MiB and full control requests.
   Sources: [WebAssembly JavaScript API](https://webassembly.org/getting-started/js-api/),
   [WebAssembly threads proposal](https://github.com/WebAssembly/threads/blob/main/proposals/threads/Overview.md),
   [Rust WASM target guidance](https://doc.rust-lang.org/nightly/rustc/platform-support/wasm32-unknown-unknown.html).
   Contract: `runtime_sab_capnp_contracts.md`. Evidence: `foundation_benchmarks.md`.
2. WASM SIMD: test `simd128` and bulk memory on contiguous, bounded batch kernels.
   Compare generated artifacts with the scalar kernel, including tails, numerical error, and unsupported hosts.
   Rust portable SIMD remains experimental; use target-specific features only behind capability checks.
   Source: [Rust WASM target guidance](https://doc.rust-lang.org/rustc/platform-support/wasm32-unknown-unknown.html).
   Promotion target: `rust_runtime_practices.md` and the runtime benchmark suite.
3. Go SIMD: keep `GOEXPERIMENT=simd` optional and limited to measured `amd64` batch kernels.
   Preserve scalar and Rust/WASM paths for other platforms, including this ARM machine.
   Source: [Go 1.26 release notes](https://go.dev/doc/go1.26).
   Promotion target: the existing `bench-simd` gate after a service-scale win.
4. PostgreSQL 18 I/O: compare `io_method=worker` and supported `io_uring` deployments under staged reads.
   Capture `EXPLAIN (ANALYZE, BUFFERS, WAL)`, `pg_stat_io`, pool waits, WAL bytes, and p95/p99 latency.
   Keep query shape and batch boundaries fixed across runs. Do not infer write gains from read-only AIO results.
   Sources: [PostgreSQL 18 resource settings](https://www.postgresql.org/docs/18/runtime-config-resource.html),
   [PostgreSQL 18 release notes](https://www.postgresql.org/docs/18/release-18.html).
   Promotion target: `database_practices.md` and service-backed load research.

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
| `database_practices.md` | Track PostgreSQL 18/19 async I/O, skip-scan caveats, virtual generated columns, `pg_stat_io`, WAL bytes/op, RLS tests, vector recall, and projection-lag fences. Queue-as-clock boundary rules and recurring-producer backlog bounds promoted 2026-08-25 (lane 8). |
| `redis_practices.md` | Track Redis 8 behavior, client-side cache invalidation, Streams pending recovery, shard policy, script safety, big-key automation, and eviction simulation. |
| `websocket_scaling.md` | Add reconnect storm modeling, browser backpressure, slow-client fairness, auth-expiry mid-socket tests, QUIC/WebTransport research, and topic fanout complexity budgets. |
| `runtime_foundation.md` | Add lane-selection proof tables: direct Go, Rust, WASM/SAB, FFI, shared memory, stdio, native GPU, WebGPU, WebSocket, HTTP, registry dispatch, graceful event emission, JSON fallback, and CUDA Rust PTX (cutile-rs Tile, cuda-oxide SIMT) when adopted. |
| `runtime_native.md` | Add native plugin provenance, OS permission matrices, fd/handle leak checks, sensor/camera/audio latency budgets, and mobile store/privacy review. |
| `rust_runtime_practices.md` | Track Rust 2024 unsafe discipline, Miri eligibility, Loom concurrency tests, `cargo-semver-checks`, panic strategy, Criterion profiles, and FFI fuzzing. |
| `gpu_practices.md` | Add WebGPU compatibility matrix, WGSL layout generator, device-loss chaos tests, CUDA graph invalidation, occupancy-vs-latency guidance, capture bundle schema, and CUDA Rust native kernel lane readiness (cutile-rs Tile track for stable Rust, cuda-oxide SIMT track when it ships on stable). |
| `game_runtime_practices.md` | Add hitch ledger, browser trace bundle, input latency vs frame latency, quality-tier contracts, and streaming priority scheduler rules. |
| `ui_render_performance_research.md` | Added 2026-09-11 (research handoff): DOM/WebView rendering gap. Promote P1 frame telemetry and the evidence harness into `game_runtime_practices.md`/`performance_lab.md`, raster and motion rules into `styling_design_practices.md`, and native WebView signals into `runtime_native.md`; the styling-runtime choice needs an ADR. |
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
   CUDA, CUDA Rust (cuda-oxide book, cuTile Rust docs), Tauri, OpenTelemetry,
   NIST, OWASP, CISA, W3C, Khronos.
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
