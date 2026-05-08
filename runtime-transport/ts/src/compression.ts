import { decodeRuntimeEnvelope } from "./binaryEnvelope";
import type { RuntimeEnvelope } from "./index";

export type RuntimeCompressionEncoding = "identity" | "gzip" | "br" | "deflate";

export type RuntimeCompressionOptions = {
  enabled?: boolean;
  minBytes?: number;
  preferred?: RuntimeCompressionEncoding[];
};

export type RuntimeCompressedBytes = {
  bytes: Uint8Array;
  encoding: RuntimeCompressionEncoding;
  rawLength: number;
};

const FRAME_MAGIC = 0x4f565254; // OVRT
const FRAME_VERSION = 1;
const FRAME_HEADER_BYTES = 16;
const ENCODING_IDS: Record<RuntimeCompressionEncoding, number> = {
  identity: 0,
  gzip: 1,
  br: 2,
  deflate: 3,
};
const ENCODINGS_BY_ID = new Map<number, RuntimeCompressionEncoding>(
  Object.entries(ENCODING_IDS).map(([encoding, id]) => [id, encoding as RuntimeCompressionEncoding])
);

const DEFAULT_PREFERRED: RuntimeCompressionEncoding[] = ["br", "gzip", "deflate", "identity"];

export const compressRuntimeBytes = async (
  bytes: Uint8Array,
  options: RuntimeCompressionOptions = {}
): Promise<RuntimeCompressedBytes> => {
  const minBytes = Math.max(0, options.minBytes ?? 4096);
  if (!options.enabled || bytes.byteLength < minBytes) {
    return { bytes, encoding: "identity", rawLength: bytes.byteLength };
  }

  for (const encoding of options.preferred ?? DEFAULT_PREFERRED) {
    if (encoding === "identity") {
      return { bytes, encoding: "identity", rawLength: bytes.byteLength };
    }
    const compressed = await tryCompress(bytes, encoding);
    if (compressed && compressed.byteLength < bytes.byteLength) {
      return { bytes: compressed, encoding, rawLength: bytes.byteLength };
    }
  }

  return { bytes, encoding: "identity", rawLength: bytes.byteLength };
};

export const decompressRuntimeBytes = async (
  bytes: Uint8Array,
  encoding: RuntimeCompressionEncoding
): Promise<Uint8Array> => {
  if (encoding === "identity") {
    return bytes;
  }
  const ctor = getDecompressionStream();
  if (!ctor) {
    throw new Error(`runtime transport cannot decompress ${encoding}: DecompressionStream is unavailable`);
  }
  try {
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(bytes);
        controller.close();
      },
    }).pipeThrough(new ctor(encoding));
    return new Uint8Array(await new Response(stream).arrayBuffer());
  } catch (error) {
    const message = error instanceof Error ? error.message : "unknown decompression error";
    throw new Error(`runtime transport failed to decompress ${encoding}: ${message}`);
  }
};

export const encodeRuntimeBinaryFrame = (payload: RuntimeCompressedBytes): Uint8Array => {
  if (payload.encoding === "identity") {
    return payload.bytes;
  }
  const framed = new Uint8Array(FRAME_HEADER_BYTES + payload.bytes.byteLength);
  const view = new DataView(framed.buffer);
  view.setUint32(0, FRAME_MAGIC, false);
  view.setUint8(4, FRAME_VERSION);
  view.setUint8(5, ENCODING_IDS[payload.encoding]);
  view.setUint16(6, 0, false);
  view.setUint32(8, payload.rawLength, false);
  view.setUint32(12, payload.bytes.byteLength, false);
  framed.set(payload.bytes, FRAME_HEADER_BYTES);
  return framed;
};

export const decodeRuntimeBinaryFrame = async (input: Uint8Array): Promise<Uint8Array> => {
  if (!isRuntimeBinaryFrame(input)) {
    return input;
  }
  const view = new DataView(input.buffer, input.byteOffset, input.byteLength);
  const version = view.getUint8(4);
  if (version !== FRAME_VERSION) {
    throw new Error(`unsupported runtime binary frame version ${version}`);
  }
  const encoding = ENCODINGS_BY_ID.get(view.getUint8(5));
  if (!encoding) {
    throw new Error(`unsupported runtime binary frame encoding id ${view.getUint8(5)}`);
  }
  const rawLength = view.getUint32(8, false);
  const payloadLength = view.getUint32(12, false);
  if (FRAME_HEADER_BYTES + payloadLength > input.byteLength) {
    throw new Error("runtime binary frame is truncated");
  }
  const payload = input.subarray(FRAME_HEADER_BYTES, FRAME_HEADER_BYTES + payloadLength);
  const decoded = await decompressRuntimeBytes(payload, encoding);
  if (rawLength > 0 && decoded.byteLength !== rawLength) {
    throw new Error(`runtime binary frame length mismatch: ${decoded.byteLength} != ${rawLength}`);
  }
  return decoded;
};

export const decodeRuntimeBinaryEnvelope = async (input: Uint8Array): Promise<RuntimeEnvelope<unknown>> =>
  decodeRuntimeEnvelope(await decodeRuntimeBinaryFrame(input));

export const supportedRuntimeCompressionEncodings = (): RuntimeCompressionEncoding[] => {
  const encodings: RuntimeCompressionEncoding[] = [];
  const compression = getCompressionStream();
  const decompression = getDecompressionStream();
  if (!compression || !decompression) {
    return ["identity"];
  }
  for (const encoding of DEFAULT_PREFERRED) {
    if (encoding !== "identity" && canConstruct(compression, encoding) && canConstruct(decompression, encoding)) {
      encodings.push(encoding);
    }
  }
  encodings.push("identity");
  return Array.from(new Set(encodings));
};

const tryCompress = async (bytes: Uint8Array, encoding: RuntimeCompressionEncoding): Promise<Uint8Array | null> => {
  const ctor = getCompressionStream();
  if (!ctor) {
    return null;
  }
  try {
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(bytes);
        controller.close();
      },
    }).pipeThrough(new ctor(encoding));
    return new Uint8Array(await new Response(stream).arrayBuffer());
  } catch {
    return null;
  }
};

const isRuntimeBinaryFrame = (bytes: Uint8Array): boolean => {
  if (bytes.byteLength < FRAME_HEADER_BYTES) {
    return false;
  }
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  return view.getUint32(0, false) === FRAME_MAGIC;
};

type CompressionCtor = new (format: RuntimeCompressionEncoding) => TransformStream<Uint8Array, Uint8Array>;

const getCompressionStream = (): CompressionCtor | undefined =>
  typeof CompressionStream === "undefined"
    ? undefined
    : (CompressionStream as unknown as CompressionCtor);

const getDecompressionStream = (): CompressionCtor | undefined =>
  typeof DecompressionStream === "undefined"
    ? undefined
    : (DecompressionStream as unknown as CompressionCtor);

const canConstruct = (ctor: CompressionCtor, encoding: RuntimeCompressionEncoding): boolean => {
  try {
    new ctor(encoding);
    return true;
  } catch {
    return false;
  }
};
