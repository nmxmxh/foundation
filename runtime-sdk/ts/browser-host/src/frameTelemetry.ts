/**
 * Frame evidence for the DOM lane (ui_render_performance_research.md, P1).
 *
 * The canvas lane measures itself; a DOM screen was blind. This records the
 * interval between displayed frames for a bounded window — a scroll, a route
 * change, a sheet opening — and reports where it landed.
 *
 * ## Why it runs its own animation-frame loop
 *
 * `frameClock` is the one scheduler for work, and work must not add loops. But
 * it cannot also be the instrument: its ticks come from the pulse worker, so
 * the gap between two of its callbacks is pulse timing plus frame timing, and a
 * tick that drifts past a vsync reports a skipped frame the viewer never saw.
 * It is also capped at its target rate, so a 90 or 120 Hz display is invisible
 * through it. Measurement therefore reads `requestAnimationFrame` timestamps
 * directly, and only inside a window the caller opens and closes. A session
 * does no work of its own per frame beyond writing one number.
 *
 * ## Invariants
 *
 * 1. Visible time only. Browsers stop animation frames in a hidden document, so
 *    the gap across a hide is not a frozen frame; the first frame after the page
 *    returns starts a new interval instead of closing an old one.
 * 2. Slow is relative to the display. The expected interval is estimated from
 *    the session's own fastest frames, so "slow" means "spanned more than one
 *    vsync" at 60, 90, or 120 Hz alike.
 * 3. Frozen matches Android vitals (> 700 ms), so web, WebView, and native
 *    evidence line up.
 * 4. Bounded memory: a ring of the most recent intervals, allocated once.
 * 5. A report that could not measure says so (`degraded`) rather than reporting
 *    a clean run.
 */

import { markLane } from "./renderMarks";

export const FRAME_TELEMETRY_PASS = "ovasabi.render.frame";

/** Android vitals' frozen-frame threshold. */
export const FROZEN_FRAME_MS = 700;

export interface FrameStats {
  scope: string;
  /** Frame intervals recorded (visible time only). */
  frames: number;
  p50Ms: number;
  p95Ms: number;
  p99Ms: number;
  maxMs: number;
  /** Estimated display interval, from the fastest tenth of frames. */
  displayIntervalMs: number;
  /** Frames that spanned more than one display interval (a missed vsync). */
  slowFrames: number;
  /** Frames longer than FROZEN_FRAME_MS. */
  frozenFrames: number;
  /** Long animation frames observed during the window; null where unsupported. */
  longAnimationFrames: number | null;
  /** Summed blocking duration of those frames, in ms; null where unsupported. */
  longAnimationBlockingMs: number | null;
  /** Visible time the window covered, in ms. */
  visibleMs: number;
  /** True when frames could not be observed; exclude from gates. */
  degraded: boolean;
}

export interface FrameTelemetrySession {
  /** Stats so far; the session keeps recording. */
  snapshot(): FrameStats;
  /** Stop recording and return the final stats. Idempotent. */
  stop(): FrameStats;
}

interface VisibilitySource {
  visibilityState: string;
  addEventListener(type: "visibilitychange", listener: () => void): void;
  removeEventListener(type: "visibilitychange", listener: () => void): void;
}

interface LongFrameEntry {
  duration: number;
  blockingDuration?: number;
}

export interface FrameTelemetryOptions {
  /** Most recent intervals kept. Defaults to 1200 (about 20 s at 60 Hz). */
  capacity?: number;
  /** Publish the final stats through renderMarks on stop. Defaults to true. */
  mark?: boolean;
  /** Test seams; default to the browser globals. */
  requestFrame?: (callback: (timestamp: number) => void) => number;
  cancelFrame?: (handle: number) => void;
  visibility?: VisibilitySource | null;
  observeLongFrames?: ((onEntries: (entries: readonly LongFrameEntry[]) => void) => () => void) | null;
}

const defaultObserveLongFrames = (): FrameTelemetryOptions["observeLongFrames"] => {
  const Observer = (globalThis as { PerformanceObserver?: typeof PerformanceObserver }).PerformanceObserver;
  if (!Observer || !Observer.supportedEntryTypes?.includes("long-animation-frame")) return null;
  return (onEntries) => {
    const observer = new Observer((list) => onEntries(list.getEntries() as unknown as LongFrameEntry[]));
    observer.observe({ type: "long-animation-frame", buffered: false });
    return () => observer.disconnect();
  };
};

