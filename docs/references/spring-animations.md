# Spring Animations

Use springs when the motion needs believable mass, overlap, or release behavior.

Good spring use cases:

1. toggle thumbs
2. drag release and settle
3. magnetic button offsets
4. stacked cards and overshoot

Avoid springs for:

1. high-frequency text or opacity-only fades
2. deterministic multi-step sequences better expressed as transitions
3. accessibility-sensitive surfaces where bounce adds distraction

Framer Motion guidance:

1. prefer springs for pointer-release motion, not every state change
2. keep one transform owner per element
3. do not mix Motion `x` and `y` props with a handwritten `transform` string on the same element

Suggested starting presets:

1. micro feedback: stiffness `420`, damping `30`, mass `0.7`
2. drawer settle: stiffness `320`, damping `32`, mass `0.9`
3. magnetic hover: stiffness `260`, damping `18`, mass `0.8`
4. drag release: stiffness `220`, damping `24`, mass `1`

Use `useSpring` when:

1. one value must remain continuously responsive to input
2. motion should retarget smoothly rather than restart

Do not use a spring where a 150ms transition communicates the same thing more clearly.
