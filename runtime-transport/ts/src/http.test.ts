import { describe, expect, it } from "vitest";

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
});
