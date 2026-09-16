#!/usr/bin/env node
/**
 * Load profile: bytes, layout stability, paint timing and style-injection cost
 * of a production build on an emulated phone — the numbers a scroll profile
 * cannot see.
 *
 * Two targets:
 *
 * - The lab feed (default). Builds profile/index.html in several variants (see
 *   VARIANT_CONFIG) and loads them interleaved.
 * - A real app's production build (LOAD_APP=profile/apps/<app>.mjs), served
 *   as its edge would serve it: brotli, SPA fallback, per-route HTML, plus the
 *   API fixtures and media the app config declares, so the page renders real
 *   content over the same throttled network. Requests the config does not
 *   answer are listed per run, so a missing fixture is visible, not silent.
 *
 * Each variant gets its own origin. Every run records:
 *
 * - bytes: encoded (over the wire) and decoded, per resource type;
 * - FCP, LCP (and which element it was), long-task total (TBT-style, >50 ms);
 * - CLS during load, a scripted scroll, and a tier change — every layout shift
 *   with the elements that moved, so a shift has an address;
 * - CSS rules at first contentful paint vs after load and after the scroll.
 *
 * Devices (LOAD_DEVICE):
 *   mid    412×915 @2.625, CPU 4x, Slow 4G (150 ms RTT, 1.6 Mbps)  — Lighthouse mobile
 *   small  360×640 @2, CPU 6x, 3G (300 ms RTT, 750 kbps down)     — entry Android on a weak link
 *
 *   node profile/loadProfile.mjs
 *   LOAD_DEVICE=small LOAD_RUNS=5 node profile/loadProfile.mjs
 *   LOAD_APP=profile/apps/choosechow.mjs LOAD_DEVICE=small node profile/loadProfile.mjs
 */

