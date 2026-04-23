# Gesture And Drag

During direct manipulation, the element must stay attached to the pointer.

Rules:

1. no easing while the pointer is actively dragging
2. add damping or spring only after release
3. capture the pointer when the gesture owns the interaction
4. gate hover-only gesture polish behind hover-capable media queries

Use for:

1. swipe-to-dismiss
2. reorder handles
3. bottom-sheet pull gestures
4. sliders with momentum

Avoid:

1. CSS-variable-driven drag transforms on large trees
2. hard stops at boundaries without friction or resistance
3. mixing multiple transform owners

Validation:

1. test on real touch devices
2. test interruption and retargeting
3. verify keyboard alternatives still exist for the same task
