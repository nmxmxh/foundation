//! Publication of descriptor tables and mirrored statistics.
//!
//! The publisher is the only writer of the static buffers. It fills the
//! inactive buffer completely, then flips the index with a Release store.
//! Readers take the Acquire edge before touching any row byte, so every byte
//! they observe belongs to one coherent table generation.
//!
//! Mirrored statistics follow the same single-writer rule from the host
//! side: a remote lane's numbers arrive over transport and exactly one local
//! process applies them into the locally owned stat row.
//!
//! The loom module at the bottom models both protocols that this crate's
//! correctness rests on — buffer publication and global-tick issuance —
//! over every legal thread interleaving.

#[cfg(unix)]
use std::sync::atomic::Ordering;

#[cfg(unix)]
use ovrt_core::{
    DISPATCH_BUFFERS_OFFSET, DISPATCH_BUFFER_BYTES, DISPATCH_JURISDICTION_GLOBAL,
    DISPATCH_LANE_ROW_BYTES,
};

#[cfg(unix)]
use crate::block::{encode_descriptor, DispatchBlock};

use crate::decide::{LaneDescriptor, LaneStats, MAX_LANES};

// Retired slots encode as ineligible: empty class mask selects nothing.
#[cfg(unix)]
fn retired_descriptor(slot: usize, generation: u32) -> LaneDescriptor {
    LaneDescriptor {
        lane_id: slot as u16,
        jurisdiction: DISPATCH_JURISDICTION_GLOBAL as u16,
        max_concurrency: 0,
        generation,
        unit_class_mask: 0,
        affinity_bloom: 0,
    }
}

#[cfg(unix)]
impl DispatchBlock {
    /// Publishes a full descriptor table and returns the buffer index now
    /// active.
    ///
    /// Rows are positional: slot `i` serves lane id `i`, so the table never
    /// fragments across generations. Unlisted slots become retired rows.
    /// Every slot of the inactive buffer is written before the Release flip;
    /// no reader can observe a half-written table.
    pub fn publish_descriptors(
        &self,
        rows: &[LaneDescriptor],
        generation: u32,
    ) -> Result<u32, String> {
        if rows.len() > MAX_LANES {
            return Err(format!("{} rows exceed the {}-lane table", rows.len(), MAX_LANES));
        }
        let active = self.flip_index()?.load(Ordering::Acquire);
        if active >= 2 {
            return Err(format!("flip index {active} selects no buffer"));
        }
        let target = 1 - active;
        let base =
            DISPATCH_BUFFERS_OFFSET as usize + target as usize * DISPATCH_BUFFER_BYTES as usize;
        for slot in 0..MAX_LANES {
            let mut descriptor = match rows.get(slot) {
                Some(row) => *row,
                None => retired_descriptor(slot, generation),
            };
            descriptor.lane_id = slot as u16;
            descriptor.generation = generation;
            self.mapping.write_at(
                base + slot * DISPATCH_LANE_ROW_BYTES as usize,
                &encode_descriptor(&descriptor),
            )?;
        }
        self.flip_index()?.store(target, Ordering::Release);
        Ok(target)
    }

    /// Applies one remote lane's reported statistics into its locally owned
    /// mirror row.
    pub fn apply_mirror_stats(&self, lane: usize, stats: &LaneStats) -> Result<(), String> {
        self.stat_row(lane)?.apply_mirror(stats);
        Ok(())
    }
}

// The model cells need raw pointers, so this one module opts out of the
// crate-level unsafe deny; production paths never touch it.
//
// Both cfg attributes stay on their own lines: the runtime practices check
// treats a bare `#[cfg(test)]` line as the end of production source.
#[cfg(test)]
#[cfg(feature = "loom")]
#[allow(unsafe_code)]
mod loom_verification {
    use loom::cell::UnsafeCell;
    use loom::sync::atomic::{AtomicU64, AtomicUsize, Ordering};
    use loom::sync::{Arc, Mutex};

    const SENTINEL: u64 = 0xD15C_0001;

    struct ModelTable {
        index: AtomicUsize,
        slabs: [UnsafeCell<u64>; 2],
    }

    // SAFETY: the model's fields are only touched through the flip protocol
    // under test; loom verifies no interleaving races them.
    unsafe impl Send for ModelTable {}
    // SAFETY: same protocol as `Send`; every shared access is either atomic
    // or ordered by the index Acquire edge.
    unsafe impl Sync for ModelTable {}
    #[test]
    fn flip_publishes_a_complete_table() {
        loom::model(|| {
            let table = Arc::new(ModelTable {
                index: AtomicUsize::new(0),
                slabs: [UnsafeCell::new(0), UnsafeCell::new(0)],
            });

            let publisher = {
                let table = Arc::clone(&table);
                loom::thread::spawn(move || {
                    let target = 1 - table.index.load(Ordering::Acquire);
                    // SAFETY: exclusive until published; the model checks
                    // every interleaving for races against the reader below.
                    table.slabs[target].with_mut(|slot| unsafe { *slot = SENTINEL });
                    table.index.store(target, Ordering::Release);
                })
            };

            let observed = table.index.load(Ordering::Acquire);
            // SAFETY: gated behind the Acquire edge of the flip word, which
            // happens-after the publisher's full write.
            let value = table.slabs[observed].with(|slot| unsafe { *slot });
            assert!(value == 0 || value == SENTINEL, "reader saw a torn table");

            assert!(publisher.join().is_ok(), "publisher must complete");
        });
    }

