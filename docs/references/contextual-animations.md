# Contextual Animations

Contextual motion keeps the user oriented because the transition begins from the element or region that caused it.

Use contextual motion for:

1. icon swaps inside toggles or segmented controls
2. word-level stagger entrances in hero copy or section headings
3. panels or trays launched from a specific trigger
4. fixed-offset exits where a returning direction matters

Rules:

1. persistent elements should move from their current position to their next position
2. do not duplicate shared elements and crossfade them as separate instances
3. avoid generic center-screen entrances for contextual surfaces
4. keep directional travel aligned with actual layout direction

Stagger guidance:

1. `30-50ms` per item
2. total stagger under `300ms`
3. reserve larger theatrical staggers for rare, branded moments
