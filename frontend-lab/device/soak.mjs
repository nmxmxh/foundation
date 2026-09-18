#!/usr/bin/env node
/**
 * Degradation soak for a WebView app on an emulator or device.
 *
 * Why (2026-09-17): after ~1 h of use the ChooseChow emulator drew a plain
 * 200-row list at p50 89–200 ms; a cold emulator boot restored 17 ms, and
 * force-stopping the app did not. A state like that invalidates every A/B taken
 * inside it, and if the app causes it, it is itself the user-facing defect.
 *
 * Each cycle:
 *   1. a user-like tour of the app (in-app navigation + swipes, incl. a
 *      horizontal rail) — this is the load;
 *   2. samples: the WebView control (plain list injected into the app shell),
 *      the app's real scroll page, and a NON-WebView control (Android Settings)
 *      so a degraded device can be told apart from a degraded app;
 *   3. health: app PSS, WebView renderer RSS, SurfaceFlinger RSS, guest
 *      MemAvailable/swap, emulator host RSS (qemu), host load.
 * One JSON line per sample in soak.jsonl, one readable line on stdout.
 * Stops after SOAK_MIN minutes, or after DEGRADED_STREAK consecutive cycles in
 * which the WebView control is over budget (then it takes one extra "after
 * force-stop" sample to see whether killing the app recovers it).
 *
 *   SOAK_MIN=45 node device/soak.mjs
 */
import { execFileSync } from "node:child_process";
import { appendFileSync, mkdirSync } from "node:fs";
import { loadavg } from "node:os";
import { fileURLToPath } from "node:url";

const PKG = process.env.PKG ?? "com.ovasabi.choosechow";
const SCROLL_ROUTE = process.env.SCROLL_ROUTE ?? "/orders";
const TOUR = (process.env.TOUR ?? "/dashboard,/chefs,/dishes,/orders,/profile,/mealplan,/subscriptions").split(",");
const SOAK_MIN = Number(process.env.SOAK_MIN ?? 45);
const DEGRADED_P50 = Number(process.env.DEGRADED_P50 ?? 25);
const DEGRADED_STREAK = Number(process.env.DEGRADED_STREAK ?? 2);
const lab = fileURLToPath(new URL("..", import.meta.url));
const outDir = process.env.OUT ?? `${lab}results/device/soak-${new Date().toISOString().replace(/[:.]/g, "-")}`;
mkdirSync(outDir, { recursive: true });
const out = `${outDir}/soak.jsonl`;

const adb = (...a) => execFileSync("adb", a, { encoding: "utf8", maxBuffer: 64 * 1024 * 1024 });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const hostHasQemu = () => {
  try {
    return Number(execFileSync("sh", ["-c", "ps -o rss= -p $(pgrep -f qemu-system | head -1)"], { encoding: "utf8" }).trim()) / 1024;
  } catch {
    return null;
  }
};

async function cdpConnect() {
  const pid = adb("shell", "pidof", PKG).trim();
  execFileSync("adb", ["forward", "--remove-all"]);
  execFileSync("adb", ["forward", "tcp:9222", `localabstract:webview_devtools_remote_${pid}`]);
  let page;
  for (let i = 0; i < 60 && !page; i++) {
    try {
      page = (await (await fetch("http://127.0.0.1:9222/json")).json()).find((t) => t.type === "page");
    } catch {}
    if (!page) await sleep(500);
  }
  if (!page) throw new Error("no page target");
  const ws = new WebSocket(page.webSocketDebuggerUrl);
  let id = 1;
  const pending = new Map();
  ws.onmessage = (e) => {
    const m = JSON.parse(e.data);
    if (m.id && pending.has(m.id)) (pending.get(m.id)(m), pending.delete(m.id));
  };
  await new Promise((res, rej) => ((ws.onopen = res), (ws.onerror = rej)));
  const evaluate = (expression) =>
    new Promise((res) => {
      const i = id++;
      pending.set(i, (m) => res(m.result?.result?.value));
      ws.send(JSON.stringify({ id: i, method: "Runtime.evaluate", params: { expression, awaitPromise: true, returnByValue: true } }));
    });
  return { evaluate, close: () => ws.close() };
}

const go = (cdp, route) => cdp.evaluate(`(()=>{history.pushState({},'',${JSON.stringify(route)});dispatchEvent(new PopStateEvent('popstate'));return 1})()`);
const swipeV = () => adb("shell", "input", "swipe", "540", "1900", "540", "500", "280");
const swipeVBack = () => adb("shell", "input", "swipe", "540", "500", "540", "1900", "280");

function gfx(pkg) {
  const t = adb("shell", "dumpsys", "gfxinfo", pkg);
  const g = (re) => Number(t.match(re)?.[1] ?? NaN);
  return { frames: g(/Total frames rendered: (\d+)/), janky: g(/Janky frames: \d+ \(([\d.]+)%\)/), p50: g(/50th percentile: (\d+)ms/), p90: g(/90th percentile: (\d+)ms/), p99: g(/99th percentile: (\d+)ms/), gpuP50: g(/50th gpu percentile: (\d+)ms/) };
}
async function sampleSwipes(pkg) {
  adb("shell", "dumpsys", "gfxinfo", pkg, "reset");
  for (let i = 0; i < 2; i++) {
    swipeV();
    await sleep(700);
    swipeVBack();
    await sleep(700);
  }
  return gfx(pkg);
}

