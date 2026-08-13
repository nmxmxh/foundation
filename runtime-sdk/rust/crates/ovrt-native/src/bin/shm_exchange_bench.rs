//! What the shared-memory exchange body costs, before and after mapping.
//!
//! This measures only the part of an exchange that changed: getting the control
//! buffer in front of the unit and the result back out. The doorbell — the
//! stdio unit-id frame and its acknowledgement — is not here, because it is not
//! what this benchmark is about and it dominates anything it is included with.
//!
//! `positional` is the loop this crate used to run: open the segment as an
//! ordinary file, `pread` the whole 4 KiB control buffer into a fresh `Vec`,
//! process it, `pwrite` 4 KiB back. `mapped` is the current one: the segment is
//! mapped once at startup and the unit runs against it where it lies.
//!
//! Run with `cargo run --release -p ovrt-native --bin shm_exchange_bench`.

use std::collections::BTreeMap;
use std::hint::black_box;
use std::sync::Arc;
use std::time::Instant;

use ovrt_core::{RuntimeRole, RuntimeUnitDescriptor, BUFFER_TOTAL_BYTES};
use ovrt_native::{process_runtime_buffer_in_place, NativeBuffer, NativeRuntimeHost};
use ovrt_unit::RuntimeUnit;

const ITERS: usize = 200_000;
const UNIT_ID: &str = "bench.echo";

struct EchoUnit;

impl RuntimeUnit for EchoUnit {
    fn descriptor(&self) -> RuntimeUnitDescriptor {
        RuntimeUnitDescriptor {
            unit_id: UNIT_ID.to_string(),
            role: RuntimeRole::Compute,
            input_schema: "foundation/v1/envelope.capnp".to_string(),
            output_schema: "foundation/v1/envelope.capnp".to_string(),
            supports_wasm: false,
            supports_native: true,
            requires_shared_memory: true,
            supports_gpu: false,
            max_concurrency: 1,
        }
    }

    fn run(&self, input: &[u8]) -> Result<Vec<u8>, String> {
        Ok(input.to_vec())
    }
}

fn bench_ns(name: &str, mut f: impl FnMut()) -> f64 {
    let start = Instant::now();
    for _ in 0..ITERS {
        f();
    }
    let ns = start.elapsed().as_nanos() as f64 / ITERS as f64;
    println!("{name:<46} {ns:>10.2} ns/op");
    ns
}

#[cfg(unix)]
fn main() {
    use std::io::Write;
    use std::os::unix::fs::FileExt;

    use ovrt_core::SharedMapping;

    let host = NativeRuntimeHost::new(BTreeMap::new());
    host.register_unit(Arc::new(EchoUnit)).expect("register unit");

    let mut seed = NativeBuffer::with_capacity();
    seed.initialize_control_plane(1).expect("init control plane");
    seed.write_input_bytes(&vec![17_u8; 1024]).expect("seed input");
    let seed = seed.into_inner();

    // A plain temp file rather than a dev-dependency: this is a bin, so it only
    // sees the crate's real dependencies, and the host creates its segments the
    // same way (see newSharedMemorySegment in shared_memory_unix.go).
    let path = std::env::temp_dir().join(format!("ovrt-shm-bench-{}", std::process::id()));
    let mut file = std::fs::File::create(&path).expect("create segment");
    file.write_all(&seed).expect("size segment");
    file.flush().expect("flush segment");
    drop(file);

    let positional = {
        let handle =
            std::fs::OpenOptions::new().read(true).write(true).open(&path).expect("open segment");
        bench_ns("positional: pread + alloc + process + pwrite", || {
            let mut raw = vec![0_u8; BUFFER_TOTAL_BYTES as usize];
            handle.read_exact_at(&mut raw, 0).expect("pread");
            process_runtime_buffer_in_place(&host, UNIT_ID, black_box(raw.as_mut_slice()))
                .expect("process");
            handle.write_all_at(&raw, 0).expect("pwrite");
        })
    };

    let mapped = {
        let mut mapping =
            SharedMapping::open(&path, BUFFER_TOTAL_BYTES as usize).expect("map segment");
        bench_ns("mapped: process in place", || {
            process_runtime_buffer_in_place(&host, UNIT_ID, black_box(mapping.as_mut_slice()))
                .expect("process");
        })
    };

    // The unit is an echo, so whatever remains is the floor: the epoch and
    // header writes every exchange performs regardless of transport. Reporting
    // the delta rather than the ratio keeps the comparison honest — the
    // interesting number is the nanoseconds a real exchange stops paying.
    println!();
    println!("control buffer overhead removed{:>23.2} ns/op", positional - mapped);
    println!();

    bench_arena();

    let _ = std::fs::remove_file(&path);
}

