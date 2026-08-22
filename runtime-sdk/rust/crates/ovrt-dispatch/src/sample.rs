//! Completion sampling so measured latency, not guesses, drives placement.
//!
//! [`SampledUnit`] decorates any executor: it claims an in-flight slot,
//! times the inner run, blends the sample into the lane's EWMA at the fixed
//! α, stamps the heartbeat, and releases the slot. Failed runs sample too —
//! they consumed the same resources, and hiding them would let a degrading
//! lane keep winning selections.
//!
//! The decorator is deliberately non-generic over the inner unit: hosts
//! register trait objects, and wrapping `Arc<dyn RuntimeUnit>` keeps one
//! concrete type on that path.

use std::sync::Arc;
use std::time::Instant;

use ovrt_core::RuntimeUnitDescriptor;
use ovrt_unit::RuntimeUnit;

use crate::block::DispatchBlock;

/// Capability bit for a role ordinal, shared by descriptor publication and
/// placement requests so both sides speak one vocabulary.
///
/// Mirrors the fieldless [`ovrt_core::RuntimeRole`] ordering: Pulse, Compute,
/// Gpu, Io map to bits 0..3. Keep this function the single translation point.
pub fn class_mask_for_role_index(role_index: usize) -> u64 {
    1_u64 << role_index
}

/// Wraps one executor so its completions feed the placement table.
pub struct SampledUnit {
    inner: Arc<dyn RuntimeUnit>,
    block: Arc<DispatchBlock>,
    lane: usize,
}

impl SampledUnit {
    pub fn new(inner: Arc<dyn RuntimeUnit>, block: Arc<DispatchBlock>, lane: usize) -> Self {
        Self { inner, block, lane }
    }
}

impl RuntimeUnit for SampledUnit {
    fn descriptor(&self) -> RuntimeUnitDescriptor {
        self.inner.descriptor()
    }

    fn run(&self, input: &[u8]) -> Result<Vec<u8>, String> {
        let stats = self.block.stat_row(self.lane)?;
        stats.claim();
        let started = Instant::now();
        let result = self.inner.run(input);

        let elapsed = started.elapsed().as_nanos();
        // Clamp instead of panicking: a clock jump past u64 nanoseconds
        // (nearly six centuries) still yields a bounded sample.
        let sample_ns = if elapsed > u128::from(u64::MAX) { u64::MAX } else { elapsed as u64 };

        // Every completion issues its own click so heartbeats track real
        // work; reading without advancing would stamp the stale sentinel.
        let _ = self.block.advance_tick()?;
        let tick = self.block.tick_now()?;
        stats.record_completion(sample_ns, tick);
        // The single-owner invariant keeps claim and release balanced; the
        // boolean only guards against a bookkeeping bug somewhere else, and
        // compute results must never depend on that bookkeeping.
        let _ = stats.release_one();
        result
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use ovrt_core::DISPATCH_REGION_BYTES;
    use tempfile::NamedTempFile;

    struct SlowEcho;

    impl RuntimeUnit for SlowEcho {
        fn descriptor(&self) -> RuntimeUnitDescriptor {
            RuntimeUnitDescriptor {
                unit_id: "echo.sampled".to_string(),
                role: ovrt_core::RuntimeRole::Compute,
                input_schema: "test".to_string(),
                output_schema: "test".to_string(),
                supports_wasm: true,
                supports_native: true,
                requires_shared_memory: false,
                supports_gpu: false,
                max_concurrency: 4,
            }
        }

        fn run(&self, input: &[u8]) -> Result<Vec<u8>, String> {
            Ok(input.to_vec())
        }
    }

    fn temp_block() -> (NamedTempFile, Arc<DispatchBlock>) {
        let file = NamedTempFile::new().expect("temp file");
        file.as_file().set_len(u64::from(DISPATCH_REGION_BYTES)).expect("size");
        let block = Arc::new(DispatchBlock::open(file.path()).expect("open"));
        (file, block)
    }

    #[test]
    fn sampled_runs_feed_the_lane_row_and_rebalance_inflight() {
        let (_file, block) = temp_block();
        let unit = SampledUnit::new(Arc::new(SlowEcho), Arc::clone(&block), 2);

        let output = unit.run(b"ping").expect("run");
        assert_eq!(output, b"ping");

        let stats = block.stat_row(2).expect("stats").snapshot();
        assert!(stats.ewma_ns > 0, "completion must seed the estimate");
        assert_eq!(stats.inflight, 0, "claim/release must stay balanced");
        assert!(stats.last_tick_seen > 0, "heartbeat stamped from the click");
    }

    #[test]
    fn failed_runs_still_sample_so_degradation_shows() {
        struct AlwaysFails;
        impl RuntimeUnit for AlwaysFails {
            fn descriptor(&self) -> RuntimeUnitDescriptor {
                SlowEcho.descriptor()
            }
            fn run(&self, _input: &[u8]) -> Result<Vec<u8>, String> {
                Err("boom".to_string())
            }
        }

        let (_file, block) = temp_block();
        let unit = SampledUnit::new(Arc::new(AlwaysFails), Arc::clone(&block), 1);
        assert!(unit.run(b"x").is_err());

        let stats = block.stat_row(1).expect("stats").snapshot();
        assert!(stats.ewma_ns > 0, "failures must feed the estimate");
        assert_eq!(stats.inflight, 0);
    }

    #[test]
    fn bad_lane_configuration_fails_before_execution() {
        let (_file, block) = temp_block();
        let unit = SampledUnit::new(Arc::new(SlowEcho), Arc::clone(&block), MAX_TEST_LANES);
        assert!(unit.run(b"ping").is_err(), "out-of-range lane must refuse work");
    }

    const MAX_TEST_LANES: usize = 32;
}
