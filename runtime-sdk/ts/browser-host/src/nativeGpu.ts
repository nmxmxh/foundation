import {
  NATIVE_GPU_DESCRIPTOR_ID_MAX_BYTES,
  NATIVE_GPU_DESCRIPTOR_SCHEMA_VERSION,
  NATIVE_GPU_DESCRIPTOR_TEXT_MAX_BYTES,
  NATIVE_GPU_FALLBACK_COPY_TO_ARENA,
  NATIVE_GPU_FALLBACK_COPY_TO_WEBGPU,
  NATIVE_GPU_FALLBACK_CPU_MATERIALIZE,
  NATIVE_GPU_KIND_BUFFER,
  NATIVE_GPU_KIND_EXTERNAL_IMAGE,
  NATIVE_GPU_KIND_TEXTURE,
  NATIVE_GPU_PLATFORM_ANDROID_HARDWARE_BUFFER,
  NATIVE_GPU_PLATFORM_APPLE_IOSURFACE,
  NATIVE_GPU_PLATFORM_CUDA_EXTERNAL,
  NATIVE_GPU_PLATFORM_LINUX_DMABUF,
  NATIVE_GPU_PLATFORM_VULKAN_EXTERNAL,
} from "./generated/runtimeBuffer";

export const RUNTIME_NATIVE_GPU_KINDS = [
  "native-gpu-buffer",
  "native-gpu-texture",
  "external-image",
] as const;

export const RUNTIME_NATIVE_GPU_PLATFORMS = [
  "linux-dmabuf",
  "apple-iosurface",
  "android-hardware-buffer",
  "cuda-external",
  "vulkan-external",
] as const;

export const RUNTIME_NATIVE_GPU_FALLBACKS = [
  "copy-to-arena",
  "copy-to-webgpu",
  "cpu-materialize",
] as const;

export type RuntimeNativeGpuKind = typeof RUNTIME_NATIVE_GPU_KINDS[number];
export type RuntimeNativeGpuPlatform = typeof RUNTIME_NATIVE_GPU_PLATFORMS[number];
export type RuntimeNativeGpuFallback = typeof RUNTIME_NATIVE_GPU_FALLBACKS[number];

export type RuntimeNativeGpuDescriptor = {
  id: string;
  kind: RuntimeNativeGpuKind;
  platform: RuntimeNativeGpuPlatform;
  byteLength?: number;
  width?: number;
  height?: number;
  format?: string;
  schemaName?: string;
  producer: string;
  fallback: RuntimeNativeGpuFallback;
};

export type RuntimeNativeGpuContractDescriptor = {
  schemaVersion: typeof NATIVE_GPU_DESCRIPTOR_SCHEMA_VERSION;
  id: string;
  kind: number;
  platform: number;
  byteLength: number;
  width: number;
  height: number;
  format: string;
  schemaName: string;
  producer: string;
  fallback: number;
};

export type RuntimeNativeGpuDescriptorValidation =
  | { ok: true; descriptor: RuntimeNativeGpuDescriptor }
  | { ok: false; reason: string };

const HANDLE_LIKE_PUBLIC_KEYS = [
  "fd",
  "filedescriptor",
  "handle",
  "rawhandle",
  "nativehandle",
  "dmabuf",
  "dmabuffd",
  "iosurface",
  "iosurfaceid",
  "ahardwarebuffer",
  "cudahandle",
  "cudaexternalmemory",
  "vulkanmemory",
  "vulkanhandle",
  "metaltexture",
  "mtltexture",
] as const;

const TEXT_MAX_BYTES = NATIVE_GPU_DESCRIPTOR_TEXT_MAX_BYTES;
const DESCRIPTOR_ID_PATTERN = /^[A-Za-z0-9._:-]{1,128}$/;