/** Nearest-rank percentile over an ascending array. */
const percentile = (sorted: Float64Array, p: number): number => {
  if (sorted.length === 0) return 0;
  const rank = Math.min(sorted.length - 1, Math.max(0, Math.ceil((p / 100) * sorted.length) - 1));
  return sorted[rank];
};

const round = (value: number): number => Math.round(value * 10) / 10;

export function startFrameTelemetry(scope: string, options: FrameTelemetryOptions = {}): FrameTelemetrySession {
  const capacity = Math.max(8, Math.floor(options.capacity ?? 1200));
  const raf =
    options.requestFrame ??
    (typeof requestAnimationFrame === "function" ? (cb: (t: number) => void) => requestAnimationFrame(cb) : undefined);
  const caf = options.cancelFrame ?? (typeof cancelAnimationFrame === "function" ? cancelAnimationFrame : undefined);
  const visibility =
    options.visibility !== undefined
      ? options.visibility
      : typeof document !== "undefined"
        ? (document as unknown as VisibilitySource)
        : null;
  const observeLongFrames =
    options.observeLongFrames !== undefined ? options.observeLongFrames : defaultObserveLongFrames();

  const ring = new Float64Array(capacity);
  let written = 0;
  let last: number | null = null;
  let visibleMs = 0;
  let handle: number | null = null;
  let stopped = false;
  let final: FrameStats | null = null;
  let longFrames = 0;
  let longBlocking = 0;

  const isVisible = () => !visibility || visibility.visibilityState === "visible";

  const onFrame = (timestamp: number) => {
    handle = null;
    if (stopped) return;
    if (isVisible()) {
      if (last !== null) {
        const interval = timestamp - last;
        if (interval > 0) {
          ring[written % capacity] = interval;
          written += 1;
          visibleMs += interval;
        }
      }
      last = timestamp;
    } else {
      last = null;
    }
    handle = raf ? raf(onFrame) : null;
  };

  const onVisibility = () => {
    // Hidden time is not frame time: drop the open interval either way.
    last = null;
    if (!stopped && raf && handle === null && isVisible()) handle = raf(onFrame);
  };

  const disconnectLongFrames = observeLongFrames
    ? observeLongFrames((entries) => {
        if (stopped) return;
        for (const entry of entries) {
          longFrames += 1;
          longBlocking += entry.blockingDuration ?? 0;
        }
      })
    : null;

  visibility?.addEventListener("visibilitychange", onVisibility);
  if (raf) handle = raf(onFrame);

  const compute = (): FrameStats => {
    const count = Math.min(written, capacity);
    const sorted = ring.slice(0, count).sort();
    const tenth = sorted.slice(0, Math.max(1, Math.floor(count / 10)));
    let tenthSum = 0;
    for (const v of tenth) tenthSum += v;
    const displayIntervalMs = count > 0 ? tenthSum / tenth.length : 0;
    let slowFrames = 0;
    let frozenFrames = 0;
    for (const v of sorted) {
      if (displayIntervalMs > 0 && v > displayIntervalMs * 1.5) slowFrames += 1;
      if (v > FROZEN_FRAME_MS) frozenFrames += 1;
    }
    return {
      scope,
      frames: count,
      p50Ms: round(percentile(sorted, 50)),
      p95Ms: round(percentile(sorted, 95)),
      p99Ms: round(percentile(sorted, 99)),
      maxMs: round(count > 0 ? sorted[count - 1] : 0),
      displayIntervalMs: round(displayIntervalMs),
      slowFrames,
      frozenFrames,
      longAnimationFrames: observeLongFrames ? longFrames : null,
      longAnimationBlockingMs: observeLongFrames ? round(longBlocking) : null,
      visibleMs: round(visibleMs),
      degraded: !raf || count === 0,
    };
  };

  return {
    snapshot: () => final ?? compute(),
    stop() {
      if (final) return final;
      stopped = true;
      if (handle !== null && caf) caf(handle);
      handle = null;
      visibility?.removeEventListener("visibilitychange", onVisibility);
      disconnectLongFrames?.();
      final = compute();
      if (options.mark !== false) {
        markLane(`${FRAME_TELEMETRY_PASS}.${scope}`, {
          lane: "dom",
          cadence: final.displayIntervalMs > 0 ? Math.round(1000 / final.displayIntervalMs) : undefined,
          fallback: final.degraded,
          reason: `p50=${final.p50Ms} p95=${final.p95Ms} slow=${final.slowFrames}/${final.frames} frozen=${final.frozenFrames}`,
        });
      }
      return final;
    },
  };
}
