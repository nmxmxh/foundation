import { commands } from "vitest/browser";
import { afterEach, describe, expect, it } from "vitest";
import {
  createRenderSurfaceHost,
  prewarmRenderSurface,
  type RenderSurfaceDiagnostics,
  type RenderSurfaceHost,
  type RenderSurfaceQualityTier,
} from "@ovasabi/runtime-browser";
import type { LoadState } from "./fixtures/labSurfaces.worker";
import { summarizeLatency as summary } from "../latencySummary";

/*
 * The render-surface lane on the real GPU (ANGLE/Metal, non-fallback WebGPU).
 *
 * Everything under `runtime-sdk` tests this lane with mocks in Node, which can
 * prove the protocol and cannot prove what a GPU does with it. These tests put
 * real pipelines, real queue backpressure and real device destruction under the
 * same SDK code a project ships, and write what they measured to results/gpu/.
 */

const workerUrl = new URL("./fixtures/labSurfaces.worker.ts", import.meta.url);
const spawn = () => new Worker(workerUrl, { type: "module" });

type SurfaceStats = {
  lane: string;
  builtAtMs: number | null;
  firstFrameAtMs: number | null;
  draws: number;
  gaps: number[];
  drawMs: number[];
  gpuMs: number[];
  gpuWaitFailures: number;
  inFlightAtDraw: number[];
  tiers: number[];
  pixels: number[];
  disposes: number;
};
type LabStats = {
  counters: { acquires: number; releases: number; lost: string[]; served: number; acquired: boolean };
  stats: Record<string, SurfaceStats>;
};

const query = (worker: Worker) =>
  new Promise<LabStats>((resolve, reject) => {
    const timer = setTimeout(() => {
      worker.removeEventListener("message", onMessage);
      reject(new Error("timed out waiting for LAB_STATS"));
    }, 5000);
    const onMessage = (event: MessageEvent) => {
      if (event.data?.kind !== "LAB_STATS") return;
      clearTimeout(timer);
      worker.removeEventListener("message", onMessage);
      resolve(event.data as LabStats);
    };
    worker.addEventListener("message", onMessage);
    worker.postMessage({ kind: "LAB_QUERY" });
  });

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));
const epochNow = () => performance.timeOrigin + performance.now();

const mounted: Array<{ host: RenderSurfaceHost<LoadState>; canvas: HTMLCanvasElement }> = [];
const workers: Worker[] = [];

const mountCanvas = (size = 400) => {
  const canvas = document.createElement("canvas");
  canvas.style.cssText = `display:block;width:${size}px;height:${size}px`;
  document.body.appendChild(canvas);
  return canvas;
};

const mount = (
  options: Omit<Parameters<typeof createRenderSurfaceHost<LoadState>>[0], "canvas"> & { size?: number },
) => {
  const canvas = mountCanvas(options.size);
  const diagnostics: RenderSurfaceDiagnostics[] = [];
  const failures: string[] = [];
  const host = createRenderSurfaceHost<LoadState>({
    ...options,
    canvas,
    onDiagnostics: (d) => diagnostics.push(d),
    onFailed: (reason) => failures.push(reason),
  });
  mounted.push({ host, canvas });
  return { host, canvas, diagnostics, failures };
};

const waitFor = async (check: () => boolean, label: string, timeoutMs = 10_000) => {
  const deadline = performance.now() + timeoutMs;
  while (!check()) {
    if (performance.now() > deadline) throw new Error(`timed out waiting for ${label}`);
    await sleep(10);
  }
};

afterEach(() => {
  for (const { host, canvas } of mounted.splice(0)) {
    host.dispose();
    canvas.remove();
  }
  for (const worker of workers.splice(0)) worker.terminate();
});

/** A four-rung ladder at 40 Hz whose detail (fragment iterations) falls with each rung. */
const LOAD_TIERS: readonly RenderSurfaceQualityTier[] = [
  { scale: 1, cadenceMs: 25, detail: 400 },
  { scale: 0.75, cadenceMs: 25, detail: 200 },
  { scale: 0.5, cadenceMs: 25, detail: 80 },
  { scale: 0.35, cadenceMs: 25, detail: 20 },
];

