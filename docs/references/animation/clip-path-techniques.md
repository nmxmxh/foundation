# Clip-Path Techniques

Use `clip-path` when a reveal needs stronger spatial character than opacity plus translate can provide.

Good use cases:

1. directional tab reveals
2. hold-to-delete progress masks
3. comparison sliders
4. hero image wipes or content peels

Rules:

1. keep the clipped region simple enough to stay GPU-friendly
2. pair `clip-path` with opacity or transform, not layout animation
3. ensure the hidden state still preserves pointer and focus safety

Avoid `clip-path` for:

1. core repeated controls that need the cheapest possible motion path
2. large scrolling surfaces where paint cost becomes dominant
3. gestures that need perfect per-frame responsiveness if the paint cost is visible

Validation:

1. slow playback to verify edges feel intentional
2. test on lower-end devices for paint spikes
3. confirm reduced-motion fallback exists
