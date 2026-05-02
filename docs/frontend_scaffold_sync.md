# Frontend Scaffold Sync

The foundation communication contract is native to foundation and consumed by apps as packages, not by raw source aliases.

## Package Boundaries

- `@ovasabi/runtime-transport` owns HTTP, WebSocket, binary envelope handling, compression, offline queueing, route registries, and transport metadata stores.
- `@ovasabi/frontend-kit` owns browser storage, runtime/WASM manifest access, store handle registries, metadata normalization, and app-facing runtime helpers.
- `@ovasabi/ui-minimal` owns shared UI primitives and theme surfaces.
- Apps may wrap these packages, but they must not import or alias files from `foundation/*/ts/src`.

## UI Primitive Recognition

- Start new frontend surfaces by checking `@ovasabi/ui-minimal` before creating app-local `Button`, `Input`, `Card`, `Table`, `Calendar`, `Modal`, `Dropdown`, `Skeleton`, loader, shell, or navigation primitives.
- Use `MinimalThemeProvider`, `MinimalGlobalStyles`, and the exported CSS variables as the first styling layer. Apps may provide semantic token overrides, but they should not redefine the primitive contract.
- App `components/ui` modules should be thin wrappers around `Minimal*` primitives when product naming, icons, or copy differ.
- Page shells should compose `MinimalAppShell`, `MinimalScrollMain`, `MinimalSkipLink`, `MinimalSidebar`, and `MinimalScrollFeedbackSurface` before inventing a custom layout frame.
- Use `useMinimalMotion`, `useMinimalScrollFeedback`, and the motion helpers for layout animations, contextual entrances, and scroll feedback. Keep route/auth/business logic app-owned.
- Motion must follow `docs/foundation/styling_design_practices.md` and `docs/foundation/references`: transform/opacity first, reduced-motion aware, hover gated to hover-capable devices, exits faster than entrances, and no layout-property animation for repeated controls.
- Loading states should prefer `MinimalSkeleton`, `MinimalEmptyState`, and explicit keyed loading helpers from app/store code rather than page-local spinner sprawl.

## Generated Types

- App domain schemas live under `api/protos/<domain>/vN`.
- `make proto` generates Go service bindings from app protos.
- `make proto-ts` generates TypeScript domain contracts into `frontend/src/types/protos`.
- Generated domain contracts are app-owned. Foundation packages expose transport/runtime APIs and accept generated message types at the app boundary.

## Communication Layers

- Browser UI uses `@ovasabi/runtime-transport` for request/response and push channels.
- App stores/hooks adapt generated `frontend/src/types/protos` contracts to runtime transport routes.
- Backend handlers validate the same protobuf schema family before dispatching domain logic.
- Generated backends use `server-kit` for the server-side communication spine: registry/httpapi route dispatch, metadata normalization, graceful envelopes, security/compression/observability middleware, WebSocket routing/metrics, bounded workers, and resilience dependency registration.
- WASM/native execution uses `foundation/runtime-sdk` bindings and manifest discovery through `@ovasabi/frontend-kit`; legacy WASM globals are compatibility shims only.
- JSON remains a boundary adapter. New hot paths should use generated protobuf, binary envelopes, or runtime-sdk buffers.

## Build Rules

- `frontend/package.json` must depend on `@ovasabi/runtime-transport`, `@ovasabi/frontend-kit`, and `@ovasabi/ui-minimal` through local `file:../foundation/.../ts` packages.
- TypeScript, Vite, and Vitest must set `preserveSymlinks: true` so peer dependencies resolve through the app frontend graph.
- Vite and Vitest aliases may point to app paths such as `@` or `@generated`; they must not point to `foundation/ui-minimal/ts/src` or `foundation/runtime-transport/ts/src`.
- Frontend Docker contexts must include the package directories required by local file dependencies. Do not ignore `foundation/runtime-transport`, `foundation/frontend-kit`, or `foundation/ui-minimal` for frontend builds.

## Drift Notes

- `docuos_v1` and `forest_v1` were close to the scaffold but lacked runtime transport and symlink preservation.
- `reframe_v1` used raw source aliases for foundation packages; this is the exact drift class scaffold checks now block.
- `trotters_v1` is on a different frontend base. It should either adopt the package contract explicitly or remain documented as intentionally custom.
- `fintech_v1` layout work contributed reusable shell lessons: skip links, scroll-container identity, sidebar wheel proxying, contextual navigation motion, and subtle scroll feedback. Those belong in `ui-minimal`; app-specific auth, role gating, and route lists stay in the app.
