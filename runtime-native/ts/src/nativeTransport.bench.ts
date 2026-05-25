import { bench, describe } from "vitest";
import { createEnvelope, encodeRuntimeEnvelope } from "@ovasabi/runtime-transport";

import {
  decodeNativeDispatchResponse,
  encodeNativeDispatchFrame,
  validateRuntimeNativeGpuDescriptor,
} from "./index";

const envelope = createEnvelope({
  eventType: "media:process_asset:v1:requested",
  payload: { ok: true, bytes: "x".repeat(1024) },
});
const payload = encodeRuntimeEnvelope(envelope);

const responseFrame = (() => {
  const frame = new Uint8Array(16 + payload.byteLength);
  frame[0] = "O".charCodeAt(0);
  frame[1] = "V".charCodeAt(0);
  frame[2] = "R".charCodeAt(0);
  frame[3] = "R".charCodeAt(0);
  const view = new DataView(frame.buffer);
  view.setUint8(4, 1);
  view.setUint16(6, 0, false);
  view.setUint32(8, payload.byteLength, false);
  view.setUint32(12, 0, false);
  frame.set(payload, 16);
  return frame;
})();

const nativeGpuDescriptor = {
  id: "camera.frame.42",
  kind: "native-gpu-texture",
  platform: "apple-iosurface",
  width: 1920,
  height: 1080,
  format: "bgra8",
  schemaName: "media/v1/frame.capnp",
  producer: "camera.plugin",
  fallback: "copy-to-webgpu",
};

describe("native frame codec", () => {
  bench("encode native dispatch frame", () => {
    encodeNativeDispatchFrame({
      unitId: "media:process_asset:v1:requested",
      schemaVersion: "1.0",
      encoding: 2,
      payload,
    });
  });

  bench("decode native dispatch response", () => {
    decodeNativeDispatchResponse(responseFrame);
  });

  bench("validate native GPU descriptor receipt", () => {
    validateRuntimeNativeGpuDescriptor(nativeGpuDescriptor);
  });
});
