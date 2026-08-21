//! Parity lane benchmarks: what native and WASM exchanges actually cost.
//!
//! Evidence for the boundary-cost question in `docs/rust_unit_guide.md` §1:
//! a kernel must be worth the trip. The numbers answer three questions —
//! what a native exchange costs, what a warm WASM exchange costs on top,
//! and how much of the one-shot cost is instantiation rather than work.
//!
//! Run: `cargo bench -p ovrt-wasm-host --features wasm-runtime`

#![forbid(unsafe_code)]

#[cfg(not(feature = "wasm-runtime"))]
fn main() {
    // Every measured path lives behind the wasm-runtime feature.
    eprintln!("skip parity bench: build with --features wasm-runtime");
}

#[cfg(feature = "wasm-runtime")]
fn main() {
    parity_bench::run();
}

// Everything measurable sits in this module so the feature-off build above
// stays a valid empty binary.
#[cfg(feature = "wasm-runtime")]
mod parity_bench {
    use std::path::PathBuf;
    use std::process::Command;
    use std::sync::OnceLock;
    use std::time::Instant;

    use ovrt_wasm_host::wasm::WasmGuest;
    use ovrt_wasm_host::{native, ResourceLimits, DEFAULT_PINNED_NOW_MS};
    use parity_fixture::ParityUnit;

    const TARGET: &str = "wasm32-unknown-unknown";
    const WARM_ITERS: u32 = 2_000;
    const NATIVE_ITERS: u32 = 20_000;

    pub fn bench_ns(name: &str, iters: u32, mut f: impl FnMut()) {
        // One untimed call keeps first-touch costs out of the steady state.
        f();
        let start = Instant::now();
        for _ in 0..iters {
            f();
        }
        let ns = start.elapsed().as_nanos() as f64 / f64::from(iters);
        println!("{name:<46} {ns:>12.2} ns/op");
    }

    pub fn bench_once(name: &str, mut f: impl FnMut()) {
        let start = Instant::now();
        f();
        println!("{name:<46} {:>12.2} ms/op", start.elapsed().as_secs_f64() * 1e3);
    }

    pub fn workspace_root() -> PathBuf {
        PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("..").join("..")
    }

    /// Builds the fixture guest once; exits quietly when the target is absent.
    pub fn guest_artifact() -> Option<Vec<u8>> {
        static ARTIFACT: OnceLock<Option<Vec<u8>>> = OnceLock::new();
        ARTIFACT
            .get_or_init(|| {
                let installed = Command::new("rustup")
                    .args(["target", "list", "--installed"])
                    .output()
                    .map(|output| String::from_utf8_lossy(&output.stdout).contains(TARGET))
                    .unwrap_or(false);
                if !installed {
                    eprintln!("skip wasm lanes: {TARGET} is not installed");
                    return None;
                }
                let root = workspace_root();
                let status = Command::new("cargo")
                    .args(["build", "-p", "ovrt-parity-fixture", "--lib", "--target", TARGET])
                    .current_dir(&root)
                    .status()
                    .expect("cargo must be runnable");
                assert!(status.success(), "fixture wasm build failed");
                let path =
                    root.join("target").join(TARGET).join("debug").join("parity_fixture.wasm");
                Some(std::fs::read(path).expect("artifact readable"))
            })
            .clone()
    }

    pub fn run() {
        let limits = ResourceLimits::for_compute();
        let input = b"benchmark-input";

        bench_ns("native execute (unit + buffer + snapshot)", NATIVE_ITERS, || {
            let snapshot = native::execute(&ParityUnit::default(), input, &limits, 1)
                .expect("native exchange");
            assert_eq!(snapshot.status_code, 0);
        });

        let Some(artifact) = guest_artifact() else { return };

        bench_once("wasm one-shot (engine + compile + instantiate)", || {
            ovrt_wasm_host::execute_wasm(&artifact, input, &limits, 1).expect("one-shot exchange");
        });

        bench_once("wasm guest compile (engine + instantiate)", || {
            WasmGuest::compile(&artifact, &limits, DEFAULT_PINNED_NOW_MS).expect("compile");
        });

        let mut guest =
            WasmGuest::compile(&artifact, &limits, DEFAULT_PINNED_NOW_MS).expect("guest");
        bench_ns("wasm warm exchange (reset + call + snapshot)", WARM_ITERS, || {
            let outcome = guest.exchange(input, 1).expect("warm exchange");
            assert_eq!(outcome.guest_status, 0);
        });

        let baseline =
            native::execute(&ParityUnit::default(), input, &limits, 1).expect("baseline");
        let policy = ovrt_wasm_host::snapshot::DiffPolicy::strict();
        bench_ns("snapshot diff (strict)", WARM_ITERS * 10, || {
            let divergences = ovrt_wasm_host::snapshot::diff_with(&baseline, &baseline, &policy);
            assert!(divergences.is_empty());
        });

        println!("\nnote: warm-exchange cost is the honest wasm-lane number;");
        println!("one-shot includes engine startup and instantiation, which a");
        println!("long-lived host amortises away.");
    }
}
