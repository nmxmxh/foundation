import { afterEach, describe, expect, it, vi } from "vitest";

import { createHTTPTransport } from "./http";
import { createEnvelope, type RuntimeRoute } from "./index";

type Captured = { url: string; method: string; headers: Headers; body?: BodyInit | null };

/** A transport bound to a fetch stub that records the request it was given. */
const transportWithCapture = () => {
  const captured: Captured[] = [];
  const fetchImpl = (async (input: string | URL, init?: RequestInit) => {
    captured.push({
      url: String(input),
      method: init?.method ?? "GET",
      headers: new Headers(init?.headers),
      body: init?.body ?? null,
    });
    return new Response(JSON.stringify({ ok: true }), {
      status: 200,
      headers: { "content-type": "application/json" },
    });
  }) as unknown as typeof fetch;

  const transport = createHTTPTransport({
    baseUrl: "https://api.test",
    fetchImpl,
    // Disable compression so the JSON body stays inspectable as a string.
    compression: { enabled: false, minBytes: 0, preferred: ["gzip"] },
  });
  return { transport, captured };
};

const route = (over: Partial<RuntimeRoute>): RuntimeRoute => ({
  method: "POST",
  path: "/v1/x",
  eventType: "x:do:v1:requested",
  requiredCapability: "",
  permission: "write",
  ...over,
});

const dispatch = async (r: RuntimeRoute, payload: Record<string, unknown>) => {
  const { transport, captured } = transportWithCapture();
  await transport.dispatch(createEnvelope({ eventType: r.eventType, payload }), r, new AbortController().signal);
  return captured[0];
};

const pathOf = (url: string) => new URL(url).pathname;
const bodyJson = (c: Captured) => JSON.parse(String(c.body));

describe("createHTTPTransport path parameters", () => {
  it("substitutes a {param} into the path and strips it from the JSON body", async () => {
    const c = await dispatch(
      route({ method: "POST", path: "/v1/offers/{offer_id}/accept", eventType: "offer:accept:v1:requested" }),
      { offer_id: "o-42", reason: "fastest" },
    );
    expect(pathOf(c.url)).toBe("/v1/offers/o-42/accept");
    expect(bodyJson(c)).toEqual({ reason: "fastest" }); // offer_id removed
  });

  it("substitutes multiple params in one path", async () => {
    const c = await dispatch(
      route({ method: "PATCH", path: "/v1/orgs/{org_id}/orders/{order_id}/status", eventType: "marketplace:set_order_status:v1:requested" }),
      { org_id: "acme", order_id: "9", status: "ready" },
    );
    expect(pathOf(c.url)).toBe("/v1/orgs/acme/orders/9/status");
    expect(bodyJson(c)).toEqual({ status: "ready" });
  });

  it("percent-encodes param values so they stay one segment", async () => {
    const c = await dispatch(
      route({ method: "POST", path: "/v1/files/{key}/touch", eventType: "files:touch:v1:requested" }),
      { key: "a/b c" },
    );
    expect(pathOf(c.url)).toBe("/v1/files/a%2Fb%20c/touch");
  });

  it("strips path params from the query string on GET, keeping the rest", async () => {
    const c = await dispatch(
      route({ method: "GET", path: "/v1/marketplace/orders/{order_id}", eventType: "marketplace:get_order:v1:requested" }),
      { order_id: "123", expand: "items" },
    );
    const url = new URL(c.url);
    expect(url.pathname).toBe("/v1/marketplace/orders/123");
    expect(url.searchParams.get("order_id")).toBeNull(); // not duplicated as a query param
    expect(url.searchParams.get("expand")).toBe("items");
    expect(c.body).toBeNull(); // GET carries no body
  });

  it("leaves a static path and its full payload untouched", async () => {
    const c = await dispatch(
      route({ method: "POST", path: "/v1/user/create", eventType: "user:create:v1:requested" }),
      { email: "a@b.c", name: "A" },
    );
    expect(pathOf(c.url)).toBe("/v1/user/create");
    expect(bodyJson(c)).toEqual({ email: "a@b.c", name: "A" });
  });

  it("rejects a missing path-param value rather than emitting a broken URL", async () => {
    const r = route({ path: "/v1/offers/{offer_id}/accept", eventType: "offer:accept:v1:requested" });
    const { transport } = transportWithCapture();
    await expect(
      transport.dispatch(createEnvelope({ eventType: r.eventType, payload: { reason: "x" } }), r, new AbortController().signal),
    ).rejects.toThrow(/path parameter "offer_id" is required/);
  });

  it("rejects a non-scalar path-param value", async () => {
    const r = route({ path: "/v1/things/{id}", method: "GET", eventType: "things:get:v1:requested" });
    const { transport } = transportWithCapture();
    await expect(
      transport.dispatch(createEnvelope({ eventType: r.eventType, payload: { id: { nested: true } } }), r, new AbortController().signal),
    ).rejects.toThrow(/must be a scalar/);
  });

  it("rejects empty path parameter declarations and ArrayBuffer protobuf bodies remain valid", async () => {
    const invalid = route({ path: "/v1/things/{ }", eventType: "things:get:v1:requested" });
    const { transport } = transportWithCapture();
    await expect(transport.dispatch(createEnvelope({ eventType: invalid.eventType, payload: {} }), invalid, new AbortController().signal)).rejects.toThrow("empty path parameter");

    const binary = route({ eventType: "things:send:v1:requested" });
    const envelope = createEnvelope({ eventType: binary.eventType, payload: Uint8Array.from([7]).buffer });
    envelope.payloadEncoding = "protobuf";
    await expect(transport.dispatch(envelope, binary, new AbortController().signal)).resolves.toEqual({ ok: true });
  });
});

