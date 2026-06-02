# Ovasabi Foundation

Ovasabi Foundation is the shared infrastructure and scaffold baseline for
event-driven, tenant-isolated, realtime applications. It contains the platform
modules, generated-project templates, enforcement tooling, and source
documentation copied into downstream projects.

## Repository Map

| Path | Purpose |
| --- | --- |
| `server-kit/` | Go backend platform primitives: registry, metadata, events, workers, resilience, observability, Hermes, eventlog, Redis, database, and service-backed harnesses. |
| `runtime-transport/` | Foundation transport contracts, protobuf envelopes, command bus, route registry, binary codecs, and Hermes projection schemas. |
| `runtime-sdk/` | WASM/Rust/Go runtime kernel, 4KB control-buffer contract, shared arena, and runtime lane helpers. |
| `runtime-native/` | Native shell bridge for Tauri/device lanes, secure storage, binary native frames, and runtime dispatch. |
| `frontend-kit/` | Frontend operational utilities for metadata, storage, runtime artifacts, and external stores. |
| `ui-minimal/` | Shared structural UI primitives, theme tokens, and motion helpers. |
| `config-contracts/` | Cross-language configuration schemas and generated consumers. |
| `templates/` | Managed scaffold copied into generated Foundation projects. |
| `tooling/` | Source enforcement scripts, manifests, lint configs, and documentation. |
| `docs/` | Architecture, coding, testing, security, runtime, and performance guidance. Start with `docs/README.md`. |

## Agent-Native Workflow

Foundation is intended for one architect coordinating multiple AI coding agents.
Before agents change architecture-sensitive code, read:

1. `docs/README.md`
2. `docs/foundation_architecture_contract.md`
3. `docs/foundation_nervous_system.md`
4. `docs/agent_operating_contract.md`
5. `docs/practice_controls.md`
6. `docs/ai_threat_model.md` when tool, model, retrieved, generated, package,
   or security-sensitive input affects the change
7. the lane-specific practice doc for the change

Agent-authored changes should carry evidence: contract changed, invariant
preserved, tests or benchmarks added, fallback path, scope boundary, regression
guard, and owning docs updated.
The machine-readable control plane lives at `tooling/practice_controls.psv` and
is checked by `make check-practice-controls`.

## Code Quality Lanes

Runtime, distributed-system, and operations-sensitive changes should use these
checks before ordinary test expansion:

1. `make check-runtime-performance-contracts`: verifies low-level performance
   evidence hooks for pprof/trace, CPU counters, Rust Miri/Loom opt-ins,
   WebGPU/WGSL, CUDA/Nsight, and benchmark metadata.
2. `make check-formal-methods`: verifies TLA/PlusCal/Alloy/P guidance and the
   inherited queue, projection, and WebSocket spec templates.
3. `make check-operational-excellence`: verifies DORA, SPACE/DevEx,
   OpenTelemetry linkage, SBOM, and provenance fields.

For measured runtime work, `tooling/scripts/performance_check.sh` writes
`machine.json` and supports `PROFILE=1`, `TRACE=1`, `PROFILE_DIR=...`, and
`PERF_COUNTERS=1` opt-in evidence capture.

## Core Commands

```bash
make generate-contracts
make lint
make test
make check-rust
make verify
```

Service-backed checks require local infrastructure and use explicit targets:

```bash
make docker-up
make test-service-backed
```

## Project Bootstrap

From the parent workspace:

```bash
./foundation/scripts/init-project.sh my-app full
./foundation/scripts/update-project.sh /path/to/project
```

Generated projects consume Foundation through package and module boundaries.
They should not import raw files from `foundation/*/ts/src` or copy internal Go
packages into app code.

Generated projects also receive Rust issue checks in `scripts/checks/`.
`make check-rust` discovers app Rust, native Tauri Rust, and vendored
`foundation/runtime-*` manifests, then runs fmt, Clippy safety lints,
runtime-practice checks, and tests where Rust lanes are enabled.
