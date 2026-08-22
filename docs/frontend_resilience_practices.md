# Frontend Resilience Practices

Status: baseline
Date: 2026-08-22

## Purpose

This document records navigation, state, and persistence failure classes that
produced production bugs in earlier Foundation-derived apps. Each rule names
the failure mode, the Foundation primitive that prevents it, and the
enforcement point.

## Routing rules

### FR-1 No transition-wrapped route updates

React Router `startTransition` wrapping starves route swaps when heavy pages
and continuous store updates compete: the URL changes while the previous view
stays mounted forever.

Rules:

1. Do not enable `v7_startTransition` or wrap `<Routes>` updates in
   transitions.
2. Route swap specs must assert visible content change, not only URL change.
3. Keep full-remount route keys when pages hold non-cancellable work.

### FR-2 Route chunks load through createLazyPage

A stalled chunk fetch otherwise leaves an eternal loader: React caches a
pending `lazy()` promise forever, so no retry path exists.

Rules:

1. Load route chunks only through `createLazyPage(...)` from
   `@ovasabi/frontend-kit`. It applies a timeout gate (`chunkGate`, default
   20s), renders a retry boundary, and rebuilds the lazy component per retry.
2. Never introduce bare `lazy(() => import(...))` routes.
3. Adding a guarded page means adding its loader to the authenticated prewarm
   list in project space; cold chunks are the first slow path users hit after
   context switches.

Enforcement: template ESLint rule flags bare `lazy()` calls
(`templates/frontend/eslint.config.js`). Regression specs must assert view
content swap against a real build in a real browser; mocked-page unit tests
skip the failing layer.

## State rules

### FR-3 Selectors return stable references

An object- or array-building Zustand selector creates a fresh value on every
snapshot check. `useSyncExternalStore` treats that as a perpetual store
change: hot render loop, zero DOM commits, frozen app.

Rules:

1. One field per selector call.
2. Use `useShallow` only with proven need, documented at the call site.
3. Derive collections with `useMemo` outside store subscriptions.

Diagnostic signature: hot CPU in render functions plus zero DOM mutations over
seconds plus unresponsive navigation.

Enforcement: template ESLint rules flag object, array, and inline-derived
selectors inside `use*Store(...)` calls.

### FR-4 Business state lives in stores

Do not observe business state through DOM mutation observers or ad-hoc DOM
reads. Use Zustand stores fed by transport events.

## Persistence rules

### FR-5 Client persistence uses the shared IndexedDB adapter

Browsers close IndexedDB connections without asking: eviction, backgrounding,
or another tab winning a version upgrade.

Contract enforced by `frontend-kit/createIndexedDBStorage`:

1. `onclose` and `onversionchange` drop stale handles immediately.
2. Recoverable connection failures retry once over a fresh connection.
3. A failed open never poisons future attempts.
4. Every promise resolves; persist bursts cannot emit unhandled rejections.
5. Reads fail soft to null, meaning "no persisted state".

New client-persisted stores use this adapter. Do not open private IndexedDB
handles.

## Session model rules

### FR-6 Credentials never persist

Token-bearing stores persist nothing. Do not fix refresh-logout by persisting
credentials; passkey and token hardening depends on storage staying empty.

### FR-7 Workspace memory survives sign-out

Context stores may persist active workspace identifiers. Login restores the
last workspace through a properly scoped token mint. Landing on login after a
refresh on a guarded route is expected behavior.

## Debugging playbook

Order of operations for freeze-class incidents:

1. Reproduce against the real build in a real browser. Mocked tests pass while
   production freezes because mocks skip the failing layer.
2. Classify by signature:

   | Symptom | Likely cause |
   | --- | --- |
   | URL changes, view never swaps | Pending transition or suspension (FR-1, FR-2) |
   | Hot CPU, zero DOM mutations over seconds | Selector churn loop (FR-3) |
   | Chunks loaded, console silent, still stuck | Render layer, not network |
   | Stale data after create or update | Cache invalidation gap |
   | Unhandled `InvalidStateError` on IDB | Stale connection handle (FR-5) |

3. Instrument decisively: sampling profiler for hot stacks, mutation observer
   commit counters, resource timing entries for prewarm checks.
4. Remember project templates silence console info and warn levels; keep
   diagnostics on `console.error`.
5. Lock the lesson in: name regression tests after the failure mode.
