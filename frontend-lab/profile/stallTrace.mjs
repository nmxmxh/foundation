#!/usr/bin/env node
/**
 * The P3 evidence gate: does motion keep moving while the main thread is busy?
 *
 * Research doc §5 P3: "during a forced main-thread block, compositor motion
 * keeps its frame interval". The first attempt read an animation's
 * `currentTime` across a synchronous block — which cannot advance on the thread
 * being blocked, so it proved nothing and was removed (§14.2). This measures
 * what the viewer sees instead: frames the compositor presents, captured with
 * `Page.startScreencast` (produced and acknowledged outside the renderer's main
 * thread), and whether consecutive frames differ — i.e. whether anything moved.
 *
 * Three ways to move one box sideways, in one page each:
 *   css-keyframes  a CSS animation on `transform`        (compositor)
 *   waapi          element.animate() on `transform`      (compositor)
 *   js-raf         requestAnimationFrame writing style    (main thread — how
 *                  framer-motion drove x/y/scale, §14.2)
 *
 * For each: let it run, then block the main thread for BLOCK_MS inside a
 * single task, and count the screencast frames that arrive inside the block
 * and changed from the one before. Real GPU (ANGLE/Metal).
 *
 *   node profile/stallTrace.mjs
 *   STALL_BLOCK_MS=600 STALL_RUNS=5 node profile/stallTrace.mjs
 */
import { createHash } from "node:crypto";
import { createServer } from "node:http";
import { mkdirSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const BLOCK_MS = Number(process.env.STALL_BLOCK_MS ?? 400);
const RUNS = Number(process.env.STALL_RUNS ?? 3);
const CPU_THROTTLE = Number(process.env.STALL_CPU_THROTTLE ?? 1);
const WS_ENDPOINT = process.env.STALL_CDP_ENDPOINT; // optional: attach to a device browser instead

const page = (variant) => `<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<style>
  html,body{margin:0;background:#fff}
  #box{width:80px;height:80px;background:#2563eb;border-radius:12px;position:absolute;top:60px;left:20px}
  .css #box{animation:slide 1s linear infinite alternate}
  @keyframes slide{from{transform:translateX(0)}to{transform:translateX(240px)}}
</style></head>
<body class="${variant === "css-keyframes" ? "css" : ""}"><div id="box"></div>
<script>
(() => {
  const box = document.getElementById("box");
  const variant = ${JSON.stringify(variant)};
  if (variant === "waapi") {
    box.animate([{transform:"translateX(0)"},{transform:"translateX(240px)"}], {duration:1000, iterations:Infinity, direction:"alternate", easing:"linear"});
  }
  if (variant === "js-raf") {
    const t0 = performance.now();
    const tick = (now) => {
      const p = ((now - t0) % 2000) / 1000;
      const x = (p <= 1 ? p : 2 - p) * 240;
      box.style.transform = "translateX(" + x + "px)";
      requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  }
  window.block = (ms) => { const end = performance.now() + ms; while (performance.now() < end) {} return true; };
  window.ready = true;
})();
</script></body></html>`;

const VARIANTS = ["css-keyframes", "waapi", "js-raf"];
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const server = createServer((req, res) => {
  const variant = new URL(req.url, "http://x").searchParams.get("v") ?? "css-keyframes";
  res.writeHead(200, { "content-type": "text/html" }).end(page(variant));
}).listen(Number(process.env.STALL_PORT ?? 0));
const port = server.address().port;
const origin = process.env.STALL_ORIGIN ?? `http://localhost:${port}`;

/*
 * Device mode (STALL_DEVICE_CDP=http://127.0.0.1:9222): drive an Android
 * WebView's existing page over raw CDP instead of launching Chromium. Setup:
 *   adb forward tcp:9222 localabstract:webview_devtools_remote_<pid>
 *   STALL_DEVICE_CDP=http://127.0.0.1:9222 node profile/stallTrace.mjs
 * The page returns to STALL_DEVICE_RETURN (its original URL by default) afterwards.
 */
const DEVICE = process.env.STALL_DEVICE_CDP;

const rawSession = async (endpoint) => {
  const target = (await (await fetch(`${endpoint}/json`)).json()).find((t) => t.type === "page");
  if (!target) throw new Error(`no page target at ${endpoint}`);
  const ws = new WebSocket(target.webSocketDebuggerUrl);
  let nextId = 1;
  const pending = new Map();
  const handlers = new Map();
  ws.onmessage = (event) => {
    const msg = JSON.parse(event.data);
    if (msg.id && pending.has(msg.id)) {
      const { resolve, reject } = pending.get(msg.id);
      pending.delete(msg.id);
      msg.error ? reject(new Error(msg.error.message)) : resolve(msg.result);
    } else for (const handler of handlers.get(msg.method) ?? []) handler(msg.params);
  };
  await new Promise((resolve, reject) => ((ws.onopen = resolve), (ws.onerror = reject)));
  const send = (method, params = {}) =>
    new Promise((resolve, reject) => {
      const id = nextId++;
      pending.set(id, { resolve, reject });
      ws.send(JSON.stringify({ id, method, params }));
    });
  const on = (method, handler) => handlers.set(method, [...(handlers.get(method) ?? []), handler]);
  const evaluate = async (expression) => (await send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true })).result?.value;
  return { url: target.url, send, on, evaluate, close: () => ws.close() };
};

