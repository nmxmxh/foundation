import { describe, expect, it } from "vitest";
import { PERFORMANCE_TRANSPORT_ORDER, createEnvelope, createRouteRegistry, createCommandBus, type TransportStrategy } from "./index";

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
      strategies: [http, ws, wasm],
      grantedCapabilities: ["*"],
      hasPolicyAccess: () => true,
    });
    await expect(bus.dispatch(createEnvelope({ eventType: "media:process_asset:v1:requested", payload: {} }))).resolves.toBe("ok");
    expect(attempts).toEqual(["wasm", "ws"]);
  });
});