    #[test]
    fn every_fetch_add_returns_a_unique_click() {
        loom::model(|| {
            let tick = Arc::new(AtomicU64::new(0));
            let seen = Arc::new(Mutex::new(Vec::<u64>::new()));

            let mut handles = Vec::new();
            for _ in 0..2 {
                let tick = Arc::clone(&tick);
                let seen = Arc::clone(&seen);
                handles.push(loom::thread::spawn(move || {
                    let previous = tick.fetch_add(1, Ordering::AcqRel);
                    seen.lock().expect("lock").push(previous);
                }));
            }
            for handle in handles {
                handle.join().expect("join");
            }

            let mut clicks = seen.lock().expect("lock").clone();
            clicks.sort_unstable();
            assert_eq!(clicks, vec![0, 1], "each caller owns exactly one click");
        });
    }
}

#[cfg(test)]
#[cfg(unix)]
mod tests {
    use super::*;
    use crate::block::DispatchBlock;
    use crate::decide::{decide, DispatchRequest};
    use ovrt_core::{DISPATCH_REGION_BYTES, DISPATCH_STALE_TICKS};
    use tempfile::NamedTempFile;

    fn temp_region() -> NamedTempFile {
        let file = NamedTempFile::new().expect("temp file");
        file.as_file().set_len(u64::from(DISPATCH_REGION_BYTES)).expect("size region");
        file
    }

    fn lane(id: u16, jurisdiction: u16, affinity_bloom: u64) -> LaneDescriptor {
        LaneDescriptor {
            lane_id: id,
            jurisdiction,
            max_concurrency: 4,
            generation: 1,
            unit_class_mask: 0b01,
            affinity_bloom,
        }
    }

    fn snapshot_stats(block: &DispatchBlock) -> Vec<Option<LaneStats>> {
        (0..MAX_LANES).map(|lane| block.stat_row(lane).ok().map(|row| row.snapshot())).collect()
    }

    #[test]
    fn publish_roundtrips_and_alternates_buffers() {
        let file = temp_region();
        let block = DispatchBlock::open(file.path()).expect("open");

        let first = block.publish_descriptors(&[lane(0, 7, 1 << 3)], 1).expect("publish");
        assert_eq!(first, 1, "buffer 0 starts active, so the first flip targets buffer 1");
        let snapshot = block.snapshot_descriptors().expect("snapshot");
        assert_eq!(snapshot[0].lane_id, 0);
        assert_eq!(snapshot[0].jurisdiction, 7);
        assert_eq!(snapshot[0].generation, 1);
        assert_eq!(snapshot[5].unit_class_mask, 0, "unlisted slots retire");

        let second = block.publish_descriptors(&[], 2).expect("republish");
        assert_ne!(first, second, "generations alternate buffers");
        let snapshot = block.snapshot_descriptors().expect("snapshot");
        assert_eq!(snapshot[0].unit_class_mask, 0);
        assert_eq!(snapshot[0].generation, 2);
    }

    #[test]
    fn publish_rejects_oversized_tables() {
        let file = temp_region();
        let block = DispatchBlock::open(file.path()).expect("open");
        let oversized = (0..=MAX_LANES).map(|slot| lane(slot as u16, 0, 0)).collect::<Vec<_>>();
        assert!(block.publish_descriptors(&oversized, 1).is_err());
    }

    #[test]
    fn end_to_end_selection_over_a_real_region() {
        let file = temp_region();
        let block = DispatchBlock::open(file.path()).expect("open");
        block.publish_descriptors(&[lane(0, 7, 1 << 3), lane(1, 0, 0)], 1).expect("publish");

        // Sample both lanes once so neither looks unsampled. Stamps carry the
        // current click: fetch_add hands back the previous value, and a zero
        // heartbeat reads as never-checked-in.
        block.advance_tick().expect("tick");
        block.stat_row(0).expect("row").record_completion(5_000, block.tick_now().expect("tick"));
        block.advance_tick().expect("tick");
        block.stat_row(1).expect("row").record_completion(6_000, block.tick_now().expect("tick"));

        let request = DispatchRequest {
            required_class_mask: 0b01,
            jurisdiction: 7,
            deadline_ns: u64::MAX,
            affinity_key: 9,
        };

        let snapshot = block.snapshot_descriptors().expect("snapshot");
        let stats = snapshot_stats(&block);
        let now = block.tick_now().expect("tick");
        assert_eq!(decide(now, &snapshot, &stats, &request), Some(0));

        // Queue pressure flips the choice through real region bytes.
        for _ in 0..4 {
            block.stat_row(0).expect("row").claim();
        }
        let stats = snapshot_stats(&block);
        assert_eq!(decide(now, &snapshot, &stats, &request), Some(1));

        // Age the clock past the freshness window, mark the local lane's
        // heartbeat stale, and watch selection settle on the global peer.
        for _ in 0..u64::from(DISPATCH_STALE_TICKS) + 2 {
            block.advance_tick().expect("tick");
        }
        while block.stat_row(0).expect("row").release_one() {}
        let now = block.tick_now().expect("tick");
        // The surviving peer keeps its heartbeat fresh across the aged clock.
        block.stat_row(1).expect("row").heartbeat(now);
        let stale_seen = now - u64::from(DISPATCH_STALE_TICKS) - 1;
        block
            .apply_mirror_stats(
                0,
                &LaneStats {
                    ewma_ns: 5_000,
                    inflight: 0,
                    max_concurrency: 4,
                    last_tick_seen: stale_seen,
                },
            )
            .expect("mirror");
        let stats = snapshot_stats(&block);
        assert_eq!(decide(now, &snapshot, &stats, &request), Some(1));

        // An impossible deadline gates everything out.
        let tight = DispatchRequest { deadline_ns: 100, ..request };
        assert_eq!(decide(now, &snapshot, &stats, &tight), None);
    }
}
