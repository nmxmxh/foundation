use std::collections::BTreeMap;
#[cfg(unix)]
use std::os::fd::OwnedFd;
use std::sync::RwLock;

use ovrt_core::{RuntimeNativeGpuDescriptor, RuntimeNativeGpuFallback, RuntimeNativeGpuPlatform};

use crate::{NativeErrorCode, NativeRuntimeError};

const DEFAULT_MAX_NATIVE_GPU_RECORDS: usize = 4096;
const OWNER_SCOPE_TEXT_MAX_BYTES: usize = 128;

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct NativeGpuOwnerScope {
    tenant_id: String,
    producer: String,
    device_id: String,
}

impl NativeGpuOwnerScope {
    pub fn new(
        tenant_id: impl Into<String>,
        producer: impl Into<String>,
        device_id: impl Into<String>,
    ) -> Result<Self, NativeRuntimeError> {
        let scope = Self {
            tenant_id: tenant_id.into(),
            producer: producer.into(),
            device_id: device_id.into(),
        };
        scope.validate()?;
        Ok(scope)
    }

    pub fn tenant_id(&self) -> &str {
        &self.tenant_id
    }

    pub fn producer(&self) -> &str {
        &self.producer
    }

    pub fn device_id(&self) -> &str {
        &self.device_id
    }

