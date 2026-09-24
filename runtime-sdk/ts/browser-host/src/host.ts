import {
  BUFFER_TOTAL_BYTES,
  BUFFER_SCHEMA_VERSION,
  DIAGNOSTIC_MAX_BYTES,
  EPOCH_SLOT_COUNT,
  INPUT_MAX_BYTES,
  INT_IDX_MODULE_VERSION,
  INT_IDX_INPUT_LENGTH,
  INT_IDX_OUTPUT_LENGTH,
  INT_IDX_SCHEMA_VERSION,
  OFFSET_DIAGNOSTIC_BYTES,
  OUTPUT_MAX_BYTES,
  IDX_INPUT_WRITTEN,
  IDX_OUTPUT_CONSUMED,
  OFFSET_HEADER_INTS,
  OFFSET_INPUT_BYTES,
  OFFSET_OUTPUT_BYTES,
} from "./generated/runtimeBuffer";
import { arenaBytesForProfile, clampRuntimeArenaBytes, negotiateRuntimeMemory, RuntimeSharedArena, type RuntimeMemoryOptions, type RuntimeMemorySelection } from "./arena";
import { LogRingBuffer } from "./pulse/logRing";
import { RuntimeMemoryRegion, type RuntimeBuffer, type RuntimeBufferStorage } from "./memoryRegion";

type RuntimeLogLevel = 0 | 1 | 2 | 3 | 4;

type RuntimeHandle = number;

type RuntimeInstance = WebAssembly.Instance & {
  exports: WebAssembly.Exports & {
    memory?: WebAssembly.Memory;
    ovrt_buffer_abi_version?: () => number;
    ovrt_buffer_alloc?: (length: number) => number;
    ovrt_buffer_ptr?: (handle: number) => number;
    ovrt_buffer_free?: (handle: number) => number;
  };
};

const encoder = new TextEncoder();

const zeroRegion = (view: Uint8Array) => {
  view.fill(0);
};

export class BrowserRuntimeHost {
  private readonly buffers = new Map<RuntimeHandle, RuntimeMemoryRegion>();
  private readonly regions = new WeakMap<RuntimeBufferStorage, RuntimeMemoryRegion>();
  private nextHandle = 1;
  private instance: RuntimeInstance | null = null;
  private memory: WebAssembly.Memory | null = null;
  private logRing: LogRingBuffer | null = null;

  createRuntimeBuffer(): RuntimeMemoryRegion {
    return this.createBuffer(BUFFER_TOTAL_BYTES);
  }

  createBuffer(byteLength: number): RuntimeMemoryRegion {
    if (!Number.isSafeInteger(byteLength) || byteLength < BUFFER_TOTAL_BYTES || byteLength > 64 * 1024 * 1024 || byteLength % 8 !== 0) {
      throw new Error("runtime buffer size must be aligned and between 4 KiB and 64 MiB");
    }
    const exports = this.instance?.exports;
    if (exports?.ovrt_buffer_abi_version?.() !== 2) {
      const buffer = typeof SharedArrayBuffer === "undefined" ? new ArrayBuffer(byteLength) : new SharedArrayBuffer(byteLength);
      return this.region(buffer);
    }
    if (!this.memory || !exports.ovrt_buffer_alloc || !exports.ovrt_buffer_ptr || !exports.ovrt_buffer_free) {
      throw new Error("runtime linear buffer ABI is incomplete");
    }
    const handle = exports.ovrt_buffer_alloc(byteLength) >>> 0;
    if (handle < 0x80000000) throw new Error("runtime linear buffer allocation failed");
    if (this.buffers.has(handle)) throw new Error("runtime linear buffer handle is already allocated");
    const pointer = exports.ovrt_buffer_ptr(handle) >>> 0;
    try {
      if (pointer === 0) throw new Error("runtime linear buffer pointer is invalid");
      const region = new RuntimeMemoryRegion(this.memory, pointer, byteLength, handle, () => {
        if (exports.ovrt_buffer_free!(handle) !== 1) throw new Error("runtime linear buffer release failed");
        this.buffers.delete(handle);
      });
      this.buffers.set(handle, region);
      return region;
    } catch (error) {
      exports.ovrt_buffer_free(handle);
      throw error;
    }
  }

  createSharedArena(options: Pick<RuntimeMemoryOptions, "arenaBytes" | "arenaProfile"> = {}): RuntimeSharedArena {
    const memory = this.memory;
    if (memory && typeof SharedArrayBuffer !== "undefined" && memory.buffer instanceof SharedArrayBuffer &&
        this.instance?.exports.ovrt_buffer_abi_version?.() === 2) {
      return new RuntimeSharedArena(this.createBuffer(clampRuntimeArenaBytes(options.arenaBytes ?? arenaBytesForProfile(options.arenaProfile))));
    }
    if (typeof SharedArrayBuffer === "undefined") {
      throw new Error("SharedArrayBuffer is unavailable; RuntimeSharedArena cannot be created");
    }
    return RuntimeSharedArena.create(options);
  }

