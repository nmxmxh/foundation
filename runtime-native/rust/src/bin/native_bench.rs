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
            input_schema: "foundation/v1/envelope.capnp".to_string(),
            output_schema: "foundation/v1/envelope.capnp".to_string(),
            supports_wasm: true,
            supports_native: true,
            requires_shared_memory: false,
            supports_gpu: false,
            max_concurrency: 1,
        }
    }

    fn execute(
        &self,
        input: &[u8],
        __ovrt_output: &mut dyn ovrt_unit::RuntimeOutput,
    ) -> Result<(), String> {
        __ovrt_output.write(input)
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
    run_secure_store_bench();
    run_soa_btree_bench();
    run_unit_output_bench();
    run_columnar_assembly_bench();
    run_frame_reading_bench();
    run_soa_btree_arena_bench();
}

fn run_soa_btree_bench() {
    use ovrt_core::SoABTreeMap;

    println!("\n--- Tier 3: Compute Unit SoA B-Tree vs BTreeMap (N=1,000 & N=10,000) ---");
    let iters = 20_000;

    for &size in &[1_000, 10_000] {
        let mut btree = BTreeMap::new();
        let mut soa = SoABTreeMap::new();

        for i in 0..size {
            let key = format!("tenant:org_{:04}:metric_{:06}", i % 50, i);
            btree.insert(key.clone(), i as u64);
            soa.insert(key, i as u64);
        }

        let mut btree_hit_samples = Vec::with_capacity(iters);
        let mut soa_hit_samples = Vec::with_capacity(iters);
        let mut btree_miss_samples = Vec::with_capacity(iters);
        let mut soa_miss_samples = Vec::with_capacity(iters);

        for i in 0..iters {
            let key = format!("tenant:org_{:04}:metric_{:06}", (i % 50), (i % size));
            let started = Instant::now();
            let val = btree.get(black_box(&key));
            btree_hit_samples.push(started.elapsed());
            black_box(val);
        }

        for i in 0..iters {
            let key = format!("tenant:org_{:04}:metric_{:06}", (i % 50), (i % size));
            let started = Instant::now();
            let val = soa.get(black_box(&key));
            soa_hit_samples.push(started.elapsed());
            black_box(val);
        }

        for i in 0..iters {
            let key = format!("tenant:org_{:04}:miss_{:06}", (i % 50), (i % size));
            let started = Instant::now();
            let val = btree.get(black_box(&key));
            btree_miss_samples.push(started.elapsed());
            black_box(val);
        }

        for i in 0..iters {
            let key = format!("tenant:org_{:04}:miss_{:06}", (i % 50), (i % size));
            let started = Instant::now();
            let val = soa.get(black_box(&key));
            soa_miss_samples.push(started.elapsed());
            black_box(val);
        }

        btree_hit_samples.sort_unstable();
        soa_hit_samples.sort_unstable();
        btree_miss_samples.sort_unstable();
        soa_miss_samples.sort_unstable();

        let btree_hit_mean =
            btree_hit_samples.iter().copied().sum::<Duration>().as_nanos() as f64 / iters as f64;
        let soa_hit_mean =
            soa_hit_samples.iter().copied().sum::<Duration>().as_nanos() as f64 / iters as f64;
        let btree_miss_mean =
            btree_miss_samples.iter().copied().sum::<Duration>().as_nanos() as f64 / iters as f64;
        let soa_miss_mean =
            soa_miss_samples.iter().copied().sum::<Duration>().as_nanos() as f64 / iters as f64;

        println!("[Scale N = {}]", size);
        println!(
            "  BTreeMap hit   mean={:>8.2} ns p50={} ns p95={} ns p99={} ns",
            btree_hit_mean,
            percentile_ns(&btree_hit_samples, 50),
            percentile_ns(&btree_hit_samples, 95),
            percentile_ns(&btree_hit_samples, 99)
        );
        println!(
            "  SoABTree hit   mean={:>8.2} ns p50={} ns p95={} ns p99={} ns",
            soa_hit_mean,
            percentile_ns(&soa_hit_samples, 50),
            percentile_ns(&soa_hit_samples, 95),
            percentile_ns(&soa_hit_samples, 99)
        );
        println!(
            "  BTreeMap miss  mean={:>8.2} ns p50={} ns p95={} ns p99={} ns",
            btree_miss_mean,
            percentile_ns(&btree_miss_samples, 50),
            percentile_ns(&btree_miss_samples, 95),
            percentile_ns(&btree_miss_samples, 99)
        );
        println!(
            "  SoABTree miss  mean={:>8.2} ns p50={} ns p95={} ns p99={} ns",
            soa_miss_mean,
            percentile_ns(&soa_miss_samples, 50),
            percentile_ns(&soa_miss_samples, 95),
            percentile_ns(&soa_miss_samples, 99)
        );
    }

    println!("\n[Numeric u64 Order Book simulation N = 10,000]");
    let size = 10_000;
    let mut btree_num = BTreeMap::new();
    let mut soa_num = SoABTreeMap::new();
    for i in 0..size {
        let price = (100_000 + i * 5) as u64;
        btree_num.insert(price, i as u64);
        soa_num.insert(price, i as u64);
    }

    let mut btree_num_samples = Vec::with_capacity(iters);
    let mut soa_num_samples = Vec::with_capacity(iters);

    for i in 0..iters {
        let price = (100_000 + (i % size) * 5) as u64;
        let started = Instant::now();
        let val = btree_num.get(black_box(&price));
        btree_num_samples.push(started.elapsed());
        black_box(val);
    }

    for i in 0..iters {
        let price = (100_000 + (i % size) * 5) as u64;
        let started = Instant::now();
        let val = soa_num.get(black_box(&price));
        soa_num_samples.push(started.elapsed());
        black_box(val);
    }

    btree_num_samples.sort_unstable();
    soa_num_samples.sort_unstable();

    println!(
        "  BTreeMap u64 hit mean={:>8.2} ns p50={} ns p95={} ns p99={} ns",
        btree_num_samples.iter().copied().sum::<Duration>().as_nanos() as f64 / iters as f64,
        percentile_ns(&btree_num_samples, 50),
        percentile_ns(&btree_num_samples, 95),
        percentile_ns(&btree_num_samples, 99)
    );
    println!(
        "  SoABTree u64 hit mean={:>8.2} ns p50={} ns p95={} ns p99={} ns",
        soa_num_samples.iter().copied().sum::<Duration>().as_nanos() as f64 / iters as f64,
        percentile_ns(&soa_num_samples, 50),
        percentile_ns(&soa_num_samples, 95),
        percentile_ns(&soa_num_samples, 99)
    );
}

