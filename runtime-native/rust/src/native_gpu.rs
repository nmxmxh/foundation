use std::collections::BTreeMap;
#[cfg(unix)]
use std::os::fd::OwnedFd;
use std::sync::atomic::{AtomicU64, Ordering};
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

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct NativeGpuSlotReleaseResult {
    pub remaining_refs: u32,
    pub removed: bool,
}

struct NativeGpuSlot {
    _slot_index: u32,
    /// Generation in the high 32 bits, reference count in the low 32 bits.
    /// One word lets a single CAS check the generation and change the count,
    /// so an acquire can never land on a slot that a final release retires.
    state: AtomicU64,
    signaled_value: AtomicU64,
    required_value: AtomicU64,
    fence_id: AtomicU64,
    /// Exact owner scope. A hash compare would let a colliding scope pass.
    owner: RwLock<Option<NativeGpuOwnerScope>>,
    /// Descriptor id, so a final release removes the record without a map scan.
    descriptor_id: RwLock<Option<String>>,
}

impl NativeGpuSlot {
    fn new(slot_index: u32) -> Self {
        Self {
            _slot_index: slot_index,
            state: AtomicU64::new(pack_slot_state(1, 0)),
            signaled_value: AtomicU64::new(0),
            required_value: AtomicU64::new(0),
            fence_id: AtomicU64::new(0),
            owner: RwLock::new(None),
            descriptor_id: RwLock::new(None),
        }
    }

    fn owner_matches(&self, owner_scope: &NativeGpuOwnerScope) -> Result<(), NativeRuntimeError> {
        let guard =
            self.owner.read().map_err(|_| gpu_unavailable("native GPU registry lock poisoned"))?;
        if guard.as_ref() != Some(owner_scope) {
            return Err(NativeRuntimeError::new(
                NativeErrorCode::NativeGpuUnauthorized,
                "native GPU descriptor owner scope mismatch",
            ));
        }
        Ok(())
    }

    fn fence_complete(&self) -> bool {
        self.signaled_value.load(Ordering::Acquire) >= self.required_value.load(Ordering::Acquire)
    }
}

const fn pack_slot_state(generation: u32, refs: u32) -> u64 {
    ((generation as u64) << 32) | refs as u64
}

const fn unpack_slot_state(state: u64) -> (u32, u32) {
    ((state >> 32) as u32, (state & 0xffff_ffff) as u32)
}

struct NativeGpuHandleRecord {
    descriptor: RuntimeNativeGpuDescriptor,
    platform_handle: PlatformHandle,
    owner_scope: NativeGpuOwnerScope,
    slot_index: u32,
    slot_handle: u64,
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

pub struct NativeGpuHandleRegistry {
    records: RwLock<BTreeMap<String, NativeGpuHandleRecord>>,
    slots: Box<[NativeGpuSlot]>,
    free_slots: RwLock<Vec<u32>>,
    max_records: usize,
}

impl Default for NativeGpuHandleRegistry {
    fn default() -> Self {
        Self::new(DEFAULT_MAX_NATIVE_GPU_RECORDS)
    }
}

impl NativeGpuHandleRegistry {
    pub fn new(max_records: usize) -> Self {
        let mut slots = Vec::with_capacity(max_records);
        let mut free_slots = Vec::with_capacity(max_records);
        for i in 0..max_records {
            slots.push(NativeGpuSlot::new(i as u32));
            free_slots.push(i as u32);
        }
        free_slots.reverse();
        Self {
            records: RwLock::new(BTreeMap::new()),
            slots: slots.into_boxed_slice(),
            free_slots: RwLock::new(free_slots),
            max_records,
        }
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
            fence_id,
            required_fence_value,
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
            fence_id,
            required_fence_value,
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
            fence_id,
            required_fence_value,
            owner_scope,
        )
    }

    pub fn slot_handle(&self, descriptor_id: &str) -> Result<u64, NativeRuntimeError> {
        let guard = self
            .records
            .read()
            .map_err(|_| gpu_unavailable("native GPU registry lock poisoned"))?;
        let record = guard.get(descriptor_id).ok_or_else(|| {
            gpu_unavailable(format!("native GPU descriptor {descriptor_id} is unavailable"))
        })?;
        Ok(record.slot_handle)
    }

