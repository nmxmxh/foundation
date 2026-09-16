// Minimal Chrome DevTools Protocol client for the forwarded WebView socket.
//   node cdp.mjs eval '<js expression>'        -> prints JSON result (awaits promises)
//   node cdp.mjs nav '<url>'                   -> Page.navigate and wait for load
// Uses Node's built-in WebSocket; target is the first "page" on 127.0.0.1:9222.
const [mode, arg] = process.argv.slice(2);
const targets = await (await fetch("http://127.0.0.1:9222/json")).json();
const page = targets.find((t) => t.type === "page");
if (!page) throw new Error("no page target");
const ws = new WebSocket(page.webSocketDebuggerUrl);
let nextId = 1;
const pending = new Map();
const listeners = [];
ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);
  if (msg.id && pending.has(msg.id)) {
    pending.get(msg.id)(msg);
    pending.delete(msg.id);
  } else {
    for (const l of listeners) l(msg);
  }
};
await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = reject;
});
const send = (method, params = {}) =>
  new Promise((resolve) => {
    const id = nextId++;
    pending.set(id, resolve);
    ws.send(JSON.stringify({ id, method, params }));
  });

if (mode === "eval") {
  const res = await send("Runtime.evaluate", {
    expression: arg,
    awaitPromise: true,
    returnByValue: true,
  });
  if (res.result?.exceptionDetails) {
    console.log("EXCEPTION:", JSON.stringify(res.result.exceptionDetails.exception?.description ?? res.result.exceptionDetails));
  } else {
    console.log(JSON.stringify(res.result?.result?.value, null, 2));
  }
} else if (mode === "nav") {
  await send("Page.enable");
  const loaded = new Promise((resolve) => listeners.push((m) => m.method === "Page.loadEventFired" && resolve()));
  await send("Page.navigate", { url: arg });
  await Promise.race([loaded, new Promise((r) => setTimeout(r, 15000))]);
  console.log("navigated", arg);
}
ws.close();
