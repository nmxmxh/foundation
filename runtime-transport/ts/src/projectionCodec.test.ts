import { describe, expect, it } from "vitest";

import { encodeRuntimeEnvelope } from "./binaryEnvelope";
import {
  ProjectionSnapshot,
  RecordMutationBatch,
} from "./generated/foundation/v1/projection";
import { createRuntimeTransportProjectionCodec } from "./projectionCodec";

const metadata = {
  correlationId: "corr_projection_codec",
  extra: {},
  idempotencyKey: "idem_projection_codec",
  requestId: "req_projection_codec",
  schemaVersion: "v1",
  timestamp: "2026-07-12T00:00:00.000Z",
};

describe("createRuntimeTransportProjectionCodec", () => {
  it("decodes generated projection snapshots", () => {
    const codec = createRuntimeTransportProjectionCodec();
    const encoded = ProjectionSnapshot.encode(
      ProjectionSnapshot.create({ batch: { mutations: [] }, watermark: "wm-7" })
    ).finish();
    expect(codec.decodeSnapshot(encoded).watermark).toBe("wm-7");
  });

  it("decodes protobuf mutation batches carried by runtime envelopes", () => {
    const codec = createRuntimeTransportProjectionCodec();
    const payload = RecordMutationBatch.encode({ mutations: [] }).finish();
    const frame = encodeRuntimeEnvelope({
      eventType: "projection:records:success",
      metadata,
      payload,
      payloadEncoding: "protobuf",
    });
    expect(codec.decodeDeltaFrame(frame).mutations).toEqual([]);
  });

  it("fails closed to an empty batch for non-binary envelope payloads", () => {
    const codec = createRuntimeTransportProjectionCodec();
    const frame = encodeRuntimeEnvelope({
      eventType: "projection:records:success",
      metadata,
      payload: { ignored: true },
      payloadEncoding: "json",
    });
    expect(codec.decodeDeltaFrame(frame).mutations).toEqual([]);
  });
});
