import { describe, expect, it } from "vitest";
import { PERFORMANCE_TRANSPORT_ORDER, createEnvelope, createRouteRegistry, createCommandBus, type TransportDiagnostics, type TransportStrategy } from "./index";

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
});