    fn validate(&self) -> Result<(), NativeRuntimeError> {
        validate_scope_text("tenant_id", &self.tenant_id)?;
        validate_scope_text("producer", &self.producer)?;
        validate_scope_text("device_id", &self.device_id)?;
        Ok(())
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct NativeGpuFenceSnapshot {
    pub fence_id: u64,
    pub required_value: u64,
    pub signaled_value: u64,
    pub complete: bool,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct NativeGpuRegistrySnapshot {
    pub descriptor: RuntimeNativeGpuDescriptor,
    pub owner_scope: NativeGpuOwnerScope,
    pub ref_count: u32,
    pub fence: NativeGpuFenceSnapshot,
    pub handle: NativeGpuPlatformHandleSnapshot,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct NativeGpuPlatformHandleSnapshot {
    pub kind: NativeGpuPlatformHandleKind,
    pub platform: RuntimeNativeGpuPlatform,
    pub plane_count: u8,
    pub has_external_sync: bool,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum NativeGpuPlatformHandleKind {
    Stub,
    UnixFd,
    PluginOpaque,
}

#[cfg(unix)]
pub struct NativeGpuUnixFdHandle {
    pub fd: OwnedFd,
    pub sync_file: Option<OwnedFd>,
    pub plane_count: u8,
    pub modifier: Option<u64>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct NativeGpuOpaquePluginHandle {
    token: String,
    platform: RuntimeNativeGpuPlatform,
    has_external_sync: bool,
}

impl NativeGpuOpaquePluginHandle {
    pub fn new(
        token: impl Into<String>,
        platform: RuntimeNativeGpuPlatform,
        has_external_sync: bool,
    ) -> Result<Self, NativeRuntimeError> {
        let handle = Self { token: token.into(), platform, has_external_sync };
        validate_scope_text("opaque_token", &handle.token)?;
        Ok(handle)
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct NativeGpuMaterializationPlan {
    pub descriptor: RuntimeNativeGpuDescriptor,
    pub fallback: RuntimeNativeGpuFallback,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct NativeGpuReleaseResult {
    pub descriptor_id: String,
    pub remaining_refs: u32,
    pub removed: bool,
}

struct NativeGpuHandleRecord {
    descriptor: RuntimeNativeGpuDescriptor,
    platform_handle: PlatformHandle,
    fence: NativeFence,
    owner_scope: NativeGpuOwnerScope,
    ref_count: u32,
}

enum PlatformHandle {
    Stub {
        _token: u64,
        platform: RuntimeNativeGpuPlatform,
    },
    #[cfg(unix)]
    UnixFd {
        _fd: OwnedFd,
        _sync_file: Option<OwnedFd>,
        platform: RuntimeNativeGpuPlatform,
        plane_count: u8,
        _modifier: Option<u64>,
    },
    PluginOpaque {
        _token: String,
        platform: RuntimeNativeGpuPlatform,
        has_external_sync: bool,
    },
}

#[derive(Clone, Debug, PartialEq, Eq)]
struct NativeFence {
    id: u64,
    required_value: u64,
    signaled_value: u64,
}

impl NativeFence {
    fn new(id: u64, required_value: u64) -> Self {
        Self { id, required_value, signaled_value: 0 }
    }

    fn signal(&mut self, value: u64) {
        self.signaled_value = self.signaled_value.max(value);
    }

    fn is_complete(&self) -> bool {
        self.signaled_value >= self.required_value
    }

    fn snapshot(&self) -> NativeGpuFenceSnapshot {
        NativeGpuFenceSnapshot {
            fence_id: self.id,
            required_value: self.required_value,
            signaled_value: self.signaled_value,
            complete: self.is_complete(),
        }
    }
}

pub struct NativeGpuHandleRegistry {
    records: RwLock<BTreeMap<String, NativeGpuHandleRecord>>,
    max_records: usize,
}

impl Default for NativeGpuHandleRegistry {
    fn default() -> Self {
        Self::new(DEFAULT_MAX_NATIVE_GPU_RECORDS)
    }
}

impl NativeGpuHandleRegistry {
    pub fn new(max_records: usize) -> Self {
        Self { records: RwLock::new(BTreeMap::new()), max_records }
    }

    pub fn register_stub(
        &self,
        descriptor: RuntimeNativeGpuDescriptor,
        owner_scope: NativeGpuOwnerScope,
        fence_id: u64,
        required_fence_value: u64,
    ) -> Result<RuntimeNativeGpuDescriptor, NativeRuntimeError> {
        let platform = descriptor.platform;
        let token = stable_descriptor_token(&descriptor.id);
        self.register_private(
            descriptor,
            PlatformHandle::Stub { _token: token, platform },
            NativeFence::new(fence_id, required_fence_value),
            owner_scope,
        )
    }

    #[cfg(unix)]
    pub fn register_unix_fd(
        &self,
        descriptor: RuntimeNativeGpuDescriptor,
        owner_scope: NativeGpuOwnerScope,
        handle: NativeGpuUnixFdHandle,
        fence_id: u64,
        required_fence_value: u64,
    ) -> Result<RuntimeNativeGpuDescriptor, NativeRuntimeError> {
        validate_plane_count(handle.plane_count)?;
        let platform = descriptor.platform;
        self.register_private(
            descriptor,
            PlatformHandle::UnixFd {
                _fd: handle.fd,
                _sync_file: handle.sync_file,
                platform,
                plane_count: handle.plane_count,
                _modifier: handle.modifier,
            },
            NativeFence::new(fence_id, required_fence_value),
            owner_scope,
        )
    }

    pub fn register_plugin_opaque(
        &self,
        descriptor: RuntimeNativeGpuDescriptor,
        owner_scope: NativeGpuOwnerScope,
        handle: NativeGpuOpaquePluginHandle,
        fence_id: u64,
        required_fence_value: u64,
    ) -> Result<RuntimeNativeGpuDescriptor, NativeRuntimeError> {
        self.register_private(
            descriptor,
            PlatformHandle::PluginOpaque {
                _token: handle.token,
                platform: handle.platform,
                has_external_sync: handle.has_external_sync,
            },
            NativeFence::new(fence_id, required_fence_value),
            owner_scope,
        )
    }

    pub fn acquire(
        &self,
        descriptor_id: &str,
        owner_scope: &NativeGpuOwnerScope,
    ) -> Result<RuntimeNativeGpuDescriptor, NativeRuntimeError> {
        owner_scope.validate()?;
        let mut guard = self
            .records
            .write()
            .map_err(|_| gpu_unavailable("native GPU registry lock poisoned"))?;
        let record = guard.get_mut(descriptor_id).ok_or_else(|| {
            gpu_unavailable(format!("native GPU descriptor {descriptor_id} is unavailable"))
        })?;
        assert_owner(record, owner_scope)?;
        record.ref_count = record
            .ref_count
            .checked_add(1)
            .ok_or_else(|| gpu_busy("native GPU descriptor ref_count overflow"))?;
        Ok(record.descriptor.clone())
    }

    pub fn release(
        &self,
        descriptor_id: &str,
        owner_scope: &NativeGpuOwnerScope,
    ) -> Result<NativeGpuReleaseResult, NativeRuntimeError> {
        owner_scope.validate()?;
        let mut guard = self
            .records
            .write()
            .map_err(|_| gpu_unavailable("native GPU registry lock poisoned"))?;
        let record = guard.get_mut(descriptor_id).ok_or_else(|| {
            gpu_unavailable(format!("native GPU descriptor {descriptor_id} is unavailable"))
        })?;
        assert_owner(record, owner_scope)?;

        if record.ref_count > 1 {
            record.ref_count -= 1;
            return Ok(NativeGpuReleaseResult {
                descriptor_id: descriptor_id.to_string(),
                remaining_refs: record.ref_count,
                removed: false,
            });
        }
        if !record.fence.is_complete() {
            return Err(gpu_busy("native GPU descriptor fence is not complete"));
        }
        guard.remove(descriptor_id);
        Ok(NativeGpuReleaseResult {
            descriptor_id: descriptor_id.to_string(),
            remaining_refs: 0,
            removed: true,
        })
    }

    pub fn signal_fence(
        &self,
        descriptor_id: &str,
        signaled_value: u64,
    ) -> Result<NativeGpuFenceSnapshot, NativeRuntimeError> {
        let mut guard = self
            .records
            .write()
            .map_err(|_| gpu_unavailable("native GPU registry lock poisoned"))?;
        let record = guard.get_mut(descriptor_id).ok_or_else(|| {
            gpu_unavailable(format!("native GPU descriptor {descriptor_id} is unavailable"))
        })?;
        record.fence.signal(signaled_value);
        Ok(record.fence.snapshot())
    }

    pub fn materialization_plan(
        &self,
        descriptor_id: &str,
        owner_scope: &NativeGpuOwnerScope,
    ) -> Result<NativeGpuMaterializationPlan, NativeRuntimeError> {
        owner_scope.validate()?;
        let guard = self
            .records
            .read()
            .map_err(|_| gpu_unavailable("native GPU registry lock poisoned"))?;
        let record = guard.get(descriptor_id).ok_or_else(|| {
            gpu_unavailable(format!("native GPU descriptor {descriptor_id} is unavailable"))
        })?;
        assert_owner(record, owner_scope)?;
        Ok(NativeGpuMaterializationPlan {
            descriptor: record.descriptor.clone(),
            fallback: record.descriptor.fallback,
        })
    }

    pub fn snapshot(
        &self,
        descriptor_id: &str,
        owner_scope: &NativeGpuOwnerScope,
    ) -> Result<NativeGpuRegistrySnapshot, NativeRuntimeError> {
        owner_scope.validate()?;
        let guard = self
            .records
            .read()
            .map_err(|_| gpu_unavailable("native GPU registry lock poisoned"))?;
        let record = guard.get(descriptor_id).ok_or_else(|| {
            gpu_unavailable(format!("native GPU descriptor {descriptor_id} is unavailable"))
        })?;
        assert_owner(record, owner_scope)?;
        Ok(NativeGpuRegistrySnapshot {
            descriptor: record.descriptor.clone(),
            owner_scope: record.owner_scope.clone(),
            ref_count: record.ref_count,
            fence: record.fence.snapshot(),
            handle: record.platform_handle.snapshot(),
        })
    }

    pub fn len(&self) -> Result<usize, NativeRuntimeError> {
        let guard = self
            .records
            .read()
            .map_err(|_| gpu_unavailable("native GPU registry lock poisoned"))?;
        Ok(guard.len())
    }

    pub fn is_empty(&self) -> Result<bool, NativeRuntimeError> {
        let guard = self
            .records
            .read()
            .map_err(|_| gpu_unavailable("native GPU registry lock poisoned"))?;
        Ok(guard.is_empty())
    }

    fn register_private(
        &self,
        descriptor: RuntimeNativeGpuDescriptor,
        platform_handle: PlatformHandle,
        fence: NativeFence,
        owner_scope: NativeGpuOwnerScope,
    ) -> Result<RuntimeNativeGpuDescriptor, NativeRuntimeError> {
        descriptor
            .validate()
            .map_err(|error| NativeRuntimeError::new(NativeErrorCode::MalformedFrame, error))?;
        owner_scope.validate()?;
        if !platform_matches_descriptor(&platform_handle, descriptor.platform) {
            return Err(gpu_unavailable(
                "native GPU platform handle does not match descriptor platform",
            ));
        }
        let mut guard = self
            .records
            .write()
            .map_err(|_| gpu_unavailable("native GPU registry lock poisoned"))?;
        if guard.len() >= self.max_records {
            return Err(gpu_busy("native GPU registry record limit reached"));
        }
        if guard.contains_key(&descriptor.id) {
            return Err(gpu_busy(format!(
                "native GPU descriptor {} is already registered",
                descriptor.id
            )));
        }
        let public_descriptor = descriptor.clone();
        guard.insert(
            descriptor.id.clone(),
            NativeGpuHandleRecord { descriptor, platform_handle, fence, owner_scope, ref_count: 1 },
        );
        Ok(public_descriptor)
    }
}

impl PlatformHandle {
    fn platform(&self) -> RuntimeNativeGpuPlatform {
        match self {
            PlatformHandle::Stub { platform, .. }
            | PlatformHandle::PluginOpaque { platform, .. } => *platform,
            #[cfg(unix)]
            PlatformHandle::UnixFd { platform, .. } => *platform,
        }
    }

    fn snapshot(&self) -> NativeGpuPlatformHandleSnapshot {
        match self {
            PlatformHandle::Stub { platform, .. } => NativeGpuPlatformHandleSnapshot {
                kind: NativeGpuPlatformHandleKind::Stub,
                platform: *platform,
                plane_count: 0,
                has_external_sync: false,
            },
            #[cfg(unix)]
            PlatformHandle::UnixFd { platform, plane_count, _sync_file, .. } => {
                NativeGpuPlatformHandleSnapshot {
                    kind: NativeGpuPlatformHandleKind::UnixFd,
                    platform: *platform,
                    plane_count: *plane_count,
                    has_external_sync: _sync_file.is_some(),
                }
            }
            PlatformHandle::PluginOpaque { platform, has_external_sync, .. } => {
                NativeGpuPlatformHandleSnapshot {
                    kind: NativeGpuPlatformHandleKind::PluginOpaque,
                    platform: *platform,
                    plane_count: 0,
                    has_external_sync: *has_external_sync,
                }
            }
        }
    }
}

fn assert_owner(
    record: &NativeGpuHandleRecord,
    owner_scope: &NativeGpuOwnerScope,
) -> Result<(), NativeRuntimeError> {
    if &record.owner_scope != owner_scope {
        return Err(NativeRuntimeError::new(
            NativeErrorCode::NativeGpuUnauthorized,
            "native GPU descriptor owner scope mismatch",
        ));
    }
    Ok(())
}

fn platform_matches_descriptor(
    handle: &PlatformHandle,
    platform: RuntimeNativeGpuPlatform,
) -> bool {
    handle.platform() == platform
}

fn validate_scope_text(name: &str, value: &str) -> Result<(), NativeRuntimeError> {
    if value.is_empty() || value.len() > OWNER_SCOPE_TEXT_MAX_BYTES {
        return Err(NativeRuntimeError::new(
            NativeErrorCode::MalformedFrame,
            format!("native GPU owner scope {name} is invalid"),
        ));
    }
    Ok(())
}

fn validate_plane_count(plane_count: u8) -> Result<(), NativeRuntimeError> {
    if (1..=4).contains(&plane_count) {
        return Ok(());
    }
    Err(NativeRuntimeError::new(
        NativeErrorCode::MalformedFrame,
        "native GPU fd handle plane_count must be between 1 and 4",
    ))
}

fn stable_descriptor_token(value: &str) -> u64 {
    let mut hash = 0xcbf2_9ce4_8422_2325_u64;
    for byte in value.bytes() {
        hash ^= u64::from(byte);
        hash = hash.wrapping_mul(0x0000_0100_0000_01b3);
    }
    hash
}

fn gpu_unavailable(message: impl Into<String>) -> NativeRuntimeError {
    NativeRuntimeError::new(NativeErrorCode::NativeGpuUnavailable, message)
}

fn gpu_busy(message: impl Into<String>) -> NativeRuntimeError {
    NativeRuntimeError::new(NativeErrorCode::NativeGpuBusy, message)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[cfg(unix)]
    use std::fs::File;
    #[cfg(unix)]
    use std::os::fd::OwnedFd;

    use ovrt_core::{
        RuntimeNativeGpuDescriptor, RuntimeNativeGpuFallback, RuntimeNativeGpuKind,
        RuntimeNativeGpuPlatform,
    };

    fn owner() -> NativeGpuOwnerScope {
        NativeGpuOwnerScope::new("tenant-1", "camera-plugin", "gpu-0").expect("owner")
    }

    fn descriptor() -> RuntimeNativeGpuDescriptor {
        RuntimeNativeGpuDescriptor {
            id: "frame-1".to_string(),
            kind: RuntimeNativeGpuKind::Texture,
            platform: RuntimeNativeGpuPlatform::AppleIosurface,
            byte_length: None,
            width: Some(1920),
            height: Some(1080),
            format: Some("rgba8unorm".to_string()),
            schema_name: Some("media.CameraFrame.v1".to_string()),
            producer: "camera-plugin".to_string(),
            fallback: RuntimeNativeGpuFallback::CopyToWebGpu,
        }
    }

    #[test]
    fn registers_descriptor_without_exposing_platform_handle() {
        let registry = NativeGpuHandleRegistry::new(4);
        let public = registry.register_stub(descriptor(), owner(), 7, 1).expect("register");

        assert_eq!(public.id, "frame-1");
        assert_eq!(registry.len().expect("len"), 1);
        let snapshot = registry.snapshot("frame-1", &owner()).expect("snapshot");
        assert_eq!(snapshot.ref_count, 1);
        assert_eq!(snapshot.fence.fence_id, 7);
        assert!(!snapshot.fence.complete);
        assert_eq!(snapshot.handle.kind, NativeGpuPlatformHandleKind::Stub);
    }

    #[test]
    fn acquire_and_release_preserve_owner_scope_and_ref_counts() {
        let registry = NativeGpuHandleRegistry::new(4);
        registry.register_stub(descriptor(), owner(), 1, 0).expect("register");

        let acquired = registry.acquire("frame-1", &owner()).expect("acquire");
        assert_eq!(acquired.id, "frame-1");
        assert_eq!(registry.snapshot("frame-1", &owner()).expect("snapshot").ref_count, 2);

        let first_release = registry.release("frame-1", &owner()).expect("release");
        assert_eq!(first_release.remaining_refs, 1);
        assert!(!first_release.removed);
        let second_release = registry.release("frame-1", &owner()).expect("release");
        assert!(second_release.removed);
        assert_eq!(registry.len().expect("len"), 0);
    }

    #[test]
    fn rejects_owner_scope_mismatch() {
        let registry = NativeGpuHandleRegistry::new(4);
        registry.register_stub(descriptor(), owner(), 1, 0).expect("register");
        let other = NativeGpuOwnerScope::new("tenant-2", "camera-plugin", "gpu-0").expect("owner");

        let error = registry.acquire("frame-1", &other).expect_err("owner mismatch");
        assert_eq!(error.code, NativeErrorCode::NativeGpuUnauthorized);
    }

    #[test]
    fn pending_fence_blocks_final_release_until_signaled() {
        let registry = NativeGpuHandleRegistry::new(4);
        registry.register_stub(descriptor(), owner(), 42, 3).expect("register");

        let blocked = registry.release("frame-1", &owner()).expect_err("pending");
        assert_eq!(blocked.code, NativeErrorCode::NativeGpuBusy);
        assert_eq!(registry.len().expect("len"), 1);

        let fence = registry.signal_fence("frame-1", 3).expect("signal");
        assert!(fence.complete);
        assert!(registry.release("frame-1", &owner()).expect("release").removed);
    }

    #[test]
    fn record_limit_is_enforced() {
        let registry = NativeGpuHandleRegistry::new(1);
        registry.register_stub(descriptor(), owner(), 1, 0).expect("register");
        let mut second = descriptor();
        second.id = "frame-2".to_string();

        let error = registry.register_stub(second, owner(), 2, 0).expect_err("limit");
        assert_eq!(error.code, NativeErrorCode::NativeGpuBusy);
    }

    #[test]
    fn duplicate_descriptor_id_is_rejected() {
        let registry = NativeGpuHandleRegistry::new(4);
        registry.register_stub(descriptor(), owner(), 1, 0).expect("register");

        let error = registry.register_stub(descriptor(), owner(), 2, 0).expect_err("duplicate");
        assert_eq!(error.code, NativeErrorCode::NativeGpuBusy);
        assert_eq!(registry.len().expect("len"), 1);
    }

    #[test]
    fn platform_handle_must_match_descriptor_platform() {
        let registry = NativeGpuHandleRegistry::new(4);
        let error = registry
            .register_private(
                descriptor(),
                PlatformHandle::Stub {
                    _token: 99,
                    platform: RuntimeNativeGpuPlatform::LinuxDmabuf,
                },
                NativeFence::new(1, 0),
                owner(),
            )
            .expect_err("platform mismatch");

        assert_eq!(error.code, NativeErrorCode::NativeGpuUnavailable);
        assert_eq!(registry.len().expect("len"), 0);
    }

    #[cfg(unix)]
    #[test]
    fn registers_unix_fd_handle_without_exposing_fd() {
        let registry = NativeGpuHandleRegistry::new(4);
        let fd: OwnedFd = File::open("/dev/null").expect("open fd").into();
        let sync_file: OwnedFd = File::open("/dev/null").expect("open sync fd").into();
        let descriptor = RuntimeNativeGpuDescriptor {
            id: "dmabuf-frame-1".to_string(),
            kind: RuntimeNativeGpuKind::Texture,
            platform: RuntimeNativeGpuPlatform::LinuxDmabuf,
            byte_length: None,
            width: Some(1280),
            height: Some(720),
            format: Some("nv12".to_string()),
            schema_name: Some("media.CameraFrame.v1".to_string()),
            producer: "camera-plugin".to_string(),
            fallback: RuntimeNativeGpuFallback::CopyToWebGpu,
        };

        registry
            .register_unix_fd(
                descriptor,
                owner(),
                NativeGpuUnixFdHandle {
                    fd,
                    sync_file: Some(sync_file),
                    plane_count: 2,
                    modifier: Some(0),
                },
                9,
                0,
            )
            .expect("register fd");

        let snapshot = registry.snapshot("dmabuf-frame-1", &owner()).expect("snapshot");
        assert_eq!(snapshot.handle.kind, NativeGpuPlatformHandleKind::UnixFd);
        assert_eq!(snapshot.handle.platform, RuntimeNativeGpuPlatform::LinuxDmabuf);
        assert_eq!(snapshot.handle.plane_count, 2);
        assert!(snapshot.handle.has_external_sync);
    }

    #[cfg(unix)]
    #[test]
    fn rejects_invalid_unix_fd_plane_count() {
        let registry = NativeGpuHandleRegistry::new(4);
        let fd: OwnedFd = File::open("/dev/null").expect("open fd").into();
        let mut descriptor = descriptor();
        descriptor.platform = RuntimeNativeGpuPlatform::LinuxDmabuf;

        let error = registry
            .register_unix_fd(
                descriptor,
                owner(),
                NativeGpuUnixFdHandle { fd, sync_file: None, plane_count: 0, modifier: None },
                1,
                0,
            )
            .expect_err("invalid plane count");

        assert_eq!(error.code, NativeErrorCode::MalformedFrame);
    }

    #[test]
    fn registers_plugin_opaque_handle_without_exposing_token() {
        let registry = NativeGpuHandleRegistry::new(4);
        let handle = NativeGpuOpaquePluginHandle::new(
            "iosurface-plugin-slot-42",
            RuntimeNativeGpuPlatform::AppleIosurface,
            true,
        )
        .expect("opaque handle");

        registry
            .register_plugin_opaque(descriptor(), owner(), handle, 11, 0)
            .expect("register opaque");

        let snapshot = registry.snapshot("frame-1", &owner()).expect("snapshot");
        assert_eq!(snapshot.handle.kind, NativeGpuPlatformHandleKind::PluginOpaque);
        assert_eq!(snapshot.handle.platform, RuntimeNativeGpuPlatform::AppleIosurface);
        assert!(snapshot.handle.has_external_sync);
    }

    #[test]
    fn owner_scope_is_bounded() {
        let error = NativeGpuOwnerScope::new("tenant-1", "p".repeat(129), "gpu-0")
            .expect_err("owner text too long");

        assert_eq!(error.code, NativeErrorCode::MalformedFrame);
    }

    #[test]
    fn invalid_descriptor_is_rejected_before_registry_insert() {
        let registry = NativeGpuHandleRegistry::new(4);
        let mut invalid = descriptor();
        invalid.width = None;

        let error = registry.register_stub(invalid, owner(), 1, 0).expect_err("invalid");
        assert_eq!(error.code, NativeErrorCode::MalformedFrame);
        assert_eq!(registry.len().expect("len"), 0);
    }

    #[test]
    fn materialization_plan_returns_public_fallback_only() {
        let registry = NativeGpuHandleRegistry::new(4);
        registry.register_stub(descriptor(), owner(), 1, 0).expect("register");

        let plan = registry.materialization_plan("frame-1", &owner()).expect("plan");
        assert_eq!(plan.descriptor.id, "frame-1");
        assert_eq!(plan.fallback, RuntimeNativeGpuFallback::CopyToWebGpu);
    }
}
