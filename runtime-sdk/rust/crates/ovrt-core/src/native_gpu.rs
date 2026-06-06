#![forbid(unsafe_code)]

use crate::generated::{
    NATIVE_GPU_DESCRIPTOR_ID_MAX_BYTES, NATIVE_GPU_DESCRIPTOR_SCHEMA_VERSION,
    NATIVE_GPU_DESCRIPTOR_TEXT_MAX_BYTES, NATIVE_GPU_FALLBACK_COPY_TO_ARENA,
    NATIVE_GPU_FALLBACK_COPY_TO_WEBGPU, NATIVE_GPU_FALLBACK_CPU_MATERIALIZE,
    NATIVE_GPU_KIND_BUFFER, NATIVE_GPU_KIND_EXTERNAL_IMAGE, NATIVE_GPU_KIND_TEXTURE,
    NATIVE_GPU_PLATFORM_ANDROID_HARDWARE_BUFFER, NATIVE_GPU_PLATFORM_APPLE_IOSURFACE,
    NATIVE_GPU_PLATFORM_CUDA_EXTERNAL, NATIVE_GPU_PLATFORM_LINUX_DMABUF,
    NATIVE_GPU_PLATFORM_VULKAN_EXTERNAL,
};

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum RuntimeNativeGpuKind {
    Buffer,
    Texture,
    ExternalImage,
}