  negotiateMemory(options: RuntimeMemoryOptions = {}): RuntimeMemorySelection {
    return negotiateRuntimeMemory(options);
  }

  registerBuffer(buffer: RuntimeBuffer): RuntimeHandle {
    const region = this.region(buffer);
    region.assertRange(0, 0);
    if (region.handle >= 0x80000000) {
      if (this.buffers.get(region.handle) !== region) throw new Error("runtime region belongs to another host");
      return region.handle;
    }
    if (this.nextHandle >= 0x80000000) throw new Error("runtime buffer handles exhausted");
    const handle = this.nextHandle++;
    this.buffers.set(handle, region);
    return handle;
  }

  unregisterBuffer(handle: RuntimeHandle): void {
    if (handle >= 0x80000000) this.buffers.get(handle)?.release();
    this.buffers.delete(handle);
  }

  attachInstance(instance: RuntimeInstance, memory = instance.exports.memory): void {
    if ((this.instance !== instance || this.memory !== (memory ?? null)) &&
        [...this.buffers.keys()].some((handle) => handle >= 0x80000000)) {
      throw new Error("release linear buffers before replacing the runtime instance");
    }
    this.instance = instance;
    this.memory = memory ?? null;
  }

  fork(): BrowserRuntimeHost {
    const host = new BrowserRuntimeHost();
    host.logRing = this.logRing;
    return host;
  }

  /** Stop workers and release borrowed views before disposing this host. */
  dispose(): void {
    for (const region of this.buffers.values()) region.release();
    this.buffers.clear();
    this.instance = null;
    this.memory = null;
  }

  getEpochView(buffer: RuntimeBuffer): Int32Array {
    return this.region(buffer).ints.subarray(0, EPOCH_SLOT_COUNT);
  }

  getHeaderView(buffer: RuntimeBuffer): Int32Array {
    return this.region(buffer).ints.subarray(OFFSET_HEADER_INTS / 4, OFFSET_HEADER_INTS / 4 + 8);
  }

  getImportObject(extraImports: WebAssembly.Imports = {}): WebAssembly.Imports {
    const env = {
      ovrt_get_byte_length: (handle: RuntimeHandle) => this.getBuffer(handle).byteLength,
      ovrt_copy_to_buffer: (handle: RuntimeHandle, targetOffset: number, srcPtr: number, len: number) => {
        const bytes = this.getMemoryView(srcPtr, len);
        this.getBuffer(handle).subarray(targetOffset, len).set(bytes);
      },
      ovrt_copy_from_buffer: (handle: RuntimeHandle, srcOffset: number, destPtr: number, len: number) => {
        const source = this.getBuffer(handle).subarray(srcOffset, len);
        this.getMemoryView(destPtr, len).set(source);
      },
      ovrt_atomic_load: (handle: RuntimeHandle, index: number) => Atomics.load(this.getBuffer(handle).ints, index),
      ovrt_atomic_store: (handle: RuntimeHandle, index: number, value: number) => Atomics.store(this.getBuffer(handle).ints, index, value),
      ovrt_atomic_add: (handle: RuntimeHandle, index: number, delta: number) => Atomics.add(this.getBuffer(handle).ints, index, delta),
      ovrt_atomic_compare_exchange: (handle: RuntimeHandle, index: number, expected: number, replacement: number) =>
        Atomics.compareExchange(this.getBuffer(handle).ints, index, expected, replacement),
      ovrt_atomic_notify: (handle: RuntimeHandle, index: number, count: number) => {
        const region = this.getBuffer(handle);
        return region.shared && typeof Atomics.notify === "function" ? Atomics.notify(region.ints, index, count) : 0;
      },
      ovrt_log: (ptr: number, len: number, level: RuntimeLogLevel) => {
        const message = new TextDecoder().decode(this.getMemoryView(ptr, len));
        this.log(message, level);
      },
      ovrt_log_ring: (ptr: number, len: number) => {
        if (this.logRing) {
          const bytes = this.getMemoryView(ptr, len);
          this.logRing.writeRaw(bytes);
        }
      },
      ovrt_get_now: () => Date.now(),
      ovrt_fill_random: (ptr: number, len: number) => {
        const slice = this.getMemoryView(ptr, len);
        crypto.getRandomValues(slice);
      },
    };

    return {
      ...extraImports,
      env: {
        ...(extraImports.env ?? {}),
        ...env,
      },
    };
  }

