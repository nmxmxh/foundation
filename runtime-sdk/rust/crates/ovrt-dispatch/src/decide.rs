//! Pure placement decision over lane tables.
//!
//! Everything here is plain data in, option out: no atomics, no mapping, no
//! I/O. The shared-memory layer in `block` feeds snapshots into [`decide`],
//! which keeps the selection rules testable as golden vectors and lets loom
//! model the publication protocol without modeling a filesystem.
//!
//! Selection contract, in order:
//! 1. capability cover — required class mask must be a subset of the lane's
//! 2. jurisdiction — exact match or a lane declared global; anything else
//!    selects nothing (fail closed)
//! 3. freshness — a heartbeat older than `DISPATCH_STALE_TICKS` excludes the
//!    lane
//! 4. sampling — a lane with no measured completion (`ewma_ns == 0`) earns no
//!    traffic until its first completion primes the estimate
//! 5. feasibility — expected latency must fit the caller's deadline
//! 6. score — argmin of expected latency, minus the locality bonus when the
//!    request key hits the lane's Bloom set

use ovrt_core::DISPATCH_AFFINITY_BONUS_NS;
use ovrt_core::DISPATCH_EWMA_ALPHA_DEN;
use ovrt_core::DISPATCH_EWMA_ALPHA_NUM;
use ovrt_core::DISPATCH_STALE_TICKS;

/// Lane count is fixed by the schema so offsets never move under a running
/// deployment.
pub const MAX_LANES: usize = ovrt_core::DISPATCH_MAX_LANES as usize;

/// Immutable membership row for one lane, decoded from a published buffer.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct LaneDescriptor {
    pub lane_id: u16,
    pub jurisdiction: u16,
    pub max_concurrency: u32,
    pub generation: u32,
    pub unit_class_mask: u64,
    pub affinity_bloom: u64,
}

impl LaneDescriptor {
    pub fn covers(&self, required_class_mask: u64) -> bool {
        self.unit_class_mask & required_class_mask == required_class_mask
    }

    /// A request may run on an exactly matching jurisdiction or on a lane
    /// declared global. Mismatched pairs are rejected before scoring.
    pub fn allows_jurisdiction(&self, request: u16) -> bool {
        let global = ovrt_core::DISPATCH_JURISDICTION_GLOBAL as u16;
        self.jurisdiction == global || self.jurisdiction == request
    }

    pub fn holds_locality(&self, key: u64) -> bool {
        (self.affinity_bloom >> (key % 64)) & 1 == 1
    }
}

/// Live statistics for one lane, snapshotted by the caller.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct LaneStats {
    pub ewma_ns: u64,
    pub inflight: u32,
    pub max_concurrency: u32,
    pub last_tick_seen: u64,
}

/// What a caller needs from the dispatch table to place one unit of work.
#[derive(Clone, Copy, Debug)]
pub struct DispatchRequest {
    pub required_class_mask: u64,
    pub jurisdiction: u16,
    pub deadline_ns: u64,
    pub affinity_key: u64,
}

impl DispatchRequest {
    /// Expected latency for this request on this lane, before the locality
    /// bonus: measured mean inflated by current queue pressure.
    ///
    /// Returns `None` while the lane has no measurement; unsampled lanes must
    /// not look free.
    pub fn expected_latency_ns(
        &self,
        stats: &LaneStats,
        descriptor: &LaneDescriptor,
    ) -> Option<u64> {
        if stats.ewma_ns == 0 {
            return None;
        }
        let concurrency = match stats.max_concurrency.max(descriptor.max_concurrency) {
            0 => 1,
            value => value,
        };
        let factor = 1 + u64::from(stats.inflight) / u64::from(concurrency);
        Some(stats.ewma_ns.saturating_mul(factor))
    }

    fn is_fresh(&self, tick_now: u64, stats: &LaneStats) -> bool {
        // Heartbeat zero means the owner never checked in, which is stale by
        // definition even though wrapping arithmetic would call it fresh.
        stats.last_tick_seen != 0
            && tick_now.wrapping_sub(stats.last_tick_seen) <= u64::from(DISPATCH_STALE_TICKS)
    }
}

/// Applies the fixed EWMA blend to one sample: `α·sample + (1-α)·previous`
/// with α = 1/8 from the generated constants. Wide intermediate math keeps
/// the multiply overflow-free at any u64 inputs.
pub fn blend_ewma(previous_ns: u64, sample_ns: u64) -> u64 {
    let numerator = u128::from(DISPATCH_EWMA_ALPHA_NUM) * u128::from(sample_ns)
        + u128::from(DISPATCH_EWMA_ALPHA_DEN - DISPATCH_EWMA_ALPHA_NUM) * u128::from(previous_ns);
    (numerator / u128::from(DISPATCH_EWMA_ALPHA_DEN)) as u64
}

/// Selects the fastest eligible lane, or `None` when nothing qualifies and
/// the caller should fall back to its static path.
///
/// Ties keep the lower lane id, which keeps the choice stable across equal
/// lanes instead of flickering between them.
pub fn decide(
    tick_now: u64,
    descriptors: &[LaneDescriptor],
    stats: &[Option<LaneStats>],
    request: &DispatchRequest,
) -> Option<u16> {
    let mut best: Option<(u64, u16)> = None;
    for index in 0..descriptors.len().min(MAX_LANES).min(stats.len()) {
        let descriptor = &descriptors[index];
        let Some(lane_stats) = stats[index] else {
            continue;
        };
        // Retired or unpublished slots carry an empty class mask and must
        // never serve traffic, even when the request constrains nothing.
        if descriptor.unit_class_mask == 0 {
            continue;
        }
        if !descriptor.covers(request.required_class_mask) {
            continue;
        }
        if !descriptor.allows_jurisdiction(request.jurisdiction) {
            continue;
        }
        if !request.is_fresh(tick_now, &lane_stats) {
            continue;
        }
        let Some(mut score) = request.expected_latency_ns(&lane_stats, descriptor) else {
            continue;
        };
        if descriptor.holds_locality(request.affinity_key) {
            score = score.saturating_sub(u64::from(DISPATCH_AFFINITY_BONUS_NS));
        }
        if score > request.deadline_ns {
            continue;
        }
        let lane_id = descriptor.lane_id;
        if best.is_none_or(|(best_score, _)| score < best_score) {
            best = Some((score, lane_id));
        }
    }
    best.map(|(_, lane_id)| lane_id)
}

