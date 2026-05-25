import {
  decodeRuntimeEnvelope,
  encodeRuntimeEnvelope,
  type RuntimeEnvelope,
  type RuntimeRoute,
  type Subscription,
  type TransportStrategy,
} from "@ovasabi/runtime-transport";
import type { RuntimeNativeGpuPlatform } from "./nativeGpu";

export * from "./nativeGpu";

export const NATIVE_DISPATCH_COMMAND = "foundation_runtime_dispatch";
export const NATIVE_CAPABILITIES_COMMAND = "foundation_runtime_capabilities";
export const SECURE_STORE_GET_COMMAND = "foundation_secure_store_get";
export const SECURE_STORE_PUT_COMMAND = "foundation_secure_store_put";
export const SECURE_STORE_DELETE_COMMAND = "foundation_secure_store_delete";
export const NATIVE_REQUEST_MAGIC = "OVRN";
export const NATIVE_RESPONSE_MAGIC = "OVRR";
export const NATIVE_FRAME_VERSION = 1;
export const NATIVE_ENCODING_CAPNP = 1;
export const NATIVE_ENCODING_PROTOBUF = 2;

export type NativeRuntimeCapabilities = {
  native: boolean;
  nativeFfi: boolean;
  nativeSharedMemory: boolean;
  nativeGpu: boolean;
  nativeGpuPlatforms: RuntimeNativeGpuPlatform[];
  wasmSab: boolean;
  wasmTransfer: boolean;
  platform: string;
};

export type NativeInvokeResult =
  | ArrayBuffer
  | Uint8Array
  | number[]
  | {
      bytes?: ArrayBuffer | Uint8Array | number[];
      value?: unknown;
    };

export type NativeBinaryInvoke = (
  command: string,
  payload?: Uint8Array | Record<string, unknown>,
  signal?: AbortSignal
) => Promise<NativeInvokeResult>;

export type NativeRuntimeTransportOptions = {
  invoke: NativeBinaryInvoke;
  isAvailable?: () => boolean;
  decodeResponseEnvelope?: boolean;
  unitIdForRoute?: (route: RuntimeRoute, envelope: RuntimeEnvelope<unknown>) => string;
};

export type NativeSecureStore = {
  get: (key: string, signal?: AbortSignal) => Promise<Uint8Array | null>;
  put: (key: string, value: Uint8Array, signal?: AbortSignal) => Promise<void>;
  delete: (key: string, signal?: AbortSignal) => Promise<boolean>;
};

export const createNativeRuntimeTransport = ({
  invoke,
  isAvailable = () => true,
  decodeResponseEnvelope = true,
  unitIdForRoute = (route) => route.eventType,
}: NativeRuntimeTransportOptions): TransportStrategy => ({
  kind: "native",
  async dispatch<TPayload>(
    envelope: RuntimeEnvelope<TPayload>,
    _route: RuntimeRoute,
    signal: AbortSignal
  ): Promise<unknown> {
    throwIfAborted(signal);
    if (!isAvailable()) {
      throw new Error("native runtime transport is unavailable");
    }

    const requestBytes = encodeRuntimeEnvelope(envelope);
    const nativeFrame = encodeNativeDispatchFrame({
      unitId: unitIdForRoute(_route, envelope as RuntimeEnvelope<unknown>),
      schemaVersion: envelope.metadata.schemaVersion,
      encoding: NATIVE_ENCODING_PROTOBUF,
      payload: requestBytes,
      metadata: new TextEncoder().encode(
        `${envelope.metadata.correlationId}:${envelope.metadata.requestId}:${envelope.metadata.idempotencyKey}`
      ),
    });
    const response = await invoke(NATIVE_DISPATCH_COMMAND, nativeFrame, signal);
    throwIfAborted(signal);
    const responseFrame = decodeNativeDispatchResponse(coerceNativeBytes(response));
    if (responseFrame.statusCode !== 0) {
      const diagnostics = responseFrame.diagnostics.byteLength > 0
        ? `: ${new TextDecoder().decode(responseFrame.diagnostics)}`
        : "";
      throw new Error(`native runtime dispatch failed with status ${responseFrame.statusCode}${diagnostics}`);
    }
    return decodeResponseEnvelope ? decodeRuntimeEnvelope(responseFrame.payload) : responseFrame.payload;
  },
  async subscribe(_pattern: string, _callback: (envelope: RuntimeEnvelope<unknown>) => void): Promise<Subscription> {
    throw new Error("native runtime transport does not own subscriptions; use websocket transport");
  },
});

