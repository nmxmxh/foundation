export const NATIVE_GPU_CAPABILITIES_COMMAND = "foundation_native_gpu_capabilities";
export const NATIVE_GPU_ACQUIRE_COMMAND = "foundation_native_gpu_acquire";
export const NATIVE_GPU_RELEASE_COMMAND = "foundation_native_gpu_release";
export const NATIVE_GPU_MATERIALIZE_COMMAND = "foundation_native_gpu_materialize";

export {
  RUNTIME_NATIVE_GPU_FALLBACKS,
  RUNTIME_NATIVE_GPU_KINDS,
  RUNTIME_NATIVE_GPU_PLATFORMS,
  assertRuntimeNativeGpuDescriptor,
  isRuntimeNativeGpuDescriptor,
  toRuntimeNativeGpuContractDescriptor,
  validateRuntimeNativeGpuDescriptor,
  type RuntimeNativeGpuContractDescriptor,
  type RuntimeNativeGpuDescriptor,
  type RuntimeNativeGpuDescriptorValidation,
  type RuntimeNativeGpuFallback,
  type RuntimeNativeGpuKind,
  type RuntimeNativeGpuPlatform,
} from "@ovasabi/runtime-browser";
