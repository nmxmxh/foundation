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

Extraction status:

1. `field_os` is the reference structural baseline.
2. `fintech_v1` still needs primitive normalization before extraction.
3. `reframe_v1` keeps app-owned primitives that mirror this package boundary.

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

Extension model:

1. apps wrap `MinimalThemeProvider` with app-owned token overrides
2. apps keep brand icons, copy, shells, and feature composition locally
3. motion helpers are exported separately so apps can compose page choreography without forking primitives
4. primitive contracts stay generic; app-specific variants should be wrappers, not changes to the shared component API
5. app shells pass app-owned navigation, auth, and status content into `MinimalAppShell`/`MinimalScrollMain`; shared primitives only own layout, focus, scroll, and motion behavior

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

Observer posture:

1. layout measurement should prefer `ResizeObserver`
2. visibility-triggered behavior should prefer `IntersectionObserver`
3. `MutationObserver` is exception-only for narrow third-party DOM or `contenteditable` adapters, never the default state-management path for shared primitives
4. any observer-backed primitive must disconnect cleanly and avoid unbounded render/measure loops

Extraction gate:

1. primitive prop and slot structures across the three apps must converge enough that the shared package is not just moving divergence into one repo.
