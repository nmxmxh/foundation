import { describe, expect, it } from "vitest";

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
import {
  assertRuntimeNativeGpuDescriptor,
  isRuntimeNativeGpuDescriptor,
  toRuntimeNativeGpuContractDescriptor,
  validateRuntimeNativeGpuDescriptor,
  type RuntimeNativeGpuDescriptor,
} from "./nativeGpu";

const bufferDescriptor: RuntimeNativeGpuDescriptor = {
  id: "camera.frame.42",
  kind: "native-gpu-buffer",
  platform: "apple-iosurface",
  byteLength: 4096,
  schemaName: "media/v1/frame.capnp",
  producer: "camera.plugin",
  fallback: "copy-to-webgpu",
};

const imageDescriptor: RuntimeNativeGpuDescriptor = {
  id: "decoder.frame.7",
  kind: "external-image",
  platform: "android-hardware-buffer",
  width: 1920,
  height: 1080,
  format: "nv12",
  producer: "decoder.plugin",
  fallback: "cpu-materialize",
};

describe("runtime native GPU descriptor contract", () => {
  it("accepts opaque buffer descriptors without materializing platform handles", () => {
    const result = validateRuntimeNativeGpuDescriptor(bufferDescriptor);

    expect(result).toEqual({ ok: true, descriptor: bufferDescriptor });
    expect(isRuntimeNativeGpuDescriptor(bufferDescriptor)).toBe(true);
    expect(assertRuntimeNativeGpuDescriptor(bufferDescriptor)).toEqual(bufferDescriptor);
  });

  it("maps public descriptors to the canonical Cap'n Proto numeric contract", () => {
    expect(toRuntimeNativeGpuContractDescriptor(bufferDescriptor)).toEqual({
      schemaVersion: NATIVE_GPU_DESCRIPTOR_SCHEMA_VERSION,
      id: "camera.frame.42",
      kind: NATIVE_GPU_KIND_BUFFER,
      platform: NATIVE_GPU_PLATFORM_APPLE_IOSURFACE,
      byteLength: 4096,
      width: 0,
      height: 0,
      format: "",
      schemaName: "media/v1/frame.capnp",
      producer: "camera.plugin",
      fallback: NATIVE_GPU_FALLBACK_COPY_TO_WEBGPU,
    });
  });

  it("maps all public enum strings to Cap'n Proto contract codes", () => {
    const cases: Array<[RuntimeNativeGpuDescriptor, number, number, number]> = [
      [
        {
          ...bufferDescriptor,
          kind: "native-gpu-buffer",
          platform: "linux-dmabuf",
          fallback: "copy-to-arena",
        },
        NATIVE_GPU_KIND_BUFFER,
        NATIVE_GPU_PLATFORM_LINUX_DMABUF,
        NATIVE_GPU_FALLBACK_COPY_TO_ARENA,
      ],
      [
        {
          ...imageDescriptor,
          kind: "native-gpu-texture",
          platform: "cuda-external",
          fallback: "copy-to-webgpu",
        },
        NATIVE_GPU_KIND_TEXTURE,
        NATIVE_GPU_PLATFORM_CUDA_EXTERNAL,
        NATIVE_GPU_FALLBACK_COPY_TO_WEBGPU,
      ],
      [
        {
          ...imageDescriptor,
          kind: "external-image",
          platform: "vulkan-external",
          fallback: "cpu-materialize",
        },
        NATIVE_GPU_KIND_EXTERNAL_IMAGE,
        NATIVE_GPU_PLATFORM_VULKAN_EXTERNAL,
        NATIVE_GPU_FALLBACK_CPU_MATERIALIZE,
      ],
      [
        {
          ...imageDescriptor,
          platform: "android-hardware-buffer",
        },
        NATIVE_GPU_KIND_EXTERNAL_IMAGE,
        NATIVE_GPU_PLATFORM_ANDROID_HARDWARE_BUFFER,
        NATIVE_GPU_FALLBACK_CPU_MATERIALIZE,
      ],
    ];

    for (const [descriptor, kind, platform, fallback] of cases) {
      expect(toRuntimeNativeGpuContractDescriptor(descriptor)).toMatchObject({
        schemaVersion: NATIVE_GPU_DESCRIPTOR_SCHEMA_VERSION,
        kind,
        platform,
        fallback,
      });
    }
  });

  it("accepts external image descriptors with explicit dimensions and format", () => {
    const result = validateRuntimeNativeGpuDescriptor(imageDescriptor);

    expect(result.ok).toBe(true);
    if (result.ok) {
      expect(result.descriptor.width).toBe(1920);
      expect(result.descriptor.height).toBe(1080);
      expect(result.descriptor.format).toBe("nv12");
    }
  });

  it("rejects public descriptor fields that look like raw platform handles", () => {
    const result = validateRuntimeNativeGpuDescriptor({
      ...bufferDescriptor,
      fd: 12,
    });

    expect(result).toEqual({
      ok: false,
      reason: "native GPU descriptor must not expose raw handle field fd",
    });
  });

  it("rejects incomplete buffers and images before lane planning can select native GPU", () => {
    expect(validateRuntimeNativeGpuDescriptor({
      ...bufferDescriptor,
      byteLength: undefined,
    })).toMatchObject({ ok: false });

    expect(validateRuntimeNativeGpuDescriptor({
      ...imageDescriptor,
      format: undefined,
    })).toMatchObject({ ok: false });
  });

  it("rejects unsupported platforms, fallbacks, empty producers, and unsafe ids", () => {
    expect(validateRuntimeNativeGpuDescriptor({
      ...bufferDescriptor,
      platform: "metal-texture",
    })).toMatchObject({ ok: false });

    expect(validateRuntimeNativeGpuDescriptor({
      ...bufferDescriptor,
      fallback: "direct-map",
    })).toMatchObject({ ok: false });

    expect(validateRuntimeNativeGpuDescriptor({
      ...bufferDescriptor,
      producer: "",
    })).toMatchObject({ ok: false });

    expect(validateRuntimeNativeGpuDescriptor({
      ...bufferDescriptor,
      id: "../raw/handle",
    })).toMatchObject({ ok: false });
  });

  it("checks descriptor text boundaries using the Cap'n Proto limits", () => {
    const maxId = "a".repeat(NATIVE_GPU_DESCRIPTOR_ID_MAX_BYTES);
    const maxProducer = "p".repeat(NATIVE_GPU_DESCRIPTOR_TEXT_MAX_BYTES);

    expect(validateRuntimeNativeGpuDescriptor({
      ...bufferDescriptor,
      id: maxId,
      producer: maxProducer,
    })).toMatchObject({ ok: true });

    expect(validateRuntimeNativeGpuDescriptor({
      ...bufferDescriptor,
      id: `${maxId}x`,
    })).toMatchObject({ ok: false });

    expect(validateRuntimeNativeGpuDescriptor({
      ...bufferDescriptor,
      producer: `${maxProducer}x`,
    })).toMatchObject({ ok: false });
  });

  it("rejects non-number and unsafe dimension fields", () => {
    expect(validateRuntimeNativeGpuDescriptor({
      ...bufferDescriptor,
      byteLength: "4096",
    })).toMatchObject({ ok: false });

    expect(validateRuntimeNativeGpuDescriptor({
      ...imageDescriptor,
      width: Number.MAX_SAFE_INTEGER + 1,
    })).toMatchObject({ ok: false });

    expect(validateRuntimeNativeGpuDescriptor({
      ...imageDescriptor,
      height: 0,
    })).toMatchObject({ ok: false });
  });

  it("throws with the validation reason for assertion-style callers", () => {
    expect(() => assertRuntimeNativeGpuDescriptor({ ...bufferDescriptor, byteLength: 0 })).toThrow(
      "byteLength"
    );
  });
});
