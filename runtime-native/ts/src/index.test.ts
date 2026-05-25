import { describe, expect, it } from "vitest";
import {
  createEnvelope,
  encodeRuntimeEnvelope,
  type RuntimeRoute,
} from "@ovasabi/runtime-transport";

import {
  NATIVE_CAPABILITIES_COMMAND,
  NATIVE_DISPATCH_COMMAND,
  SECURE_STORE_DELETE_COMMAND,
  SECURE_STORE_GET_COMMAND,
  SECURE_STORE_PUT_COMMAND,
  coerceNativeBytes,
  createNativeRuntimeTransport,
  createNativeSecureStore,
  decodeNativeDispatchResponse,
  encodeNativeDispatchFrame,
  readNativeRuntimeCapabilities,
  validateRuntimeNativeGpuDescriptor,
  type NativeBinaryInvoke,
} from "./index";

const route: RuntimeRoute = {
  method: "POST",
  path: "/native",
  eventType: "media:process_asset:v1:requested",
  requiredCapability: "",
  permission: "write",
};

describe("runtime native transport", () => {
  it("sends encoded runtime envelopes through the native dispatch command", async () => {
    const envelope = createEnvelope({
      eventType: "media:process_asset:v1:requested",
      payload: { ok: true },
      correlationId: "corr_keep",
      requestId: "req_keep",
      idempotencyKey: "idem_keep",
    });
    const seen: unknown[] = [];
    const invoke: NativeBinaryInvoke = async (command, payload) => {
      seen.push(command, payload);
      expect(payload).toBeInstanceOf(Uint8Array);
      return buildNativeResponse(encodeRuntimeEnvelope(envelope));
    };
    const transport = createNativeRuntimeTransport({ invoke });

    const result = await transport.dispatch(envelope, route, new AbortController().signal);

    expect(seen[0]).toBe(NATIVE_DISPATCH_COMMAND);
    expect(seen[1]).toBeInstanceOf(Uint8Array);
    expect(result).toMatchObject({
      eventType: "media:process_asset:v1:requested",
      metadata: {
        correlationId: "corr_keep",
        requestId: "req_keep",
        idempotencyKey: "idem_keep",
      },
    });
  });

  it("rejects unavailable native runtime transport", async () => {
    const transport = createNativeRuntimeTransport({
      invoke: async () => new Uint8Array(),
      isAvailable: () => false,
    });
    await expect(
      transport.dispatch(
        createEnvelope({ eventType: "media:process_asset:v1:requested", payload: {} }),
        route,
        new AbortController().signal
      )
    ).rejects.toThrow("unavailable");
  });

  it("rejects aborted native dispatch without invoking native code", async () => {
    let calls = 0;
    const controller = new AbortController();
    controller.abort();
    const transport = createNativeRuntimeTransport({
      invoke: async () => {
        calls += 1;
        return new Uint8Array();
      },
    });

    await expect(
      transport.dispatch(
        createEnvelope({ eventType: "media:process_asset:v1:requested", payload: {} }),
        route,
        controller.signal
      )
    ).rejects.toThrow("aborted");
    expect(calls).toBe(0);
  });

  it("surfaces native dispatch status and diagnostics", async () => {
    const envelope = createEnvelope({
      eventType: "media:process_asset:v1:requested",
      payload: {},
    });
    const transport = createNativeRuntimeTransport({
      invoke: async () => buildNativeResponse(new Uint8Array(), 7, new TextEncoder().encode("unit denied")),
    });

    await expect(transport.dispatch(envelope, route, new AbortController().signal)).rejects.toThrow(
      "status 7: unit denied"
    );
  });

  it("normalizes native capabilities from snake or camel case", async () => {
    const capabilities = await readNativeRuntimeCapabilities(async (command) => {
      expect(command).toBe(NATIVE_CAPABILITIES_COMMAND);
      return {
        value: {
          native: true,
          native_ffi: true,
          native_shared_memory: false,
          native_gpu: true,
          native_gpu_platforms: ["apple-iosurface", "apple-iosurface", "raw-handle"],
          wasm_sab: true,
          wasm_transfer: true,
          platform: "darwin",
        },
      };
    });

    expect(capabilities).toEqual({
      native: true,
      nativeFfi: true,
      nativeSharedMemory: false,
      nativeGpu: true,
      nativeGpuPlatforms: ["apple-iosurface"],
      wasmSab: true,
      wasmTransfer: true,
      platform: "darwin",
    });
  });

  it("routes secure store calls through native commands", async () => {
    const calls: string[] = [];
    const invoke: NativeBinaryInvoke = async (command) => {
      calls.push(command);
      if (command === SECURE_STORE_GET_COMMAND) {
        return { bytes: [1, 2, 3] };
      }
      if (command === SECURE_STORE_DELETE_COMMAND) {
        return { value: true };
      }
      return { value: null };
    };
    const store = createNativeSecureStore(invoke);

    await store.put("session:token", new Uint8Array([9]));
    await expect(store.get("session:token")).resolves.toEqual(new Uint8Array([1, 2, 3]));
    await expect(store.delete("session:token")).resolves.toBe(true);
    expect(calls).toEqual([SECURE_STORE_PUT_COMMAND, SECURE_STORE_GET_COMMAND, SECURE_STORE_DELETE_COMMAND]);
  });

  it("treats native secure store null values as missing", async () => {
    const store = createNativeSecureStore(async () => ({ value: null }));

    await expect(store.get("session:token")).resolves.toBeNull();
  });

  it("rejects non-byte native responses", () => {
    expect(() => coerceNativeBytes({ value: { ok: true } })).toThrow("bytes");
  });

  it("validates native GPU descriptors as opaque public receipts", () => {
    const descriptor = {
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

    expect(validateRuntimeNativeGpuDescriptor(descriptor)).toEqual({ ok: true, descriptor });
    expect(validateRuntimeNativeGpuDescriptor({ ...descriptor, IOSurface: 123 })).toMatchObject({
      ok: false,
      reason: "native GPU descriptor must not expose raw handle field IOSurface",
    });
  });

  it("encodes and decodes native frames around runtime envelope bytes", () => {
    const payload = encodeRuntimeEnvelope(
      createEnvelope({ eventType: "media:process_asset:v1:requested", payload: { ok: true } })
    );
    const frame = encodeNativeDispatchFrame({
      unitId: "media:process_asset:v1:requested",
      schemaVersion: "1.0",
      encoding: 2,
      payload,
    });
    expect(String.fromCharCode(...frame.subarray(0, 4))).toBe("OVRN");
    const response = decodeNativeDispatchResponse(buildNativeResponse(payload));
    expect(response.statusCode).toBe(0);
    expect(response.payload).toEqual(payload);
  });

  it("rejects native response frames with mismatched lengths", () => {
    const payload = new Uint8Array([1, 2, 3]);
    const frame = buildNativeResponse(payload);
    new DataView(frame.buffer).setUint32(8, payload.byteLength + 1, false);

    expect(() => decodeNativeDispatchResponse(frame)).toThrow("length");
  });
});

const buildNativeResponse = (
  payload: Uint8Array,
  statusCode = 0,
  diagnostics = new Uint8Array()
): Uint8Array => {
  const frame = new Uint8Array(16 + payload.byteLength + diagnostics.byteLength);
  frame[0] = "O".charCodeAt(0);
  frame[1] = "V".charCodeAt(0);
  frame[2] = "R".charCodeAt(0);
  frame[3] = "R".charCodeAt(0);
  const view = new DataView(frame.buffer);
  view.setUint8(4, 1);
  view.setUint8(5, 0);
  view.setUint16(6, statusCode, false);
  view.setUint32(8, payload.byteLength, false);
  view.setUint32(12, diagnostics.byteLength, false);
  frame.set(payload, 16);
  frame.set(diagnostics, 16 + payload.byteLength);
  return frame;
};
