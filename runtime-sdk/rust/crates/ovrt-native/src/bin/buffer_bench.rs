use std::hint::black_box;
use std::time::Instant;

use ovrt_native::NativeBuffer;

const ITERS: usize = 1_000_000;

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
        buffer.write_output_bytes(black_box(&output)).expect("write");
    });

    bench_ns("native write_output_bytes_fast copy only", || {
        buffer
            .write_output_bytes_fast(black_box(&output))
            .expect("fast write");
    });
}
