package runtimehost

import (
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

type RuntimeRole string

const (
	RuntimeRolePulse   RuntimeRole = "pulse"
	RuntimeRoleCompute RuntimeRole = "compute"
	RuntimeRoleGPU     RuntimeRole = "gpu"
	RuntimeRoleIO      RuntimeRole = "io"
)

type RuntimeUnitDescriptor struct {
	UnitID               string      `json:"unit_id"`
	Role                 RuntimeRole `json:"role"`
	InputSchema          string      `json:"input_schema"`
	OutputSchema         string      `json:"output_schema"`
	SupportsWASM         bool        `json:"supports_wasm"`
	SupportsNative       bool        `json:"supports_native"`
	RequiresSharedMemory bool        `json:"requires_shared_memory"`
	SupportsGPU          bool        `json:"supports_gpu"`
	MaxConcurrency       int         `json:"max_concurrency"`
}

type RuntimeNativeGPUKind string

const (
	RuntimeNativeGPUKindBuffer        RuntimeNativeGPUKind = "native-gpu-buffer"
	RuntimeNativeGPUKindTexture       RuntimeNativeGPUKind = "native-gpu-texture"
	RuntimeNativeGPUKindExternalImage RuntimeNativeGPUKind = "external-image"
)

type RuntimeNativeGPUPlatform string

const (
	RuntimeNativeGPUPlatformLinuxDMABUF     RuntimeNativeGPUPlatform = "linux-dmabuf"
	RuntimeNativeGPUPlatformAppleIOSurface  RuntimeNativeGPUPlatform = "apple-iosurface"
	RuntimeNativeGPUPlatformAndroidHWBuffer RuntimeNativeGPUPlatform = "android-hardware-buffer"
	RuntimeNativeGPUPlatformCUDAExternal    RuntimeNativeGPUPlatform = "cuda-external"
	RuntimeNativeGPUPlatformVulkanExternal  RuntimeNativeGPUPlatform = "vulkan-external"
)

type RuntimeNativeGPUFallback string

const (
	RuntimeNativeGPUFallbackCopyToArena    RuntimeNativeGPUFallback = "copy-to-arena"
	RuntimeNativeGPUFallbackCopyToWebGPU   RuntimeNativeGPUFallback = "copy-to-webgpu"
	RuntimeNativeGPUFallbackCPUMaterialize RuntimeNativeGPUFallback = "cpu-materialize"
)

type RuntimeNativeGPUDescriptor struct {
	ID         string                   `json:"id"`
	Kind       RuntimeNativeGPUKind     `json:"kind"`
	Platform   RuntimeNativeGPUPlatform `json:"platform"`
	ByteLength uint64                   `json:"byte_length,omitempty"`
	Width      uint32                   `json:"width,omitempty"`
	Height     uint32                   `json:"height,omitempty"`
	Format     string                   `json:"format,omitempty"`
	SchemaName string                   `json:"schema_name,omitempty"`
	Producer   string                   `json:"producer"`
	Fallback   RuntimeNativeGPUFallback `json:"fallback"`
}

type RuntimeNativeGPUContractDescriptor struct {
	SchemaVersion uint32 `json:"schema_version"`
	ID            string `json:"id"`
	Kind          uint32 `json:"kind"`
	Platform      uint32 `json:"platform"`
	ByteLength    uint64 `json:"byte_length"`
	Width         uint32 `json:"width"`
	Height        uint32 `json:"height"`
	Format        string `json:"format"`
	SchemaName    string `json:"schema_name"`
	Producer      string `json:"producer"`
	Fallback      uint32 `json:"fallback"`
}

func (d RuntimeUnitDescriptor) Validate() error {
	if d.UnitID == "" {
		return ErrInvalidDescriptor("unit_id is required")
	}
	if d.InputSchema == "" {
		return ErrInvalidDescriptor("input_schema is required")
	}
	if d.OutputSchema == "" {
		return ErrInvalidDescriptor("output_schema is required")
	}
	if d.MaxConcurrency <= 0 {
		return ErrInvalidDescriptor("max_concurrency must be positive")
	}
	return nil
}

func (d RuntimeNativeGPUDescriptor) Validate() error {
	if !validNativeGPUDescriptorID(d.ID) {
		return ErrInvalidDescriptor("native gpu descriptor id must be 1-128 URL-safe identifier bytes")
	}
	if !validShortText(d.Producer) {
		return ErrInvalidDescriptor("native gpu descriptor producer is required")
	}
	if d.SchemaName != "" && !validShortText(d.SchemaName) {
		return ErrInvalidDescriptor("native gpu descriptor schema_name is invalid")
	}
	if d.Format != "" && !validShortText(d.Format) {
		return ErrInvalidDescriptor("native gpu descriptor format is invalid")
	}
	switch d.Kind {
	case RuntimeNativeGPUKindBuffer:
		if d.ByteLength == 0 {
			return ErrInvalidDescriptor("native gpu buffer descriptors require byte_length")
		}
	case RuntimeNativeGPUKindTexture, RuntimeNativeGPUKindExternalImage:
		if d.Width == 0 || d.Height == 0 || strings.TrimSpace(d.Format) == "" {
			return ErrInvalidDescriptor("native gpu image descriptors require width, height, and format")
		}
	default:
		return ErrInvalidDescriptor("native gpu descriptor kind is unsupported")
	}
	switch d.Platform {
	case RuntimeNativeGPUPlatformLinuxDMABUF,
		RuntimeNativeGPUPlatformAppleIOSurface,
		RuntimeNativeGPUPlatformAndroidHWBuffer,
		RuntimeNativeGPUPlatformCUDAExternal,
		RuntimeNativeGPUPlatformVulkanExternal:
	default:
		return ErrInvalidDescriptor("native gpu descriptor platform is unsupported")
	}
	switch d.Fallback {
	case RuntimeNativeGPUFallbackCopyToArena,
		RuntimeNativeGPUFallbackCopyToWebGPU,
		RuntimeNativeGPUFallbackCPUMaterialize:
	default:
		return ErrInvalidDescriptor("native gpu descriptor fallback is unsupported")
	}
	return nil
}

func (d RuntimeNativeGPUDescriptor) ContractDescriptor() (RuntimeNativeGPUContractDescriptor, error) {
	if err := d.Validate(); err != nil {
		return RuntimeNativeGPUContractDescriptor{}, err
	}
	kind, ok := d.Kind.ContractCode()
	if !ok {
		return RuntimeNativeGPUContractDescriptor{}, ErrInvalidDescriptor("native gpu descriptor kind is unsupported")
	}
	platform, ok := d.Platform.ContractCode()
	if !ok {
		return RuntimeNativeGPUContractDescriptor{}, ErrInvalidDescriptor("native gpu descriptor platform is unsupported")
	}
	fallback, ok := d.Fallback.ContractCode()
	if !ok {
		return RuntimeNativeGPUContractDescriptor{}, ErrInvalidDescriptor("native gpu descriptor fallback is unsupported")
	}
	return RuntimeNativeGPUContractDescriptor{
		SchemaVersion: generated.NATIVE_GPU_DESCRIPTOR_SCHEMA_VERSION,
		ID:            d.ID,
		Kind:          kind,
		Platform:      platform,
		ByteLength:    d.ByteLength,
		Width:         d.Width,
		Height:        d.Height,
		Format:        d.Format,
		SchemaName:    d.SchemaName,
		Producer:      d.Producer,
		Fallback:      fallback,
	}, nil
}

func (k RuntimeNativeGPUKind) ContractCode() (uint32, bool) {
	switch k {
	case RuntimeNativeGPUKindBuffer:
		return generated.NATIVE_GPU_KIND_BUFFER, true
	case RuntimeNativeGPUKindTexture:
		return generated.NATIVE_GPU_KIND_TEXTURE, true
	case RuntimeNativeGPUKindExternalImage:
		return generated.NATIVE_GPU_KIND_EXTERNAL_IMAGE, true
	default:
		return 0, false
	}
}

func (p RuntimeNativeGPUPlatform) ContractCode() (uint32, bool) {
	switch p {
	case RuntimeNativeGPUPlatformLinuxDMABUF:
		return generated.NATIVE_GPU_PLATFORM_LINUX_DMABUF, true
	case RuntimeNativeGPUPlatformAppleIOSurface:
		return generated.NATIVE_GPU_PLATFORM_APPLE_IOSURFACE, true
	case RuntimeNativeGPUPlatformAndroidHWBuffer:
		return generated.NATIVE_GPU_PLATFORM_ANDROID_HARDWARE_BUFFER, true
	case RuntimeNativeGPUPlatformCUDAExternal:
		return generated.NATIVE_GPU_PLATFORM_CUDA_EXTERNAL, true
	case RuntimeNativeGPUPlatformVulkanExternal:
		return generated.NATIVE_GPU_PLATFORM_VULKAN_EXTERNAL, true
	default:
		return 0, false
	}
}

func (f RuntimeNativeGPUFallback) ContractCode() (uint32, bool) {
	switch f {
	case RuntimeNativeGPUFallbackCopyToArena:
		return generated.NATIVE_GPU_FALLBACK_COPY_TO_ARENA, true
	case RuntimeNativeGPUFallbackCopyToWebGPU:
		return generated.NATIVE_GPU_FALLBACK_COPY_TO_WEBGPU, true
	case RuntimeNativeGPUFallbackCPUMaterialize:
		return generated.NATIVE_GPU_FALLBACK_CPU_MATERIALIZE, true
	default:
		return 0, false
	}
}

func validNativeGPUDescriptorID(value string) bool {
	if len(value) == 0 || len(value) > int(generated.NATIVE_GPU_DESCRIPTOR_ID_MAX_BYTES) {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		if r == '.' || r == '_' || r == ':' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func validShortText(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && len(trimmed) <= int(generated.NATIVE_GPU_DESCRIPTOR_TEXT_MAX_BYTES)
}
