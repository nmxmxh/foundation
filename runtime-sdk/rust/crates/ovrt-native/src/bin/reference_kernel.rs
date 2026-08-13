//! A minimal kernel that speaks every process transport, for measuring them.
//!
//! Foundation had no runnable kernel of its own: its Go tests re-exec the test
//! binary as a fake, which is fine for protocol coverage and useless for
//! latency — the fake's pages are already resident and it never crosses a real
//! process boundary the way a spawned child does. Every transport number the
//! project had therefore came from an application repo, which is the wrong place
//! for evidence about foundation's own transport.
//!
//! This binary is that missing counterpart. It registers one echo unit and
//! hands off to `serve_transport`, so the transport under test is chosen
//! entirely by `OVRT_RUNTIME_TRANSPORT` and nothing else differs between runs.
//!
//! ```bash
//! cargo build --release -p ovrt-native --bin reference_kernel
//! OVRT_REFERENCE_KERNEL=$PWD/target/release/reference_kernel \
//!   go test ./runtimehost/ -run '^$' -bench 'Transport' -benchtime 2000x
//! ```
//!
//! Two units, and the second one matters as much as the first.
//!
//! `runtime.echo` is trivial, because the question it answers is what a
//! crossing costs and a kernel doing real work would bury it. But an echo unit
//! **flatters the epoch doorbell by construction**: the doorbell wins by
//! spinning until the reply lands, and with zero compute the reply always lands
//! inside the spin. Benchmarking only against echo produces a number that is
//! true and misleading, and it is how a transport gets adopted into a pool it
//! makes slower.
//!
//! `runtime.busy` therefore burns a caller-specified number of microseconds
//! before replying, so the crossover can be measured here rather than
//! discovered downstream. Busy rather than asleep on purpose: a real kernel
//! holds a core while it computes, and a sleeping one would let the host's spin
//! share the core it is contending for.

use std::collections::BTreeMap;
use std::sync::Arc;

use ovrt_core::{RuntimeRole, RuntimeUnitDescriptor};
use ovrt_native::{serve_transport, NativeRuntimeHost};
use ovrt_unit::RuntimeUnit;

struct EchoUnit;

impl RuntimeUnit for EchoUnit {
    fn descriptor(&self) -> RuntimeUnitDescriptor {
        RuntimeUnitDescriptor {
            unit_id: "runtime.echo".to_string(),
            role: RuntimeRole::Compute,
            input_schema: "foundation/v1/envelope.capnp".to_string(),
            output_schema: "foundation/v1/envelope.capnp".to_string(),
            supports_wasm: false,
            supports_native: true,
            requires_shared_memory: false,
            supports_gpu: false,
            max_concurrency: 1,
        }
    }

    fn run(&self, input: &[u8]) -> Result<Vec<u8>, String> {
        Ok(input.iter().map(|byte| byte.to_ascii_uppercase()).collect())
    }
}

/// Burns the microseconds named in its input, then answers.
///
/// The delay is little-endian u32 microseconds in the first four bytes, so the
/// caller varies service time per call without restarting the kernel. Anything
/// shorter than four bytes is zero delay, which makes it an echo.
struct BusyUnit;

impl RuntimeUnit for BusyUnit {
    fn descriptor(&self) -> RuntimeUnitDescriptor {
        RuntimeUnitDescriptor {
            unit_id: "runtime.busy".to_string(),
            role: RuntimeRole::Compute,
            input_schema: "foundation/v1/envelope.capnp".to_string(),
            output_schema: "foundation/v1/envelope.capnp".to_string(),
            supports_wasm: false,
            supports_native: true,
            requires_shared_memory: false,
            supports_gpu: false,
            max_concurrency: 1,
        }
    }

    fn run(&self, input: &[u8]) -> Result<Vec<u8>, String> {
        let micros = if input.len() >= 4 {
            u32::from_le_bytes([input[0], input[1], input[2], input[3]])
        } else {
            0
        };
        if micros > 0 {
            let deadline =
                std::time::Instant::now() + std::time::Duration::from_micros(u64::from(micros));
            // A spin, not a sleep. A kernel that slept would yield its core to
            // the host's own spin and measure a contention pattern that does
            // not occur when the kernel is actually computing.
            while std::time::Instant::now() < deadline {
                std::hint::spin_loop();
            }
        }
        Ok(input.to_vec())
    }
}

fn main() {
    let host = NativeRuntimeHost::new(BTreeMap::new());
    if let Err(error) = host.register_unit(Arc::new(EchoUnit)) {
        eprintln!("reference kernel: register echo unit: {error}");
        std::process::exit(1);
    }
    if let Err(error) = host.register_unit(Arc::new(BusyUnit)) {
        eprintln!("reference kernel: register busy unit: {error}");
        std::process::exit(1);
    }
    if let Err(error) = serve_transport(&host) {
        eprintln!("reference kernel: {error}");
        std::process::exit(1);
    }
}