export const validateRuntimeNativeGpuDescriptor = (
  value: unknown
): RuntimeNativeGpuDescriptorValidation => {
  if (!isRecord(value)) {
    return invalid("native GPU descriptor must be an object");
  }
  const leakedKey = findHandleLikeKey(value);
  if (leakedKey) {
    return invalid(`native GPU descriptor must not expose raw handle field ${leakedKey}`);
  }

  const id = requiredString(value.id, "id");
  if (!id.ok) {
    return id;
  }
  if (!DESCRIPTOR_ID_PATTERN.test(id.value)) {
    return invalid("native GPU descriptor id must be 1-128 URL-safe identifier bytes");
  }

  const kind = requiredEnum(value.kind, RUNTIME_NATIVE_GPU_KINDS, "kind");
  if (!kind.ok) {
    return kind;
  }
  const platform = requiredEnum(value.platform, RUNTIME_NATIVE_GPU_PLATFORMS, "platform");
  if (!platform.ok) {
    return platform;
  }
  const fallback = requiredEnum(value.fallback, RUNTIME_NATIVE_GPU_FALLBACKS, "fallback");
  if (!fallback.ok) {
    return fallback;
  }
  const producer = requiredString(value.producer, "producer");
  if (!producer.ok) {
    return producer;
  }

  const byteLength = optionalPositiveInteger(value.byteLength, "byteLength");
  if (!byteLength.ok) {
    return byteLength;
  }
  const width = optionalPositiveInteger(value.width, "width");
  if (!width.ok) {
    return width;
  }
  const height = optionalPositiveInteger(value.height, "height");
  if (!height.ok) {
    return height;
  }
  const format = optionalString(value.format, "format");
  if (!format.ok) {
    return format;
  }
  const schemaName = optionalString(value.schemaName, "schemaName");
  if (!schemaName.ok) {
    return schemaName;
  }

  if (kind.value === "native-gpu-buffer" && byteLength.value === undefined) {
    return invalid("native GPU buffer descriptors require byteLength");
  }
  if ((kind.value === "native-gpu-texture" || kind.value === "external-image") &&
    (width.value === undefined || height.value === undefined || format.value === undefined)) {
    return invalid("native GPU image descriptors require width, height, and format");
  }

  return {
    ok: true,
    descriptor: {
      id: id.value,
      kind: kind.value,
      platform: platform.value,
      ...(byteLength.value === undefined ? {} : { byteLength: byteLength.value }),
      ...(width.value === undefined ? {} : { width: width.value }),
      ...(height.value === undefined ? {} : { height: height.value }),
      ...(format.value === undefined ? {} : { format: format.value }),
      ...(schemaName.value === undefined ? {} : { schemaName: schemaName.value }),
      producer: producer.value,
      fallback: fallback.value,
    },
  };
};

export const assertRuntimeNativeGpuDescriptor = (value: unknown): RuntimeNativeGpuDescriptor => {
  const validation = validateRuntimeNativeGpuDescriptor(value);
  if (!validation.ok) {
    throw new Error(validation.reason);
  }
  return validation.descriptor;
};

export const isRuntimeNativeGpuDescriptor = (value: unknown): value is RuntimeNativeGpuDescriptor =>
  validateRuntimeNativeGpuDescriptor(value).ok;

export const toRuntimeNativeGpuContractDescriptor = (
  value: RuntimeNativeGpuDescriptor
): RuntimeNativeGpuContractDescriptor => {
  const descriptor = assertRuntimeNativeGpuDescriptor(value);
  return {
    schemaVersion: NATIVE_GPU_DESCRIPTOR_SCHEMA_VERSION,
    id: descriptor.id,
    kind: kindToContractCode(descriptor.kind),
    platform: platformToContractCode(descriptor.platform),
    byteLength: descriptor.byteLength ?? 0,
    width: descriptor.width ?? 0,
    height: descriptor.height ?? 0,
    format: descriptor.format ?? "",
    schemaName: descriptor.schemaName ?? "",
    producer: descriptor.producer,
    fallback: fallbackToContractCode(descriptor.fallback),
  };
};

const invalid = (reason: string): Extract<RuntimeNativeGpuDescriptorValidation, { ok: false }> => ({ ok: false, reason });

const isRecord = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === "object";

const findHandleLikeKey = (value: Record<string, unknown>): string | null => {
  for (const key in value) {
    if (!Object.prototype.hasOwnProperty.call(value, key)) {
      continue;
    }
    if (isHandleLikeKey(key)) {
      return key;
    }
  }
  return null;
};

const isHandleLikeKey = (key: string): boolean => {
  for (const reserved of HANDLE_LIKE_PUBLIC_KEYS) {
    if (asciiEqualsIgnoreCase(key, reserved)) {
      return true;
    }
  }
  return false;
};

const asciiEqualsIgnoreCase = (left: string, right: string): boolean => {
  if (left.length !== right.length) {
    return false;
  }
  for (let index = 0; index < left.length; index += 1) {
    const leftCode = left.charCodeAt(index);
    const rightCode = right.charCodeAt(index);
    if (leftCode === rightCode) {
      continue;
    }
    const normalizedLeft = leftCode >= 65 && leftCode <= 90 ? leftCode + 32 : leftCode;
    const normalizedRight = rightCode >= 65 && rightCode <= 90 ? rightCode + 32 : rightCode;
    if (normalizedLeft !== normalizedRight) {
      return false;
    }
  }
  return true;
};

