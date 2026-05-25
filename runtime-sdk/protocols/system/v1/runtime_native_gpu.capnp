@0xfafac001d15ea003;

# Opaque native GPU descriptor receipt.
#
# This is the canonical Foundation contract for platform GPU resources. Public
# APIs may expose ergonomic string-shaped descriptors, but native/browser hosts
# must map them to this Cap'n Proto contract before crossing runtime boundaries.
# Raw OS/GPU handles such as file descriptors, IOSurface objects,
# AHardwareBuffer pointers, CUDA external memory, Vulkan memory handles, Metal
# textures, or synchronization primitives are deliberately absent. Those live in
# runtime-native/plugin-owned side tables.

const NATIVE_GPU_DESCRIPTOR_SCHEMA_VERSION :UInt32 = 1;

const NATIVE_GPU_KIND_BUFFER :UInt32 = 1;
const NATIVE_GPU_KIND_TEXTURE :UInt32 = 2;
const NATIVE_GPU_KIND_EXTERNAL_IMAGE :UInt32 = 3;

const NATIVE_GPU_PLATFORM_LINUX_DMABUF :UInt32 = 1;
const NATIVE_GPU_PLATFORM_APPLE_IOSURFACE :UInt32 = 2;
const NATIVE_GPU_PLATFORM_ANDROID_HARDWARE_BUFFER :UInt32 = 3;
const NATIVE_GPU_PLATFORM_CUDA_EXTERNAL :UInt32 = 4;
const NATIVE_GPU_PLATFORM_VULKAN_EXTERNAL :UInt32 = 5;

const NATIVE_GPU_FALLBACK_COPY_TO_ARENA :UInt32 = 1;
const NATIVE_GPU_FALLBACK_COPY_TO_WEBGPU :UInt32 = 2;
const NATIVE_GPU_FALLBACK_CPU_MATERIALIZE :UInt32 = 3;

const NATIVE_GPU_DESCRIPTOR_TEXT_MAX_BYTES :UInt32 = 256;
const NATIVE_GPU_DESCRIPTOR_ID_MAX_BYTES :UInt32 = 128;

struct RuntimeNativeGpuDescriptor {
  schemaVersion @0 :UInt32;
  id @1 :Text;
  kind @2 :UInt32;
  platform @3 :UInt32;
  byteLength @4 :UInt64;
  width @5 :UInt32;
  height @6 :UInt32;
  format @7 :Text;
  schemaName @8 :Text;
  producer @9 :Text;
  fallback @10 :UInt32;
}
