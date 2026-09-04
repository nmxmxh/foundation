import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createCanvasStage } from "./canvasStage";
import { _resetFrameClockForTesting } from "./frameClock";

describe("canvas stage", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    _resetFrameClockForTesting();
  });

  afterEach(() => {
    _resetFrameClockForTesting();
    vi.useRealTimers();
  });

  it("sizes the backing store and coordinates without forced layouts", () => {
    const canvas = {
      clientWidth: 300,
      clientHeight: 200,
      width: 0,
      height: 0,
      getBoundingClientRect: vi.fn(() => ({ left: 50, top: 80, width: 300, height: 200 })),
    } as unknown as HTMLCanvasElement;

    const ctx = {
      setTransform: vi.fn(),
    } as unknown as CanvasRenderingContext2D;

    const stage = createCanvasStage(canvas, ctx, {
      cadenceMs: 25,
      maxRatio: 1.5,
      position: true,
    });

    const frame = stage.frame(100);
    expect(frame).not.toBeNull();
    expect(frame?.width).toBe(300);
    expect(frame?.height).toBe(200);
    expect(frame?.left).toBe(50);
    expect(frame?.top).toBe(80);
    expect(canvas.width).toBe(Math.round(300 * frame!.ratio));
    expect(ctx.setTransform).toHaveBeenCalledWith(frame!.ratio, 0, 0, frame!.ratio, 0, 0);

    // Immediate second call within cadence interval returns null (gated)
    const gated = stage.frame(110);
    expect(gated).toBeNull();

    stage.dispose();
  });

  /*
   * The rule the module's own docstring always claimed and the code did not
   * keep. `position: true` used to call `getBoundingClientRect()` inside
   * `frame()`, after the backing store had been resized — so it was a *forced*
   * synchronous layout, three of them per frame across the apparatuses on one
   * page, at their worst during a scroll when layout was already dirty.
   */
  it("never measures layout inside a frame, however many frames it draws", () => {
    const rect = vi.fn(() => ({ left: 50, top: 80, width: 300, height: 200 }));
    const canvas = {
      clientWidth: 300,
      clientHeight: 200,
      width: 0,
      height: 0,
      getBoundingClientRect: rect,
    } as unknown as HTMLCanvasElement;

    const stage = createCanvasStage(canvas, null, { cadenceMs: 0, position: true });
    // One read at construction, so the first frame has a position to report.
    expect(rect).toHaveBeenCalledTimes(1);
    rect.mockClear();

    for (let i = 0; i < 60; i += 1) {
      const frame = stage.frame(i * 16);
      expect(frame?.left).toBe(50);
      expect(frame?.top).toBe(80);
    }
    expect(rect).not.toHaveBeenCalled();

    stage.dispose();
  });

  it("tracks position from events, and only for a stage that asked for it", () => {
    const listen = vi.fn();
    vi.stubGlobal("window", {
      innerWidth: 1440,
      innerHeight: 900,
      devicePixelRatio: 2,
      addEventListener: listen,
      removeEventListener: vi.fn(),
    });

    const makeCanvas = () =>
      ({
        clientWidth: 100,
        clientHeight: 100,
        width: 0,
        height: 0,
        getBoundingClientRect: vi.fn(() => ({ left: 12, top: 34, width: 100, height: 100 })),
      }) as unknown as HTMLCanvasElement;

    const plain = makeCanvas();
    const plainStage = createCanvasStage(plain, null, {});
    // No position tracking asked for: no listener, and layout never touched.
    expect(listen.mock.calls.some(([type]) => type === "scroll")).toBe(false);
    expect(plain.getBoundingClientRect).not.toHaveBeenCalled();
    plainStage.dispose();

    listen.mockClear();
    const tracked = makeCanvas();
    const trackedStage = createCanvasStage(tracked, null, { cadenceMs: 0, position: true });
    const scroll = listen.mock.calls.find(([type]) => type === "scroll");
    expect(scroll?.[2]).toMatchObject({ passive: true, capture: true });

    // The viewport arrives on the frame, so a draw placing itself against the
    // window has no reason to read `window.innerWidth` itself.
    const frame = trackedStage.frame(0);
    expect(frame).toMatchObject({ left: 12, top: 34, viewportWidth: 1440, viewportHeight: 900 });

    // A scroll refreshes it, outside the frame, where layout is still clean.
    (tracked.getBoundingClientRect as ReturnType<typeof vi.fn>).mockReturnValue({
      left: 12,
      top: -220,
      width: 100,
      height: 100,
    });
    (scroll?.[1] as () => void)();
    expect(trackedStage.frame(1000)).toMatchObject({ top: -220 });

    trackedStage.dispose();
    vi.unstubAllGlobals();
  });

  it("runs draw on clock start", async () => {
    const canvas = {
      clientWidth: 100,
      clientHeight: 100,
      width: 100,
      height: 100,
    } as unknown as HTMLCanvasElement;

    const stage = createCanvasStage(canvas, null, { cadenceMs: 20 });
    const draw = vi.fn();
    stage.start(draw);

    await vi.advanceTimersByTimeAsync(80);
    expect(draw).toHaveBeenCalled();

    stage.dispose();
  });
});

/**
 * The observer paths, which the node test environment otherwise skips whole.
 *
 * `ResizeObserver`, `IntersectionObserver`, `window` and `document` are all
 * absent under vitest's node environment, so every branch that constructs or
 * feeds one was unexecuted — including the three this stage's whole position
 * contract rests on. A fix whose callbacks are never run is a fix on paper.
 */
