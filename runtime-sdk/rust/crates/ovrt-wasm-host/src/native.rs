//! Native lane: execute a platform unit into a real control buffer.
//!
//! This is the reference half of the parity pair. The protocol mirrors the
//! browser host: the lane owns control-plane initialization and the input
//! epoch, the unit produces output or a controlled error, and the lane
//! records status, diagnostics, and the output epoch. A unit panic is caught
//! and converted to the controlled error path, never propagated.

#![forbid(unsafe_code)]

use std::panic::{catch_unwind, AssertUnwindSafe};

use ovrt_core::{IDX_INPUT_WRITTEN, IDX_OUTPUT_WRITTEN, INT_IDX_STATUS_CODE};
use ovrt_native::NativeBuffer;
use ovrt_unit::RuntimeUnit;

use crate::limits::ResourceLimits;
use crate::snapshot::BufferSnapshot;

/// Module version written when a caller does not supply one.
pub const DEFAULT_MODULE_VERSION: i32 = 1;
/// Status recorded for a successful exchange.
pub const STATUS_OK: i32 = 0;
/// Status recorded for a controlled unit error.
pub const STATUS_UNIT_ERROR: i32 = 1;

/// Builds the exact buffer state a host presents before execution.
///
/// Both lanes must start from this state; it is the shared prefix that makes
/// any later divergence attributable to the unit.
pub fn build_initial_buffer(input: &[u8], module_version: i32) -> Result<NativeBuffer, String> {
    let mut buffer = NativeBuffer::with_capacity();
    buffer.initialize_control_plane(module_version)?;
    buffer.write_input_bytes(input)?;
    buffer.add_epoch(IDX_INPUT_WRITTEN, 1)?;
    Ok(buffer)
}

/// Runs one unit against the native lane and captures final buffer state.
pub fn execute(
    unit: &dyn RuntimeUnit,
    input: &[u8],
    limits: &ResourceLimits,
    module_version: i32,
) -> Result<BufferSnapshot, String> {
    limits.validate()?;
    if !limits.admits_input(input.len()) {
        return Err(format!(
            "input payload exceeds the declared budget: {} > {}",
            input.len(),
            limits.max_input_bytes
        ));
    }
    let mut buffer = build_initial_buffer(input, module_version)?;
    match invoke(unit, input) {
        Ok(output) => write_success(&mut buffer, &output, limits)?,
        Err(message) => write_failure(&mut buffer, &message)?,
    }
    BufferSnapshot::capture(&buffer)
}

fn invoke(unit: &dyn RuntimeUnit, input: &[u8]) -> Result<Vec<u8>, String> {
    catch_unwind(AssertUnwindSafe(|| unit.run(input)))
        .unwrap_or_else(|_| Err("runtime unit panicked during execution".to_string()))
}

fn write_success(
    buffer: &mut NativeBuffer,
    output: &[u8],
    limits: &ResourceLimits,
) -> Result<(), String> {
    if !limits.admits_output(output.len()) {
        return Err(format!(
            "output payload exceeds the declared budget: {} > {}",
            output.len(),
            limits.max_output_bytes
        ));
    }
    buffer.write_output_bytes(output)?;
    buffer.set_header_int(INT_IDX_STATUS_CODE, STATUS_OK)?;
    buffer.add_epoch(IDX_OUTPUT_WRITTEN, 1)?;
    Ok(())
}

fn write_failure(buffer: &mut NativeBuffer, message: &str) -> Result<(), String> {
    // set_diagnostics_text increments IDX_DIAGNOSTICS_WRITTEN, matching the
    // guest protocol where the diagnostic bump precedes the output bump.
    buffer.set_diagnostics_text(message)?;
    buffer.set_header_int(INT_IDX_STATUS_CODE, STATUS_UNIT_ERROR)?;
    buffer.add_epoch(IDX_OUTPUT_WRITTEN, 1)?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use parity_fixture::ParityUnit;

    use super::*;

    fn limits() -> ResourceLimits {
        ResourceLimits::for_compute()
    }

    #[test]
    fn success_path_records_contract_state() {
        let snapshot =
            execute(&ParityUnit::default(), b"preview", &limits(), DEFAULT_MODULE_VERSION)
                .expect("native execution");
        assert_eq!(snapshot.status_code, STATUS_OK);
        assert_eq!(snapshot.input, b"preview");
        assert_eq!(snapshot.output.len(), snapshot.output_length as usize);
        assert_eq!(snapshot.epochs[ovrt_core::IDX_KERNEL_READY as usize], 1);
        assert_eq!(snapshot.epochs[IDX_INPUT_WRITTEN as usize], 1);
        assert_eq!(snapshot.epochs[IDX_OUTPUT_WRITTEN as usize], 1);
        assert_eq!(snapshot.epochs[ovrt_core::IDX_DIAGNOSTICS_WRITTEN as usize], 0);
        assert!(snapshot.diagnostics.is_empty());
    }

    #[test]
    fn error_path_is_controlled_and_recorded() {
        let snapshot = execute(&ParityUnit::default(), b"!nope", &limits(), DEFAULT_MODULE_VERSION)
            .expect("native execution");
        assert_eq!(snapshot.status_code, STATUS_UNIT_ERROR);
        assert_eq!(snapshot.diagnostics, "parity unit rejected the leading marker");
        assert_eq!(snapshot.output_length, 0);
        assert_eq!(snapshot.epochs[ovrt_core::IDX_DIAGNOSTICS_WRITTEN as usize], 1);
        assert_eq!(snapshot.epochs[IDX_OUTPUT_WRITTEN as usize], 1);
    }

    #[test]
    fn oversized_input_is_rejected_before_execution() {
        let limits = ResourceLimits { max_input_bytes: 4, ..ResourceLimits::for_compute() };
        let error = execute(&ParityUnit::default(), b"toolong", &limits, DEFAULT_MODULE_VERSION)
            .expect_err("budget must reject");
        assert!(error.contains("exceeds the declared budget"));
    }
}
