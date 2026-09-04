/**
 * Standardized 2D/Canvas stage lifecycle manager.
 *
 * Enforces Foundation graphics and performance rules:
 * - Zero per-frame layout reads. Size comes from a `ResizeObserver`, position
 *   from the `IntersectionObserver` entry and a passive scroll listener, and
 *   the viewport from a passive resize listener. `frame()` reads none of them.
 * - Visibility culling using IntersectionObserver and visibilitychange.
 * - Cadence-gated execution synchronized with the shared frameClock.
 *
 * ## Why position is cached rather than measured
 *
 * `position: true` used to call `getBoundingClientRect()` inside `frame()` —
 * after the backing store had already been resized, which invalidates layout,
 * so the read was a *forced synchronous* one. Three apparatuses on one page at
 * 30 Hz made that ninety forced reflows a second, each interleaved with canvas
 * mutation, and worst during a scroll when layout was dirty anyway. The module
 * docstring claimed the opposite of what the code did.
 *
 * Nothing about the position actually needs measuring per frame. It changes
 * when the element moves, when the page scrolls, and when the viewport
 * resizes — three events the browser will tell us about. The
 * `IntersectionObserver` entry even carries a `boundingClientRect` the browser
 * has already computed, so the common case costs nothing at all. The scroll
 * listener reads directly rather than deferring to `requestAnimationFrame`,
 * because a scroll event is dispatched at a point in the frame where layout is
 * clean; deferring it into rAF would land the read *after* another stage's
 * draw had dirtied layout again, reintroducing exactly the forced reflow this
 * is here to remove.
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

/**
 * The per-frame descriptor.
 *
 * **Reused between frames, not freshly allocated.** Read what you need inside
 * the draw; do not retain it, and do not hold a reference across frames
 * expecting it to keep this frame's values. `renderSurfaceClient` has always
 * worked this way for the worker half of the lane and the reason is the same
 * here: a fresh object per frame is 80 bytes of garbage per stage per frame,
 * and the front page this was written for runs three stages at 30 Hz — 7.2 KB
 * a second of allocation, on the main thread, to describe a rectangle that
 * changes only when the window does.
 */
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
  /**
   * Viewport size in CSS pixels, cached from a passive resize listener.
   *
   * Here so a draw that needs to place itself against the window — a depth
   * gradient, a parallax offset — does not reach for `window.innerWidth` on
   * every frame. Those reads are the same forced layout as a rect read, and a
   * draw that takes four of them per call at 30 Hz across three canvases is
   * paying for a number that changes only when the window does.
   */
  viewportWidth: number;
  viewportHeight: number;
}

export interface StageOptions {
  /** Milliseconds between draws. Defaults to 1000 / 30. */
  cadenceMs?: number;
  /** Device pixel ratio ceiling. Defaults to 1.5. */
  maxRatio?: number;
  /**
   * Whether to track the canvas's viewport position.
   *
   * Tracked from observers and a passive scroll listener, never measured
   * inside a frame. Off by default because the listener is only worth
   * attaching for a stage that actually places itself against the viewport.
   */
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
  const tracksPosition = options.position === true;

  let width = canvas.clientWidth || 0;
  let height = canvas.clientHeight || 0;
  let measured = width > 0 && height > 0;
  let onScreen = true;
  let lastDrawAt = 0;
  let subscription: FrameSubscription | null = null;
  let drawing: ((now: number) => void) | null = null;

  let left = 0;
  let top = 0;
  let viewportWidth = typeof window !== "undefined" ? window.innerWidth || 0 : 0;
  let viewportHeight = typeof window !== "undefined" ? window.innerHeight || 0 : 0;
  let deviceRatio = typeof window !== "undefined" && window.devicePixelRatio ? window.devicePixelRatio : 1;

  // One descriptor for the life of the stage. See `StageFrame`.
  const frameDescriptor: StageFrame = {
    width: 0,
    height: 0,
    ratio: 1,
    left: 0,
    top: 0,
    viewportWidth: 0,
    viewportHeight: 0,
  };

  /**
   * Measure the position, outside any frame.
   *
   * Called at construction and from events — never from `frame()`. At each of
   * those points layout is either clean or about to be recomputed anyway, so
   * the read is the cheap kind.
   */
  const readPosition = () => {
    if (!tracksPosition || typeof canvas.getBoundingClientRect !== "function") return;
    const box = canvas.getBoundingClientRect();
    left = box.left;
    top = box.top;
  };
  readPosition();

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
      // A resized canvas has usually moved as well; the observer callback runs
      // at a layout-clean point, so this costs what the entry already cost.
      readPosition();
    });
    sizeObserver.observe(canvas);
  }

  let visibility: IntersectionObserver | null = null;
  if (typeof IntersectionObserver !== "undefined") {
    visibility = new IntersectionObserver(
      (entries) => {
        const was = onScreen;
        onScreen = entries.some((entry) => entry.isIntersecting);
        if (tracksPosition) {
          // Free: the browser computed this rect to decide the intersection.
          const rect = entries[entries.length - 1]?.boundingClientRect;
          if (rect) {
            left = rect.left;
            top = rect.top;
          }
        }
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

  // Capturing, so a scroll in any ancestor scroller counts and not only the
  // document's own. Passive, so the listener can never block a scroll.
  const onScroll = () => readPosition();
  if (tracksPosition && typeof window !== "undefined") {
    window.addEventListener("scroll", onScroll, { passive: true, capture: true });
  }

  const onViewportResize = () => {
    if (typeof window === "undefined") return;
    viewportWidth = window.innerWidth || 0;
    viewportHeight = window.innerHeight || 0;
    deviceRatio = window.devicePixelRatio || 1;
    readPosition();
  };
  if (typeof window !== "undefined") {
    window.addEventListener("resize", onViewportResize, { passive: true });
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

      const ratio = Math.min(deviceRatio, maxRatio);
      const backingWidth = Math.round(width * ratio);
      const backingHeight = Math.round(height * ratio);

      if (canvas.width !== backingWidth || canvas.height !== backingHeight) {
        canvas.width = backingWidth;
        canvas.height = backingHeight;
      }
      context?.setTransform(ratio, 0, 0, ratio, 0, 0);

      frameDescriptor.width = width;
      frameDescriptor.height = height;
      frameDescriptor.ratio = ratio;
      frameDescriptor.left = left;
      frameDescriptor.top = top;
      frameDescriptor.viewportWidth = viewportWidth;
      frameDescriptor.viewportHeight = viewportHeight;
      return frameDescriptor;
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
      if (typeof window !== "undefined") {
        if (tracksPosition) {
          window.removeEventListener("scroll", onScroll, { capture: true } as EventListenerOptions);
        }
        window.removeEventListener("resize", onViewportResize);
      }
    },
  };
}
