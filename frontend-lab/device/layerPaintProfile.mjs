#!/usr/bin/env node
/**
 * Per-layer raster cost on the device, measured by the compositor itself.
 *
 * Why (2026-09-17): whole-frame numbers could not separate variants on either
 * emulator lane — the host-GPU lane draws everything inside budget (17 ms
 * regardless of content) and the SwiftShader lane saturates on full-screen fill
 * (~60 ms regardless of content). `LayerTree.makeSnapshot` + `profileSnapshot`
 * replays a layer's recorded paint ops N times and returns per-op timings, so it
 * measures what the PAGE costs to raster, independent of how fast the device's
 * fill is. That is the quantity a weak phone GPU is short of.
 *
 * For each variant it prints the scrolling layer's replay cost (median of
 * PROFILE_REPEATS snapshots), so variants can be ranked by the raster work they
 * remove. Interleaved and repeated like every other device measurement.
 *
 *   node device/layerPaintProfile.mjs
 *   ROUTE=/orders VARIANTS=baseline,no-shadow-blur REPEATS=5 node device/layerPaintProfile.mjs
 */
import { mkdirSync, writeFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const PKG = process.env.PKG ?? "com.ovasabi.choosechow";
const ROUTE = process.env.ROUTE ?? "/orders";
const REPEATS = Number(process.env.REPEATS ?? 5);
const lab = fileURLToPath(new URL("..", import.meta.url));
const outDir = process.env.OUT ?? `${lab}results/device/paint-${new Date().toISOString().replace(/[:.]/g, "-")}`;
mkdirSync(outDir, { recursive: true });

const HEADER = "#main-content > * > header";
const VARIANTS_ALL = {
  baseline: "",
  "no-shadow": `*,*::before,*::after{box-shadow:none!important}`,
  "no-blur": `*,*::before,*::after{backdrop-filter:none!important;-webkit-backdrop-filter:none!important;filter:none!important}`,
  "no-radius": `*,*::before,*::after{border-radius:0!important}`,
  "no-borders": `*,*::before,*::after{border-color:transparent!important}`,
  "no-text": `#main-content{color:transparent!important} #main-content *{color:transparent!important}`,
  "header-flat": `${HEADER}{border-radius:0!important;box-shadow:none!important;background:#9f1d3a!important}`,
  "s-layer1-only": `#main-content div{box-shadow:0 2px 4px rgba(23,39,62,0.05)!important}`,
  "s-layer2-only": `#main-content div{box-shadow:0 8px 20px -4px rgba(23,39,62,0.11)!important}`,
  "s-no-spread": `#main-content div{box-shadow:0 2px 4px rgba(23,39,62,0.05),0 8px 20px rgba(23,39,62,0.11)!important}`,
  "s-small-blur": `#main-content div{box-shadow:0 2px 6px rgba(23,39,62,0.11)!important}`,
  "s-low-tier-token": `#main-content div{box-shadow:0 1px 3px rgba(45,20,10,0.08)!important}`,
  "s-border-only": `#main-content div{box-shadow:none!important;border:1px solid rgba(23,39,62,0.12)!important}`,
  "tier-low-attr": ``,
  "flat-cards": `#main-content div{box-shadow:none!important;border-radius:0!important}`,
};
const VARIANTS = (process.env.VARIANTS ?? Object.keys(VARIANTS_ALL).join(",")).split(",");

const adb = (...a) => execFileSync("adb", a, { encoding: "utf8", maxBuffer: 64 * 1024 * 1024 });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const pid = adb("shell", "pidof", PKG).trim();
execFileSync("adb", ["forward", "--remove-all"]);
execFileSync("adb", ["forward", "tcp:9222", `localabstract:webview_devtools_remote_${pid}`]);
const target = (await (await fetch("http://127.0.0.1:9222/json")).json()).find((t) => t.type === "page");
const ws = new WebSocket(target.webSocketDebuggerUrl);
let id = 1;
const pending = new Map();
let layers = [];
ws.onmessage = (e) => {
  const m = JSON.parse(e.data);
  if (m.id && pending.has(m.id)) (pending.get(m.id)(m), pending.delete(m.id));
  else if (m.method === "LayerTree.layerTreeDidChange" && m.params?.layers) layers = m.params.layers;
};
await new Promise((res, rej) => ((ws.onopen = res), (ws.onerror = rej)));
const send = (method, params = {}) =>
  new Promise((res) => {
    const i = id++;
    pending.set(i, res);
    ws.send(JSON.stringify({ id: i, method, params }));
  });
const evaluate = async (expression) => (await send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true })).result?.result?.value;

