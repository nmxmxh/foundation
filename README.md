# Ovasabi Foundation (Work in Progress - Version 0.0.1)

> **Note**: Ovasabi Foundation is currently under active development. The entire repository and its template scaffolds represent a Work in Progress (WIP) baseline.

Ovasabi Foundation is a co-designed, event-driven, tenant-isolated application substrate. It provides the core platform modules, generated templates, enforcement checks, and source documentation used to bootstrap and maintain high-performance downstream projects.

---

## Bridging the Software Deficit: The 1ns Metric

In modern computing, hardware performance is bounded by physics:

* **1 Nanosecond (1ns)** is one-billionth of a second ($10^{-9}$ seconds).
* In **1ns**, light travels approximately 30 centimeters (11.8 inches)—the width of a standard motherboard.
* A modern 3.0 GHz CPU core completes a single clock cycle in **0.33ns**. A simple CPU instruction takes less than **1ns**.
* Accessing L1 cache takes **~0.5–1ns**; L2 cache takes **~3–4ns**; main memory (DRAM) takes **~50–100ns**.

### The Software Deficit

While hardware executes billions of operations per core each second, typical software stacks suffer from a massive **software deficit**. Due to bloated framework layers, excessive heap allocations, and heavy serialization formats, a simple JSON payload parse or router dispatch often spends **50,000ns to 1,000,000ns (50µs to 1ms)**. This wastefully consumes millions of potential CPU cycles.

Ovasabi Foundation is engineered to bridge this software deficit. It provides a co-designed runtime ladder, zero-allocation hotpaths, and hardware-aligned memory interfaces to keep operations in the nanosecond/microsecond domain.

---

## Core Scaffolding & Fleet Management

### Ovasabi CLI

Foundation now includes a baseline CLI under `cmd/ovasabi` that wraps the
manifest-driven scaffold scripts and adds the distribution/licensing boundary
for package-registry installs.

Local development usage:

```bash
# From this repository
cd cmd/ovasabi
go run . init --profile=performance --name=trader_os --skip-license
go run . update --project-dir=../../trader_os_v1 --skip-license
go run . license verify --offline-license --license-file=ovasabi.lic --license-public-key="$(cat ovasabi.pub)"
```

NPM package skeleton usage:

```bash
# From the repository root
node cmd/ovasabi/bin/ovasabi.js init --profile=performance --name=trader_os --foundation-dir . --skip-license
```

The intended public install command after publishing is:

```bash
npx -y @ovasabi/cli init --profile=performance --name=trader_os
```

Current status:

* implemented: `init`, `update`, `license verify`, npm `bin` shim, online
  license verification, offline Ed25519 JWT license verification;
* pending distribution work: publish `@ovasabi/cli`, add prebuilt binaries,
  wire signed remote template downloads, and automate registry-token setup.

### What is a Project Scaffold?

A project scaffold is a blueprint template (defined in `templates/` and mapped by `templates/scaffold.manifest.tsv`) used to initialize new projects. Instead of starting from scratch, a bootstrapped project immediately receives a fully configured, production-grade folder structure, container configs, database migrations, and CI workflows.

### Managing Multiple Projects (Fleet Synchronization)

In an enterprise environment, application drift and dependency decay occur rapidly across separate codebases. The Foundation core acts as the single source of truth for the platform:

* **Generation**: Running `scripts/init-project.sh <path> <profile>` creates a new conforming repository.
* **Updates**: Running `scripts/update-project.sh <path>` synchronizes an existing project. It purges retired foundation files (e.g. wiping `docs/foundation/` before copying the fresh docs) to enforce clean synchronization.
* **Fleet Synchronization**: The script [scripts/update-all.sh](scripts/update-all.sh) reads `scaffolded-projects.tsv` in the parent directory and updates the entire fleet of projects in one invocation, applying patches and updating toolchains.

---

## Architectural Agnosticism & Zero-Copy Communication

### Decoupled Runtime Agnosticism

