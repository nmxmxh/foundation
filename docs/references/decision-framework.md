# Decision Framework

Answer these four questions before writing motion code:

1. Should it animate?
2. What purpose does the motion serve?
3. What easing fits that purpose?
4. What speed keeps it responsive?

Use motion only for:

1. feedback
2. orientation
3. continuity
4. deliberate delight

Skip animation when:

1. the interaction is keyboard-initiated
2. the surface is extremely frequent and the motion would become noise
3. the state change is already obvious without motion
4. the implementation would require layout animation for a trivial benefit

Default implementation order:

1. CSS transition
2. WAAPI
3. spring-based Motion
4. keyframes
5. manual JS animation

Default easings:

1. enter: `cubic-bezier(0.22, 1, 0.36, 1)`
2. move: `cubic-bezier(0.25, 1, 0.5, 1)`
3. drawer: `cubic-bezier(0.32, 0.72, 0, 1)`

Default timing posture:

1. enter can be slightly slower
2. exit should be fast
3. hover should feel nearly invisible
4. route or modal transitions should feel smooth, not theatrical
