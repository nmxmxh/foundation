import { describe, expect, it } from "vitest";
import { RuntimePacketRing, type RuntimePacketDescriptor } from "./packetRing";

describe("RuntimePacketRing", () => {
  it("moves descriptors through rx, processing, tx, and free states", () => {
    let now = 0n;
    const ring = new RuntimePacketRing({
      slots: 4,
      slotBytes: 64,
      nowNs: () => {
        now += 10n;
        return now;
      },
    });
    const payload = new Uint8Array([1, 2, 3, 4]);
    const descriptor = ring.enqueue(payload, 7);
    const timestamps = descriptor?.timestamps;

    expect(descriptor?.state).toBe("rx-ready");
    expect(descriptor?.timestamps.ingressNs).toBe(10n);
    expect(ring.depth()).toBe(1);

    const dequeued = ring.dequeue();
    expect(dequeued?.id).toBe(descriptor?.id);
    expect(dequeued?.state).toBe("processing");
    expect(ring.view(dequeued?.id ?? -1)).toEqual(payload);

    const completed = ring.complete(dequeued?.id ?? -1);
    expect(completed.state).toBe("tx-ready");
    expect(completed.timestamps.processedNs).toBe(30n);

    ring.release(completed.id);
    const reused = ring.enqueue(payload, 3);
    expect(reused?.id).toBe(completed.id);
    expect(reused?.timestamps).toBe(timestamps);
    expect(reused?.timestamps.ingressNs).toBe(50n);
    expect(reused?.timestamps.dequeuedNs).toBe(0n);
    const reusedDequeued = ring.dequeue();
    expect(reusedDequeued?.id).toBe(reused?.id);
    ring.complete(reusedDequeued?.id ?? -1);
    ring.release(reusedDequeued?.id ?? -1);
    expect(ring.depth()).toBe(0);
    expect(ring.counters()).toMatchObject({
      enqueued: 2,
      dequeued: 2,
      completed: 2,
      released: 2,
      dropped: 0,
      highWaterDepth: 1,
    });
  });

  it("supports burst enqueue and dequeue with bounded capacity", () => {
    const ring = new RuntimePacketRing({ slots: 2, slotBytes: 8, nowNs: () => 1n });
    const payloads = [new Uint8Array([1]), new Uint8Array([2]), new Uint8Array([3])];

    expect(ring.enqueueBurst(payloads)).toBe(2);
    expect(ring.counters().dropped).toBe(1);

    const scratch: RuntimePacketDescriptor[] = [];
    const drained = ring.dequeueBurst(8, scratch);
    expect(drained.descriptors).toBe(scratch);
    expect(drained.count).toBe(2);
    expect(drained.descriptors.map((descriptor) => descriptor.id)).toEqual([0, 1]);
  });

  it("drains descriptor ids into caller-owned scratch storage", () => {
    const ring = new RuntimePacketRing({ slots: 4, slotBytes: 8, nowNs: () => 1n });
    const payloads = [new Uint8Array([1]), new Uint8Array([2]), new Uint8Array([3])];
    const ids: number[] = [99];

    expect(ring.enqueueBurst(payloads)).toBe(3);
    expect(ring.dequeueIdsBurstInto(2, ids)).toBe(2);
    expect(ids).toEqual([0, 1]);
    for (const id of ids) {
      ring.complete(id);
      ring.release(id);
    }

    const drained = ring.dequeueIdsBurst(8, ids);
    expect(drained.descriptorIds).toBe(ids);
    expect(drained.count).toBe(1);
    expect(drained.descriptorIds).toEqual([2]);
  });
});
