export type RuntimeHostBuffer = SharedArrayBuffer | ArrayBuffer;

export type RuntimeEpochListener = (epoch: number, previousEpoch: number) => void;

export type RuntimeBridgeInit = {
  buffer: RuntimeHostBuffer;
  memory?: WebAssembly.Memory | null;
  offset?: number;
  size?: number;
  flagOffset?: number;
  flagCount?: number;
};

export type RuntimeEpochSubscriptionOptions = {
  emitInitial?: boolean;
  pollMs?: number;
  waitTimeoutMs?: number;
};

type RuntimeEpochSubscription = {
  listener: RuntimeEpochListener;
  emitInitial: boolean;
};

type RuntimeEpochWatcher = {
  index: number;
  subscriptions: Set<RuntimeEpochSubscription>;
  lastEpoch: number | null;
  pollMs: number;
  waitTimeoutMs: number;
  active: boolean;
  running: boolean;
  generation: number;
};

const DEFAULT_FLAG_COUNT = 64;
const DEFAULT_POLL_MS = 16;
const DEFAULT_WAIT_TIMEOUT_MS = 250;

const hasSharedArrayBuffer = (): boolean => typeof SharedArrayBuffer !== "undefined";

const isSharedBuffer = (buffer: RuntimeHostBuffer): buffer is SharedArrayBuffer =>
  hasSharedArrayBuffer() && buffer instanceof SharedArrayBuffer;

const hasWaitAsync = (): boolean =>
  typeof Atomics !== "undefined" && typeof (Atomics as unknown as { waitAsync?: unknown }).waitAsync === "function";

const sleep = (ms: number): Promise<void> => new Promise((resolve) => setTimeout(resolve, ms));

const assertRegion = (offset: number, size: number, capacity: number): void => {
  if (!Number.isInteger(offset) || offset < 0) {
    throw new Error(`runtime region offset must be a non-negative integer: ${offset}`);
  }
  if (!Number.isInteger(size) || size < 0) {
    throw new Error(`runtime region size must be a non-negative integer: ${size}`);
  }
  if (offset + size > capacity) {
    throw new Error(`runtime region exceeds buffer capacity: ${offset} + ${size} > ${capacity}`);
  }
};

export class RuntimeBridge {
  private buffer: RuntimeHostBuffer | null = null;
  private memory: WebAssembly.Memory | null = null;
  private offset = 0;
  private size = 0;
  private flagOffset = 0;
  private flagCount = DEFAULT_FLAG_COUNT;
  private dataView: DataView | null = null;
  private flagsView: Int32Array | null = null;
  private readonly dataViews = new Map<string, DataView>();
  private readonly uint8Views = new Map<string, Uint8Array>();
  private readonly epochWatchers = new Map<number, RuntimeEpochWatcher>();

  initialize({
    buffer,
    memory = null,
    offset = 0,
    size = buffer.byteLength - offset,
    flagOffset = 0,
    flagCount = DEFAULT_FLAG_COUNT,
  }: RuntimeBridgeInit): void {
    assertRegion(offset, size, buffer.byteLength);
    assertRegion(flagOffset, flagCount * 4, buffer.byteLength);

    this.buffer = buffer;
    this.memory = memory;
    this.offset = offset;
    this.size = size;
    this.flagOffset = flagOffset;
    this.flagCount = flagCount;
    this.dataView = new DataView(buffer);
    this.flagsView = new Int32Array(buffer, flagOffset, flagCount);
    this.dataViews.clear();
    this.uint8Views.clear();
    this.restartEpochWatchers();
  }

  clear(): void {
    this.buffer = null;
    this.memory = null;
    this.offset = 0;
    this.size = 0;
    this.flagOffset = 0;
    this.flagCount = DEFAULT_FLAG_COUNT;
    this.dataView = null;
    this.flagsView = null;
    this.dataViews.clear();
    this.uint8Views.clear();
    for (const watcher of this.epochWatchers.values()) {
      this.stopEpochWatcher(watcher, true);
    }
  }

  isReady(): boolean {
    return this.buffer !== null && this.dataView !== null && this.flagsView !== null;
  }

  isShared(): boolean {
    return this.buffer !== null && isSharedBuffer(this.buffer);
  }

  getBuffer(): RuntimeHostBuffer | null {
    return this.buffer;
  }

  getMemory(): WebAssembly.Memory | null {
    return this.memory;
  }

  getOffset(): number {
    return this.offset;
  }