fn run_secure_store_bench() {
    use ovasabi_runtime_native::NativeSecureStore;

    let store = NativeSecureStore::default();
    let iters = 20_000;

    // Populate keys for lookup test.
    for i in 0..128 {
        let key = format!("tenant:org_{:04}:token", i);
        store.put(&key, b"secret_value_payload").expect("setup put");
    }

    let mut get_hit_samples = Vec::with_capacity(iters);
    let mut get_miss_samples = Vec::with_capacity(iters);
    let mut inspect_hit_samples = Vec::with_capacity(iters);
    let mut put_samples = Vec::with_capacity(iters);

    // 1. Owned get (100% hits, measures search + heap clone)
    for i in 0..iters {
        let key = format!("tenant:org_{:04}:token", (i % 128));
        let started = Instant::now();
        let val = store.get(black_box(&key)).expect("bench get hit");
        get_hit_samples.push(started.elapsed());
        black_box(val);
    }

    // 2. Zero-allocation inspect (100% hits, measures pure search + zero allocs)
    for i in 0..iters {
        let key = format!("tenant:org_{:04}:token", (i % 128));
        let started = Instant::now();
        let len =
            store.inspect(black_box(&key), |val| val.map(|v| v.len())).expect("bench inspect hit");
        inspect_hit_samples.push(started.elapsed());
        black_box(len);
    }

    // 3. Owned get (100% misses, measures pure search failure, 0 allocs)
    for i in 0..iters {
        let key = format!("tenant:miss_{:04}:token", (i % 128));
        let started = Instant::now();
        let val = store.get(black_box(&key)).expect("bench get miss");
        get_miss_samples.push(started.elapsed());
        black_box(val);
    }

    // 4. Put updates
    for i in 0..iters {
        let key = format!("bench:key_{:04}", (i % 64));
        let started = Instant::now();
        store.put(black_box(&key), black_box(b"dynamic_value")).expect("bench put");
        put_samples.push(started.elapsed());
    }

    get_hit_samples.sort_unstable();
    inspect_hit_samples.sort_unstable();
    get_miss_samples.sort_unstable();
    put_samples.sort_unstable();

    println!("--- NativeSecureStore (BTreeMap + Zero-Alloc Inspect) ---");
    println!(
        "get hit (cloned)      mean={:>8.2} ns p50={} ns p95={} ns p99={} ns",
        get_hit_samples.iter().copied().sum::<Duration>().as_nanos() as f64 / iters as f64,
        percentile_ns(&get_hit_samples, 50),
        percentile_ns(&get_hit_samples, 95),
        percentile_ns(&get_hit_samples, 99)
    );
    println!(
        "inspect hit (0-alloc) mean={:>8.2} ns p50={} ns p95={} ns p99={} ns",
        inspect_hit_samples.iter().copied().sum::<Duration>().as_nanos() as f64 / iters as f64,
        percentile_ns(&inspect_hit_samples, 50),
        percentile_ns(&inspect_hit_samples, 95),
        percentile_ns(&inspect_hit_samples, 99)
    );
    println!(
        "get miss (0-alloc)    mean={:>8.2} ns p50={} ns p95={} ns p99={} ns",
        get_miss_samples.iter().copied().sum::<Duration>().as_nanos() as f64 / iters as f64,
        percentile_ns(&get_miss_samples, 50),
        percentile_ns(&get_miss_samples, 95),
        percentile_ns(&get_miss_samples, 99)
    );
    println!(
        "put (updates)         mean={:>8.2} ns p50={} ns p95={} ns p99={} ns",
        put_samples.iter().copied().sum::<Duration>().as_nanos() as f64 / iters as f64,
        percentile_ns(&put_samples, 50),
        percentile_ns(&put_samples, 95),
        percentile_ns(&put_samples, 99)
    );
}

