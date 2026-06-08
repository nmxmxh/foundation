import { describe, expect, it } from "vitest";

import { RuntimeRegistryReader, type RuntimeRegistryLayout } from "./runtimeRegistryReader";

const layout: RuntimeRegistryLayout = {
  registryOffset: 64,
  entrySize: 32,
  maxEntries: 4,
  idOffset: 8,
  idLength: 8,
  activeFlagOffset: 4,
  activeFlagMask: 0b10,
  versionMajorOffset: 5,
  versionMinorOffset: 6,
  versionPatchOffset: 7,
  capabilityTableOffsetOffset: 20,
  capabilityCountOffset: 24,
  capabilityEntrySize: 12,
  capabilityIdOffset: 0,
  capabilityIdLength: 12,
};

const writeCString = (bytes: Uint8Array, offset: number, value: string): void => {
  bytes.set(new TextEncoder().encode(value), offset);
};

describe("RuntimeRegistryReader", () => {
  it("scans active module entries and capability tables", () => {
    const buffer = new ArrayBuffer(512);
    const bytes = new Uint8Array(buffer);
    const view = new DataView(buffer);
    const entry = layout.registryOffset;
    bytes[entry + layout.activeFlagOffset] = 0b10;
    bytes[entry + 5] = 1;
    bytes[entry + 6] = 2;
    bytes[entry + 7] = 3;
    writeCString(bytes, entry + layout.idOffset, "compute");
    view.setUint32(entry + 20, 256, true);
    view.setUint16(entry + 24, 2, true);
    writeCString(bytes, 256, "hash");
    writeCString(bytes, 268, "compress");

    const modules = new RuntimeRegistryReader(buffer, layout).scan();
    expect(modules.compute).toEqual({
      id: "compute",
      active: true,
      version: "1.2.3",
      capabilities: ["hash", "compress"],
      memoryUsage: 0,
      slot: 0,
    });
  });
});
