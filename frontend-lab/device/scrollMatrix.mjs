#!/usr/bin/env node
/**
 * Interleaved, instrumented on-device scroll matrix for a WebView app
 * (research doc §7; supersedes scrollGfx.sh for diagnosis, which stays for quick A/B).
 *
 * Per run it records, for the same swipes:
 *   - platform truth: `dumpsys gfxinfo` summary (all frames since reset) and
 *     `framestats` phase breakdown (last ≤120 frames: input, animation,
 *     traversal, sync, draw commands, swap, GPU completion);
 *   - page truth: rAF intervals (share over 1.5 display frames), long animation frames;
 *   - page work: Performance.getMetrics deltas (style recalcs, layouts, durations),
 *     composited layer count (LayerTree), WebSocket frames received mid-run
 *     (the bimodal-runs hypothesis: a projection delta landing during the swipe);
 *   - context: seconds since app launch, run index, variant, route, PSS;
 *   - environment health: host 1-min load average and the guest's MemAvailable
 *     and swap in use. Found 2026-09-17: a busy host (Docker VM at a full core,
 *     load 10) and a guest with 135 MB free made the plain-list *control* read
 *     p50 89 ms — slower than the real page. A run whose control is slow is not
 *     evidence; these fields are how a run is excluded.
 *
 * Variants are CSS/JS injections applied after an in-app route change
 * (history.pushState + popstate, so the app stays warm; no reload per run).
 * Order: one discarded warm-up round, then PAIRS rounds with variants in
 * rotating order so drift lands evenly. Every run is appended to runs.jsonl
 * as it finishes (tail it to monitor), and a summary is printed at the end.
 *
 *   node device/scrollMatrix.mjs                       # defaults below
 *   ROUTES=/profile,/dashboard VARIANTS=baseline,no-shadow PAIRS=4 node device/scrollMatrix.mjs
 *
 * Prerequisites: handover §4 — app installed from a devtools build, signed in,
 * `adb forward tcp:9222 localabstract:webview_devtools_remote_<pid>`.
 */
import { execFileSync } from "node:child_process";
import { appendFileSync, mkdirSync, writeFileSync } from "node:fs";
import { loadavg } from "node:os";
import { fileURLToPath } from "node:url";

const PKG = process.env.PKG ?? "com.ovasabi.choosechow";
const ROUTES = (process.env.ROUTES ?? "/profile").split(",");
const PAIRS = Number(process.env.PAIRS ?? 4);
const SETTLE_MS = Number(process.env.SETTLE_MS ?? 8000);
const SWIPES = Number(process.env.SWIPES ?? 3);
const lab = fileURLToPath(new URL("..", import.meta.url));
const stamp = new Date().toISOString().replace(/[:.]/g, "-");
const outDir = process.env.OUT ?? `${lab}results/device/${stamp}`;
mkdirSync(outDir, { recursive: true });
const runsFile = `${outDir}/runs.jsonl`;

/*
 * Suspects. Selectors avoid Linaria's hashed class names: they anchor on the
 * shell's stable ids and element roles.
 */
const HEADER = "#main-content > * > header";
const VARIANTS_ALL = {
  baseline: { css: "" },
  "no-shadow-blur": {
    css: `*,*::before,*::after{box-shadow:none!important;backdrop-filter:none!important;-webkit-backdrop-filter:none!important;filter:none!important}`,
  },
  "header-static": { css: `${HEADER}{position:relative!important}` },
  "header-flat": {
    css: `${HEADER}{border-radius:0!important;box-shadow:none!important;background:#9f1d3a!important;isolation:auto!important}`,
  },
  "no-radius": { css: `*,*::before,*::after{border-radius:0!important}` },
  "no-images": { css: `img,picture,video{visibility:hidden!important}` },
  "no-chrome": {
    // Dock, cart FAB and any fixed banners: everything painted over the scroller.
    css: `#main-content ~ *{display:none!important} #main-content{padding-bottom:0!important}`,
    js: `(()=>{const m=document.getElementById('main-content');const p=m&&m.previousElementSibling;if(p)p.style.display='none';return 1})()`,
  },
  "no-bg-image": { css: `*,*::before,*::after{background-image:none!important}` },
  "contain-cards": {
    css: `#main-content > * > div > *{contain:layout paint!important}`,
  },
  "tier-low": { js: `(()=>{document.documentElement.setAttribute('data-ui-tier','low_power');return 1})()` },
  "everything-off": {
    css: `*,*::before,*::after{box-shadow:none!important;backdrop-filter:none!important;-webkit-backdrop-filter:none!important;filter:none!important;border-radius:0!important;background-image:none!important;animation:none!important;transition:none!important} img{visibility:hidden!important} ${HEADER}{position:relative!important} #main-content ~ *{display:none!important}`,
  },
  "plain-list": {
    // Control: same shell, same scroller, trivial content.
    js: `(()=>{const m=document.getElementById('main-content');const box=m.firstElementChild;box.innerHTML='';for(let i=0;i<200;i++){const d=document.createElement('div');d.textContent='Row '+i;d.style.cssText='height:56px;border-bottom:1px solid #ddd;padding:16px;box-sizing:border-box;font:16px sans-serif';box.appendChild(d)}return 1})()`,
  },
};
const VARIANTS = (process.env.VARIANTS ?? "baseline,no-shadow-blur,header-static,header-flat,no-images,no-chrome,everything-off,plain-list").split(",");
for (const v of VARIANTS) if (!VARIANTS_ALL[v]) throw new Error(`unknown variant ${v}`);

