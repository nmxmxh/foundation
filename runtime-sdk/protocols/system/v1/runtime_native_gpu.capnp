@0xfafac001d15ea009;

# Opaque native GPU descriptor receipt.
#
# This is the canonical Foundation contract for platform GPU resources. Public
# APIs may expose ergonomic string-shaped descriptors, but native/browser hosts
# must map them to this Cap'n Proto contract before crossing runtime boundaries.
# Raw OS/GPU handles such as file descriptors, IOSurface objects,
# AHardwareBuffer pointers, CUDA external memory, Vulkan memory handles, Metal
# textures, or synchronization primitives are deliberately absent. Those live in
# runtime-native/plugin-owned side tables.

const nativeGpuDescriptorSchemaVersion :UInt32 = 1;

const nativeGpuKindBuffer :UInt32 = 1;
const nativeGpuKindTexture :UInt32 = 2;
const nativeGpuKindExternalImage :UInt32 = 3;

const nativeGpuPlatformLinuxDmabuf :UInt32 = 1;
const nativeGpuPlatformAppleIosurface :UInt32 = 2;
const nativeGpuPlatformAndroidHardwareBuffer :UInt32 = 3;
const nativeGpuPlatformCudaExternal :UInt32 = 4;
const nativeGpuPlatformVulkanExternal :UInt32 = 5;

const nativeGpuFallbackCopyToArena :UInt32 = 1;
const nativeGpuFallbackCopyToWebgpu :UInt32 = 2;
const nativeGpuFallbackCpuMaterialize :UInt32 = 3;

const nativeGpuDescriptorTextMaxBytes :UInt32 = 256;
const nativeGpuDescriptorIdMaxBytes :UInt32 = 128;

# Version travels as the nativeGpuDescriptorSchemaVersion const above, not as
# a per-descriptor field: descriptors are constructed in-process and never
# persisted, so a per-instance version byte would be dead weight on a hot path.
struct RuntimeNativeGpuDescriptor @0xdc8535f1bb689c0c {
  id @0 :Text;
  kind @1 :UInt32;
  platform @2 :UInt32;
  byteLength @3 :UInt64;
  width @4 :UInt32;
  height @5 :UInt32;
  format @6 :Text;
  schemaName @7 :Text;
  producer @8 :Text;
  fallback @9 :UInt32;
}