fn run_size(bridge: &NativeRuntimeBridge, size: usize) {
    use ovrt_core::ScratchArena;

    let payload = vec![17_u8; size];
    let request = NativeDispatchRequest {
        unit_id: "bench.echo".to_string(),
        schema_version: "1.0".to_string(),
        encoding: NativePayloadEncoding::Capnp,
        payload,
        metadata: b"corr=native-bench".to_vec(),
    };
    let frame = encode_dispatch_request(&request).expect("encode benchmark frame");
    let mut heap_samples = Vec::with_capacity(ITERS);
    let mut scratch_samples = Vec::with_capacity(ITERS);

    // 1. Heap allocated dispatch (5x heap allocs per exchange)
    for _ in 0..ITERS {
        let started = Instant::now();
        let response_frame =
            bridge.dispatch_frame(black_box(&frame)).expect("dispatch benchmark frame");
        let response = decode_dispatch_response(&response_frame, MAX_NATIVE_FRAME_BYTES)
            .expect("decode benchmark response");
        black_box(response.payload);
        heap_samples.push(started.elapsed());
    }

    // 2. Scratch arena zero-allocation dispatch
    let mut scratch_arena = ScratchArena::with_capacity(size + 1024);
    for _ in 0..ITERS {
        scratch_arena.reset();
        let dest = scratch_arena.alloc_bytes(size + 16).expect("scratch alloc");
        let started = Instant::now();
        let written =
            bridge.dispatch_frame_into(black_box(&frame), dest).expect("dispatch frame into");
        scratch_samples.push(started.elapsed());
        black_box(written);
    }

    heap_samples.sort_unstable();
    scratch_samples.sort_unstable();

    let heap_mean = heap_samples.iter().copied().sum::<Duration>().as_nanos() as f64 / ITERS as f64;
    let scratch_mean =
        scratch_samples.iter().copied().sum::<Duration>().as_nanos() as f64 / ITERS as f64;

    println!(
        "{:>7} bytes [Heap Alloc]    mean={:>10.2} ns p50={} ns p95={} ns p99={} ns",
        size,
        heap_mean,
        percentile_ns(&heap_samples, 50),
        percentile_ns(&heap_samples, 95),
        percentile_ns(&heap_samples, 99)
    );
    println!(
        "{:>7} bytes [Scratch Arena]  mean={:>10.2} ns p50={} ns p95={} ns p99={} ns",
        size,
        scratch_mean,
        percentile_ns(&scratch_samples, 50),
        percentile_ns(&scratch_samples, 95),
        percentile_ns(&scratch_samples, 99)
    );
}

