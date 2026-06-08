import { describe, expect, it } from "vitest";

import { RuntimeBridge } from "./runtimeBridge";

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
});
