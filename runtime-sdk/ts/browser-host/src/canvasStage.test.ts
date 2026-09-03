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