export const readNativeRuntimeCapabilities = async (
  invoke: NativeBinaryInvoke,
  signal?: AbortSignal
): Promise<NativeRuntimeCapabilities> => {
  throwIfAborted(signal);
  const result = await invoke(NATIVE_CAPABILITIES_COMMAND, {}, signal);
  const value = isNativeValueResult(result) ? result.value : result;
  if (!isRecord(value)) {
    throw new Error("native runtime capabilities response must be an object");
  }
  return normalizeCapabilities(value);
};

export const createNativeSecureStore = (invoke: NativeBinaryInvoke): NativeSecureStore => ({
  async get(key: string, signal?: AbortSignal): Promise<Uint8Array | null> {
    const result = await invoke(SECURE_STORE_GET_COMMAND, { key }, signal);
    if (isNativeValueResult(result) && result.value === null) {
      return null;
    }
    const bytes = coerceOptionalNativeBytes(result);
    return bytes.byteLength === 0 ? null : bytes;
  },
  async put(key: string, value: Uint8Array, signal?: AbortSignal): Promise<void> {
    await invoke(SECURE_STORE_PUT_COMMAND, { key, value: Array.from(value) }, signal);
  },
  async delete(key: string, signal?: AbortSignal): Promise<boolean> {
    const result = await invoke(SECURE_STORE_DELETE_COMMAND, { key }, signal);
    const value = isNativeValueResult(result) ? result.value : result;
    return value === true;
  },
});

export type NativeDispatchFrameInput = {
  unitId: string;
  schemaVersion: string;
  encoding: typeof NATIVE_ENCODING_CAPNP | typeof NATIVE_ENCODING_PROTOBUF;
  payload: Uint8Array;
  metadata?: Uint8Array;
};

export type NativeDispatchResponseFrame = {
  statusCode: number;
  payload: Uint8Array;
  diagnostics: Uint8Array;
};

export const encodeNativeDispatchFrame = ({
  unitId,
  schemaVersion,
  encoding,
  payload,
  metadata = new Uint8Array(),
}: NativeDispatchFrameInput): Uint8Array => {
  const encoder = new TextEncoder();
  const unitBytes = encoder.encode(unitId.trim());
  const schemaBytes = encoder.encode(schemaVersion.trim());
  if (unitBytes.byteLength === 0 || unitBytes.byteLength > 0xffff) {
    throw new Error("native unit id length is invalid");
  }
  if (schemaBytes.byteLength === 0 || schemaBytes.byteLength > 0xffff) {
    throw new Error("native schema version length is invalid");
  }
  const frame = new Uint8Array(20 + unitBytes.byteLength + schemaBytes.byteLength + payload.byteLength + metadata.byteLength);
  const view = new DataView(frame.buffer, frame.byteOffset, frame.byteLength);
  writeMagic(frame, 0, NATIVE_REQUEST_MAGIC);
  view.setUint8(4, NATIVE_FRAME_VERSION);
  view.setUint8(5, encoding);
  view.setUint16(6, 0, false);
  view.setUint16(8, unitBytes.byteLength, false);
  view.setUint16(10, schemaBytes.byteLength, false);
  view.setUint32(12, payload.byteLength, false);
  view.setUint32(16, metadata.byteLength, false);
  let cursor = 20;
  frame.set(unitBytes, cursor);
  cursor += unitBytes.byteLength;
  frame.set(schemaBytes, cursor);
  cursor += schemaBytes.byteLength;
  frame.set(payload, cursor);
  cursor += payload.byteLength;
  frame.set(metadata, cursor);
  return frame;
};