    /// Returns the live slot for a handle. The handle generation must match and the slot must hold references.
    fn live_slot(&self, handle: u64) -> Result<(&NativeGpuSlot, usize, u32), NativeRuntimeError> {
        let slot_index = (handle & 0xffff_ffff) as usize;
        let generation = (handle >> 32) as u32;
        let slot = self
            .slots
            .get(slot_index)
            .ok_or_else(|| gpu_unavailable("native GPU slot handle out of bounds"))?;
        let (current_gen, refs) = unpack_slot_state(slot.state.load(Ordering::Acquire));
        if current_gen != generation || refs == 0 {
            return Err(gpu_unavailable("native GPU slot handle is unavailable or expired"));
        }
        Ok((slot, slot_index, generation))
    }

    pub fn acquire_slot(
        &self,
        handle: u64,
        owner_scope: &NativeGpuOwnerScope,
    ) -> Result<u32, NativeRuntimeError> {
        owner_scope.validate()?;
        let (slot, _, generation) = self.live_slot(handle)?;
        slot.owner_matches(owner_scope)?;

        // The CAS repeats the generation check. A slot retired and reused after
        // the owner check has a new generation, so the CAS fails safely.
        let mut current = slot.state.load(Ordering::Acquire);
        loop {
            let (current_gen, refs) = unpack_slot_state(current);
            if current_gen != generation || refs == 0 {
                return Err(gpu_unavailable("native GPU slot handle is unavailable or expired"));
            }
            if refs == u32::MAX {
                return Err(gpu_busy("native GPU descriptor ref_count overflow"));
            }
            match slot.state.compare_exchange_weak(
                current,
                pack_slot_state(generation, refs + 1),
                Ordering::AcqRel,
                Ordering::Acquire,
            ) {
                Ok(_) => return Ok(refs + 1),
                Err(actual) => current = actual,
            }
        }
    }

    pub fn release_slot(
        &self,
        handle: u64,
        owner_scope: &NativeGpuOwnerScope,
    ) -> Result<NativeGpuSlotReleaseResult, NativeRuntimeError> {
        owner_scope.validate()?;
        let (slot, slot_index, generation) = self.live_slot(handle)?;
        slot.owner_matches(owner_scope)?;

        let mut current = slot.state.load(Ordering::Acquire);
        loop {
            let (current_gen, refs) = unpack_slot_state(current);
            if current_gen != generation || refs == 0 {
                return Err(gpu_unavailable("native GPU slot descriptor already released"));
            }
            if refs == 1 {
                if !slot.fence_complete() {
                    return Err(gpu_busy("native GPU descriptor fence is not complete"));
                }
                return self.finalize_slot_release(slot_index, generation, owner_scope);
            }
            match slot.state.compare_exchange_weak(
                current,
                pack_slot_state(generation, refs - 1),
                Ordering::AcqRel,
                Ordering::Acquire,
            ) {
                Ok(_) => {
                    return Ok(NativeGpuSlotReleaseResult {
                        remaining_refs: refs - 1,
                        removed: false,
                    });
                }
                Err(actual) => current = actual,
            }
        }
    }

    /// Signals the slot fence. Only the owner may signal: an early signal lets a
    /// final release run before the GPU finishes with the buffer.
    pub fn signal_fence_slot(
        &self,
        handle: u64,
        owner_scope: &NativeGpuOwnerScope,
        signaled_value: u64,
    ) -> Result<NativeGpuFenceSnapshot, NativeRuntimeError> {
        owner_scope.validate()?;
        let (slot, _, _) = self.live_slot(handle)?;
        slot.owner_matches(owner_scope)?;

        let prev = slot.signaled_value.fetch_max(signaled_value, Ordering::AcqRel);
        let final_value = prev.max(signaled_value);
        let required = slot.required_value.load(Ordering::Acquire);
        let fence_id = slot.fence_id.load(Ordering::Acquire);

        Ok(NativeGpuFenceSnapshot {
            fence_id,
            required_value: required,
            signaled_value: final_value,
            complete: final_value >= required,
        })
    }

    pub fn is_fence_complete_slot(&self, handle: u64) -> Result<bool, NativeRuntimeError> {
        let (slot, _, _) = self.live_slot(handle)?;
        Ok(slot.fence_complete())
    }

    pub fn fence_snapshot_slot(
        &self,
        handle: u64,
    ) -> Result<NativeGpuFenceSnapshot, NativeRuntimeError> {
        let (slot, _, _) = self.live_slot(handle)?;
        let signaled = slot.signaled_value.load(Ordering::Acquire);
        let required = slot.required_value.load(Ordering::Acquire);
        let fence_id = slot.fence_id.load(Ordering::Acquire);
        Ok(NativeGpuFenceSnapshot {
            fence_id,
            required_value: required,
            signaled_value: signaled,
            complete: signaled >= required,
        })
    }

