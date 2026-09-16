import { minimalVars } from "./tokens.ts";

/*
 * ui-minimal's motion as style strings, with no React and no runtime imports.
 *
 * Separate from `motion.ts` (which has the React hooks) because these strings
 * are interpolated into Linaria templates, and the extractor evaluates whatever
 * a template imports at build time. When an app builds a vendored kit, that
 * evaluation runs from the kit's real path, where React is not resolvable — so a
 * template that reached React through `motion.ts` failed the app's build
 * (found in the ChooseChow pilot, 2026-09-15). Keep this module runtime-free.
 *
 * Why the motion is CSS at all, and why each fragment carries its own
 * `@keyframes`: research doc sections 14.2 and 14.8.
 */

/** Durations in milliseconds, for code that must wait on an animation (presence). */
export const minimalMotionMs = {
  micro: 180,
  standard: 300,
  slow: 450,
} as const;

const enter = (name: string, from: string, duration: string) => `
  @keyframes ${name} {
    from {
      ${from}
    }
  }

  animation: ${name} ${duration} ${minimalVars.motion.easeEntrance} backwards;

  @media (prefers-reduced-motion: reduce) {
    animation: none;
  }

  :root:where([data-ui-tier="reduced_motion"]) & {
    animation: none;
  }

  &[data-minimal-enter="false"] {
    animation: none;
  }
`;

/**
 * Enter animations as style fragments, for any styled template:
 *
 *   const Panel = styled.div`
 *     ${minimalEnter.pop}
 *   `;
 *
 * Each plays once when the element is inserted, uses `backwards` fill so it
 * never holds a transform after it ends, stops for reduced motion (the media
 * query and the `reduced_motion` tier), and is switched off per element with
 * `data-minimal-enter="false"`. Re-keying an element replays it.
 */
export const minimalEnter = {
  fade: enter("minimal-enter-fade", "opacity: 0;", minimalVars.motion.standard),
  pop: enter("minimal-enter-pop", "opacity: 0; scale: 0.97;", minimalVars.motion.standard),
  slideUp: enter("minimal-enter-slide-up", "opacity: 0; translate: 0 var(--minimal-motion-offset, 12px);", minimalVars.motion.standard),
  page: enter("minimal-enter-page", "opacity: 0; translate: 0 var(--minimal-motion-offset, 12px);", minimalVars.motion.slow),
  tooltip: enter("minimal-enter-tooltip", "opacity: 0; scale: 0.96; translate: 0 4px;", minimalVars.motion.micro),
  /** Set `--minimal-motion-shift` on the element (e.g. `18px` or `-18px`). */
  slideX: enter("minimal-enter-slide-x", "opacity: 0; translate: var(--minimal-motion-shift, 0px) 0;", minimalVars.motion.micro),
} as const;
