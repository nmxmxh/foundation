import { useSyncExternalStore } from "react";


/*
 * Motion for ui-minimal, on the platform rather than on a JavaScript runtime.
 *
 * This module used to build framer-motion `Variants` and `Transition` objects.
 * The frontend lab measured what those cost (research doc section 14): framer-
 * motion as ui-minimal imported it was 47.6 KB gzip — more than React and
 * ReactDOM together — it animated `y`, `scale`, `height: auto` and springs by
 * writing style from JavaScript on every frame (49–61 writes per animation,
 * against zero for the CSS equivalent), and a `motion.*` card mounted 3.9×
 * slower cold than the same card with a CSS fade.
 *
 * So motion is CSS. Enter animations are `@keyframes` on insertion, which play
 * in every engine the shells target (the iOS shells run back to iOS 14): no
 * `@starting-style` requirement and no JavaScript presence tracking. Keyframes
 * animate `opacity` and the individual `translate` / `scale` properties — never
 * `transform`, which components use for their own positioning (a centred
 * modal, a placed tooltip) and an animation would override. On an engine
 * without individual transform properties the movement is dropped and the fade
 * remains, which is the fallback P3 asks for.
 *
 * The fragments are plain strings, extracted at build time (Linaria, research
 * doc 14.7). Each carries its own `@keyframes`: Linaria scopes keyframe names to
 * the component that declares them, so a keyframe declared once globally and
 * referenced by name elsewhere would not resolve. The repetition is a few dozen
 * bytes per component that animates.
 */

export { minimalEnter, minimalMotionMs } from "./motionStyles";
import { minimalMotionMs } from "./motionStyles";

const REDUCED_MOTION_QUERY = "(prefers-reduced-motion: reduce)";

const subscribeReducedMotion = (onChange: () => void) => {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") return () => undefined;
  const query = window.matchMedia(REDUCED_MOTION_QUERY);
  query.addEventListener?.("change", onChange);
  return () => query.removeEventListener?.("change", onChange);
};

const readReducedMotion = () =>
  typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia(REDUCED_MOTION_QUERY).matches;

/** Whether the user asked for reduced motion. Live, and `false` on the server. */
export const useMinimalReducedMotion = (): boolean =>
  useSyncExternalStore(subscribeReducedMotion, readReducedMotion, () => false);

/**
 * Motion facts for components that decide something in script — whether to
 * wait for an exit, how long to delay. Styling itself should use
 * `minimalEnter` and the `minimalVars.motion` tokens.
 */
export const useMinimalMotion = () => {
  const reducedMotion = useMinimalReducedMotion();
  return { reducedMotion, ms: minimalMotionMs } as const;
};