  async instantiate(source: string | URL, extraImports: WebAssembly.Imports = {}): Promise<RuntimeInstance> {
    const imports = this.getImportObject(extraImports);
    const response = await fetch(source);
    let result: WebAssembly.WebAssemblyInstantiatedSource;
    if ("instantiateStreaming" in WebAssembly) {
      result = await WebAssembly.instantiateStreaming(response, imports);
    } else {
      const bytes = await response.arrayBuffer();
      result = await WebAssembly.instantiate(bytes, imports);
    }
    const instance = result.instance as RuntimeInstance;
    this.attachInstance(instance, instance.exports.memory ?? extraImports.env?.memory as WebAssembly.Memory | undefined);

    return instance;
  }

  clearOutput(buffer: RuntimeBuffer): void {
    const header = this.getHeaderView(buffer);
    header[INT_IDX_OUTPUT_LENGTH] = 0;
  }

  clearInput(buffer: RuntimeBuffer): void {
    const header = this.getHeaderView(buffer);
    header[INT_IDX_INPUT_LENGTH] = 0;
  }

  setHeaderInt(buffer: RuntimeBuffer, index: number, value: number): void {
    this.getHeaderView(buffer)[index] = value;
  }

  getHeaderInt(buffer: RuntimeBuffer, index: number): number {
    return this.getHeaderView(buffer)[index] ?? 0;
  }

  setInputBytes(buffer: RuntimeBuffer, bytes: Uint8Array, moduleVersion = 1): void {
    if (bytes.byteLength > INPUT_MAX_BYTES) {
      throw new Error(`input payload exceeds runtime capacity: ${bytes.byteLength} > ${INPUT_MAX_BYTES}`);
    }
    const view = this.region(buffer).subarray(OFFSET_INPUT_BYTES, INPUT_MAX_BYTES);
    zeroRegion(view);
    view.set(bytes);
    const header = this.getHeaderView(buffer);
    header[INT_IDX_SCHEMA_VERSION] = BUFFER_SCHEMA_VERSION;
    header[INT_IDX_MODULE_VERSION] = moduleVersion;
    header[INT_IDX_INPUT_LENGTH] = bytes.byteLength;
    Atomics.add(this.getEpochView(buffer), IDX_INPUT_WRITTEN, 1);
  }

  readOutputBytes(buffer: RuntimeBuffer): Uint8Array {
    const length = this.getHeaderInt(buffer, INT_IDX_OUTPUT_LENGTH);
    if (length < 0 || length > OUTPUT_MAX_BYTES) {
      throw new Error(`invalid output length ${length}`);
    }
    return this.region(buffer).subarray(OFFSET_OUTPUT_BYTES, length).slice();
  }

  markOutputConsumed(buffer: RuntimeBuffer): number {
    return Atomics.add(this.getEpochView(buffer), IDX_OUTPUT_CONSUMED, 1) + 1;
  }

  writeDiagnostics(buffer: RuntimeBuffer, message: string): void {
    const bytes = encoder.encode(message);
    const view = this.region(buffer).subarray(OFFSET_DIAGNOSTIC_BYTES, DIAGNOSTIC_MAX_BYTES);
    zeroRegion(view);
    view.set(bytes.slice(0, view.byteLength));
  }

  readDiagnostics(buffer: RuntimeBuffer): string {
    const view = this.region(buffer).subarray(OFFSET_DIAGNOSTIC_BYTES, DIAGNOSTIC_MAX_BYTES);
    const end = view.findIndex((value) => value === 0);
    const slice = end >= 0 ? view.slice(0, end) : view;
    return new TextDecoder().decode(slice);
  }

  private getBuffer(handle: RuntimeHandle): RuntimeMemoryRegion {
    const buffer = this.buffers.get(handle >>> 0);
    if (!buffer) {
      throw new Error(`unknown runtime buffer handle ${handle}`);
    }
    return buffer;
  }

  private region(buffer: RuntimeBuffer): RuntimeMemoryRegion {
    if (buffer instanceof RuntimeMemoryRegion) return buffer;
    let region = this.regions.get(buffer);
    if (!region) {
      region = new RuntimeMemoryRegion(buffer, 0, buffer.byteLength);
      this.regions.set(buffer, region);
    }
    return region;
  }

  private getMemoryView(ptr: number, len: number): Uint8Array {
    const memory = this.memory;
    if (!memory) {
      throw new Error("runtime memory is not attached");
    }
    return new Uint8Array(memory.buffer, ptr, len);
  }

  private log(message: string, level: RuntimeLogLevel): void {
    if (level === 0) {
      console.error(message);
      return;
    }
    if (level === 1) {
      console.warn(message);
      return;
    }
    if (level === 2) {
      console.info(message);
      return;
    }
    console.debug(message);
  }

  setLogRing(ring: LogRingBuffer): void {
    this.logRing = ring;
  }
}
