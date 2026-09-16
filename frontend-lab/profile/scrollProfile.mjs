#!/usr/bin/env node
/**
 * Scripted scroll profile (research doc section 7, P10) — writes a capture bundle.
 *
 * Serves profile/index.html with the lab's own Vite config (same aliases, same
 * COOP/COEP), drives headless Chromium through every variant, and records what
 * the page's shipped instruments see. Runs follow the harness rules recorded in
 * the research ledger:
 *
 * - warm first (PROFILE_WARMUP passes are measured and thrown away): cold
 *   numbers are a first-use hitch, not scroll performance (finding 6);
 * - interleave variants inside each pass, so drift lands on every variant
 *   rather than on whichever ran last (finding 3);
 * - report ranges across runs, and gate P1 on repeatability: p95 within ±10%.
 *
 * Desktop Chromium numbers are lab evidence about Foundation's primitives, not
 * evidence about a floor device.
 *
 *   node profile/scrollProfile.mjs
 *   PROFILE_RUNS=5 PROFILE_WARMUP=2 PROFILE_SECTIONS=120 PROFILE_TRACE=1 node profile/scrollProfile.mjs
 */

import { execSync } from "node:child_process";
import { mkdirSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";
import { createServer } from "vite";

const lab = fileURLToPath(new URL("..", import.meta.url));
const RUNS = Number(process.env.PROFILE_RUNS ?? 3);
const WARMUP = Number(process.env.PROFILE_WARMUP ?? 1);
const SECTIONS = Number(process.env.PROFILE_SECTIONS ?? 80);
const TRACE = process.env.PROFILE_TRACE === "1";
/*
 * CPU slowdown factor applied through the DevTools protocol. At 1x a desktop
 * draws the fixture at display cadence in every variant (first bundle,
 * 2026-09-14: p50 8.3 ms, 0% slow everywhere), which proves repeatability and
 * nothing else. A floor Android phone is several times slower than a laptop
 * core; PROFILE_CPU_THROTTLE=6 is the conventional approximation.
 */
const CPU_THROTTLE = Math.max(1, Number(process.env.PROFILE_CPU_THROTTLE ?? 1));
/*
 * Which GPU backend Chromium rasterises on. Headless Chromium's default is
 * SwiftShader — a *CPU* implementation of Vulkan — so every raster and GPU
 * number in a default bundle is software rendering on the host CPU, and it
 * is throttled along with everything else. Found 2026-09-15: the first traced
 * bundles (raster −93% under low_power) were all SwiftShader. `metal` uses
 * ANGLE on the host GPU (macOS), which is the backend a real device's GPU
 * raster resembles; `swiftshader` keeps the old behaviour for comparison.
 */
const GPU = process.env.PROFILE_GPU ?? "swiftshader";
const GPU_ARGS = { swiftshader: [], metal: ["--use-angle=metal", "--enable-unsafe-webgpu"] };
if (!(GPU in GPU_ARGS)) throw new Error(`PROFILE_GPU must be one of ${Object.keys(GPU_ARGS).join(", ")}`);
const VIEWPORT = { width: 412, height: 915 };
// The Android emulator the ChooseChow captures came from reported DPR 2.625;
// raster cost scales with device pixels, so the lab draws at the same density.
const DEVICE_SCALE_FACTOR = 2.625;

/*
 * Variant sets. `tiers` (default) is the P2/P4 comparison; `skeleton` compares
 * loading-placeholder animation designs at the untiered baseline — added
 * 2026-09-15 after moving the sweep to a transform cut paint 98% and still
 * made frames slower (layer count), which only an interleaved A/B can settle.
 */
const VARIANT_SETS = {
  tiers: [
    { name: "baseline", tier: null, cull: false },
    { name: "cull", tier: null, cull: true },
    { name: "low_power", tier: "low_power", cull: false },
    { name: "low_power+cull", tier: "low_power", cull: true },
  ],
  skeleton: [
    { name: "transform", tier: null, cull: false, skeleton: "minimal" },
    { name: "bgpos", tier: null, cull: false, skeleton: "bgpos" },
    { name: "pulse", tier: null, cull: false, skeleton: "pulse" },
    { name: "static", tier: null, cull: false, skeleton: "static" },
    { name: "none", tier: null, cull: false, skeleton: "none" },
    { name: "paused", tier: null, cull: false, skeleton: "paused" },
    { name: "transform+cull", tier: null, cull: true, skeleton: "minimal" },
  ],
};
const SET = process.env.PROFILE_SET ?? "tiers";
if (!(SET in VARIANT_SETS)) throw new Error(`PROFILE_SET must be one of ${Object.keys(VARIANT_SETS).join(", ")}`);
const VARIANTS = VARIANT_SETS[SET];

const TRACE_CATEGORIES = [
  "devtools.timeline",
  "disabled-by-default-devtools.timeline",
  "disabled-by-default-devtools.timeline.frame",
  "blink",
  "cc",
  "gpu",
  "viz",
];
/** Timeline events worth summing: where a frame's main-thread and raster time goes. */
const SUMMARY_EVENTS = new Set([
  "UpdateLayoutTree",
  "Layout",
  "Paint",
  "PrePaint",
  "Layerize",
  "Commit",
  "RasterTask",
  "CompositeLayers",
  "GPUTask",
  "FunctionCall",
  "EventDispatch",
  "ParseHTML",
  "DecodeImage",
]);

const git = (args) => {
  try {
    return execSync(`git ${args}`, { cwd: lab, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }).trim();
  } catch {
    return null;
  }
};

const median = (values) => {
  if (values.length === 0) return null;
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  return sorted.length % 2 ? sorted[mid] : (sorted[mid - 1] + sorted[mid]) / 2;
};

const scrollPass = async (page) => {
  for (let step = 0; step < 45; step += 1) {
    await page.mouse.wheel(0, 200);
    await page.waitForTimeout(16);
  }
  for (let step = 0; step < 45; step += 1) {
    await page.mouse.wheel(0, -200);
    await page.waitForTimeout(16);
  }
};

const traceSummary = async (context, page, run) => {
  const cdp = await context.newCDPSession(page);
  const events = [];
  cdp.on("Tracing.dataCollected", ({ value }) => events.push(...value));
  const done = new Promise((resolve) => cdp.once("Tracing.tracingComplete", resolve));
  await cdp.send("Tracing.start", {
    transferMode: "ReportEvents",
    traceConfig: { recordMode: "recordAsMuchAsPossible", includedCategories: TRACE_CATEGORIES },
  });
  await run();
  await cdp.send("Tracing.end");
  await done;
  await cdp.detach();
  const totals = {};
  for (const event of events) {
    if (event.ph !== "X" || typeof event.dur !== "number" || !SUMMARY_EVENTS.has(event.name)) continue;
    totals[event.name] = (totals[event.name] ?? 0) + event.dur / 1000;
  }
  for (const key of Object.keys(totals)) totals[key] = Math.round(totals[key] * 10) / 10;
  return { events: events.length, totalsMs: totals };
};

const server = await createServer({
  configFile: fileURLToPath(new URL("../vitest.config.ts", import.meta.url)),
  root: lab,
  logLevel: "warn",
  server: { port: 0, strictPort: false },
});
await server.listen();
const origin = server.resolvedUrls?.local?.[0];
if (!origin) throw new Error("vite did not report a local URL");

const browser = await chromium.launch({ headless: true, args: GPU_ARGS[GPU] });
const context = await browser.newContext({ viewport: VIEWPORT, deviceScaleFactor: DEVICE_SCALE_FACTOR });
const samples = [];

// The capture schema (gpu_practices.md) requires the adapter; read it rather than assume the flag took.
const gpuPage = await context.newPage();
await gpuPage.goto(`${origin}profile/index.html?sections=1`);
const gpuInfo = await gpuPage.evaluate(async () => {
  const gl = document.createElement("canvas").getContext("webgl2");
  const ext = gl?.getExtension("WEBGL_debug_renderer_info");
  const adapter = await navigator.gpu?.requestAdapter().catch(() => null);
  return {
    webgl2Renderer: gl ? (ext ? gl.getParameter(ext.UNMASKED_RENDERER_WEBGL) : gl.getParameter(gl.RENDERER)) : null,
    webgpuAdapter: adapter ? { vendor: adapter.info?.vendor, architecture: adapter.info?.architecture, fallback: adapter.info?.isFallbackAdapter } : null,
  };
});
await gpuPage.close();

try {
  for (let pass = 0; pass < WARMUP + RUNS; pass += 1) {
    const measured = pass >= WARMUP;
    for (const variant of VARIANTS) {
      const page = await context.newPage();
      // Throttling holds only while this session stays attached, so it lives until the page closes.
      const throttle = CPU_THROTTLE > 1 ? await context.newCDPSession(page) : null;
      if (throttle) await throttle.send("Emulation.setCPUThrottlingRate", { rate: CPU_THROTTLE });
      const query = new URLSearchParams({ sections: String(SECTIONS) });
      if (variant.tier) query.set("tier", variant.tier);
      if (variant.cull) query.set("cull", "1");
      if (variant.skeleton) query.set("skeleton", variant.skeleton);
      await page.goto(`${origin}profile/index.html?${query}`);
      await page.waitForFunction(() => window.__lab?.ready === true, null, { timeout: 30_000 });
      await page.mouse.move(VIEWPORT.width / 2, VIEWPORT.height / 2);
      await page.waitForTimeout(400);

      let sample;
      const run = async () => {
        await page.evaluate(() => window.__lab.start());
        await scrollPass(page);
        sample = await page.evaluate(() => window.__lab.stop());
      };
      const trace = TRACE && measured ? await traceSummary(context, page, run) : (await run(), null);
      if (measured) samples.push({ variant: variant.name, run: pass - WARMUP, ...sample, trace });
      await throttle?.detach().catch(() => undefined);
      await page.close();
    }
    console.log(`pass ${pass + 1}/${WARMUP + RUNS}${measured ? "" : " (warm-up, discarded)"} done`);
  }
} finally {
  await browser.close().catch(() => undefined);
  await server.close();
}

const variants = VARIANTS.map((variant) => {
  const runs = samples.filter((s) => s.variant === variant.name);
  const p95s = runs.map((s) => s.stats.p95Ms);
  const midP95 = median(p95s);
  const spread = midP95 ? (Math.max(...p95s) - Math.min(...p95s)) / midP95 : null;
  const degraded = runs.some((s) => s.stats.degraded);
  return {
    variant: variant.name,
    runs: runs.length,
    p50Ms: median(runs.map((s) => s.stats.p50Ms)),
    p95Ms: { min: Math.min(...p95s), median: midP95, max: Math.max(...p95s) },
    slowShare: median(runs.map((s) => (s.stats.frames ? s.stats.slowFrames / s.stats.frames : 0))),
    loafCount: median(runs.map((s) => s.loaf?.count ?? 0)),
    loafBlockingMs: median(runs.map((s) => s.loaf?.blockingMs ?? 0)),
    domNodes: median(runs.map((s) => s.domNodes)),
    p95Spread: spread,
    repeatable: !degraded && spread !== null && spread <= 0.1,
  };
});

const bundle = {
  kind: "frontend-lab.scroll-profile",
  capturedAt: new Date().toISOString(),
  foundationSha: git("rev-parse HEAD"),
  foundationDirty: (git("status --porcelain") ?? "").length > 0,
  browser: `chromium ${browser.version()}`,
  gpu: { backend: GPU, ...gpuInfo },
  variantSet: SET,
  viewport: VIEWPORT,
  deviceScaleFactor: DEVICE_SCALE_FACTOR,
  cpuThrottle: CPU_THROTTLE,
  sections: SECTIONS,
  warmupPasses: WARMUP,
  measuredRuns: RUNS,
  scenario: "document scroll: 45 wheel steps of 200px down then up, 16 ms apart",
  gate: { name: "P1 repeatability: p95 within ±10% across runs", passed: variants.every((v) => v.repeatable) },
  variants,
  samples,
};

const stamp = bundle.capturedAt.replace(/[:.]/g, "-");
const dir = fileURLToPath(new URL(`../results/profile/${stamp}/`, import.meta.url));
mkdirSync(dir, { recursive: true });
writeFileSync(`${dir}bundle.json`, `${JSON.stringify(bundle, null, 2)}\n`);

const pct = (value) => (value === null ? "-" : `${Math.round(value * 100)}%`);
console.log(`\ngpu ${GPU}: ${gpuInfo.webgl2Renderer}`);
console.log(`scroll profile — ${bundle.browser}, ${VIEWPORT.width}x${VIEWPORT.height} @${DEVICE_SCALE_FACTOR}x, CPU ${CPU_THROTTLE}x, ${SECTIONS} sections, ${RUNS} runs after ${WARMUP} warm-up`);
console.log("variant          p50    p95 (min–median–max)   slow   LoAF  block  nodes  p95 spread");
for (const v of variants) {
  console.log(
    `${v.variant.padEnd(16)} ${String(v.p50Ms).padStart(5)}  ${`${v.p95Ms.min}–${v.p95Ms.median}–${v.p95Ms.max}`.padEnd(22)} ${pct(v.slowShare).padStart(5)} ${String(v.loafCount).padStart(5)} ${String(v.loafBlockingMs).padStart(6)} ${String(v.domNodes).padStart(6)}  ${pct(v.p95Spread)}${v.repeatable ? "" : "  (not repeatable)"}`,
  );
}
if (TRACE) {
  // Rendering work per variant: the signal culling and tiers change even when frames stay on budget.
  const keys = ["UpdateLayoutTree", "Layout", "PrePaint", "Paint", "Layerize", "RasterTask", "GPUTask", "FunctionCall"];
  console.log(`\nmedian trace ms   ${keys.map((k) => k.padStart(10)).join("")}`);
  for (const v of VARIANTS) {
    const runs = samples.filter((s) => s.variant === v.name && s.trace);
    const row = keys.map((k) => median(runs.map((s) => s.trace.totalsMs[k] ?? 0)));
    console.log(`${v.name.padEnd(17)} ${row.map((ms) => String(ms ?? "-").padStart(10)).join("")}`);
  }
}
console.log(`\n${bundle.gate.name}: ${bundle.gate.passed ? "PASS" : "FAIL"}`);
console.log(`bundle: ${dir}bundle.json`);
process.exitCode = bundle.gate.passed ? 0 : 1;
