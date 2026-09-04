/**
 * A cheap, synchronous prior on what this device can hold.
 *
 * ## Why a prior at all
 *
 * A quality ladder can only climb down from where it starts, and until now
 * every render surface started at rung zero — the largest backing store and
 * the longest inner loop — on every device that ever opened the page. A phone
 * therefore discovered it was a phone by janking: it drew a full-resolution
 * frame, missed its cadence, drew another, and kept going until the ladder had
 * counted enough misses to move. That window is seconds long and it lands on
 * first paint, which is the one moment a viewer is definitely looking.
 *
 * The fix is not a better measurement. It is not measuring at all: the browser
 * already knows the things that matter, and every one of them can be read
 * synchronously before the first frame. This module reads them and turns them
 * into an opening rung.
 *
 * ## Why display and compute are weighed separately
 *
 * The obvious implementation — count constraints, divide by the total — gets
 * flagship phones badly wrong. A current iPhone is coarse-pointer, small, and
 * DPR 3; it trips every display signal there is and it can hold a rung a
 * laptop would struggle with. Those three signals describe the *panel*, not
 * the silicon behind it, and treating them as evidence of weakness pins fast
 * hardware to the bottom of the ladder for the six seconds a promotion takes.
 *
 * So the signals are split. Display constraints — pointer, viewport, pixel
 * ratio — say the surface has more pixels to fill than a desktop and cost the
 * ladder *at most half* its span. Compute constraints — core count, memory —
 * are the ones that say the device is actually slow, and only they push past
 * the midpoint. A desktop trips neither and opens at rung zero, exactly as it
 * does today.
 *
 * ## Reading nothing is not the same as reading zero
 *
 * `deviceMemory` is Chromium-only; `matchMedia` does not exist in a worker at
 * all. An absent signal is excluded from its group's numerator *and*
 * denominator rather than being scored as "unconstrained", because a Safari
 * user is not a fast user — they are an unmeasured one, and inferring capacity
 * from a missing API is how a heuristic becomes a browser-sniff.
 *
 * `gpu_practices.md`, *Device profile and starting tier*.
 */

/** What could be read about this device. `null` means the signal was absent. */
export type DeviceProfile = {
  /** Touch-first input — `(pointer: coarse)`. */
  coarsePointer: boolean | null;
  /** Phone-width viewport — `(max-width: 48rem)`. */
  smallViewport: boolean | null;
  /** `devicePixelRatio >= 2.5`: phone-class panel, disproportionate fill cost. */
  denseDisplay: boolean | null;
  /** `hardwareConcurrency <= 4`. */
  lowConcurrency: boolean | null;
  /** `deviceMemory <= 4`. Chromium only. */
  lowMemory: boolean | null;
  /** `prefers-reduced-motion: reduce`. */
  reducedMotion: boolean | null;
};

type NavigatorHints = Navigator & {
  deviceMemory?: number;
  hardwareConcurrency?: number;
};

/**
 * Ask a media query, or say we could not.
 *
 * Returns `null` rather than `false` where `matchMedia` is missing — a worker,
 * a test harness, a server render — so the caller can tell "no" from "not
 * asked". See the module note on absent signals.
 */
const askMedia = (query: string): boolean | null => {
  const ask = (globalThis as { matchMedia?: (q: string) => { matches: boolean } }).matchMedia;
  if (typeof ask !== "function") return null;
  try {
    return ask.call(globalThis, query).matches;
  } catch {
    return null;
  }
};

/** Read the prior. Synchronous, allocation-light, safe to call before paint. */
export const readDeviceProfile = (): DeviceProfile => {
  const nav = (globalThis as { navigator?: NavigatorHints }).navigator;
  const cores = nav?.hardwareConcurrency;
  const memory = nav?.deviceMemory;
  const ratio = (globalThis as { devicePixelRatio?: number }).devicePixelRatio;

  return {
    coarsePointer: askMedia("(pointer: coarse)"),
    smallViewport: askMedia("(max-width: 48rem)"),
    denseDisplay: typeof ratio === "number" && Number.isFinite(ratio) ? ratio >= 2.5 : null,
    lowConcurrency: typeof cores === "number" && cores > 0 ? cores <= 4 : null,
    lowMemory: typeof memory === "number" && memory > 0 ? memory <= 4 : null,
    reducedMotion: askMedia("(prefers-reduced-motion: reduce)"),
  };
};

/** Fraction of the readable signals in a group that fired. `0` when none were. */
const load = (signals: readonly (boolean | null)[]): number => {
  let read = 0;
  let fired = 0;
  for (const signal of signals) {
    if (signal === null) continue;
    read += 1;
    if (signal) fired += 1;
  }
  return read === 0 ? 0 : fired / read;
};

/**
 * The opening rung, given a ladder of `tierCount` rungs (best first).
 *
 * Never returns the best rung for a device that tripped a constraint, and
 * never returns worse than the ladder has. A device that reports nothing at
 * all — the SSR case, a stripped test global — opens at rung zero, which is
 * the behaviour before this module existed.
 */
export const startingTier = (profile: DeviceProfile, tierCount: number): number => {
  const span = tierCount - 1;
  if (!Number.isFinite(span) || span <= 0) return 0;

  // Asked for less motion, given less work. This is the one signal that is a
  // stated preference rather than an inference, so it is not blended with the
  // others — it goes straight to the cheapest rung.
  if (profile.reducedMotion === true) return span;

  const display = load([profile.coarsePointer, profile.smallViewport, profile.denseDisplay]);
  const compute = load([profile.lowConcurrency, profile.lowMemory]);

  const midpoint = span / 2;
  const tier = display * midpoint + compute * (span - midpoint);
  return Math.min(span, Math.max(0, Math.round(tier)));
};

/** Read the device and pick an opening rung in one call. */
export const startingTierForDevice = (tierCount: number): number =>
  startingTier(readDeviceProfile(), tierCount);
