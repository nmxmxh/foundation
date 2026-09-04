import { bench, describe } from "vitest";
import type { RenderSurfaceFrame } from "./renderSurfaceClient";

/*
 * These are throughput benchmarks. They are NOT allocation benchmarks, and the
 * pair below used to be labelled as if they were.
 *
 * Neither value escapes its callback, so V8 stack-allocates the object literal
 * outright and both variants compile to the same arithmetic — measured, the
 * "allocating" one came out 1.06x *faster* than the "zero-allocation" one. A
 * benchmark that arranges to be unable to observe the property it is named for
 * is worse than no benchmark, because it is now the reason nobody checks.
 *
 * Allocation is measured in `renderSurface.profile.test.ts`, differentially,
 * against a retained sink that defeats escape analysis, under a real
 * `--expose-gc`. That file reports 0 bytes/frame for both halves of the lane
 * and 88 bytes/frame for the literal these benches cannot tell apart.
 */
describe("render surface loop throughput", () => {
  const reusedFrame: RenderSurfaceFrame = {
    width: 0,
    height: 0,
    detail: 0,
    elapsed: 0,
    delta: 0,
    tier: 0,
    shared: null,
    sharedGeneration: 0,
  };

  let sink = 0;

  bench("draw step writing a reused frame descriptor", () => {
    reusedFrame.width = 1920;
    reusedFrame.height = 1080;
    reusedFrame.detail = 80;
    reusedFrame.elapsed = 1500;
    reusedFrame.delta = 16.6;
    reusedFrame.tier = 1;

    // Simulate pass drawing
    sink += reusedFrame.width + reusedFrame.height + reusedFrame.detail;
  });

  bench("draw step building a frame object literal", () => {
    const frame: RenderSurfaceFrame = {
      width: 1920,
      height: 1080,
      detail: 80,
      elapsed: 1500,
      delta: 16.6,
      tier: 1,
      shared: null,
      sharedGeneration: 0,
    };

    sink += frame.width + frame.height + frame.detail;
  });
});

describe("canvas stage frame gating throughput", () => {
  let lastDrawAt = 0;
  const cadenceMs = 25;
  const width = 800;
  const height = 600;
  const maxRatio = 1.5;
  let sink = 0;

  bench("cadence and scale gating check", () => {
    const now = 1000;
    if (now - lastDrawAt >= cadenceMs) {
      lastDrawAt = now;
      const ratio = Math.min(2, maxRatio);
      const backingWidth = Math.round(width * ratio);
      const backingHeight = Math.round(height * ratio);
      sink += backingWidth + backingHeight;
    }
  });
});