describe("createHTTPTransport response and cancellation contracts", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  const transportFor = (response: Response | ((signal: AbortSignal) => Promise<Response>), timeoutMs = 100) =>
    createHTTPTransport({
      baseUrl: "https://api.test",
      timeoutMs,
      compression: { enabled: false },
      getHeaders: () => ({ Authorization: "Bearer test" }),
      fetchImpl: (async (_input, init) =>
        typeof response === "function" ? response(init?.signal as AbortSignal) : response) as typeof fetch,
    });

  it("returns protobuf bytes, octet streams, JSON payloads, and NDJSON records", async () => {
    const signal = new AbortController().signal;
    const protobuf = transportFor(new Response(new Uint8Array([1, 2]), { headers: { "content-type": "application/x-protobuf" } }));
    await expect(protobuf.dispatch(createEnvelope({ eventType: route({}).eventType, payload: {} }), route({}), signal)).resolves.toEqual(new Uint8Array([1, 2]));

    const octets = transportFor(new Response(new Uint8Array([3]), { headers: { "content-type": "application/octet-stream" } }));
    expect(await octets.dispatch(createEnvelope({ eventType: route({}).eventType, payload: {} }), route({}), signal)).toBeInstanceOf(ReadableStream);

    const json = transportFor(new Response(JSON.stringify({ response_payload: { ok: 1 } }), { headers: { "content-type": "application/json" } }));
    await expect(json.dispatch(createEnvelope({ eventType: route({}).eventType, payload: {} }), route({}), signal)).resolves.toEqual({ ok: 1 });

    const ndjson = transportFor(new Response('{"id":1}\n\n{"id":2}', { headers: { "content-type": "application/x-ndjson" } }));
    const stream = await ndjson.dispatch(createEnvelope({ eventType: route({}).eventType, payload: {} }), route({}), signal) as AsyncIterable<unknown>;
    const records = [];
    for await (const record of stream) records.push(record);
    expect(records).toEqual([{ id: 1 }, { id: 2 }]);
  });

  it("propagates caller cancellation and bounded timeout reasons", async () => {
    vi.useFakeTimers();
    const pending = (signal: AbortSignal) => new Promise<Response>((_resolve, reject) => {
      if (signal.aborted) {
        reject(signal.reason);
        return;
      }
      signal.addEventListener("abort", () => reject(signal.reason), { once: true });
    });
    const caller = new AbortController();
    const cancelled = transportFor(pending).dispatch(createEnvelope({ eventType: route({}).eventType, payload: {} }), route({}), caller.signal);
    await Promise.resolve();
    caller.abort(new Error("caller stopped"));
    await expect(cancelled).rejects.toThrow("caller stopped");

    const timed = transportFor(pending, 5).dispatch(createEnvelope({ eventType: route({}).eventType, payload: {} }), route({}), new AbortController().signal);
    const timedExpectation = expect(timed).rejects.toThrow("http transport timed out after 5ms");
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(5);
    await timedExpectation;
  });

  it("normalizes structured, invalid JSON, text, and empty HTTP errors", async () => {
    const envelope = createEnvelope({ eventType: route({}).eventType, payload: {} });
    const signal = new AbortController().signal;
    await expect(transportFor(new Response(JSON.stringify({ error: { message: "denied" } }), { status: 403, headers: { "content-type": "application/json" } })).dispatch(envelope, route({}), signal)).rejects.toThrow("denied");
    await expect(transportFor(new Response("{", { status: 500, headers: { "content-type": "application/json" } })).dispatch(envelope, route({}), signal)).rejects.toThrow("status 500");
    await expect(transportFor(new Response("unavailable", { status: 503 })).dispatch(envelope, route({}), signal)).rejects.toThrow("unavailable");
    await expect(transportFor(new Response(null, { status: 502 })).dispatch(envelope, route({}), signal)).rejects.toThrow("status 502");
  });

  it("serializes protobuf bodies and GET array query values", async () => {
    const captured: Captured[] = [];
    const transport = createHTTPTransport({ baseUrl: "https://api.test", compression: { enabled: false }, fetchImpl: (async (input, init) => {
      captured.push({ url: String(input), method: init?.method ?? "GET", headers: new Headers(init?.headers), body: init?.body });
      return new Response("{}");
    }) as typeof fetch });
    const protobufRoute = route({ eventType: "binary:send:v1:requested" });
    const protobufEnvelope = createEnvelope({ eventType: protobufRoute.eventType, payload: new Uint8Array([4, 5]) });
    protobufEnvelope.payloadEncoding = "protobuf";
    await transport.dispatch(protobufEnvelope, protobufRoute, new AbortController().signal);
    expect(captured[0].headers.get("content-type")).toBe("application/x-protobuf");
    expect(captured[0].body).toEqual(new Uint8Array([4, 5]));

    await transport.dispatch(createEnvelope({ eventType: "items:list:v1:requested", payload: { tag: ["a", null, "b"], skip: undefined } }), route({ method: "GET", eventType: "items:list:v1:requested" }), new AbortController().signal);
    expect(new URL(captured[1].url).searchParams.getAll("tag")).toEqual(["a", "b"]);
  });
});