const sh = (...args) => execFileSync("adb", args, { encoding: "utf8", maxBuffer: 64 * 1024 * 1024 });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// ---- CDP client (one socket for the whole matrix) ---------------------------
async function connect() {
  const targets = await (await fetch("http://127.0.0.1:9222/json")).json();
  const page = targets.find((t) => t.type === "page");
  if (!page) throw new Error("no page target on 127.0.0.1:9222");
  const ws = new WebSocket(page.webSocketDebuggerUrl);
  let id = 1;
  const pending = new Map();
  const listeners = new Set();
  ws.onmessage = (e) => {
    const m = JSON.parse(e.data);
    if (m.id && pending.has(m.id)) {
      pending.get(m.id)(m);
      pending.delete(m.id);
    } else for (const l of listeners) l(m);
  };
  await new Promise((res, rej) => ((ws.onopen = res), (ws.onerror = rej)));
  const send = (method, params = {}) =>
    new Promise((res) => {
      const i = id++;
      pending.set(i, res);
      ws.send(JSON.stringify({ id: i, method, params }));
    });
  const evaluate = async (expression) => {
    const r = await send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true });
    if (r.result?.exceptionDetails) throw new Error(JSON.stringify(r.result.exceptionDetails).slice(0, 400));
    return r.result?.result?.value;
  };
  return { send, evaluate, listeners, close: () => ws.close() };
}

const cdp = await connect();
await cdp.send("Performance.enable");
await cdp.send("Network.enable");
await cdp.send("LayerTree.enable");
let wsFrames = 0;
let lastLayers = null;
cdp.listeners.add((m) => {
  if (m.method === "Network.webSocketFrameReceived") wsFrames++;
  if (m.method === "LayerTree.layerTreeDidChange" && m.params?.layers) lastLayers = m.params.layers.length;
});

const launchedAt = (() => {
  // Process start from /proc, so "seconds since launch" is honest across reruns.
  try {
    const pid = sh("shell", "pidof", PKG).trim();
    const up = Number(sh("shell", "cat", "/proc/uptime").split(" ")[0]);
    const startTicks = Number(sh("shell", "cat", `/proc/${pid}/stat`).split(") ")[1].split(" ")[19]);
    return Date.now() - (up - startTicks / 100) * 1000;
  } catch {
    return null;
  }
})();

// ---- framestats ----------------------------------------------------------------
function parseFramestats(text) {
  const blocks = text.split("---PROFILEDATA---");
  const rows = [];
  for (let b = 1; b < blocks.length; b += 2) {
    const lines = blocks[b].trim().split("\n").filter(Boolean);
    const head = lines[0].split(",");
    const col = (n) => head.indexOf(n);
    for (const line of lines.slice(1)) {
      const c = line.split(",").map(Number);
      if (c[col("Flags")] !== 0) continue; // skip frames flagged as not user-visible
      const ns = (a, z) => (c[col(z)] - c[col(a)]) / 1e6;
      rows.push({
        total: ns("IntendedVsync", "FrameCompleted"),
        input: ns("HandleInputStart", "AnimationStart"),
        anim: ns("AnimationStart", "PerformTraversalsStart"),
        traversal: ns("PerformTraversalsStart", "SyncStart"),
        sync: ns("SyncStart", "IssueDrawCommandsStart"),
        draw: ns("IssueDrawCommandsStart", "SwapBuffers"),
        swap: ns("SwapBuffers", "FrameCompleted"),
        gpu: col("GpuCompleted") >= 0 ? ns("SwapBuffers", "GpuCompleted") : null,
      });
    }
  }
  return rows;
}
const pct = (xs, p) => {
  const s = xs.filter((x) => Number.isFinite(x)).sort((a, b) => a - b);
  return s.length ? Math.round(s[Math.min(s.length - 1, Math.floor(p * s.length))] * 10) / 10 : null;
};
function gfxSummary(text) {
  const g = (re) => Number(text.match(re)?.[1] ?? NaN);
  return {
    frames: g(/Total frames rendered: (\d+)/),
    jankyPct: g(/Janky frames: \d+ \(([\d.]+)%\)/),
    p50: g(/50th percentile: (\d+)ms/),
    p90: g(/90th percentile: (\d+)ms/),
    p95: g(/95th percentile: (\d+)ms/),
    p99: g(/99th percentile: (\d+)ms/),
    slowUi: g(/Number Slow UI thread: (\d+)/),
    slowBitmap: g(/Number Slow bitmap uploads: (\d+)/),
    slowDraw: g(/Number Slow issue draw commands: (\d+)/),
    frameDeadlineMissed: g(/Number Frame deadline missed: (\d+)/),
    gpuP50: g(/50th gpu percentile: (\d+)ms/),
    gpuP90: g(/90th gpu percentile: (\d+)ms/),
    gpuP99: g(/99th gpu percentile: (\d+)ms/),
  };
}

