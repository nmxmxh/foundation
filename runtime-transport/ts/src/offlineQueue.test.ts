import { describe, expect, it } from "vitest";
import { createEnvelope } from "./index";
import { createOfflineQueue } from "./offlineQueue";

describe("offline queue", () => {
  it("queues mutations and drains in order", () => {
    const queue = createOfflineQueue({ maxQueueSize: 2, conflictResolution: "manual" });
    queue.enqueue(createEnvelope({ eventType: "order:create:v1:requested", payload: { id: "1" } }));
    queue.enqueue(createEnvelope({ eventType: "order:update:v1:requested", payload: { id: "1" } }));

    expect(queue.size()).toBe(2);
    expect(queue.conflictResolution()).toBe("manual");
    expect(queue.drain().map((entry) => entry.envelope.eventType)).toEqual([
      "order:create:v1:requested",
      "order:update:v1:requested",
    ]);
    expect(queue.size()).toBe(0);
  });

  it("fails closed when queue capacity is exceeded", () => {
    const queue = createOfflineQueue({ maxQueueSize: 1 });
    queue.enqueue(createEnvelope({ eventType: "order:create:v1:requested", payload: {} }));
    expect(() => queue.enqueue(createEnvelope({ eventType: "order:create:v1:requested", payload: {} }))).toThrow(
      /capacity exceeded/
    );
  });

  it("exposes bounded queue state for sync/backpressure decisions", () => {
    const queue = createOfflineQueue({ maxQueueSize: 3 });
    queue.enqueue(createEnvelope({ eventType: "order:create:v1:requested", payload: {} }));

    expect(queue.snapshot()).toMatchObject({
      size: 1,
      capacity: 3,
      attempts: 0,
    });
    expect(queue.snapshot().oldestQueuedAt).toEqual(expect.any(String));
  });
});
