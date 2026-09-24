# Ovasabi Styling and Design Practices

Status: 0.0.1  
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
4. Creating standalone app-local `Button`, `Input`, `Card`, `Table`, `Modal`, `Dropdown`, or `Skeleton` implementations when `ui-minimal` already exposes the structural primitive.
5. Starting a frontend vertical slice with hand-written API contract types instead of generating `frontend/src/types/protos` with `make proto-ts`.
6. Importing `foundation/ui-minimal/ts/src/*` directly or creating source aliases to it. Use `@ovasabi/ui-minimal` as a local file dependency and preserve symlinks in Vite, Vitest, and TypeScript config.

`ui-minimal` remains the structural baseline, but its internal implementation should continue normalizing toward the grouped `Style` pattern instead of expanding legacy declaration sprawl.

Removing an exported contract from `@ovasabi/ui-minimal` requires an entry in `ui-minimal/CHANGELOG.md` naming the replacement. The package is a local file dependency, so a removal does not fail at install — it surfaces later in each consumer as a type error with no explanation attached, and every consumer independently writes a local re-implementation that then drifts from the others. A comment in a consumer saying the package "used to carry this" records the absence of a migration path, not the presence of one.

Operational frontend concerns belong in `frontend-kit`, not visual primitives. Use `@ovasabi/frontend-kit` for persistence, metadata, reset handles, and runtime snapshot hooks, then compose those handles with `ui-minimal` surfaces.

## 2. Architecture Layers

Keep frontend styling responsibilities in this order:

1. `theme`: semantic tokens, color systems, spacing, motion, and z-index.
2. `context theme`: maps feature or product state to semantic token bundles.
3. `shared primitives`: buttons, cards, headers, tables, inputs, alerts, empty states, loaders.
4. `feature wrappers`: app-specific composition, brand variants, workflow-specific copy.
5. `page shell`: route layout, suspense/loading boundaries, section ordering, shell-level motion.

Do not let page files become the source of truth for tokens, default focus states, or reusable interaction patterns.

## 3. Linaria Format (build-time extracted styles)

Foundation styles are written with Linaria: the same `styled` tagged-template
shape, extracted to a static stylesheet at build time by `@wyw-in-js/vite`. No
styling runtime ships, no rules are injected on the main thread, and the CSS can
load before any script (research doc `ui_render_performance_research.md` §14.8).
`ui-minimal` and the frontend template use it; new code must.

Preferred file shape for app and feature code:

```tsx
import { styled } from "@linaria/react";
import { minimalVars, variantRules } from "@ovasabi/ui-minimal";

const Style = {
  Root: styled.section`
    display: grid;
    gap: ${minimalVars.space.md};
    padding: ${minimalVars.space.lg};
    background: ${minimalVars.color.bgSurface};
    border: 1px solid ${minimalVars.color.borderSubtle};
    border-radius: ${minimalVars.radius.lg};
  `,
  Title: styled.h2`
    margin: 0;
    font: ${minimalVars.typography.weightSemibold} ${minimalVars.typography.h2Size}
      ${minimalVars.typography.displayFamily};
    color: ${minimalVars.color.textPrimary};
  `,
  Meta: styled.span`
    color: ${minimalVars.color.textPrimary};
    ${variantRules("tone", ["muted"] as const, () => `color: ${minimalVars.color.textSecondary};`)}
  `,
};

export const ExamplePanel = () => (
  <Style.Root>
    <Style.Title>Panel title</Style.Title>
    <Style.Meta data-minimal-tone="muted">Supporting metadata</Style.Meta>
  </Style.Root>
);
```

Rules:

