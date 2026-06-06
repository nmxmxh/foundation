use std::collections::BTreeMap;
use std::hint::black_box;
use std::sync::Arc;
use std::time::{Duration, Instant};

use ovasabi_runtime_native::{
    decode_dispatch_response, encode_dispatch_request, NativeDispatchRequest,
    NativePayloadEncoding, NativeRuntimeBridge, MAX_NATIVE_FRAME_BYTES,
};
use ovrt_core::{RuntimeRole, RuntimeUnitDescriptor};
use ovrt_unit::RuntimeUnit;

const ITERS: usize = 2_000;

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

fn main() {
    let mut bridge = NativeRuntimeBridge::with_role_limits(BTreeMap::new());
    bridge.register_allowed_unit(Arc::new(EchoUnit)).expect("register benchmark unit");

    println!("foundation runtime-native report-only benchmark");
    println!("iters: {ITERS}");
    run_size(&bridge, 4 * 1024);
    run_size(&bridge, 64 * 1024);
    run_size(&bridge, 1024 * 1024);
}

fn run_size(bridge: &NativeRuntimeBridge, size: usize) {
    let payload = vec![17_u8; size];
    let request = NativeDispatchRequest {
        unit_id: "bench.echo".to_string(),
        schema_version: "1.0".to_string(),
        encoding: NativePayloadEncoding::Capnp,
        payload,
        metadata: b"corr=native-bench".to_vec(),
    };
    let frame = encode_dispatch_request(&request).expect("encode benchmark frame");
    let mut samples = Vec::with_capacity(ITERS);

    for _ in 0..ITERS {
        let started = Instant::now();
        let response_frame =
            bridge.dispatch_frame(black_box(&frame)).expect("dispatch benchmark frame");
        let response = decode_dispatch_response(&response_frame, MAX_NATIVE_FRAME_BYTES)
            .expect("decode benchmark response");
        black_box(response.payload);
        samples.push(started.elapsed());
    }

    samples.sort_unstable();
    let mean = samples.iter().copied().sum::<Duration>().as_nanos() as f64 / ITERS as f64;
    let p50 = percentile_ns(&samples, 50);
    let p95 = percentile_ns(&samples, 95);
    let p99 = percentile_ns(&samples, 99);
    println!(
        "{:>7} bytes native dispatch frame mean={:>10.2} ns p50={} ns p95={} ns p99={} ns",
        size, mean, p50, p95, p99
    );
}

fn percentile_ns(samples: &[Duration], percentile: usize) -> u128 {
    let index = ((samples.len().saturating_sub(1)) * percentile) / 100;
    samples[index].as_nanos()
}
