import { describe, expect, it, vi } from "vitest";

import { createAppRuntime } from "./appRuntime";
import {
  createRouteRegistry,
  type RuntimeEnvelope,
  type RuntimeRoute,
  type Subscription,
  type TransportStrategy,
} from "./index";

const routes: RuntimeRoute[] = [
  { method: "POST", path: "/v1/user/create", eventType: "user:create:v1:requested", requiredCapability: "user.write", permission: "write" },
  { method: "PUT", path: "/media/upload", eventType: "media:upload:v1:requested", requiredCapability: "media.write", permission: "write" },
];

const registry = () => createRouteRegistry(routes);

/** A recording HTTP-kind transport that captures the envelope/route it received. */
const recordingHttp = (result: unknown = { ok: true }) => {
  const calls: Array<{ envelope: RuntimeEnvelope<unknown>; route: RuntimeRoute }> = [];
  const strategy: TransportStrategy = {
    kind: "http",
    async dispatch(envelope, route) {
      calls.push({ envelope: envelope as RuntimeEnvelope<unknown>, route });
      return result;
    },
  };
  return { strategy, calls };
};

describe("createAppRuntime", () => {
  it("validates required inputs", () => {
    expect(() => createAppRuntime({ registry: undefined as never, strategies: [] })).toThrow(/route registry/);
    expect(() => createAppRuntime({ registry: registry(), strategies: [] })).toThrow(/transport strategy/);
  });

  it("dispatches by resolving the real route from the registry (no client re-derivation)", async () => {
    const http = recordingHttp({ accepted: true });
    const runtime = createAppRuntime({ registry: registry(), strategies: [http.strategy] });

    const result = await runtime.dispatch<{ accepted: boolean }>("media:upload:v1:requested", { name: "x.png" });

    expect(result.data).toEqual({ accepted: true });
    expect(http.calls).toHaveLength(1);
    // The bus handed the transport the registry's route — custom path included.
    expect(http.calls[0].route.path).toBe("/media/upload");
    expect(http.calls[0].route.method).toBe("PUT");
    expect(http.calls[0].envelope.eventType).toBe("media:upload:v1:requested");
    expect(http.calls[0].envelope.payload).toEqual({ name: "x.png" });
  });

  it("generates correlation/request/idempotency tokens and returns them", async () => {
    const http = recordingHttp();
    const runtime = createAppRuntime({ registry: registry(), strategies: [http.strategy] });

    const result = await runtime.dispatch("user:create:v1:requested", { email: "a@b.c" });

    expect(result.correlationId).toBeTruthy();
    expect(result.requestId).toBeTruthy();
    expect(result.idempotencyKey).toBeTruthy();
    // The tokens sent on the wire match what is returned.
    const sent = http.calls[0].envelope.metadata;
    expect(sent.correlationId).toBe(result.correlationId);
    expect(sent.idempotencyKey).toBe(result.idempotencyKey);
  });

  it("honors explicit override tokens", async () => {
    const http = recordingHttp();
    const runtime = createAppRuntime({ registry: registry(), strategies: [http.strategy] });

    const result = await runtime.dispatch("user:create:v1:requested", {}, {
      correlationId: "corr-fixed",
      idempotencyKey: "idem-fixed",
      extra: { source: "test" },
    });

    expect(result.correlationId).toBe("corr-fixed");
    expect(result.idempotencyKey).toBe("idem-fixed");
    expect(http.calls[0].envelope.metadata.extra).toMatchObject({ source: "test" });
  });

  it("defaults an empty payload object when none is given", async () => {
    const http = recordingHttp();
    const runtime = createAppRuntime({ registry: registry(), strategies: [http.strategy] });
    await runtime.dispatch("user:create:v1:requested");
    expect(http.calls[0].envelope.payload).toEqual({});
  });

  it("rejects an unregistered event type", async () => {
    const http = recordingHttp();
    const runtime = createAppRuntime({ registry: registry(), strategies: [http.strategy] });
    await expect(runtime.dispatch("ghost:do:v1:requested", {})).rejects.toThrow(/not registered/);
  });

  it("gates dispatch on capabilities and exposes canDispatch", async () => {
    const http = recordingHttp();
    const runtime = createAppRuntime({
      registry: registry(),
      strategies: [http.strategy],
      grantedCapabilities: ["user.write"], // media.write withheld
    });

    expect(runtime.canDispatch("user:create:v1:requested")).toBe(true);
    expect(runtime.canDispatch("media:upload:v1:requested")).toBe(false);
    expect(runtime.canDispatch("ghost:do:v1:requested")).toBe(false);

    await expect(runtime.dispatch("media:upload:v1:requested", {})).rejects.toThrow(/not allowed/);
    expect(http.calls).toHaveLength(0);
  });

  it("defaults to allow-all capabilities when none are supplied", async () => {
    const http = recordingHttp();
    const runtime = createAppRuntime({ registry: registry(), strategies: [http.strategy] });
    expect(runtime.canDispatch("media:upload:v1:requested")).toBe(true);
    await expect(runtime.dispatch("media:upload:v1:requested", {})).resolves.toBeDefined();
  });

  it("applies a custom policy predicate", () => {
    const http = recordingHttp();
    const runtime = createAppRuntime({
      registry: registry(),
      strategies: [http.strategy],
      hasPolicyAccess: (route) => route?.eventType !== "media:upload:v1:requested",
    });
    expect(runtime.canDispatch("user:create:v1:requested")).toBe(true);
    expect(runtime.canDispatch("media:upload:v1:requested")).toBe(false);
  });

  it("resolveRoute surfaces the registry route", () => {
    const runtime = createAppRuntime({ registry: registry(), strategies: [recordingHttp().strategy] });
    expect(runtime.resolveRoute("user:create:v1:requested")?.path).toBe("/v1/user/create");
    expect(runtime.resolveRoute("nope:x:v1:requested")).toBeUndefined();
  });

  it("subscribe delegates to the websocket transport", async () => {
    const subscription: Subscription = { unsubscribe: vi.fn() };
    const subscribe = vi.fn(async () => subscription);
    const ws: TransportStrategy = {
      kind: "ws",
      async dispatch() {
        return undefined;
      },
      subscribe,
    };
    const runtime = createAppRuntime({ registry: registry(), strategies: [recordingHttp().strategy, ws] });

    const result = await runtime.subscribe("user:*", () => {});
    expect(subscribe).toHaveBeenCalledWith("user:*", expect.any(Function));
    expect(result).toBe(subscription);
  });

  it("forwards diagnostics from the bus", async () => {
    const http = recordingHttp();
    const onDiagnostics = vi.fn();
    const runtime = createAppRuntime({ registry: registry(), strategies: [http.strategy], onDiagnostics });
    await runtime.dispatch("user:create:v1:requested", {});
    expect(onDiagnostics).toHaveBeenCalledTimes(1);
    expect(onDiagnostics.mock.calls[0][0]).toMatchObject({ transport: "http", eventType: "user:create:v1:requested" });
  });
});
