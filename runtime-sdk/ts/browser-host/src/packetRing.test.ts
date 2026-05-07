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
    expect(ring.depth()).toBe(0);
    expect(ring.counters()).toMatchObject({
      enqueued: 1,
      dequeued: 1,
      completed: 1,
      released: 1,
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
});
