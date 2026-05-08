import { bench, describe } from "vitest";
import {
  canDispatch,
  createEnvelope,
  createRouteRegistry,
  parseEventType,
  resolveRoute,
} from "./index";

const route = {
  method: "POST",
  path: "/v1/runtime/dispatch",
  eventType: "runtime:dispatch:v1:requested",
  requiredCapability: "runtime.write",
  permission: "write" as const,
};
const registry = createRouteRegistry([
  route,
  ...Array.from({ length: 31 }, (_, index) => ({
    ...route,
    path: `/v1/runtime/dispatch/${index}`,
    eventType: `runtime:dispatch_${index}:v1:requested`,
  })),
]);
const resolvedRoute = resolveRoute(registry, route.eventType);

describe("runtime transport routing hot paths", () => {
  bench("parse event type", () => {
    parseEventType("runtime:dispatch:v1:requested");
  });

  bench("create json envelope", () => {
    createEnvelope({
      eventType: "runtime:dispatch:v1:requested",
      payload: { ok: true },
      correlationId: "corr_bench",
      requestId: "req_bench",
      idempotencyKey: "idem_bench",
    });
  });

  bench("resolve route by event type", () => {
    if (!resolveRoute(registry, route.eventType)) {
      throw new Error("route not found");
    }
  });

  bench("resolve route by path", () => {
    if (!registry.resolveRouteByPath("post", "/v1/runtime/dispatch")) {
      throw new Error("route path not found");
    }
  });

  bench("can dispatch exact capability", () => {
    if (!canDispatch(resolvedRoute, ["runtime.write"], () => true)) {
      throw new Error("dispatch denied");
    }
  });

  bench("can dispatch write via admin fallback", () => {
    if (!canDispatch(resolvedRoute, ["runtime.admin"], () => true)) {
      throw new Error("dispatch denied");
    }
  });
});
