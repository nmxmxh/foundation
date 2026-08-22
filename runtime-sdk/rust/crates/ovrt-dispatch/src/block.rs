//! Shared-memory view of one dispatch region.
//!
//! [`DispatchBlock`] owns a [`SharedMapping`] sized by the generated
//! constants and exposes three accessor families:
//!
//! 1. the flip index and global tick words;
//! 2. per-lane statistics handles — each row has exactly one writer, the
//!    executor that reported it, so its words never bounce between cores;
//! 3. descriptor snapshots — plain reads of whichever buffer the flip index
//!    publishes. Safety rests on the publisher writing the inactive buffer
//!    fully before its Release store (see `publisher`) and on readers taking
//!    the Acquire edge first.
//!
//! Every offset comes from the generated schema bindings; this file
//! hard-codes no geometry of its own.

use std::path::Path;
use std::sync::atomic::{AtomicU32, AtomicU64, Ordering};
use std::sync::Arc;

use ovrt_core::{
    SharedMapping, DISPATCH_BUFFERS_OFFSET, DISPATCH_BUFFER_BYTES, DISPATCH_FLIP_INDEX_OFFSET,
    DISPATCH_LANE_ROW_BYTES, DISPATCH_REGION_BYTES, DISPATCH_STATS_OFFSET, DISPATCH_TICK_OFFSET,
};

use crate::decide::{blend_ewma, LaneDescriptor, LaneStats, MAX_LANES};

// Descriptor field offsets inside one 64-byte slot.
const ROW_UNIT_CLASSES: usize = 0;
const ROW_AFFINITY_BLOOM: usize = 8;
const ROW_LANE_ID: usize = 16;
const ROW_JURISDICTION: usize = 18;
const ROW_MAX_CONCURRENCY: usize = 20;
const ROW_GENERATION: usize = 24;

// Stat row field offsets inside one 64-byte slot.
const STAT_EWMA: usize = 0;
const STAT_INFLIGHT: usize = 8;
const STAT_MAX_CONCURRENCY: usize = 12;
const STAT_LAST_TICK_SEEN: usize = 16;

// Fixed-width little-endian readers. Bounds are guaranteed by the caller's
// row-length check, and the copy form keeps every failure a checked error at
// that boundary instead of a panic buried in decoding.
fn read_u16(row: &[u8], at: usize) -> u16 {
    let mut window = [0_u8; 2];
    window.copy_from_slice(&row[at..at + 2]);
    u16::from_le_bytes(window)
}

fn read_u32(row: &[u8], at: usize) -> u32 {
    let mut window = [0_u8; 4];
    window.copy_from_slice(&row[at..at + 4]);
    u32::from_le_bytes(window)
}

fn read_u64(row: &[u8], at: usize) -> u64 {
    let mut window = [0_u8; 8];
    window.copy_from_slice(&row[at..at + 8]);
    u64::from_le_bytes(window)
}

pub(crate) fn encode_descriptor(descriptor: &LaneDescriptor) -> [u8; 64] {
    let mut bytes = [0_u8; DISPATCH_LANE_ROW_BYTES as usize];
    bytes[ROW_UNIT_CLASSES..ROW_UNIT_CLASSES + 8]
        .copy_from_slice(&descriptor.unit_class_mask.to_le_bytes());
    bytes[ROW_AFFINITY_BLOOM..ROW_AFFINITY_BLOOM + 8]
        .copy_from_slice(&descriptor.affinity_bloom.to_le_bytes());
    bytes[ROW_LANE_ID..ROW_LANE_ID + 2].copy_from_slice(&descriptor.lane_id.to_le_bytes());
    bytes[ROW_JURISDICTION..ROW_JURISDICTION + 2]
        .copy_from_slice(&descriptor.jurisdiction.to_le_bytes());
    bytes[ROW_MAX_CONCURRENCY..ROW_MAX_CONCURRENCY + 4]
        .copy_from_slice(&descriptor.max_concurrency.to_le_bytes());
    bytes[ROW_GENERATION..ROW_GENERATION + 4].copy_from_slice(&descriptor.generation.to_le_bytes());
    bytes
}

pub(crate) fn decode_descriptor(row: &[u8]) -> Result<LaneDescriptor, String> {
    if row.len() < DISPATCH_LANE_ROW_BYTES as usize {
        return Err(format!(
            "descriptor row holds {} bytes; {} required",
            row.len(),
            DISPATCH_LANE_ROW_BYTES
        ));
    }
    Ok(LaneDescriptor {
        unit_class_mask: read_u64(row, ROW_UNIT_CLASSES),
        affinity_bloom: read_u64(row, ROW_AFFINITY_BLOOM),
        lane_id: read_u16(row, ROW_LANE_ID),
        jurisdiction: read_u16(row, ROW_JURISDICTION),
        max_concurrency: read_u32(row, ROW_MAX_CONCURRENCY),
        generation: read_u32(row, ROW_GENERATION),
    })
}