Ovasabi Foundation is agnostic of specific CPU architectures, memory allocation models, and processing layers. Its performance rules and patterns run identically on:

* **Go Backends**: Running multi-tenant database pools and concurrent worker loops.
* **Rust Computes**: Dispatched via FFI or WebAssembly guest environments.
* **Browser Engines**: Running asynchronous JS event loops and background worker normalizers.
* **Native Operating Systems**: Interfacing directly with hardware APIs.

### Zero-Copy Communication

To avoid CPU-bound serialization bottlenecks, the Foundation uses zero-copy and shared-memory communication:

* **Cap'n Proto Buffers**: Define physical byte layouts and offsets inside the 4KB `runtime-sdk` buffer. JavaScript hosts and WASM/Rust guests read and write to the same physical memory space without serialization boundaries.
* **SharedArrayBuffer (SAB)**: Shares raw buffers directly between browser threads and worker normalizers.
* **Direct Frame Clients**: Go services communicate viaDirect Frame binary envelopes (`grcsvc.DirectFrame`), bypassing local socket/network round-trips.

### Runtime Native (Work in Progress)

The native desktop wrapper (`runtime-native`) uses a Rust-based Tauri shell to provide local secure storage, hardware-accelerated WebGPU/Nsight compute lanes, and native window dispatch.

---

## Day-One Capabilities

Every project generated from the Foundation scaffold receives the following capabilities out-of-the-box from day one:

1. **Multi-Tenant Isolation**: Automatic tenant derivation from authenticated context via `auth.OrgIDFromContext(ctx)`, preventing cross-tenant data leaks at the database query level.
2. **Event-Driven Nervous System**: A standardized lifecycle (`requested` -> `success` / `failed`) with correlation metadata (`CorrelationID`) propagated through all logs and workers.
3. **Hermes Hotplane Projections**: Node-local, memory-bounded, indexed read models that automatically project database mutations, performing dashboard reads in sub-microseconds with stale-while-revalidate fallbacks.
4. **Resumable Progress Transfers**: A progress-bearing, chunk-based file upload/download lifecycle (`server-kit/go/transfer`) with progression events.
5. **Bounded Background Processing**: Bounded queues, exponential backoff retries, and worker chain orchestration powered by River.
6. **Unified Observability & Resilience**: Built-in OpenTelemetry tracing, circuit breakers, and a structured, categorized error taxonomy.

---

## Universal Performance Primitives

The Foundation's performance primitives are not limited to simple database APIs. The exact same guidelines—such as avoiding heap allocations in hotpaths, pre-sorting indexes, utilizing Structure-of-Arrays (SoA) layout prefetching, and performing bounded SIMD loops—apply universally to:

* **Financial Arithmetic**: Bounded, checked integer minor-unit money calculations (`server-kit/go/money`).
* **Game Runtimes**: Frame-budgeted animation loops, event queue fanouts, and state-machine transitions.
* **GPU Computing**: WebGPU/WGSL compute pass setups and CPU-to-GPU memory buffer transfers.
* **Data Processing**: Vectorized sequential aggregations (columnar scans) that outpace standard pointer-chase loops.

---

## Repository Map

| Path | Purpose |
| --- | --- |
| `server-kit/` | Go backend platform primitives: registry, metadata, events, workers, resilience, observability, Hermes, eventlog, Redis, database, transfer, projection gateway, object storage, bulk operations, intelligence signals, and service-backed harnesses. |
| `runtime-transport/` | Foundation transport contracts, protobuf envelopes, command bus, route registry, binary codecs, and Hermes projection schemas. |
| `runtime-sdk/` | WASM/Rust/Go runtime kernel, 4KB control-buffer contract, shared arena, and runtime lane helpers. |
| `runtime-native/` | Native shell bridge for Tauri/device lanes, secure storage, binary native frames, and runtime dispatch. |
| `frontend-kit/` | Frontend operational utilities for metadata, storage, runtime artifacts, transfer progress, and external stores. |
| `ui-minimal/` | Shared structural UI primitives, theme tokens, and motion helpers. |
| `config-contracts/` | Cross-language configuration schemas and generated consumers. |
| `templates/` | Managed scaffold copied into generated Foundation projects. |
| `tooling/` | Source enforcement scripts, manifests, lint configs, and documentation. |
| `docs/` | Architecture, coding, testing, security, runtime, and performance guidance. Start with `docs/README.md`. |

