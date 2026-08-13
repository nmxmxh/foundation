//! Waiting on a cross-process epoch, and the ordering that makes it mean
//! something.
//!
//! An epoch slot is a `u32` in a `MAP_SHARED` region that both processes hold.
//! One side publishes by incrementing it; the other observes the change and
//! reads the region the increment refers to. The increment is the crossing —
//! there is no message, no syscall, and nothing copied.
//!
//! Two properties make that safe, and neither is optional:
//!
//! **Release on publish, acquire on observe.** The payload is written before
//! the epoch and read after it. Without the pairing, a compiler or a CPU may
//! reorder either half, and the reader sees a descriptor that is addressable
//! but not yet filled. This is the failure the pipe doorbell could not have —
//! a pipe read is a full barrier — and it is the one this design can, which is
//! why [`observe`] and [`publish`] exist rather than callers touching the slot.
//!
//! **Change, not equality.** A waiter compares against the value it saw before
//! it asked, never against an expected value. Epochs only increase, and a
//! consumer that missed one exchange must not wait forever for a number that
//! has already gone past.
//!
//! # Why there is no futex here
//!
//! Parking is a spin, then a yield, then a capped sleep ladder. No `futex`, no
//! `__ulock_wait`.
//!
//! The reason is that parking only happens on the slow path. When the peer
//! answers in the time a crossing is supposed to take, the spin wins and the
//! waiter never sleeps at all — that is the case the whole design exists for,
//! and a futex would not make it faster. When the peer takes long enough to
//! exhaust the spin, it is doing real work, and the difference between a futex
//! wake and a 100us sleep granularity is noise against it.
//!
//! What that buys: `futex` is Linux-only, and macOS's equivalent
//! (`__ulock_wait`/`__ulock_wake`) is private API. Avoiding both keeps one
//! implementation, identically tested, on every platform. If a workload ever
//! shows up that parks often *and* cares about wake latency, a futex belongs
//! behind this same interface — not spread through the callers.

use std::sync::atomic::{fence, AtomicU32, Ordering};
use std::time::{Duration, Instant};

/// How long a waiter spins before it starts sleeping.
#[derive(Debug, Clone, Copy)]
pub struct WaitPolicy {
    /// Iterations of the spin phase before the first sleep.
    ///
    /// Tunable per pool because call rates differ by orders of magnitude: a
    /// pool answering thousands of exchanges a second wants to spin through
    /// them, and one answering a few an hour should not hold a core to do it.
    pub spin_iterations: u32,
    /// Longest single sleep. The ladder doubles up to this and stays there.
    pub max_sleep: Duration,
    /// Total time before the wait gives up.
    pub timeout: Duration,
}

impl Default for WaitPolicy {
    fn default() -> Self {
        Self {
            // Roughly the cost of a same-core handoff. Long enough that a
            // responsive peer is never slept on, short enough that an
            // unresponsive one does not hold a core for meaningful time.
            spin_iterations: 2_000,
            max_sleep: Duration::from_micros(200),
            timeout: Duration::from_secs(30),
        }
    }
}

/// Why a wait ended without observing a change.
#[derive(Debug, PartialEq, Eq)]
pub enum WaitError {
    /// The policy's timeout elapsed.
    TimedOut,
    /// A caller-supplied predicate reported the peer is gone.
    ///
    /// Exists because a dead peer and a slow one are indistinguishable from the
    /// slot alone — the whole liveness problem the pipe used to solve by
    /// reaching EOF. The waiter cannot detect it, so the caller supplies the
    /// check and this reports it.
    PeerLost,
}

/// Publishes a new epoch, making every prior write to the region visible.
///
/// The release store is the publication. Everything the payload consists of
/// must already be written when this is called; nothing may be written after it
/// and before the peer observes it.
pub fn publish(slot: &AtomicU32, value: u32) {
    slot.store(value, Ordering::Release);
}

/// Increments and publishes, returning the new value.
pub fn publish_next(slot: &AtomicU32) -> u32 {
    // wrapping, not saturating: an epoch is a change marker, not a count, and a
    // saturated counter would stop signalling entirely after 4 billion
    // exchanges rather than wrapping harmlessly past zero.
    let next = slot.load(Ordering::Relaxed).wrapping_add(1);
    publish(slot, next);
    next
}

