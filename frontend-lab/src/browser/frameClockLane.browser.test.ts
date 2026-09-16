import { afterEach, describe, expect, it } from "vitest";
import {
  PASS,
  _resetFrameClockForTesting,
  clearRenderFacts,
  configureFrameClock,
  frameClockMode,
  onFrame,
  renderFacts,
} from "@ovasabi/runtime-browser";

/*
 * The single frame pacer must actually run on its worker lane in an isolated
 * page. ovasabi_v1 shipped for its whole life with the clock on the main-thread
 * fallback while its fact read "ok" (research doc section 13); a Node unit test
 * could only prove the diagnostic string, because Node has no Worker and no
 * isolation. This lane has both (isolation.browser.test.ts guards that).
 */

const pulseWorkerUrl = new URL("../../../runtime-sdk/ts/browser-host/src/pulse/pulse.worker.ts", import.meta.url);

const waitFor = async (predicate: () => boolean, timeoutMs = 3000) => {
  const started = performance.now();
  while (!predicate()) {
    if (performance.now() - started > timeoutMs) return false;
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  return true;
};

describe("frame clock lane (chromium, cross-origin isolated)", () => {
  afterEach(() => {
    _resetFrameClockForTesting();
    clearRenderFacts();
  });

  it("runs on the worker pulse when the consumer passes createWorker", async () => {
    configureFrameClock({ createWorker: () => new Worker(pulseWorkerUrl, { type: "module" }) });
    let ticks = 0;
    const subscription = onFrame(() => {
      ticks += 1;
    }, 16);

    expect(await waitFor(() => ticks >= 5), "subscriber never ticked").toBe(true);
    // Past the 120 ms window in which the clock switches itself to plain frames
    // if no worker tick has arrived.
    await new Promise((resolve) => setTimeout(resolve, 300));

    const mode = frameClockMode();
    expect(mode.mode).toBe("worker");
    expect(mode.degraded).toBe(false);
    const fact = renderFacts()[PASS.clock];
    expect(fact?.lane).toBe("worker");
    expect(fact?.fallback).toBe(false);
    subscription.release();
  });

  it("reports a worker that fails to load as unavailable, never as ok", async () => {
    configureFrameClock({
      createWorker: () => new Worker(new URL("./does-not-exist.worker.ts", import.meta.url), { type: "module" }),
    });
    let ticks = 0;
    const subscription = onFrame(() => {
      ticks += 1;
    }, 16);

    const settled = await waitFor(() => renderFacts()[PASS.clock]?.fallback === true);
    expect(settled, "the clock never reported its fallback").toBe(true);
    const fact = renderFacts()[PASS.clock];
    expect(fact?.lane).toBe("main-thread");
    expect(fact?.reason).not.toBe("ok");
    expect(fact?.reason).toContain("worker unavailable");
    // Degraded is not dead: frames still arrive on the fallback lane.
    expect(await waitFor(() => ticks >= 5), "fallback lane never ticked").toBe(true);
    subscription.release();
  });
});
