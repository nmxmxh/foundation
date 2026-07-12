import { afterEach, describe, expect, it, vi } from "vitest";
import { encodeRuntimeEnvelope } from "./binaryEnvelope";
import {
  compressRuntimeBytes,
  decompressRuntimeBytes,
  decodeRuntimeBinaryFrame,
  decodeRuntimeBinaryEnvelope,
  encodeRuntimeBinaryFrame,
  supportedRuntimeCompressionEncodings,
} from "./compression";
import { createEnvelope } from "./index";

describe("runtime transport compression", () => {
  afterEach(() => vi.unstubAllGlobals());
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

  it("falls back to identity when compression streams are unavailable", async () => {
    vi.stubGlobal("CompressionStream", undefined);
    vi.stubGlobal("DecompressionStream", undefined);
    const bytes = new Uint8Array([1, 2, 3]);
    await expect(compressRuntimeBytes(bytes, { enabled: true, minBytes: 0, preferred: ["gzip"] })).resolves.toEqual({ bytes, encoding: "identity", rawLength: 3 });
    expect(supportedRuntimeCompressionEncodings()).toEqual(["identity"]);
    await expect(decompressRuntimeBytes(bytes, "gzip")).rejects.toThrow("DecompressionStream is unavailable");
    await expect(decompressRuntimeBytes(bytes, "identity")).resolves.toBe(bytes);
    await expect(decodeRuntimeBinaryFrame(bytes)).resolves.toBe(bytes);
  });

  it("returns identity when explicitly preferred or compression is not smaller", async () => {
    const bytes = new Uint8Array([1]);
    await expect(compressRuntimeBytes(bytes, { enabled: true, minBytes: 0, preferred: ["identity"] })).resolves.toMatchObject({ encoding: "identity" });
    await expect(compressRuntimeBytes(bytes, { enabled: false })).resolves.toMatchObject({ encoding: "identity" });
  });

  it("rejects corrupt compressed payloads and declared raw-length mismatches", async () => {
    if (typeof CompressionStream === "undefined" || typeof DecompressionStream === "undefined") return;
    await expect(decompressRuntimeBytes(new Uint8Array([1, 2, 3]), "gzip")).rejects.toThrow("failed to decompress gzip");
    const raw = new TextEncoder().encode("compressible|".repeat(100));
    const compressed = await compressRuntimeBytes(raw, { enabled: true, minBytes: 0, preferred: ["gzip"] });
    const frame = encodeRuntimeBinaryFrame(compressed);
    new DataView(frame.buffer).setUint32(8, raw.byteLength + 1, false);
    await expect(decodeRuntimeBinaryFrame(frame)).rejects.toThrow("length mismatch");
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
