import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { _resetFrameClockForTesting, frameClockMode, onFrame } from "./frameClock";

describe("unified frame clock", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    _resetFrameClockForTesting();
  });

  afterEach(() => {
    _resetFrameClockForTesting();
    vi.useRealTimers();
  });

  it("subscribes and receives scheduled frames", async () => {
    const callback = vi.fn();
    const subscription = onFrame(callback, 25);

    expect(frameClockMode().subscribers).toBe(1);

    await vi.advanceTimersByTimeAsync(100);
    expect(callback).toHaveBeenCalled();
    const tick = callback.mock.calls[0]?.[0];
    expect(tick).toHaveProperty("now");
    expect(tick).toHaveProperty("delta");

    subscription.release();
    expect(frameClockMode().subscribers).toBe(0);
  });

  it("isolates errors thrown by faulty subscribers", async () => {
    const errorCallback = vi.fn(() => {
      throw new Error("Draw crash");
    });
    const goodCallback = vi.fn();

    const sub1 = onFrame(errorCallback, 25);
    const sub2 = onFrame(goodCallback, 25);

    await vi.advanceTimersByTimeAsync(60);

    expect(errorCallback).toHaveBeenCalled();
    expect(goodCallback).toHaveBeenCalled();

    sub1.release();
    sub2.release();
  });

  it("allows dynamic cadence changes", async () => {
    const callback = vi.fn();
    const subscription = onFrame(callback, 50);

    await vi.advanceTimersByTimeAsync(100);
    const initialCalls = callback.mock.calls.length;

    subscription.setCadence(10);
    await vi.advanceTimersByTimeAsync(100);
    const fastCalls = callback.mock.calls.length - initialCalls;

    expect(fastCalls).toBeGreaterThanOrEqual(initialCalls);
    subscription.release();
  });
});
