import { bench, describe } from "vitest";
import {
  decodeRuntimeEnvelope,
  encodeJSONRuntimeEnvelope,
  encodeRuntimeEnvelope,
} from "./binaryEnvelope";
import { decodeRuntimeBinaryFrame, encodeRuntimeBinaryFrame } from "./compression";
import { createEnvelope, createProtobufEnvelope, type ProtobufCodec } from "./index";

const jsonEnvelope = createEnvelope({
  eventType: "runtime:dispatch:v1:requested",
  payload: {
    body: "runtime-transport-json-payload|".repeat(64),
    sequence: 42,
  },
  correlationId: "corr_bench",
  requestId: "req_bench",
  idempotencyKey: "idem_bench",
});

const protobufCodec: ProtobufCodec<Uint8Array> = {
  encode: (payload) => payload,
  decode: (payload) => payload,
};

const protobufEnvelope = createProtobufEnvelope({
  eventType: "runtime:dispatch:v1:requested",
  payload: new Uint8Array(1024).fill(17),
  correlationId: "corr_proto",
  requestId: "req_proto",
  idempotencyKey: "idem_proto",
}, protobufCodec);

const encodedJSONEnvelope = encodeRuntimeEnvelope(jsonEnvelope);
const encodedProtobufEnvelope = encodeRuntimeEnvelope(protobufEnvelope);
const framedIdentity = encodeRuntimeBinaryFrame({
  bytes: encodedProtobufEnvelope,
  encoding: "identity",
  rawLength: encodedProtobufEnvelope.byteLength,
});

describe("runtime transport binary envelope", () => {
  bench("encode json envelope to protobuf bytes", () => {
    encodeRuntimeEnvelope(jsonEnvelope);
  });

  bench("decode json envelope from protobuf bytes", () => {
    decodeRuntimeEnvelope(encodedJSONEnvelope);
  });

  bench("encode protobuf envelope bytes", () => {
    encodeRuntimeEnvelope(protobufEnvelope);
  });

  bench("decode protobuf envelope bytes", () => {
    decodeRuntimeEnvelope(encodedProtobufEnvelope);
  });

  bench("encode json compatibility envelope", () => {
    encodeJSONRuntimeEnvelope(jsonEnvelope);
  });

  bench("decode identity binary frame", async () => {
    await decodeRuntimeBinaryFrame(framedIdentity);
  });
});
