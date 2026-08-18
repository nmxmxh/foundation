//! Bounded duplicate suppression for raced frames.
//!
//! Racing a frame down two paths means the receiver gets it twice whenever both
//! paths work, which is most of the time. Something has to drop the second copy,
//! and that something is here.
//!
//! The key is the idempotency key already carried in `foundation.v1.Metadata`
//! and already required to survive lane changes by the `MetadataPreserved`
//! invariant. Nothing new goes on the wire to make racing possible.
//!
//! # The failure direction is chosen, not incidental
//!
//! This is a **bounded window**, not a set. It holds a fixed number of slots and
//! forgets the oldest keys under pressure, because an unbounded set of every key
//! ever seen is a memory leak with a formal name.
//!
//! Forgetting means the ring can occasionally admit a duplicate. It can never
//! reject a first arrival. That asymmetry is deliberate and it is the whole
//! safety argument:
//!
//! - A false *duplicate* would drop a frame that was never delivered. Silent
//!   data loss, indistinguishable from a network failure.
//! - A false *first-seen* delivers a frame twice. The idempotency key is still
//!   attached, so the durable path deduplicates it exactly as it deduplicates a
//!   client retry — a case the system already handles.
//!
//! So the ring is a cheap filter in front of a correct one, never a substitute
//! for it. `TestRingNeverRejectsAFirstArrival` holds that line.

use std::sync::atomic::{AtomicU64, Ordering};

use crate::NetworkError;

/// How many slots a single key is willing to examine before giving up and
/// evicting. Bounded so `observe` is constant-time; four is enough that a
/// half-full ring almost always finds a free slot, and small enough that a full
/// ring gives up quickly rather than scanning.
const PROBE_LIMIT: usize = 4;

/// Reserved to mean "this slot is empty", so a real key must never hash to it.
/// `fingerprint` folds zero to one; the cost is that two keys in 2^64 collide
/// slightly more often than uniform, which is not a cost.
const EMPTY: u64 = 0;

/// What the ring knows about a key it has just been shown.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Observation {
    /// This key has not been seen in the current window. Process the frame.
    FirstSeen,
    /// This key is already in the window. Drop the frame.
    Duplicate,
}

/// A fixed-capacity, lock-free set of recently seen idempotency keys.
///
/// Shared by reference across threads; `observe` takes `&self` because the
/// receive path for two raced copies is two threads arriving at once, and a
/// design needing `&mut self` would put a lock exactly where the contention is.
#[derive(Debug)]
pub struct DedupRing {
    slots: Box<[AtomicU64]>,
    mask: usize,
}

impl DedupRing {
    /// Creates a ring holding `capacity` keys.
    ///
    /// # Errors
    ///
    /// [`NetworkError::InvalidCapacity`] unless `capacity` is a power of two and
    /// at least [`PROBE_LIMIT`]. The power of two lets the slot index be a mask
    /// rather than a remainder, which keeps a division off the receive path; the
    /// floor keeps the probe sequence from wrapping onto itself and reporting a
    /// key as its own duplicate.
    pub fn with_capacity(capacity: usize) -> Result<Self, NetworkError> {
        if capacity < PROBE_LIMIT || !capacity.is_power_of_two() {
            return Err(NetworkError::InvalidCapacity { requested: capacity });
        }
        let mut slots = Vec::with_capacity(capacity);
        slots.resize_with(capacity, || AtomicU64::new(EMPTY));
        Ok(Self { slots: slots.into_boxed_slice(), mask: capacity - 1 })
    }

    /// The number of slots in the window.
    pub fn capacity(&self) -> usize {
        self.slots.len()
    }

    /// Records `key` and reports whether it had already been seen.
    ///
    /// Constant time: at most [`PROBE_LIMIT`] slots are examined regardless of
    /// how full the ring is.
    pub fn observe(&self, key: &[u8]) -> Observation {
        let fingerprint = fingerprint(key);
        let home = (fingerprint as usize) & self.mask;

        for step in 0..PROBE_LIMIT {
            let slot = &self.slots[(home + step) & self.mask];

            // Acquire, not Relaxed: a caller that reads FirstSeen goes on to
            // process the frame, and the caller that lost the race must observe
            // everything the winner published before claiming the slot.
            if slot.load(Ordering::Acquire) == fingerprint {
                return Observation::Duplicate;
            }

            match slot.compare_exchange(EMPTY, fingerprint, Ordering::AcqRel, Ordering::Acquire) {
                // Claimed an empty slot: this thread is demonstrably first.
                Ok(_) => return Observation::FirstSeen,
                // Lost the slot to another thread between the load and the swap.
                // If it was the other copy of *this* frame, that thread is the
                // first-seen and this one is the duplicate.
                Err(observed) if observed == fingerprint => return Observation::Duplicate,
                // Lost it to an unrelated key; keep probing.
                Err(_) => continue,
            }
        }

        // Every candidate slot is occupied by other keys. Evict the home slot
        // rather than growing or rejecting.
        //
        // This is the branch that makes the ring bounded, and the branch that
        // can admit a duplicate: the evicted key's second copy, if it arrives
        // later, will read as first-seen. Returning Duplicate here instead would
        // discard a frame that may never have been delivered at all — the one
        // outcome this type must never produce.
        self.slots[home].store(fingerprint, Ordering::Release);
        Observation::FirstSeen
    }

    /// Empties the window.
    ///
    /// For session boundaries — a reconnect starts a new key space, and carrying
    /// the previous session's keys forward only risks suppressing a live frame.
    pub fn clear(&self) {
        for slot in self.slots.iter() {
            slot.store(EMPTY, Ordering::Release);
        }
    }
}