#[cfg(test)]
mod tests {
    use super::*;

    const TICK: u64 = 100;

    fn lane(id: u16) -> LaneDescriptor {
        LaneDescriptor {
            lane_id: id,
            jurisdiction: 0,
            max_concurrency: 4,
            generation: 1,
            unit_class_mask: 0b11,
            affinity_bloom: 0,
        }
    }

    fn stats(ewma_ns: u64) -> Option<LaneStats> {
        Some(LaneStats { ewma_ns, inflight: 0, max_concurrency: 0, last_tick_seen: TICK })
    }

    fn request(deadline_ns: u64) -> DispatchRequest {
        DispatchRequest { required_class_mask: 0b01, jurisdiction: 7, deadline_ns, affinity_key: 3 }
    }

    #[test]
    fn picks_the_only_lane_covering_the_request() {
        let descriptors = vec![lane(1), lane(2)];
        let table = vec![stats(5_000), stats(9_000)];
        assert_eq!(decide(TICK, &descriptors, &table, &request(1_000_000)), Some(1));
    }

    #[test]
    fn queue_pressure_flips_the_choice() {
        let mut busy = stats(5_000).expect("stats");
        busy.inflight = 8; // 2x overload on max 4 → expected 15_000.
        let descriptors = vec![lane(1), lane(2)];
        let table = vec![Some(busy), stats(10_000)];
        assert_eq!(decide(TICK, &descriptors, &table, &request(1_000_000)), Some(2));
    }

    #[test]
    fn mismatched_jurisdiction_is_never_served() {
        let mut restricted = lane(1);
        restricted.jurisdiction = 9;
        let descriptors = vec![restricted];
        let table = vec![stats(1_000)];
        assert_eq!(decide(TICK, &descriptors, &table, &request(1_000_000)), None);
    }

    #[test]
    fn global_lanes_serve_any_jurisdiction() {
        let descriptors = vec![lane(1)]; // jurisdiction 0 = global.
        let table = vec![stats(1_000)];
        assert_eq!(decide(TICK, &descriptors, &table, &request(1_000_000)), Some(1));
    }

    #[test]
    fn missing_required_classes_select_nothing() {
        let mut narrow = lane(1);
        narrow.unit_class_mask = 0b100;
        let descriptors = vec![narrow];
        let table = vec![stats(1_000)];
        assert_eq!(decide(TICK, &descriptors, &table, &request(1_000_000)), None);
    }

    #[test]
    fn stale_heartbeats_are_excluded() {
        let mut old = stats(1_000).expect("stats");
        old.last_tick_seen = TICK - u64::from(DISPATCH_STALE_TICKS) - 1;
        let descriptors = vec![lane(1)];
        assert_eq!(decide(TICK, &descriptors, &[Some(old)], &request(1_000_000)), None);
    }

    #[test]
    fn deadline_feasibility_gates_selection() {
        let descriptors = vec![lane(1)];
        assert_eq!(decide(TICK, &descriptors, &[stats(2_000)], &request(999)), None);
    }

    #[test]
    fn unsampled_lanes_do_not_look_free() {
        let descriptors = vec![lane(1)];
        assert_eq!(decide(TICK, &descriptors, &[stats(0)], &request(1_000_000)), None);
    }

    #[test]
    fn locality_bonus_breaks_near_ties() {
        let mut local = lane(1);
        // Request key 3 hits bit 3, matching the modulo in holds_locality.
        local.affinity_bloom = 1 << 3;
        let mut remote = lane(2);
        remote.lane_id = 2;
        let descriptors = vec![local, remote];
        // Remote raw mean is smaller, but only by less than the bonus.
        let table = vec![stats(6_000), stats(5_500)];
        assert_eq!(decide(TICK, &descriptors, &table, &request(1_000_000)), Some(1));
    }

    #[test]
    fn retired_slots_never_serve_unconstrained_requests() {
        let mut retired = lane(1);
        retired.unit_class_mask = 0;
        let descriptors = vec![retired];
        let table = vec![stats(1_000)];
        let unconstrained = DispatchRequest {
            required_class_mask: 0,
            jurisdiction: 7,
            deadline_ns: u64::MAX,
            affinity_key: 0,
        };
        assert_eq!(decide(TICK, &descriptors, &table, &unconstrained), None);
    }

    #[test]
    fn ewma_blend_matches_the_fixed_alpha() {
        // (1·16_000 + 7·8_000) / 8 = 9_000.
        assert_eq!(blend_ewma(8_000, 16_000), 9_000);
        assert_eq!(blend_ewma(0, 12_345), 12_345 / 8);
        // Saturation-safe at extreme inputs via wide intermediates.
        assert_eq!(blend_ewma(u64::MAX, u64::MAX), u64::MAX);
    }

    #[test]
    fn ties_keep_the_lower_lane_id() {
        let mut second = lane(2);
        second.unit_class_mask = 0b01;
        let descriptors = vec![lane(1), second];
        let table = vec![stats(4_000), stats(4_000)];
        assert_eq!(decide(TICK, &descriptors, &table, &request(1_000_000)), Some(1));
    }
}