impl RuntimeNativeGpuKind {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::Buffer => "native-gpu-buffer",
            Self::Texture => "native-gpu-texture",
            Self::ExternalImage => "external-image",
        }
    }

    pub const fn contract_code(self) -> u32 {
        match self {
            Self::Buffer => NATIVE_GPU_KIND_BUFFER,
            Self::Texture => NATIVE_GPU_KIND_TEXTURE,
            Self::ExternalImage => NATIVE_GPU_KIND_EXTERNAL_IMAGE,
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum RuntimeNativeGpuPlatform {
    LinuxDmabuf,
    AppleIosurface,
    AndroidHardwareBuffer,
    CudaExternal,
    VulkanExternal,
}

impl RuntimeNativeGpuPlatform {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::LinuxDmabuf => "linux-dmabuf",
            Self::AppleIosurface => "apple-iosurface",
            Self::AndroidHardwareBuffer => "android-hardware-buffer",
            Self::CudaExternal => "cuda-external",
            Self::VulkanExternal => "vulkan-external",
        }
    }

    pub const fn contract_code(self) -> u32 {
        match self {
            Self::LinuxDmabuf => NATIVE_GPU_PLATFORM_LINUX_DMABUF,
            Self::AppleIosurface => NATIVE_GPU_PLATFORM_APPLE_IOSURFACE,
            Self::AndroidHardwareBuffer => NATIVE_GPU_PLATFORM_ANDROID_HARDWARE_BUFFER,
            Self::CudaExternal => NATIVE_GPU_PLATFORM_CUDA_EXTERNAL,
            Self::VulkanExternal => NATIVE_GPU_PLATFORM_VULKAN_EXTERNAL,
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum RuntimeNativeGpuFallback {
    CopyToArena,
    CopyToWebGpu,
    CpuMaterialize,
}

impl RuntimeNativeGpuFallback {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::CopyToArena => "copy-to-arena",
            Self::CopyToWebGpu => "copy-to-webgpu",
            Self::CpuMaterialize => "cpu-materialize",
        }
    }

    pub const fn contract_code(self) -> u32 {
        match self {
            Self::CopyToArena => NATIVE_GPU_FALLBACK_COPY_TO_ARENA,
            Self::CopyToWebGpu => NATIVE_GPU_FALLBACK_COPY_TO_WEBGPU,
            Self::CpuMaterialize => NATIVE_GPU_FALLBACK_CPU_MATERIALIZE,
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct RuntimeNativeGpuDescriptor {
    pub id: String,
    pub kind: RuntimeNativeGpuKind,
    pub platform: RuntimeNativeGpuPlatform,
    pub byte_length: Option<u64>,
    pub width: Option<u32>,
    pub height: Option<u32>,
    pub format: Option<String>,
    pub schema_name: Option<String>,
    pub producer: String,
    pub fallback: RuntimeNativeGpuFallback,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct RuntimeNativeGpuContractDescriptor<'a> {
    pub schema_version: u32,
    pub id: &'a str,
    pub kind: u32,
    pub platform: u32,
    pub byte_length: u64,
    pub width: u32,
    pub height: u32,
    pub format: &'a str,
    pub schema_name: &'a str,
    pub producer: &'a str,
    pub fallback: u32,
}

impl RuntimeNativeGpuDescriptor {
    pub fn validate(&self) -> Result<(), String> {
        if !valid_descriptor_id(&self.id) {
            return Err(
                "native GPU descriptor id must be 1-128 URL-safe identifier bytes".to_string()
            );
        }
        if !valid_short_text(&self.producer) {
            return Err("native GPU descriptor producer is required".to_string());
        }
        if let Some(schema_name) = &self.schema_name {
            if !valid_short_text(schema_name) {
                return Err("native GPU descriptor schema_name is invalid".to_string());
            }
        }
        if let Some(format) = &self.format {
            if !valid_short_text(format) {
                return Err("native GPU descriptor format is invalid".to_string());
            }
        }
        match self.kind {
            RuntimeNativeGpuKind::Buffer => {
                if self.byte_length.unwrap_or(0) == 0 {
                    return Err("native GPU buffer descriptors require byte_length".to_string());
                }
            }
            RuntimeNativeGpuKind::Texture | RuntimeNativeGpuKind::ExternalImage => {
                if self.width.unwrap_or(0) == 0
                    || self.height.unwrap_or(0) == 0
                    || self.format.as_deref().unwrap_or("").trim().is_empty()
                {
                    return Err("native GPU image descriptors require width, height, and format"
                        .to_string());
                }
            }
        }
        Ok(())
    }

    pub fn contract_descriptor(&self) -> Result<RuntimeNativeGpuContractDescriptor<'_>, String> {
        self.validate()?;
        Ok(RuntimeNativeGpuContractDescriptor {
            schema_version: NATIVE_GPU_DESCRIPTOR_SCHEMA_VERSION,
            id: self.id.as_str(),
            kind: self.kind.contract_code(),
            platform: self.platform.contract_code(),
            byte_length: self.byte_length.unwrap_or(0),
            width: self.width.unwrap_or(0),
            height: self.height.unwrap_or(0),
            format: self.format.as_deref().unwrap_or_default(),
            schema_name: self.schema_name.as_deref().unwrap_or_default(),
            producer: self.producer.as_str(),
            fallback: self.fallback.contract_code(),
        })
    }
}

fn valid_descriptor_id(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= NATIVE_GPU_DESCRIPTOR_ID_MAX_BYTES as usize
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b':' | b'-'))
}

fn valid_short_text(value: &str) -> bool {
    let trimmed = value.trim();
    !trimmed.is_empty() && trimmed.len() <= NATIVE_GPU_DESCRIPTOR_TEXT_MAX_BYTES as usize
}

#[cfg(test)]
mod tests {
    use super::*;

    fn texture_descriptor() -> RuntimeNativeGpuDescriptor {
        RuntimeNativeGpuDescriptor {
            id: "camera.frame.42".to_string(),
            kind: RuntimeNativeGpuKind::Texture,
            platform: RuntimeNativeGpuPlatform::AppleIosurface,
            byte_length: None,
            width: Some(1920),
            height: Some(1080),
            format: Some("bgra8".to_string()),
            schema_name: Some("media/v1/frame.capnp".to_string()),
            producer: "camera.plugin".to_string(),
            fallback: RuntimeNativeGpuFallback::CopyToWebGpu,
        }
    }

    #[test]
    fn validates_native_gpu_texture_descriptors() {
        let descriptor = texture_descriptor();

        assert!(descriptor.validate().is_ok());
        assert_eq!(RuntimeNativeGpuKind::Texture.as_str(), "native-gpu-texture");
        assert_eq!(RuntimeNativeGpuPlatform::AppleIosurface.as_str(), "apple-iosurface");
        assert_eq!(RuntimeNativeGpuFallback::CopyToWebGpu.as_str(), "copy-to-webgpu");
        assert_eq!(
            descriptor.contract_descriptor().expect("contract descriptor"),
            RuntimeNativeGpuContractDescriptor {
                schema_version: NATIVE_GPU_DESCRIPTOR_SCHEMA_VERSION,
                id: "camera.frame.42",
                kind: NATIVE_GPU_KIND_TEXTURE,
                platform: NATIVE_GPU_PLATFORM_APPLE_IOSURFACE,
                byte_length: 0,
                width: 1920,
                height: 1080,
                format: "bgra8",
                schema_name: "media/v1/frame.capnp",
                producer: "camera.plugin",
                fallback: NATIVE_GPU_FALLBACK_COPY_TO_WEBGPU,
            }
        );
    }

    #[test]
    fn maps_all_native_gpu_enums_to_contract_codes() {
        assert_eq!(RuntimeNativeGpuKind::Buffer.contract_code(), NATIVE_GPU_KIND_BUFFER);
        assert_eq!(RuntimeNativeGpuKind::Texture.contract_code(), NATIVE_GPU_KIND_TEXTURE);
        assert_eq!(
            RuntimeNativeGpuKind::ExternalImage.contract_code(),
            NATIVE_GPU_KIND_EXTERNAL_IMAGE
        );

        assert_eq!(
            RuntimeNativeGpuPlatform::LinuxDmabuf.contract_code(),
            NATIVE_GPU_PLATFORM_LINUX_DMABUF
        );
        assert_eq!(
            RuntimeNativeGpuPlatform::AppleIosurface.contract_code(),
            NATIVE_GPU_PLATFORM_APPLE_IOSURFACE
        );
        assert_eq!(
            RuntimeNativeGpuPlatform::AndroidHardwareBuffer.contract_code(),
            NATIVE_GPU_PLATFORM_ANDROID_HARDWARE_BUFFER
        );
        assert_eq!(
            RuntimeNativeGpuPlatform::CudaExternal.contract_code(),
            NATIVE_GPU_PLATFORM_CUDA_EXTERNAL
        );
        assert_eq!(
            RuntimeNativeGpuPlatform::VulkanExternal.contract_code(),
            NATIVE_GPU_PLATFORM_VULKAN_EXTERNAL
        );

        assert_eq!(
            RuntimeNativeGpuFallback::CopyToArena.contract_code(),
            NATIVE_GPU_FALLBACK_COPY_TO_ARENA
        );
        assert_eq!(
            RuntimeNativeGpuFallback::CopyToWebGpu.contract_code(),
            NATIVE_GPU_FALLBACK_COPY_TO_WEBGPU
        );
        assert_eq!(
            RuntimeNativeGpuFallback::CpuMaterialize.contract_code(),
            NATIVE_GPU_FALLBACK_CPU_MATERIALIZE
        );
    }

    #[test]
    fn checks_native_gpu_descriptor_boundaries() {
        let descriptor = RuntimeNativeGpuDescriptor {
            id: "a".repeat(NATIVE_GPU_DESCRIPTOR_ID_MAX_BYTES as usize),
            kind: RuntimeNativeGpuKind::Buffer,
            platform: RuntimeNativeGpuPlatform::LinuxDmabuf,
            byte_length: Some(1),
            width: None,
            height: None,
            format: None,
            schema_name: None,
            producer: "p".repeat(NATIVE_GPU_DESCRIPTOR_TEXT_MAX_BYTES as usize),
            fallback: RuntimeNativeGpuFallback::CopyToArena,
        };
        assert!(descriptor.validate().is_ok());

        let mut invalid = descriptor.clone();
        invalid.id.push('x');
        assert!(invalid.validate().expect_err("id must fail").contains("id"));

        let mut invalid = descriptor;
        invalid.producer.push('x');
        assert!(invalid.validate().expect_err("producer must fail").contains("producer"));
    }

    #[test]
    fn validates_native_gpu_buffer_descriptors() {
        let descriptor = RuntimeNativeGpuDescriptor {
            id: "pipeline.buffer.7".to_string(),
            kind: RuntimeNativeGpuKind::Buffer,
            platform: RuntimeNativeGpuPlatform::VulkanExternal,
            byte_length: Some(4096),
            width: None,
            height: None,
            format: None,
            schema_name: Some("features/v1/batch.capnp".to_string()),
            producer: "native.pipeline".to_string(),
            fallback: RuntimeNativeGpuFallback::CopyToArena,
        };

        assert!(descriptor.validate().is_ok());
    }

    #[test]
    fn rejects_incomplete_native_gpu_descriptors() {
        let mut descriptor = texture_descriptor();
        descriptor.format = None;
        assert!(descriptor.validate().expect_err("format is required").contains("image"));

        let mut descriptor = texture_descriptor();
        descriptor.id = "../camera".to_string();
        assert!(descriptor.validate().expect_err("id is invalid").contains("id"));

        let descriptor = RuntimeNativeGpuDescriptor {
            id: "pipeline.buffer.8".to_string(),
            kind: RuntimeNativeGpuKind::Buffer,
            platform: RuntimeNativeGpuPlatform::CudaExternal,
            byte_length: None,
            width: None,
            height: None,
            format: None,
            schema_name: None,
            producer: "native.pipeline".to_string(),
            fallback: RuntimeNativeGpuFallback::CpuMaterialize,
        };
        assert!(descriptor
            .validate()
            .expect_err("byte_length is required")
            .contains("byte_length"));
    }
}
