# Foundation Project Standardization & Drift Control

Status: design
Date: 2026-06-21
Owner: Platform Architecture

## Why this exists

Nine scaffolded projects drifted from the current foundation structure. The
projection read path (`projectiongw`) could be synced as a vendored module to all
nine, but **mounting** it required editing each project's `cmd/server/main.go`,
`internal/server/server.go`, and `internal/startup/dependencies.go` — and those
files had diverged so far that only 3 of 9 could take the change mechanically.

This document explains why the drift happened, what is and is not legitimate, and
the architecture change that prevents this class of drift going forward.

## What actually drifted (measured)

Differing lines vs the current template (module path normalized):

| Project | main.go | server.go | dependencies.go | generation |
| --- | ---: | ---: | ---: | --- |
| marketer | 25 | 14 | 43 | recent |
| trader | 25 | 17 | 120 | recent |
| global | 24 | 17 | 313 | recent |
| chowdash | 35 | 16 | 69 | recent |
| civic | 32 | 463 | 450 | recent |
| trotters | 108 | 630 | 431 | recent |
| docuos / forest / reframe | — | — | missing (`init.go`, `startup.Initialize`) | old |

Two distinct causes:

1. **Generational drift** (docuos/forest/reframe): generated from an older
   template — `internal/startup/init.go` with `startup.Initialize`, no
   `dependencies.go`. Their `startup_test.go` is `force`-managed and already
   tests `InitDependencies`, so their contract test is already red. The
   drift-detector is working; the projects were never migrated.

2. **Composition-root drift** (civic, trotters, global, …): these files are the
   project's **dependency-injection composition root**. civic's
   `dependencies.go` imports 12 domain services (audit, evidence, factcheck,
   geo, identity, …) and wires them with custom fields (`RiverClient`, service
   aliases). This is correct, essential project code — not boilerplate.

## The real problem (not a misclassification)

`cmd/server/main.go`, `internal/server/server.go`, and
`internal/startup/dependencies.go` are manifest class `create` (scaffolded once,
project-owned). That is **correct**: projects legitimately own their service
wiring and routes. Their `_test.go` counterparts are `force` (foundation owns the
contract; a refreshed test reddens on drift).

The defect is that **foundation runtime wiring and project domain wiring live in
the same files**:

- Foundation-owned, should always be current: DB+Hermes+Redis+bus assembly,
  health/liveness/readiness, dispatch route, WebSocket route, **projection
  gateway mount**, middleware stack, graceful shutdown.
- Project-owned, legitimately customized: domain service construction, domain
  routes, project config, extra dependencies.

Because they are interleaved, foundation cannot ship a wiring change (like the
projection mount) without editing project-owned files — which is exactly why this
was painful and only partially possible.

## The fix: extract a foundation runtime assembly; make project files thin

Move the foundation-owned runtime wiring **out** of the project-owned files and
**into** a module-synced server-kit package (proposed: `server-kit/go/appkit`).
The project files become thin composition roots that (a) register domain
extensions and (b) call the foundation assembly.

```
                 ┌────────────────────────────────────────────┐
project-owned    │ main.go (thin): load cfg, register domain   │
(create)         │   services + routes via hooks, run runtime  │
                 │ bootstrap/services.go: domain handlers      │
                 │ config.go: project config                   │
                 └───────────────────┬────────────────────────┘
                                     │ hooks (narrow, stable)
                 ┌───────────────────▼────────────────────────┐
foundation-owned │ appkit.Runtime (server-kit, module-synced): │
(force / module) │   DB+Hermes+Redis+bus, health, dispatch,    │
                 │   websocket, projection gateway mount,      │
                 │   middleware, graceful shutdown             │
                 └─────────────────────────────────────────────┘
```

### Target project `main.go` (illustrative)

```go
func run(ctx context.Context) error {
    cfg, err := config.Load()
    if err != nil { return err }
    rt, err := appkit.New(ctx, cfg, appkit.Options{
        RegisterServices: bootstrap.Register,   // domain handlers + deps
        RegisterRoutes:   routes.Register,       // domain-specific routes (optional)
    })
    if err != nil { return err }
    defer rt.Close()
    return rt.Run(ctx)
}
```

Everything foundation owns — including new features like the projection gateway —
now lives in `appkit` (a vendored module, refreshed by `update`). Adding a future
standard endpoint is a server-kit change that reaches every project on the next
sync, with **zero project-file edits**. That is the drift prevention.

### What stays project-owned

- `bootstrap/services.go` / `internal/service/*`: domain services, registered
  through `appkit.Options.RegisterServices`.
- `config.go`: project configuration (extends `appkit` config).
- domain routes via `RegisterRoutes`.

### Extension points (so projects never edit foundation wiring)

`appkit.Options` exposes the seams projects actually use today: service
registration, extra routes, extra dependencies/cleanup, middleware injection,
auth configuration. The projection gateway needs **no** project seam — `appkit`
mounts it from the projected store automatically.

## Drift control going forward

1. **Narrow the project surface.** The less foundation wiring lives in
   project-owned files, the less there is to drift. After extraction, the
   project's foundation footprint is one `appkit.New(...)` call.
2. **Keep `force`-managed contract tests.** They already redden on structural
   drift (docuos proves it). Promote a single `make check-project-structure`
   that asserts the thin shape (presence of `appkit.New`, absence of inlined
   foundation wiring) so drift is a CI signal, not a discovery during a feature.
3. **Version the runtime contract.** `appkit` carries a runtime contract version;
   `update` warns when a project's shell predates the current `appkit` API.
4. **Migration is a one-time move, not per-feature.** Once a project is on
   `appkit`, it inherits foundation wiring changes by module sync forever.

## Migration plan

**Phase 1 — build `appkit` in server-kit.** Encapsulate the current
`InitDependencies` + server assembly + projection mount behind `appkit.New`,
with the options seams. Ship the thin `main.go`/`dependencies.go`/`server.go`
template that uses it. New projects are born standardized.

**Phase 2 — migrate recent-gen projects** (chowdash, civic, global, marketer,
trader, trotters). Mechanical for light-drift projects; for heavy composition
roots (civic, trotters) the domain-service wiring moves into the
`RegisterServices` hook (a contained, reviewable move per project — domain code
preserved verbatim, only the assembly boilerplate is removed).

**Phase 3 — regenerate old-gen backends** (docuos, forest, reframe). Replace the
`init.go`/`Initialize` startup with the `appkit` shell, carrying their domain
services into `RegisterServices`. Their `force`-managed contract tests turn green.

## Non-goals

- No change to the event-sourced write path, domain logic, or the read-path
  contract (`projectiongw`, hermes projections) — this is purely about where the
  runtime wiring *lives*.
- `bootstrap/services.go` and `config.go` remain project-owned (`create`).
