//! Native and WASM parity harness for runtime units.
//!
//! A unit that declares `supports_wasm` must produce identical control-buffer
//! state on both lanes. This crate is the evidence gate for that claim, as
//! required by `docs/rust_unit_guide.md` (§8). The native lane executes a
//! [`ovrt_unit::RuntimeUnit`] directly. The WASM lane instantiates a compiled
//! guest artifact under wasmtime with the same import surface the browser
//! host provides.
//!
//! # Guest contract
//!
//! A parity-eligible guest is a `wasm32-unknown-unknown` cdylib that exports
//! `memory` and `ovrt_unit_run(handle: u32) -> i32`. The host owns
//! control-plane initialization and the input epoch; the guest reads input,
//! writes output or diagnostics, sets the status header, then signals the
//! output epoch through the `ovrt_browser` helpers. Guests must not
//! re-initialize the control plane.
//!
//! # Determinism requirement
//!
//! Parity compares full buffer state. Units that consume wall-clock time or
//! randomness cannot match byte-for-byte unless both lanes see identical
//! values. The harness pins the guest clock through
//! [`ParityOptions::fixed_now_ms`]; the native unit must observe the same
//! value through its own injected clock (see the parity fixture for the
//! pattern). Counters that legitimately differ per lane are excluded by
//! name via [`ParityOptions::ignore_epoch_slots`] and
//! [`ParityOptions::ignore_header_ints`]. Payload regions are never
//! excludable: if payload bytes diverge, parity has failed.
//!
//! Randomness needs no pin when guests read it through the
//! `ovrt_fill_random` import: the browser fills it from crypto, while this
//! harness and the native fallbacks share one deterministic pattern, so the
//! two compared lanes always agree.
//!
//! # Feature gate
//!
//! The wasmtime lane requires `--features wasm-runtime`. Without it,
//! [`ParityHarness::compare_units`] reports a configuration error instead of
//! silently skipping evidence.
//!
//! # Examples
//!
//! ```no_run
//! use ovrt_unit::RuntimeUnit;
//! use ovrt_wasm_host::{ParityHarness, ResourceLimits};
//!
//! // unit: your type implementing ovrt_unit::RuntimeUnit
//! // artifact: bytes of the same unit built for wasm32-unknown-unknown
//! fn check(unit: &dyn RuntimeUnit, artifact: &[u8]) -> Result<(), String> {
//!     let report = ParityHarness::compare_units(
//!         unit,
//!         artifact,
//!         b"preview",
//!         &ResourceLimits::for_compute(),
//!     )?;
//!     if report.matched {
//!         Ok(())
//!     } else {
//!         Err(format!("parity failed: {:?}", report.divergences))
//!     }
//! }
//! ```

#![forbid(unsafe_code)]

pub mod limits;
pub mod native;
pub mod snapshot;
#[cfg(feature = "wasm-runtime")]
pub mod wasm;

use ovrt_unit::RuntimeUnit;

pub use limits::ResourceLimits;
pub use native::{execute as execute_native, DEFAULT_MODULE_VERSION};
pub use snapshot::{BufferSnapshot, Divergence};
#[cfg(feature = "wasm-runtime")]
pub use wasm::{execute as execute_wasm, WasmOutcome};

/// Clock value reported by `ovrt_get_now` when no pin is supplied.
///
/// A fixed default keeps time-reading guests repeatable across runs and
/// lanes. Override through [`ParityOptions::fixed_now_ms`] for a specific
/// instant.
pub const DEFAULT_PINNED_NOW_MS: f64 = 1_700_000_000_000.0;

/// Outcome of a two-lane comparison.
#[derive(Debug, Clone)]
pub struct ParityReport {
    /// True when every compared field agrees across lanes.
    pub matched: bool,
    /// Every disagreement, in contract order. Empty when matched.
    pub divergences: Vec<Divergence>,
    /// Final state of the native lane.
    pub native: BufferSnapshot,
    /// Final state of the WASM lane, when it ran.
    pub wasm: Option<BufferSnapshot>,
}

