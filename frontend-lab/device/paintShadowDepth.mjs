#!/usr/bin/env node
/**
 * What a card shadow costs to raster, measured on the device.
 *
 * Follow-up to layerPaintProfile.mjs (2026-09-17), which found the Orders
 * scrolling layer's paint cost collapses ~80% when EITHER the card shadow or the
 * card radius is removed. This isolates the shadow's shape while holding the
 * *set of elements* fixed: every variant restyles exactly the elements that
 * already have a box-shadow (found at runtime), so variants differ only in the
 * shadow they draw, never in how many elements draw one.
 *
 *   node device/paintShadowDepth.mjs
 *   ROUTE=/orders REPEATS=3 node device/paintShadowDepth.mjs
 */
import { mkdirSync, writeFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const PKG = process.env.PKG ?? "com.ovasabi.choosechow";
const ROUTE = process.env.ROUTE ?? "/orders";
const REPEATS = Number(process.env.REPEATS ?? 3);
const TIER = process.env.TIER ?? "balanced";
const lab = fileURLToPath(new URL("..", import.meta.url));
const outDir = process.env.OUT ?? `${lab}results/device/shadow-${new Date().toISOString().replace(/[:.]/g, "-")}`;
mkdirSync(outDir, { recursive: true });

/** Each variant is applied inline to the elements that natively carry a shadow. */
const VARIANTS = {
  baseline: null, // restore the page's own shadow
  none: { shadow: "none" },
  "border-1px": { shadow: "none", border: "1px solid rgba(23,39,62,0.12)" },
  "blur-0-hairline": { shadow: "0 1px 0 rgba(23,39,62,0.18)" },
  "blur-1": { shadow: "0 1px 1px rgba(23,39,62,0.18)" },
  "blur-3": { shadow: "0 1px 3px rgba(23,39,62,0.16)" },
  "blur-8": { shadow: "0 2px 8px rgba(23,39,62,0.14)" },
  "blur-20": { shadow: "0 8px 20px rgba(23,39,62,0.11)" },
  "blur-20-spread-neg": { shadow: "0 8px 20px -4px rgba(23,39,62,0.11)" },
  "two-layer": { shadow: "0 2px 4px rgba(23,39,62,0.05), 0 8px 20px -4px rgba(23,39,62,0.11)" },
  "blur-8-square": { shadow: "0 2px 8px rgba(23,39,62,0.14)", radius: "0px" },
  "none-square": { shadow: "none", radius: "0px" },
};
const NAMES = (process.env.VARIANTS ?? Object.keys(VARIANTS).join(",")).split(",");

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

await send("LayerTree.enable");
const contentLayer = () => layers.filter((l) => l.width > 300 && l.height > 300).sort((a, b) => b.width * b.height - a.width * a.height)[0];

/** Tag the shadowed elements once, remembering their own shadow and radius. */
const TAG = `(()=>{
  const tagged = [];
  for (const el of document.querySelectorAll('#main-content *')) {
    const cs = getComputedStyle(el);
    if (cs.boxShadow && cs.boxShadow !== 'none') { el.dataset.shadowProbe = '1'; el.__own = { s: cs.boxShadow, r: cs.borderRadius }; tagged.push(el); }
  }
  window.__shadowed = tagged;
  return tagged.length;
})()`;
const apply = (v) => `(()=>{
  const conf = ${JSON.stringify(v)};
  for (const el of window.__shadowed) {
    if (!conf) { el.style.boxShadow = ''; el.style.borderRadius = ''; el.style.border = ''; continue; }
    el.style.boxShadow = conf.shadow;
    if (conf.radius !== undefined) el.style.borderRadius = conf.radius; else el.style.borderRadius = '';
    if (conf.border !== undefined) el.style.border = conf.border; else el.style.border = '';
  }
  return window.__shadowed.length;
})()`;

async function paintCost() {
  const runs = [];
  for (let i = 0; i < REPEATS; i++) {
    await evaluate(`(()=>{document.getElementById('main-content').scrollTop=${200 + i * 140};return 1})()`);
    await sleep(700);
    const layer = contentLayer();
    const snap = await send("LayerTree.makeSnapshot", { layerId: layer.layerId });
    const snapshotId = snap.result?.snapshotId;
    if (!snapshotId) continue;
    const prof = await send("LayerTree.profileSnapshot", { snapshotId, minRepeatCount: 3, minDuration: 0.3 });
    await send("LayerTree.releaseSnapshot", { snapshotId });
    const per = (prof.result?.timings ?? []).map((ops) => ops.reduce((s, t) => s + t, 0) * 1000).sort((a, b) => a - b);
    if (per.length) runs.push(Math.round(per[0] * 100) / 100);
  }
  runs.sort((a, b) => a - b);
  return { median: runs[Math.floor(runs.length / 2)] ?? null, min: runs[0] ?? null, max: runs[runs.length - 1] ?? null, runs };
}

await evaluate(`(()=>{document.documentElement.setAttribute('data-ui-tier','${TIER}');history.pushState({},'',${JSON.stringify(ROUTE)});dispatchEvent(new PopStateEvent('popstate'));return 1})()`);
await sleep(6000);
const shadowed = await evaluate(TAG);
console.log(`route ${ROUTE}  tier ${TIER}  elements with a shadow: ${shadowed}`);

const all = [];
for (let round = 1; round <= 2; round++) {
  const order = round === 1 ? NAMES : [...NAMES].reverse();
  for (const name of order) {
    await evaluate(apply(VARIANTS[name]));
    await sleep(1000);
    const r = await paintCost();
    all.push({ round, name, ...r });
    console.log(`round ${round}  ${name.padEnd(20)} ${String(r.median).padStart(7)} ms  (${r.min}–${r.max})`);
  }
}
await evaluate(apply(null));
ws.close();
writeFileSync(`${outDir}/shadow.json`, JSON.stringify({ route: ROUTE, tier: TIER, shadowed, results: all }, null, 2));
const by = new Map();
for (const r of all) by.set(r.name, [...(by.get(r.name) ?? []), r.median]);
console.log("\nvariant | medians | best");
for (const [k, v] of by) console.log(`${k} | ${v.join(", ")} | ${Math.min(...v)}`);
console.log(`\n→ ${outDir}/shadow.json`);
