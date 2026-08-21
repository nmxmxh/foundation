//! Full control-buffer state capture and comparison.
//!
//! Parity compares the whole 4 KiB contract surface: header integers, every
//! epoch slot, both payload regions as declared by their header lengths, and
//! the diagnostics text. Comparing payload bytes alone would miss status or
//! epoch divergence, which is exactly the class of bug a parity gate exists
//! to catch.

#![forbid(unsafe_code)]

use ovrt_core::{
    EPOCH_SLOT_COUNT, HEADER_INT_COUNT, INT_IDX_INPUT_LENGTH, INT_IDX_MODULE_VERSION,
    INT_IDX_OUTPUT_LENGTH, INT_IDX_SCHEMA_VERSION, INT_IDX_STATUS_CODE,
};
use ovrt_native::NativeBuffer;

const EPOCH_NAMES: [&str; EPOCH_SLOT_COUNT as usize] = [
    "kernel_ready",
    "input_written",
    "output_written",
    "output_consumed",
    "panic_state",
    "diagnostics_written",
    "runtime_tick",
    "visibility_state",
    "epoch_08",
    "epoch_09",
    "epoch_10",
    "epoch_11",
    "epoch_12",
    "epoch_13",
    "epoch_14",
    "epoch_15",
];

/// An immutable view of one lane's final control-buffer state.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct BufferSnapshot {
    pub schema_version: i32,
    pub module_version: i32,
    pub status_code: i32,
    pub input_length: i32,
    pub output_length: i32,
    pub header_ints: [i32; HEADER_INT_COUNT as usize],
    pub epochs: [i32; EPOCH_SLOT_COUNT as usize],
    pub input: Vec<u8>,
    pub output: Vec<u8>,
    pub diagnostics: String,
}

impl BufferSnapshot {
    /// Captures the complete contract surface of a finished buffer.
    pub fn capture(buffer: &NativeBuffer) -> Result<Self, String> {
        let mut header_ints = [0i32; HEADER_INT_COUNT as usize];
        for (index, slot) in header_ints.iter_mut().enumerate() {
            *slot = buffer.header_int(index as u32)?;
        }
        let mut epochs = [0i32; EPOCH_SLOT_COUNT as usize];
        for (index, slot) in epochs.iter_mut().enumerate() {
            *slot = buffer.load_epoch(index as u32);
        }
        Ok(Self {
            schema_version: buffer.header_int(INT_IDX_SCHEMA_VERSION)?,
            module_version: buffer.header_int(INT_IDX_MODULE_VERSION)?,
            status_code: buffer.header_int(INT_IDX_STATUS_CODE)?,
            input_length: buffer.header_int(INT_IDX_INPUT_LENGTH)?,
            output_length: buffer.header_int(INT_IDX_OUTPUT_LENGTH)?,
            header_ints,
            epochs,
            input: buffer.read_input_bytes()?,
            output: buffer.read_output_bytes()?,
            diagnostics: buffer.diagnostics_text(),
        })
    }
}

/// Names fields a comparison may skip.
///
/// Exclusions exist for legitimately volatile state: counters that advance
/// per lane, or values derived from an environment the harness does not
/// control. Payload regions are never excludable; a parity gate that skips
/// payload bytes proves nothing.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct DiffPolicy {
    /// Epoch slot indexes excluded from comparison.
    pub ignore_epoch_slots: Vec<u32>,
    /// Header integer indexes excluded from comparison.
    pub ignore_header_ints: Vec<u32>,
}

impl DiffPolicy {
    /// A policy with no exclusions: every field must agree.
    pub fn strict() -> Self {
        Self::default()
    }

    fn excludes_epoch(&self, index: usize) -> bool {
        self.ignore_epoch_slots.contains(&(index as u32))
    }

    fn excludes_header(&self, index: usize) -> bool {
        self.ignore_header_ints.contains(&(index as u32))
    }
}

/// One field whose native and WASM values disagree.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Divergence {
    pub field: String,
    pub native: String,
    pub wasm: String,
}

impl Divergence {
    fn new(field: impl Into<String>, native: impl Into<String>, wasm: impl Into<String>) -> Self {
        Self { field: field.into(), native: native.into(), wasm: wasm.into() }
    }
}

/// Compares two snapshots and returns every divergence, in contract order.
///
/// Empty means byte-identical state across all compared fields.
pub fn diff(native: &BufferSnapshot, wasm: &BufferSnapshot) -> Vec<Divergence> {
    diff_with(native, wasm, &DiffPolicy::strict())
}

/// Compares two snapshots under a policy that may exclude volatile fields.
pub fn diff_with(
    native: &BufferSnapshot,
    wasm: &BufferSnapshot,
    policy: &DiffPolicy,
) -> Vec<Divergence> {
    let mut divergences = Vec::new();
    if native.schema_version != wasm.schema_version {
        divergences.push(Divergence::new(
            "schema_version",
            native.schema_version.to_string(),
            wasm.schema_version.to_string(),
        ));
    }
    if native.module_version != wasm.module_version {
        divergences.push(Divergence::new(
            "module_version",
            native.module_version.to_string(),
            wasm.module_version.to_string(),
        ));
    }
    if native.status_code != wasm.status_code {
        divergences.push(Divergence::new(
            "status_code",
            native.status_code.to_string(),
            wasm.status_code.to_string(),
        ));
    }
    compare_header_ints(native, wasm, policy, &mut divergences);
    compare_epochs(native, wasm, policy, &mut divergences);
    compare_region("input", &native.input, &wasm.input, &mut divergences);
    compare_region("output", &native.output, &wasm.output, &mut divergences);
    if native.diagnostics != wasm.diagnostics {
        divergences.push(Divergence::new(
            "diagnostics",
            native.diagnostics.clone(),
            wasm.diagnostics.clone(),
        ));
    }
    divergences
}

