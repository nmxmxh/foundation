import { expect, test } from "vitest";
import { createCanvasStage } from "./canvasStage";
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
test("render surface loop throughput", async ({ bench }) => {
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

  await bench.compare(
    bench("draw step writing a reused frame descriptor", () => {
      reusedFrame.width = 1920;
      reusedFrame.height = 1080;
      reusedFrame.detail = 80;
      reusedFrame.elapsed = 1500;
      reusedFrame.delta = 16.6;
      reusedFrame.tier = 1;

      // Simulate pass drawing
      sink += reusedFrame.width + reusedFrame.height + reusedFrame.detail;
    }),

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
    }),
  );
  expect(sink).toBeGreaterThan(0);
});

test("canvas stage frame gating throughput", async ({ bench }) => {
  const cadenceMs = 25;
  // Node measures SDK bookkeeping. Browser layout and GPU work need the frontend lab.
  const canvas = { clientWidth: 800, clientHeight: 600, width: 800, height: 600 } as HTMLCanvasElement;
  const drawing = createCanvasStage(canvas, null, { cadenceMs });
  const gated = createCanvasStage(canvas, null, { cadenceMs });
  gated.frame(cadenceMs);
  let now = 0;
  let sink = 0;
  let skipped = 0;
  try {
    await bench.compare(
      bench("canvas stage eligible frame bookkeeping", () => {
        now += cadenceMs;
        const frame = drawing.frame(now);
        if (!frame) throw new Error("eligible frame was skipped");
        sink += frame.width + frame.height;
      }),
      bench("canvas stage cadence-skipped frame", () => {
        if (gated.frame(cadenceMs) !== null) throw new Error("early frame was drawn");
        skipped += 1;
      }),
    );
    expect(sink).toBeGreaterThan(0);
    expect(skipped).toBeGreaterThan(0);
  } finally {
    drawing.dispose();
    gated.dispose();
  }
});