/// Reads an epoch with the ordering that makes the region it refers to safe.
///
/// Callers must use this rather than a relaxed load before touching the payload
/// a slot describes.
pub fn observe(slot: &AtomicU32) -> u32 {
    slot.load(Ordering::Acquire)
}

/// Waits until `slot` differs from `previous`, or the policy runs out.
///
/// `peer_alive` is consulted between sleeps. It is not consulted during the
/// spin phase, where the answer cannot have changed meaningfully and the check
/// would cost more than the spin.
pub fn wait_for_change(
    slot: &AtomicU32,
    previous: u32,
    policy: &WaitPolicy,
    mut peer_alive: impl FnMut() -> bool,
) -> Result<u32, WaitError> {
    for _ in 0..policy.spin_iterations {
        let current = observe(slot);
        if current != previous {
            return Ok(current);
        }
        std::hint::spin_loop();
    }

    let deadline = Instant::now() + policy.timeout;
    let mut sleep = Duration::from_micros(1);
    loop {
        let current = observe(slot);
        if current != previous {
            return Ok(current);
        }
        if !peer_alive() {
            // Checked after the load, never before: a peer that published and
            // then exited has produced a valid result, and reporting it as lost
            // would discard work that completed.
            return Err(WaitError::PeerLost);
        }
        if Instant::now() >= deadline {
            return Err(WaitError::TimedOut);
        }
        std::thread::sleep(sleep);
        sleep = (sleep * 2).min(policy.max_sleep);
    }
}

/// A full barrier, for the rare caller that needs one outside publish/observe.
pub fn full_fence() {
    fence(Ordering::SeqCst);
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Arc;

    #[test]
    fn a_waiter_returns_as_soon_as_the_epoch_moves() {
        let slot = Arc::new(AtomicU32::new(7));
        let writer = Arc::clone(&slot);
        let handle = std::thread::spawn(move || {
            std::thread::sleep(Duration::from_millis(5));
            publish_next(&writer);
        });

        let observed = wait_for_change(&slot, 7, &WaitPolicy::default(), || true);
        handle.join().unwrap_or(());
        assert_eq!(observed, Ok(8));
    }

    #[test]
    fn a_waiter_that_already_missed_the_change_does_not_block() {
        // The reason waits compare against what the caller last saw rather than
        // an expected value: this one is already past.
        let slot = AtomicU32::new(9);
        let policy = WaitPolicy { timeout: Duration::from_millis(50), ..WaitPolicy::default() };
        assert_eq!(wait_for_change(&slot, 4, &policy, || true), Ok(9));
    }

    #[test]
    fn a_wait_gives_up_rather_than_hanging() {
        let slot = AtomicU32::new(1);
        let policy = WaitPolicy {
            spin_iterations: 8,
            max_sleep: Duration::from_micros(50),
            timeout: Duration::from_millis(20),
        };
        assert_eq!(wait_for_change(&slot, 1, &policy, || true), Err(WaitError::TimedOut));
    }

    #[test]
    fn a_lost_peer_ends_the_wait_before_the_timeout() {
        // The liveness hole the pipe used to cover: without this the caller
        // waits the full timeout on a kernel that is already gone.
        let slot = AtomicU32::new(1);
        let policy = WaitPolicy {
            spin_iterations: 8,
            max_sleep: Duration::from_micros(50),
            timeout: Duration::from_secs(30),
        };
        let started = Instant::now();
        assert_eq!(wait_for_change(&slot, 1, &policy, || false), Err(WaitError::PeerLost));
        assert!(started.elapsed() < Duration::from_secs(1), "wait did not end early");
    }

    #[test]
    fn a_result_published_before_the_peer_died_is_still_returned() {
        let slot = AtomicU32::new(1);
        publish_next(&slot);
        let policy = WaitPolicy { spin_iterations: 0, ..WaitPolicy::default() };
        assert_eq!(wait_for_change(&slot, 1, &policy, || false), Ok(2));
    }

    #[test]
    fn epochs_wrap_rather_than_stopping() {
        let slot = AtomicU32::new(u32::MAX);
        assert_eq!(publish_next(&slot), 0);
        assert_ne!(0, u32::MAX, "a wrapped epoch still differs from the previous one");
    }
}