  getSize(): number {
    return this.size;
  }

  getDataView(): DataView | null {
    return this.dataView;
  }

  getFlagsView(): Int32Array | null {
    return this.flagsView;
  }

  getRegionDataView(regionOffset: number, regionSize: number): DataView | null {
    if (!this.buffer) return null;
    assertRegion(this.offset + regionOffset, regionSize, this.buffer.byteLength);
    const key = `${regionOffset}:${regionSize}`;
    let view = this.dataViews.get(key);
    if (!view) {
      view = new DataView(this.buffer, this.offset + regionOffset, regionSize);
      this.dataViews.set(key, view);
    }
    return view;
  }

  getRegionUint8View(regionOffset: number, regionSize: number): Uint8Array | null {
    if (!this.buffer) return null;
    assertRegion(this.offset + regionOffset, regionSize, this.buffer.byteLength);
    const key = `${regionOffset}:${regionSize}`;
    let view = this.uint8Views.get(key);
    if (!view) {
      view = new Uint8Array(this.buffer, this.offset + regionOffset, regionSize);
      this.uint8Views.set(key, view);
    }
    return view;
  }

  readI32(byteOffset: number): number {
    if (!this.dataView) return 0;
    return this.dataView.getInt32(this.offset + byteOffset, true);
  }

  readU32(byteOffset: number): number {
    if (!this.dataView) return 0;
    return this.dataView.getUint32(this.offset + byteOffset, true);
  }

  readF32(byteOffset: number): number {
    if (!this.dataView) return 0;
    return this.dataView.getFloat32(this.offset + byteOffset, true);
  }

  atomicLoad(index: number): number {
    if (!this.flagsView || index < 0 || index >= this.flagCount) return 0;
    if (this.isShared()) {
      return Atomics.load(this.flagsView, index);
    }
    return this.flagsView[index] ?? 0;
  }

  atomicStore(index: number, value: number): number {
    if (!this.flagsView || index < 0 || index >= this.flagCount) return 0;
    if (this.isShared()) {
      return Atomics.store(this.flagsView, index, value);
    }
    const previous = this.flagsView[index] ?? 0;
    this.flagsView[index] = value;
    return previous;
  }

  atomicAdd(index: number, delta: number): number {
    if (!this.flagsView || index < 0 || index >= this.flagCount) return 0;
    if (this.isShared()) {
      return Atomics.add(this.flagsView, index, delta);
    }
    const previous = this.flagsView[index] ?? 0;
    this.flagsView[index] = previous + delta;
    return previous;
  }

  signalEpoch(index: number, delta = 1): number {
    const next = this.atomicAdd(index, delta) + delta;
    if (this.flagsView && this.isShared() && typeof Atomics.notify === "function") {
      Atomics.notify(this.flagsView, index);
    }
    return next;
  }

  async waitForEpochChange(index: number, expectedValue: number, timeoutMs = 5000, pollMs = DEFAULT_POLL_MS): Promise<number> {
    if (!this.flagsView) return expectedValue;
    let current = this.atomicLoad(index);
    if (current !== expectedValue) return current;
    const startedAt = performance.now();

    while (performance.now() - startedAt < timeoutMs) {
      const remainingMs = Math.max(1, Math.ceil(timeoutMs - (performance.now() - startedAt)));
      if (this.isShared() && hasWaitAsync()) {
        const waitAsync = (Atomics as unknown as {
          waitAsync: (typed: Int32Array, index: number, value: number, timeout?: number) =>
            | { async: true; value: Promise<unknown> }
            | { async: false; value: unknown };
        }).waitAsync;
        const result = waitAsync(this.flagsView, index, current, remainingMs);
        if (result.async) {
          await result.value;
        }
      } else {
        await sleep(Math.min(Math.max(4, pollMs), remainingMs));
      }
      current = this.atomicLoad(index);
      if (current !== expectedValue) return current;
    }

    return this.atomicLoad(index);
  }

  subscribeEpoch(
    index: number,
    listener: RuntimeEpochListener,
    options: RuntimeEpochSubscriptionOptions = {}
  ): () => void {
    const watcher = this.getOrCreateEpochWatcher(index, options);
    const subscription: RuntimeEpochSubscription = {
      listener,
      emitInitial: options.emitInitial === true,
    };
    watcher.subscriptions.add(subscription);

    if (watcher.lastEpoch !== null && subscription.emitInitial) {
      this.notifyEpochListener(listener, watcher.lastEpoch, watcher.lastEpoch);
    }
    if (this.isReady()) {
      this.startEpochWatcher(watcher);
    }

    return () => {
      watcher.subscriptions.delete(subscription);
      if (watcher.subscriptions.size > 0) return;
      this.stopEpochWatcher(watcher, true);
      this.epochWatchers.delete(index);
    };
  }

