use std::collections::BTreeMap;
use std::hint::black_box;
use std::sync::Arc;
use std::time::{Duration, Instant};

use ovasabi_runtime_native::{
    decode_dispatch_response, encode_dispatch_request, NativeDispatchRequest,
    NativePayloadEncoding, NativeRuntimeBridge, MAX_NATIVE_FRAME_BYTES,
};
use ovrt_core::{RuntimeRole, RuntimeUnitDescriptor};
use ovrt_native::{process_runtime_buffer_in_place, NativeBuffer, NativeRuntimeHost};
use ovrt_unit::RuntimeUnit;

const ITERS: usize = 2_000;
const DESCRIPTOR_BYTES: usize = 96;

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
    bridge
        .register_allowed_unit(Arc::new(EchoUnit))
        .expect("register benchmark unit");

    println!("foundation native communication simulation");
    println!("iters: {ITERS}");
    println!("copy model: structural byte copies in the current frame/buffer contract; allocator counts remain language-specific benchmark data");
    println!();

    for size in [4 * 1024, 64 * 1024, 1024 * 1024] {
        run_full_payload_frame(&bridge, size);
    }

    println!();
    for size in [4 * 1024, 64 * 1024, 1024 * 1024] {
        run_descriptor_control_frame(&bridge, size);
    }

    println!();
    run_runtime_buffer_control();
}

fn run_full_payload_frame(bridge: &NativeRuntimeBridge, payload_bytes: usize) {
    let payload = vec![17_u8; payload_bytes];
    let request = NativeDispatchRequest {
        unit_id: "bench.echo".to_string(),
        schema_version: "1.0".to_string(),
        encoding: NativePayloadEncoding::Capnp,
        payload,
        metadata: b"corr=native-flow-sim".to_vec(),
    };

    let stats = sample(|| {
        let frame = encode_dispatch_request(black_box(&request)).expect("encode request");
        let response_frame = bridge
            .dispatch_frame(black_box(&frame))
            .expect("dispatch frame");
        let response = decode_dispatch_response(&response_frame, MAX_NATIVE_FRAME_BYTES)
            .expect("decode response");
        black_box(response.payload.len());
    });

    let modeled_copied = payload_bytes * 5;
    println!(
        "full-payload-frame payload={payload_bytes:>7}B mean={:>10.2}ns p50={:>8}ns p95={:>8}ns p99={:>8}ns modeled_payload_copy={:>9}B ({:.1}x)",
        stats.mean_ns,
        stats.p50_ns,
        stats.p95_ns,
        stats.p99_ns,
        modeled_copied,
        modeled_copied as f64 / payload_bytes as f64
    );
}

fn run_descriptor_control_frame(bridge: &NativeRuntimeBridge, external_payload_bytes: usize) {
    let descriptor = make_descriptor_payload(external_payload_bytes);
    let request = NativeDispatchRequest {
        unit_id: "bench.echo".to_string(),
        schema_version: "1.0".to_string(),
        encoding: NativePayloadEncoding::Capnp,
        payload: descriptor,
        metadata: b"corr=native-flow-sim".to_vec(),
    };

    let stats = sample(|| {
        let frame = encode_dispatch_request(black_box(&request)).expect("encode descriptor");
        let response_frame = bridge
            .dispatch_frame(black_box(&frame))
            .expect("dispatch descriptor");
        let response = decode_dispatch_response(&response_frame, MAX_NATIVE_FRAME_BYTES)
            .expect("decode descriptor response");
        black_box(response.payload.len());
    });

    let modeled_control_copy = DESCRIPTOR_BYTES * 5;
    println!(
        "descriptor-control external={external_payload_bytes:>7}B descriptor={DESCRIPTOR_BYTES:>3}B mean={:>10.2}ns p50={:>8}ns p95={:>8}ns p99={:>8}ns modeled_hot_payload_copy={:>2}B modeled_control_copy={modeled_control_copy:>5}B",
        stats.mean_ns,
        stats.p50_ns,
        stats.p95_ns,
        stats.p99_ns,
        0
    );
}

fn run_runtime_buffer_control() {
    let host = NativeRuntimeHost::new(BTreeMap::new());
    host.register_unit(Arc::new(EchoUnit)).expect("register unit");

    let input = vec![31_u8; 1024];
    let mut buffer = NativeBuffer::with_capacity();
    buffer.initialize_control_plane(1).expect("init buffer");
    buffer
        .write_input_bytes_fast(&input)
        .expect("write runtime input");
    let mut raw = buffer.into_inner();

    let stats = sample(|| {
        process_runtime_buffer_in_place(&host, "bench.echo", black_box(raw.as_mut_slice()))
            .expect("process runtime buffer");
    });

    println!(
        "runtime-buffer-in-place input={:>7}B mean={:>10.2}ns p50={:>8}ns p95={:>8}ns p99={:>8}ns modeled_input_view_copy=0B modeled_payload_copy={}B modeled_output_region_clear=2048B",
        input.len(),
        stats.mean_ns,
        stats.p50_ns,
        stats.p95_ns,
        stats.p99_ns,
        input.len() * 2
    );
}

fn make_descriptor_payload(external_payload_bytes: usize) -> Vec<u8> {
    let mut descriptor = vec![0_u8; DESCRIPTOR_BYTES];
    descriptor[0..4].copy_from_slice(b"OVDS");
    descriptor[4..8].copy_from_slice(&1_u32.to_le_bytes());
    descriptor[8..16].copy_from_slice(&(external_payload_bytes as u64).to_le_bytes());
    descriptor[16..24].copy_from_slice(&4096_u64.to_le_bytes());
    descriptor[24..32].copy_from_slice(&1_u64.to_le_bytes());
    descriptor[32..40].copy_from_slice(&0_u64.to_le_bytes());
    descriptor[40..48].copy_from_slice(&(DESCRIPTOR_BYTES as u64).to_le_bytes());
    descriptor
}

fn sample(mut f: impl FnMut()) -> Stats {
    let mut samples = Vec::with_capacity(ITERS);
    for _ in 0..ITERS {
        let started = Instant::now();
        f();
        samples.push(started.elapsed());
    }
    samples.sort_unstable();
    Stats {
        mean_ns: samples.iter().copied().sum::<Duration>().as_nanos() as f64 / ITERS as f64,
        p50_ns: percentile_ns(&samples, 50),
        p95_ns: percentile_ns(&samples, 95),
        p99_ns: percentile_ns(&samples, 99),
    }
}

struct Stats {
    mean_ns: f64,
    p50_ns: u128,
    p95_ns: u128,
    p99_ns: u128,
}

fn percentile_ns(samples: &[Duration], percentile: usize) -> u128 {
    let index = ((samples.len().saturating_sub(1)) * percentile) / 100;
    samples[index].as_nanos()
}
