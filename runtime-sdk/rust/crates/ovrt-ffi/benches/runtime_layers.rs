#![deny(unsafe_op_in_unsafe_fn)]

use std::alloc::{GlobalAlloc, Layout, System};
use std::collections::BTreeMap;
use std::hint::black_box;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::Arc;
use std::time::Instant;

use ovrt_core::{RuntimeRole, RuntimeUnitDescriptor};
use ovrt_native::NativeRuntimeHost;
use ovrt_unit::{RuntimeUnit, UnitRegistry};

struct CountingAllocator;
static COUNTING: AtomicBool = AtomicBool::new(false);
static ALLOCS: AtomicU64 = AtomicU64::new(0);
static BYTES: AtomicU64 = AtomicU64::new(0);

// SAFETY: Allocation and deallocation use System with the original pointer and layout.
unsafe impl GlobalAlloc for CountingAllocator {
    unsafe fn alloc(&self, layout: Layout) -> *mut u8 {
        if COUNTING.load(Ordering::Relaxed) {
            ALLOCS.fetch_add(1, Ordering::Relaxed);
            BYTES.fetch_add(layout.size() as u64, Ordering::Relaxed);
        }
        // SAFETY: The allocator caller supplies a valid allocation layout.
        unsafe { System.alloc(layout) }
    }

    unsafe fn dealloc(&self, ptr: *mut u8, layout: Layout) {
        // SAFETY: The caller returns a System allocation with its original layout.
        unsafe { System.dealloc(ptr, layout) }
    }
}

#[global_allocator]
static ALLOCATOR: CountingAllocator = CountingAllocator;

fn measure(name: &str, iterations: usize, mut operation: impl FnMut()) {
    for _ in 0..1_000 {
        operation();
    }
    let mut samples = [0.0_f64; 7];
    for sample in &mut samples {
        let start = Instant::now();
        for _ in 0..iterations {
            operation();
        }
        *sample = start.elapsed().as_nanos() as f64 / iterations as f64;
    }
    ALLOCS.store(0, Ordering::Relaxed);
    BYTES.store(0, Ordering::Relaxed);
    COUNTING.store(true, Ordering::Relaxed);
    for _ in 0..1_000 {
        operation();
    }
    COUNTING.store(false, Ordering::Relaxed);
    samples.sort_by(f64::total_cmp);
    println!(
        "{name}\t{:.2}\t{:.2}\t{:.2}\t{:.3}\t{:.3}",
        samples[0],
        samples[3],
        samples[6],
        ALLOCS.load(Ordering::Relaxed) as f64 / 1_000.0,
        BYTES.load(Ordering::Relaxed) as f64 / 1_000.0,
    );
    let ceiling = match name {
        "unit/descriptor" => 3.0,
        "native/queued/1024" => 7.0,
        "native/direct/0" => 0.0,
        value if value.starts_with("native/direct/") => 1.0,
        _ => 0.0,
    };
    assert!(
        ALLOCS.load(Ordering::Relaxed) as f64 / 1_000.0 <= ceiling,
        "allocation budget: {name}"
    );
}

struct EchoUnit;

