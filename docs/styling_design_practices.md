# Ovasabi Styling and Design Practices

Status: v1.0  
Date: 2026-04-22  
Owner: Platform Architecture

This document defines the preferred frontend styling, theming, loading-surface, and motion posture for Ovasabi applications.

It is grounded in local code, not generic design advice:

1. `foundation/ui-minimal/ts/src/theme.tsx` is the canonical token-to-CSS-variable pipeline.
2. `foundation/ui-minimal/ts/src/primitives.tsx` is the shared structural primitive surface.
3. `fintech_v1/frontend/src/utils/contextTheme.ts` shows the correct split between contextual color intent and component behavior.
4. `fintech_v1/frontend/src/utils/loadingState.ts` shows the preferred keyed loading-state model.
5. `fintech_v1/frontend/src/router/AppRouter.tsx` shows the right route-level loading boundary pattern with `Suspense`.

## 1. Local Lessons To Keep

Keep these patterns:

1. Theme objects should map to semantic tokens first, then to CSS variables, then to shared primitives.
2. Context theme selection should stay separate from component markup and business logic.
3. Loading state should be keyed and reference-counted where multiple concurrent actions can overlap.
4. Route and page loading boundaries should use dedicated loaders or entry components, not copy-pasted spinners inside every page.
5. Shared primitives should own structure, accessibility, and token consumption; apps should own brand voice, shells, and composition.

Do not repeat these older patterns in new work:

1. Large inline style objects inside shared components.
2. One-file sprawl of many unrelated standalone styled-component constants when a grouped `Style` object would be clearer.
3. Mixing data fetching, loading placeholders, theme mapping, and visual styling in a single component body.

`ui-minimal` remains the structural baseline, but its internal implementation should continue normalizing toward the grouped `Style` pattern instead of expanding legacy declaration sprawl.

## 2. Architecture Layers

Keep frontend styling responsibilities in this order:

1. `theme`: semantic tokens, color systems, spacing, motion, and z-index.
2. `context theme`: maps feature or product state to semantic token bundles.
3. `shared primitives`: buttons, cards, headers, tables, inputs, alerts, empty states, loaders.
4. `feature wrappers`: app-specific composition, brand variants, workflow-specific copy.
5. `page shell`: route layout, suspense/loading boundaries, section ordering, shell-level motion.

Do not let page files become the source of truth for tokens, default focus states, or reusable interaction patterns.

## 3. Styled-Components Format

Preferred file shape for app and feature code:

```tsx
import styled, { css } from "styled-components";

const Style = {
  Root: styled.section`
    display: grid;
    gap: ${({ theme }) => theme.spacing.md};
    padding: ${({ theme }) => theme.spacing.lg};
    background: ${({ theme }) => theme.color.bgSurface};
    border: 1px solid ${({ theme }) => theme.color.borderSubtle};
    border-radius: ${({ theme }) => theme.radius.lg};
  `,
  Title: styled.h2`
    margin: 0;
    font: ${({ theme }) => theme.typography.weightSemibold}
      ${({ theme }) => theme.typography.h2Size}
      ${({ theme }) => theme.typography.displayFamily};
    color: ${({ theme }) => theme.color.textPrimary};
  `,
  Meta: styled.span<{ $tone: "default" | "muted" }>`
    color: ${({ theme, $tone }) =>
      $tone === "muted" ? theme.color.textSecondary : theme.color.textPrimary};
  `,
};

export const ExamplePanel = () => (
  <Style.Root>
    <Style.Title>Panel title</Style.Title>
    <Style.Meta $tone="muted">Supporting metadata</Style.Meta>
  </Style.Root>
);
```

Rules:

1. Use one `Style` object per component module unless the file is purely primitives/tokens.
2. Keep transient styling props prefixed with `$`.
3. Keep conditionals in helpers or `css` blocks, not inlined string chaos.
4. Export React components, not styled primitives, from feature modules.
5. For shared primitives packages, internal helper groupings may be split by concern, but new work should still favor grouped declarations over long flat lists.

Allowed inline style exceptions:

1. runtime positioning for portals, popovers, and anchored overlays
2. injecting CSS variables from dynamic measurements
3. transform values controlled by Motion/WAAPI where styled-components would fight the runtime

## 4. Theme And Token Rules

Use the `ui-minimal` theme model as the baseline:

1. base theme contains semantic tokens only, not product copy or feature names
2. theme provider merges overrides instead of replacing the whole tree
3. CSS variables are exported once from the active theme
4. shared primitives read semantic tokens, not hard-coded palette literals

