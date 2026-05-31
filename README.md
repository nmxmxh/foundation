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

## Core Commands

```bash
make generate-contracts
make lint
make test
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
