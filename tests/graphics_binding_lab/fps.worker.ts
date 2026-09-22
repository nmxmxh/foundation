import {
  serveRenderSurface as current,
  type RenderSurfaceScope,
  type RenderSurfaceFrame,
} from "@ovasabi/runtime-browser";
import { serveRenderSurface as baseline } from "./cadenceBaseline";
import { createBlackHolePass } from "@reference/render/passes/blackHolePass";

addEventListener("message", async function start(event) {
  removeEventListener("message", start);
  const { mode, canvas, width, height } = event.data;
  const serve = mode === "baseline60" ? baseline : current;
  const targetFps = mode === "current40" ? 40 : 60;
  let listener: ((event: MessageEvent) => void) | undefined;
  let startedAt = 0,
    sampleAt = 0,
    completed = 0,
    submitted = 0,
    outstanding = 0,
    maximumOutstanding = 0;
  let lastCompletion = 0;
  const gaps: number[] = [],
    costs: number[] = [],
    cpu: number[] = [];
  let pending: Promise<unknown> = Promise.resolve();
  let initialized = false;
  let finishTimer: ReturnType<typeof setTimeout>;
  const percentile = (values: number[], fraction: number) => {
    const sorted = values.slice().sort((a, b) => a - b);
    return (
      sorted[
        Math.min(sorted.length - 1, Math.floor(sorted.length * fraction))
      ] ?? null
    );
  };
  const scope: RenderSurfaceScope = {
    postMessage: (message) => {
      if ((message as { kind: string }).kind === "FAILED") postMessage(message);
    },
    addEventListener: (_type, callback) => {
      listener = callback;
    },
    removeEventListener: () => {
      listener = undefined;
    },
  };
  const stop = serve(
    "benchmark",
    {
      build: async () => {
        const pass = await createBlackHolePass(canvas);
        if (!pass?.settled) {
          pass?.dispose();
          return null;
        }
        const fixed: RenderSurfaceFrame = {
          width,
          height,
          detail: 118,
          elapsed: 30000,
          delta: 0,
          tier: 0,
          shared: null,
          sharedGeneration: 0,
        };
        return {
          lane: pass.lane,
          resize: pass.resize,
          dispose: pass.dispose,
          settled: () => pending,
          draw: () => {
            const now = performance.now();
            if (!initialized) {
              initialized = true;
              startedAt = now;
              sampleAt = now + 1000;
              finishTimer = setTimeout(() => {
                const seconds = (performance.now() - sampleAt) / 1000;
                postMessage({
                  kind: "RESULT",
                  result: {
                    mode,
                    targetFps,
                    width,
                    height,
                    pixels: width * height,
                    steps: 118,
                    camera:
                      "initial orbit pose; zero simulation delta; shader time 30 seconds",
                    warmupMs: 1000,
                    durationSeconds: seconds,
                    completed,
                    completedFps: completed / seconds,
                    maximumOutstanding,
                    completionGapMs: {
                      p50: percentile(gaps, 0.5),
                      p95: percentile(gaps, 0.95),
                      p99: percentile(gaps, 0.99),
                    },
                    submitToCompletionMs: {
                      p50: percentile(costs, 0.5),
                      p95: percentile(costs, 0.95),
                    },
                    cpuDrawMs: {
                      p50: percentile(cpu, 0.5),
                      p95: percentile(cpu, 0.95),
                    },
                    timingScope:
                      "completed render frames; not presentation timestamps",
                    lane: pass.lane,
                  },
                });
                stop();
              }, 6000);
            }
            if (++submitted > 1200 || now - startedAt > 15000) {
              clearTimeout(finishTimer);
              postMessage({
                kind: "FAILED",
                reason: "sample work bound exceeded",
              });
              stop();
              return;
            }
            pass.draw(
              { pointerX: 0, pointerY: 0, level: 0, reduced: false },
              fixed,
            );
            const drawCost = performance.now() - now;
            maximumOutstanding = Math.max(maximumOutstanding, ++outstanding);
            pending = pass.settled!().then(() => {
              outstanding--;
              const end = performance.now();
              if (now >= sampleAt) {
                completed++;
                costs.push(end - now);
                cpu.push(drawCost);
                if (lastCompletion) gaps.push(end - lastCompletion);
                lastCompletion = end;
              }
            });
          },
        };
      },
    },
    scope,
  );
  listener?.({
    data: {
      kind: "INIT",
      surface: "benchmark",
      canvas,
      width,
      height,
      ratio: 1,
      tiers: [{ scale: 1, cadenceMs: 1000 / targetFps, detail: 118 }],
      requirements: {
        gpuCompletion: "required",
        maxBackingPixels: width * height,
      },
    },
  } as MessageEvent);
});