/// Read/write view of one lane's statistics row.
///
/// Exactly one process owns each row: the executor that reports it. All
/// mutation flows through that owner; `apply_mirror` exists for the host that
/// mirrors remote lanes, which is likewise the single writer of those rows
/// locally.
pub struct StatRowHandle<'a> {
    ewma_ns: &'a AtomicU64,
    inflight: &'a AtomicU32,
    max_concurrency: &'a AtomicU32,
    last_tick_seen: &'a AtomicU64,
}

impl<'a> StatRowHandle<'a> {
    /// Marks one unit of work in flight for this lane.
    pub fn claim(&self) -> u32 {
        self.inflight.fetch_add(1, Ordering::AcqRel) + 1
    }

    /// Clears one in-flight unit. Returns false when the row was already at
    /// zero, so an unbalanced release can never wrap the counter.
    pub fn release_one(&self) -> bool {
        self.inflight
            .fetch_update(Ordering::AcqRel, Ordering::Acquire, |current| current.checked_sub(1))
            .is_ok()
    }

    /// Blends one completion sample into the EWMA and refreshes the
    /// heartbeat. Returns the new estimate.
    pub fn record_completion(&self, sample_ns: u64, tick_now: u64) -> u64 {
        let previous = self.ewma_ns.load(Ordering::Acquire);
        let blended = blend_ewma(previous, sample_ns);
        self.ewma_ns.store(blended, Ordering::Release);
        self.last_tick_seen.store(tick_now, Ordering::Release);
        blended
    }

    /// Refreshes liveness without touching the latency estimate.
    pub fn heartbeat(&self, tick_now: u64) {
        self.last_tick_seen.store(tick_now, Ordering::Release);
    }

    /// Host-side overwrite used to mirror a remote lane's reported stats.
    pub(crate) fn apply_mirror(&self, stats: &LaneStats) {
        self.ewma_ns.store(stats.ewma_ns, Ordering::Release);
        self.inflight.store(stats.inflight, Ordering::Release);
        self.max_concurrency.store(stats.max_concurrency, Ordering::Release);
        self.last_tick_seen.store(stats.last_tick_seen, Ordering::Release);
    }

    pub fn snapshot(&self) -> LaneStats {
        LaneStats {
            ewma_ns: self.ewma_ns.load(Ordering::Acquire),
            inflight: self.inflight.load(Ordering::Acquire),
            max_concurrency: self.max_concurrency.load(Ordering::Acquire),
            last_tick_seen: self.last_tick_seen.load(Ordering::Acquire),
        }
    }
}

/// Owns the mapping behind one dispatch region.
///
/// The mapping sits behind an `Arc` so sampled decorators and hosts can hold
/// cheap handles to one region without lifetime plumbing through registries.
pub struct DispatchBlock {
    // Shared with the publisher module, which writes the static buffers.
    pub(crate) mapping: Arc<SharedMapping>,
}

impl DispatchBlock {
    /// Maps `path` at exactly the generated region size. Short files fail
    /// here rather than SIGBUS later.
    pub fn open(path: &Path) -> Result<Self, String> {
        let mapping = SharedMapping::open(path, DISPATCH_REGION_BYTES as usize)?;
        Ok(Self { mapping: Arc::new(mapping) })
    }

    /// Wraps an existing mapping after a length check.
    pub fn from_mapping(mapping: SharedMapping) -> Result<Self, String> {
        if mapping.len() < DISPATCH_REGION_BYTES as usize {
            return Err(format!(
                "dispatch region needs {} bytes; mapping holds {}",
                DISPATCH_REGION_BYTES,
                mapping.len()
            ));
        }
        Ok(Self { mapping: Arc::new(mapping) })
    }

    /// The active-buffer selector: 0 or 1, stored with Release by the
    /// publisher and read with Acquire before any descriptor byte.
    pub fn flip_index(&self) -> Result<&AtomicU32, String> {
        self.mapping.atomic_u32(DISPATCH_FLIP_INDEX_OFFSET as usize)
    }

    /// Reads the global click. Monotonic across processes sharing the region.
    pub fn tick_now(&self) -> Result<u64, String> {
        Ok(self.tick_word()?.load(Ordering::Acquire))
    }

    /// Advances the global click and returns the previous value.
    pub fn advance_tick(&self) -> Result<u64, String> {
        Ok(self.tick_word()?.fetch_add(1, Ordering::AcqRel))
    }

    fn tick_word(&self) -> Result<&AtomicU64, String> {
        self.mapping.atomic_u64(DISPATCH_TICK_OFFSET as usize)
    }

