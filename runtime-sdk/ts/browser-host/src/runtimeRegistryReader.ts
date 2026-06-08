export type RuntimeRegistryLayout = {
  registryOffset: number;
  entrySize: number;
  maxEntries: number;
  idOffset: number;
  idLength: number;
  activeFlagOffset: number;
  activeFlagMask: number;
  versionMajorOffset?: number;
  versionMinorOffset?: number;
  versionPatchOffset?: number;
  memoryUsageOffset?: number;
  capabilityTableOffsetOffset?: number;
  capabilityCountOffset?: number;
  capabilityEntrySize?: number;
  capabilityIdOffset?: number;
  capabilityIdLength?: number;
};

export type RuntimeRegistryModule = {
  id: string;
  active: boolean;
  version: string;
  capabilities: string[];
  memoryUsage: number;
  slot: number;
};

export const INOS_COMPAT_REGISTRY_LAYOUT: Omit<RuntimeRegistryLayout, "registryOffset"> = {
  entrySize: 96,
  maxEntries: 64,
  idOffset: 64,
  idLength: 12,
  activeFlagOffset: 15,
  activeFlagMask: 0b0010,
  versionMajorOffset: 12,
  versionMinorOffset: 13,
  versionPatchOffset: 14,
  memoryUsageOffset: 34,
  capabilityTableOffsetOffset: 56,
  capabilityCountOffset: 60,
  capabilityEntrySize: 36,
  capabilityIdOffset: 0,
  capabilityIdLength: 32,
};

const decoder = new TextDecoder();

const readCString = (buffer: ArrayBufferLike, offset: number, length: number): string => {
  if (offset < 0 || length <= 0 || offset + length > buffer.byteLength) return "";
  const bytes = new Uint8Array(buffer, offset, length);
  let end = 0;
  while (end < bytes.length && bytes[end] !== 0) {
    end += 1;
  }
  return decoder.decode(bytes.slice(0, end));
};

const readUint16 = (view: DataView, offset: number | undefined): number => {
  if (offset === undefined || offset < 0 || offset + 2 > view.byteLength) return 0;
  return view.getUint16(offset, true);
};

const readUint32 = (view: DataView, offset: number | undefined): number => {
  if (offset === undefined || offset < 0 || offset + 4 > view.byteLength) return 0;
  return view.getUint32(offset, true);
};

export class RuntimeRegistryReader {
  private readonly view: DataView;

  constructor(
    private readonly buffer: ArrayBufferLike,
    private readonly layout: RuntimeRegistryLayout,
    private readonly baseOffset = 0
  ) {
    this.view = new DataView(buffer);
  }

  scan(): Record<string, RuntimeRegistryModule> {
    const modules: Record<string, RuntimeRegistryModule> = {};
    for (let slot = 0; slot < this.layout.maxEntries; slot += 1) {
      const offset = this.absoluteEntryOffset(slot);
      if (offset + this.layout.entrySize > this.buffer.byteLength) break;

      const flags = this.view.getUint8(offset + this.layout.activeFlagOffset);
      const active = (flags & this.layout.activeFlagMask) !== 0;
      if (!active) continue;

      const id = readCString(this.buffer, offset + this.layout.idOffset, this.layout.idLength);
      if (!id) continue;

      modules[id] = {
        id,
        active,
        version: this.readVersion(offset),
        capabilities: this.readCapabilities(offset),
        memoryUsage: readUint16(this.view, offset + (this.layout.memoryUsageOffset ?? -1)),
        slot,
      };
    }
    return modules;
  }

  private absoluteEntryOffset(slot: number): number {
    return this.baseOffset + this.layout.registryOffset + slot * this.layout.entrySize;
  }

  private readVersion(entryOffset: number): string {
    const major =
      this.layout.versionMajorOffset === undefined
        ? 0
        : this.view.getUint8(entryOffset + this.layout.versionMajorOffset);
    const minor =
      this.layout.versionMinorOffset === undefined
        ? 0
        : this.view.getUint8(entryOffset + this.layout.versionMinorOffset);
    const patch =
      this.layout.versionPatchOffset === undefined
        ? 0
        : this.view.getUint8(entryOffset + this.layout.versionPatchOffset);
    return `${major}.${minor}.${patch}`;
  }

  private readCapabilities(entryOffset: number): string[] {
    const tableField = this.layout.capabilityTableOffsetOffset;
    const countField = this.layout.capabilityCountOffset;
    const entrySize = this.layout.capabilityEntrySize;
    const idOffset = this.layout.capabilityIdOffset ?? 0;
    const idLength = this.layout.capabilityIdLength ?? 0;
    if (
      tableField === undefined ||
      countField === undefined ||
      entrySize === undefined ||
      idLength <= 0
    ) {
      return [];
    }

    const tableOffset = readUint32(this.view, entryOffset + tableField);
    const count = readUint16(this.view, entryOffset + countField);
    if (tableOffset === 0 || count === 0) return [];

    const capabilities: string[] = [];
    const absoluteTableOffset = this.baseOffset + tableOffset;
    for (let index = 0; index < count; index += 1) {
      const capabilityOffset = absoluteTableOffset + index * entrySize + idOffset;
      if (capabilityOffset + idLength > this.buffer.byteLength) break;
      const id = readCString(this.buffer, capabilityOffset, idLength);
      if (id) capabilities.push(id);
    }
    return capabilities;
  }
}

export const createInosCompatRegistryReader = (
  buffer: ArrayBufferLike,
  registryOffset: number,
  baseOffset = 0,
  overrides: Partial<RuntimeRegistryLayout> = {}
): RuntimeRegistryReader =>
  new RuntimeRegistryReader(
    buffer,
    {
      ...INOS_COMPAT_REGISTRY_LAYOUT,
      registryOffset,
      ...overrides,
    },
    baseOffset
  );
