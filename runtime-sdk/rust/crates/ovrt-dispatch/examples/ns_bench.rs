//! Measures the per-dispatch placement decision cost in nanoseconds.
//!
//! Run: `cargo run --release -p ovrt-dispatch --example ns_bench`
//! (driven by `make test-bench-dispatch`).
//!
//! Two numbers matter:
//!
//! 1. **pure decide** — cached descriptor tables, one Acquire tick read plus
//!    the argmin scan. This is the floor for any caller that keeps a snapshot
//!    between table generations.
//! 2. **full placement** — tick read plus a fresh 32-row descriptor
//!    snapshot and stats sweep per call, which is what the native host pays
//!    today before any caching.
//!
//! Gates keep the claim honest: pure median must stay under 500ns and full
//! median under 5µs. Breaching either exits nonzero so the make target fails
//! loudly instead of quietly redefining "nanosecond class".

#[cfg(unix)]
fn main_real() -> Result<(), String> {
    use ovrt_dispatch::{
        decide, DispatchBlock, DispatchLaneDescriptor, DispatchLaneStats, DispatchRequest,
        MAX_LANES,
    };
    use std::hint::black_box;
    use std::sync::Arc;
    use std::time::Instant;
    use tempfile::NamedTempFile;

    let file = NamedTempFile::new().map_err(|error| error.to_string())?;
    file.as_file()
        .set_len(u64::from(ovrt_core::DISPATCH_REGION_BYTES))
        .map_err(|error| error.to_string())?;
    let block = Arc::new(DispatchBlock::open(file.path()).map_err(|error| error.to_string())?);

    // Eight live lanes covering class bit 0 with staggered estimates; the
    // remaining slots stay retired, matching a realistic table.
    let mut rows = Vec::new();
    for slot in 0..8_usize {
        rows.push(DispatchLaneDescriptor {
            lane_id: slot as u16,
            jurisdiction: 0,
            max_concurrency: 8,
            generation: 1,
            unit_class_mask: 1,
            affinity_bloom: 1 << (slot % 64),
        });
    }
    block.publish_descriptors(&rows, 1).map_err(|error| error.to_string())?;

    block.advance_tick()?;
    let seed_tick = block.tick_now()?;
    for lane in 0..MAX_LANES {
        let live = lane < 8;
        block
            .apply_mirror_stats(
                lane,
                &DispatchLaneStats {
                    ewma_ns: if live { 1_000 + lane as u64 * 250 } else { 0 },
                    inflight: (lane % 3) as u32,
                    max_concurrency: 8,
                    last_tick_seen: if live { seed_tick } else { 0 },
                },
            )
            .map_err(|error| error.to_string())?;
    }

    let request = DispatchRequest {
        required_class_mask: 1,
        jurisdiction: 7,
        deadline_ns: u64::MAX,
        affinity_key: 2,
    };
    let descriptors = block.snapshot_descriptors().map_err(|error| error.to_string())?;
    let build_stats = || block.snapshot_stats();

    // Warm caches and clocks.
    for _ in 0..20_000_u32 {
        black_box(decide(block.tick_now()?, &descriptors, &build_stats(), &request));
    }

    const PURE_ITERS: usize = 200_000;
    let mut pure: Vec<u128> = Vec::with_capacity(PURE_ITERS);
    let cached_stats = build_stats();
    for _ in 0..PURE_ITERS {
        let started = Instant::now();
        let now = block.tick_now()?;
        let selected = black_box(decide(
            now,
            black_box(&descriptors),
            black_box(&cached_stats),
            black_box(&request),
        ));
        pure.push(started.elapsed().as_nanos());
        if selected.is_none() {
            return Err("benchmark lost all eligible lanes".to_owned());
        }
    }
    report("pure decide", pure, 500)?;

    const FULL_ITERS: usize = 20_000;
    let mut full: Vec<u128> = Vec::with_capacity(FULL_ITERS);
    for _ in 0..FULL_ITERS {
        let started = Instant::now();
        let measured = (|| -> Result<(), String> {
            let now = block.tick_now()?;
            let descriptors = block.snapshot_descriptors()?;
            let stats = build_stats();
            if black_box(decide(
                now,
                black_box(&descriptors),
                black_box(&stats),
                black_box(&request),
            ))
            .is_none()
            {
                return Err("benchmark lost all eligible lanes".to_owned());
            }
            Ok(())
        })();
        full.push(started.elapsed().as_nanos());
        measured?;
    }
    report("full placement", full, 5_000)?;

    println!("[OK] dispatch decision stays nanosecond-class");
    Ok(())
}

#[cfg(unix)]
fn report(label: &str, mut samples: Vec<u128>, gate_ns: u128) -> Result<(), String> {
    samples.sort_unstable();
    let pick = |fraction: f64| -> u128 {
        let index = ((samples.len() - 1) as f64 * fraction).round() as usize;
        samples[index]
    };
    let (min, median, p99) = (samples[0], pick(0.50), pick(0.99));
    println!("{label}: min={min}ns p50={median}ns p99={p99}ns (gate {gate_ns}ns)");
    if median > gate_ns {
        return Err(format!("{label} median {median}ns breaches the {gate_ns}ns gate"));
    }
    Ok(())
}

#[cfg(unix)]
fn main() {
    std::process::exit(match main_real() {
        Ok(()) => 0,
        Err(error) => {
            eprintln!("dispatch bench failed: {error}");
            1
        }
    });
}

#[cfg(not(unix))]
fn main() {
    println!("dispatch bench requires a unix host with shared-memory support");
}