const requiredString = (
  value: unknown,
  field: string
): { ok: true; value: string } | { ok: false; reason: string } => {
  if (typeof value !== "string" || value.trim() === "") {
    return invalid(`native GPU descriptor ${field} is required`);
  }
  const byteLength = utf8ByteLength(value);
  if (field === "id" && byteLength > NATIVE_GPU_DESCRIPTOR_ID_MAX_BYTES) {
    return invalid(`native GPU descriptor ${field} exceeds ${NATIVE_GPU_DESCRIPTOR_ID_MAX_BYTES} bytes`);
  }
  if (byteLength > TEXT_MAX_BYTES) {
    return invalid(`native GPU descriptor ${field} exceeds ${TEXT_MAX_BYTES} bytes`);
  }
  return { ok: true, value };
};

const optionalString = (
  value: unknown,
  field: string
): { ok: true; value?: string } | { ok: false; reason: string } => {
  if (value === undefined) {
    return { ok: true };
  }
  if (typeof value !== "string" || value.trim() === "") {
    return invalid(`native GPU descriptor ${field} must be a non-empty string`);
  }
  if (utf8ByteLength(value) > TEXT_MAX_BYTES) {
    return invalid(`native GPU descriptor ${field} exceeds ${TEXT_MAX_BYTES} bytes`);
  }
  return { ok: true, value };
};

const optionalPositiveInteger = (
  value: unknown,
  field: string
): { ok: true; value?: number } | { ok: false; reason: string } => {
  if (value === undefined) {
    return { ok: true };
  }
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value <= 0) {
    return invalid(`native GPU descriptor ${field} must be a positive safe integer`);
  }
  return { ok: true, value };
};

const requiredEnum = <T extends readonly string[]>(
  value: unknown,
  allowed: T,
  field: string
): { ok: true; value: T[number] } | { ok: false; reason: string } => {
  if (typeof value !== "string" || !allowed.includes(value as T[number])) {
    return invalid(`native GPU descriptor ${field} is unsupported`);
  }
  return { ok: true, value };
};

const kindToContractCode = (kind: RuntimeNativeGpuKind): number => {
  switch (kind) {
    case "native-gpu-buffer":
      return NATIVE_GPU_KIND_BUFFER;
    case "native-gpu-texture":
      return NATIVE_GPU_KIND_TEXTURE;
    case "external-image":
      return NATIVE_GPU_KIND_EXTERNAL_IMAGE;
  }
};

const platformToContractCode = (platform: RuntimeNativeGpuPlatform): number => {
  switch (platform) {
    case "linux-dmabuf":
      return NATIVE_GPU_PLATFORM_LINUX_DMABUF;
    case "apple-iosurface":
      return NATIVE_GPU_PLATFORM_APPLE_IOSURFACE;
    case "android-hardware-buffer":
      return NATIVE_GPU_PLATFORM_ANDROID_HARDWARE_BUFFER;
    case "cuda-external":
      return NATIVE_GPU_PLATFORM_CUDA_EXTERNAL;
    case "vulkan-external":
      return NATIVE_GPU_PLATFORM_VULKAN_EXTERNAL;
  }
};

const fallbackToContractCode = (fallback: RuntimeNativeGpuFallback): number => {
  switch (fallback) {
    case "copy-to-arena":
      return NATIVE_GPU_FALLBACK_COPY_TO_ARENA;
    case "copy-to-webgpu":
      return NATIVE_GPU_FALLBACK_COPY_TO_WEBGPU;
    case "cpu-materialize":
      return NATIVE_GPU_FALLBACK_CPU_MATERIALIZE;
  }
};

const utf8ByteLength = (value: string): number => {
  let bytes = 0;
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index);
    if (code <= 0x7f) {
      bytes += 1;
    } else if (code <= 0x7ff) {
      bytes += 2;
    } else if (code >= 0xd800 && code <= 0xdbff && index + 1 < value.length) {
      const next = value.charCodeAt(index + 1);
      if (next >= 0xdc00 && next <= 0xdfff) {
        bytes += 4;
        index += 1;
      } else {
        bytes += 3;
      }
    } else {
      bytes += 3;
    }
  }
  return bytes;
};
