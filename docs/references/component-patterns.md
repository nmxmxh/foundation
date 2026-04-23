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

## Modals And Drawers

1. start around `scale(0.85-0.95)`, never `scale(0)`
2. separate backdrop fade from panel movement
3. drawers should move in the direction of actual placement

## Toasts

1. enter with a short slide and fade
2. allow interruption when many toasts stack or dismiss quickly
3. avoid heavy bounce for high-frequency notifications

## Menus And Navigation Overlays

1. persistent shared elements should transition in place
2. large overlays can stagger links, but keep total stagger under `300ms`
3. treat navigation as orientation first, personality second