fn compare_header_ints(
    native: &BufferSnapshot,
    wasm: &BufferSnapshot,
    policy: &DiffPolicy,
    divergences: &mut Vec<Divergence>,
) {
    for (index, (left, right)) in native.header_ints.iter().zip(&wasm.header_ints).enumerate() {
        if left != right && !policy.excludes_header(index) {
            divergences.push(Divergence::new(
                format!("header[{index}]"),
                left.to_string(),
                right.to_string(),
            ));
        }
    }
}

fn compare_epochs(
    native: &BufferSnapshot,
    wasm: &BufferSnapshot,
    policy: &DiffPolicy,
    divergences: &mut Vec<Divergence>,
) {
    for (index, (left, right)) in native.epochs.iter().zip(&wasm.epochs).enumerate() {
        if left != right && !policy.excludes_epoch(index) {
            let name = EPOCH_NAMES.get(index).copied().unwrap_or("unknown");
            divergences.push(Divergence::new(
                format!("epoch.{name}"),
                left.to_string(),
                right.to_string(),
            ));
        }
    }
}

fn compare_region(name: &str, native: &[u8], wasm: &[u8], divergences: &mut Vec<Divergence>) {
    if native == wasm {
        return;
    }
    let first = native
        .iter()
        .zip(wasm.iter())
        .position(|(left, right)| left != right)
        .unwrap_or_else(|| native.len().min(wasm.len()));
    divergences.push(Divergence::new(
        format!("{name}[{first}]"),
        format!("len={} byte=0x{:02x}", native.len(), native.get(first).copied().unwrap_or(0)),
        format!("len={} byte=0x{:02x}", wasm.len(), wasm.get(first).copied().unwrap_or(0)),
    ));
}

#[cfg(test)]
mod tests {
    use super::*;

    fn snapshot(status: i32, output: &[u8], output_epoch: i32) -> BufferSnapshot {
        BufferSnapshot {
            schema_version: 1,
            module_version: 1,
            status_code: status,
            input_length: 3,
            output_length: output.len() as i32,
            header_ints: [1, 3, output.len() as i32, status, 0, 1, 0, 0],
            epochs: {
                let mut epochs = [0i32; EPOCH_SLOT_COUNT as usize];
                epochs[0] = 1;
                epochs[1] = 1;
                epochs[2] = output_epoch;
                epochs
            },
            input: b"abc".to_vec(),
            output: output.to_vec(),
            diagnostics: String::new(),
        }
    }

    #[test]
    fn identical_states_produce_no_divergences() {
        let left = snapshot(0, b"xyz", 1);
        let right = snapshot(0, b"xyz", 1);
        assert!(diff(&left, &right).is_empty());
    }

    #[test]
    fn every_field_class_is_checked() {
        let mut right = snapshot(0, b"xyz", 1);
        right.status_code = 1;
        right.header_ints[5] = 2;
        right.epochs[6] = 9;
        right.output = b"xy!".to_vec();
        right.diagnostics = "drift".to_string();

        let fields: Vec<String> =
            diff(&snapshot(0, b"xyz", 1), &right).into_iter().map(|d| d.field).collect();
        assert!(fields.contains(&"status_code".to_string()));
        assert!(fields.contains(&"header[5]".to_string()));
        assert!(fields.contains(&"epoch.runtime_tick".to_string()));
        assert!(fields.contains(&"output[2]".to_string()));
        assert!(fields.contains(&"diagnostics".to_string()));
    }

    #[test]
    fn length_mismatch_is_reported_at_the_boundary() {
        let left = snapshot(0, b"abcd", 1);
        let right = snapshot(0, b"abc", 1);
        let divergences = diff(&left, &right);
        assert_eq!(divergences.len(), 2, "length header and region must both diverge");
        assert_eq!(divergences[0].field, "header[2]");
        assert_eq!(divergences[1].field, "output[3]");
    }

    #[test]
    fn policy_exclusions_silence_only_the_named_fields() {
        let mut right = snapshot(0, b"xyz", 1);
        right.epochs[6] = 9;
        right.header_ints[5] = 2;
        right.output = b"xy!".to_vec();

        let policy = DiffPolicy { ignore_epoch_slots: vec![6], ignore_header_ints: vec![5] };
        let divergences = diff_with(&snapshot(0, b"xyz", 1), &right, &policy);
        assert_eq!(divergences.len(), 1, "only the payload divergence may remain");
        assert_eq!(divergences[0].field, "output[2]");

        assert_eq!(diff(&snapshot(0, b"xyz", 1), &right).len(), 3, "strict sees all three");
    }
}
