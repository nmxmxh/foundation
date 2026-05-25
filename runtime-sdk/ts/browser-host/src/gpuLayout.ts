export type RuntimeGpuBatchItem = {
  byteLength?: number;
  length?: number;
};

export type RuntimeGpuDispatchItemsFrom = "regions" | "bytes" | "u32" | "f32";

export type RuntimeGpuBatchRegion = {
  index: number;
  offset: number;
  byteLength: number;
  paddedByteLength: number;
};

export type RuntimeGpuBatchLayout = {
  alignment: number;
  itemCount: number;
  payloadBytes: number;
  paddingBytes: number;
  maxRegionBytes: number;
  totalBytes: number;
  regions: RuntimeGpuBatchRegion[];
  workgroupSize: number;
  workgroupCount: number;
  dispatchItems: number;
  dispatchWaste: number;
};

export type RuntimeGpuBatchLayoutOptions = {
  alignment?: number;
  workgroupSize?: number;
  dispatchItems?: number;
  dispatchItemsFrom?: RuntimeGpuDispatchItemsFrom;
  strict?: boolean;
  allowZeroByteRegions?: boolean;
  maxTotalBytes?: number;
};

export const createRuntimeGpuBatchLayoutScratch = (): RuntimeGpuBatchLayout => ({
  alignment: 0,
  itemCount: 0,
  payloadBytes: 0,
  paddingBytes: 0,
  maxRegionBytes: 0,
  totalBytes: 0,
  regions: [],
  workgroupSize: 0,
  workgroupCount: 0,
  dispatchItems: 0,
  dispatchWaste: 0,
});

const DEFAULT_STORAGE_ALIGNMENT = 256;
const DEFAULT_WORKGROUP_SIZE = 64;

export const planRuntimeGpuBatchLayout = (
  items: readonly RuntimeGpuBatchItem[],
  options: RuntimeGpuBatchLayoutOptions = {}
): RuntimeGpuBatchLayout => {
  return planRuntimeGpuBatchLayoutInto(items, createRuntimeGpuBatchLayoutScratch(), options);
};

export const planRuntimeGpuBatchLayoutInto = (
  items: readonly RuntimeGpuBatchItem[],
  target: RuntimeGpuBatchLayout,
  options: RuntimeGpuBatchLayoutOptions = {}
): RuntimeGpuBatchLayout => {
  const alignment = positivePowerOfTwo(options.alignment ?? DEFAULT_STORAGE_ALIGNMENT, "alignment");
  const workgroupSize = positiveInteger(options.workgroupSize ?? DEFAULT_WORKGROUP_SIZE, "workgroup size");
  const regions = target.regions;
  regions.length = items.length;
  let cursor = 0;
  let payloadBytes = 0;
  let maxRegionBytes = 0;

  for (let index = 0; index < items.length; index += 1) {
    const item = items[index];
    const byteLength = normalizeByteLength(item?.byteLength ?? item?.length ?? 0, index, options);
    const offset = align(cursor, alignment);
    const paddedByteLength = align(byteLength, alignment);
    const region = regions[index];
    if (region) {
      region.index = index;
      region.offset = offset;
      region.byteLength = byteLength;
      region.paddedByteLength = paddedByteLength;
    } else {
      regions[index] = { index, offset, byteLength, paddedByteLength };
    }
    cursor = offset + paddedByteLength;
    payloadBytes += byteLength;
    maxRegionBytes = Math.max(maxRegionBytes, byteLength);
  }
  const totalBytes = align(cursor, alignment);
  if (
    options.maxTotalBytes !== undefined &&
    totalBytes > positiveInteger(options.maxTotalBytes, "max total bytes")
  ) {
    throw new Error(`runtime GPU batch layout exceeds max total bytes: ${totalBytes} > ${options.maxTotalBytes}`);
  }

  const dispatchItems = resolveDispatchItems(totalBytes, items.length, options);
  const workgroupCount = Math.ceil(Math.max(1, dispatchItems) / workgroupSize);
  const dispatchCapacity = workgroupCount * workgroupSize;

  target.alignment = alignment;
  target.itemCount = items.length;
  target.payloadBytes = payloadBytes;
  target.paddingBytes = totalBytes - payloadBytes;
  target.maxRegionBytes = maxRegionBytes;
  target.totalBytes = totalBytes;
  target.workgroupSize = workgroupSize;
  target.workgroupCount = workgroupCount;
  target.dispatchItems = dispatchItems;
  target.dispatchWaste = dispatchCapacity - dispatchItems;
  return target;
};

const align = (value: number, alignment: number): number =>
  Math.ceil(value / alignment) * alignment;

const positivePowerOfTwo = (value: number, name: string): number => {
  const normalized = Math.floor(value);
  if (normalized <= 0 || (normalized & (normalized - 1)) !== 0) {
    throw new Error(`runtime GPU ${name} must be a positive power of two`);
  }
  return normalized;
};

const positiveInteger = (value: number, name: string): number => {
  const normalized = Math.floor(value);
  if (!Number.isFinite(value) || normalized <= 0) {
    throw new Error(`runtime GPU ${name} must be a positive integer`);
  }
  return normalized;
};

const normalizeByteLength = (
  value: number,
  index: number,
  options: RuntimeGpuBatchLayoutOptions
): number => {
  if (!Number.isFinite(value)) {
    if (options.strict) {
      throw new Error(`runtime GPU batch item ${index} byte length must be finite`);
    }
    return 0;
  }
  const normalized = Math.floor(value);
  if (options.strict && normalized !== value) {
    throw new Error(`runtime GPU batch item ${index} byte length must be an integer`);
  }
  if (options.strict && normalized < 0) {
    throw new Error(`runtime GPU batch item ${index} byte length must be non-negative`);
  }
  if (options.strict && normalized === 0 && options.allowZeroByteRegions !== true) {
    throw new Error(`runtime GPU batch item ${index} byte length must be non-zero`);
  }
  return Math.max(0, normalized);
};

const resolveDispatchItems = (
  totalBytes: number,
  regionCount: number,
  options: RuntimeGpuBatchLayoutOptions
): number => {
  if (options.dispatchItems !== undefined) {
    return positiveInteger(options.dispatchItems, "dispatch items");
  }
  switch (options.dispatchItemsFrom ?? "regions") {
    case "bytes":
      return Math.max(1, totalBytes);
    case "u32":
    case "f32":
      if (options.strict && totalBytes % 4 !== 0) {
        throw new Error(
          `runtime GPU ${options.dispatchItemsFrom} dispatch requires 4-byte aligned total bytes`
        );
      }
      return Math.max(1, Math.ceil(totalBytes / 4));
    case "regions":
      return Math.max(1, regionCount);
  }
};