fn percentile_ns(samples: &[Duration], percentile: usize) -> u128 {
    let index = ((samples.len().saturating_sub(1)) * percentile) / 100;
    samples[index].as_nanos()
}

fn run_unit_output_bench() {
    println!("\n--- Target A: Unit Output Backing (Vec<u8> vs ScratchOutput) ---");
    let iters = 20_000;
    let unit = EchoUnit;

    for &size in &[256, 4096, 65536] {
        let input = vec![42u8; size];
        let mut vec_samples = Vec::with_capacity(iters);
        let mut scratch_samples = Vec::with_capacity(iters);

        for _ in 0..iters {
            let started = Instant::now();
            let out = unit.run(black_box(&input)).expect("run");
            vec_samples.push(started.elapsed());
            black_box(out);
        }

        let mut arena = ovrt_core::ScratchArena::with_capacity(size + 1024);
        for _ in 0..iters {
            arena.reset();
            let started = Instant::now();
            let out = unit.run_scratch(black_box(&input), &mut arena).expect("run_scratch");
            scratch_samples.push(started.elapsed());
            black_box(out);
        }

        vec_samples.sort_unstable();
        scratch_samples.sort_unstable();

        let vec_mean =
            vec_samples.iter().copied().sum::<Duration>().as_nanos() as f64 / iters as f64;
        let scratch_mean =
            scratch_samples.iter().copied().sum::<Duration>().as_nanos() as f64 / iters as f64;

        println!(
            "{:>6} bytes [Vec<u8> Dynamic] mean={:>8.2} ns p50={} ns p95={} ns p99={} ns",
            size,
            vec_mean,
            percentile_ns(&vec_samples, 50),
            percentile_ns(&vec_samples, 95),
            percentile_ns(&vec_samples, 99)
        );
        println!(
            "{:>6} bytes [ScratchOutput]   mean={:>8.2} ns p50={} ns p95={} ns p99={} ns (speedup: {:.2}x)",
            size,
            scratch_mean,
            percentile_ns(&scratch_samples, 50),
            percentile_ns(&scratch_samples, 95),
            percentile_ns(&scratch_samples, 99),
            vec_mean / scratch_mean
        );
    }
}