// ---- one run ---------------------------------------------------------------------
const PAGE_PROBE = `(()=>{
  const w = window.__probe = { f: [], loaf: 0, loafMs: 0, run: true };
  let last = performance.now();
  const tick = (t) => { w.f.push(t - last); last = t; if (w.run) requestAnimationFrame(tick); };
  requestAnimationFrame(tick);
  try { w.obs = new PerformanceObserver((l) => { for (const e of l.getEntries()) { w.loaf++; w.loafMs += e.blockingDuration || 0; } }); w.obs.observe({ type: 'long-animation-frame' }); } catch {}
  return 1;
})()`;
const PAGE_READ = `(()=>{
  const w = window.__probe; w.run = false; try { w.obs && w.obs.disconnect(); } catch {}
  const f = w.f.slice(2).sort((a, b) => a - b);
  const q = (p) => f.length ? Math.round(f[Math.min(f.length - 1, Math.floor(p * f.length))]) : null;
  const m = document.getElementById('main-content');
  return { rafN: f.length, rafP50: q(0.5), rafP95: q(0.95), rafMax: q(1), rafSlowPct: f.length ? Math.round(100 * f.filter((x) => x > 25).length / f.length) : null,
    loaf: w.loaf, loafBlockingMs: Math.round(w.loafMs), scrollTop: m ? Math.round(m.scrollTop) : null, scrollMax: m ? m.scrollHeight - m.clientHeight : null,
    tier: document.documentElement.dataset.uiTier || null, domNodes: document.getElementsByTagName('*').length, path: location.pathname };
})()`;

const metrics = async () => Object.fromEntries((await cdp.send("Performance.getMetrics")).result.metrics.map((m) => [m.name, m.value]));