/// Folds a key into the 64-bit value stored in a slot.
///
/// This is FNV-1a, and it is called FNV-1a. It is not a cryptographic hash and
/// nothing here pretends otherwise: an attacker who can choose idempotency keys
/// can collide them and suppress a frame. That is acceptable *here* only because
/// the ring is an optimisation in front of the durable idempotency check, never
/// an authorisation boundary — see the module documentation on which direction
/// this type is allowed to be wrong in.
///
/// The naming is deliberate. The post-mortem of the deleted swarm branch records
/// a function called `generateFastProof` that announced "HMAC-SHA256-FAST" and
/// computed FNV-1a. A hash that lies about its strength is worse than a weak
/// hash, because the lie is what gets designed against.
fn fingerprint(key: &[u8]) -> u64 {
    const OFFSET_BASIS: u64 = 0xcbf2_9ce4_8422_2325;
    const PRIME: u64 = 0x1000_0000_01b3;

    let mut hash = OFFSET_BASIS;
    for byte in key {
        hash ^= u64::from(*byte);
        hash = hash.wrapping_mul(PRIME);
    }
    // EMPTY is reserved for "no key here", so no real key may take that value.
    if hash == EMPTY {
        1
    } else {
        hash
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Arc;

    #[test]
    fn a_key_is_new_once_and_a_duplicate_after() {
        let ring = DedupRing::with_capacity(64).expect("64 is a valid capacity");

        assert_eq!(ring.observe(b"corr-1"), Observation::FirstSeen);
        assert_eq!(ring.observe(b"corr-1"), Observation::Duplicate);
        assert_eq!(ring.observe(b"corr-1"), Observation::Duplicate);
    }

    #[test]
    fn distinct_keys_do_not_suppress_each_other() {
        let ring = DedupRing::with_capacity(1024).expect("1024 is a valid capacity");

        for i in 0..256 {
            let key = format!("corr-{i}");
            assert_eq!(
                ring.observe(key.as_bytes()),
                Observation::FirstSeen,
                "key {i} was suppressed by an unrelated key"
            );
        }
    }

    #[test]
    fn capacity_must_be_a_power_of_two_and_leave_room_to_probe() {
        assert!(DedupRing::with_capacity(0).is_err());
        assert!(DedupRing::with_capacity(1).is_err());
        assert!(DedupRing::with_capacity(3).is_err());
        assert!(DedupRing::with_capacity(100).is_err());
        assert!(DedupRing::with_capacity(4).is_ok());
        assert!(DedupRing::with_capacity(65_536).is_ok());
    }

    #[test]
    fn the_ring_never_rejects_a_first_arrival() {
        // The safety property. Every key here is distinct, and the ring is far
        // too small to hold them all, so eviction is running constantly. Not one
        // of them may be reported as a duplicate: that would be a dropped frame
        // that was never delivered.
        let ring = DedupRing::with_capacity(16).expect("16 is a valid capacity");

        for i in 0..10_000 {
            let key = format!("unique-{i}");
            assert_eq!(
                ring.observe(key.as_bytes()),
                Observation::FirstSeen,
                "a never-before-seen key {i} was rejected under eviction pressure"
            );
        }
    }

    #[test]
    fn an_immediate_second_copy_is_suppressed_even_under_pressure() {
        // The useful property, stated as the realistic case: a raced frame's two
        // copies arrive close together, so the second copy is caught even when
        // the ring is small and busy. This is the same shape as the safety test
        // above but asserts the opposite side of the trade.
        let ring = DedupRing::with_capacity(16).expect("16 is a valid capacity");

        for i in 0..1_000 {
            let key = format!("raced-{i}");
            assert_eq!(ring.observe(key.as_bytes()), Observation::FirstSeen);
            assert_eq!(
                ring.observe(key.as_bytes()),
                Observation::Duplicate,
                "the second copy of frame {i} was not suppressed"
            );
        }
    }

    #[test]
    fn clearing_the_window_readmits_previously_seen_keys() {
        let ring = DedupRing::with_capacity(64).expect("64 is a valid capacity");

        assert_eq!(ring.observe(b"session-a"), Observation::FirstSeen);
        assert_eq!(ring.observe(b"session-a"), Observation::Duplicate);

        ring.clear();

        assert_eq!(
            ring.observe(b"session-a"),
            Observation::FirstSeen,
            "a cleared window still suppressed a key"
        );
    }

    #[test]
    fn exactly_one_thread_wins_a_contended_key() {
        // Two raced copies arriving simultaneously is the case the whole type
        // exists for, and the case a lock-free design can most easily get wrong:
        // both threads load EMPTY, both claim first-seen, and the frame is
        // processed twice. Exactly one CAS may succeed.
        for attempt in 0..200 {
            let ring = Arc::new(DedupRing::with_capacity(64).expect("64 is a valid capacity"));
            let key = format!("contended-{attempt}");

            let mut handles = Vec::new();
            for _ in 0..8 {
                let ring = Arc::clone(&ring);
                let key = key.clone();
                handles.push(std::thread::spawn(move || ring.observe(key.as_bytes())));
            }

            let firsts = handles
                .into_iter()
                .map(|handle| handle.join().expect("dedup thread should not panic"))
                .filter(|observation| *observation == Observation::FirstSeen)
                .count();

            assert_eq!(firsts, 1, "attempt {attempt}: {firsts} threads claimed first-seen");
        }
    }

    #[test]
    fn the_fingerprint_never_collides_with_the_empty_marker() {
        // A key hashing to EMPTY would be stored as "this slot is free" and
        // would be re-admitted forever, silently disabling deduplication for it.
        for i in 0..100_000u32 {
            assert_ne!(fingerprint(&i.to_le_bytes()), EMPTY);
        }
        assert_ne!(fingerprint(b""), EMPTY);
    }
}