1. Read tokens through `minimalVars` (CSS variables with base-theme fallbacks), never a runtime theme object. Code that needs a resolved value in script uses `useMinimalTheme()`.
2. Discrete variants are attribute selectors — `variantRules(name, values, block)` plus `data-minimal-<name>` on the element — not prop functions returning blocks. They cost nothing at runtime and are cheaper for the style engine than per-element variables (research doc §14.3).
3. A prop function (`${(props) => value}`) is allowed only in a property value, for genuinely continuous values; Linaria turns it into a per-element CSS variable. Keep transient props prefixed with `$`.
4. Everything else in a template is evaluated at build time, so it must be importable without React or a styling runtime: import tokens from `@ovasabi/ui-minimal/tokens` in build-time helpers, hoist helpers that take callbacks into module constants, and write `import type` rather than inline `type` specifiers.
5. Keyframes live in the template that uses them (Linaria scopes their names); `minimalEnter` fragments already do this.
6. Use one `Style` object per component module unless the file is purely primitives/tokens, and export React components, not styled primitives, from feature modules.

Existing apps whose own code still uses styled-components keep working through
`MinimalStyledThemeBridge` (`@ovasabi/ui-minimal/styled-components`, mounted by
`AppThemeProvider`); migrate a module to the format above when it is touched.

### Development extraction

Vite development requires two controls together:

1. Linaria filters must accept query strings, including `?v=` and HMR timestamps.
2. Exclude `@ovasabi/ui-minimal` from dependency prebundling so WyW receives the original source.

```ts
wyw({
  include: [/ui-minimal[\\/](ts[\\/])?src[\\/].*\.[jt]sx?(?:\?.*)?$/, /[\\/]src[\\/].*\.[jt]sx?(?:\?.*)?$/],
  transformLibraries: true,
  prefixer: false,
})
// This property belongs in the Vite configuration object.
optimizeDeps: { exclude: ['@ovasabi/ui-minimal'] }
```

Keep React and its CommonJS entrypoints eligible for optimization.
The package exclusion also covers `ui-minimal` subpaths. Existing explicit subpath exclusions can remain.
Do not disable the whole dependency optimizer or replace styled components with runtime fallbacks.

The managed patch upgrades existing WyW configurations in both Vite and Vitest files.
It preserves custom plugins, aliases, optimizer entries, and established project fixes.
Dynamic configuration receives a manual review message. The patch does not execute configuration files.

Regression: `npm --prefix frontend-lab run test:linaria-dev` checks cold rendering and CSS HMR in Chromium.
Its three negative cases prove that either partial fix still fails.
Vite 7.3.6 and 8.3.0 passed this test with WyW 2.5.1 on 2026-09-24.
Evidence: `benchmark-results/linaria_dev_20260924_vite7.json` and `benchmark-results/linaria_dev_20260924_vite8.json`.

