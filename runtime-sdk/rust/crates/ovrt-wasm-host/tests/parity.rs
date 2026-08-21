//! Parity evidence tests.
//!
//! Native-lane tests always run. WASM-lane tests build the parity fixture
//! for `wasm32-unknown-unknown` and execute it under wasmtime; they skip
//! with a notice when the target is not installed, and fail loudly when the
//! target exists but parity breaks.

#![forbid(unsafe_code)]

use ovrt_wasm_host::native;
use ovrt_wasm_host::{ParityHarness, ResourceLimits};
use parity_fixture::{ParityUnit, MODULE_VERSION};

fn limits() -> ResourceLimits {
    ResourceLimits::for_compute()
}

#[test]
fn native_lane_meets_the_contract_on_both_paths() {
    let ok = native::execute(&ParityUnit::default(), b"preview", &limits(), MODULE_VERSION)
        .expect("success path");
    assert_eq!(ok.status_code, 0);
    assert_eq!(ok.output, b"preview".iter().map(|b| b ^ 0x5A).collect::<Vec<u8>>());

    let failed = native::execute(&ParityUnit::default(), b"!stop", &limits(), MODULE_VERSION)
        .expect("error path");
    assert_eq!(failed.status_code, 1);
    assert!(!failed.diagnostics.is_empty());
}

#[test]
fn snapshot_comparison_flags_any_drift() {
    let baseline = native::execute(&ParityUnit::default(), b"preview", &limits(), MODULE_VERSION)
        .expect("baseline");
    let mut drifted = baseline.clone();
    drifted.epochs[2] += 1;
    let report = ParityHarness::compare_snapshots(baseline, drifted);
    assert!(!report.matched);
    assert_eq!(report.divergences.len(), 1);
}

#[test]
fn budgets_reject_oversized_payloads_before_execution() {
    let tight = ResourceLimits { max_input_bytes: 4, ..ResourceLimits::for_compute() };
    let error = native::execute(&ParityUnit::default(), b"toolong", &tight, MODULE_VERSION)
        .expect_err("budget must reject");
    assert!(error.contains("exceeds the declared budget"));
}

#[cfg(feature = "wasm-runtime")]
mod wasm_lane {
    use super::*;
    use ovrt_wasm_host::wasm;
    use std::path::PathBuf;
    use std::process::Command;
    use std::sync::OnceLock;

    const TARGET: &str = "wasm32-unknown-unknown";
    const ARTIFACT: &str = "parity_fixture.wasm";
    const DEFAULT_PINNED_NOW_MS: f64 = ovrt_wasm_host::DEFAULT_PINNED_NOW_MS;

    fn workspace_root() -> PathBuf {
        PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("..").join("..")
    }

    fn wasm_target_installed() -> bool {
        Command::new("rustup")
            .args(["target", "list", "--installed"])
            .output()
            .map(|output| String::from_utf8_lossy(&output.stdout).contains(TARGET))
            .unwrap_or(false)
    }