fn run_columnar_assembly_bench() {
    use ovrt_core::{
        assemble_columnar_batch_scratch, ColumnarFieldSpec, ScratchArena,
        COLUMNAR_LOGICAL_TYPE_FLOAT, COLUMNAR_LOGICAL_TYPE_INT, COLUMNAR_LOGICAL_TYPE_UINT,
    };

    println!("\n--- Target B: Columnar Batch Descriptor Assembly (Heap Vec vs ScratchArena) ---");
    let iters = 20_000;

    let fields = [
        ColumnarFieldSpec::new_fixed(0, COLUMNAR_LOGICAL_TYPE_FLOAT, 1000, 4, 1),
        ColumnarFieldSpec::new_fixed(1, COLUMNAR_LOGICAL_TYPE_UINT, 1000, 4, 2),
        ColumnarFieldSpec::new_fixed(2, COLUMNAR_LOGICAL_TYPE_INT, 1000, 4, 3),
        ColumnarFieldSpec::new_fixed(3, COLUMNAR_LOGICAL_TYPE_FLOAT, 1000, 4, 4),
    ];

    let mut heap_samples = Vec::with_capacity(iters);
    let mut scratch_samples = Vec::with_capacity(iters);

    for _ in 0..iters {
        let started = Instant::now();
        let total = ovrt_core::columnar_descriptor_payload_bytes(fields.len() as u32) as usize;
        let mut buffer = vec![0u8; total];
        ovrt_core::assemble_columnar_batch_into(1000, black_box(&fields), &mut buffer)
            .expect("assemble");
        heap_samples.push(started.elapsed());
        black_box(buffer);
    }

    let mut arena = ScratchArena::with_capacity(1024);
    for _ in 0..iters {
        arena.reset();
        let started = Instant::now();
        let slab = assemble_columnar_batch_scratch(1000, black_box(&fields), &mut arena)
            .expect("assemble_scratch");
        scratch_samples.push(started.elapsed());
        black_box(slab);
    }

    heap_samples.sort_unstable();
    scratch_samples.sort_unstable();

    let heap_mean = heap_samples.iter().copied().sum::<Duration>().as_nanos() as f64 / iters as f64;
    let scratch_mean =
        scratch_samples.iter().copied().sum::<Duration>().as_nanos() as f64 / iters as f64;

    println!(
        "4 cols [Heap Vec Alloc] mean={:>8.2} ns p50={} ns p95={} ns p99={} ns",
        heap_mean,
        percentile_ns(&heap_samples, 50),
        percentile_ns(&heap_samples, 95),
        percentile_ns(&heap_samples, 99)
    );
    println!(
        "4 cols [ScratchArena]   mean={:>8.2} ns p50={} ns p95={} ns p99={} ns (speedup: {:.2}x)",
        scratch_mean,
        percentile_ns(&scratch_samples, 50),
        percentile_ns(&scratch_samples, 95),
        percentile_ns(&scratch_samples, 99),
        heap_mean / scratch_mean
    );
}

fn run_frame_reading_bench() {
    use ovrt_native::read_frame_slice;
    use std::io::Cursor;

    println!("\n--- Target C: Control Buffer Frame Read (Vec::resize vs read_frame_slice) ---");
    let iters = 20_000;
    let payload = vec![0xAAu8; 4096];
    let mut frame_bytes = Vec::with_capacity(4096 + 4);
    frame_bytes.extend_from_slice(&(payload.len() as u32).to_le_bytes());
    frame_bytes.extend_from_slice(&payload);

    let mut vec_samples = Vec::with_capacity(iters);
    let mut slice_samples = Vec::with_capacity(iters);

    let mut dynamic_buf = Vec::with_capacity(4096);
    for _ in 0..iters {
        let mut cursor = Cursor::new(&frame_bytes);
        let started = Instant::now();
        let mut size = [0u8; 4];
        std::io::Read::read_exact(&mut cursor, &mut size).unwrap();
        let len = u32::from_le_bytes(size) as usize;
        dynamic_buf.clear();
        dynamic_buf.resize(len, 0);
        std::io::Read::read_exact(&mut cursor, &mut dynamic_buf).unwrap();
        vec_samples.push(started.elapsed());
        black_box(&dynamic_buf);
    }

    let mut scratch_buf = [0u8; 4096];
    for _ in 0..iters {
        let mut cursor = Cursor::new(&frame_bytes);
        let started = Instant::now();
        let n = read_frame_slice(&mut cursor, &mut scratch_buf).unwrap();
        slice_samples.push(started.elapsed());
        black_box(&scratch_buf[..n]);
    }

    vec_samples.sort_unstable();
    slice_samples.sort_unstable();

    let vec_mean = vec_samples.iter().copied().sum::<Duration>().as_nanos() as f64 / iters as f64;
    let slice_mean =
        slice_samples.iter().copied().sum::<Duration>().as_nanos() as f64 / iters as f64;

    println!(
        "4 KiB Frame [Vec resize(0)]    mean={:>8.2} ns p50={} ns p95={} ns p99={} ns",
        vec_mean,
        percentile_ns(&vec_samples, 50),
        percentile_ns(&vec_samples, 95),
        percentile_ns(&vec_samples, 99)
    );
    println!(
        "4 KiB Frame [read_frame_slice] mean={:>8.2} ns p50={} ns p95={} ns p99={} ns (speedup: {:.2}x)",
        slice_mean,
        percentile_ns(&slice_samples, 50),
        percentile_ns(&slice_samples, 95),
        percentile_ns(&slice_samples, 99),
        vec_mean / slice_mean
    );
}

