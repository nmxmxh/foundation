// Linaria vs styled-components mount cost, from the production spike build.
// Run `node linaria/build.mjs` first. Interleaved, fresh page per engine for
// the cold run, then the fastest of repeated warm mounts. Real GPU (Metal).
import { createServer } from "node:http";
import { readFileSync, existsSync, statSync, writeFileSync } from "node:fs";
import { extname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const dist = fileURLToPath(new URL("../results/linaria-spike/", import.meta.url));
const N = Number(process.env.CARDS ?? 300);
const REPEATS = 9;
const types = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css" };
const server = createServer((req, res) => {
  const path = join(dist, req.url === "/" ? "index.html" : decodeURIComponent(req.url.split("?")[0]));
  if (!existsSync(path) || statSync(path).isDirectory()) return res.writeHead(404).end();
  res.writeHead(200, { "content-type": types[extname(path)] ?? "application/octet-stream" }).end(readFileSync(path));
}).listen(0);
const url = `http://localhost:${server.address().port}/`;

const browser = await chromium.launch({ headless: true, args: ["--use-angle=metal"] });
const results = { linaria: { cold: [], warm: [] }, styled: { cold: [], warm: [] } };
try {
  for (let round = 0; round < 5; round += 1) {
    for (const kind of round % 2 ? ["styled", "linaria"] : ["linaria", "styled"]) {
      const page = await browser.newPage();
      await page.goto(url);
      await page.waitForFunction(() => window.__lab?.ready === true);
      results[kind].cold.push(await page.evaluate(([k, n]) => window.__lab.mount(k, n), [kind, N]));
      for (let i = 0; i < REPEATS; i += 1) results[kind].warm.push(await page.evaluate(([k, n]) => window.__lab.mount(k, n), [kind, N]));
      await page.close();
    }
  }
} finally {
  await browser.close();
  server.close();
}

const median = (values) => {
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.floor(sorted.length / 2)];
};
const summary = Object.fromEntries(
  Object.entries(results).map(([kind, { cold, warm }]) => [
    kind,
    {
      coldScriptMs: +median(cold.map((s) => s.scriptMs)).toFixed(1),
      coldStyleMs: +median(cold.map((s) => s.styleMs)).toFixed(1),
      coldRulesInjected: median(cold.map((s) => s.rulesAdded)),
      warmScriptMs: +Math.min(...warm.map((s) => s.scriptMs)).toFixed(1),
      warmStyleMs: +Math.min(...warm.map((s) => s.styleMs)).toFixed(1),
    },
  ]),
);
const report = { measuredAt: new Date().toISOString(), cards: N, note: "cold = median of 5 first mounts in fresh pages; warm = fastest of 45 repeat mounts", summary };
writeFileSync(fileURLToPath(new URL("../results/linaria-spike/measure.json", import.meta.url)), `${JSON.stringify(report, null, 2)}\n`);
console.log(JSON.stringify(report, null, 2));
