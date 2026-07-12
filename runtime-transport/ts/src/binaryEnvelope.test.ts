import { describe, expect, it } from "vitest";

import {
  decodeJSONRuntimeEnvelope,
  decodeRuntimeEnvelope,
  encodeJSONRuntimeEnvelope,
  encodeRuntimeEnvelope,
  tryDecodeRuntimeEnvelope,
} from "./binaryEnvelope";
import { createEnvelope } from "./index";

describe("runtime binary envelope codec", () => {
  it.each(["protobuf", "capnp", "binary"] as const)("round-trips %s byte payloads", (payloadEncoding) => {
    const envelope = createEnvelope({ eventType: "runtime:dispatch:v1:requested", payload: new Uint8Array([1, 2, 3]) });
    envelope.payloadEncoding = payloadEncoding;
    const encoded = encodeRuntimeEnvelope(envelope);
    expect(decodeRuntimeEnvelope(encoded)).toMatchObject({ eventType: envelope.eventType, payloadEncoding, payload: new Uint8Array([1, 2, 3]) });
    expect(tryDecodeRuntimeEnvelope(Uint8Array.from(encoded).buffer)).toMatchObject({ payloadEncoding });
  });

  it("accepts ArrayBuffer binary payloads and rejects object payloads", () => {
    const envelope = createEnvelope({ eventType: "runtime:dispatch:v1:requested", payload: new Uint8Array([4]).buffer });
    envelope.payloadEncoding = "protobuf";
    expect(decodeRuntimeEnvelope(encodeRuntimeEnvelope(envelope)).payload).toEqual(new Uint8Array([4]));
    expect(() => encodeRuntimeEnvelope({ ...envelope, payload: { invalid: true } })).toThrow("require Uint8Array payloads");
  });

  it("normalizes JSON metadata and strips reserved extra keys", () => {
    const envelope = createEnvelope({ eventType: "asset:get:v1:requested", payload: { id: 1 }, extra: { correlation_id: "spoofed", trace_hint: "edge" } });
    const encoded = encodeJSONRuntimeEnvelope(envelope);
    const raw = JSON.parse(encoded) as { metadata: Record<string, unknown> };
    expect(raw.metadata).toMatchObject({ correlation_id: envelope.metadata.correlationId, trace_hint: "edge" });
    const decoded = tryDecodeRuntimeEnvelope(encoded);
    expect(decoded).toMatchObject({ eventType: envelope.eventType, payload: { id: 1 }, metadata: { correlationId: envelope.metadata.correlationId } });
  });

  it("defaults missing JSON fields and rejects incompatible schemas", () => {
    const decoded = decodeJSONRuntimeEnvelope('{"eventType":"asset:get:v1:requested"}');
    expect(decoded.payload).toEqual({});
    expect(decoded.payloadEncoding).toBe("json");
    expect(() => decodeJSONRuntimeEnvelope('{"event_type":"asset:get:v1:requested","schema_version":"v99"}')).toThrow("unsupported envelope schema version");
  });
});
