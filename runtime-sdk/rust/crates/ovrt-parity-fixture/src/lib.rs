//! Reference guest unit for the native and WASM parity lanes.
//!
//! One implementation serves both targets. The rlib build is the native
//! reference the harness executes directly. The cdylib build exports
//! `ovrt_unit_run` for a WASM host. Parity means both produce identical
//! control-buffer state for identical input.

// deny, not forbid: the guest entrypoint must export a stable linker symbol,
// which rustc classifies as unsafe. The exception is scoped to that one
// declaration; everything else stays unsafe-free.
#![deny(unsafe_code)]

use ovrt_core::{RuntimeRole, RuntimeUnitDescriptor, INT_IDX_STATUS_CODE};
use ovrt_unit::RuntimeUnit;

/// Registry identifier advertised by the descriptor.
pub const UNIT_ID: &str = "preview.parity.v1";
/// Module version written into the control plane during host initialization.
pub const MODULE_VERSION: i32 = 1;
/// Input prefix that makes the unit take its controlled error path.
pub const REJECT_MARKER: u8 = b'!';
/// Input prefix that makes the unit stamp its observed clock into output.
///
/// The eight bytes appended to the transformed payload are the f64 bit
/// pattern of the millisecond instant the unit observed. Both lanes must
/// observe the same pinned value for parity to hold.
pub const CLOCK_MARKER: u8 = b'@';
/// Clock value used when no explicit pin is supplied.
pub const DEFAULT_UNIT_NOW_MS: f64 = 1_700_000_000_000.0;
/// Exit code returned when the buffer handle cannot be resolved.
pub const STATUS_BAD_HANDLE: i32 = 2;
/// Exit code returned when a buffer write fails validation.
pub const STATUS_BUFFER_FAULT: i32 = 3;

const TRANSFORM_KEY: u8 = 0x5A;

/// The deterministic transform under parity test.
///
/// Errors are controlled: a leading reject marker produces an error string
/// instead of output. A leading clock marker stamps the observed instant
/// into the output, which is only reproducible when both lanes are pinned
/// to the same value.
pub fn compute_with_env(input: &[u8], now_ms: f64) -> Result<Vec<u8>, String> {
    match input.first() {
        Some(&REJECT_MARKER) => Err("parity unit rejected the leading marker".to_string()),
        Some(&CLOCK_MARKER) => {
            let mut output: Vec<u8> = input[1..].iter().map(|byte| byte ^ TRANSFORM_KEY).collect();
            output.extend_from_slice(&now_ms.to_bits().to_le_bytes());
            Ok(output)
        }
        _ => Ok(input.iter().map(|byte| byte ^ TRANSFORM_KEY).collect()),
    }
}

/// Descriptor declaring both lane capabilities.
pub fn descriptor() -> RuntimeUnitDescriptor {
    RuntimeUnitDescriptor {
        unit_id: UNIT_ID.to_string(),
        role: RuntimeRole::Compute,
        input_schema: "preview/v1/bytes".to_string(),
        output_schema: "preview/v1/bytes".to_string(),
        supports_wasm: true,
        supports_native: true,
        requires_shared_memory: false,
        supports_gpu: false,
        max_concurrency: 1,
    }
}

/// Native-side reference implementing the platform unit trait.
///
/// The clock is injected, not read: time-dependent parity requires the
/// native side to observe exactly the instant the harness pins for guests.
#[derive(Debug, Clone)]
pub struct ParityUnit {
    /// Millisecond instant this unit reports in clock-marker mode.
    pub now_ms: f64,
}

impl Default for ParityUnit {
    fn default() -> Self {
        Self { now_ms: DEFAULT_UNIT_NOW_MS }
    }
}

impl ParityUnit {
    /// Builds a unit observing a specific pinned instant.
    pub fn pinned(now_ms: f64) -> Self {
        Self { now_ms }
    }
}

impl RuntimeUnit for ParityUnit {
    fn descriptor(&self) -> RuntimeUnitDescriptor {
        descriptor()
    }

    fn run(&self, input: &[u8]) -> Result<Vec<u8>, String> {
        compute_with_env(input, self.now_ms)
    }
}

/// WASM guest entrypoint invoked by a host after it fills the input region.
///
/// Protocol: the host owns control-plane initialization and the input epoch.
/// The guest reads input, writes output or diagnostics, sets the status
/// header, then signals the output epoch. It must not re-initialize the
/// control plane, because that would erase the input length.
#[allow(unsafe_code)]
#[unsafe(no_mangle)]
pub extern "C" fn ovrt_unit_run(handle: u32) -> i32 {
    let buffer = match ovrt_browser::SafeBuffer::new(handle) {
        Ok(buffer) => buffer,
        Err(_) => return STATUS_BAD_HANDLE,
    };
    let input = match buffer.read_input_bytes() {
        Ok(input) => input,
        Err(_) => return STATUS_BAD_HANDLE,
    };
    // The guest observes the clock through the host import, which a parity
    // harness pins and a browser fills with real time.
    let now_ms = ovrt_browser::js_interop::get_now();
    match compute_with_env(&input, now_ms) {
        Ok(output) => write_success(&buffer, &output),
        Err(message) => write_failure(&buffer, &message),
    }
}

fn write_success(buffer: &ovrt_browser::SafeBuffer, output: &[u8]) -> i32 {
    if buffer.write_output_bytes(output).is_err() {
        return STATUS_BUFFER_FAULT;
    }
    if buffer.set_header_int(INT_IDX_STATUS_CODE, 0).is_err() {
        return STATUS_BUFFER_FAULT;
    }
    ovrt_browser::signal::mark_output_written(*buffer);
    0
}

fn write_failure(buffer: &ovrt_browser::SafeBuffer, message: &str) -> i32 {
    if buffer.write_diagnostic_bytes(message.as_bytes()).is_err() {
        return STATUS_BUFFER_FAULT;
    }
    ovrt_browser::signal::mark_diagnostics_written(*buffer);
    if buffer.set_header_int(INT_IDX_STATUS_CODE, 1).is_err() {
        return STATUS_BUFFER_FAULT;
    }
    ovrt_browser::signal::mark_output_written(*buffer);
    1
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn descriptor_declares_both_lanes() {
        let validated = descriptor();
        assert!(validated.validate().is_ok());
        assert!(validated.supports_wasm);
        assert!(validated.supports_native);
    }

    #[test]
    fn compute_is_deterministic_and_reversible() {
        let once = compute_with_env(b"preview", DEFAULT_UNIT_NOW_MS).expect("compute");
        assert_eq!(once, compute_with_env(b"preview", DEFAULT_UNIT_NOW_MS).expect("compute"));
        let restored: Vec<u8> = once.iter().map(|byte| byte ^ TRANSFORM_KEY).collect();
        assert_eq!(restored, b"preview");
    }

    #[test]
    fn leading_marker_takes_the_error_path() {
        assert!(compute_with_env(b"!nope", DEFAULT_UNIT_NOW_MS).is_err());
    }

    #[test]
    fn clock_marker_stamps_the_observed_instant() {
        let output = compute_with_env(b"@tick", 42.0).expect("clocked compute");
        let (body, stamp) = output.split_at(output.len() - 8);
        assert_eq!(body, b"tick".iter().map(|b| b ^ TRANSFORM_KEY).collect::<Vec<u8>>());
        let observed = f64::from_bits(u64::from_le_bytes(stamp.try_into().expect("stamp")));
        assert_eq!(observed, 42.0);
    }
}
