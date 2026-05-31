# Foundation Architecture Contract

Status: v1.1
Owner: Platform Architecture

Foundation is split into three explicit layers.

## 1. Platform Modules

These are shared modules that should be versioned, tested, and rarely edited inside applications:

- `server-kit`: Go server primitives, registry/HTTP dispatch, metadata, events, workers, resilience, logging, metrics, SLOs, profiling, service-backed test harnesses, object/bulk helpers, event logs, and Hermes projection runtime.
- `runtime-transport`: envelope contracts, Foundation protobuf transport, WebSocket/event conventions, Hermes projection contracts, and generated transport helpers.
- `runtime-sdk`: browser/WASM, Go runtime-host, Rust runtime, 4KB control-buffer contract, shared arena, and lane-planning helpers.
- `runtime-native`: Tauri-backed native shell bridge, binary native dispatch frames, secure storage surfaces, and native runtime capability discovery.
- `frontend-kit`: operational frontend storage, metadata, runtime artifact discovery, and external-store helpers.
- `ui-minimal`: shared UI primitives, semantic theme tokens, and motion helpers.
- `config-contracts`: configuration schema contracts and examples shared by generated apps.

If logic is needed by multiple apps, move it into a platform module instead of adding root `pkg/` code to an app.

## 2. Managed Scaffold

Managed scaffold is generated and synchronized from `templates/scaffold.manifest.tsv`:

- `Makefile`, CI workflows, Docker files, config baselines, lint/check scripts, and documentation.
- `cmd/server`, `cmd/worker`, `cmd/docgen`, `internal/bootstrap`, `internal/startup`, and `internal/worker` baseline wiring.
- frontend build/test config and base WASM entrypoint.
- runtime communication, cross-origin isolation headers, and default post-quantum security posture.
- production-safe ingress defaults: exact CORS origins, auth-on production posture, protected operational endpoints, and CI delivery-metrics capture.
- frontend communication package boundaries: `@ovasabi/runtime-transport`, `@ovasabi/frontend-kit`, and `@ovasabi/ui-minimal` are consumed as local packages with symlink-preserving Vite, Vitest, and TypeScript config.
- backend runtime bindings: generated projects wire `server-kit` registry, HTTP API bridge, metadata normalization, graceful responses, security, compression, observability, WebSocket routing/metrics, bounded worker queues, Hermes runtime-store wrapping, eventlog hooks, and resilience dependency registration through startup/server code.

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
- app domain protobuf schemas under `api/protos` and generated TypeScript contracts under `frontend/src/types/protos`.

Generated projects must keep `.foundation` current so tooling can enforce the intended profile and feature flags.

Raw Vite/Vitest aliases into `foundation/*/ts/src` are not a supported extension point. If an app needs shared communication behavior, promote it into the appropriate foundation package and consume the package boundary.

Generated projects should not treat `server-kit` as optional sample code. The scaffolded runtime must actively use the package surfaces it ships with: startup registers database, Redis, object storage, Hermes, and other critical dependencies with `resilience`; server ingress goes through registry/httpapi/metadata/graceful/security/compress/observability; WebSocket ingress uses routing and metrics; worker queues use bounded server-kit defaults. `tooling/scripts/server_kit_usage_check.sh` is the Foundation source for the generated `scripts/checks/server_kit_usage_check.sh` gate that enforces this deeper wiring for `.foundation` projects while limiting custom or mid-migration apps to package-presence checks until they adopt the managed scaffold profile.

Generated projects should also keep operational readiness scaffolded but app-owned. Foundation provides `docs/operations` templates, `make delivery-metrics`, and CI artifact capture; app deployment platforms own dashboard aggregation, incident process, and production alert policies.

## Nervous System Contract

The official runtime substrate contract is `docs/foundation_nervous_system.md`. Generated projects must preserve the canonical lifecycle:

```text
client command -> RuntimeEnvelope -> auth/tenant/correlation/idempotency validation
-> domain command -> requested event -> worker/cache/Redis/realtime coordination
-> success/failed event -> frontend projection/store update
```

Any optimized lane, template helper, code generator, or app-owned service must refine that lifecycle rather than inventing a parallel path. `tooling/scripts/generate_lifecycle_contract_tests.mjs` derives baseline lifecycle vectors from mutating proto request/response pairs and emits tests that call `server-kit/go/contracttest.VerifyCommandLifecycle`. App integration tests should reuse the same verifier with real observed envelopes/jobs.

Hermes is the current Foundation hotplane refinement of that lifecycle. It is
documented in `docs/hermes_hotplane.md` and implemented in `server-kit/go/hermes`;
it may accelerate scoped live reads, but it must not become source-of-truth
state or bypass tenant, correlation, idempotency, event, and freshness rules.