describe("canvas stage observers", () => {
  type Observed = { callback: (entries: unknown[]) => void; disconnect: ReturnType<typeof vi.fn> };
  let sizeObservers: Observed[] = [];
  let visibilityObservers: Observed[] = [];
  let windowListeners: Map<string, (event?: unknown) => void>;
  let documentListeners: Map<string, () => void>;

  beforeEach(() => {
    sizeObservers = [];
    visibilityObservers = [];
    windowListeners = new Map();
    documentListeners = new Map();
    _resetFrameClockForTesting();

    const record = (into: Observed[]) =>
      class {
        disconnect = vi.fn();
        constructor(public callback: (entries: unknown[]) => void) {
          into.push(this as unknown as Observed);
        }
        observe() {}
      };

    vi.stubGlobal("ResizeObserver", record(sizeObservers));
    vi.stubGlobal("IntersectionObserver", record(visibilityObservers));
    vi.stubGlobal("window", {
      innerWidth: 1024,
      innerHeight: 768,
      devicePixelRatio: 2,
      addEventListener: (type: string, handler: (event?: unknown) => void) =>
        windowListeners.set(type, handler),
      removeEventListener: (type: string) => windowListeners.delete(type),
    });
    vi.stubGlobal("document", {
      hidden: false,
      addEventListener: (type: string, handler: () => void) => documentListeners.set(type, handler),
      removeEventListener: (type: string) => documentListeners.delete(type),
    });
  });

  afterEach(() => vi.unstubAllGlobals());

  const makeCanvas = (rect = { left: 10, top: 20, width: 300, height: 200 }) =>
    ({
      clientWidth: 300,
      clientHeight: 200,
      width: 0,
      height: 0,
      getBoundingClientRect: vi.fn(() => rect),
    }) as unknown as HTMLCanvasElement;

  it("takes size from the resize observer and position from the entry it already had", () => {
    const canvas = makeCanvas();
    const stage = createCanvasStage(canvas, null, { cadenceMs: 0, position: true });

    // Size arrives on the modern `contentBoxSize` path.
    sizeObservers[0]!.callback([{ contentBoxSize: [{ inlineSize: 640, blockSize: 480 }] }]);
    expect(stage.frame(0)).toMatchObject({ width: 640, height: 480 });

    // And on the legacy `contentRect` fallback.
    sizeObservers[0]!.callback([{ contentRect: { width: 320, height: 240 } }]);
    expect(stage.frame(100)).toMatchObject({ width: 320, height: 240 });

    /*
     * The intersection entry carries a rect the browser computed to decide the
     * intersection. Taking it costs nothing; measuring it again would be the
     * forced layout this stage exists to avoid.
     */
    visibilityObservers[0]!.callback([
      { isIntersecting: true, boundingClientRect: { left: 77, top: -14 } },
    ]);
    expect(stage.frame(200)).toMatchObject({ left: 77, top: -14 });

    stage.dispose();
    expect(sizeObservers[0]!.disconnect).toHaveBeenCalled();
    expect(visibilityObservers[0]!.disconnect).toHaveBeenCalled();
  });

  it("sleeps when nothing can see it and wakes when something can", () => {
    const wake = vi.fn();
    const stage = createCanvasStage(makeCanvas(), null, { cadenceMs: 0, wake });

    visibilityObservers[0]!.callback([{ isIntersecting: false }]);
    expect(stage.awake).toBe(false);
    expect(stage.frame(0)).toBeNull();

    visibilityObservers[0]!.callback([{ isIntersecting: true }]);
    expect(stage.awake).toBe(true);
    expect(wake).toHaveBeenCalledTimes(1);

    // A hidden tab is not a slower tab; it is a tab that must not draw at all.
    (globalThis as unknown as { document: { hidden: boolean } }).document.hidden = true;
    expect(stage.awake).toBe(false);
    expect(stage.frame(100)).toBeNull();

    (globalThis as unknown as { document: { hidden: boolean } }).document.hidden = false;
    documentListeners.get("visibilitychange")?.();
    expect(wake).toHaveBeenCalledTimes(2);

    stage.dispose();
  });

  it("refreshes the viewport and the pixel ratio on resize, not on every frame", () => {
    const canvas = makeCanvas();
    const stage = createCanvasStage(canvas, null, { cadenceMs: 0, position: true, maxRatio: 3 });

    expect(stage.frame(0)).toMatchObject({ viewportWidth: 1024, viewportHeight: 768, ratio: 2 });

    const win = globalThis as unknown as {
      window: { innerWidth: number; innerHeight: number; devicePixelRatio: number };
    };
    win.window.innerWidth = 390;
    win.window.innerHeight = 844;
    win.window.devicePixelRatio = 3;
    windowListeners.get("resize")?.();

    expect(stage.frame(100)).toMatchObject({ viewportWidth: 390, viewportHeight: 844, ratio: 3 });

    stage.dispose();
    // Both listeners released: a stage that keeps a scroll handler alive after
    // unmount keeps the canvas alive with it.
    expect(windowListeners.has("resize")).toBe(false);
    expect(windowListeners.has("scroll")).toBe(false);
  });

  it("only draws through the shared clock while it is on screen", async () => {
    vi.useFakeTimers();
    const stage = createCanvasStage(makeCanvas(), null, { cadenceMs: 20 });
    const draw = vi.fn();
    stage.start(draw);

    visibilityObservers[0]!.callback([{ isIntersecting: false }]);
    await vi.advanceTimersByTimeAsync(100);
    expect(draw).not.toHaveBeenCalled();

    visibilityObservers[0]!.callback([{ isIntersecting: true }]);
    await vi.advanceTimersByTimeAsync(100);
    expect(draw).toHaveBeenCalled();

    stage.dispose();
    vi.useRealTimers();
  });
});