describe("render surface lane on the real GPU", () => {
  it("records completed WebGL2 fences and stops polling after disposal", async () => {
    const worker = spawn();
    workers.push(worker);
    const { host, failures } = mount({
      surface: "gl-a", createWorker: () => worker, ownsWorker: false,
      tiers: LOAD_TIERS, startingTier: 3, size: 120,
      initialState: { load: 0.05, timeGpu: true },
    });
    const deadline = performance.now() + 5000;
    let observed = (await query(worker)).stats["gl-a"];
    while ((!observed || observed.gpuMs.length < 2) && performance.now() < deadline) {
      await sleep(50);
      observed = (await query(worker)).stats["gl-a"];
    }
    expect(failures).toEqual([]);
    expect(observed?.gpuMs.length, "WebGL2 fence produced no completion evidence").toBeGreaterThanOrEqual(2);
    host.dispose();
    await sleep(100);
    const stopped = (await query(worker)).stats["gl-a"]!;
    await sleep(100);
    const later = (await query(worker)).stats["gl-a"]!;
    expect(stopped.disposes).toBe(1);
    expect(stopped.gpuWaitFailures).toBe(0);
    expect(later.gpuMs).toEqual(stopped.gpuMs);
    await commands.writeFile("results/gpu/webgl-fence.json", `${JSON.stringify({
      measuredAt: new Date().toISOString(), userAgent: navigator.userAgent,
      completionMs: summary(stopped.gpuMs), disposed: stopped.disposes, waitFailures: stopped.gpuWaitFailures,
      note: "Queue completion plus timer polling; not GPU kernel time.",
    }, null, 2)}\n`);
  });

  it("records the adapter and context the lane draws on", async () => {
    const adapter = await (navigator as any).gpu?.requestAdapter({ powerPreference: "low-power" });
    const gl = document.createElement("canvas").getContext("webgl2");
    const ext = gl?.getExtension("WEBGL_debug_renderer_info");
    const capability = {
      measuredAt: new Date().toISOString(),
      userAgent: navigator.userAgent,
      crossOriginIsolated,
      devicePixelRatio,
      hardwareConcurrency: navigator.hardwareConcurrency,
      webgl2Renderer: gl && ext ? gl.getParameter(ext.UNMASKED_RENDERER_WEBGL) : null,
      webgpu: adapter
        ? {
            vendor: adapter.info?.vendor,
            architecture: adapter.info?.architecture,
            fallback: adapter.info?.isFallbackAdapter,
            features: [...adapter.features].sort(),
            maxTextureDimension2D: adapter.limits.maxTextureDimension2D,
            maxBufferSize: adapter.limits.maxBufferSize,
          }
        : null,
    };
    await commands.writeFile("results/gpu/capability.json", `${JSON.stringify(capability, null, 2)}\n`);
    expect(capability.webgpu, "no WebGPU adapter: the gpu lane needs a real GPU (see vitest.config.ts)").not.toBeNull();
    expect(capability.webgpu?.fallback, "WebGPU adapter is a software fallback").toBe(false);
  });

  it("measures what the ladder does under real GPU load (WebGPU and WebGL2)", async () => {
    const results: Record<string, unknown> = {};
    // Each case on a fresh owned worker. `backpressure: false` is the control:
    // the loop as it was before `settled`, blind to the GPU.
    const cases = [
      { lane: "gpu-a", load: 0.05, backpressure: true },
      { lane: "gpu-a", load: 4, backpressure: true },
      { lane: "gpu-a", load: 40, backpressure: false },
      { lane: "gpu-a", load: 40, backpressure: true },
      { lane: "gl-a", load: 0.05, backpressure: true },
      { lane: "gl-a", load: 40, backpressure: true },
    ] as const;
    {
      // 4× sits inside the 25 ms cadence on an M1 Pro at 400² px; 40× is far over it.
      for (const { lane, load, backpressure } of cases) {
        const worker = spawn();
        workers.push(worker);
        const { host, canvas, diagnostics, failures } = mount({
          surface: lane,
          createWorker: () => worker,
          tiers: LOAD_TIERS,
          startingTier: 0,
          maxRatio: 1,
          initialState: { load, timeGpu: true, backpressure },
        });
        await waitFor(() => diagnostics.some((d) => d.lane !== "none") || failures.length > 0, `${lane} READY`);
        expect(failures).toEqual([]);
        await sleep(6000);
        const { stats } = await query(worker);
        const s = stats[lane]!;
        const tierChanges = diagnostics.filter((d, i) => i > 0 && d.tier !== diagnostics[i - 1]!.tier).map((d) => d.tier);
        results[`${lane} load=${load}${backpressure ? "" : " no-backpressure"}`] = {
          lane: s.lane,
          draws: s.draws,
          achievedHz: Math.round((s.draws / 6) * 10) / 10,
          gapMs: summary(s.gaps),
          drawCpuMs: summary(s.drawMs),
          gpuMs: summary(s.gpuMs),
          gpuWaitFailures: s.gpuWaitFailures,
          maxInFlightAtDraw: s.inFlightAtDraw.length ? Math.max(...s.inFlightAtDraw) : null,
          finalTier: s.tiers.at(-1),
          tierChanges,
        };
        host.dispose();
        // Removed now, not in afterEach: a disposed canvas left in the document
        // pushes the next one below the fold, where culling (correctly) pauses it.
        canvas.remove();
        worker.terminate();
      }
    }
    await commands.writeFile("results/gpu/ladder-under-load.json", `${JSON.stringify(results, null, 2)}\n`);
    type Row = { finalTier: number; maxInFlightAtDraw: number | null; gpuMs: { p95: number | null } };
    const heavy = results["gpu-a load=40"] as Row;
    const blind = results["gpu-a load=40 no-backpressure"] as Row;
    const light = results["gpu-a load=0.05"] as Row;
    // The control reproduces the defect: frames pile up behind the GPU.
    expect(blind.maxInFlightAtDraw, "control did not overload the GPU; raise the load").toBeGreaterThan(1);
    // With `settled`, overload keeps one frame in flight and walks down the ladder.
    expect(heavy.maxInFlightAtDraw).toBeLessThanOrEqual(1);
    expect(heavy.finalTier, "GPU overload did not demote the surface").toBeGreaterThan(0);
    // And a surface inside its budget is not demoted by the extra wait.
    expect(light.finalTier).toBe(0);
    expect(Object.keys(results).length).toBe(cases.length);
  });

  it("puts three surfaces on one device and destroys it when the last one leaves", async () => {
    const worker = spawn();
    workers.push(worker);
    const surfaces = ["gpu-a", "gpu-b", "gpu-c"].map((surface) =>
      mount({ surface, createWorker: () => worker, ownsWorker: false, tiers: LOAD_TIERS, startingTier: 3, size: 120, initialState: { load: 0.05 } }),
    );
    await waitFor(() => surfaces.every((s) => s.diagnostics.some((d) => d.lane === "webgpu")), "three READY");
    await sleep(300);
    const live = await query(worker);
    expect(live.counters.acquires, "each surface requested its own device").toBe(1);
    expect(Object.values(live.stats).every((s) => s.draws > 0)).toBe(true);

    surfaces[0]!.host.dispose();
    surfaces[1]!.host.dispose();
    await sleep(100);
    const partial = await query(worker);
    expect(partial.counters.releases, "released while a surface still drew").toBe(0);

    surfaces[2]!.host.dispose();
    // STOP retires every pass. The fixture's idle window is 1 s: inside it the
    // device is kept for a remount, after it the device is destroyed.
    await sleep(200);
    const inWindow = await query(worker);
    await sleep(1300);
    const afterWindow = await query(worker);
    await commands.writeFile(
      "results/gpu/shared-device.json",
      `${JSON.stringify({ live: live.counters, partial: partial.counters, inWindow: inWindow.counters, afterWindow: afterWindow.counters, disposes: Object.fromEntries(Object.entries(afterWindow.stats).map(([k, v]) => [k, v.disposes])) }, null, 2)}\n`,
    );
    expect(Object.values(afterWindow.stats).map((s) => s.disposes)).toEqual([1, 1, 1]);
    expect(inWindow.counters.releases, "released inside the idle window").toBe(0);
    expect(afterWindow.counters.releases, "every pass retired, device never released").toBe(1);
    // The driver's own word that the memory went back, not just our counter.
    expect(afterWindow.counters.lost).toEqual(["destroyed"]);
    // Registrations are for the worker's life; only the device went.
    expect(afterWindow.counters.served).toBe(3);
  });

  it("measures time to first frame cold and prewarmed, on a real pipeline compile", async () => {
    const rows: Array<{ kind: string; readyMs: number; firstFrameMs: number | null }> = [];
    for (let round = 0; round < 6; round += 1) {
      for (const kind of round % 2 === 0 ? ["cold", "warm"] : ["warm", "cold"]) {
        let handle: ReturnType<typeof prewarmRenderSurface> = null;
        let worker: Worker;
        if (kind === "warm") {
          handle = prewarmRenderSurface({ surface: "gpu-a", createWorker: spawn });
          expect(handle).not.toBeNull();
          worker = handle!.worker;
          await sleep(1500); // the page knew the surface was coming
        } else {
          worker = spawn();
        }
        workers.push(worker);
        const startedAt = epochNow();
        const { host, canvas, diagnostics } = mount({
          surface: "gpu-a",
          createWorker: () => worker,
          warmed: handle ?? undefined,
          tiers: LOAD_TIERS,
          startingTier: 3,
          size: 200,
          initialState: { load: 0.05 },
        });
        await waitFor(() => diagnostics.some((d) => d.lane === "webgpu"), `${kind} READY`);
        const readyMs = epochNow() - startedAt;
        await sleep(150);
        const { stats } = await query(worker);
        const first = stats["gpu-a"]?.firstFrameAtMs;
        if (round > 0) rows.push({ kind, readyMs: Math.round(readyMs), firstFrameMs: first ? Math.round(first - startedAt) : null });
        host.dispose();
        canvas.remove();
        worker.terminate();
      }
    }
    const by = (kind: string) => summary(rows.filter((r) => r.kind === kind).map((r) => r.firstFrameMs ?? Number.NaN));
    await commands.writeFile(
      "results/gpu/prewarm.json",
      `${JSON.stringify({ note: "round 0 discarded as warm-up; ms from host creation", cold: by("cold"), warm: by("warm"), rows }, null, 2)}\n`,
    );
    expect(rows.every((r) => r.firstFrameMs !== null)).toBe(true);
  });
});
