import { describe, expect, it } from "vitest";
import { PERFORMANCE_TRANSPORT_ORDER, assertCompatibleSchemaVersion, canDispatch, createEnvelope, createProtobufEnvelope, createRouteRegistry, createCommandBus, decodeProtobufPayload, eventTerminalState, parseEventType, type TransportDiagnostics, type TransportStrategy } from "./index";

describe("performance-first transport defaults", () => {
  it("defaults routes to the performance ladder", () => {
    const registry = createRouteRegistry([
      {
        method: "POST",
        path: "/v1/media/assets",
        eventType: "media:process_asset:v1:requested",
        requiredCapability: "media.write",
        permission: "write",
      },
    ]);
    expect(registry.routes[0]?.transportOrder).toEqual(Array.from(PERFORMANCE_TRANSPORT_ORDER));
  });

  it("tries faster available lanes before HTTP", async () => {
    const attempts: string[] = [];
    const registry = createRouteRegistry([
      {
        method: "POST",
        path: "/v1/media/assets",
        eventType: "media:process_asset:v1:requested",
        requiredCapability: "",
        permission: "write",
      },
    ]);
    const wasm: TransportStrategy = {
      kind: "wasm",
      async dispatch() {
        attempts.push("wasm");
        throw new Error("wasm unavailable");
      },
    };
    const native: TransportStrategy = {
      kind: "native",
      async dispatch() {
        attempts.push("native");
        throw new Error("native unavailable");
      },
    };
    const ws: TransportStrategy = {
      kind: "ws",
      async dispatch() {
        attempts.push("ws");
        return "ok";
      },
    };
    const http: TransportStrategy = {
      kind: "http",
      async dispatch() {
        attempts.push("http");
        return "slow";
      },
    };
    const bus = createCommandBus({
      registry,
      strategies: [http, ws, native, wasm],
      grantedCapabilities: ["*"],
      hasPolicyAccess: () => true,
    });
    await expect(bus.dispatch(createEnvelope({ eventType: "media:process_asset:v1:requested", payload: {} }))).resolves.toBe("ok");
    expect(attempts).toEqual(["wasm", "native", "ws"]);
  });

  it("preserves request identity in diagnostics across fallback attempts", async () => {
    const diagnostics: TransportDiagnostics[] = [];
    const registry = createRouteRegistry([
      {
        method: "POST",
        path: "/v1/media/assets",
        eventType: "media:process_asset:v1:requested",
        requiredCapability: "",
        permission: "write",
        transportOrder: ["sab", "http"],
      },
    ]);
    const sab: TransportStrategy = {
      kind: "sab",
      async dispatch() {
        throw new Error("sab unavailable");
      },
    };
    const http: TransportStrategy = {
      kind: "http",
      async dispatch() {
        return "ok";
      },
    };
    const bus = createCommandBus({
      registry,
      strategies: [sab, http],
      grantedCapabilities: ["*"],
      hasPolicyAccess: () => true,
      onDiagnostics: (entry) => diagnostics.push(entry),
    });
    await expect(
      bus.dispatch(
        createEnvelope({
          eventType: "media:process_asset:v1:requested",
          payload: {},
          correlationId: "corr_keep",
          requestId: "req_keep",
          idempotencyKey: "idem_keep",
        })
      )
    ).resolves.toBe("ok");
    expect(diagnostics).toHaveLength(2);
    expect(diagnostics.map((entry) => [entry.transport, entry.correlationId, entry.requestId, entry.idempotencyKey])).toEqual([
      ["sab", "corr_keep", "req_keep", "idem_keep"],
      ["http", "corr_keep", "req_keep", "idem_keep"],
    ]);
    expect(diagnostics[0]?.error).toContain("sab unavailable");
  });

  it("places native after WASM and before network fallbacks", () => {
    expect(PERFORMANCE_TRANSPORT_ORDER).toEqual([
      "sab",
      "wasm",
      "native",
      "transferable",
      "ws",
      "http",
      "postMessage",
    ]);
  });

  it("validates event, schema, route, and protobuf boundaries", () => {
    expect(eventTerminalState("asset:get:v1:success")).toBe("success");
    expect(parseEventType("asset:get:preview:v2:requested")).toMatchObject({ domain: "asset", action: "get:preview", version: "v2" });
    for (const invalid of ["", "asset:get", "asset:get:unknown", "Asset:get:requested", "asset:v1:requested", "asset:bad-action:requested"]) {
      expect(() => parseEventType(invalid)).toThrow();
    }
    expect(assertCompatibleSchemaVersion("v1")).toBe("1.0");
    expect(() => assertCompatibleSchemaVersion("v2")).toThrow("unsupported envelope schema version");

    const codec = { encode: (value: string) => new TextEncoder().encode(value), decode: (bytes: Uint8Array) => new TextDecoder().decode(bytes) };
    const encoded = createProtobufEnvelope({ eventType: "asset:get:v1:requested", payload: "asset" }, codec);
    expect(decodeProtobufPayload(encoded.payload, codec)).toBe("asset");
    expect(decodeProtobufPayload(Uint8Array.from(encoded.payload).buffer, codec)).toBe("asset");
    expect(() => decodeProtobufPayload({}, codec)).toThrow("require Uint8Array");

    expect(() => createRouteRegistry([{ method: "", path: "/x", eventType: "asset:get:v1:requested", requiredCapability: "", permission: "view" }])).toThrow("requires method and path");
    const duplicate = { method: "GET", path: "/x", eventType: "asset:get:v1:requested", requiredCapability: "", permission: "view" as const };
    expect(() => createRouteRegistry([duplicate, duplicate])).toThrow("duplicate route registration");
    expect(() => createRouteRegistry([duplicate, { ...duplicate, eventType: "asset:list:v1:requested" }])).toThrow("duplicate route registration for path");
  });

  it("enforces capability inheritance and command-bus failure contracts", async () => {
    const route = { method: "POST", path: "/x", eventType: "asset:update:v1:requested", requiredCapability: "asset.update", permission: "write" as const };
    expect(canDispatch(route, ["asset.admin"], () => true)).toBe(true);
    expect(canDispatch(route, ["asset.view"], () => true)).toBe(false);
    expect(canDispatch(route, ["asset.*"], () => true)).toBe(true);
    expect(canDispatch(undefined, ["*"], () => true)).toBe(false);
    expect(canDispatch({ ...route, permission: "view" }, ["asset.write"], () => true)).toBe(true);
    expect(canDispatch({ ...route, permission: "view" }, ["other.view"], () => true)).toBe(false);
    expect(canDispatch({ ...route, permission: "admin" }, ["asset.admin"], () => true)).toBe(true);
    expect(canDispatch({ ...route, permission: "admin" }, ["asset.write"], () => true)).toBe(false);

    const registry = createRouteRegistry([route]);
    const denied = createCommandBus({ registry, strategies: [], grantedCapabilities: [], hasPolicyAccess: () => false });
    await expect(denied.dispatch(createEnvelope({ eventType: route.eventType, payload: {} }))).rejects.toThrow("not allowed");
    await expect(denied.dispatch(createEnvelope({ eventType: "asset:get:v1:requested", payload: {} }))).rejects.toThrow("not registered");
    await expect(denied.subscribe("*", () => undefined)).rejects.toThrow("websocket transport is required");

    const allowed = createCommandBus({ registry, strategies: [], grantedCapabilities: ["asset.write"], hasPolicyAccess: () => true });
    await expect(allowed.dispatch(createEnvelope({ eventType: route.eventType, payload: {} }))).rejects.toThrow("no transport strategy");

    const stringFailure: TransportStrategy = { kind: "http", async dispatch() { throw "offline"; } };
    const failed = createCommandBus({ registry, strategies: [stringFailure], grantedCapabilities: ["asset.write"], hasPolicyAccess: () => true });
    await expect(failed.dispatch(createEnvelope({ eventType: route.eventType, payload: {} }))).rejects.toThrow("transport dispatch failed");

    const subscription = { unsubscribe: () => undefined };
    const ws: TransportStrategy = { kind: "ws", async dispatch() { return undefined; }, async subscribe() { return subscription; } };
    const subscribed = createCommandBus({ registry, strategies: [ws], grantedCapabilities: ["asset.write"], hasPolicyAccess: () => true });
    await expect(subscribed.subscribe("asset:*", () => undefined)).resolves.toBe(subscription);
  });
});
