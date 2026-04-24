# Foundation Architecture Contract

Status: v1  
Owner: Platform Architecture

Foundation is split into three explicit layers.

## 1. Platform Modules

These are shared modules that should be versioned, tested, and rarely edited inside applications:

- `server-kit`: Go server primitives, health, config loading, logging, middleware, and shared runtime adapters.
- `runtime-transport`: envelope contracts, protobuf transport, websocket/event conventions, and generated transport helpers.
- `runtime-sdk`: browser/WASM runtime bridge, 4KB control-buffer contract, and optional shared-arena helpers.
- `config-contracts`: configuration schema contracts and examples shared by generated apps.

If logic is needed by multiple apps, move it into a platform module instead of adding root `pkg/` code to an app.

## 2. Managed Scaffold

Managed scaffold is generated and synchronized from `templates/scaffold.manifest.tsv`:

- `Makefile`, CI workflows, Docker files, config baselines, lint/check scripts, and documentation.
- `cmd/server`, `cmd/worker`, `cmd/docgen`, `internal/bootstrap`, `internal/startup`, and `internal/worker` baseline wiring.
- frontend build/test config and base WASM entrypoint.
- runtime communication, cross-origin isolation headers, and default post-quantum security posture.

Each manifest row declares profile, feature gate, and ownership mode:

- `overwrite`: foundation-owned file, always synchronized.
- `force`: foundation-managed baseline, overwritten only when `--force` is used or the file does not exist.
- `create`: created once, then project-owned.

## 3. Project-Owned Architecture

Applications own behavior:

- domain services and repositories under `internal/service/<domain>`.
- handlers, route registration, and app-specific startup wiring.
- business workers registered through `internal/worker`.
- product-specific UI, state, and integration behavior.
- app-specific runtime units and artifact-signing decisions inside the foundation security contract.

Generated projects must keep `.foundation` current so tooling can enforce the intended profile and feature flags.