export const decodeNativeDispatchResponse = (frame: Uint8Array): NativeDispatchResponseFrame => {
  if (frame.byteLength < 16 || readMagic(frame, 0) !== NATIVE_RESPONSE_MAGIC) {
    throw new Error("native response frame has invalid magic or header");
  }
  const view = new DataView(frame.buffer, frame.byteOffset, frame.byteLength);
  if (view.getUint8(4) !== NATIVE_FRAME_VERSION) {
    throw new Error(`unsupported native response version ${view.getUint8(4)}`);
  }
  const statusCode = view.getUint16(6, false);
  const payloadLength = view.getUint32(8, false);
  const diagnosticsLength = view.getUint32(12, false);
  const expectedLength = 16 + payloadLength + diagnosticsLength;
  if (expectedLength !== frame.byteLength) {
    throw new Error("native response frame length does not match header");
  }
  return {
    statusCode,
    payload: frame.subarray(16, 16 + payloadLength),
    diagnostics: frame.subarray(16 + payloadLength),
  };
};

export const coerceNativeBytes = (result: NativeInvokeResult): Uint8Array => {
  if (result instanceof Uint8Array) {
    return result;
  }
  if (result instanceof ArrayBuffer) {
    return new Uint8Array(result);
  }
  if (Array.isArray(result)) {
    return Uint8Array.from(result);
  }
  if (isRecord(result) && "bytes" in result) {
    const bytes = result.bytes;
    if (bytes instanceof Uint8Array) {
      return bytes;
    }
    if (bytes instanceof ArrayBuffer) {
      return new Uint8Array(bytes);
    }
    if (Array.isArray(bytes)) {
      return Uint8Array.from(bytes);
    }
  }
  throw new Error("native command response did not contain bytes");
};

const coerceOptionalNativeBytes = (result: NativeInvokeResult): Uint8Array => {
  if (isNativeValueResult(result) && result.value === null) {
    return new Uint8Array();
  }
  return coerceNativeBytes(result);
};

const normalizeCapabilities = (value: Record<string, unknown>): NativeRuntimeCapabilities => ({
  native: value.native === true,
  nativeFfi: value.nativeFfi === true || value.native_ffi === true,
  nativeSharedMemory: value.nativeSharedMemory === true || value.native_shared_memory === true,
  nativeGpu: value.nativeGpu === true || value.native_gpu === true,
  nativeGpuPlatforms: normalizeNativeGpuPlatforms(value.nativeGpuPlatforms ?? value.native_gpu_platforms),
  wasmSab: value.wasmSab === true || value.wasm_sab === true,
  wasmTransfer: value.wasmTransfer === true || value.wasm_transfer === true,
  platform: typeof value.platform === "string" ? value.platform : "unknown",
});

const normalizeNativeGpuPlatforms = (value: unknown): RuntimeNativeGpuPlatform[] => {
  if (!Array.isArray(value)) {
    return [];
  }
  const platforms = value.filter((platform): platform is RuntimeNativeGpuPlatform =>
    platform === "linux-dmabuf" ||
    platform === "apple-iosurface" ||
    platform === "android-hardware-buffer" ||
    platform === "cuda-external" ||
    platform === "vulkan-external"
  );
  return [...new Set(platforms)];
};

const throwIfAborted = (signal?: AbortSignal): void => {
  if (signal?.aborted) {
    throw new DOMException("native runtime request aborted", "AbortError");
  }
};

const isRecord = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === "object";

const isNativeValueResult = (value: NativeInvokeResult): value is { value: unknown } =>
  isRecord(value) && "value" in value;

const writeMagic = (target: Uint8Array, offset: number, magic: string): void => {
  for (let index = 0; index < magic.length; index += 1) {
    target[offset + index] = magic.charCodeAt(index);
  }
};

const readMagic = (source: Uint8Array, offset: number): string =>
  String.fromCharCode(...source.subarray(offset, offset + 4));