Recommended split:

1. `theme.ts[x]`: base tokens, theme merge, CSS variable export
2. `contextTheme.ts`: feature or mode-specific semantic token mapping
3. `motion.ts`: reusable motion helpers and defaults
4. `styles.ts` or component-local `Style`: component surface declarations

Do:

1. use semantic names such as `bgSurface`, `borderSubtle`, `brandSoft`
2. keep z-index, radius, spacing, and typography in the theme contract
3. disable transitions during theme flips: `[data-theme-switching] * { transition: none !important; }`

Do not:

1. bake raw hex values into page components when a token already exists
2. use a page file as the only place where a brand color or radius value is defined
3. spread visual constants across stores, hooks, and components without a theme boundary

## 5. Loading Surfaces And Separation Of Concerns

Use `fintech_v1` as the maturity model here.

Preferred pattern:

1. route-level `Suspense` or shell loader for page hydration
2. keyed loading-state utilities in stores for concurrent feature actions
3. section-level loaders or skeletons as dedicated components
4. button-level `loading` or `busy` state only for the action being performed

Component split:

1. data orchestration and store access
2. loading/error/empty branching
3. presentational component tree
4. style declarations

Do not collapse all four into one giant component unless the surface is truly trivial.

Suggested folder split:

1. `components/shared/MinimalEntry` or route shell for page loading
2. `components/ui/*Skeleton*` or `*Loader*` for reusable loading surfaces
3. `utils/loadingState.ts` for keyed loading helpers
4. feature component for business rendering only

## 6. Motion Design System

Animate only when it improves:

1. feedback
2. orientation
3. continuity
4. deliberate delight

Never animate:

1. keyboard-initiated actions such as shortcut navigation, focus movement, or tab traversal
2. layout properties for interactive feedback
3. theme switches

Implementation order:

1. CSS transitions
2. WAAPI
3. spring-based Motion
4. CSS keyframes
5. manual `requestAnimationFrame`

Animate:

1. `transform`
2. `opacity`
3. `color` and `background-color` for state feedback

Avoid:

1. `transition: all`
2. `width`, `height`, `top`, `left`
3. permanent `will-change`
4. blur-heavy animation for core flows

Default timings:

| Interaction | Duration | Easing |
|-------------|----------|--------|
| Button press | 100-160ms | `cubic-bezier(0.22, 1, 0.36, 1)` |
| Tooltips and small popovers | 125-200ms | `ease-out` or enter curve |
| Dropdowns and selects | 150-250ms | `cubic-bezier(0.22, 1, 0.36, 1)` |
| Modals and drawers | 200-350ms | `cubic-bezier(0.22, 1, 0.36, 1)` |
| Slides and screen movement | 200-300ms | `cubic-bezier(0.25, 1, 0.5, 1)` |
| Simple hover | 200ms | `ease` |

Directional rules:

1. shared elements should transition in place rather than hard-cut
2. directional motion should reflect actual layout direction
3. overlays should emerge from their trigger when the trigger is known
4. exits should be faster than enters

Accessibility:

1. gate hover motion behind `@media (hover: hover) and (pointer: fine)`
2. respect `prefers-reduced-motion`
3. during drag, keep the element attached to the pointer with no lag

Performance:

1. pause loops off-screen with `IntersectionObserver`
2. toggle `will-change` only during heavy motion
3. avoid CSS-variable-driven drag transforms on complex trees
4. do not mix Motion `x`/`y` props with handwritten `transform` on the same element

## 7. Review Checklist

Before merging frontend work, verify:

1. theme tokens exist before introducing raw literals
2. new component-local styles use the grouped `Style` object pattern
3. loading state is separated into explicit boundaries, not hidden in random booleans
4. animations use `transform` and `opacity` only
5. hover motion is gated for actual hover devices
6. exits are faster than enters
7. repeated surfaces reuse primitives instead of page-local restyling

## 8. Reference Notes

Use the animation reference notes in `docs/references/`:

1. [Reference Index](./references/README.md)
2. [Decision Framework](./references/decision-framework.md)
3. [Spring Animations](./references/spring-animations.md)
4. [Component Patterns](./references/component-patterns.md)
5. [Clip-Path Techniques](./references/clip-path-techniques.md)
6. [Gesture And Drag](./references/gesture-drag.md)
7. [Performance Deep Dive](./references/performance-deep-dive.md)
8. [Review Format](./references/review-format.md)
9. [Contextual Animations](./references/contextual-animations.md)
