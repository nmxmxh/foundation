import { describe, expect, it } from "vitest";

import { createRuntimeBridge, RuntimeBridge } from "./runtimeBridge";

describe("RuntimeBridge", () => {
  it("initializes cached views and reads regions", () => {
    const buffer = new ArrayBuffer(1024);
    const bridge = new RuntimeBridge();
    bridge.initialize({ buffer, offset: 128, size: 512, flagOffset: 0, flagCount: 16 });

    const view = bridge.getRegionDataView(32, 16);
    expect(view).not.toBeNull();
    expect(bridge.getRegionDataView(32, 16)).toBe(view);

    view?.setUint32(0, 42, true);
    expect(bridge.readU32(32)).toBe(42);
  });

  it("supports non-shared epoch mutation for test and fallback lanes", async () => {
    const buffer = new ArrayBuffer(1024);
    const bridge = new RuntimeBridge();
    bridge.initialize({ buffer, flagCount: 16 });

    const seen: Array<[number, number]> = [];
    const unsubscribe = bridge.subscribeEpoch(2, (epoch, previous) => {
      seen.push([epoch, previous]);
    }, { pollMs: 4, waitTimeoutMs: 8 });

    expect(bridge.signalEpoch(2)).toBe(1);
    await bridge.waitForEpochChange(2, 0, 50);
    await new Promise((resolve) => setTimeout(resolve, 20));
    unsubscribe();

    expect(seen).toContainEqual([1, 0]);
  });

  it("exposes initialized state, cached byte views, scalar reads, and clearing", () => {
    const memory = new WebAssembly.Memory({ initial: 1 });
    const buffer = new ArrayBuffer(256);
    const bridge = createRuntimeBridge({ buffer, memory, offset: 32, size: 128, flagOffset: 0, flagCount: 4 });
    expect(bridge.isReady()).toBe(true);
    expect(bridge.isShared()).toBe(false);
    expect(bridge.getBuffer()).toBe(buffer);
    expect(bridge.getMemory()).toBe(memory);
    expect(bridge.getOffset()).toBe(32);
    expect(bridge.getSize()).toBe(128);
    expect(bridge.getDataView()).not.toBeNull();
    expect(bridge.getFlagsView()).toHaveLength(4);
    const bytes = bridge.getRegionUint8View(8, 8);
    expect(bridge.getRegionUint8View(8, 8)).toBe(bytes);
    const data = bridge.getRegionDataView(0, 16);
    data?.setInt32(0, -2, true);
    data?.setFloat32(4, 1.5, true);
    expect(bridge.readI32(0)).toBe(-2);
    expect(bridge.readF32(4)).toBeCloseTo(1.5);
    expect(bridge.atomicStore(1, 4)).toBe(0);
    expect(bridge.atomicLoad(1)).toBe(4);
    expect(bridge.atomicAdd(1, 3)).toBe(4);
    expect(bridge.atomicLoad(-1)).toBe(0);
    bridge.clear();
    expect(bridge.isReady()).toBe(false);
    expect(bridge.getRegionDataView(0, 1)).toBeNull();
    expect(bridge.getRegionUint8View(0, 1)).toBeNull();
    expect(bridge.readI32(0)).toBe(0);
    expect(bridge.atomicStore(0, 1)).toBe(0);
  });

  it("validates regions and supports shared atomic mutation", () => {
    const bridge = new RuntimeBridge();
    expect(() => bridge.initialize({ buffer: new ArrayBuffer(32), offset: -1 })).toThrow("non-negative integer");
    expect(() => bridge.initialize({ buffer: new ArrayBuffer(32), size: 33 })).toThrow("exceeds buffer capacity");
    expect(() => bridge.initialize({ buffer: new ArrayBuffer(32), flagCount: 9 })).toThrow("exceeds buffer capacity");
    const shared = new SharedArrayBuffer(128);
    bridge.initialize({ buffer: shared, flagCount: 4 });
    expect(bridge.isShared()).toBe(true);
    expect(bridge.atomicStore(2, 5)).toBe(5);
    expect(bridge.atomicAdd(2, 2)).toBe(5);
    expect(bridge.signalEpoch(2)).toBe(8);
  });

  it("emits initial epochs and isolates listener failures", async () => {
    const bridge = new RuntimeBridge();
    const buffer = new ArrayBuffer(128);
    bridge.initialize({ buffer, flagCount: 4 });
    bridge.atomicStore(1, 3);
    const seen: number[] = [];
    const first = bridge.subscribeEpoch(1, () => { throw new Error("listener"); }, { emitInitial: true, pollMs: 1, waitTimeoutMs: 1 });
    const second = bridge.subscribeEpoch(1, (epoch) => seen.push(epoch), { emitInitial: true, pollMs: 8, waitTimeoutMs: 4 });
    bridge.signalEpoch(1);
    await new Promise((resolve) => setTimeout(resolve, 20));
    first();
    second();
    expect(seen).toEqual(expect.arrayContaining([3, 4]));
  });
});
