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

type RuntimeLogLevel = 0 | 1 | 2 | 3 | 4;

type RuntimeHandle = number;

type RuntimeInstance = WebAssembly.Instance & {
  exports: WebAssembly.Exports & {
    memory?: WebAssembly.Memory;
  };
};

const encoder = new TextEncoder();

const zeroRegion = (view: Uint8Array) => {
  view.fill(0);
};

export class BrowserRuntimeHost {
  private readonly buffers = new Map<RuntimeHandle, SharedArrayBuffer>();
  private nextHandle = 1;
  private instance: RuntimeInstance | null = null;

  createRuntimeBuffer(): SharedArrayBuffer {
    return new SharedArrayBuffer(BUFFER_TOTAL_BYTES);
  }

  registerBuffer(buffer: SharedArrayBuffer): RuntimeHandle {
    const handle = this.nextHandle++;
    this.buffers.set(handle, buffer);
    return handle;
  }

  unregisterBuffer(handle: RuntimeHandle): void {
    this.buffers.delete(handle);
  }

  attachInstance(instance: RuntimeInstance): void {
    this.instance = instance;
  }

  getEpochView(buffer: SharedArrayBuffer): Int32Array {
    return new Int32Array(buffer, 0, EPOCH_SLOT_COUNT);
  }

  getHeaderView(buffer: SharedArrayBuffer): Int32Array {
    return new Int32Array(buffer, OFFSET_HEADER_INTS, 8);
  }

  getImportObject(extraImports: WebAssembly.Imports = {}): WebAssembly.Imports {
    const env = {
      ovrt_get_byte_length: (handle: RuntimeHandle) => this.getBuffer(handle).byteLength,
      ovrt_copy_to_buffer: (handle: RuntimeHandle, targetOffset: number, srcPtr: number, len: number) => {
        const bytes = this.getMemoryView(srcPtr, len);
        new Uint8Array(this.getBuffer(handle), targetOffset, len).set(bytes);
      },
      ovrt_copy_from_buffer: (handle: RuntimeHandle, srcOffset: number, destPtr: number, len: number) => {
        const source = new Uint8Array(this.getBuffer(handle), srcOffset, len);
        this.getMemoryView(destPtr, len).set(source);
      },
      ovrt_atomic_load: (handle: RuntimeHandle, index: number) => Atomics.load(new Int32Array(this.getBuffer(handle)), index),
      ovrt_atomic_store: (handle: RuntimeHandle, index: number, value: number) => Atomics.store(new Int32Array(this.getBuffer(handle)), index, value),
      ovrt_atomic_add: (handle: RuntimeHandle, index: number, delta: number) => Atomics.add(new Int32Array(this.getBuffer(handle)), index, delta),
      ovrt_atomic_compare_exchange: (handle: RuntimeHandle, index: number, expected: number, replacement: number) =>
        Atomics.compareExchange(new Int32Array(this.getBuffer(handle)), index, expected, replacement),
      ovrt_atomic_notify: (handle: RuntimeHandle, index: number, count: number) => {
        const typed = new Int32Array(this.getBuffer(handle));
        return typeof Atomics.notify === "function" ? Atomics.notify(typed, index, count) : 0;
      },
      ovrt_log: (ptr: number, len: number, level: RuntimeLogLevel) => {
        const message = new TextDecoder().decode(this.getMemoryView(ptr, len));
        this.log(message, level);
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
    this.attachInstance(instance);
    return instance;
  }

  clearOutput(buffer: SharedArrayBuffer): void {
    const header = this.getHeaderView(buffer);
    header[INT_IDX_OUTPUT_LENGTH] = 0;
  }

  clearInput(buffer: SharedArrayBuffer): void {
    const header = this.getHeaderView(buffer);
    header[INT_IDX_INPUT_LENGTH] = 0;
  }

  setHeaderInt(buffer: SharedArrayBuffer, index: number, value: number): void {
    this.getHeaderView(buffer)[index] = value;
  }

  getHeaderInt(buffer: SharedArrayBuffer, index: number): number {
    return this.getHeaderView(buffer)[index] ?? 0;
  }

  setInputBytes(buffer: SharedArrayBuffer, bytes: Uint8Array, moduleVersion = 1): void {
    if (bytes.byteLength > INPUT_MAX_BYTES) {
      throw new Error(`input payload exceeds runtime capacity: ${bytes.byteLength} > ${INPUT_MAX_BYTES}`);
    }
    const view = new Uint8Array(buffer, OFFSET_INPUT_BYTES, INPUT_MAX_BYTES);
    zeroRegion(view);
    view.set(bytes);
    const header = this.getHeaderView(buffer);
    header[INT_IDX_SCHEMA_VERSION] = BUFFER_SCHEMA_VERSION;
    header[INT_IDX_MODULE_VERSION] = moduleVersion;
    header[INT_IDX_INPUT_LENGTH] = bytes.byteLength;
    Atomics.add(this.getEpochView(buffer), IDX_INPUT_WRITTEN, 1);
  }

  readOutputBytes(buffer: SharedArrayBuffer): Uint8Array {
    const length = this.getHeaderInt(buffer, INT_IDX_OUTPUT_LENGTH);
    if (length < 0 || length > OUTPUT_MAX_BYTES) {
      throw new Error(`invalid output length ${length}`);
    }
    return new Uint8Array(buffer, OFFSET_OUTPUT_BYTES, length).slice();
  }

  markOutputConsumed(buffer: SharedArrayBuffer): number {
    return Atomics.add(this.getEpochView(buffer), IDX_OUTPUT_CONSUMED, 1) + 1;
  }

  writeDiagnostics(buffer: SharedArrayBuffer, message: string): void {
    const bytes = encoder.encode(message);
    const view = new Uint8Array(buffer, OFFSET_DIAGNOSTIC_BYTES, DIAGNOSTIC_MAX_BYTES);
    zeroRegion(view);
    view.set(bytes.slice(0, view.byteLength));
  }

  readDiagnostics(buffer: SharedArrayBuffer): string {
    const view = new Uint8Array(buffer, OFFSET_DIAGNOSTIC_BYTES, DIAGNOSTIC_MAX_BYTES);
    const end = view.findIndex((value) => value === 0);
    const slice = end >= 0 ? view.slice(0, end) : view;
    return new TextDecoder().decode(slice);
  }

  private getBuffer(handle: RuntimeHandle): SharedArrayBuffer {
    const buffer = this.buffers.get(handle);
    if (!buffer) {
      throw new Error(`unknown runtime buffer handle ${handle}`);
    }
    return buffer;
  }

  private getMemoryView(ptr: number, len: number): Uint8Array {
    const memory = this.instance?.exports.memory;
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
}
