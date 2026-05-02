export type LogLevel = 'trace' | 'debug' | 'info' | 'warn' | 'error' | 'fatal';

export interface LogEntry {
  level: LogLevel;
  component: string;
  message: string;
  timestamp: number;
  correlationId: string;
  extra?: Record<string, any>;
}

/**
 * LogRingBuffer implements a high-performance, SharedArrayBuffer-backed
 * ring buffer for log streaming from WASM/Rust to the Browser Host.
 * 
 * Memory Layout:
 * [0..4]   - Write Offset (atomic)
 * [4..8]   - Read Offset (atomic)
 * [8..12]  - Buffer Size
 * [12..16] - Wrap Count
 * [16..64] - Reserved
 * [64..]   - Data Slabs
 */
export class LogRingBuffer {
  private readonly view: DataView;
  private readonly uint32: Uint32Array;
  private readonly buffer: Uint8Array;

  constructor(sab: SharedArrayBuffer) {
    this.view = new DataView(sab);
    this.uint32 = new Uint32Array(sab);
    this.buffer = new Uint8Array(sab);
  }

  static create(sizeBytes: number): LogRingBuffer {
    const sab = new SharedArrayBuffer(sizeBytes + 64);
    const ring = new LogRingBuffer(sab);
    Atomics.store(ring.uint32, 2, sizeBytes); // Buffer Size
    return ring;
  }

  write(entry: LogEntry): void {
    const json = JSON.stringify(entry);
    const encoder = new TextEncoder();
    const bytes = encoder.encode(json);
    this.writeRaw(bytes);
  }

  writeRaw(bytes: Uint8Array): void {
    const length = bytes.byteLength;

    // We store: [Length (4 bytes)] [Bytes]
    const totalNeeded = length + 4;
    const size = Atomics.load(this.uint32, 2);
    
    let writeOffset = Atomics.load(this.uint32, 0);
    
    // Ensure we have enough space (simple wrap-around, might overwrite unread data)
    if (writeOffset + totalNeeded > size) {
      writeOffset = 0; // Wrap to start
      Atomics.add(this.uint32, 3, 1); // Increment Wrap Count
    }

    this.view.setUint32(writeOffset + 64, length, true);
    this.buffer.set(bytes, writeOffset + 68);

    Atomics.store(this.uint32, 0, writeOffset + totalNeeded);
  }

  readAll(): LogEntry[] {
    const entries: LogEntry[] = [];
    let readOffset = Atomics.load(this.uint32, 1);
    const writeOffset = Atomics.load(this.uint32, 0);
    const size = Atomics.load(this.uint32, 2);

    const decoder = new TextDecoder();

    while (readOffset !== writeOffset) {
      if (readOffset + 4 > size) {
        readOffset = 0;
        continue;
      }

      const length = this.view.getUint32(readOffset + 64, true);
      if (length === 0 || readOffset + 4 + length > size) {
        readOffset = 0; // Likely wrapped by writer
        if (readOffset === writeOffset) break;
        continue;
      }

      const bytes = this.buffer.slice(readOffset + 68, readOffset + 68 + length);
      try {
        const json = decoder.decode(bytes);
        entries.push(JSON.parse(json));
      } catch (e) {
        // Corrupt entry, skip
      }

      readOffset += (4 + length);
      if (readOffset > size) readOffset = 0;
    }

    Atomics.store(this.uint32, 1, readOffset);
    return entries;
  }
}
