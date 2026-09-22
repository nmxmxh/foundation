import {
  serveRenderSurface,
  type RenderSurfacePass,
} from "@ovasabi/runtime-browser";
import { createBlackHolePass } from "@reference/render/passes/blackHolePass";

let mode = "settled";
let submitted = 0,
  completed = 0,
  inflight = 0,
  maximumInflight = 0;
let width = 0,
  height = 0;
let maximumBackingPixels = 0;
let sampleStarted = 0;
let halted = false;
let pending: Promise<unknown> = Promise.resolve();
const durations: number[] = [];
let started = false;

function report(failure?: string) {
  const ordered = durations.slice().sort((a, b) => a - b);
  const percentile = (fraction: number) =>
    ordered[
      Math.min(ordered.length - 1, Math.floor(ordered.length * fraction))
    ] ?? null;
  postMessage({
    kind: "LAB_RESULT",
    result: {
      submitted,
      completed,
      inflight,
      maximumInflight,
      width,
      height,
      maximumBackingPixels,
      backingPixels: width * height,
      completionMilliseconds: { p50: percentile(0.5), p95: percentile(0.95) },
      timingScope: "CPU submission through GPU queue completion",
      durationSeconds: (performance.now() - sampleStarted) / 1000,
      ...(failure ? { failure } : {}),
    },
  });
}

addEventListener("message", (event) => {
  if (event.data.kind === "LAB_CONFIG") mode = event.data.mode;
  if (event.data.kind === "LAB_START" && !started) {
    started = true;
    sampleStarted = performance.now();
    setTimeout(() => report(), 5000);
  }
});

serveRenderSurface("blackHole", {
  build: async (canvas) => {
    const pass = await createBlackHolePass(canvas);
    if (!pass) return null;
    const wrapped: RenderSurfacePass<unknown> = {
      lane: pass.lane,
      resize: pass.resize,
      dispose: pass.dispose,
      draw: (state, frame) => {
        if (halted) return;
        if (submitted >= 600 || inflight >= 4) {
          halted = true;
          report("graphics sample reached its work budget");
          return;
        }
        width = frame.width;
        height = frame.height;
        maximumBackingPixels = Math.max(maximumBackingPixels, width * height);
        const start = performance.now();
        pass.draw(state as Parameters<typeof pass.draw>[0], frame);
        submitted++;
        inflight++;
        maximumInflight = Math.max(maximumInflight, inflight);
        pending = (pass.settled?.() ?? Promise.resolve()).then(() => {
          completed++;
          inflight--;
          durations.push(performance.now() - start);
        });
      },
      settled: mode === "settled" && pass.settled ? () => pending : undefined,
    };
    return wrapped;
  },
});
