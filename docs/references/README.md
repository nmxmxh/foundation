# Foundation References Directory Index

This directory contains specialized resources, templates, and guides for developers and AI agents working on Ovasabi Foundation projects.

## Subdirectories

### [1. UI Animation (`animation/`)](animation/)

Quick-reference companion to [`styling_design_practices.md`](../styling_design_practices.md).

| File | Read when |
| ------ | ----------- |
| [`decision-framework.md`](animation/decision-framework.md) | Default: animation decisions, easing, and duration |
| [`spring-animations.md`](animation/spring-animations.md) | Using spring physics, Framer Motion `useSpring`, or tuning spring params |
| [`component-patterns.md`](animation/component-patterns.md) | Building buttons, popovers, tooltips, drawers, modals, toasts, and menus |
| [`clip-path-techniques.md`](animation/clip-path-techniques.md) | Using `clip-path` for reveals, tabs, hold-to-delete, or comparison sliders |
| [`gesture-drag.md`](animation/gesture-drag.md) | Implementing drag, swipe-to-dismiss, momentum, and pointer capture |
| [`performance-deep-dive.md`](animation/performance-deep-dive.md) | Debugging jank, choosing CSS vs WAAPI vs Motion, and avoiding repaint traps |
| [`review-format.md`](animation/review-format.md) | Reviewing animation code using a before/after/why format and issue checklist |
| [`contextual-animations.md`](animation/contextual-animations.md) | Contextual icon swaps, staggered entrances, shared-element continuity, or fixed-offset exits |
| [`design-md.md`](animation/design-md.md) | Writing or consuming product-level `DESIGN.md` visual identity contracts |

---

### [2. Machine-Readable Lifecycle Contracts (`lifecycle/`)](lifecycle/)

The machine-readable source of truth for all mutating command event lifecycles.

- **JSON Schema / Manifest**: [`lifecycle_contract.json`](lifecycle/lifecycle_contract.json)
- **Agent Guide / Cheat Sheet**: [`lifecycle_contract_guide.md`](lifecycle/lifecycle_contract_guide.md)

Read this when designing new API events or validating that an agent-produced command handler adheres to the 6 lifecycle invariants.

---

## Security Profiles Are App-Owned

Foundation does not keep concrete product security postures in this references
tree. Generated applications own their security profile at
`docs/security/profile.md`; Foundation only provides the create-once scaffold
template and lint contract.
