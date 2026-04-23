# Review Format

When reviewing motion code, use this table first:

| Before | After | Why |
|--------|-------|-----|
| What the interaction did previously | What now animates or transitions | Why the change improves feedback, orientation, continuity, or delight |

Then check this issue list:

1. Does the motion have a clear purpose?
2. Is the animation interruptible?
3. Are only `transform` and `opacity` animating?
4. Does exit complete faster than enter?
5. Is hover behavior gated for actual hover devices?
6. Does reduced motion have a fallback?
7. Are shared elements transitioning in place instead of hard-cutting?
8. Does the motion emerge from the trigger when contextual?
9. Is `will-change` temporary rather than permanent?
10. Would this still feel good after the hundredth repetition?
