export type LogLevel = 'trace' | 'debug' | 'info' | 'warn' | 'error' | 'fatal';

export interface LogEntry {
  level: LogLevel;
  component: string;
  message: string;
  timestamp: number;
  correlationId: string;
  extra?: Record<string, unknown>;
}

export interface LogRingDiagnostics {
  wrapCount: number;
  droppedWrites: number;
  corruptReads: number;
}

const FIELD_SEPARATOR = "\x1f";
const textEncoder = new TextEncoder();
const textDecoder = new TextDecoder();

const encodeField = (value: unknown): string => encodeURIComponent(String(value ?? ""));
const decodeField = (value: string): string => decodeURIComponent(value);

const encodeExtra = (extra: Record<string, unknown> | undefined): string => {
  if (!extra) {
    return "";
  }
  return Object.entries(extra)
    .map(([key, value]) => `${encodeField(key)}=${encodeField(value)}`)
    .join("&");
};

const decodeExtra = (encoded: string): Record<string, string> | undefined => {
  if (encoded === "") {
    return undefined;
  }
  const extra: Record<string, string> = {};
  for (const pair of encoded.split("&")) {
    const separator = pair.indexOf("=");
    if (separator <= 0) {
      continue;
    }
    extra[decodeField(pair.slice(0, separator))] = decodeField(pair.slice(separator + 1));
  }
  return extra;
};

const encodeEntry = (entry: LogEntry): Uint8Array => {
  const line = [
    encodeField(entry.level),
    encodeField(entry.component),
    encodeField(entry.message),
    encodeField(entry.timestamp),
    encodeField(entry.correlationId),
    encodeExtra(entry.extra),
  ].join(FIELD_SEPARATOR);
  return textEncoder.encode(line);
};

const decodeEntry = (bytes: Uint8Array): LogEntry => {
  const fields = textDecoder.decode(bytes).split(FIELD_SEPARATOR);
  return {
    level: decodeField(fields[0] ?? "info") as LogLevel,
    component: decodeField(fields[1] ?? ""),
    message: decodeField(fields[2] ?? ""),
    timestamp: Number(decodeField(fields[3] ?? "0")),
    correlationId: decodeField(fields[4] ?? ""),
    extra: decodeExtra(fields[5] ?? ""),
  };
};

/**
 * LogRingBuffer implements a high-performance, SharedArrayBuffer-backed
 * ring buffer for log streaming from WASM/Rust to the Browser Host.
 * 
 * Memory Layout:
 * [0..4]   - Write Offset (atomic)
 * [4..8]   - Read Offset (atomic)
 * [8..12]  - Buffer Size
 * [12..16] - Wrap Count
 * [16..20] - Dropped Writes
 * [20..24] - Corrupt Reads
 * [24..64] - Reserved
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
    this.writeRaw(encodeEntry(entry));
  }

  writeRaw(bytes: Uint8Array): void {
    const length = bytes.byteLength;

    // We store: [Length (4 bytes)] [Bytes]
    const totalNeeded = length + 4;
    const size = Atomics.load(this.uint32, 2);

    if (totalNeeded > size) {
      Atomics.add(this.uint32, 4, 1);
      return;
    }
    
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
        entries.push(decodeEntry(bytes));
      } catch (e) {
        Atomics.add(this.uint32, 5, 1);
      }

      readOffset += (4 + length);
      if (readOffset > size) readOffset = 0;
    }

    Atomics.store(this.uint32, 1, readOffset);
    return entries;
  }

  diagnostics(): LogRingDiagnostics {
    return {
      wrapCount: Atomics.load(this.uint32, 3),
      droppedWrites: Atomics.load(this.uint32, 4),
      corruptReads: Atomics.load(this.uint32, 5),
    };
  }
}
