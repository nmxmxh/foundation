# ui-minimal

`ui-minimal` is the structural design-system boundary for shared Ovasabi product primitives.

It owns:

1. slot-based `Minimal*` component contracts
2. CSS variable tokens
3. shared motion tokens and reduced-motion behavior
4. accessibility baselines for focus, keyboard, and live regions
5. typography and surface defaults that apps can override through theme composition

It does not own:

1. app brand identity
2. page copy
3. product-specific iconography
4. app-specific route, auth, and page shell composition

Compatibility status:

1. Generated apps consume `@ovasabi/ui-minimal` through the package boundary.
2. App-local UI components wrap `Minimal*` primitives instead of duplicating
   foundation primitives.
3. Product-specific auth, route lists, content, and domain state stay in the
   application.

Current canonical surfaces:

1. `MinimalAppShell`
2. `MinimalSkipLink`
3. `MinimalSidebar`
4. `MinimalScrollMain`
5. `MinimalScrollFeedbackSurface`
6. `MinimalHeader`
7. `MinimalButton`
8. `MinimalInput`
9. `MinimalCard`
10. `MinimalBadge`
11. `MinimalTable`
12. `MinimalCalendar`
13. `MinimalStatCard`
14. `MinimalFilterBar`
15. `MinimalDropdown`
16. `MinimalSegmentedControl`
17. `MinimalAlert`
18. `MinimalEmptyState`
19. `MinimalExplainer`
20. `MinimalTooltip`
21. `MinimalActionModal`
22. `MinimalFormSection`
23. `MinimalFieldGrid`
24. `MinimalActionRow`
25. `MinimalSkeleton`
26. `MinimalDisplaySection`
27. `MinimalLandingSection`
28. `MinimalInfoPanel`
29. `MinimalCheckbox` (Base UI-backed)
30. `MinimalSwitch` (Base UI-backed)
31. `MinimalNumberField` (Base UI-backed)
32. `MinimalTabs` (Base UI-backed)
33. `MinimalDatePicker` (Base UI Popover + Foundation calendar)
34. `MinimalTimePicker`

Extension model:

1. apps wrap `MinimalThemeProvider` with app-owned token overrides
2. apps keep brand icons, copy, shells, and feature composition locally
3. motion helpers are exported separately so apps can compose page choreography without forking primitives
4. primitive contracts stay generic; app-specific variants should be wrappers, not changes to the shared component API
5. app shells pass app-owned navigation, auth, and status content into `MinimalAppShell`/`MinimalScrollMain`; shared primitives only own layout, focus, scroll, and motion behavior
6. Base UI is an implementation peer of `ui-minimal`; generated app manifests install it once because Foundation packages are preserved local symlinks, while application source consumes `Minimal*` contracts and never imports `@base-ui/react` directly

Implementation posture:

1. `theme.tsx` is the canonical source for semantic tokens, theme merging, and CSS variable export.
2. `primitives.tsx` is the canonical surface area for shared structural components.
3. New primitive or app-level styled-component work should prefer grouped declarations in `const Style = { ... }`; legacy standalone declarations should be treated as normalization debt, not the preferred pattern to expand.
4. Loading shells, entry states, and skeletons should remain separate from business feature rendering; use shared primitives such as `MinimalSkeleton` instead of ad hoc feature-level placeholders.
5. Use `../docs/styling_design_practices.md` and `../docs/references/` for detailed styling and motion rules.
6. Layout motion should reuse `useMinimalScrollFeedback` and `MinimalScrollFeedbackSurface` for tactile scroll response. Keep it subtle, reduced-motion aware, and limited to transform values.
7. Route shells should use `MinimalSkipLink` and the `minimalMainScrollAttribute`/`scrollMinimalMainToTop` helpers so accessibility and scroll-reset behavior converge across apps.
8. Anchored overlays must be width/height aware: dropdowns clamp to the viewport, expose max-height/min-width policy, and preserve trigger alignment without overflowing small screens.
9. Dialog primitives must separate shell height from body scroll. Use modal max-width/max-height and scrollable body regions instead of letting content push panels off-screen.
10. Display and landing sections should encode composition anchors, media aspect ratios, and min-height decisions in props. Page files should not recreate hero/display geometry with ad hoc inline CSS.
11. Frontend reference imagery should be section-sliced: one horizontal frame per section, consistent palette/type/CTA logic, and enough width/height clarity that components can be rebuilt without guessing.
12. **Mobile-First Phone-Frame Containment**: When targeting mobile/app viewports, avoid full-viewport stretch. Wrap in a centered flex column (`max-width: 480px; margin: 0 auto; min-height: 100vh; position: relative;`) and build navigation docks as in-frame absolute/sticky bottom bars rather than globally fixed elements that escape the frame bounds.
13. **Fluid Sizing and modular Spacing**: Implement spacing and typography dynamically using the `clamp()` formula (for example, `font-size: clamp(1rem, 0.9rem + 0.5vw, 1.25rem)` and `padding: clamp(12px, 2vw + 4px, 24px)`) matching the 8px modular scale.
14. **Z-Index Layer Hierarchy**: Enforce a strict z-index scale: overlays/dropdowns at `200`, modals/dialogs/bottom-sheets at `300`, and system alerts/toasts at `400`.
15. **Accessibility Invariants**: Always provide custom `:focus-visible` outline rings (do not use `outline: none` globally). Maintain a minimum touch target size of `44px x 44px` for mobile interactive primitives.
16. **Interaction Ownership**: New checkbox, switch, number-field, tabs, and popover mechanics compose Base UI subpath exports. Public props stay framework-neutral and do not expose Base UI event-detail types.
17. **Scheduling Values**: Calendar overlays may accept `Date | string` for compatibility, but app wrappers should serialize date-only values as local `YYYY-MM-DD`, wall times as `HH:mm`, and instants as ISO/RFC3339 with an explicit product timezone policy.
18. **Calendar Navigation**: `MinimalCalendar` owns bounded day, month, and 16-year drill-down views with synchronized animated transitions. Use `showAdjacentDays={false}` for compact picker overlays and `showTodayAction={false}` only when the product intentionally removes the shortcut.
19. **Calendar Safety**: Date-only strings are parsed and emitted as local calendar dates. Bounds, disabled-date rules, focus movement, and keyboard selection apply consistently across direct day navigation and month/year jumps.

Theme additions in `0.2.0` are compatibility-normalized by `createMinimalTheme`:

1. `colorScheme` for native light/dark control rendering
2. `control` tokens for target size, control heights, and icon size
3. `overlay` tokens for viewport gutters, anchor offsets, and maximum height
4. full typography weight, line-height, motion, control, and overlay CSS variables
5. `MinimalThemeScope` for nested edition/widget overrides without rewriting document-level variables

Observer posture:

1. layout measurement should prefer `ResizeObserver`
2. visibility-triggered behavior should prefer `IntersectionObserver`
3. `MutationObserver` is exception-only for narrow third-party DOM or `contenteditable` adapters, never the default state-management path for shared primitives
4. any observer-backed primitive must disconnect cleanly and avoid unbounded render/measure loops

Compatibility gate:

1. Primitive prop and slot structures must stay generic enough that the shared
   package does not move app-specific divergence into Foundation Core.
