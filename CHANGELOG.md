# Changelog

All notable changes to the Ovasabi Foundation will be documented in this file.

## [0.0.1] - 2026-06-28

Foundation reset to version 0.0.1. This marks the first clean documentation
baseline with structural integrity, complete cross-references, and the agent
glossary.

### Documentation

- Added `docs/foundation_glossary.md`: agent Q&A reference, concept glossary,
  module cards, invariant reference, and practice summaries.
- Added `docs/info/` directory for informational documents that are not
  directly Foundation practices or contracts.
- Moved `coding_magic.md`, `columnar_projection_lane.md`, and
  `scaffolded_projects_executive_summary.md` to `docs/info/`.
- Removed `handover_note_codex.md`, `inos_runtime_reuse_plan.md`, and
  `frontend_prototype_runtime_todo.md`.
- Refreshed `docs/README.md` documentation map with all missing entries.
- Updated all version references to 0.0.1.
- Fixed stale dates across all documentation files.
- Added missing server-kit modules to `AGENTS.md` module table.

### Previous Development History

The following entries record the development history prior to the 0.0.1 reset.

#### Pre-reset: 1.2.0-dev

- Direct reflection-based serialization/deserialization for custom structs in
  `extension.Value` and `extension.FromJSON`.
- Queue capacity and queue current length tracking in `registry.MetricsSnapshot`.
- Transfer lane: progress-bearing upload/download lifecycle with bookend events,
  monotonic progress, and resumable multipart surface.
- Frontend command registry: generated route catalog, `createAppRuntime`,
  and typed dispatch with custom route support.
- Projection gateway: HTTP read surface for Hermes-backed projections.
- Frontend runtime workbench completion: dummy data, tenant stores, live
  projections, runtime adapters, and prototype generator.
- Fixed timer leak in worker retry backoff loops.

#### Pre-reset: 1.1.0

- **policy**: Policy-as-code authorization engine (Cedar-inspired).
- **redis**: Native Redis client integration for server-kit.
- **worker**: River-based background job handling infrastructure.
- **docgen**: Automated documentation generation for generated projects.
- Updated tech stack standards to Go 1.26, React 19.2, TypeScript 5.9+,
  Rust 1.95, PostgreSQL 18, Redis 8.

#### Pre-reset: 1.0.0

- Initial release with server-kit modules: `circuitbreaker`, `featureflags`,
  `tracing`, `policy`, `retry`, `healthcheck`, `errors`, `cache`,
  `degradation`, `versioning`.
- Project bootstrapper (`init.sh`) with profile support.
- Update mechanism (`update-project.sh`).
- Foundation documentation set.
