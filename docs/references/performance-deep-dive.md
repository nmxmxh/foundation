# Performance Deep Dive

Prefer the cheapest implementation that preserves quality.

Order of preference:

1. CSS transitions for simple, interruptible UI
2. WAAPI for timeline control without React re-render churn
3. Motion springs for gesture or physics-heavy work
4. keyframes for predetermined decorative sequences
5. manual JS only when the others cannot express the behavior

Performance rules:

1. animate only `transform` and `opacity`
2. keep blur under control and avoid animating heavy filters on core flows
3. toggle `will-change` only during active heavy motion
4. pause loops off-screen with `IntersectionObserver`
5. avoid layout-triggering properties such as `top`, `left`, `width`, `height`

Known traps:

1. `transition: all`
2. CSS variables driving drag transforms on deep trees
3. backdrop blur on large scrolling surfaces
4. mixing Motion `x`/`y` with custom `transform`
5. mount animations with no user or navigation context

Debugging:

1. slow animations in DevTools
2. inspect paint and layout timelines
3. record frame-by-frame for coordinated sequences
4. retoggle rapidly to confirm interruption behaves cleanly
