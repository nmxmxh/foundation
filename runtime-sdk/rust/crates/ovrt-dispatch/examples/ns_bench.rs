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
//! 2. **full placement** — tick advance plus a fresh 32-row descriptor
//!    snapshot and stats sweep per call, which is what the native host pays
//!    today before any caching.
//!
//! Gates keep the claim honest: pure median must stay under 500ns and full
//! median under 5µs. Breaching either exits nonzero so the make target fails
//! loudly instead of quietly redefining "nanosecond class".

#[cfg(unix)]
fn main_real() -> Result<(), String> {
    use ovrt_dispatch::{
        decide, DispatchBlock, DispatchRequest, LaneDescriptor, LaneStats, MAX_LANES,
    };
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
        rows.push(LaneDescriptor {
            lane_id: slot as u16,
            jurisdiction: 0,
            max_concurrency: 8,
            generation: 1,
            unit_class_mask: 1,
            affinity_bloom: 1 << (slot % 64),
        });
    }
    block.publish_descriptors(&rows, 1).map_err(|error| error.to_string())?;

    let seed_tick = block.advance_tick().map_err(|error| error.to_string())?;
    for lane in 0..MAX_LANES {
        let live = lane < 8;
        block
            .apply_mirror_stats(
                lane,
                &LaneStats {
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
    let build_stats = || -> Vec<Option<LaneStats>> {
        (0..MAX_LANES).map(|lane| block.stat_row(lane).ok().map(|row| row.snapshot())).collect()
    };

    // Warm caches and clocks.
    for _ in 0..20_000_u32 {
        let _ = block.advance_tick();
        let _ = decide(block.tick_now().unwrap_or(0), &descriptors, &build_stats(), &request);
    }

    const PURE_ITERS: usize = 200_000;
    let mut pure: Vec<u128> = Vec::with_capacity(PURE_ITERS);
    let cached_stats = build_stats();
    for _ in 0..PURE_ITERS {
        let started = Instant::now();
        let now = block.tick_now()?;
        let _ = decide(now, &descriptors, &cached_stats, &request);
        pure.push(started.elapsed().as_nanos());
    }
    report("pure decide", pure, 500)?;

    const FULL_ITERS: usize = 20_000;
    let mut full: Vec<u128> = Vec::with_capacity(FULL_ITERS);
    for _ in 0..FULL_ITERS {
        let started = Instant::now();
        let measured = (|| -> Result<(), String> {
            let now = block.advance_tick()?;
            let descriptors = block.snapshot_descriptors()?;
            let stats = build_stats();
            let _ = decide(now, &descriptors, &stats, &request);
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
