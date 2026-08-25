# @ovasabi/ui-minimal Changelog

`ui-minimal` is a local file dependency of every generated frontend, so a
removed export does not fail at install time — it fails later, in each consumer,
as a type error with no explanation attached. Whoever hits it writes a local
re-implementation from whatever the call sites imply, and the copies drift.

**A removal is only complete when it is recorded here with its replacement
named.** A comment in a consumer's file saying the package "used to carry this"
is not a migration path; it is the absence of one.

Entries are newest first.

## Unreleased

### Restored

- `ReactStyleConfig`, `ReactStyleRuntimeLane`, `ReactStyleQuality`,
  `ReactStyleRuntime`, `ReactStyleProvider`, `useReactStyle`,
  `createReactStyle`, and `minimalReactStyle`, in `src/runtimeStyle.tsx`.

  This contract — the render lane and frame budget a surface runs under — was
  removed from the package without a recorded replacement. At least one project
  re-implemented it locally in `frontend/src/styles/theme.ts` and
  `frontend/src/styles/runtimeStyleContext.ts`, and any other consumer that
  needed it would have written a third copy.

  The original export names are kept deliberately, so a project can delete its
  local copy and change only the import path:

  ```ts
  // before — project-owned copy
  import { createAppReactStyle, type ReactStyleConfig } from './styles/theme'
  import { RuntimeStyleProvider, useRuntimeStyle } from './styles/runtimeStyleContext'

  // after
  import { createReactStyle, ReactStyleProvider, useReactStyle, type ReactStyleConfig } from '@ovasabi/ui-minimal'
  ```

  `createReactStyle(lane, overrides?)` replaces a project-local
  `createAppReactStyle(lane)`; pass `{ name, quality, frameBudgetMs,
  hitchBudgetMs }` for values the app owns. A project keeping its own defaults
  should build them once with `createReactStyle` rather than redeclaring the
  types.

## 0.2.0 — 2026-08-24

### Removed

- The eager `@base-ui/react` re-export. Base UI is CommonJS-only, so exporting
  it dragged CJS into every module graph that imported `@ovasabi/ui-minimal`,
  SSR included.

  **Replacement:** none needed at the call site. `Checkbox`, `Switch`,
  `NumberField`, `Tabs`, and `DatePicker` are now native implementations behind
  identical public props and `data-minimal` markers. Consumers remove
  `@base-ui/react` from `package.json`; the managed patch
  `patch_remove_base_ui_dependency` does this on update.

  **Behavior deltas to check in a consumer:** the anchored date panel clamps
  in-viewport instead of collision flipping; the switch track state uses
  `:has()`; `NumberField` accepts `format` and `locale` props but does not apply
  them to display text.
