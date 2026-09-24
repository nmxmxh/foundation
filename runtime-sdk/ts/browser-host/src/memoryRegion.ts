export type RuntimeBufferStorage = ArrayBuffer | SharedArrayBuffer;

/** A fixed region whose views follow WebAssembly memory growth. */
export class RuntimeMemoryRegion {
  private cachedBuffer: RuntimeBufferStorage | null = null;
  private cachedBytes: Uint8Array | null = null;
  private cachedInts: Int32Array | null = null;
  private cachedView: DataView | null = null;
  private released = false;

  constructor(
    readonly source: WebAssembly.Memory | RuntimeBufferStorage,
    readonly byteOffset: number,
    readonly byteLength: number,
    readonly handle = 0,
    private readonly onRelease?: () => void,
  ) {
    if (!Number.isSafeInteger(byteOffset) || byteOffset < 0 || byteOffset % 8 !== 0 ||
        !Number.isSafeInteger(byteLength) || byteLength <= 0 || byteLength % 4 !== 0) {
      throw new Error("runtime region requires aligned integer bounds");
    }
    this.refresh();
  }

  get buffer(): RuntimeBufferStorage { this.refresh(); return this.cachedBuffer!; }
  get bytes(): Uint8Array { this.refresh(); return this.cachedBytes!; }
  get ints(): Int32Array { this.refresh(); return this.cachedInts!; }
  get view(): DataView { this.refresh(); return this.cachedView!; }
  get shared(): boolean {
    return typeof SharedArrayBuffer !== "undefined" && this.buffer instanceof SharedArrayBuffer;
  }

  subarray(offset: number, length: number): Uint8Array {
    this.assertRange(offset, length);
    return this.bytes.subarray(offset, offset + length);
  }

  assertRange(offset: number, length: number): void {
    if (!Number.isSafeInteger(offset) || !Number.isSafeInteger(length) || offset < 0 || length < 0 ||
        offset > this.byteLength || length > this.byteLength - offset) {
      throw new Error("runtime region access exceeds bounds");
    }
    this.refresh();
  }

  /** Call only after workers stop and all borrowed views are released. */
  release(): void {
    if (this.released) return;
    this.onRelease?.();
    this.released = true;
    this.cachedBuffer = null;
    this.cachedBytes = null;
    this.cachedInts = null;
    this.cachedView = null;
  }

  private refresh(): void {
    if (this.released) throw new Error("runtime region is released");
    const buffer = this.source instanceof WebAssembly.Memory ? this.source.buffer : this.source;
    if (buffer === this.cachedBuffer) return;
    if (this.byteOffset + this.byteLength > buffer.byteLength) throw new Error("runtime region exceeds memory capacity");
    this.cachedBuffer = buffer;
    this.cachedBytes = new Uint8Array(buffer, this.byteOffset, this.byteLength);
    this.cachedInts = new Int32Array(buffer, this.byteOffset, this.byteLength / 4);
    this.cachedView = new DataView(buffer, this.byteOffset, this.byteLength);
  }
}

export type RuntimeBuffer = RuntimeMemoryRegion | RuntimeBufferStorage;