impl RuntimeUnit for EchoUnit {
    fn descriptor(&self) -> RuntimeUnitDescriptor {
        RuntimeUnitDescriptor {
            unit_id: "bench.echo".to_owned(),
            role: RuntimeRole::Compute,
            input_schema: "bench/bytes".to_owned(),
            output_schema: "bench/bytes".to_owned(),
            supports_wasm: true,
            supports_native: true,
            requires_shared_memory: false,
            supports_gpu: false,
            max_concurrency: 8,
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

fn unit_and_ffi() {
    let registry = UnitRegistry::default();
    registry.register(Arc::new(EchoUnit)).expect("register");
    measure("unit/lookup", 200_000, || {
        black_box(registry.get(black_box("bench.echo")).expect("lookup"));
    });
    measure("unit/descriptor", 200_000, || {
        black_box(registry.get("bench.echo").expect("lookup").expect("unit").descriptor());
    });
    let host = NativeRuntimeHost::new(BTreeMap::from([(RuntimeRole::Compute, 4)]));
    host.register_unit(Arc::new(EchoUnit)).expect("register");
    for size in [0, 1_024, 65_536, 1_048_576] {
        let input = vec![42; size];
        measure(&format!("native/direct/{size}"), 5_000, || {
            black_box(host.dispatch_direct("bench.echo", black_box(&input)).expect("dispatch"));
        });
        let mut destination = vec![0; size];
        measure(&format!("native/into/{size}"), 5_000, || {
            let written = host
                .dispatch_direct_into("bench.echo", black_box(&input), black_box(&mut destination))
                .expect("destination");
            assert_eq!(written, input.len());
            black_box(&destination);
        });
    }
    measure("native/queued/1024", 2_000, || {
        black_box(host.dispatch("bench.echo", vec![42; 1_024]).expect("dispatch"));
    });
    let mut raw = vec![0; ovrt_core::BUFFER_TOTAL_BYTES as usize];
    let input_length =
        (ovrt_core::OFFSET_HEADER_INTS + ovrt_core::INT_IDX_INPUT_LENGTH * 4) as usize;
    raw[input_length..input_length + 4].copy_from_slice(&1_024_i32.to_le_bytes());
    let mut error = [0_i8; 4_096];
    let mut handle = std::ptr::null_mut();
    // SAFETY: Both output regions are writable; the builder transfers host ownership to the ABI.
    let created = unsafe {
        ovrt_ffi::create_host(4, &mut handle, error.as_mut_ptr(), error.len(), |_| Ok(host))
    };
    assert_eq!(created, 0);
    measure("ffi/process/1024", 100_000, || {
        // SAFETY: The live host and all borrowed byte regions remain valid throughout this synchronous call.
        let status = unsafe {
            ovrt_ffi::process_buffer(
                handle,
                b"bench.echo".as_ptr(),
                10,
                raw.as_mut_ptr(),
                raw.len(),
                error.as_mut_ptr(),
                error.len(),
            )
        };
        assert_eq!(black_box(status), 0);
    });
    // SAFETY: The ABI created this handle; every process call has completed.
    unsafe { ovrt_ffi::destroy_host(handle) };
}

fn parallel_direct() {
    for workers in [1, 4, 8] {
        let host = NativeRuntimeHost::new(BTreeMap::new());
        host.register_unit(Arc::new(EchoUnit)).expect("register");
        let mut samples = [0.0_f64; 7];
        for sample in &mut samples {
            *sample = parallel_round(&host, workers, 20_000, false);
        }
        parallel_round(&host, workers, 1_000, true);
        samples.sort_by(f64::total_cmp);
        let operations = (workers * 1_000) as f64;
        println!(
            "native/parallel/{workers}\t{:.2}\t{:.2}\t{:.2}\t{:.3}\t{:.3}",
            samples[0],
            samples[3],
            samples[6],
            ALLOCS.load(Ordering::Relaxed) as f64 / operations,
            BYTES.load(Ordering::Relaxed) as f64 / operations
        );
    }
}

fn parallel_round(host: &NativeRuntimeHost, workers: usize, iterations: usize, count: bool) -> f64 {
    use std::sync::Barrier;
    let ready = Barrier::new(workers + 1);
    let start = Barrier::new(workers + 1);
    std::thread::scope(|scope| {
        let mut handles = Vec::with_capacity(workers);
        for _ in 0..workers {
            let (ready, start) = (&ready, &start);
            handles.push(scope.spawn(move || {
                let input = [42; 1_024];
                for _ in 0..200 {
                    black_box(host.dispatch_direct("bench.echo", &input).expect("warmup"));
                }
                ready.wait();
                start.wait();
                for _ in 0..iterations {
                    black_box(host.dispatch_direct("bench.echo", &input).expect("dispatch"));
                }
            }));
        }
        ready.wait();
        ALLOCS.store(0, Ordering::Relaxed);
        BYTES.store(0, Ordering::Relaxed);
        COUNTING.store(count, Ordering::Relaxed);
        let started = Instant::now();
        start.wait();
        for handle in handles {
            handle.join().expect("worker");
        }
        let elapsed = started.elapsed().as_nanos() as f64 / (workers * iterations) as f64;
        COUNTING.store(false, Ordering::Relaxed);
        elapsed
    })
}

#[cfg(unix)]
fn placement() {
    use ovrt_dispatch::{
        decide, DispatchBlock, DispatchLaneDescriptor, DispatchLaneStats, DispatchRequest,
        MAX_LANES,
    };
    let file = tempfile::NamedTempFile::new().expect("file");
    file.as_file().set_len(u64::from(ovrt_core::DISPATCH_REGION_BYTES)).expect("size");
    let block = DispatchBlock::open(file.path()).expect("mapping");
    let rows: Vec<_> = (0..MAX_LANES)
        .map(|lane| DispatchLaneDescriptor {
            lane_id: lane as u16,
            jurisdiction: 0,
            max_concurrency: 8,
            generation: 1,
            unit_class_mask: 1,
            affinity_bloom: 0,
        })
        .collect();
    block.publish_descriptors(&rows, 1).expect("publish");
    for lane in 0..MAX_LANES {
        block
            .apply_mirror_stats(
                lane,
                &DispatchLaneStats {
                    ewma_ns: 1_000 + lane as u64 * 100,
                    inflight: 0,
                    max_concurrency: 8,
                    last_tick_seen: 1,
                },
            )
            .expect("stats");
    }
    let request = DispatchRequest {
        required_class_mask: 1,
        jurisdiction: 0,
        deadline_ns: u64::MAX,
        affinity_key: 0,
    };
    measure("dispatch/snapshot/32", 100_000, || {
        let descriptors = block.snapshot_descriptors().expect("descriptors");
        let stats = block.snapshot_stats();
        assert_eq!(black_box(decide(1, &descriptors, &stats, &request)), Some(0));
    });
}

fn network() {
    use ovrt_network::DedupRing;
    let ring = DedupRing::with_capacity(64).expect("ring");
    ring.observe(b"repeat");
    measure("network/dedup/hit", 200_000, || {
        black_box(ring.observe(black_box(b"repeat")));
    });
    let mut key = 0_u64;
    measure("network/dedup/pressure", 200_000, || {
        key += 1;
        black_box(ring.observe(black_box(&key.to_le_bytes())));
    });
    let released = Arc::new((std::sync::Mutex::new(false), std::sync::Condvar::new()));
    let racer = ovrt_network::Racer::new(vec![Arc::new(BlockedPath(Arc::clone(&released)))])
        .expect("racer");
    racer.race(&[42; 4_096], std::time::Duration::ZERO).expect("initial race");
    measure("network/race/busy/4096", 100_000, || {
        assert!(matches!(
            racer.race(black_box(&[42; 4_096]), std::time::Duration::ZERO),
            Err(ovrt_network::NetworkError::AllPathsBusy)
        ));
    });
    *released.0.lock().expect("release") = true;
    released.1.notify_all();
}

struct BlockedPath(Arc<(std::sync::Mutex<bool>, std::sync::Condvar)>);

impl ovrt_network::Path for BlockedPath {
    fn label(&self) -> &str {
        "blocked"
    }

    fn send(&self, _: &[u8]) -> Result<(), String> {
        let (lock, condition) = &*self.0;
        let _ = condition
            .wait_timeout_while(
                lock.lock().map_err(|error| error.to_string())?,
                std::time::Duration::from_secs(10),
                |ready| !*ready,
            )
            .map_err(|error| error.to_string())?;
        Ok(())
    }
}

fn main() {
    println!("name\tmin_ns\tmedian_ns\tmax_ns\tallocs/op\tbytes/op");
    if std::env::args().any(|argument| argument == "--large") {
        let host = NativeRuntimeHost::new(BTreeMap::new());
        host.register_unit(Arc::new(EchoUnit)).expect("register");
        for size in [65_536, 1_048_576] {
            let input = vec![42; size];
            measure(&format!("native/direct/{size}"), 50_000, || {
                black_box(host.dispatch_direct("bench.echo", black_box(&input)).expect("dispatch"));
            });
            let mut destination = vec![0; size];
            measure(&format!("native/into/{size}"), 50_000, || {
                let written = host
                    .dispatch_direct_into(
                        "bench.echo",
                        black_box(&input),
                        black_box(&mut destination),
                    )
                    .expect("destination");
                assert_eq!(written, input.len());
                black_box(&destination);
            });
        }
        return;
    }
    measure("core/validate_region", 1_000_000, || {
        black_box(ovrt_core::validate_region(black_box(128), black_box(1_048_576), 2_097_152))
            .expect("region");
    });
    unit_and_ffi();
    parallel_direct();
    #[cfg(unix)]
    placement();
    network();
}
