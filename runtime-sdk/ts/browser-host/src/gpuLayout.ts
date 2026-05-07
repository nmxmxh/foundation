export type RuntimeGpuBatchItem = {
  byteLength?: number;
  length?: number;
};

export type RuntimeGpuBatchRegion = {
  index: number;
  offset: number;
  byteLength: number;
  paddedByteLength: number;
};

export type RuntimeGpuBatchLayout = {
  alignment: number;
  totalBytes: number;
  regions: RuntimeGpuBatchRegion[];
  workgroupSize: number;
  workgroupCount: number;
};

const DEFAULT_STORAGE_ALIGNMENT = 256;
const DEFAULT_WORKGROUP_SIZE = 64;

export const planRuntimeGpuBatchLayout = (
  items: readonly RuntimeGpuBatchItem[],
  options: { alignment?: number; workgroupSize?: number } = {}
): RuntimeGpuBatchLayout => {
  const alignment = positivePowerOfTwo(options.alignment ?? DEFAULT_STORAGE_ALIGNMENT, "alignment");
  const workgroupSize = Math.max(1, Math.floor(options.workgroupSize ?? DEFAULT_WORKGROUP_SIZE));
  const regions: RuntimeGpuBatchRegion[] = [];
  let cursor = 0;

  for (let index = 0; index < items.length; index += 1) {
    const item = items[index];
    const byteLength = Math.max(0, Math.floor(item?.byteLength ?? item?.length ?? 0));
    const offset = align(cursor, alignment);
    const paddedByteLength = align(byteLength, alignment);
    regions.push({ index, offset, byteLength, paddedByteLength });
    cursor = offset + paddedByteLength;
  }

  return {
    alignment,
    totalBytes: align(cursor, alignment),
    regions,
    workgroupSize,
    workgroupCount: Math.ceil(Math.max(1, items.length) / workgroupSize),
  };
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
