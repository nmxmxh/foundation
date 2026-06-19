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

  it("strips the captured auth token on enqueue so it cannot be replayed stale", () => {
    const queue = createOfflineQueue({ maxQueueSize: 2 });
    queue.enqueue(
      createEnvelope({
        eventType: "order:create:v1:requested",
        payload: {},
        extra: { auth_token: "stale-token", trace: "keep-me" },
      })
    );

    const [entry] = queue.drain();
    expect(entry.envelope.metadata.extra).not.toHaveProperty("auth_token");
    // Non-credential context is preserved.
    expect(entry.envelope.metadata.extra).toMatchObject({ trace: "keep-me" });
  });

  it("re-stamps a fresh auth token on drain after rotation", () => {
    let current = "token-v1";
    const queue = createOfflineQueue({ maxQueueSize: 2, resolveAuthToken: () => current });
    queue.enqueue(
      createEnvelope({
        eventType: "order:create:v1:requested",
        payload: {},
        extra: { auth_token: "token-v1" },
      })
    );

    // Session rotates (e.g. context switch) while the request sits queued.
    current = "token-v2";

    const [entry] = queue.drain();
    expect(entry.envelope.metadata.extra.auth_token).toBe("token-v2");
  });

  it("does not mutate the caller's original envelope when stripping", () => {
    const queue = createOfflineQueue({ maxQueueSize: 1 });
    const envelope = createEnvelope({
      eventType: "order:create:v1:requested",
      payload: {},
      extra: { auth_token: "stale-token" },
    });
    queue.enqueue(envelope);
    expect(envelope.metadata.extra.auth_token).toBe("stale-token");
  });
});
