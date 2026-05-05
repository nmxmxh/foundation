# Component Patterns

## Buttons

1. hover: subtle lift or brightness only on hover-capable devices
2. press: slight compression, faster than hover
3. loading: keep width stable; replace label content without causing layout jump

## Tooltips And Small Popovers

1. emerge from the trigger, not generic center-screen fade
2. use scale `0.9` to `1` or small `translateY` plus opacity
3. exit faster than enter

## Dropdowns And Selects

1. use transform and opacity only
2. keep stagger subtle or absent for fast repeated interaction
3. preserve anchor alignment during animation
4. clamp panel width to the viewport before rendering
5. expose a max-height policy and scroll the option list, not the page
6. choose deliberately between trigger-matched width and a wider minimum panel for long labels

## Modals And Drawers

1. start around `scale(0.85-0.95)`, never `scale(0)`
2. separate backdrop fade from panel movement
3. drawers should move in the direction of actual placement
4. set `width`, `max-width`, and `max-height` as shell constraints
5. put overflowing content in an internal scroll body so title and actions remain reachable
6. treat small-screen action dialogs as bottom sheets when it improves reach and vertical fit

## Display And Landing Sections

1. declare the section anchor before styling: centered, bottom-left, bottom-right, side visual, stacked, or offset
2. pair every media region with an aspect ratio and max-height
3. use `min-height` as a deliberate viewport relationship, not a fixed decorative number
4. keep text safe areas explicit when imagery is full-bleed
5. vary section rhythm without changing the design system: palette, radius, type scale, and CTA family stay consistent

## Information Panels

1. reserve stable regions for icon, copy, metadata, and action
2. collapse to one column before text or actions collide
3. use border or accent bars for tone before adding heavy fills
4. keep dense panels readable with fixed gaps and predictable line-height

## Toasts

1. enter with a short slide and fade
2. allow interruption when many toasts stack or dismiss quickly
3. avoid heavy bounce for high-frequency notifications

## Menus And Navigation Overlays

1. persistent shared elements should transition in place
2. large overlays can stagger links, but keep total stagger under `300ms`
3. treat navigation as orientation first, personality second
