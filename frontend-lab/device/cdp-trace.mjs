// Record a Chromium trace from the forwarded WebView while a scroll runs, then
// summarise where frame time went, per thread and per event.
//   node cdp-trace.mjs <out.json> <seconds>
// The scroll itself is driven externally (adb input swipe) during the window.
import fs from "node:fs";

const [outFile = "trace.json", seconds = "8"] = process.argv.slice(2);
const targets = await (await fetch("http://127.0.0.1:9222/json")).json();
const page = targets.find((t) => t.type === "page");
const ws = new WebSocket(page.webSocketDebuggerUrl);
let nextId = 1;
const pending = new Map();
const events = [];
let complete;
const done = new Promise((r) => (complete = r));
ws.onmessage = (e) => {
  const m = JSON.parse(e.data);
  if (m.id && pending.has(m.id)) {
    pending.get(m.id)(m);
    pending.delete(m.id);
  } else if (m.method === "Tracing.dataCollected") {
    events.push(...m.params.value);
  } else if (m.method === "Tracing.tracingComplete") {
    complete();
  }
};
await new Promise((res, rej) => ((ws.onopen = res), (ws.onerror = rej)));
const send = (method, params = {}) =>
  new Promise((res) => {
    const id = nextId++;
    pending.set(id, res);
    ws.send(JSON.stringify({ id, method, params }));
  });

const started = await send("Tracing.start", {
  transferMode: "ReportEvents",
  traceConfig: {
    recordMode: "recordAsMuchAsPossible",
    includedCategories: [
      "devtools.timeline",
      "disabled-by-default-devtools.timeline",
      "disabled-by-default-devtools.timeline.frame",
      "blink",
      "cc",
      "gpu",
      "viz",
      "benchmark",
      "latencyInfo",
      "toplevel",
    ],
  },
});
if (started.error) throw new Error(JSON.stringify(started.error));
console.error(`tracing for ${seconds}s…`);
await new Promise((r) => setTimeout(r, Number(seconds) * 1000));
await send("Tracing.end");
await Promise.race([done, new Promise((r) => setTimeout(r, 20000))]);
ws.close();
fs.writeFileSync(outFile, JSON.stringify(events));

// Thread names come from TracingStartedInBrowser / thread_name metadata events.
const threadName = new Map();
for (const e of events) {
  if (e.name === "TracingStartedInBrowser" || e.name === "TracingStartedInPage") continue;
  if (e.args?.data?.threadName) threadName.set(`${e.pid}:${e.tid}`, e.args.data.threadName);
}
for (const e of events) {
  if (e.cat === "__metadata" && e.args?.data?.name) threadName.set(`${e.pid}:${e.tid}`, e.args.data.name);
}

// Self time is approximated by duration of top-level-ish events on each thread.
const byThread = new Map();
const byEvent = new Map();
for (const e of events) {
  if (e.ph !== "X" || typeof e.dur !== "number") continue;
  const ms = e.dur / 1000;
  const key = `${e.pid}:${e.tid}`;
  const tn = threadName.get(key) ?? key;
  byThread.set(tn, (byThread.get(tn) ?? 0) + (e.name === "ThreadControllerImpl::RunTask" || e.name === "RunTask" ? ms : 0));
  const ek = `${tn} :: ${e.name}`;
  const cur = byEvent.get(ek) ?? { ms: 0, n: 0, max: 0 };
  cur.ms += ms;
  cur.n += 1;
  cur.max = Math.max(cur.max, ms);
  byEvent.set(ek, cur);
}
const top = [...byEvent.entries()]
  .filter(([k]) => !/RunTask$/.test(k))
  .sort((a, b) => b[1].ms - a[1].ms)
  .slice(0, 30)
  .map(([k, v]) => `${v.ms.toFixed(0).padStart(7)} ms  n=${String(v.n).padStart(5)}  max=${v.max.toFixed(1).padStart(6)}  ${k}`);
const threads = [...byThread.entries()]
  .filter(([, ms]) => ms > 0)
  .sort((a, b) => b[1] - a[1])
  .map(([k, ms]) => `${ms.toFixed(0).padStart(7)} ms  ${k}`);
console.log(`events=${events.length}`);
console.log("== task time by thread (RunTask)");
console.log(threads.join("\n"));
console.log("== top events by total duration");
console.log(top.join("\n"));