async function run({ route, variant, round, warmup }) {
  const v = VARIANTS_ALL[variant];
  await cdp.evaluate(`(()=>{document.querySelectorAll('style[data-matrix]').forEach(s=>s.remove());
    history.pushState({}, '', '/__matrix_blank'); dispatchEvent(new PopStateEvent('popstate'));
    return 1})()`);
  await sleep(300);
  await cdp.evaluate(`(()=>{history.pushState({}, '', ${JSON.stringify(route)}); dispatchEvent(new PopStateEvent('popstate')); return 1})()`);
  await sleep(SETTLE_MS);
  if (v.css) await cdp.evaluate(`(()=>{const s=document.createElement('style');s.dataset.matrix='1';s.textContent=${JSON.stringify(v.css)};document.head.append(s);return 1})()`);
  if (v.js) await cdp.evaluate(v.js);
  await sleep(1500);
  await cdp.evaluate(`(()=>{const m=document.getElementById('main-content'); if(m) m.scrollTop=0; return 1})()`);
  await sleep(500);

  sh("shell", "dumpsys", "gfxinfo", PKG, "reset");
  const m0 = await metrics();
  wsFrames = 0;
  await cdp.evaluate(PAGE_PROBE);
  const t0 = Date.now();
  for (let i = 0; i < SWIPES; i++) {
    sh("shell", "input", "swipe", "540", "1900", "540", "500", "280");
    await sleep(700);
    sh("shell", "input", "swipe", "540", "500", "540", "1900", "280");
    await sleep(700);
  }
  const page = await cdp.evaluate(PAGE_READ);
  const m1 = await metrics();
  const gfx = sh("shell", "dumpsys", "gfxinfo", PKG, "framestats");
  const frames = parseFramestats(gfx);
  const phase = (k) => ({ p50: pct(frames.map((f) => f[k]), 0.5), p90: pct(frames.map((f) => f[k]), 0.9) });
  const meminfo = sh("shell", "cat", "/proc/meminfo");
  const memKB = (k) => Number(meminfo.match(new RegExp(`${k}:\\s+(\\d+)`))?.[1] ?? NaN);
  const pss = Number(sh("shell", "dumpsys", "meminfo", PKG).match(/TOTAL PSS:\s+(\d+)/)?.[1] ?? NaN);
  const rec = {
    t: new Date().toISOString(),
    sinceLaunchS: launchedAt ? Math.round((Date.now() - launchedAt) / 1000) : null,
    round, warmup, route, variant,
    gfx: gfxSummary(gfx),
    phases: { n: frames.length, total: phase("total"), input: phase("input"), anim: phase("anim"), traversal: phase("traversal"), sync: phase("sync"), draw: phase("draw"), swap: phase("swap"), gpu: phase("gpu") },
    page,
    work: {
      styleRecalcs: m1.RecalcStyleCount - m0.RecalcStyleCount,
      styleMs: Math.round((m1.RecalcStyleDuration - m0.RecalcStyleDuration) * 1000),
      layouts: m1.LayoutCount - m0.LayoutCount,
      layoutMs: Math.round((m1.LayoutDuration - m0.LayoutDuration) * 1000),
      scriptMs: Math.round((m1.ScriptDuration - m0.ScriptDuration) * 1000),
      taskMs: Math.round((m1.TaskDuration - m0.TaskDuration) * 1000),
      jsHeapMB: Math.round(m1.JSHeapUsedSize / 1e6),
    },
    layers: lastLayers,
    wsFramesDuringSwipes: wsFrames,
    swipeWallMs: Date.now() - t0,
    pssMB: Math.round(pss / 1024),
    env: {
      hostLoad1: Math.round(loadavg()[0] * 10) / 10,
      hostCpus: navigator.hardwareConcurrency ?? null,
      guestMemAvailMB: Math.round(memKB("MemAvailable") / 1024),
      guestSwapUsedMB: Math.round((memKB("SwapTotal") - memKB("SwapFree")) / 1024),
    },
  };
  appendFileSync(runsFile, JSON.stringify(rec) + "\n");
  const g = rec.gfx;
  console.log(
    `${warmup ? "warm-up" : `round ${round}`}  ${route.padEnd(11)} ${variant.padEnd(15)} gfx p50=${g.p50} p90=${g.p90} p99=${g.p99} janky=${g.jankyPct}% | draw p50=${rec.phases.draw.p50} sync p50=${rec.phases.sync.p50} gpu p50=${rec.phases.gpu.p50} | raf slow=${page.rafSlowPct}% | style ${rec.work.styleRecalcs}/${rec.work.styleMs}ms layout ${rec.work.layouts}/${rec.work.layoutMs}ms | layers=${rec.layers} ws=${rec.wsFramesDuringSwipes} | +${rec.sinceLaunchS}s | host load ${rec.env.hostLoad1} guest avail ${rec.env.guestMemAvailMB}MB swap ${rec.env.guestSwapUsedMB}MB`,
  );
  return rec;
}

writeFileSync(`${outDir}/config.json`, JSON.stringify({ PKG, ROUTES, VARIANTS, PAIRS, SETTLE_MS, SWIPES, variantsCss: Object.fromEntries(VARIANTS.map((v) => [v, VARIANTS_ALL[v]])) }, null, 2));
console.log(`matrix → ${outDir}`);
const all = [];
for (const route of ROUTES) {
  for (const variant of VARIANTS) all.push(await run({ route, variant, round: 0, warmup: true }));
  for (let round = 1; round <= PAIRS; round++) {
    const k = (round - 1) % VARIANTS.length;
    const order = [...VARIANTS.slice(k), ...VARIANTS.slice(0, k)];
    for (const variant of order) all.push(await run({ route, variant, round, warmup: false }));
  }
}
await cdp.evaluate(`(()=>{document.querySelectorAll('style[data-matrix]').forEach(s=>s.remove());return 1})()`);
cdp.close();

// ---- summary ---------------------------------------------------------------------
const lines = ["route | variant | runs | gfx p50 (range) | p90 median | janky % (range) | draw p50 median | gpu p50 median | raf slow % median"];
const med = (xs) => pct(xs, 0.5);
for (const route of ROUTES) {
  for (const variant of VARIANTS) {
    const rs = all.filter((r) => !r.warmup && r.route === route && r.variant === variant);
    const p50s = rs.map((r) => r.gfx.p50);
    const jk = rs.map((r) => r.gfx.jankyPct);
    lines.push(
      `${route} | ${variant} | ${rs.length} | ${med(p50s)} (${Math.min(...p50s)}–${Math.max(...p50s)}) | ${med(rs.map((r) => r.gfx.p90))} | ${med(jk)} (${Math.min(...jk)}–${Math.max(...jk)}) | ${med(rs.map((r) => r.phases.draw.p50))} | ${med(rs.map((r) => r.phases.gpu.p50))} | ${med(rs.map((r) => r.page.rafSlowPct))}`,
    );
  }
}
writeFileSync(`${outDir}/summary.md`, lines.join("\n") + "\n");
console.log("\n" + lines.join("\n"));
