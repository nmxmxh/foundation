/**
 * Quality tiers for DOM/CSS effects (ui_render_performance_research.md, P2).
 *
 * The render surface ladder protects canvas frames by lowering detail before a
 * frame is missed; CSS effects had nothing. This picks a tier, writes it to
 * `data-ui-tier` on the root element, and moves it with the same shape of
 * hysteresis as the surface ladder. Stylesheets select cheap or rich effects by
 * attribute — with ui-minimal's CSS-variable tokens that is a token swap on
 * `:root`, not a change to any component.
 *
 * ## What it listens to, and why not the median
 *
 * Measured on a WebView (2026-09-14): while the app's render thread drew at
 * ~10 fps (gfxinfo p50 93–150 ms), `requestAnimationFrame` intervals on the
 * page still read p50 17 ms — the renderer main thread kept its cadence and the
 * compositor fell behind. The *share* of slow frames did move: 22–34% against
 * ~5% on a plain list in the same WebView. So a window counts against the tier
 * by its slow-frame share, never by its median. Inside native shells the
 * authoritative numbers come from the platform (P8); they enter through the same
 * `recordWindow`.
 *
 * ## Hysteresis, translated from frames to windows
 *
 * The surface ladder counts frames: miss after a proportional overrun, demote
 * after a burst, promote only after a long clean run, forgive a tally after a
 * sustained clean stretch. DOM jank arrives in scroll-sized bursts rather than
 * steady cadence misses, so the unit here is a measured window (a scroll, a
 * route change): demote after 2 bad windows, forgive after 3 clean ones,
 * promote after 8. Tiers never flap on one hitch.
 *
 * ## What a tier may change
 *
 * Detail only — blur, shadow, motion density, list overscan, image resolution.
 * Never data, auth, or commands (P2 invariant 2).
 */

import { readDeviceProfile, startingTier, type DeviceProfile } from "./deviceProfile";
import type { FrameStats } from "./frameTelemetry";
import { markLane } from "./renderMarks";

export type UiTier = "high" | "balanced" | "low_power" | "reduced_motion";

/** The measured ladder, best first. `reduced_motion` is a preference, not a rung. */
export const UI_TIER_LADDER = ["high", "balanced", "low_power"] as const;
export type UiLadderTier = (typeof UI_TIER_LADDER)[number];

export const UI_TIER_ATTRIBUTE = "data-ui-tier";
export const UI_TIER_PASS = "ovasabi.render.uiTier";

/** A window with more than this share of slow frames counts against the tier. */
export const SLOW_WINDOW_SHARE = 0.15;
/** Windows shorter than this are too small to judge. */
export const MIN_WINDOW_FRAMES = 30;
export const DEMOTE_AFTER_WINDOWS = 2;
export const FORGIVE_AFTER_WINDOWS = 3;
export const PROMOTE_AFTER_WINDOWS = 8;

export type UiQualityWindow = Pick<FrameStats, "frames" | "slowFrames" | "degraded">;

export interface UiQualityOptions {
  /** Device prior; defaults to reading the current device. */
  profile?: DeviceProfile;
  /** Save-Data; defaults to `navigator.connection.saveData` where exposed. */
  saveData?: boolean | null;
  /** Element that receives `data-ui-tier`; defaults to `document.documentElement`. */
  target?: { setAttribute(name: string, value: string): void } | null;
  /** Record tier changes through renderMarks. Defaults to true. */
  mark?: boolean;
}

export interface UiQuality {
  tier(): UiTier;
  /** Feed one measured window: a P1 session's stats, or a native frame report. */
  recordWindow(window: UiQualityWindow): UiTier;
  /**
   * Hold the tier at or below a rung regardless of frames — for signals that
   * say "spend less" before frames do (thermal headroom, low-power mode, P8).
   * Pass "high" to release.
   */
  setFloor(floor: UiLadderTier, reason: string): UiTier;
  subscribe(listener: (tier: UiTier, reason: string) => void): () => void;
  dispose(): void;
}

const readSaveData = (): boolean | null => {
  const connection = (globalThis as { navigator?: { connection?: { saveData?: boolean } } }).navigator?.connection;
  return typeof connection?.saveData === "boolean" ? connection.saveData : null;
};

const defaultTarget = (): UiQualityOptions["target"] =>
  typeof document !== "undefined" ? document.documentElement : null;

export function createUiQuality(options: UiQualityOptions = {}): UiQuality {
  const profile = options.profile ?? readDeviceProfile();
  const saveData = options.saveData !== undefined ? options.saveData : readSaveData();
  const target = options.target !== undefined ? options.target : defaultTarget();
  const reducedMotion = profile.reducedMotion === true;

  const lastRung = UI_TIER_LADDER.length - 1;
  let rung = startingTier({ ...profile, reducedMotion: null }, UI_TIER_LADDER.length);
  let startReason = "device prior";
  if (saveData === true && rung < 1) {
    rung = 1;
    startReason = "device prior, save-data";
  }
  let floor = 0;
  let over = 0;
  let under = 0;
  let disposed = false;
  const listeners = new Set<(tier: UiTier, reason: string) => void>();

  const current = (): UiTier => (reducedMotion ? "reduced_motion" : UI_TIER_LADDER[Math.max(rung, floor)]);

  const publish = (reason: string) => {
    const tier = current();
    target?.setAttribute(UI_TIER_ATTRIBUTE, tier);
    if (options.mark !== false) {
      markLane(UI_TIER_PASS, {
        lane: "dom",
        tier: reducedMotion ? lastRung : Math.max(rung, floor),
        fallback: false,
        reason: `${tier}: ${reason}`,
      });
    }
    for (const listener of listeners) listener(tier, reason);
  };

  publish(reducedMotion ? "prefers-reduced-motion" : startReason);

  return {
    tier: current,
    recordWindow(window) {
      if (disposed || reducedMotion || window.degraded || window.frames < MIN_WINDOW_FRAMES) return current();
      const share = window.slowFrames / window.frames;
      if (share > SLOW_WINDOW_SHARE) {
        over += 1;
        under = 0;
      } else {
        under += 1;
        if (under >= FORGIVE_AFTER_WINDOWS) over = 0;
      }
      const before = current();
      if (over >= DEMOTE_AFTER_WINDOWS && rung < lastRung) {
        rung += 1;
        over = 0;
        under = 0;
        if (current() !== before) publish(`demote: ${Math.round(share * 100)}% slow frames`);
      } else if (under >= PROMOTE_AFTER_WINDOWS && rung > 0) {
        rung -= 1;
        over = 0;
        under = 0;
        if (current() !== before) publish(`promote: ${PROMOTE_AFTER_WINDOWS} clean windows`);
      }
      return current();
    },
    setFloor(next, reason) {
      if (disposed) return current();
      const before = current();
      floor = UI_TIER_LADDER.indexOf(next);
      if (current() !== before) publish(`floor ${next}: ${reason}`);
      return current();
    },
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    dispose() {
      disposed = true;
      listeners.clear();
    },
  };
}
