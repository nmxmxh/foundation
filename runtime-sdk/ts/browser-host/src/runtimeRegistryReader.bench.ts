import { bench, describe } from "vitest";
import {
  INOS_COMPAT_REGISTRY_LAYOUT,
  RuntimeRegistryReader,
} from "./runtimeRegistryReader";

const encoder = new TextEncoder();

const writeCString = (buffer: ArrayBuffer, offset: number, length: number, value: string): void => {
  const bytes = encoder.encode(value);
  new Uint8Array(buffer, offset, length).fill(0);
  new Uint8Array(buffer, offset, Math.min(length, bytes.length)).set(bytes.slice(0, length));
};

describe("RuntimeRegistryReader", () => {
  const layout = {
    ...INOS_COMPAT_REGISTRY_LAYOUT,
    registryOffset: 0,
  };
  const buffer = new ArrayBuffer(layout.entrySize * layout.maxEntries + 4096);
  const view = new DataView(buffer);
  const capabilityBase = layout.entrySize * layout.maxEntries;
  const capabilityEntrySize = layout.capabilityEntrySize!;
  const capabilityIdOffset = layout.capabilityIdOffset!;
  const capabilityIdLength = layout.capabilityIdLength!;

  for (let slot = 0; slot < layout.maxEntries; slot += 1) {
    const entryOffset = slot * layout.entrySize;
    view.setUint8(entryOffset + layout.activeFlagOffset, layout.activeFlagMask);
    view.setUint8(entryOffset + (layout.versionMajorOffset ?? 0), 1);
    view.setUint8(entryOffset + (layout.versionMinorOffset ?? 0), slot % 8);
    view.setUint8(entryOffset + (layout.versionPatchOffset ?? 0), slot % 16);
    view.setUint16(entryOffset + (layout.memoryUsageOffset ?? 0), slot * 16, true);
    writeCString(buffer, entryOffset + layout.idOffset, layout.idLength, `mod${slot}`);

    const capabilityOffset = capabilityBase + slot * capabilityEntrySize;
    view.setUint32(entryOffset + (layout.capabilityTableOffsetOffset ?? 0), capabilityOffset, true);
    view.setUint16(entryOffset + (layout.capabilityCountOffset ?? 0), 1, true);
    writeCString(
      buffer,
      capabilityOffset + capabilityIdOffset,
      capabilityIdLength,
      `compute:${slot}`
    );
  }

  const reader = new RuntimeRegistryReader(buffer, layout);

  bench("scan 64 modules", () => {
    reader.scan();
  });
});
