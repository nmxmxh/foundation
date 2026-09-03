import { bench, describe } from "vitest";
import type { RenderSurfaceFrame } from "./renderSurfaceClient";

describe("render surface loop allocation benchmark", () => {
  const reusedFrame: RenderSurfaceFrame = {
    width: 0,
    height: 0,
    detail: 0,
    elapsed: 0,
    delta: 0,
    tier: 0,
  };

  let sink = 0;

  bench("zero-allocation draw step (reused frame descriptor)", () => {
    // Zero allocations on 60/120fps hot path
    reusedFrame.width = 1920;
    reusedFrame.height = 1080;
    reusedFrame.detail = 80;
    reusedFrame.elapsed = 1500;
    reusedFrame.delta = 16.6;
    reusedFrame.tier = 1;

    // Simulate pass drawing
    sink += reusedFrame.width + reusedFrame.height + reusedFrame.detail;
  });

  bench("allocating draw step (object literal creation per frame)", () => {
    // Allocates an object on every tick
    const frame: RenderSurfaceFrame = {
      width: 1920,
      height: 1080,
      detail: 80,
      elapsed: 1500,
      delta: 16.6,
      tier: 1,
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
