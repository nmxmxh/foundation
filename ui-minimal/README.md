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
4. page shell composition

Extraction status:

1. `field_os` is the reference structural baseline.
2. `fintech_v1` still needs primitive normalization before extraction.
3. `reframe_v1` keeps app-owned primitives that mirror this package boundary.

Current canonical surfaces:

1. `MinimalHeader`
2. `MinimalButton`
3. `MinimalInput`
4. `MinimalCard`
5. `MinimalBadge`
6. `MinimalTable`
7. `MinimalCalendar`
8. `MinimalStatCard`
9. `MinimalFilterBar`
10. `MinimalDropdown`
11. `MinimalSegmentedControl`
12. `MinimalAlert`
13. `MinimalEmptyState`
14. `MinimalExplainer`
15. `MinimalTooltip`
16. `MinimalActionModal`
17. `MinimalFormSection`
18. `MinimalFieldGrid`
19. `MinimalActionRow`
20. `MinimalSkeleton`

Extension model:

1. apps wrap `MinimalThemeProvider` with app-owned token overrides
2. apps keep brand icons, copy, shells, and feature composition locally
3. motion helpers are exported separately so apps can compose page choreography without forking primitives
4. primitive contracts stay generic; app-specific variants should be wrappers, not changes to the shared component API

Implementation posture:

1. `theme.tsx` is the canonical source for semantic tokens, theme merging, and CSS variable export.
2. `primitives.tsx` is the canonical surface area for shared structural components.
3. New primitive or app-level styled-component work should prefer grouped declarations in `const Style = { ... }`; legacy standalone declarations should be treated as normalization debt, not the preferred pattern to expand.
4. Loading shells, entry states, and skeletons should remain separate from business feature rendering; use shared primitives such as `MinimalSkeleton` instead of ad hoc feature-level placeholders.
5. Use `../docs/styling_design_practices.md` and `../docs/references/` for detailed styling and motion rules.

Observer posture:

1. layout measurement should prefer `ResizeObserver`
2. visibility-triggered behavior should prefer `IntersectionObserver`
3. `MutationObserver` is exception-only for narrow third-party DOM or `contenteditable` adapters, never the default state-management path for shared primitives
4. any observer-backed primitive must disconnect cleanly and avoid unbounded render/measure loops

Extraction gate:

1. primitive prop and slot structures across the three apps must converge enough that the shared package is not just moving divergence into one repo.