---

## Agent-Native Workflow

Foundation is intended for one architect coordinating multiple AI coding agents. Before agents change architecture-sensitive code, read:

1. `docs/foundation_glossary.md` — concept lookup and agent Q&A
2. `docs/foundation_quick_start.md` — minimum viable understanding path
3. `docs/README.md` — full documentation map
4. `docs/foundation_architecture_contract.md` — ownership boundaries
5. `docs/foundation_nervous_system.md` — canonical lifecycle
6. `docs/agent_operating_contract.md` — agent workflow and evidence
7. `docs/practice_controls.md` — rule-to-check mapping
8. `docs/ai_threat_model.md` when tool, model, retrieved, generated, package, or security-sensitive input affects the change
9. The lane-specific practice doc for the change

Agent-authored changes should carry evidence: contract changed, invariant preserved, tests or benchmarks added, fallback path, scope boundary, regression guard, and owning docs updated.
The machine-readable control plane lives at `tooling/practice_controls.psv` and is checked by `make check-practice-controls`.

---

## Code Quality Lanes

Runtime, distributed-system, and operations-sensitive changes should use these checks before ordinary test expansion:

1. `make check-runtime-performance-contracts`: verifies low-level performance evidence hooks for pprof/trace, CPU counters, Rust Miri/Loom opt-ins, WebGPU/WGSL, CUDA/Nsight, and benchmark metadata.
2. `make check-formal-methods`: verifies TLA/PlusCal/Alloy/P guidance and the inherited queue, projection, and WebSocket spec templates.
3. `make check-operational-excellence`: verifies DORA, SPACE/DevEx, OpenTelemetry linkage, SBOM, and provenance fields.

For measured runtime work, `tooling/scripts/performance_check.sh` writes `machine.json` and supports `PROFILE=1`, `TRACE=1`, `PROFILE_DIR=...`, and `PERF_COUNTERS=1` opt-in evidence capture.

---

## Core Commands

```bash
make generate-contracts      # Full code gen (Protos -> Go/TS)
make lint                    # All linters
make test                    # All tests (Go, TS, Rust)
make check-rust              # Rust fmt, clippy, tests
make verify                  # Full CI verification suite
make check-practice-controls # Practice matrix integrity
make check-doc-references    # Markdown link validation
make lifecycle-manifest      # Regenerate lifecycle contract
make lifecycle-contracts     # Regenerate lifecycle tests
```

Service-backed checks require local infrastructure and use explicit targets:

```bash
make docker-up
make test-service-backed
```

---

## Project Bootstrap

From the parent directory of `foundation`:

```bash
# Initialize with the CLI wrapper
node foundation/cmd/ovasabi/bin/ovasabi.js init --profile=performance --name=my-app --foundation-dir foundation --skip-license

# Compatibility fallback: initialize with shell scripts
./foundation/scripts/init-project.sh my-app full

# Update an existing project to sync with Foundation changes
node foundation/cmd/ovasabi/bin/ovasabi.js update --project-dir=/path/to/project --foundation-dir foundation --skip-license

# Compatibility fallback: update with shell scripts
./foundation/scripts/update-project.sh /path/to/project
```

Generated projects consume Foundation through package and module boundaries. They should not import raw source files from `foundation/*/ts/src` or copy internal Go packages into app code.

Generated projects also receive Rust issue checks in `scripts/checks/`. `make check-rust` discovers app Rust, native Tauri Rust, and vendored `foundation/runtime-*` manifests, then runs fmt, Clippy safety lints, runtime-practice checks, and tests where Rust lanes are enabled.