function health() {
  const mem = adb("shell", "cat", "/proc/meminfo");
  const kb = (k) => Number(mem.match(new RegExp(`${k}:\\s+(\\d+)`))?.[1] ?? NaN);
  const rss = (name) => {
    const line = adb("shell", "ps", "-A", "-o", "RSS,NAME").split("\n").find((l) => l.includes(name));
    return line ? Math.round(Number(line.trim().split(/\s+/)[0]) / 1024) : null;
  };
  const pss = Number(adb("shell", "dumpsys", "meminfo", PKG).match(/TOTAL PSS:\s+(\d+)/)?.[1] ?? NaN);
  return {
    appPssMB: Math.round(pss / 1024),
    rendererRssMB: rss("sandboxed_process"),
    surfaceflingerRssMB: rss("surfaceflinger"),
    guestAvailMB: Math.round(kb("MemAvailable") / 1024),
    guestSwapMB: Math.round((kb("SwapTotal") - kb("SwapFree")) / 1024),
    qemuHostRssMB: Math.round(hostHasQemu()),
    hostLoad1: Math.round(loadavg()[0] * 10) / 10,
  };
}

const PLAIN = `(()=>{const m=document.getElementById('main-content');const box=m.firstElementChild;box.innerHTML='';for(let i=0;i<200;i++){const d=document.createElement('div');d.textContent='Row '+i;d.style.cssText='height:56px;border-bottom:1px solid #ddd;padding:16px;box-sizing:border-box;font:16px sans-serif';box.appendChild(d)}m.scrollTop=0;return 1})()`;

async function measure(cdp, cycle, startedAt, tag = "cycle") {
  // App scroll page, real content.
  await go(cdp, SCROLL_ROUTE);
  await sleep(5000);
  await cdp.evaluate(`(()=>{const m=document.getElementById('main-content');if(m)m.scrollTop=0;return 1})()`);
  await sleep(500);
  const app = await sampleSwipes(PKG);
  // WebView control: plain list in the same shell.
  await go(cdp, "/profile");
  await sleep(300);
  await go(cdp, SCROLL_ROUTE);
  await sleep(4000);
  await cdp.evaluate(PLAIN);
  await sleep(1500);
  const control = await sampleSwipes(PKG);
  // Non-WebView control: Android Settings, then back to the app.
  adb("shell", "am", "start", "-W", "-a", "android.settings.SETTINGS");
  await sleep(2500);
  const settings = await sampleSwipes("com.android.settings");
  adb("shell", "am", "start", "-W", "-n", `${PKG}/.MainActivity`);
  await sleep(2000);
  const rec = { t: new Date().toISOString(), tag, cycle, minutes: Math.round((Date.now() - startedAt) / 6000) / 10, app, control, settings, health: health() };
  appendFileSync(out, JSON.stringify(rec) + "\n");
  const h = rec.health;
  console.log(
    `${tag} ${String(cycle).padStart(3)} +${rec.minutes}m | app p50=${app.p50} janky=${app.janky}% (${app.frames}f) | control p50=${control.p50} janky=${control.janky}% (${control.frames}f) | settings p50=${settings.p50} janky=${settings.janky}% | appPss=${h.appPssMB} renderer=${h.rendererRssMB} sf=${h.surfaceflingerRssMB} guestAvail=${h.guestAvailMB} swap=${h.guestSwapMB} qemu=${h.qemuHostRssMB} load=${h.hostLoad1}`,
  );
  return rec;
}

async function tour(cdp) {
  for (const route of TOUR) {
    await go(cdp, route);
    await sleep(2500);
    swipeV();
    await sleep(600);
    swipeVBack();
    await sleep(600);
    if (route === "/dashboard") {
      // Horizontal rail ("Hot chefs") sits around y≈1300 on a 2400px screen.
      adb("shell", "input", "swipe", "900", "1300", "150", "1300", "250");
      await sleep(600);
      adb("shell", "input", "swipe", "150", "1300", "900", "1300", "250");
      await sleep(600);
    }
  }
}

console.log(`soak → ${out}  (up to ${SOAK_MIN} min; degraded = control p50 > ${DEGRADED_P50} ms for ${DEGRADED_STREAK} cycles)`);
const startedAt = Date.now();
let cdp = await cdpConnect();
let streak = 0;
for (let cycle = 0; Date.now() - startedAt < SOAK_MIN * 60000; cycle++) {
  if (cycle > 0) await tour(cdp);
  let rec;
  try {
    rec = await measure(cdp, cycle, startedAt);
  } catch (e) {
    console.log(`cycle ${cycle} error: ${String(e).slice(0, 200)} — reconnecting`);
    cdp.close();
    cdp = await cdpConnect();
    continue;
  }
  streak = rec.control.p50 > DEGRADED_P50 ? streak + 1 : 0;
  if (streak >= DEGRADED_STREAK) {
    console.log(`DEGRADED at +${rec.minutes}m — force-stopping the app and re-sampling`);
    cdp.close();
    adb("shell", "am", "force-stop", PKG);
    await sleep(3000);
    adb("shell", "am", "start", "-W", "-n", `${PKG}/.MainActivity`);
    await sleep(8000);
    cdp = await cdpConnect();
    await measure(cdp, cycle, startedAt, "after-force-stop");
    break;
  }
}
cdp.close();
console.log("SOAK DONE");