    /// Builds the fixture guest once per test process and shares the bytes.
    ///
    /// Returns None when the wasm32 target is absent so environments without
    /// cross-compilation still pass their remaining evidence.
    fn guest_artifact() -> Option<&'static Vec<u8>> {
        static ARTIFACT_BYTES: OnceLock<Option<Vec<u8>>> = OnceLock::new();
        ARTIFACT_BYTES
            .get_or_init(|| {
                if !wasm_target_installed() {
                    eprintln!("skipping wasm parity: {TARGET} is not installed");
                    return None;
                }
                let root = workspace_root();
                let status = Command::new("cargo")
                    .args(["build", "-p", "ovrt-parity-fixture", "--lib", "--target", TARGET])
                    .current_dir(&root)
                    .status()
                    .expect("cargo must be runnable to build the parity fixture");
                assert!(status.success(), "fixture wasm build failed; this is a real regression");
                let artifact = root.join("target").join(TARGET).join("debug").join(ARTIFACT);
                Some(std::fs::read(artifact).expect("built fixture artifact must be readable"))
            })
            .as_ref()
    }

    #[test]
    fn both_lanes_produce_identical_state() {
        let Some(artifact) = guest_artifact() else { return };
        for input in [&b"preview"[..], &b""[..], &[0u8, 255, 7][..]] {
            let report =
                ParityHarness::compare_units(&ParityUnit::default(), artifact, input, &limits())
                    .unwrap_or_else(|error| {
                        panic!("parity exchange failed for {input:?}: {error}")
                    });
            assert!(report.matched, "divergences: {:?}", report.divergences);
            assert!(report.wasm.is_some());
        }
    }

    #[test]
    fn controlled_error_path_matches_across_lanes() {
        let Some(artifact) = guest_artifact() else { return };
        let report =
            ParityHarness::compare_units(&ParityUnit::default(), artifact, b"!rejected", &limits())
                .expect("error-path exchange");
        assert!(report.matched, "divergences: {:?}", report.divergences);
        let state = report.wasm.expect("wasm snapshot");
        assert_eq!(state.status_code, 1);
        assert_eq!(state.diagnostics, "parity unit rejected the leading marker");
        assert_eq!(state.output_length, 0);
    }

    #[test]
    fn fuel_bound_stops_runaway_guests_without_hanging() {
        let Some(artifact) = guest_artifact() else { return };
        let starved = ResourceLimits { max_fuel: 1, ..ResourceLimits::for_compute() };
        let error = wasm::execute(artifact, b"preview", &starved, MODULE_VERSION)
            .expect_err("one unit of fuel cannot complete an exchange");
        assert!(error.contains("fuel"), "unexpected error: {error}");
    }

    #[test]
    fn watchdog_does_not_trip_fast_exchanges() {
        // A one-millisecond deadline must not false-positive on a guest that
        // finishes immediately; the exchange must succeed.
        let Some(artifact) = guest_artifact() else { return };
        let rushed = ResourceLimits { timeout_ms: 1, ..ResourceLimits::for_compute() };
        let outcome =
            wasm::execute(artifact, b"tiny", &rushed, MODULE_VERSION).expect("fast exchange");
        assert_eq!(outcome.guest_status, 0);
        assert!(outcome.fuel_consumed > 0, "fuel accounting must be active");
    }

    #[test]
    fn memory_ceiling_rejects_guests_above_the_limit() {
        let Some(artifact) = guest_artifact() else { return };
        let cramped = ResourceLimits { max_memory_pages: 1, ..ResourceLimits::for_compute() };
        // A std-linked guest reserves well over one 64 KiB page, so the
        // limiter must refuse instantiation instead of admitting it.
        let error = wasm::execute(artifact, b"tiny", &cramped, MODULE_VERSION)
            .expect_err("one page cannot hold a std guest");
        assert!(
            error.contains("memory limits") || error.contains("memory"),
            "unexpected error: {error}"
        );
    }

    #[test]
    fn volatile_clock_unit_matches_when_both_lanes_are_pinned() {
        let Some(artifact) = guest_artifact() else { return };
        let instant = 1_900_000_000_000.0;
        let unit = ParityUnit::pinned(instant);
        let options = ovrt_wasm_host::ParityOptions {
            fixed_now_ms: instant,
            ..ovrt_wasm_host::ParityOptions::default()
        };
        let report = ParityHarness::compare_units_with_options(
            &unit,
            artifact,
            b"@stamp-me",
            &limits(),
            &options,
        )
        .expect("pinned-clock exchange");
        assert!(report.matched, "divergences: {:?}", report.divergences);

        // The stamp must actually be in the output, not silently skipped.
        let expected_stamp = instant.to_bits().to_le_bytes();
        let output = &report.wasm.expect("wasm snapshot").output;
        assert_eq!(&output[output.len() - 8..], &expected_stamp);
    }

    #[test]
    fn clock_drift_between_lanes_is_detected_not_hidden() {
        let Some(artifact) = guest_artifact() else { return };
        let native_snapshot =
            native::execute(&ParityUnit::pinned(1.0e12), b"@x", &limits(), MODULE_VERSION)
                .expect("native side at T");
        let wasm_outcome =
            wasm::execute_with_env(artifact, b"@x", &limits(), MODULE_VERSION, 1.0e12 + 7.0)
                .expect("guest side at T+7ms");
        let divergences = ovrt_wasm_host::snapshot::diff(&native_snapshot, &wasm_outcome.snapshot);
        assert!(
            divergences.iter().any(|d| d.field.starts_with("output[")),
            "clock drift must surface as an output divergence: {:?}",
            divergences
        );
    }

    #[test]
    fn epoch_exclusions_silence_only_declared_slots() {
        let baseline =
            native::execute(&ParityUnit::default(), b"preview", &limits(), MODULE_VERSION)
                .expect("baseline");
        let mut drifted = baseline.clone();
        drifted.epochs[6] += 3; // IDX_RUNTIME_TICK: a per-lane counter
        let options = ovrt_wasm_host::ParityOptions {
            ignore_epoch_slots: vec![6],
            ..ovrt_wasm_host::ParityOptions::default()
        };
        let policy = ovrt_wasm_host::snapshot::DiffPolicy::from(&options);
        let quiet = ovrt_wasm_host::snapshot::diff_with(&baseline, &drifted, &policy);
        assert!(quiet.is_empty(), "excluded slot must not diverge: {:?}", quiet);
        assert_eq!(ovrt_wasm_host::snapshot::diff(&baseline, &drifted).len(), 1);
    }

    #[test]
    fn a_warm_guest_matches_the_one_shot_path_across_repeated_exchanges() {
        use ovrt_wasm_host::wasm::WasmGuest;

        let Some(artifact) = guest_artifact() else { return };
        let mut guest =
            WasmGuest::compile(artifact, &limits(), DEFAULT_PINNED_NOW_MS).expect("warm guest");

        for input in [&b"preview"[..], &b""[..], &b"second-call"[..]] {
            let warm = guest.exchange(input, MODULE_VERSION).expect("warm exchange");
            let one_shot =
                wasm::execute(artifact, input, &limits(), MODULE_VERSION).expect("one-shot");
            assert_eq!(
                warm.snapshot, one_shot.snapshot,
                "warm and one-shot paths disagree for {input:?}"
            );
            assert_eq!(warm.guest_status, 0);
            assert!(warm.fuel_consumed > 0);
        }
    }
}