    /// Statistics handle for one lane. Fails on out-of-range lane ids.
    pub fn stat_row(&self, lane: usize) -> Result<StatRowHandle<'_>, String> {
        if lane >= MAX_LANES {
            return Err(format!("lane {lane} sits outside the {MAX_LANES}-lane table"));
        }
        let base = DISPATCH_STATS_OFFSET as usize + lane * DISPATCH_LANE_ROW_BYTES as usize;
        Ok(StatRowHandle {
            ewma_ns: self.mapping.atomic_u64(base + STAT_EWMA)?,
            inflight: self.mapping.atomic_u32(base + STAT_INFLIGHT)?,
            max_concurrency: self.mapping.atomic_u32(base + STAT_MAX_CONCURRENCY)?,
            last_tick_seen: self.mapping.atomic_u64(base + STAT_LAST_TICK_SEEN)?,
        })
    }

    /// Decodes the descriptor table currently published by the flip index.
    ///
    /// Rows are read as plain bytes: the publisher's Release store on the
    /// flip index happens-before this function's Acquire load, which orders
    /// every prior write into the buffer. See `publisher` for the other half.
    pub fn snapshot_descriptors(&self) -> Result<Vec<LaneDescriptor>, String> {
        let active = self.active_buffer_index()?;
        let base = DISPATCH_BUFFERS_OFFSET as usize + active * DISPATCH_BUFFER_BYTES as usize;
        let bytes = self.mapping.as_slice();
        (0..MAX_LANES)
            .map(|lane| {
                decode_descriptor(&bytes[base + lane * 64..][..DISPATCH_LANE_ROW_BYTES as usize])
            })
            .collect()
    }

    fn active_buffer_index(&self) -> Result<usize, String> {
        let index = self.flip_index()?.load(Ordering::Acquire);
        if index >= 2 {
            return Err(format!("flip index {index} selects no buffer"));
        }
        Ok(index as usize)
    }
}

#[cfg(test)]
#[cfg(unix)]
mod tests {
    use super::*;

    fn temp_region() -> tempfile::NamedTempFile {
        let file = tempfile::NamedTempFile::new().expect("temp file");
        file.as_file().set_len(u64::from(DISPATCH_REGION_BYTES)).expect("size region");
        file
    }

    #[test]
    fn descriptor_codec_roundtrips_every_field() {
        let original = LaneDescriptor {
            lane_id: 7,
            jurisdiction: 42,
            max_concurrency: 9,
            generation: 3,
            unit_class_mask: 0xA5A5_5A5A_0101_00FF,
            affinity_bloom: 1 << 40 | 1 << 3,
        };
        let decoded = decode_descriptor(&encode_descriptor(&original)).expect("decode");
        assert_eq!(decoded, original);
    }

    #[test]
    fn decode_rejects_short_rows() {
        assert!(decode_descriptor(&[0_u8; 16]).is_err(), "short rows must fail");
    }

    #[test]
    fn tick_advances_monotonically() {
        let file = temp_region();
        let block = DispatchBlock::open(file.path()).expect("open");
        let first = block.tick_now().expect("tick");
        let advanced = block.advance_tick().expect("advance");
        let second = block.tick_now().expect("tick");
        assert_eq!(advanced, first);
        assert_eq!(second, first + 1);
    }

    #[test]
    fn completion_blends_with_the_fixed_alpha_and_tracks_inflight() {
        let file = temp_region();
        let block = DispatchBlock::open(file.path()).expect("open");

        let stats = block.stat_row(3).expect("stats row");
        stats.apply_mirror(&LaneStats {
            ewma_ns: 8_000,
            inflight: 0,
            max_concurrency: 4,
            last_tick_seen: 0,
        });

        assert_eq!(stats.claim(), 1);
        assert_eq!(stats.claim(), 2);

        let tick = block.advance_tick().expect("advance");
        let blended = stats.record_completion(16_000, tick);
        // (1·16_000 + 7·8_000) / 8 = 9_000.
        assert_eq!(blended, 9_000);
        assert_eq!(stats.snapshot().last_tick_seen, tick);

        assert!(stats.release_one(), "first release clears an in-flight unit");
        assert!(stats.release_one(), "second release clears the last unit");
        assert!(!stats.release_one(), "release below zero must be refused");
    }

    #[test]
    fn open_refuses_a_short_region() {
        let file = tempfile::NamedTempFile::new().expect("temp file");
        file.as_file().set_len(128).expect("shrink");
        let error = DispatchBlock::open(file.path()).err().expect("refusal");
        assert!(error.contains("bytes"), "unexpected error: {error}");
    }

    #[test]
    fn stat_row_bounds_are_enforced() {
        let file = temp_region();
        let block = DispatchBlock::open(file.path()).expect("open");
        assert!(block.stat_row(MAX_LANES).is_err(), "out-of-range lane must fail");
    }
}
