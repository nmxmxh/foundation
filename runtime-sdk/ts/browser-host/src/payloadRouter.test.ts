import { describe, expect, it } from "vitest";
import { RuntimeSharedArena } from "./arena";
import { collectRuntimeStream, routeRuntimePayload, routeRuntimeStream } from "./payloadRouter";

describe("runtime payload routing", () => {
  it("routes small payloads to the control buffer", () => {
    const routed = routeRuntimePayload(new Uint8Array(128), { controlMaxBytes: 4096 });
    expect(routed.lane).toBe("control");
    expect(routed.byteLength).toBe(128);
    expect(routeRuntimePayload(new Uint8Array(0)).lane).toBe("control");
  });

  it("routes medium payloads into the shared arena when available", () => {
    const arena = RuntimeSharedArena.create({ arenaBytes: 2 * 1024 * 1024 });
    const routed = routeRuntimePayload(new Uint8Array(64 * 1024), {
      controlMaxBytes: 4096,
      arenaMaxBytes: 1024 * 1024,
      arena,
    });
    expect(routed.lane).toBe("arena");
    expect(routed.descriptor?.length).toBe(64 * 1024);
  });

  it("routes large payloads to explicit streaming with backpressure-sized chunks", async () => {
    const routed = routeRuntimePayload(new Uint8Array(2 * 1024 * 1024 + 1), {
      controlMaxBytes: 4096,
      arenaMaxBytes: 1024 * 1024,
      chunkBytes: 256 * 1024,
    });
    expect(routed.lane).toBe("stream");
    expect(routed.chunks).toBeDefined();

    let chunks = 0;
    for await (const chunk of routed.chunks!) {
      chunks += 1;
      expect(chunk.byteLength).toBeLessThanOrEqual(256 * 1024);
    }
    expect(chunks).toBeGreaterThan(1);
    const defaultStream = routeRuntimePayload(new Uint8Array(4096));
    expect(defaultStream.lane).toBe("stream");
    expect(await collectRuntimeStream(defaultStream.chunks!)).toHaveLength(4096);
  });

  it("normalizes arbitrary input streams into fixed chunks", async () => {
    const output = await collectRuntimeStream(
      routeRuntimeStream([new Uint8Array(3), new Uint8Array(4), new Uint8Array(5)], { chunkBytes: 5 })
    );
    expect(output.byteLength).toBe(12);
    expect((await collectRuntimeStream(routeRuntimeStream([new Uint8Array([1])])))).toEqual(new Uint8Array([1]));
  });

  it("keeps chunk order and owns output across mixed input sizes", async () => {
    const first = new Uint8Array([1, 2]);
    const second = new Uint8Array([3, 4, 5, 6, 7, 8, 9, 10, 11]);
    const third = new Uint8Array([12, 13]);
    const output: Uint8Array[] = [];
    for await (const chunk of routeRuntimeStream([first, second, third], { chunkBytes: 4 })) {
      output.push(chunk);
    }
    expect(output).toEqual([
      new Uint8Array([1, 2, 3, 4]),
      new Uint8Array([5, 6, 7, 8]),
      new Uint8Array([9, 10, 11, 12]),
      new Uint8Array([13]),
    ]);
    first.fill(0);
    second.fill(0);
    third.fill(0);
    expect(output[0]).toEqual(new Uint8Array([1, 2, 3, 4]));
    expect(output[1]).toEqual(new Uint8Array([5, 6, 7, 8]));
    expect(output[3]).toEqual(new Uint8Array([13]));
  });

  it("accepts asynchronous input without merging its backing buffers", async () => {
    async function* input() {
      yield new Uint8Array([1]);
      yield new Uint8Array(0);
      yield new Uint8Array([2, 3, 4, 5, 6, 7]);
    }
    const output: Uint8Array[] = [];
    for await (const chunk of routeRuntimeStream(input(), { chunkBytes: 3 })) {
      output.push(chunk);
    }
    expect(output).toEqual([
      new Uint8Array([1, 2, 3]),
      new Uint8Array([4, 5, 6]),
      new Uint8Array([7]),
    ]);
  });

  it("normalizes empty chunks and minimum option bounds", async () => {
    const output = await collectRuntimeStream(
      routeRuntimeStream([new Uint8Array(0), new Uint8Array([1]), new Uint8Array([2, 3])], { chunkBytes: 0 })
    );
    expect(output).toEqual(new Uint8Array([1, 2, 3]));
    const routed = routeRuntimePayload(new Uint8Array(1), { controlMaxBytes: 0, arenaMaxBytes: 0, chunkBytes: 0 });
    expect(routed.lane).toBe("stream");
    expect(await collectRuntimeStream(routed.chunks!)).toEqual(new Uint8Array(1));
  });

  it("rejects invalid chunk sizes before losing stream data", async () => {
    for (const chunkBytes of [Number.NaN, Number.POSITIVE_INFINITY, 1.5]) {
      await expect(collectRuntimeStream(routeRuntimeStream([new Uint8Array([1])], { chunkBytes })))
        .rejects.toThrow("finite safe integer");
      const routed = routeRuntimePayload(new Uint8Array(2), {
        controlMaxBytes: 1,
        arenaMaxBytes: 1,
        chunkBytes,
      });
      await expect(collectRuntimeStream(routed.chunks!)).rejects.toThrow("finite safe integer");
    }
  });

  it("surfaces arena queue backpressure after descriptor publication", () => {
    const arena = {
      allocate: () => ({ id: 1 }),
      writeSlab: () => undefined,
      enqueueDescriptorReady: () => false,
      readDescriptor: () => ({ id: 1 }),
    } as unknown as RuntimeSharedArena;
    expect(() => routeRuntimePayload(new Uint8Array(8), { controlMaxBytes: 4, arenaMaxBytes: 16, arena })).toThrow("queue backpressure");
  });
});