/// Volatility controls for a parity run.
///
/// Time-reading units need both lanes to observe the same instant: pass
/// [`Self::fixed_now_ms`] here and construct the native unit with the same
/// value through its injected clock. Counters that legitimately differ per
/// lane are excluded by name through the ignore lists. Payload regions are
/// never excludable; if payload bytes diverge, parity has failed.
#[derive(Debug, Clone)]
pub struct ParityOptions {
    /// Millisecond instant reported to guests by `ovrt_get_now`.
    pub fixed_now_ms: f64,
    /// Epoch slot indexes excluded from comparison.
    pub ignore_epoch_slots: Vec<u32>,
    /// Header integer indexes excluded from comparison.
    pub ignore_header_ints: Vec<u32>,
}

impl Default for ParityOptions {
    fn default() -> Self {
        Self {
            fixed_now_ms: DEFAULT_PINNED_NOW_MS,
            ignore_epoch_slots: Vec::new(),
            ignore_header_ints: Vec::new(),
        }
    }
}

impl From<&ParityOptions> for snapshot::DiffPolicy {
    fn from(options: &ParityOptions) -> Self {
        Self {
            ignore_epoch_slots: options.ignore_epoch_slots.clone(),
            ignore_header_ints: options.ignore_header_ints.clone(),
        }
    }
}

/// Entry point for parity evidence.
pub struct ParityHarness;

#[cfg(feature = "wasm-runtime")]
fn run_wasm_lane(
    artifact: &[u8],
    input: &[u8],
    limits: &ResourceLimits,
    env_now_ms: f64,
) -> Result<BufferSnapshot, String> {
    let outcome =
        wasm::execute_with_env(artifact, input, limits, DEFAULT_MODULE_VERSION, env_now_ms)?;
    Ok(outcome.snapshot)
}

#[cfg(not(feature = "wasm-runtime"))]
fn run_wasm_lane(
    _artifact: &[u8],
    _input: &[u8],
    _limits: &ResourceLimits,
    _env_now_ms: f64,
) -> Result<BufferSnapshot, String> {
    Err("the wasm-runtime feature is disabled; rebuild with --features wasm-runtime".to_string())
}

impl ParityHarness {
    /// Runs one input through both lanes and compares full buffer state.
    ///
    /// Fails fast on budget violations before either lane executes. Returns
    /// a configuration error when the `wasm-runtime` feature is disabled.
    pub fn compare_units(
        unit: &dyn RuntimeUnit,
        wasm_artifact: &[u8],
        input: &[u8],
        limits: &ResourceLimits,
    ) -> Result<ParityReport, String> {
        Self::compare_units_with_options(
            unit,
            wasm_artifact,
            input,
            limits,
            &ParityOptions::default(),
        )
    }

    /// Runs one comparison with explicit volatility controls.
    ///
    /// The pinned clock flows to the guest imports; the native unit must
    /// observe the same value through its own injected clock. Exclusions in
    /// `options` silence only the named epoch slots and header integers.
    pub fn compare_units_with_options(
        unit: &dyn RuntimeUnit,
        wasm_artifact: &[u8],
        input: &[u8],
        limits: &ResourceLimits,
        options: &ParityOptions,
    ) -> Result<ParityReport, String> {
        let native_snapshot = native::execute(unit, input, limits, DEFAULT_MODULE_VERSION)?;
        let wasm_snapshot = run_wasm_lane(wasm_artifact, input, limits, options.fixed_now_ms)?;
        let policy = snapshot::DiffPolicy::from(options);
        let divergences = snapshot::diff_with(&native_snapshot, &wasm_snapshot, &policy);
        Ok(ParityReport {
            matched: divergences.is_empty(),
            divergences,
            native: native_snapshot,
            wasm: Some(wasm_snapshot),
        })
    }

    /// Compares two already-captured snapshots. Always available.
    pub fn compare_snapshots(native: BufferSnapshot, wasm: BufferSnapshot) -> ParityReport {
        let divergences = snapshot::diff(&native, &wasm);
        ParityReport { matched: divergences.is_empty(), divergences, native, wasm: Some(wasm) }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use parity_fixture::ParityUnit;

    #[test]
    fn compare_units_reports_lane_configuration() {
        let error = ParityHarness::compare_units(
            &ParityUnit::default(),
            &[],
            b"preview",
            &ResourceLimits::for_compute(),
        )
        .expect_err("empty artifact or disabled feature must fail");
        #[cfg(feature = "wasm-runtime")]
        assert!(error.contains("invalid wasm artifact"));
        #[cfg(not(feature = "wasm-runtime"))]
        assert!(error.contains("wasm-runtime feature is disabled"));
    }
}
