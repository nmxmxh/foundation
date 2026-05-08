import { describe, expect, it } from "vitest";
import { encodeRuntimeEnvelope } from "./binaryEnvelope";
import {
  compressRuntimeBytes,
  decodeRuntimeBinaryFrame,
  decodeRuntimeBinaryEnvelope,
  encodeRuntimeBinaryFrame,
} from "./compression";
import { createEnvelope } from "./index";

describe("runtime transport compression", () => {
  it("keeps small binary envelopes on the identity path", async () => {
    const envelope = createEnvelope({
      eventType: "system:ping:v1:requested",
      payload: { ok: true },
    });
    const encoded = encodeRuntimeEnvelope(envelope);
    const compressed = await compressRuntimeBytes(encoded, {
      enabled: true,
      minBytes: encoded.byteLength + 1,
      preferred: ["gzip"],
    });

    expect(compressed.encoding).toBe("identity");
    const decoded = await decodeRuntimeBinaryEnvelope(encodeRuntimeBinaryFrame(compressed));
    expect(decoded.eventType).toBe("system:ping:v1:requested");
    expect(decoded.payload).toEqual({ ok: true });
  });

  it("round-trips compressed binary websocket frames when gzip streams are available", async () => {
    if (typeof CompressionStream === "undefined" || typeof DecompressionStream === "undefined") {
      return;
    }

    const envelope = createEnvelope({
      eventType: "system:bulk_payload:v1:requested",
      payload: { body: "foundation-runtime-transport|".repeat(1024) },
    });
    const encoded = encodeRuntimeEnvelope(envelope);
    const compressed = await compressRuntimeBytes(encoded, {
      enabled: true,
      minBytes: 1,
      preferred: ["gzip", "identity"],
    });

    expect(compressed.encoding).toBe("gzip");
    expect(compressed.bytes.byteLength).toBeLessThan(encoded.byteLength);

    const decoded = await decodeRuntimeBinaryEnvelope(encodeRuntimeBinaryFrame(compressed));
    expect(decoded.eventType).toBe("system:bulk_payload:v1:requested");
    expect(decoded.payload).toEqual(envelope.payload);
  });

  it("rejects malformed runtime binary frames before envelope decode", async () => {
    const malformed = new Uint8Array(20);
    const view = new DataView(malformed.buffer);
    view.setUint32(0, 0x4f565254, false);
    view.setUint8(4, 2);
    await expect(decodeRuntimeBinaryFrame(malformed)).rejects.toThrow(/unsupported runtime binary frame version/);

    view.setUint8(4, 1);
    view.setUint8(5, 99);
    await expect(decodeRuntimeBinaryFrame(malformed)).rejects.toThrow(/unsupported runtime binary frame encoding id/);

    view.setUint8(5, 1);
    view.setUint32(12, 100, false);
    await expect(decodeRuntimeBinaryFrame(malformed)).rejects.toThrow(/runtime binary frame is truncated/);
  });
});