fn run_soa_btree_arena_bench() {
    use ovrt_core::ArenaSoABTreeMap;

    println!("\n--- Target D: SoA B-Tree Batch Builds & O(1) Resets (N=1,000) ---");
    let batch_iters = 500;
    let size = 1_000;

    let mut btree_build_samples = Vec::with_capacity(batch_iters);
    let mut arena_build_samples = Vec::with_capacity(batch_iters);
    let mut btree_reset_samples = Vec::with_capacity(batch_iters);
    let mut arena_reset_samples = Vec::with_capacity(batch_iters);

    let mut btree = BTreeMap::new();
    let mut arena_tree = ArenaSoABTreeMap::with_capacity(128);

    for _ in 0..batch_iters {
        btree.clear();
        let started = Instant::now();
        for i in 0..size {
            btree.insert(i as u64, (i * 2) as u64);
        }
        btree_build_samples.push(started.elapsed());

        arena_tree.clear();
        let started = Instant::now();
        for i in 0..size {
            arena_tree.insert(i as u64, (i * 2) as u64);
        }
        arena_build_samples.push(started.elapsed());

        let started = Instant::now();
        btree.clear();
        btree_reset_samples.push(started.elapsed());

        let started = Instant::now();
        arena_tree.clear();
        arena_reset_samples.push(started.elapsed());
    }

    btree_build_samples.sort_unstable();
    arena_build_samples.sort_unstable();
    btree_reset_samples.sort_unstable();
    arena_reset_samples.sort_unstable();

    let btree_b_mean = btree_build_samples.iter().copied().sum::<Duration>().as_nanos() as f64
        / batch_iters as f64;
    let arena_b_mean = arena_build_samples.iter().copied().sum::<Duration>().as_nanos() as f64
        / batch_iters as f64;

    let btree_r_mean = btree_reset_samples.iter().copied().sum::<Duration>().as_nanos() as f64
        / batch_iters as f64;
    let arena_r_mean = arena_reset_samples.iter().copied().sum::<Duration>().as_nanos() as f64
        / batch_iters as f64;

    println!(
        "Build 1,000 items [std BTreeMap]  mean={:>10.2} ns p50={} ns",
        btree_b_mean,
        percentile_ns(&btree_build_samples, 50)
    );
    println!(
        "Build 1,000 items [ArenaSoABTree] mean={:>10.2} ns p50={} ns (speedup vs BTreeMap: {:.2}x)",
        arena_b_mean,
        percentile_ns(&arena_build_samples, 50),
        btree_b_mean / arena_b_mean
    );
    println!(
        "Reset/Clear Tree  [std BTreeMap]  mean={:>10.2} ns p50={} ns",
        btree_r_mean,
        percentile_ns(&btree_reset_samples, 50)
    );
    println!(
        "Reset/Clear Tree  [ArenaSoABTree] mean={:>10.2} ns p50={} ns (speedup: {:.2}x)",
        arena_r_mean,
        percentile_ns(&arena_reset_samples, 50),
        btree_r_mean / arena_r_mean
    );
}
