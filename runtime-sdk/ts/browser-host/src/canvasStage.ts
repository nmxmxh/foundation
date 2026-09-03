/**
 * Standardized 2D/Canvas stage lifecycle manager.
 *
 * Enforces Foundation graphics and performance rules:
 * - Zero per-frame getBoundingClientRect() layout reads; uses ResizeObserver.
 * - Visibility culling using IntersectionObserver and visibilitychange.
 * - Cadence-gated execution synchronized with the shared frameClock.
 */

import { onFrame, type FrameSubscription, type Tick } from "./frameClock";

export interface CanvasStage {
  /**
   * Run draw callback on the shared clock at this stage's cadence.
   */
  start: (draw: (now: number) => void) => void;
  /**
   * Evaluates whether this frame should draw and returns the layout dimensions.
   * Returns null if offscreen, hidden, or throttled by cadence.
   */
  frame: (now: number) => StageFrame | null;
  /** Whether the stage is on-screen and visible. */
  readonly awake: boolean;
  /** Release observers and frame subscriptions. */
  dispose: () => void;
}

export interface StageFrame {
  /** Width in CSS pixels. */
  width: number;
  /** Height in CSS pixels. */
  height: number;
  /** Device pixel ratio applied to backing store. */
  ratio: number;
  /** Viewport X coordinate when position tracking is enabled. */
  left: number;
  /** Viewport Y coordinate when position tracking is enabled. */
  top: number;
}

export interface StageOptions {
  /** Milliseconds between draws. Defaults to 1000 / 30. */
  cadenceMs?: number;
  /** Device pixel ratio ceiling. Defaults to 1.5. */
  maxRatio?: number;
  /** Whether to report viewport bounding rect on each drawn frame. */
  position?: boolean;
  /** Callback fired when canvas transitions from hidden to visible. */
  wake?: () => void;
}

/**
 * Construct a CanvasStage bound to a canvas element and optional 2D context.
 */
export function createCanvasStage(
  canvas: HTMLCanvasElement,
  context: CanvasRenderingContext2D | null,
  options: StageOptions = {},
): CanvasStage {
  const cadenceMs = options.cadenceMs ?? 1000 / 30;
  const maxRatio = options.maxRatio ?? 1.5;

  let width = canvas.clientWidth || 0;
  let height = canvas.clientHeight || 0;
  let measured = width > 0 && height > 0;
  let onScreen = true;
  let lastDrawAt = 0;
  let subscription: FrameSubscription | null = null;
  let drawing: ((now: number) => void) | null = null;

  let sizeObserver: ResizeObserver | null = null;
  if (typeof ResizeObserver !== "undefined") {
    sizeObserver = new ResizeObserver((entries) => {
      for (const entry of entries) {
        const box = entry.contentBoxSize?.[0];
        if (box) {
          width = box.inlineSize;
          height = box.blockSize;
        } else {
          width = entry.contentRect.width;
          height = entry.contentRect.height;
        }
        measured = true;
      }
    });
    sizeObserver.observe(canvas);
  }

  let visibility: IntersectionObserver | null = null;
  if (typeof IntersectionObserver !== "undefined") {
    visibility = new IntersectionObserver(
      (entries) => {
        const was = onScreen;
        onScreen = entries.some((entry) => entry.isIntersecting);
        if (onScreen && !was) options.wake?.();
      },
      { threshold: 0 },
    );
    visibility.observe(canvas);
  }

  const onTabVisibility = () => {
    if (typeof document !== "undefined" && !document.hidden && onScreen) {
      options.wake?.();
    }
  };
  if (typeof document !== "undefined") {
    document.addEventListener("visibilitychange", onTabVisibility);
  }

  return {
    get awake() {
      const tabVisible = typeof document === "undefined" || !document.hidden;
      return onScreen && tabVisible;
    },

    start(draw) {
      drawing = draw;
      subscription?.release();
      subscription = onFrame((tick: Tick) => {
        const tabHidden = typeof document !== "undefined" && document.hidden;
        if (!onScreen || tabHidden) return;
        drawing?.(tick.now);
      }, cadenceMs);
    },

    frame(now: number): StageFrame | null {
      const tabHidden = typeof document !== "undefined" && document.hidden;
      if (!onScreen || tabHidden) return null;
      if (!subscription && now - lastDrawAt < cadenceMs) return null;
      lastDrawAt = now;

      if (!measured) {
        width = canvas.clientWidth || 0;
        height = canvas.clientHeight || 0;
        if (width > 0 && height > 0) measured = true;
      }
      if (width < 1 || height < 1) return null;

      const deviceRatio = typeof window !== "undefined" && window.devicePixelRatio ? window.devicePixelRatio : 1;
      const ratio = Math.min(deviceRatio, maxRatio);
      const backingWidth = Math.round(width * ratio);
      const backingHeight = Math.round(height * ratio);

      if (canvas.width !== backingWidth || canvas.height !== backingHeight) {
        canvas.width = backingWidth;
        canvas.height = backingHeight;
      }
      context?.setTransform(ratio, 0, 0, ratio, 0, 0);

      let left = 0;
      let top = 0;
      if (options.position && typeof canvas.getBoundingClientRect === "function") {
        const box = canvas.getBoundingClientRect();
        left = box.left;
        top = box.top;
      }

      return { width, height, ratio, left, top };
    },

    dispose() {
      subscription?.release();
      subscription = null;
      drawing = null;
      sizeObserver?.disconnect();
      visibility?.disconnect();
      if (typeof document !== "undefined") {
        document.removeEventListener("visibilitychange", onTabVisibility);
      }
    },
  };
}