import { createServer } from "node:http";
import { existsSync, mkdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { extname, isAbsolute, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { brotliCompressSync, constants as zlibConstants } from "node:zlib";
import { chromium } from "playwright";
import { build } from "vite";
import { prerenderShell } from "../../frontend-kit/ts/vite/prerenderShell.ts";

const lab = fileURLToPath(new URL("..", import.meta.url));
const RUNS = Number(process.env.LOAD_RUNS ?? 3);
const SECTIONS = Number(process.env.LOAD_SECTIONS ?? 40);
const COMPRESS = process.env.LOAD_COMPRESS !== "0";

const DEVICES = {
  mid: {
    viewport: { width: 412, height: 915 },
    deviceScaleFactor: 2.625,
    cpu: 4,
    network: { offline: false, latency: 150, downloadThroughput: (1.6 * 1024 * 1024) / 8, uploadThroughput: (750 * 1024) / 8 },
    label: "412x915 @2.625x, CPU 4x, Slow 4G (150 ms, 1.6 Mbps)",
  },
  small: {
    viewport: { width: 360, height: 640 },
    deviceScaleFactor: 2,
    cpu: 6,
    network: { offline: false, latency: 300, downloadThroughput: (750 * 1024) / 8, uploadThroughput: (250 * 1024) / 8 },
    label: "360x640 @2x, CPU 6x, 3G (300 ms, 750 kbps)",
  },
};
const DEVICE_NAME = process.env.LOAD_DEVICE ?? "mid";
const DEVICE = DEVICES[DEVICE_NAME];
if (!DEVICE) throw new Error(`unknown LOAD_DEVICE ${DEVICE_NAME}`);
const CPU_THROTTLE = Number(process.env.LOAD_CPU_THROTTLE ?? DEVICE.cpu);

const types = {
  ".html": "text/html",
  ".js": "text/javascript",
  ".css": "text/css",
  ".json": "application/json",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".webp": "image/webp",
  ".avif": "image/avif",
  ".woff2": "font/woff2",
  ".ico": "image/x-icon",
  ".webmanifest": "application/manifest+json",
};

/** A static server for one variant: brotli for text, like a production edge. */
const serve = ({ root, spa = false, isolation = false, fixtures = {}, mounts = {} }) => {
  const missing = new Set();
  const send = (req, res, status, body, type) => {
    const brotli = COMPRESS && /br/.test(req.headers["accept-encoding"] ?? "") && /^(text\/|application\/(json|manifest)|image\/svg)/.test(type);
    res
      .writeHead(status, {
        "content-type": type,
        "cache-control": "no-store",
        ...(isolation ? { "Cross-Origin-Opener-Policy": "same-origin", "Cross-Origin-Embedder-Policy": "require-corp" } : {}),
        ...(brotli ? { "content-encoding": "br", vary: "accept-encoding" } : {}),
      })
      .end(brotli ? brotliCompressSync(body, { params: { [zlibConstants.BROTLI_PARAM_QUALITY]: 11 } }) : body);
  };
  const file = (path) => existsSync(path) && statSync(path).isFile() && path;
  const server = createServer((req, res) => {
    const pathname = decodeURIComponent((req.url ?? "/").split("?")[0]);
    if (Object.hasOwn(fixtures, pathname)) return send(req, res, 200, Buffer.from(JSON.stringify(fixtures[pathname])), "application/json");
    for (const [prefix, dir] of Object.entries(mounts)) {
      const hit = pathname.startsWith(prefix) && file(join(dir, pathname.slice(prefix.length)));
      if (hit) return send(req, res, 200, readFileSync(hit), types[extname(hit)] ?? "application/octet-stream");
    }
    const hit =
      file(join(root, pathname)) ||
      file(join(root, pathname, "index.html")) ||
      (spa && !extname(pathname) && !pathname.startsWith("/api/") && file(join(root, "index.html")));
    if (!hit) {
      missing.add(pathname);
      return send(req, res, 404, Buffer.from("not found"), "text/plain");
    }
    send(req, res, 200, readFileSync(hit), types[extname(hit)] ?? "application/octet-stream");
  }).listen(0);
  return { server, missing, origin: `http://localhost:${server.address().port}` };
};

/*
 * Lab variants, built side by side:
 *   csr                       the HTML is an empty #root; the client renders everything
 *   prerender                 prerenderShell writes the whole feed into the HTML; the client hydrates
 *   prerender-first           only the first LOAD_FIRST sections are in the HTML and hydrated;
 *                             the rest render after, in a transition (useFirstScreen)
 *   prerender-first-critical  + the CSS those sections can use inlined in <head>,
 *                             full stylesheet moved to the end of <body>
 *   prerender-first-inline    + the whole stylesheet inlined, no link
 */
const FIRST = Number(process.env.LOAD_FIRST ?? 3);
const VARIANT_CONFIG = {
  csr: { prerender: null, first: null },
  prerender: { prerender: {}, first: null },
  "prerender-first": { prerender: {}, first: FIRST },
  "prerender-first-critical": { prerender: { criticalCss: "subset" }, first: FIRST },
  "prerender-first-inline": { prerender: { criticalCss: "all" }, first: FIRST },
};

const APP = process.env.LOAD_APP
  ? (await import(pathToFileURL(isAbsolute(process.env.LOAD_APP) ? process.env.LOAD_APP : resolve(lab, process.env.LOAD_APP)).href)).default
  : null;

/** name → { origin, missing, server, url, lab } */
const targets = {};
if (APP) {
  // LOAD_BUILDS=baseline,prerender-critical profiles a subset of the app's builds.
  const only = process.env.LOAD_BUILDS?.split(",");
  for (const [name, dist] of Object.entries(APP.builds).filter(([name]) => !only || only.includes(name))) {
    // A build that is registered but not built would profile a 404 page and look fast.
    if (!existsSync(join(dist, "index.html"))) throw new Error(`build "${name}" has no ${join(dist, "index.html")}; build it or leave it out of LOAD_BUILDS`);
    const served = serve({ root: dist, spa: true, isolation: APP.isolation, fixtures: APP.fixtures, mounts: APP.mounts });
    targets[name] = { ...served, url: `${served.origin}${APP.route}` };
  }
} else {
  const names = (process.env.LOAD_VARIANTS ?? Object.keys(VARIANT_CONFIG).join(",")).split(",");
  for (const name of names) {
    const config = VARIANT_CONFIG[name];
    if (!config) throw new Error(`unknown LOAD_VARIANTS entry ${name}`);
    const page = `/profile/index.html?sections=${SECTIONS}${config.first ? `&first=${config.first}` : ""}`;
    const outDir = join(lab, `results/load-build/${name}/`);
    await build({
      configFile: join(lab, "vitest.config.ts"),
      root: lab,
      logLevel: "info",
      base: "/",
      plugins: config.prerender
        ? [prerenderShell({ entry: join(lab, "profile/entry-server.tsx"), routes: [{ url: page, file: "profile/index.html" }], ...config.prerender })]
        : [],
      build: { outDir, emptyOutDir: true, rollupOptions: { input: join(lab, "profile/index.html") }, sourcemap: false },
    });
    const served = serve({ root: outDir, isolation: true });
    targets[name] = { ...served, url: `${served.origin}${page}` };
  }
}
const VARIANTS = Object.keys(targets);

const INSTRUMENT = () => {
  const lab = (window.__load = { shifts: [], lcp: 0, lcpElement: null, fcp: 0, longTasks: [], rulesAtFcp: null, phase: "load" });
  const rules = () =>
    Array.from(document.styleSheets).reduce((sum, sheet) => {
      try {
        return sum + sheet.cssRules.length;
      } catch {
        return sum;
      }
    }, 0);
  lab.rules = rules;
  const describe = (node) => {
    if (!node || node.nodeType !== 1) return String(node?.nodeName ?? "?");
    const el = node;
    const tag = el.tagName.toLowerCase();
    const data = el.getAttribute("data-minimal");
    const cls = typeof el.className === "string" && el.className ? `.${el.className.trim().split(/\s+/).slice(0, 2).join(".")}` : "";
    return data ? `${tag}[data-minimal=${data}]` : `${tag}${el.id ? `#${el.id}` : ""}${cls}`;
  };
  new PerformanceObserver((list) => {
    for (const entry of list.getEntries()) {
      if (entry.hadRecentInput) continue;
      lab.shifts.push({
        phase: lab.phase,
        value: entry.value,
        at: Math.round(entry.startTime),
        sources: (entry.sources ?? []).slice(0, 3).map((s) => ({
          node: describe(s.node),
          from: s.previousRect ? [Math.round(s.previousRect.y), Math.round(s.previousRect.height)] : null,
          to: s.currentRect ? [Math.round(s.currentRect.y), Math.round(s.currentRect.height)] : null,
        })),
      });
    }
  }).observe({ type: "layout-shift", buffered: true });
  new PerformanceObserver((list) => {
    for (const entry of list.getEntries()) {
      lab.lcp = entry.startTime;
      lab.lcpElement = `${describe(entry.element)}${entry.url ? ` ${entry.url.split("/").pop()}` : ""} (${entry.size}px²)`;
    }
  }).observe({ type: "largest-contentful-paint", buffered: true });
  new PerformanceObserver((list) => {
    for (const entry of list.getEntries()) {
      if (entry.name === "first-contentful-paint") {
        lab.fcp = entry.startTime;
        lab.rulesAtFcp = rules();
      }
    }
  }).observe({ type: "paint", buffered: true });
  new PerformanceObserver((list) => {
    for (const entry of list.getEntries()) lab.longTasks.push({ at: Math.round(entry.startTime), ms: Math.round(entry.duration) });
  }).observe({ type: "longtask", buffered: true });
};

const median = (values) => {
  const sorted = values.filter((v) => typeof v === "number").sort((a, b) => a - b);
  return sorted.length ? sorted[Math.floor(sorted.length / 2)] : null;
};

const browser = await chromium.launch({ headless: true, args: ["--use-angle=metal"] });
const runs = [];
try {
  for (let run = 0; run < RUNS + 1; run += 1)
    for (const variant of run % 2 ? [...VARIANTS].reverse() : VARIANTS) {
      const target = targets[variant];
      target.missing.clear();
      const context = await browser.newContext({ viewport: DEVICE.viewport, deviceScaleFactor: DEVICE.deviceScaleFactor, isMobile: true, hasTouch: true });
      const page = await context.newPage();
      const cdp = await context.newCDPSession(page);
      await cdp.send("Network.enable");
      await cdp.send("Network.setCacheDisabled", { cacheDisabled: true });
      await cdp.send("Network.emulateNetworkConditions", DEVICE.network);
      await cdp.send("Emulation.setCPUThrottlingRate", { rate: CPU_THROTTLE });
      const bytes = {};
      const decoded = {};
      const byRequest = new Map();
      cdp.on("Network.responseReceived", ({ requestId, type }) => byRequest.set(requestId, type));
      cdp.on("Network.loadingFinished", ({ requestId, encodedDataLength }) => {
        const type = byRequest.get(requestId) ?? "Other";
        bytes[type] = (bytes[type] ?? 0) + encodedDataLength;
      });
      cdp.on("Network.dataReceived", ({ requestId, dataLength }) => {
        const type = byRequest.get(requestId) ?? "Other";
        decoded[type] = (decoded[type] ?? 0) + dataLength;
      });
      await page.addInitScript(INSTRUMENT);
      // Nothing leaves the machine: a run must be deterministic, and an app build
      // that names a production host must not send it traffic. Blocked requests
      // are recorded — an external dependency on the first screen is a finding.
      const external = new Set();
      await context.route(/^(?!http:\/\/localhost[:/])(https?|wss?):\/\//, (route) => {
        external.add(new URL(route.request().url()).host);
        return route.abort("blockedbyclient");
      });
      // A hydration mismatch makes React discard the prerendered DOM and client-render: count them.
      const hydrationErrors = [];
      const pageErrors = [];
      page.on("console", (message) => {
        if (/hydrat/i.test(message.text())) hydrationErrors.push(message.text().slice(0, 200));
      });
      page.on("pageerror", (error) => {
        if (/hydrat/i.test(String(error))) hydrationErrors.push(String(error).slice(0, 200));
        else pageErrors.push(String(error).slice(0, 200));
      });

      const started = Date.now();
      await page.goto(target.url, { waitUntil: "load", timeout: 120_000 });
      const htmlDecodedBytes = await page.evaluate(() => performance.getEntriesByType("navigation")[0]?.decodedBodySize ?? 0);
      let timing = {};
      if (APP) {
        // An app has no readiness flag: let data, images and fonts land.
        await page.waitForTimeout(APP.settleMs ?? 4000);
      } else {
        await page.waitForFunction(() => window.__lab?.ready === true, null, { timeout: 60_000 });
        // The first-screen variants finish rendering after hydration; scroll only a complete feed.
        await page.waitForFunction(() => typeof window.__labFeedComplete === "number", null, { timeout: 60_000 });
        timing = await page.evaluate(() => ({
          mountedMs: Math.round(window.__labMountedAt ?? 0),
          completeMs: Math.round(window.__labFeedComplete ?? 0),
        }));
        await page.waitForTimeout(1500);
      }
      const afterLoad = await page.evaluate(() => ({ rules: window.__load.rules(), domNodes: document.getElementsByTagName("*").length }));
      const loadMs = Date.now() - started;

      await page.evaluate(() => (window.__load.phase = "scroll"));
      for (let i = 0; i < 20; i += 1) {
        await page.mouse.wheel(0, 300);
        await page.waitForTimeout(40);
      }
      await page.waitForTimeout(500);
      await page.evaluate(() => (window.__load.phase = "tier"));
      await page.evaluate(() => document.documentElement.setAttribute("data-ui-tier", "low_power"));
      await page.waitForTimeout(500);

      const sample = await page.evaluate(() => {
        const l = window.__load;
        const cls = (phase) => l.shifts.filter((s) => s.phase === phase).reduce((sum, s) => sum + s.value, 0);
        const tbt = l.longTasks.filter((t) => t.at < l.fcp + 5000).reduce((sum, t) => sum + Math.max(0, t.ms - 50), 0);
        // When fonts and images finished, to line shifts up with what arrived.
        const resources = performance.getEntriesByType("resource");
        const doneBy = (pattern) =>
          resources.filter((e) => pattern.test(e.name)).reduce((max, e) => Math.max(max, Math.round(e.responseEnd)), 0) || null;
        return {
          fcpMs: Math.round(l.fcp),
          lcpMs: Math.round(l.lcp),
          lcpElement: l.lcpElement,
          fontsDoneMs: doneBy(/\.woff2?(\?|$)/),
          // Each web font's arrival: a swap re-wraps text, so shifts line up with these.
          fontArrivals: resources
            .filter((e) => /\.woff2?(\?|$)/.test(e.name))
            .map((e) => ({ font: e.name.split("/").pop(), endMs: Math.round(e.responseEnd) }))
            .sort((a, b) => a.endMs - b.endMs),
          imagesDoneMs: doneBy(/\.(png|jpe?g|webp|avif|gif)(\?|$)/),
          fetchDoneMs: doneBy(/\/api\//),
          tbtMs: tbt,
          longTasks: l.longTasks,
          cls: { load: +cls("load").toFixed(4), scroll: +cls("scroll").toFixed(4), tier: +cls("tier").toFixed(4) },
          shifts: l.shifts,
          rules: { atFcp: l.rulesAtFcp, afterScroll: l.rules() },
        };
      });
      await context.close();
      if (run === 0) continue; // cold browser warm-up, discarded
      runs.push({
        variant,
        ...sample,
        rules: { ...sample.rules, afterLoad: afterLoad.rules },
        domNodes: afterLoad.domNodes,
        loadMs,
        ...timing,
        htmlDecodedBytes,
        hydrationErrors,
        pageErrors,
        unanswered: [...target.missing],
        externalBlocked: [...external],
        bytesEncoded: bytes,
        bytesDecoded: decoded,
      });
    }
} finally {
  await browser.close();
  for (const target of Object.values(targets)) target.server.close();
}

const kb = (n) => Math.round(n / 102.4) / 10;
const sum = (o) => Object.values(o).reduce((a, b) => a + b, 0);
const summarize = (rows) => ({
  runs: rows.length,
  fcpMs: median(rows.map((r) => r.fcpMs)),
  lcpMs: median(rows.map((r) => r.lcpMs)),
  lcpElement: rows[0]?.lcpElement,
  fontsDoneMs: median(rows.map((r) => r.fontsDoneMs)),
  imagesDoneMs: median(rows.map((r) => r.imagesDoneMs)),
  fetchDoneMs: median(rows.map((r) => r.fetchDoneMs)),
  shiftsAtMs: [...new Set(rows[0]?.shifts.filter((s) => s.phase === "load").map((s) => s.at) ?? [])],
  tbtMs: median(rows.map((r) => r.tbtMs)),
  ...(APP ? {} : { mountedMs: median(rows.map((r) => r.mountedMs)), completeMs: median(rows.map((r) => r.completeMs)) }),
  cls: {
    load: median(rows.map((r) => r.cls.load)),
    scroll: median(rows.map((r) => r.cls.scroll)),
    tier: median(rows.map((r) => r.cls.tier)),
  },
  domNodes: median(rows.map((r) => r.domNodes)),
  htmlDecodedKB: kb(rows[0]?.htmlDecodedBytes ?? 0),
  transferKB: Object.fromEntries(Object.entries(rows[0]?.bytesEncoded ?? {}).map(([k, v]) => [k, kb(v)])),
  transferTotalKB: kb(sum(rows[0]?.bytesEncoded ?? {})),
  decodedKB: Object.fromEntries(Object.entries(rows[0]?.bytesDecoded ?? {}).map(([k, v]) => [k, kb(v)])),
  rules: rows[0]?.rules,
  rulesInjectedAfterFcp: median(rows.map((r) => (r.rules.afterScroll ?? 0) - (r.rules.atFcp ?? 0))),
  hydrationErrors: rows.reduce((n, r) => n + r.hydrationErrors.length, 0),
  pageErrors: [...new Set(rows.flatMap((r) => r.pageErrors))].slice(0, 5),
  unanswered: [...new Set(rows.flatMap((r) => r.unanswered))],
  externalBlocked: [...new Set(rows.flatMap((r) => r.externalBlocked))],
});
const summary = Object.fromEntries(VARIANTS.map((variant) => [variant, summarize(runs.filter((r) => r.variant === variant))]));
const bundle = {
  kind: "frontend-lab.load-profile",
  capturedAt: new Date().toISOString(),
  target: APP ? { app: APP.name, route: APP.route, builds: APP.builds } : { lab: "profile/index.html", sections: SECTIONS, first: FIRST },
  emulation: { device: DEVICE_NAME, label: DEVICE.label, cpuThrottle: CPU_THROTTLE, gpu: "ANGLE Metal", compression: COMPRESS ? "brotli-11" : "none" },
  summary,
  runs,
};
const stamp = bundle.capturedAt.replace(/[:.]/g, "-");
const dir = join(lab, `results/load/${stamp}-${APP ? APP.name : "lab"}-${DEVICE_NAME}/`);
mkdirSync(dir, { recursive: true });
writeFileSync(join(dir, "bundle.json"), `${JSON.stringify(bundle, null, 2)}\n`);
console.log(JSON.stringify({ emulation: bundle.emulation, summary }, null, 2));
const worst = runs.flatMap((r) => r.shifts.map((s) => ({ variant: r.variant, ...s }))).sort((a, b) => b.value - a.value).slice(0, 8);
if (worst.length) console.log("largest shifts:", JSON.stringify(worst, null, 1));
console.log(`bundle: ${dir}bundle.json`);