    fn finalize_slot_release(
        &self,
        slot_index: usize,
        generation: u32,
        owner_scope: &NativeGpuOwnerScope,
    ) -> Result<NativeGpuSlotReleaseResult, NativeRuntimeError> {
        let mut guard = self
            .records
            .write()
            .map_err(|_| gpu_unavailable("native GPU registry lock poisoned"))?;
        let slot = &self.slots[slot_index];
        slot.owner_matches(owner_scope)?;

        let mut current = slot.state.load(Ordering::Acquire);
        loop {
            let (current_gen, refs) = unpack_slot_state(current);
            if current_gen != generation || refs == 0 {
                return Err(gpu_unavailable("native GPU slot handle is unavailable or expired"));
            }
            if refs > 1 {
                // A concurrent acquire landed. Drop one reference and keep the slot.
                match slot.state.compare_exchange_weak(
                    current,
                    pack_slot_state(generation, refs - 1),
                    Ordering::AcqRel,
                    Ordering::Acquire,
                ) {
                    Ok(_) => {
                        return Ok(NativeGpuSlotReleaseResult {
                            remaining_refs: refs - 1,
                            removed: false,
                        });
                    }
                    Err(actual) => {
                        current = actual;
                        continue;
                    }
                }
            }
            if !slot.fence_complete() {
                return Err(gpu_busy("native GPU descriptor fence is not complete"));
            }
            // Retire: 1 -> 0 and bump the generation in one step. An acquire that
            // raced this CAS fails, and the loop sees its new count instead.
            match slot.state.compare_exchange(
                current,
                pack_slot_state(generation.wrapping_add(1), 0),
                Ordering::AcqRel,
                Ordering::Acquire,
            ) {
                Ok(_) => break,
                Err(actual) => current = actual,
            }
        }

        let descriptor_id = slot
            .descriptor_id
            .write()
            .map_err(|_| gpu_unavailable("native GPU registry lock poisoned"))?
            .take();
        if let Some(id) = descriptor_id {
            guard.remove(&id);
        }
        *slot.owner.write().map_err(|_| gpu_unavailable("native GPU registry lock poisoned"))? =
            None;

        let mut free_guard = self
            .free_slots
            .write()
            .map_err(|_| gpu_unavailable("native GPU registry lock poisoned"))?;
        free_guard.push(slot_index as u32);

        Ok(NativeGpuSlotReleaseResult { remaining_refs: 0, removed: true })
    }

    pub fn acquire(
        &self,
        descriptor_id: &str,
        owner_scope: &NativeGpuOwnerScope,
    ) -> Result<RuntimeNativeGpuDescriptor, NativeRuntimeError> {
        owner_scope.validate()?;
        let guard = self
            .records
            .read()
            .map_err(|_| gpu_unavailable("native GPU registry lock poisoned"))?;
        let record = guard.get(descriptor_id).ok_or_else(|| {
            gpu_unavailable(format!("native GPU descriptor {descriptor_id} is unavailable"))
        })?;
        assert_owner(record, owner_scope)?;
        self.acquire_slot(record.slot_handle, owner_scope)?;
        Ok(record.descriptor.clone())
    }

    pub fn release(
        &self,
        descriptor_id: &str,
        owner_scope: &NativeGpuOwnerScope,
    ) -> Result<NativeGpuReleaseResult, NativeRuntimeError> {
        owner_scope.validate()?;
        let slot_handle = {
            let guard = self
                .records
                .read()
                .map_err(|_| gpu_unavailable("native GPU registry lock poisoned"))?;
            let record = guard.get(descriptor_id).ok_or_else(|| {
                gpu_unavailable(format!("native GPU descriptor {descriptor_id} is unavailable"))
            })?;
            assert_owner(record, owner_scope)?;
            record.slot_handle
        };

        let result = self.release_slot(slot_handle, owner_scope)?;
        Ok(NativeGpuReleaseResult {
            descriptor_id: descriptor_id.to_string(),
            remaining_refs: result.remaining_refs,
            removed: result.removed,
        })
    }