await send("DOM.enable");
await send("LayerTree.enable");

/** The layer that actually holds the scrolling page content: the biggest one. */
const contentLayer = () =>
  layers
    .filter((l) => l.width > 300 && l.height > 300)
    .sort((a, b) => b.width * b.height - a.width * a.height)[0];

async function paintCost(label) {
  const results = [];
  for (let i = 0; i < REPEATS; i++) {
    // Nudge the scroll so the layer re-records between repeats.
    await evaluate(`(()=>{const m=document.getElementById('main-content');m.scrollTop=${200 + i * 120};return 1})()`);
    await sleep(700);
    const layer = contentLayer();
    if (!layer) return { label, error: "no layer" };
    const snap = await send("LayerTree.makeSnapshot", { layerId: layer.layerId });
    const snapshotId = snap.result?.snapshotId;
    if (!snapshotId) {
      results.push(null);
      continue;
    }
    const prof = await send("LayerTree.profileSnapshot", { snapshotId, minRepeatCount: 3, minDuration: 0.3 });
    await send("LayerTree.releaseSnapshot", { snapshotId });
    const timings = prof.result?.timings ?? [];
    // Each entry is one replay: an array of per-op times in seconds.
    const perReplayMs = timings.map((ops) => ops.reduce((s, t) => s + t, 0) * 1000);
    perReplayMs.sort((a, b) => a - b);
    const best = perReplayMs[0] ?? null; // fastest replay = least noise
    results.push(best === null ? null : Math.round(best * 100) / 100);
  }
  const ok = results.filter((x) => x !== null).sort((a, b) => a - b);
  return { label, repeats: results, median: ok.length ? ok[Math.floor(ok.length / 2)] : null, min: ok[0] ?? null, max: ok[ok.length - 1] ?? null, layer: contentLayer() };
}

await evaluate(`(()=>{history.pushState({},'',${JSON.stringify(ROUTE)});dispatchEvent(new PopStateEvent('popstate'));return 1})()`);
await sleep(6000);

const all = [];
for (let round = 1; round <= 2; round++) {
  const order = round === 1 ? VARIANTS : [...VARIANTS].reverse();
  for (const v of order) {
    await evaluate(`(()=>{document.documentElement.setAttribute('data-ui-tier', ${JSON.stringify(v === "tier-low-attr" ? "low_power" : "balanced")});document.querySelectorAll('style[data-paint]').forEach(s=>s.remove());${VARIANTS_ALL[v] ? `const s=document.createElement('style');s.dataset.paint='1';s.textContent=${JSON.stringify(VARIANTS_ALL[v])};document.head.append(s);` : ""}return 1})()`);
    await sleep(1200);
    const r = await paintCost(v);
    r.round = round;
    all.push(r);
    console.log(`round ${round}  ${v.padEnd(14)} paint replay median=${r.median} ms  (min ${r.min}, max ${r.max})  layer ${r.layer?.width}x${r.layer?.height}`);
  }
}
await evaluate(`(()=>{document.querySelectorAll('style[data-paint]').forEach(s=>s.remove());return 1})()`);
ws.close();
writeFileSync(`${outDir}/paint.json`, JSON.stringify({ route: ROUTE, repeats: REPEATS, results: all }, null, 2));
const byVariant = new Map();
for (const r of all) byVariant.set(r.label, [...(byVariant.get(r.label) ?? []), r.median].filter((x) => x !== null));
console.log("\nvariant | medians | best");
for (const [k, v] of byVariant) console.log(`${k} | ${v.join(", ")} | ${Math.min(...v)}`);
console.log(`\n→ ${outDir}/paint.json`);