/// The arena's half of the same question.
///
/// A slab read is where positional I/O was most expensive and least visible:
/// `descriptor` issued four separate four-byte `pread`s before the slab read
/// even began, and every one of them allocated. A mapped read is an offset.
#[cfg(unix)]
fn bench_arena() {
    use std::io::Write;
    use std::os::unix::fs::FileExt;

    use ovrt_core::generated::{
        ARENA_DESCRIPTOR_STATE_READY, ARENA_HEADER_IDX_CAPACITY_BYTES, ARENA_HEADER_IDX_MAGIC,
        ARENA_HEADER_IDX_SCHEMA_VERSION, ARENA_HEADER_MAGIC, ARENA_MIN_BYTES,
        ARENA_OFFSET_DESCRIPTOR_TABLE, ARENA_OFFSET_HEADER, ARENA_OFFSET_PAGES,
        ARENA_SCHEMA_VERSION,
    };
    use ovrt_native::arena::Arena;

    const SLAB_BYTES: usize = 64 * 1024;

    let capacity = ARENA_MIN_BYTES.max(ARENA_OFFSET_PAGES + SLAB_BYTES as u32);
    let mut raw = vec![0_u8; capacity as usize];
    let mut put = |at: usize, value: u32| {
        raw[at..at + 4].copy_from_slice(&value.to_le_bytes());
    };
    let header = |index: u32| (ARENA_OFFSET_HEADER + index * 4) as usize;
    put(header(ARENA_HEADER_IDX_MAGIC), ARENA_HEADER_MAGIC);
    put(header(ARENA_HEADER_IDX_SCHEMA_VERSION), ARENA_SCHEMA_VERSION);
    put(header(ARENA_HEADER_IDX_CAPACITY_BYTES), capacity);
    let entry = ARENA_OFFSET_DESCRIPTOR_TABLE as usize;
    put(entry, ARENA_DESCRIPTOR_STATE_READY);
    put(entry + 4, ARENA_OFFSET_PAGES);
    put(entry + 8, SLAB_BYTES as u32);

    let path = std::env::temp_dir().join(format!("ovrt-arena-bench-{}", std::process::id()));
    let mut file = std::fs::File::create(&path).expect("create arena");
    file.write_all(&raw).expect("size arena");
    file.flush().expect("flush arena");
    drop(file);

    let positional = {
        let handle = std::fs::OpenOptions::new().read(true).open(&path).expect("open arena");
        bench_ns("arena positional: 4 pread descriptor + slab", || {
            let mut word = [0_u8; 4];
            let mut fields = [0_u32; 4];
            for (index, field) in fields.iter_mut().enumerate() {
                handle.read_exact_at(&mut word, (entry + index * 4) as u64).expect("pread field");
                *field = u32::from_le_bytes(word);
            }
            let mut slab = vec![0_u8; fields[2] as usize];
            handle.read_exact_at(&mut slab, u64::from(fields[1])).expect("pread slab");
            black_box(slab);
        })
    };

    let mapped = {
        let arena = Arena::open(path.to_str().expect("arena path")).expect("open arena");
        bench_ns("arena mapped: descriptor + slab borrow", || {
            black_box(arena.slab(0).expect("slab"));
        })
    };

    println!();
    println!(
        "arena slab read overhead removed ({SLAB_BYTES} B){:>10.2} ns/op",
        positional - mapped
    );

    let _ = std::fs::remove_file(&path);
}

#[cfg(not(unix))]
fn bench_arena() {}

#[cfg(not(unix))]
fn main() {
    println!("shared memory exchange benchmark requires a unix platform");
}
