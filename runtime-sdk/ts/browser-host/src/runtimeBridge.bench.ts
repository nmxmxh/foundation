import { bench, describe } from "vitest";
import { RuntimeBridge } from "./runtimeBridge";

describe("RuntimeBridge epochs", () => {
  const buffer =
    typeof SharedArrayBuffer !== "undefined"
      ? new SharedArrayBuffer(4096)
      : new ArrayBuffer(4096);
  const bridge = new RuntimeBridge();
  bridge.initialize({ buffer, flagOffset: 0, flagCount: 64 });

  bench("epoch signal/load", () => {
    for (let index = 0; index < 4096; index += 1) {
      bridge.signalEpoch(1);
      bridge.atomicLoad(1);
    }
  });

  bench("cached region view", () => {
    for (let index = 0; index < 4096; index += 1) {
      bridge.getRegionUint8View(256, 1024);
    }
  });
});