    pub fn signal_fence(
        &self,
        descriptor_id: &str,
        owner_scope: &NativeGpuOwnerScope,
        signaled_value: u64,
    ) -> Result<NativeGpuFenceSnapshot, NativeRuntimeError> {
        owner_scope.validate()?;
        let slot_handle = {
            let guard = self
                .records
                .read()
                .map_err(|_| gpu_unavailable("native GPU registry lock poisoned"))?;
            let record = guard.get(descriptor_id).ok_or_else(|| {
                gpu_unavailable(format!("native GPU descriptor {descriptor_id} is unavailable"))
            })?;
            assert_owner(record, owner_scope)?;
            record.slot_handle
        };
        self.signal_fence_slot(slot_handle, owner_scope, signaled_value)
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
        let slot = &self.slots[record.slot_index as usize];
        let (_, ref_count) = unpack_slot_state(slot.state.load(Ordering::Acquire));
        let signaled_value = slot.signaled_value.load(Ordering::Acquire);
        let required_value = slot.required_value.load(Ordering::Acquire);
        let fence_id = slot.fence_id.load(Ordering::Acquire);
        let fence = NativeGpuFenceSnapshot {
            fence_id,
            required_value,
            signaled_value,
            complete: signaled_value >= required_value,
        };
        Ok(NativeGpuRegistrySnapshot {
            descriptor: record.descriptor.clone(),
            owner_scope: record.owner_scope.clone(),
            ref_count,
            fence,
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
        fence_id: u64,
        required_fence_value: u64,
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

        let mut free_guard = self
            .free_slots
            .write()
            .map_err(|_| gpu_unavailable("native GPU registry lock poisoned"))?;
        let slot_index =
            free_guard.pop().ok_or_else(|| gpu_busy("native GPU registry record limit reached"))?;

        let slot = &self.slots[slot_index as usize];
        let (current_gen, _) = unpack_slot_state(slot.state.load(Ordering::Acquire));

        slot.required_value.store(required_fence_value, Ordering::Release);
        slot.fence_id.store(fence_id, Ordering::Release);
        slot.signaled_value.store(0, Ordering::Release);
        *slot.owner.write().map_err(|_| gpu_unavailable("native GPU registry lock poisoned"))? =
            Some(owner_scope.clone());
        *slot
            .descriptor_id
            .write()
            .map_err(|_| gpu_unavailable("native GPU registry lock poisoned"))? =
            Some(descriptor.id.clone());
        // Publish last: the Release store makes the fields above visible to any acquire.
        slot.state.store(pack_slot_state(current_gen, 1), Ordering::Release);

        let slot_handle = ((current_gen as u64) << 32) | (slot_index as u64);
        let public_descriptor = descriptor.clone();
        guard.insert(
            descriptor.id.clone(),
            NativeGpuHandleRecord {
                descriptor,
                platform_handle,
                owner_scope,
                slot_index,
                slot_handle,
            },
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

        let fence = registry.signal_fence("frame-1", &owner(), 3).expect("signal");
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
                1,
                0,
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

    #[test]
    fn slot_maplet_acquire_and_release_lifecycle() {
        let registry = NativeGpuHandleRegistry::new(4);
        registry.register_stub(descriptor(), owner(), 42, 5).expect("register");
        let handle = registry.slot_handle("frame-1").expect("slot handle");

        assert!(!registry.is_fence_complete_slot(handle).expect("fence check"));
        let ref2 = registry.acquire_slot(handle, &owner()).expect("acquire slot");
        assert_eq!(ref2, 2);
        let ref3 = registry.acquire_slot(handle, &owner()).expect("acquire slot");
        assert_eq!(ref3, 3);

        let rel1 = registry.release_slot(handle, &owner()).expect("release slot");
        assert_eq!(rel1.remaining_refs, 2);
        assert!(!rel1.removed);

        let rel2 = registry.release_slot(handle, &owner()).expect("release slot");
        assert_eq!(rel2.remaining_refs, 1);
        assert!(!rel2.removed);

        let blocked = registry.release_slot(handle, &owner()).expect_err("fence incomplete");
        assert_eq!(blocked.code, NativeErrorCode::NativeGpuBusy);

        let fence = registry.signal_fence_slot(handle, &owner(), 5).expect("signal fence slot");
        assert!(fence.complete);
        assert!(registry.is_fence_complete_slot(handle).expect("fence complete"));

        let final_rel = registry.release_slot(handle, &owner()).expect("final release");
        assert_eq!(final_rel.remaining_refs, 0);
        assert!(final_rel.removed);
        assert_eq!(registry.len().expect("len"), 0);

        let expired = registry.acquire_slot(handle, &owner()).expect_err("expired");
        assert_eq!(expired.code, NativeErrorCode::NativeGpuUnavailable);
    }

    #[test]
    fn slot_maplet_rejects_owner_scope_mismatch() {
        let registry = NativeGpuHandleRegistry::new(4);
        registry.register_stub(descriptor(), owner(), 1, 0).expect("register");
        let handle = registry.slot_handle("frame-1").expect("slot handle");
        let other = NativeGpuOwnerScope::new("tenant-2", "camera-plugin", "gpu-0").expect("other");

        let acquire_err = registry.acquire_slot(handle, &other).expect_err("scope mismatch");
        assert_eq!(acquire_err.code, NativeErrorCode::NativeGpuUnauthorized);

        let release_err = registry.release_slot(handle, &other).expect_err("scope mismatch");
        assert_eq!(release_err.code, NativeErrorCode::NativeGpuUnauthorized);
    }

    #[test]
    fn slot_maplet_generational_invalidation_on_slot_reuse() {
        let registry = NativeGpuHandleRegistry::new(1);
        registry.register_stub(descriptor(), owner(), 1, 0).expect("register 1");
        let handle1 = registry.slot_handle("frame-1").expect("handle 1");

        let rel = registry.release_slot(handle1, &owner()).expect("release 1");
        assert!(rel.removed);

        let mut desc2 = descriptor();
        desc2.id = "frame-2".to_string();
        registry.register_stub(desc2, owner(), 2, 0).expect("register 2");
        let handle2 = registry.slot_handle("frame-2").expect("handle 2");

        assert_ne!(handle1, handle2);
        assert_eq!(handle1 & 0xffff_ffff, handle2 & 0xffff_ffff);

        let stale_err = registry.acquire_slot(handle1, &owner()).expect_err("stale handle");
        assert_eq!(stale_err.code, NativeErrorCode::NativeGpuUnavailable);

        let ref_ok = registry.acquire_slot(handle2, &owner()).expect("valid handle");
        assert_eq!(ref_ok, 2);
    }

    #[test]
    fn slot_fence_signal_requires_owner_scope() {
        let registry = NativeGpuHandleRegistry::new(4);
        registry.register_stub(descriptor(), owner(), 7, 5).expect("register");
        let handle = registry.slot_handle("frame-1").expect("slot handle");
        let other = NativeGpuOwnerScope::new("tenant-2", "camera-plugin", "gpu-0").expect("other");

        let slot_err = registry.signal_fence_slot(handle, &other, 5).expect_err("foreign signal");
        assert_eq!(slot_err.code, NativeErrorCode::NativeGpuUnauthorized);
        let desc_err = registry.signal_fence("frame-1", &other, 5).expect_err("foreign signal");
        assert_eq!(desc_err.code, NativeErrorCode::NativeGpuUnauthorized);
        assert!(!registry.is_fence_complete_slot(handle).expect("fence state"));

        // The fence stays pending, so the owner's final release is still blocked.
        let blocked = registry.release_slot(handle, &owner()).expect_err("fence pending");
        assert_eq!(blocked.code, NativeErrorCode::NativeGpuBusy);
    }

    /// Acquire races the final release. Every acquire that succeeds must hold a
    /// live slot: the matching release either keeps the slot or retires it once.
    #[test]
    fn slot_acquire_racing_final_release_never_resurrects() {
        use std::sync::Arc;
        use std::thread;

        for round in 0..200 {
            let registry = Arc::new(NativeGpuHandleRegistry::new(2));
            registry.register_stub(descriptor(), owner(), 1, 0).expect("register");
            let handle = registry.slot_handle("frame-1").expect("slot handle");

            let releaser = {
                let registry = Arc::clone(&registry);
                thread::spawn(move || registry.release_slot(handle, &owner()))
            };
            let acquirer = {
                let registry = Arc::clone(&registry);
                thread::spawn(move || registry.acquire_slot(handle, &owner()))
            };
            let released = releaser.join().expect("releaser");
            let acquired = acquirer.join().expect("acquirer");

            match (acquired, released) {
                // Acquire won: the release only dropped one reference.
                (Ok(_), Ok(rel)) if !rel.removed => {
                    assert_eq!(rel.remaining_refs, 1, "round {round}");
                    assert_eq!(registry.len().expect("len"), 1, "round {round}");
                    let last = registry.release_slot(handle, &owner()).expect("last release");
                    assert!(last.removed, "round {round}");
                }
                // Release won: the acquire must have failed, and the slot is gone.
                (Err(err), Ok(rel)) => {
                    assert!(rel.removed, "round {round}");
                    assert_eq!(err.code, NativeErrorCode::NativeGpuUnavailable, "round {round}");
                }
                (acq, rel) => panic!("round {round}: inconsistent outcome {acq:?} / {rel:?}"),
            }
            assert_eq!(registry.len().expect("len"), 0, "round {round}");
            registry
                .acquire_slot(handle, &owner())
                .expect_err("retired handle must stay expired");
        }
    }
}