  private getOrCreateEpochWatcher(index: number, options: RuntimeEpochSubscriptionOptions): RuntimeEpochWatcher {
    let watcher = this.epochWatchers.get(index);
    if (watcher) {
      watcher.pollMs = Math.min(watcher.pollMs, Math.max(4, options.pollMs ?? DEFAULT_POLL_MS));
      watcher.waitTimeoutMs = Math.min(
        watcher.waitTimeoutMs,
        Math.max(1, options.waitTimeoutMs ?? DEFAULT_WAIT_TIMEOUT_MS)
      );
      return watcher;
    }

    watcher = {
      index,
      subscriptions: new Set(),
      lastEpoch: null,
      pollMs: Math.max(4, options.pollMs ?? DEFAULT_POLL_MS),
      waitTimeoutMs: Math.max(1, options.waitTimeoutMs ?? DEFAULT_WAIT_TIMEOUT_MS),
      active: false,
      running: false,
      generation: 0,
    };
    this.epochWatchers.set(index, watcher);
    return watcher;
  }

  private startEpochWatcher(watcher: RuntimeEpochWatcher): void {
    if (!this.flagsView || watcher.running || watcher.subscriptions.size === 0) return;
    watcher.active = true;
    watcher.running = true;
    watcher.generation += 1;
    const generation = watcher.generation;
    void this.runEpochWatcher(watcher, generation);
  }

  private stopEpochWatcher(watcher: RuntimeEpochWatcher, clearEpoch = false): void {
    watcher.active = false;
    watcher.generation += 1;
    if (clearEpoch) {
      watcher.lastEpoch = null;
    }
  }

  private restartEpochWatchers(): void {
    for (const watcher of this.epochWatchers.values()) {
      watcher.lastEpoch = null;
      watcher.active = false;
      watcher.generation += 1;
      if (watcher.subscriptions.size > 0) {
        this.startEpochWatcher(watcher);
      }
    }
  }

  private async runEpochWatcher(watcher: RuntimeEpochWatcher, generation: number): Promise<void> {
    try {
      if (!this.flagsView) return;
      if (watcher.lastEpoch === null) {
        watcher.lastEpoch = this.atomicLoad(watcher.index);
        this.notifyEpochInitial(watcher, watcher.lastEpoch);
      }

      while (
        watcher.active &&
        watcher.generation === generation &&
        watcher.subscriptions.size > 0 &&
        this.flagsView
      ) {
        const previous: number = watcher.lastEpoch ?? this.atomicLoad(watcher.index);
        const epoch = await this.waitForEpochChange(
          watcher.index,
          previous,
          watcher.waitTimeoutMs,
          watcher.pollMs
        );
        if (!watcher.active || watcher.generation !== generation) break;
        if (epoch === previous) continue;
        watcher.lastEpoch = epoch;
        this.notifyEpochChange(watcher, epoch, previous);
      }
    } finally {
      watcher.running = false;
      if (watcher.active && watcher.subscriptions.size > 0 && this.flagsView) {
        this.startEpochWatcher(watcher);
      }
    }
  }

  private notifyEpochInitial(watcher: RuntimeEpochWatcher, epoch: number): void {
    for (const subscription of watcher.subscriptions) {
      if (subscription.emitInitial) {
        this.notifyEpochListener(subscription.listener, epoch, epoch);
      }
    }
  }

  private notifyEpochChange(watcher: RuntimeEpochWatcher, epoch: number, previous: number): void {
    for (const subscription of watcher.subscriptions) {
      this.notifyEpochListener(subscription.listener, epoch, previous);
    }
  }

  private notifyEpochListener(listener: RuntimeEpochListener, epoch: number, previous: number): void {
    try {
      listener(epoch, previous);
    } catch {
      // Runtime listeners must not break the watcher loop.
    }
  }
}

export const createRuntimeBridge = (init?: RuntimeBridgeInit): RuntimeBridge => {
  const bridge = new RuntimeBridge();
  if (init) {
    bridge.initialize(init);
  }
  return bridge;
};
