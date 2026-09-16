import { describe, expect, it } from "vitest";
import { FROZEN_FRAME_MS, startFrameTelemetry, type FrameTelemetryOptions } from "./frameTelemetry";
import { renderFacts } from "./renderMarks";

/** A manually driven animation-frame source and visibility state. */
const harness = () => {
  let pending: ((t: number) => void) | null = null;
  let now = 0;
  let state = "visible";
  const listeners = new Set<() => void>();
  let cancelled = 0;
  let longFrameSink: ((entries: readonly { duration: number; blockingDuration?: number }[]) => void) | null = null;
  const options: FrameTelemetryOptions = {
    mark: false,
    requestFrame: (cb) => {
      pending = cb;
      return 1;
    },
    cancelFrame: () => {
      cancelled += 1;
      pending = null;
    },
    visibility: {
      get visibilityState() {
        return state;
      },
      addEventListener: (_type, l) => listeners.add(l),
      removeEventListener: (_type, l) => listeners.delete(l),
    },
    observeLongFrames: (sink) => {
      longFrameSink = sink;
      return () => {
        longFrameSink = null;
      };
    },
  };
  return {
    options,
    /** Advance time and deliver one frame, if one was requested. */
    frame(intervalMs: number) {
      now += intervalMs;
      const cb = pending;
      pending = null;
      cb?.(now);
    },
    hide(forMs: number) {
      state = "hidden";
      for (const l of listeners) l();
      now += forMs;
    },
    show() {
      state = "visible";
      for (const l of listeners) l();
    },
    longFrames(entries: { duration: number; blockingDuration?: number }[]) {
      longFrameSink?.(entries);
    },
    get listenerCount() {
      return listeners.size;
    },
    get cancelled() {
      return cancelled;
    },
    get hasPending() {
      return pending !== null;
    },
  };
};

describe("frameTelemetry", () => {
  it("reports percentiles, slow and frozen frames against the measured display interval", () => {
    const h = harness();
    const session = startFrameTelemetry("scroll", h.options);
    h.frame(0); // first timestamp opens the window
    for (let i = 0; i < 90; i++) h.frame(16.7);
    for (let i = 0; i < 8; i++) h.frame(50);
    h.frame(800);
    const stats = session.stop();

    expect(stats.frames).toBe(99);
    expect(stats.displayIntervalMs).toBeCloseTo(16.7, 1);
    expect(stats.p50Ms).toBeCloseTo(16.7, 1);
    expect(stats.maxMs).toBe(800);
    expect(stats.slowFrames).toBe(9);
    expect(stats.frozenFrames).toBe(1);
    expect(stats.degraded).toBe(false);
  });

  it("treats a 120 Hz display's 16 ms frame as slow, not as on time", () => {
    const h = harness();
    const session = startFrameTelemetry("promotion", h.options);
    h.frame(0);
    for (let i = 0; i < 60; i++) h.frame(8.3);
    for (let i = 0; i < 6; i++) h.frame(16.6);
    const stats = session.stop();
    expect(stats.displayIntervalMs).toBeCloseTo(8.3, 1);
    expect(stats.slowFrames).toBe(6);
  });

  it("does not count hidden time as a frozen frame", () => {
    const h = harness();
    const session = startFrameTelemetry("route", h.options);
    h.frame(0);
    h.frame(16);
    h.hide(FROZEN_FRAME_MS * 10);
    h.show();
    h.frame(5); // first frame after returning opens a new interval
    h.frame(16);
    const stats = session.stop();
    expect(stats.frozenFrames).toBe(0);
    expect(stats.maxMs).toBe(16);
    expect(stats.frames).toBe(2);
  });

  it("counts long animation frames and their blocking time while active", () => {
    const h = harness();
    const session = startFrameTelemetry("sheet", h.options);
    h.frame(0);
    h.frame(16);
    h.longFrames([{ duration: 120, blockingDuration: 40 }, { duration: 80 }]);
    const stats = session.stop();
    h.longFrames([{ duration: 500, blockingDuration: 450 }]);
    expect(stats.longAnimationFrames).toBe(2);
    expect(stats.longAnimationBlockingMs).toBe(40);
    expect(session.snapshot().longAnimationFrames).toBe(2);
  });

  it("reports null long-frame counts where the entry type is unsupported", () => {
    const h = harness();
    const session = startFrameTelemetry("plain", { ...h.options, observeLongFrames: null });
    h.frame(0);
    h.frame(16);
    expect(session.stop().longAnimationFrames).toBeNull();
  });

  it("is degraded without animation frames, and never reports a clean empty run", () => {
    const session = startFrameTelemetry("headless", {
      mark: false,
      requestFrame: undefined,
      visibility: null,
      observeLongFrames: null,
    });
    const stats = session.stop();
    expect(stats.frames).toBe(0);
    expect(stats.degraded).toBe(true);
  });

  it("stops cleanly: cancels the frame, removes listeners, and is idempotent", () => {
    const h = harness();
    const session = startFrameTelemetry("teardown", h.options);
    h.frame(0);
    expect(h.hasPending).toBe(true);
    const first = session.stop();
    const second = session.stop();
    expect(second).toBe(first);
    expect(h.cancelled).toBe(1);
    expect(h.listenerCount).toBe(0);
    h.frame(16);
    expect(session.snapshot().frames).toBe(first.frames);
  });

  it("publishes the final stats through renderMarks when marking is on", () => {
    (globalThis as unknown as { window: unknown }).window = globalThis;
    try {
      const h = harness();
      const session = startFrameTelemetry("marked", { ...h.options, mark: true });
      h.frame(0);
      for (let i = 0; i < 10; i++) h.frame(16.7);
      session.stop();
      const fact = renderFacts()["ovasabi.render.frame.marked"];
      expect(fact?.lane).toBe("dom");
      expect(fact?.cadence).toBe(60);
      expect(fact?.reason).toContain("p50=16.7");
    } finally {
      delete (globalThis as unknown as { window?: unknown }).window;
    }
  });

  it("keeps a bounded ring of the most recent intervals", () => {
    const h = harness();
    const session = startFrameTelemetry("ring", { ...h.options, capacity: 16 });
    h.frame(0);
    for (let i = 0; i < 100; i++) h.frame(i < 90 ? 100 : 16);
    const stats = session.stop();
    expect(stats.frames).toBe(16);
    expect(stats.maxMs).toBe(100);
  });
});