/** One page per variant, behind the same interface in both modes. */
const openTab = async (variant) => {
  if (DEVICE) {
    const session = await rawSession(DEVICE);
    await session.send("Page.enable");
    // An app WebView blocks cleartext http (Android network security config), so
    // the page is written into a fresh about:blank document instead of fetched.
    const loaded = new Promise((resolve) => session.on("Page.loadEventFired", resolve));
    await session.send("Page.navigate", { url: "about:blank" });
    await Promise.race([loaded, sleep(5_000)]);
    await session.evaluate(`document.open(); document.write(${JSON.stringify(page(variant))}); document.close();`);
    return {
      cdp: session,
      evaluateBlock: (ms) => session.evaluate(`(() => { const start = performance.timeOrigin + performance.now(); window.block(${ms}); return { start, end: performance.timeOrigin + performance.now() }; })()`),
      waitReady: async () => {
        for (let i = 0; i < 100 && !(await session.evaluate("window.ready === true")); i += 1) await new Promise((r) => setTimeout(r, 100));
      },
      close: async () => session.close(),
    };
  }
  const tab = await context.newPage();
  const cdp = await context.newCDPSession(tab);
  await tab.goto(`${origin}/?v=${variant}`);
  return {
    cdp,
    evaluateBlock: (ms) =>
      tab.evaluate((blockMs) => {
        const start = performance.timeOrigin + performance.now();
        window.block(blockMs);
        return { start, end: performance.timeOrigin + performance.now() };
      }, ms),
    waitReady: () => tab.waitForFunction(() => window.ready === true),
    close: async () => {
      await cdp.detach().catch(() => undefined);
      await tab.close();
    },
  };
};

const deviceReturnUrl = DEVICE ? process.env.STALL_DEVICE_RETURN ?? (await rawSession(DEVICE).then((s) => (s.close(), s.url))) : null;
const browser = DEVICE ? null : WS_ENDPOINT ? await chromium.connectOverCDP(WS_ENDPOINT) : await chromium.launch({ headless: true, args: ["--use-angle=metal"] });
const context = DEVICE ? null : WS_ENDPOINT ? browser.contexts()[0] : await browser.newContext({ viewport: { width: 400, height: 240 } });

const results = [];
try {
  for (let run = 0; run < RUNS; run += 1) {
    for (const variant of run % 2 ? [...VARIANTS].reverse() : VARIANTS) {
      const tab = await openTab(variant);
      const { cdp } = tab;
      if (CPU_THROTTLE > 1) await cdp.send("Emulation.setCPUThrottlingRate", { rate: CPU_THROTTLE });
      await tab.waitReady();
      await sleep(600);

      const frames = [];
      cdp.on("Page.screencastFrame", ({ data, metadata, sessionId }) => {
        frames.push({ at: metadata.timestamp * 1000, hash: createHash("sha1").update(data).digest("hex") });
        cdp.send("Page.screencastFrameAck", { sessionId }).catch(() => undefined);
      });
      await cdp.send("Page.startScreencast", { format: "jpeg", quality: 60, everyNthFrame: 1, maxWidth: 540, maxHeight: 400 });
      await sleep(BLOCK_MS + 200);

      // The block, timed on the page's own clock (wall-clock ms, same base as the screencast timestamps).
      const blockWindow = await tab.evaluateBlock(BLOCK_MS);
      await sleep(500);
      await cdp.send("Page.stopScreencast");

      const inside = frames.filter((f) => f.at >= blockWindow.start && f.at <= blockWindow.end);
      let changed = 0;
      let previous = frames.filter((f) => f.at < blockWindow.start).at(-1)?.hash;
      for (const f of inside) {
        if (f.hash !== previous) changed += 1;
        previous = f.hash;
      }
      const before = frames.filter((f) => f.at < blockWindow.start && f.at >= blockWindow.start - BLOCK_MS);
      let changedBefore = 0;
      for (let i = 1; i < before.length; i += 1) if (before[i].hash !== before[i - 1].hash) changedBefore += 1;
      results.push({ run, variant, blockMs: Math.round(blockWindow.end - blockWindow.start), framesInBlock: inside.length, changedFramesInBlock: changed, changedFramesSameSpanBefore: changedBefore });
      await tab.close();
    }
  }
} finally {
  if (DEVICE && deviceReturnUrl) {
    const session = await rawSession(DEVICE);
    await session.send("Page.navigate", { url: deviceReturnUrl }).catch(() => undefined);
    session.close();
  } else if (browser && !WS_ENDPOINT) await browser.close();
  server.close();
}

const median = (xs) => [...xs].sort((a, b) => a - b)[Math.floor(xs.length / 2)];
const summary = Object.fromEntries(
  VARIANTS.map((v) => {
    // A run whose screencast delivered nothing before the block measured the
    // capture pipeline, not the page (seen on the emulator after a few runs).
    const rows = results.filter((r) => r.variant === v && r.changedFramesSameSpanBefore > 0);
    return [v, { changedFramesInBlock: median(rows.map((r) => r.changedFramesInBlock)), framesInBlock: median(rows.map((r) => r.framesInBlock)), changedFramesSameSpanBefore: median(rows.map((r) => r.changedFramesSameSpanBefore)) }];
  }),
);
const bundle = { kind: "frontend-lab.stall-trace", capturedAt: new Date().toISOString(), target: DEVICE ? `android webview (${DEVICE})` : WS_ENDPOINT ? "remote browser (CDP)" : "headless Chromium, ANGLE Metal", blockMs: BLOCK_MS, cpuThrottle: CPU_THROTTLE, runs: RUNS, summary, results };
const dir = fileURLToPath(new URL(`../results/stall/`, import.meta.url));
mkdirSync(dir, { recursive: true });
writeFileSync(`${dir}${bundle.capturedAt.replace(/[:.]/g, "-")}.json`, `${JSON.stringify(bundle, null, 2)}\n`);
console.log(JSON.stringify({ target: bundle.target, blockMs: BLOCK_MS, cpuThrottle: CPU_THROTTLE, summary }, null, 2));