References: [Vite optimizer exclusions](https://github.com/vitejs/vite/blob/main/docs/config/dep-optimization-options.md)
and [WyW library transforms](https://wyw-in-js.dev/bundlers/vite).

Allowed inline style exceptions:

1. runtime positioning for portals, popovers, and anchored overlays
2. injecting CSS variables from dynamic measurements
3. transform values driven by a WAAPI timeline (`createMinimalTimeline`)

## 4. Theme And Token Rules

Use the `ui-minimal` theme model as the baseline:

1. base theme contains semantic tokens only, not product copy or feature names
2. theme provider merges overrides instead of replacing the whole tree
3. CSS variables are exported once from the active theme
4. shared primitives read semantic tokens, not hard-coded palette literals
5. direct controls read `theme.control` dimensions and overlays read `theme.overlay` constraints; compact visuals must still preserve the 44px pointer target
6. nested edition and embedded-widget overrides use `MinimalThemeScope` instead of installing another document-global theme

Recommended split:

1. `theme.ts[x]`: base tokens, theme merge, CSS variable export
2. `contextTheme.ts`: feature or mode-specific semantic token mapping
3. `motion.ts`: reusable motion helpers and defaults
4. `styles.ts` or component-local `Style`: component surface declarations
5. `DESIGN.md`: product-level visual identity contract for agents and future audits

Do:

1. use semantic names such as `bgSurface`, `borderSubtle`, `brandSoft`
2. keep z-index, radius, spacing, and typography in the theme contract
3. disable transitions during theme flips: `[data-theme-switching] * { transition: none !important; }`
4. keep `DESIGN.md` tokens aligned with the app theme and `MinimalThemeProvider` overrides
5. express component width and height intent in `DESIGN.md` component tokens for overlays, modals, media wells, and landing sections

Do not:

1. bake raw hex values into page components when a token already exists
2. use a page file as the only place where a brand color or radius value is defined
3. spread visual constants across stores, hooks, and components without a theme boundary
4. treat `DESIGN.md` prose as decorative documentation; agents should rely on it before making visual changes

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

1. CSS transitions, with `@starting-style` for enter and `transition-behavior: allow-discrete` for exit
2. native elements that bring their own lifecycle: `<dialog>`, `popover`, `<details>`, View Transitions
3. CSS keyframes (infinite ones paused off screen)
4. WAAPI, for sequences a transition cannot express
5. spring-based Motion, only for gesture physics and drag
6. manual `requestAnimationFrame`

Why this order (measured, research doc `ui_render_performance_research.md` §14):
framer-motion animates independent transforms (`x`, `y`, `scale`, `rotate`),
`height: auto` and springs from JavaScript on the main thread — 49–61 style
writes per animation against zero for the CSS equivalent — so a main-thread
stall freezes them. Only its opacity reaches the compositor. A `motion.*`
component also costs 2.5–3.9× the mount time of the same element with a CSS
fade, per instance.

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
| ------------- | ---------- | -------- |
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

## 7. Width, Height, And Section Fidelity

Shared primitives must own their dimensional behavior. Visual polish is not only padding and radius; it is whether a component understands its container, viewport, and content load.

Rules:

1. Anchored overlays should measure the trigger and viewport, then clamp `left`, `width`, and `max-height` before rendering. Dropdowns should expose whether they match trigger width or use a minimum panel width.
2. Modals should use `width: min(...)`, explicit `max-height`, and an internal scroll body. Content should never push a dialog beyond the viewport.
3. Mobile dialogs should be able to become bottom sheets, with safe-area-aware padding and no hidden action rows.
4. Fixed-format media regions need `aspect-ratio`, `min/max-height`, and overflow policy. Do not rely on image intrinsic size to define the layout. Every image reserves its box before it loads: use `MinimalImage` (its type requires `width`+`height` or `aspectRatio`), or write both attributes on a raw `<img>` — the surface check fails an unsized one. Measured: an unsized image moved the content below it by its full height; `MinimalImage` by 0 px (research doc §15.5). The first-screen hero takes `priority`; everything else stays lazy with async decode.
5. Display sections should declare their composition anchor, visual mode, and minimum height. Do not rebuild hero geometry with one-off inline styles.
6. Information panels should handle icon, copy, metadata, and action regions without text collision at narrow widths.
7. When a component has portal positioning, runtime coordinates are allowed inline; the surrounding sizing rules still belong in the primitive.

Use these shared primitives for the common dimensional cases:

1. `MinimalImage`: any content image; sized box before load, lazy + async decode by default, `priority` for the hero, `reveal` to fade in after decode (client-only, never on a prerendered hero).
2. `MinimalDisplaySection`: hero or display-first section with art-directed anchors, background/image modes, min-height, and media aspect ratio.
3. `MinimalLandingSection`: editorial landing/information sections with optional media and responsive composition anchors.
4. `MinimalInfoPanel`: dense but readable information callouts, receipts, validation notes, proof rows, and explanation panels.
5. `MinimalDropdown`: anchored select/search panels with viewport-aware width and max-height.
6. `MinimalActionModal`: confirmation and action dialogs with max-width, max-height, mobile sheet behavior, and scrollable bodies.

## 8. Frontend Reference Art Direction

When using generated or reference images to guide frontend implementation, require one horizontal image per section. Never compress a whole page into one tall mock when component fidelity matters.

Reference images should make these decisions visible:

1. composition anchor: centered, bottom-left over image, right-third caption, off-grid, stacked, or visual-first
2. background mode: solid surface, full-bleed image, side image, canvas image, color block, or tactile texture
3. dimensional intent: section min-height, media aspect ratio, panel width, CTA position, and safe text area
4. hierarchy: headline scale, secondary copy width, button priority, and repeated component rhythm
5. continuity: one palette, type scale, CTA language, radius system, and image treatment across all section frames

Do not use generic AI design habits as references:

1. repeated left-text/right-image sections
2. full pages collapsed into one vertical frame
3. card rows where a visual section is needed
4. decorative blobs, random gradients, or fake dashboard clutter
5. typography that cannot fit its declared container

For landing pages with no explicit count, use six section frames. For full websites, use eight. Each frame should be codeable as a single section.

## 9. Review Checklist

Before merging frontend work, verify:

1. theme tokens exist before introducing raw literals
2. new component-local styles use the grouped `Style` object pattern
3. loading state is separated into explicit boundaries, not hidden in random booleans
4. animations use `transform` and `opacity` only
5. hover motion is gated for actual hover devices
6. exits are faster than enters
7. repeated surfaces reuse primitives instead of page-local restyling
8. overlays clamp width and height to the viewport
9. modals have explicit max-height and scroll-body behavior
10. media/display sections declare aspect-ratio and min-height instead of depending on content accidents
11. shared packages stay ESM-clean end to end: no CJS-only transitive dependencies or peers (removed: `@base-ui/react`, 2026-08-24). Any import of a shared package must not drag non-tree-shakeable vendor code into application module graphs, SSR included
12. date-only values use local `YYYY-MM-DD`, wall times use `HH:mm`, and scheduled instants use ISO/RFC3339 plus an explicit product timezone policy
13. spacing values come from `theme.space`; vertical rhythm in document-shaped content is margin, not gap (§11)
14. media queries use `from()` / `until()` on named breakpoints, never a hand-typed pixel literal, and read `min-width` unless the rule is genuinely phone-only chrome
15. full-screen fixed surfaces use `svh` (never `vh`, which resolves to the *large* viewport on mobile and allocates a backing store taller than the screen); `dvh` only where the layout should follow the URL bar
16. fixed chrome pads with `env(safe-area-inset-*)`, and `index.html` carries `viewport-fit=cover` so those insets resolve to something other than `0px`
17. `<meta name="theme-color">` matches the page ground, so mobile browser chrome does not render a contrasting band around it
18. the page has at most one full-viewport `position: fixed` decorative layer. Each one is a full-screen compositor texture — 5.3 MB on a 390x844 phone at DPR 2 — composited under every pixel of every route
19. animated properties are `transform` and `opacity`. A `text-shadow` with a blur radius is neither: changing it invalidates paint for the element's whole ink-overflow rect, and the blur is rasterised on the CPU

## 10. Fluid Sizing, Proportions, and Primitive Intelligence

To handle sizing, dimensions, positioning, and accessibility fluidly and systematically across different viewport profiles:

### 1. Mobile-First Phone-Frame Containment

When targeting phone/app viewports, do not let layout blocks stretch to full-screen width. Confine the viewport to a centered layout container:

* Wrap the application in a centered flex column:

  ```css
  max-width: 480px;
  width: 100%;
  margin: 0 auto;
  box-shadow: 0 0 40px rgba(0, 0, 0, 0.1);
  min-height: 100vh;
  position: relative;
  ```

* Do not position navigation docks with `position: fixed; bottom: 0; left: 0; right: 0;` globally if they must sit in a phone frame. Instead, use absolute or sticky positioning bound within the parent centered container, or clamp the fixed positioning via media queries.

### 2. Fluid Sizing and Typography Formula

Spacing and typography must scale dynamically with the viewport width without breaking minimum legibility. Implement fluid spacing/font sizing using the `clamp()` formula:

* **Typography Formula**: `font-size: clamp(minSize, preferredFormula, maxSize)`
  * *Example*: `font-size: clamp(1rem, 0.9rem + 0.5vw, 1.25rem);`
* **Spacing Formula**: `padding: clamp(minPadding, preferredFormula, maxPadding)`
  * *Example*: `padding: clamp(12px, 2vw + 4px, 24px);`

### 3. Sizing and Spacing Proportions (Modular Scale)

All margins, paddings, and sizing increments come from `theme.space`, which is
a static modular scale on the 8px grid at a ratio near 1.6:

| Token | Value | Ratio to the rung below |
| :--- | ---: | ---: |
| `3xs` | 2px | — |
| `2xs` | 4px | 2.00 |
| `xs` | 8px | 2.00 |
| `sm` | 16px | 2.00 |
| `md` | 24px | 1.50 |
| `lg` | 40px | 1.67 |
| `xl` | 64px | 1.60 |
| `2xl` | 104px | 1.63 |
| `3xl` | 168px | 1.62 |

* Avoid arbitrary numbers (for example, `13px`, `19px`).
* **Spacing does not use `clamp()`.** The scale this replaced was six fluid
  ranges, and fluid spacing turns out to be a no-op where anyone actually is:
  every token pinned to its floor below ~400px and to its ceiling above
  ~1000px, so a phone at 390px and a desktop at 2560px each got a *static*
  scale and the `vw` term only did anything in the band between. What the
  ranges did accomplish was to compress the steps — `md` and `lg` resolved to
  12px and 18px on a phone, a ratio of 1.5 — and two spacings six pixels apart
  do not read as two categories of relationship. They read as inconsistency.
  Six tokens producing four distinguishable values is why layouts built from
  them looked uniform however they were spaced.
* **A step must be far enough from its neighbours to read as a decision.** A
  reader parses a step as deliberate somewhere around 1.6; below about 1.5 it
  reads as drift. Every adjacent pair above is at least 1.5.
* **A small screen needs fewer steps, not smaller ones.** A phone renders a
  section boundary at `lg` where a desktop renders `xl` — a rung down the same
  scale. It does not render `xl` at 64% of itself. Scaling every token by a
  proportion preserves the ratios and destroys the *distinctions*, which is
  precisely the uniformity the scale exists to end; selecting a different rung
  preserves the distinctions and loses only the largest, which is the right
  thing to lose at 390px.
* `clamp()` stays for **typography**, where the scaling is genuinely continuous
  and no reader can perceive a step boundary.
* `theme.spacing` is the previous scale's six names, resolved onto these steps
  by size rather than by name (`spacing.md` is `space.sm`). It is deprecated;
  new work reads `theme.space`.

### 4. Breakpoints

Breakpoints are named for what the layout does, never for a device — there is
no "tablet" width and there never was.

| Token | Value | What changes |
| :--- | ---: | :--- |
| `hand` | 30rem / 480px | one column; everything within thumb reach |
| `page` | 48rem / 768px | a second column becomes possible |
| `desk` | 64rem / 1024px | full measure plus margins |
| `wide` | 90rem / 1440px | content stops growing; gutters absorb the rest |

* **Use `from()`.** It emits `min-width`, so a layout is built *up* from the
  small screen. Every media query in the system before these tokens existed was
  `max-width`, which means each layout was defined by subtraction — the desktop
  arrangement with values removed — and combined with a spacing scale that
  floored on small screens, that is why the phone view read as cramped rather
  than as composed.
* **`until()` is the exception, not the mirror.** Reach for it only where the
  small screen needs something the large one must not have — phone-only chrome,
  a dock that becomes a sidebar — never to undo a desktop rule.
* **Never hand-type a pixel literal in a media query.** Nine independent
  literals is what the system had, and the cost was a dead band: a grid that
  collapsed at 900px inside a container that kept its wide padding until 640px,
  so every tablet in portrait and every large phone in landscape rendered a
  stacked layout inside desktop margins. Nobody chose that. It is what
  independent literals produce.
* The tokens are `rem`, so a viewer who has set a larger default font gets the
  wider layout at the point their text actually needs the room.

### 5. Z-Index Layering Scale

To prevent overlapping bugs between alerts, headers, docks, and modals, enforce a strict, semantic z-index hierarchy:

* `z-index: 1` — Content backgrounds, overlays
* `z-index: 10` — Sticky headers/elements inside the frame
* `z-index: 50` — Floating actions, in-frame docks
* `z-index: 100` — Global headers, navigation bars
* `z-index: 200` — Dropdown panels, select overlays
* `z-index: 300` — Modals, sheets, dialog boxes
* `z-index: 400` — Toasts, alerts, high-priority system notices

### 6. Positioning & Accessibility Invariants

* **Focus Indicators**: Never disable focus rings (`outline: none`) without providing a visible, custom `:focus-visible` ring.
* **Text Reflow**: Ensure container widths allow for text magnification up to 200% without overlapping text or hiding overflow actions.
* **Click Targets**: Interactive components (buttons, links, check-boxes) must provide a minimum tap target size of `44px x 44px` for mobile accessibility.

## 11. The Spacing Model — Margin, Padding, and the Limits of `gap`

`gap` is a property of the container. `margin` is a property of the child. That
one sentence contains the whole difference, and it determines where each
belongs.

`gap: 16px` says *every child of this box is 16px from its neighbour*. It is a
statement about a set, and it cannot say "more air before a section heading
than after it" — it does not know which child is a heading. Expressing
hierarchy underneath it means nesting another container for every distinct
spacing, or overriding with margins anyway and then maintaining two spacing
systems that fight each other.

`margin-block-start: 40px` on a heading says *a heading claims 40px above it*,
and the claim travels with the element. Move the heading and its rhythm moves
with it; add another and it is already spaced correctly. This is how
typesetting has worked since metal type, and it is why editorial CSS still
reads better than component CSS: the vertical rhythm is a property of the
content model rather than of wherever the content happened to be placed.

**Margin collapsing is the mechanism that makes claims compose,** and it exists
only in flow layout. A section claiming `xl` above itself containing a heading
claiming `lg` above resolves to `xl`, not to their sum. Each element states its
requirement, the larger one wins, and nothing needs to know about anything
else. `gap` has no equivalent, because it has only one number.

### The rules

1. **`padding` is a container's claim on its own inside edge.** It is never
   used to separate siblings.
2. **`margin` is an element's claim on the space around it.** It is the default
   tool for vertical rhythm in document-shaped content.
3. **`gap` is a statement that a set of peers is uniform.** Use it where that
   is true — grids, wrapping rows, equal toolbars — and only there.
4. **Document-shaped stacks use flow layout, not `flex-direction: column`.**
   This is the trap, and it is the most important line here. Swapping `gap` for
   margins while keeping the column *flex* gets none of the benefit: margins do
   not collapse in flex or grid, so they add instead of resolving; a trailing
   margin starts pushing the container; and the result is additive uniformity
   with more code — strictly worse than the `gap` it replaced. A column that
   genuinely needs `align-items`, `order`, or `flex` on a child is not
   document-shaped and keeps its `gap`. Fighting the layout mode to satisfy a
   spacing preference is how a codebase acquires `margin-top: -1px`.
5. **Margins run in one direction: `margin-block-start`.** Exactly one element
   owns each vertical space, so there is never a trailing margin pushing the
   container and never a `:last-child` reset to remember.
6. **Every value comes from `theme.space`.** No literals, and no `calc()` on a
   token to invent an intermediate step. A layout that needs a step which does
   not exist is a finding about the scale, not a licence for a one-off.

### Where `gap` stays

* **Grids.** Two axes. Margins produce edge overhang on both, and the fix —
  negative margins on the container — is a worse artefact than the one being
  avoided.
* **Wrapping rows.** Tag lists, chip rows, button clusters that reflow. A
  margin on a wrapped item is applied at the wrap point too, so the second row
  starts inset. `gap` is the only correct tool.
* **Genuinely uniform peer sets.** A toolbar of equal buttons *is* uniform.
  Uniformity is the truth there, and `gap` states it in one place instead of N.
* **Fixed, small clusters inside a primitive.** A field's label / input /
  message, an alert's title / body, a stat's label / value / hint. These are
  two- and three-element sets at one spacing, and a consumer does not want
  hierarchy introduced inside them. `gap` in `ui-minimal/primitives.tsx` is
  deliberate for this reason — the hierarchy problem lives in page
  composition, not in primitive internals.

### `MinimalStack`

`ui-minimal` ships the margin-based stack so the rule above has a tool behind
it. It is flow layout with a default rhythm, and a child claims more with
`data-space`:

```tsx
<MinimalStack rhythm="sm">
  <p>…</p>
  <h2 data-space="lg">A heading claims its own air</h2>
  <p>…</p>
</MinimalStack>
```

A note on the "owl" pattern (`.stack > * + * { margin-block-start: … }`): it is
`gap` wearing margin's clothes — one number, applied uniformly, owned by the
container. It is a fine implementation of rule 3 for a uniform stack. It is
**not** an implementation of rule 2 and it does not produce hierarchy;
`data-space` is the part that does.

## 12. Fonts And First Paint

A stylesheet that is correct and a page that paints quickly are different
achievements, and the second one is mostly decided outside the style layer — in
`index.html`, in `public/fonts`, and in what the build emits. These rules are
the standard every app is held to; the evidence for each number is in
[ui_render_performance_research.md](ui_render_performance_research.md) §15.3–§15.6,
and the adoption state per app is in
[frontend_paint_performance_handover.md](frontend_paint_performance_handover.md).

`make check-frontend-surface-practices` enforces F1, F2, F3, F4 and F6.

### Fonts

**F1 — No third-party font origin.** No `fonts.googleapis.com` or
`fonts.gstatic.com` link, and no `@import` of one. A third-party stylesheet
costs a DNS lookup, a TLS handshake and a round trip before the first glyph;
an `@import` is worse than a `<link>`, because it cannot start until the
stylesheet containing it has arrived and so serialises behind its own parent.
Self-host in `public/fonts`, served from the app's own origin.

**F2 — One variable `woff2` per family, subset by `unicode-range`.** No `.ttf`,
`.otf` or `.woff` in `public/`. Split latin / latin-ext / vietnamese so a reader
who needs only latin downloads only latin. ChooseChow is the reference: 9 files,
220 KB, largest 40 KB.

**F3 — `font-display: swap`, plus a size-adjusted fallback face.** `swap` paints
text immediately instead of holding the line blank; the fallback's `size-adjust`
and `ascent-override` keep the swap from moving anything when the real face
arrives. That pairing is what took ChooseChow's load CLS from 0.119 to 0 on a
mid phone — `swap` alone would have caused the shift, not prevented it.
Generate the metrics with `frontend-lab/profile/fontFallbackMetrics.mjs` and
list `"<family> Fallback"` directly after the web family in every stack.

**F4 — Preload only what *this route's* first screen paints**, `as="font"
type="font/woff2" crossorigin`, one or two faces.

Get the loading model right before optimising it, because the obvious worry is
the wrong one:

- **Declaring a face costs nothing.** A `@font-face` block is a rule, not a
  fetch. The browser downloads a face only when it is matched to text being
  rendered, and `unicode-range` narrows that to the subset those characters
  need. An app may declare every family it uses anywhere; a page that renders
  none of their glyphs downloads none of them. Splitting `@font-face` blocks
  per route buys nothing and costs a cache entry.
- **Preloading is the eager part.** `<link rel="preload">` fetches
  unconditionally, on every route that ships the tag, before the browser knows
  whether any text will use it. A preload in `index.html` is a preload on all of
  them. That is the only place where "this font is only needed on the pricing
  page" turns into real waste.

So the rule is per route, not per app. An app whose first screen is the same
everywhere (a body face) preloads it once in `index.html` — trotters and
civic_watch_ng are this shape. An app with a display face used only on its
landing route should preload it only there: `render(url)` may return
`{ html, head }`, and `prerenderShell` injects that `head` into that route's HTML
alone, so each prerendered page carries exactly its own preloads.

Preloads compete with the HTML and the CSS for the same early bytes, so
preloading past the first screen makes FCP worse rather than better — which is
why the ceiling is two faces and the check enforces it.

**F5 — `@font-face` is reachable without JavaScript.** Either in `index.html` or
in the extracted stylesheet (a `css` block from `@linaria/core`). It must never
sit behind a component render — that is the failure the styled-components
removal fixed, and it is what makes critical CSS possible at all: you cannot
inline what does not exist until script runs.

**F6 — No unused face ships.** `public/` is copied verbatim into the image, so a
face nothing references is deploy weight and a supply-chain surface even though
no browser ever requested it.

### Paint

**P1 — Prerender the shell and hydrate it.** `prerenderShell({ entry:
'src/entry-server.tsx', routes: [...], criticalCss: 'subset' })` in
`vite.config.ts` and `mountRoot` in `main.tsx`. `render(url)` must produce the
same markup on both sides for that URL: no `window`, no `Date.now()`, no
randomness, no signed-in data; `useSyncExternalStore` needs its server snapshot;
anything data-dependent renders a skeleton **of the final size**. Prerender
every route whose first screen does not need a session. Two constraints that
cost a day each to find: the SSR pass must bundle React and the router together
or the two copies crash on `useContext`, and an app that paints its own markup
inside `#root` (a splash screen) has to move it out before hydrating.

**P2 — The prerendered HTML is the first screen, not the page.** Use
`useFirstScreenCount(first, total, step)`. Committing a long list in one pass was
a 70–77 ms long task at CPU 6x; growing it per frame keeps it off the critical
path.

**P3 — Route-level code splitting, with a budget.** Every route that is not the
landing route loads as its own chunk (`createLazyPage` from
`@ovasabi/frontend-kit`). Entry chunk **≤ 120 KB brotli**, no single chunk above
150 KB.

**P4 — An image always knows its box before its pixels arrive.** `MinimalImage`
with intrinsic `width`/`height` or an explicit aspect ratio. An unsized image
moved the content below it by 200 px in the §13 measurement; `MinimalImage`
moved it 0.

**P5 — Motion runs on the compositor.** `transform` and `opacity` only, in CSS
or WAAPI. JS-driven geometry per frame freezes under a main-thread stall: 1
changed frame against 36 in a 400 ms block. Enforced by
`frontend_surface_practices_check.mjs`.

## 13. Reference Notes

Use the animation reference notes in `docs/references/`:

1. [Reference Index](./references/README.md)
2. [Decision Framework](./references/animation/decision-framework.md)
3. [Spring Animations](./references/animation/spring-animations.md)
4. [Component Patterns](./references/animation/component-patterns.md)
5. [Clip-Path Techniques](./references/animation/clip-path-techniques.md)
6. [Gesture And Drag](./references/animation/gesture-drag.md)
7. [Performance Deep Dive](./references/animation/performance-deep-dive.md)
8. [Review Format](./references/animation/review-format.md)
9. [Contextual Animations](./references/animation/contextual-animations.md)
