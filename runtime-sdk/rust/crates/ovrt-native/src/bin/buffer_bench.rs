use std::collections::BTreeMap;
use std::hint::black_box;
use std::sync::Arc;
use std::time::Instant;

use ovrt_core::{RuntimeRole, RuntimeUnitDescriptor};
use ovrt_native::{process_runtime_buffer_in_place, NativeBuffer, NativeRuntimeHost};
use ovrt_unit::RuntimeUnit;

const ITERS: usize = 1_000_000;

struct EchoUnit;

impl RuntimeUnit for EchoUnit {
    fn descriptor(&self) -> RuntimeUnitDescriptor {
        RuntimeUnitDescriptor {
            unit_id: "bench.echo".to_string(),
            role: RuntimeRole::Compute,
            input_schema: "common/v1/envelope.capnp".to_string(),
            output_schema: "common/v1/envelope.capnp".to_string(),
            supports_wasm: false,
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

fn bench_ns(name: &str, mut f: impl FnMut()) {
    let start = Instant::now();
    for _ in 0..ITERS {
        f();
    }
    let elapsed = start.elapsed();
    let ns = elapsed.as_nanos() as f64 / ITERS as f64;
    println!("{name:<42} {ns:>10.2} ns/op");
}

fn main() {
    let input = vec![17_u8; 1024];
    let output = vec![29_u8; 1024];
    let mut buffer = NativeBuffer::with_capacity();
    buffer.initialize_control_plane(1).expect("init");
    buffer.write_input_bytes(&input).expect("input");
    buffer.write_output_bytes(&output).expect("output");

    bench_ns("native read_output_bytes owned Vec", || {
        let bytes = buffer.read_output_bytes().expect("read");
        black_box(bytes);
    });

    bench_ns("native output_bytes_view borrowed", || {
        let bytes = buffer.output_bytes_view().expect("view");
        black_box(bytes);
    });

    bench_ns("native write_output_bytes clear+copy", || {
        buffer
            .write_output_bytes(black_box(&output))
            .expect("write");
    });

    bench_ns("native write_output_bytes_fast copy only", || {
        buffer
            .write_output_bytes_fast(black_box(&output))
            .expect("fast write");
    });

    let host = NativeRuntimeHost::new(BTreeMap::new());
    host.register_unit(Arc::new(EchoUnit)).expect("unit");
    let mut process_buffer = NativeBuffer::with_capacity();
    process_buffer.initialize_control_plane(1).expect("init");
    process_buffer.write_input_bytes(&input).expect("input");
    let mut raw_process_buffer = process_buffer.into_inner();

    bench_ns("native process_runtime_buffer_in_place", || {
        process_runtime_buffer_in_place(
            &host,
            "bench.echo",
            black_box(raw_process_buffer.as_mut_slice()),
        )
        .expect("process");
    });
}
