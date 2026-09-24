import { RuntimeSharedArena, type RuntimeArenaDescriptor } from "./arena";
import { BUFFER_TOTAL_BYTES } from "./generated/runtimeBuffer";

export type RuntimePayloadLane = "control" | "arena" | "stream";

export type RuntimePayloadRouting = {
  lane: RuntimePayloadLane;
  byteLength: number;
  descriptor?: RuntimeArenaDescriptor;
  chunks?: AsyncIterable<Uint8Array>;
};

export type RuntimePayloadRouterOptions = {
  controlMaxBytes?: number;
  arenaMaxBytes?: number;
  arena?: RuntimeSharedArena | null;
  chunkBytes?: number;
};

const DEFAULT_CONTROL_MAX_BYTES = BUFFER_TOTAL_BYTES;
const DEFAULT_ARENA_MAX_BYTES = 1024 * 1024;
const DEFAULT_CHUNK_BYTES = 64 * 1024;

const normalizeChunkBytes = (value: number): number => {
  const size = Math.max(1, value);
  if (!Number.isSafeInteger(size)) {
    throw new Error("runtime chunk size must be a finite safe integer");
  }
  return size;
};

export const routeRuntimePayload = (
  payload: Uint8Array,
  options: RuntimePayloadRouterOptions = {}
): RuntimePayloadRouting => {
  const controlMaxBytes = Math.max(1, options.controlMaxBytes ?? DEFAULT_CONTROL_MAX_BYTES);
  const arenaMaxBytes = Math.max(controlMaxBytes, options.arenaMaxBytes ?? DEFAULT_ARENA_MAX_BYTES);

  if (payload.byteLength < controlMaxBytes) {
    return { lane: "control", byteLength: payload.byteLength };
  }

  if (payload.byteLength <= arenaMaxBytes && options.arena) {
    const descriptor = options.arena.allocate(payload.byteLength);
    options.arena.writeSlab(descriptor.id, payload);
    if (!options.arena.enqueueDescriptorReady(descriptor.id)) {
      throw new Error("runtime arena queue backpressure: descriptor-ready event was not enqueued");
    }
    return { lane: "arena", byteLength: payload.byteLength, descriptor: options.arena.readDescriptor(descriptor.id) };
  }

  return {
    lane: "stream",
    byteLength: payload.byteLength,
    chunks: chunkBytes(payload, options.chunkBytes ?? DEFAULT_CHUNK_BYTES),
  };
};

export async function* routeRuntimeStream(
  chunks: AsyncIterable<Uint8Array<ArrayBufferLike>> | Iterable<Uint8Array<ArrayBufferLike>>,
  options: Pick<RuntimePayloadRouterOptions, "chunkBytes"> = {}
): AsyncIterable<Uint8Array> {
  const chunkBytesTarget = normalizeChunkBytes(options.chunkBytes ?? DEFAULT_CHUNK_BYTES);
  let carry: Uint8Array<ArrayBufferLike> | null = null;
  let carryLength = 0;

  for await (const chunk of chunks) {
    let offset = 0;
    while (offset < chunk.byteLength) {
      if (carryLength === 0 && chunk.byteLength - offset >= chunkBytesTarget) {
        yield chunk.slice(offset, offset + chunkBytesTarget);
        offset += chunkBytesTarget;
        continue;
      }
      carry ??= new Uint8Array(chunkBytesTarget);
      const length = Math.min(chunkBytesTarget - carryLength, chunk.byteLength - offset);
      carry.set(chunk.subarray(offset, offset + length), carryLength);
      carryLength += length;
      offset += length;
      if (carryLength === chunkBytesTarget) {
        yield carry;
        carry = null;
        carryLength = 0;
      }
    }
  }

  if (carryLength > 0) {
    yield carry!.slice(0, carryLength);
  }
}

export const collectRuntimeStream = async (chunks: AsyncIterable<Uint8Array>): Promise<Uint8Array> => {
  const parts: Uint8Array[] = [];
  let total = 0;
  for await (const chunk of chunks) {
    parts.push(chunk);
    total += chunk.byteLength;
  }
  const output = new Uint8Array(total);
  let offset = 0;
  for (const part of parts) {
    output.set(part, offset);
    offset += part.byteLength;
  }
  return output;
};

async function* chunkBytes(payload: Uint8Array, chunkBytesTarget: number): AsyncIterable<Uint8Array> {
  const size = normalizeChunkBytes(chunkBytesTarget);
  for (let offset = 0; offset < payload.byteLength; offset += size) {
    yield payload.slice(offset, Math.min(payload.byteLength, offset + size));
  }
}
