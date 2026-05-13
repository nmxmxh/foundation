#![forbid(unsafe_code)]

use std::collections::{BTreeMap, BTreeSet};
use std::fmt::{Display, Formatter};
use std::sync::{Arc, RwLock};

use ovrt_core::{RuntimeRole, RuntimeUnitDescriptor};
use ovrt_native::NativeRuntimeHost;
use ovrt_unit::RuntimeUnit;

const REQUEST_MAGIC: &[u8; 4] = b"OVRN";
const RESPONSE_MAGIC: &[u8; 4] = b"OVRR";
const NATIVE_FRAME_VERSION: u8 = 1;
const SUPPORTED_SCHEMA_VERSION: &str = "1.0";
pub const MAX_NATIVE_FRAME_BYTES: usize = 2 * 1024 * 1024;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum NativePayloadEncoding {
    Capnp = 1,
    Protobuf = 2,
}

impl NativePayloadEncoding {
    fn from_id(id: u8) -> Result<Self, NativeRuntimeError> {
        match id {
            1 => Ok(Self::Capnp),
            2 => Ok(Self::Protobuf),
            _ => Err(NativeRuntimeError::new(
                NativeErrorCode::UnsupportedEncoding,
                format!("unsupported native payload encoding id {id}"),
            )),
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct NativeDispatchRequest {
    pub unit_id: String,
    pub schema_version: String,
    pub encoding: NativePayloadEncoding,
    pub payload: Vec<u8>,
    pub metadata: Vec<u8>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct NativeDispatchResponse {
    pub status_code: u16,
    pub payload: Vec<u8>,
    pub diagnostics: Vec<u8>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct NativeRuntimeCapabilities {
    pub native: bool,
    pub native_ffi: bool,
    pub native_shared_memory: bool,
    pub wasm_sab: bool,
    pub wasm_transfer: bool,
    pub platform: String,
}

impl NativeRuntimeCapabilities {
    pub fn detect() -> Self {
        Self {
            native: true,
            native_ffi: true,
            native_shared_memory: cfg!(target_os = "linux"),
            wasm_sab: false,
            wasm_transfer: false,
            platform: std::env::consts::OS.to_string(),
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum NativeErrorCode {
    MalformedFrame,
    OversizedFrame,
    UnsupportedVersion,
    UnsupportedEncoding,
    UnauthorizedUnit,
    RuntimeDispatchFailed,
    StoreUnavailable,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct NativeRuntimeError {
    pub code: NativeErrorCode,
    pub message: String,
}

impl NativeRuntimeError {
    pub fn new(code: NativeErrorCode, message: impl Into<String>) -> Self {
        Self {
            code,
            message: message.into(),
        }
    }
}

impl Display for NativeRuntimeError {
    fn fmt(&self, f: &mut Formatter<'_>) -> std::fmt::Result {
        write!(f, "{:?}: {}", self.code, self.message)
    }
}

impl std::error::Error for NativeRuntimeError {}

pub struct NativeRuntimeBridge {
    host: NativeRuntimeHost,
    allowed_units: BTreeSet<String>,
    max_frame_bytes: usize,
}

impl NativeRuntimeBridge {
    pub fn new(host: NativeRuntimeHost) -> Self {
        Self {
            host,
            allowed_units: BTreeSet::new(),
            max_frame_bytes: MAX_NATIVE_FRAME_BYTES,
        }
    }

    pub fn with_role_limits(role_limits: BTreeMap<RuntimeRole, usize>) -> Self {
        Self::new(NativeRuntimeHost::new(role_limits))
    }

    pub fn with_max_frame_bytes(mut self, max_frame_bytes: usize) -> Self {
        self.max_frame_bytes = max_frame_bytes;
        self
    }

    pub fn register_allowed_unit(
        &mut self,
        unit: Arc<dyn RuntimeUnit>,
    ) -> Result<RuntimeUnitDescriptor, NativeRuntimeError> {
        let descriptor = unit.descriptor();
        self.host.register_unit(unit).map_err(|error| {
            NativeRuntimeError::new(NativeErrorCode::RuntimeDispatchFailed, error)
        })?;
        self.allowed_units.insert(descriptor.unit_id.clone());
        Ok(descriptor)
    }

    pub fn allow_unit(&mut self, unit_id: impl Into<String>) {
        self.allowed_units.insert(unit_id.into());
    }

    pub fn dispatch_frame(&self, frame: &[u8]) -> Result<Vec<u8>, NativeRuntimeError> {
        let request = decode_dispatch_request(frame, self.max_frame_bytes)?;
        if !self.allowed_units.contains(&request.unit_id) {
            return Err(NativeRuntimeError::new(
                NativeErrorCode::UnauthorizedUnit,
                format!("runtime unit {} is not allowlisted", request.unit_id),
            ));
        }
        let output = self
            .host
            .dispatch_direct(&request.unit_id, &request.payload)
            .map_err(|error| {
                NativeRuntimeError::new(NativeErrorCode::RuntimeDispatchFailed, error)
            })?;
        encode_dispatch_response(&NativeDispatchResponse {
            status_code: 0,
            payload: output,
            diagnostics: Vec::new(),
        })
    }

    pub fn capabilities(&self) -> NativeRuntimeCapabilities {
        NativeRuntimeCapabilities::detect()
    }
}

#[derive(Default)]
pub struct NativeSecureStore {
    values: RwLock<BTreeMap<String, Vec<u8>>>,
}

impl NativeSecureStore {
    pub fn put(&self, key: &str, value: &[u8]) -> Result<(), NativeRuntimeError> {
        validate_store_key(key)?;
        let mut guard = self.values.write().map_err(|_| {
            NativeRuntimeError::new(
                NativeErrorCode::StoreUnavailable,
                "secure store lock poisoned",
            )
        })?;
        guard.insert(key.to_string(), value.to_vec());
        Ok(())
    }

    pub fn get(&self, key: &str) -> Result<Option<Vec<u8>>, NativeRuntimeError> {
        validate_store_key(key)?;
        let guard = self.values.read().map_err(|_| {
            NativeRuntimeError::new(
                NativeErrorCode::StoreUnavailable,
                "secure store lock poisoned",
            )
        })?;
        Ok(guard.get(key).cloned())
    }

    pub fn delete(&self, key: &str) -> Result<bool, NativeRuntimeError> {
        validate_store_key(key)?;
        let mut guard = self.values.write().map_err(|_| {
            NativeRuntimeError::new(
                NativeErrorCode::StoreUnavailable,
                "secure store lock poisoned",
            )
        })?;
        Ok(guard.remove(key).is_some())
    }
}

pub fn encode_dispatch_request(
    request: &NativeDispatchRequest,
) -> Result<Vec<u8>, NativeRuntimeError> {
    let unit_id = request.unit_id.as_bytes();
    let schema_version = request.schema_version.as_bytes();
    if unit_id.is_empty() || unit_id.len() > u16::MAX as usize {
        return Err(NativeRuntimeError::new(
            NativeErrorCode::MalformedFrame,
            "unit id length is invalid",
        ));
    }
    if schema_version.is_empty() || schema_version.len() > u16::MAX as usize {
        return Err(NativeRuntimeError::new(
            NativeErrorCode::MalformedFrame,
            "schema version length is invalid",
        ));
    }
    if request.payload.len() > u32::MAX as usize || request.metadata.len() > u32::MAX as usize {
        return Err(NativeRuntimeError::new(
            NativeErrorCode::OversizedFrame,
            "native request field exceeds u32 length",
        ));
    }

    let mut frame = Vec::with_capacity(
        20 + unit_id.len() + schema_version.len() + request.payload.len() + request.metadata.len(),
    );
    frame.extend_from_slice(REQUEST_MAGIC);
    frame.push(NATIVE_FRAME_VERSION);
    frame.push(request.encoding as u8);
    frame.extend_from_slice(&0_u16.to_be_bytes());
    frame.extend_from_slice(&(unit_id.len() as u16).to_be_bytes());
    frame.extend_from_slice(&(schema_version.len() as u16).to_be_bytes());
    frame.extend_from_slice(&(request.payload.len() as u32).to_be_bytes());
    frame.extend_from_slice(&(request.metadata.len() as u32).to_be_bytes());
    frame.extend_from_slice(unit_id);
    frame.extend_from_slice(schema_version);
    frame.extend_from_slice(&request.payload);
    frame.extend_from_slice(&request.metadata);
    Ok(frame)
}

pub fn decode_dispatch_request(
    frame: &[u8],
    max_frame_bytes: usize,
) -> Result<NativeDispatchRequest, NativeRuntimeError> {
    if frame.len() > max_frame_bytes {
        return Err(NativeRuntimeError::new(
            NativeErrorCode::OversizedFrame,
            format!(
                "native frame has {} bytes; max is {max_frame_bytes}",
                frame.len()
            ),
        ));
    }
    if frame.len() < 20 || &frame[0..4] != REQUEST_MAGIC {
        return Err(NativeRuntimeError::new(
            NativeErrorCode::MalformedFrame,
            "native request frame has invalid magic or header",
        ));
    }
    if frame[4] != NATIVE_FRAME_VERSION {
        return Err(NativeRuntimeError::new(
            NativeErrorCode::UnsupportedVersion,
            format!("unsupported native frame version {}", frame[4]),
        ));
    }
    let encoding = NativePayloadEncoding::from_id(frame[5])?;
    let unit_len = u16::from_be_bytes([frame[8], frame[9]]) as usize;
    let schema_len = u16::from_be_bytes([frame[10], frame[11]]) as usize;
    let payload_len = u32::from_be_bytes([frame[12], frame[13], frame[14], frame[15]]) as usize;
    let metadata_len = u32::from_be_bytes([frame[16], frame[17], frame[18], frame[19]]) as usize;
    let offset: usize = 20;
    let total = offset
        .checked_add(unit_len)
        .and_then(|value| value.checked_add(schema_len))
        .and_then(|value| value.checked_add(payload_len))
        .and_then(|value| value.checked_add(metadata_len))
        .ok_or_else(|| {
            NativeRuntimeError::new(
                NativeErrorCode::OversizedFrame,
                "native frame length overflow",
            )
        })?;
    if total != frame.len() {
        return Err(NativeRuntimeError::new(
            NativeErrorCode::MalformedFrame,
            "native request frame length does not match header",
        ));
    }

    let mut cursor = offset;
    let unit_id = read_utf8(frame, &mut cursor, unit_len, "unit id")?;
    let schema_version = read_utf8(frame, &mut cursor, schema_len, "schema version")?;
    if schema_version != SUPPORTED_SCHEMA_VERSION {
        return Err(NativeRuntimeError::new(
            NativeErrorCode::UnsupportedVersion,
            format!("unsupported native schema version {schema_version}"),
        ));
    }
    let payload = read_bytes(frame, &mut cursor, payload_len)?.to_vec();
    let metadata = read_bytes(frame, &mut cursor, metadata_len)?.to_vec();

    Ok(NativeDispatchRequest {
        unit_id,
        schema_version,
        encoding,
        payload,
        metadata,
    })
}

pub fn encode_dispatch_response(
    response: &NativeDispatchResponse,
) -> Result<Vec<u8>, NativeRuntimeError> {
    if response.payload.len() > u32::MAX as usize || response.diagnostics.len() > u32::MAX as usize
    {
        return Err(NativeRuntimeError::new(
            NativeErrorCode::OversizedFrame,
            "native response field exceeds u32 length",
        ));
    }
    let mut frame = Vec::with_capacity(16 + response.payload.len() + response.diagnostics.len());
    frame.extend_from_slice(RESPONSE_MAGIC);
    frame.push(NATIVE_FRAME_VERSION);
    frame.push(0);
    frame.extend_from_slice(&response.status_code.to_be_bytes());
    frame.extend_from_slice(&(response.payload.len() as u32).to_be_bytes());
    frame.extend_from_slice(&(response.diagnostics.len() as u32).to_be_bytes());
    frame.extend_from_slice(&response.payload);
    frame.extend_from_slice(&response.diagnostics);
    Ok(frame)
}

pub fn decode_dispatch_response(
    frame: &[u8],
    max_frame_bytes: usize,
) -> Result<NativeDispatchResponse, NativeRuntimeError> {
    if frame.len() > max_frame_bytes {
        return Err(NativeRuntimeError::new(
            NativeErrorCode::OversizedFrame,
            format!(
                "native response has {} bytes; max is {max_frame_bytes}",
                frame.len()
            ),
        ));
    }
    if frame.len() < 16 || &frame[0..4] != RESPONSE_MAGIC {
        return Err(NativeRuntimeError::new(
            NativeErrorCode::MalformedFrame,
            "native response frame has invalid magic or header",
        ));
    }
    if frame[4] != NATIVE_FRAME_VERSION {
        return Err(NativeRuntimeError::new(
            NativeErrorCode::UnsupportedVersion,
            format!("unsupported native response version {}", frame[4]),
        ));
    }
    let status_code = u16::from_be_bytes([frame[6], frame[7]]);
    let payload_len = u32::from_be_bytes([frame[8], frame[9], frame[10], frame[11]]) as usize;
    let diagnostics_len = u32::from_be_bytes([frame[12], frame[13], frame[14], frame[15]]) as usize;
    let total = 16_usize
        .checked_add(payload_len)
        .and_then(|value| value.checked_add(diagnostics_len))
        .ok_or_else(|| {
            NativeRuntimeError::new(
                NativeErrorCode::OversizedFrame,
                "native response length overflow",
            )
        })?;
    if total != frame.len() {
        return Err(NativeRuntimeError::new(
            NativeErrorCode::MalformedFrame,
            "native response frame length does not match header",
        ));
    }
    Ok(NativeDispatchResponse {
        status_code,
        payload: frame[16..16 + payload_len].to_vec(),
        diagnostics: frame[16 + payload_len..].to_vec(),
    })
}

fn read_utf8(
    frame: &[u8],
    cursor: &mut usize,
    len: usize,
    label: &str,
) -> Result<String, NativeRuntimeError> {
    let bytes = read_bytes(frame, cursor, len)?;
    let value = std::str::from_utf8(bytes).map_err(|_| {
        NativeRuntimeError::new(
            NativeErrorCode::MalformedFrame,
            format!("{label} is not utf-8"),
        )
    })?;
    if value.trim().is_empty() {
        return Err(NativeRuntimeError::new(
            NativeErrorCode::MalformedFrame,
            format!("{label} is required"),
        ));
    }
    Ok(value.to_string())
}

fn read_bytes<'a>(
    frame: &'a [u8],
    cursor: &mut usize,
    len: usize,
) -> Result<&'a [u8], NativeRuntimeError> {
    let end = cursor.checked_add(len).ok_or_else(|| {
        NativeRuntimeError::new(
            NativeErrorCode::OversizedFrame,
            "native frame cursor overflow",
        )
    })?;
    if end > frame.len() {
        return Err(NativeRuntimeError::new(
            NativeErrorCode::MalformedFrame,
            "native frame ended before declared field length",
        ));
    }
    let bytes = &frame[*cursor..end];
    *cursor = end;
    Ok(bytes)
}

fn validate_store_key(key: &str) -> Result<(), NativeRuntimeError> {
    let trimmed = key.trim();
    if trimmed.is_empty() || trimmed.len() > 128 {
        return Err(NativeRuntimeError::new(
            NativeErrorCode::MalformedFrame,
            "secure store key must be 1-128 bytes",
        ));
    }
    if !trimmed
        .bytes()
        .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b':' | b'_' | b'-'))
    {
        return Err(NativeRuntimeError::new(
            NativeErrorCode::MalformedFrame,
            "secure store key contains unsupported characters",
        ));
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    struct EchoUnit;

    impl RuntimeUnit for EchoUnit {
        fn descriptor(&self) -> RuntimeUnitDescriptor {
            RuntimeUnitDescriptor {
                unit_id: "bench.echo".to_string(),
                role: RuntimeRole::Compute,
                input_schema: "common/v1/envelope.capnp".to_string(),
                output_schema: "common/v1/envelope.capnp".to_string(),
                supports_wasm: true,
                supports_native: true,
                requires_shared_memory: false,
                supports_gpu: false,
                max_concurrency: 1,
            }
        }

        fn run(&self, input: &[u8]) -> Result<Vec<u8>, String> {
            Ok(input.to_vec())
        }
    }

    #[test]
    fn native_frame_roundtrips_without_json_materialization() {
        let request = NativeDispatchRequest {
            unit_id: "bench.echo".to_string(),
            schema_version: "1.0".to_string(),
            encoding: NativePayloadEncoding::Capnp,
            payload: b"abc".to_vec(),
            metadata: b"corr=req".to_vec(),
        };
        let frame = encode_dispatch_request(&request).expect("encode request");
        let decoded =
            decode_dispatch_request(&frame, MAX_NATIVE_FRAME_BYTES).expect("decode request");
        assert_eq!(decoded, request);
    }

    #[test]
    fn native_frame_rejects_oversized_inputs() {
        let request = NativeDispatchRequest {
            unit_id: "bench.echo".to_string(),
            schema_version: "1.0".to_string(),
            encoding: NativePayloadEncoding::Capnp,
            payload: vec![1; 64],
            metadata: Vec::new(),
        };
        let frame = encode_dispatch_request(&request).expect("encode request");
        let err = decode_dispatch_request(&frame, 8).expect_err("oversized frame must fail");
        assert_eq!(err.code, NativeErrorCode::OversizedFrame);
    }

    #[test]
    fn native_frame_rejects_unsupported_encoding() {
        let request = NativeDispatchRequest {
            unit_id: "bench.echo".to_string(),
            schema_version: "1.0".to_string(),
            encoding: NativePayloadEncoding::Capnp,
            payload: b"abc".to_vec(),
            metadata: Vec::new(),
        };
        let mut frame = encode_dispatch_request(&request).expect("encode request");
        frame[5] = 99;
        let err = decode_dispatch_request(&frame, MAX_NATIVE_FRAME_BYTES)
            .expect_err("bad encoding must fail");
        assert_eq!(err.code, NativeErrorCode::UnsupportedEncoding);
    }

    #[test]
    fn native_frame_rejects_unsupported_schema_version() {
        let request = NativeDispatchRequest {
            unit_id: "bench.echo".to_string(),
            schema_version: "9.9".to_string(),
            encoding: NativePayloadEncoding::Capnp,
            payload: b"abc".to_vec(),
            metadata: Vec::new(),
        };
        let frame = encode_dispatch_request(&request).expect("encode request");
        let err = decode_dispatch_request(&frame, MAX_NATIVE_FRAME_BYTES)
            .expect_err("bad schema must fail");
        assert_eq!(err.code, NativeErrorCode::UnsupportedVersion);
    }

    #[test]
    fn native_frame_rejects_mismatched_metadata_length() {
        let request = NativeDispatchRequest {
            unit_id: "bench.echo".to_string(),
            schema_version: "1.0".to_string(),
            encoding: NativePayloadEncoding::Capnp,
            payload: b"abc".to_vec(),
            metadata: b"corr=req=idem".to_vec(),
        };
        let mut frame = encode_dispatch_request(&request).expect("encode request");
        frame[19] = frame[19].saturating_add(1);
        let err = decode_dispatch_request(&frame, MAX_NATIVE_FRAME_BYTES)
            .expect_err("bad metadata length must fail");
        assert_eq!(err.code, NativeErrorCode::MalformedFrame);
    }

    #[test]
    fn runtime_bridge_enforces_unit_allowlist() {
        let bridge = NativeRuntimeBridge::with_role_limits(BTreeMap::new());
        let request = NativeDispatchRequest {
            unit_id: "bench.echo".to_string(),
            schema_version: "1.0".to_string(),
            encoding: NativePayloadEncoding::Capnp,
            payload: b"abc".to_vec(),
            metadata: Vec::new(),
        };
        let frame = encode_dispatch_request(&request).expect("encode request");
        let err = bridge
            .dispatch_frame(&frame)
            .expect_err("unit must be unauthorized");
        assert_eq!(err.code, NativeErrorCode::UnauthorizedUnit);
    }

    #[test]
    fn runtime_bridge_dispatches_allowlisted_units() {
        let mut bridge = NativeRuntimeBridge::with_role_limits(BTreeMap::new());
        bridge
            .register_allowed_unit(Arc::new(EchoUnit))
            .expect("register allowed unit");
        let request = NativeDispatchRequest {
            unit_id: "bench.echo".to_string(),
            schema_version: "1.0".to_string(),
            encoding: NativePayloadEncoding::Capnp,
            payload: b"abc".to_vec(),
            metadata: Vec::new(),
        };
        let frame = encode_dispatch_request(&request).expect("encode request");
        let response_frame = bridge.dispatch_frame(&frame).expect("dispatch frame");
        let response = decode_dispatch_response(&response_frame, MAX_NATIVE_FRAME_BYTES)
            .expect("decode response");
        assert_eq!(response.status_code, 0);
        assert_eq!(response.payload, b"abc");
    }

    #[test]
    fn native_capabilities_report_runtime_lanes() {
        let bridge = NativeRuntimeBridge::with_role_limits(BTreeMap::new());
        let capabilities = bridge.capabilities();
        assert!(capabilities.native);
        assert!(capabilities.native_ffi);
        assert!(!capabilities.platform.trim().is_empty());
    }

    #[test]
    fn secure_store_roundtrips_without_frontend_storage() {
        let store = NativeSecureStore::default();
        store.put("session:token", b"secret").expect("put");
        assert_eq!(
            store.get("session:token").expect("get"),
            Some(b"secret".to_vec())
        );
        assert!(store.delete("session:token").expect("delete"));
        assert_eq!(store.get("session:token").expect("get"), None);
    }

    #[test]
    fn secure_store_rejects_invalid_keys() {
        let store = NativeSecureStore::default();
        let err = store
            .put("../session", b"secret")
            .expect_err("invalid key must fail");
        assert_eq!(err.code, NativeErrorCode::MalformedFrame);
    }
}
