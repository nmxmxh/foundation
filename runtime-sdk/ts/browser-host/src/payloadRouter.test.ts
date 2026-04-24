import { describe, expect, it } from "vitest";
import { RuntimeSharedArena } from "./arena";
import { collectRuntimeStream, routeRuntimePayload, routeRuntimeStream } from "./payloadRouter";

describe("runtime payload routing", () => {
  it("routes small payloads to the control buffer", () => {
    const routed = routeRuntimePayload(new Uint8Array(128), { controlMaxBytes: 4096 });
    expect(routed.lane).toBe("control");
    expect(routed.byteLength).toBe(128);
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
  });

  it("normalizes arbitrary input streams into fixed chunks", async () => {
    const output = await collectRuntimeStream(
      routeRuntimeStream([new Uint8Array(3), new Uint8Array(4), new Uint8Array(5)], { chunkBytes: 5 })
    );
    expect(output.byteLength).toBe(12);
  });
});
