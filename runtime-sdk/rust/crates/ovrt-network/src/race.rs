//! Hedged sending: put the same small frame on every available path and take
//! whichever arrives first.
//!
//! # What racing actually buys
//!
//! Not median latency. The median barely moves, and a benchmark that reports
//! only the mean will make this look like a rounding error.
//!
//! What it buys is the tail. Receiving `min(L₁, L₂)` over independent paths
//! means a stall has to happen on *both* to be observed at all, so an event with
//! probability p on each path becomes p² end to end. A path that hiccups one
//! time in a hundred, raced against another like it, hiccups one time in ten
//! thousand. `the_race_collapses_the_tail` measures exactly that and asserts on
//! p99, because the tail is the claim.
//!
//! # Why only small frames
//!
//! A race sends the frame twice, so it costs double the bytes. That is a fine
//! trade for a control frame and a bad one for a tensor, and the boundary is not
//! a new invention: `runtime_foundation.md` already partitions payloads at 4 KB
//! for the control buffer, 4 KB–1 MB for the arena, and above that for streamed
//! chunks with backpressure. Those boundaries were drawn for copy budgets and
//! they happen to be the same boundaries duplication cost draws.
//!
//! So this module enforces the first rung and refuses the rest. Anything over
//! [`MAX_RACED_FRAME_BYTES`] is rejected rather than quietly raced, because a
//! transport that doubles a 4 MB send without saying so is a bandwidth bill, not
//! a feature. Striping with erasure repair is the answer above the line; it is
//! not in this crate yet, and this refusal is what keeps that gap visible.
//!
//! # Threading
//!
//! One long-lived worker per path, not a thread per send. A raced control frame
//! is trying to save single-digit milliseconds and spawning two OS threads costs
//! a meaningful fraction of that, which would make the mechanism its own
//! overhead. Workers are created with the [`Racer`] and shut down with it.

use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::mpsc::{self, Receiver, RecvTimeoutError, Sender};
use std::sync::{Arc, Mutex};
use std::thread::JoinHandle;
use std::time::{Duration, Instant};

use crate::NetworkError;

/// The largest frame this module will race, matching the 4 KB control-buffer
/// rung of the runtime payload ladder.
pub const MAX_RACED_FRAME_BYTES: usize = 4096;

/// One physical route a frame can take.
///
/// Implementors own their socket and its interface binding; the racer knows only
/// that a path has a label and can be asked to deliver bytes. That boundary is
/// what lets the tail-latency claim be tested against simulated paths with known
/// distributions rather than against a live network.
pub trait Path: Send + Sync + 'static {
    /// A stable name for diagnostics, usually the bound interface.
    fn label(&self) -> &str;

    /// Delivers `frame`, returning once it is on the wire and acknowledged, or
    /// reporting why it could not be.
    ///
    /// Called from this path's own worker thread. A slow implementation delays
    /// only its own path, which is the entire point — but it must eventually
    /// return, because a path that blocks forever holds its worker forever.
    fn send(&self, frame: &[u8]) -> Result<(), String>;
}

/// What one path did with one frame.
#[derive(Debug, Clone)]
pub struct Attempt {
    /// The path's label.
    ///
    /// `Arc<str>` rather than `String` because this is cloned on every report
    /// and again for the winner. The label is fixed at construction and never
    /// mutated, so the only thing a `String` bought was one heap allocation per
    /// attempt on the hot path.
    pub label: Arc<str>,
    /// How long the send took, whether or not it succeeded.
    pub elapsed: Duration,
    /// `None` on success; the path's reason on failure.
    pub error: Option<String>,
    /// Which race this attempt belongs to.
    ///
    /// A losing path keeps sending after its race concluded, so its report can
    /// arrive during a later race. Without this the next race would accept a
    /// stale success as its own — reporting a frame delivered that it never
    /// dispatched.
    pub seq: u64,
}

impl Attempt {
    /// Whether this path delivered the frame.
    pub fn succeeded(&self) -> bool {
        self.error.is_none()
    }
}

/// The result of racing one frame.
#[derive(Debug, Clone)]
pub struct RaceOutcome {
    /// The label of the path that delivered first, if any did.
    pub winner: Option<Arc<str>>,
    /// Time from dispatch until the first success, or until the race gave up.
    pub elapsed: Duration,
    /// How many paths the frame was offered to.
    pub dispatched: usize,
    /// Every attempt that had reported by the time the race concluded. Losing
    /// paths are not waited for, so this is usually shorter than `dispatched` —
    /// that truncation is the latency win, visible in the data.
    pub attempts: Vec<Attempt>,
}

impl RaceOutcome {
    /// Whether any path delivered the frame.
    pub fn delivered(&self) -> bool {
        self.winner.is_some()
    }
}

struct Job {
    frame: Arc<[u8]>,
    seq: u64,
}

struct Worker {
    label: Arc<str>,
    jobs: Sender<Job>,
    /// Held while this path is mid-send, so the racer can skip it.
    ///
    /// Without this the job channel is a queue, and a queue is the wrong
    /// structure here — see [`Racer::race`] for why a busy path is skipped
    /// rather than enqueued.
    busy: Arc<AtomicBool>,
    handle: Option<JoinHandle<()>>,
}

/// Sends frames down every path at once and returns the first delivery.
///
/// Holds one worker thread per path for its lifetime. Dropping it closes the job
/// channels, which is how the workers learn to exit; the drop joins them, so no
/// thread outlives the racer that owns it.
pub struct Racer {
    workers: Vec<Worker>,
    /// The receiving half of the single results channel, shared by every race
    /// and held under a lock for the duration of one.
    ///
    /// One channel for the racer's lifetime rather than one per race. A fresh
    /// `mpsc` channel measures at 2 allocations and ~1 KB, which was the largest
    /// single item in the per-race allocation budget — paid on every control
    /// frame, for a channel that lived microseconds.
    ///
    /// Reuse is only correct because [`Attempt::seq`] exists: a losing path
    /// reports into this channel after its race concluded, and the next race
    /// must be able to tell that report from its own.
    ///
    /// Only the workers hold senders. That is deliberate: when the last worker
    /// exits, the channel disconnects and a waiting race learns immediately
    /// rather than sitting out its deadline. A sender parked in this struct
    /// would keep the channel open forever and turn a dead racer into a slow
    /// one.
    ///
    /// The lock spans dispatch *and* collection, not just collection, and that
    /// is the correctness argument rather than a performance choice. If two
    /// races could dispatch concurrently and then contend for the receiver, one
    /// would consume the other's attempts and discard them as foreign — losing
    /// them permanently, because a consumed message is not re-queued. Serialising
    /// the whole operation makes every attempt in the channel belong to either
    /// the current race or a concluded one.
    ///
    /// Contention is not a concern: paths are single-occupancy, so a second
    /// concurrent race would find every path busy and be rejected anyway.
    inbox: Mutex<Receiver<Attempt>>,
    seq: AtomicU64,
    raced: AtomicU64,
    won: AtomicU64,
}

impl Racer {
    /// Builds a racer over `paths`, starting one worker per path.
    ///
    /// # Errors
    ///
    /// [`NetworkError::NoPaths`] when `paths` is empty. A race needs something
    /// to race; an empty set would silently become a black hole that reports
    /// every frame as undeliverable.
    pub fn new(paths: Vec<Arc<dyn Path>>) -> Result<Self, NetworkError> {
        if paths.is_empty() {
            return Err(NetworkError::NoPaths);
        }

        let (results, inbox) = mpsc::channel::<Attempt>();

        let mut workers = Vec::with_capacity(paths.len());
        for path in paths {
            let label: Arc<str> = Arc::from(path.label());
            let (jobs, job_inbox) = mpsc::channel::<Job>();
            let busy = Arc::new(AtomicBool::new(false));
            let worker_busy = Arc::clone(&busy);
            let worker_label = Arc::clone(&label);
            let worker_results = results.clone();
            let handle = std::thread::Builder::new()
                .name(format!("ovrt-race-{label}"))
                .spawn(move || {
                    run_worker(&path, &job_inbox, &worker_results, &worker_label, &worker_busy);
                })
                .map_err(|error| NetworkError::WorkerSpawn {
                    label: label.to_string(),
                    reason: error.to_string(),
                })?;

            workers.push(Worker { label, jobs, busy, handle: Some(handle) });
        }

        // Dropped so the workers hold the only senders — see the `inbox` field.
        drop(results);

        Ok(Self {
            workers,
            inbox: Mutex::new(inbox),
            seq: AtomicU64::new(0),
            raced: AtomicU64::new(0),
            won: AtomicU64::new(0),
        })
    }

    /// The paths this racer will use, in dispatch order.
    pub fn labels(&self) -> Vec<&str> {
        self.workers.iter().map(|worker| worker.label.as_ref()).collect()
    }

    /// Races `frame` down every path and returns as soon as one delivers it.
    ///
    /// Returns after the first success, without waiting for the slower paths.
    /// Their sends continue in the background and their results are discarded —
    /// which is the design: cancelling a send this small costs more than letting
    /// it finish, and the receiver drops the duplicate with a
    /// [`DedupRing`](crate::DedupRing) lookup.
    ///
    /// # A busy path is skipped, not queued
    ///
    /// Each path takes at most one frame at a time. A path still working on the
    /// previous frame is left out of this race entirely.
    ///
    /// Queueing instead is the obvious design and it is wrong, in a way that
    /// hides: a stalled path accumulates a backlog, so every answer it produces
    /// belongs to a race that has already concluded. It stops contributing to
    /// any race it could win while still appearing to be a path, and the race
    /// silently degenerates to whichever path is fastest — which is the single
    /// path case, with the duplicate bandwidth still being paid.
    ///
    /// Skipping keeps it honest. `dispatched` reports how many paths actually
    /// received the frame, so a race that ran degraded says so in its outcome
    /// rather than in its latency distribution. It also keeps the work bounded:
    /// no queue means no unbounded growth when a path is slower than the frame
    /// arrival rate.
    ///
    /// # Errors
    ///
    /// [`NetworkError::FrameTooLarge`] above [`MAX_RACED_FRAME_BYTES`],
    /// [`NetworkError::AllPathsBusy`] when every path is mid-send, and
    /// [`NetworkError::PathsUnavailable`] when every worker has died.
    ///
    /// A `deadline` elapsing is *not* an error: it produces an outcome with no
    /// winner, carrying whatever the paths reported. Failing to deliver is a
    /// result the caller must handle either way, and making it an `Err` would
    /// discard the per-path diagnostics that say why.
    pub fn race(&self, frame: &[u8], deadline: Duration) -> Result<RaceOutcome, NetworkError> {
        if frame.len() > MAX_RACED_FRAME_BYTES {
            return Err(NetworkError::FrameTooLarge {
                bytes: frame.len(),
                limit: MAX_RACED_FRAME_BYTES,
            });
        }

        let started = Instant::now();
        let shared: Arc<[u8]> = Arc::from(frame);

        // Taken before dispatch, held until the race concludes. See the field's
        // documentation: this is what stops two races from stealing each other's
        // attempts out of the shared channel.
        let inbox = match self.inbox.lock() {
            Ok(inbox) => inbox,
            // A poisoned lock means a previous race panicked while collecting.
            // The channel itself is intact, so recovering is better than
            // refusing every subsequent frame for the life of the process.
            Err(poisoned) => poisoned.into_inner(),
        };
        let seq = self.seq.fetch_add(1, Ordering::Relaxed);

        let mut dispatched = 0;
        let mut busy = 0;
        let mut dead = 0;
        for worker in &self.workers {
            // Claim the path. Losing means it is mid-send, so it is not a path
            // for this frame.
            if worker
                .busy
                .compare_exchange(false, true, Ordering::AcqRel, Ordering::Acquire)
                .is_err()
            {
                busy += 1;
                continue;
            }

            let job = Job { frame: Arc::clone(&shared), seq };
            // A closed channel means that worker's thread is gone. The other
            // paths are still live, so this is a degraded race, not a failure —
            // but the claim has to be released or the path is busy forever.
            if worker.jobs.send(job).is_ok() {
                dispatched += 1;
            } else {
                worker.busy.store(false, Ordering::Release);
                dead += 1;
            }
        }

        if dispatched == 0 {
            // The two zero-dispatch cases are different problems and must not
            // collapse into one error: every path busy is backpressure and will
            // clear on its own, every worker dead is a broken racer that will
            // not.
            return Err(if dead > 0 && busy == 0 {
                NetworkError::PathsUnavailable
            } else {
                NetworkError::AllPathsBusy
            });
        }
        self.raced.fetch_add(1, Ordering::Relaxed);

        let outcome = collect_first_success(&inbox, seq, dispatched, started, deadline);
        if outcome.delivered() {
            self.won.fetch_add(1, Ordering::Relaxed);
        }
        Ok(outcome)
    }

    /// How many frames have been raced, and how many were delivered by at least
    /// one path. The difference is the undelivered count, which is the number an
    /// operator actually wants.
    pub fn counters(&self) -> (u64, u64) {
        (self.raced.load(Ordering::Relaxed), self.won.load(Ordering::Relaxed))
    }
}

impl Drop for Racer {
    fn drop(&mut self) {
        // Two phases, and the order matters. Every job channel must close before
        // any thread is joined: joining the first worker while the second still
        // holds an open channel would block until that second worker happened to
        // finish, turning shutdown into a wait on unrelated work.
        for worker in &mut self.workers {
            let (dead, _) = mpsc::channel::<Job>();
            let _ = std::mem::replace(&mut worker.jobs, dead);
        }
        for worker in &mut self.workers {
            if let Some(handle) = worker.handle.take() {
                let _ = handle.join();
            }
        }
    }
}

/// Waits for the first successful delivery, or for the deadline.
///
/// Returns early on the first success. Otherwise it keeps collecting until every
/// dispatched path has reported — a race where all paths fail must report all
/// their reasons, because "undeliverable" without the per-path cause is not
/// something an operator can act on.
fn collect_first_success(
    inbox: &Receiver<Attempt>,
    seq: u64,
    dispatched: usize,
    started: Instant,
    deadline: Duration,
) -> RaceOutcome {
    let mut attempts = Vec::with_capacity(dispatched);

    while attempts.len() < dispatched {
        let remaining = match deadline.checked_sub(started.elapsed()) {
            Some(remaining) if !remaining.is_zero() => remaining,
            _ => break,
        };

        match inbox.recv_timeout(remaining) {
            Ok(attempt) => {
                // A report from a concluded race. Its path lost, kept sending,
                // and finished after the fact — expected on roughly half of all
                // sends. Accepting it would let a previous frame's success be
                // reported as this frame's, so it is dropped on the floor and
                // the wait continues on the remaining budget.
                if attempt.seq != seq {
                    continue;
                }
                let winner = attempt.succeeded().then(|| Arc::clone(&attempt.label));
                attempts.push(attempt);
                if let Some(label) = winner {
                    return RaceOutcome {
                        winner: Some(label),
                        elapsed: started.elapsed(),
                        dispatched,
                        attempts,
                    };
                }
            }
            // Every worker dropped its sender: all paths have reported and none
            // of them won.
            Err(RecvTimeoutError::Disconnected) => break,
            Err(RecvTimeoutError::Timeout) => break,
        }
    }

    RaceOutcome { winner: None, elapsed: started.elapsed(), dispatched, attempts }
}

/// One path's worker loop: take a job, send it, report, release the claim,
/// repeat until the racer closes the channel.
///
/// A failed report is ignored on purpose. It means the race already concluded
/// and the receiver is gone — this path lost — which is the expected outcome for
/// roughly half of all sends and not something to log.
///
/// The claim is released *after* reporting, not before, so a path is never
/// offered a second frame while its first result is still in flight.
fn run_worker(
    path: &Arc<dyn Path>,
    inbox: &Receiver<Job>,
    results: &Sender<Attempt>,
    label: &Arc<str>,
    busy: &AtomicBool,
) {
    while let Ok(job) = inbox.recv() {
        let started = Instant::now();
        let error = path.send(&job.frame).err();
        let _ = results.send(Attempt {
            label: Arc::clone(label),
            elapsed: started.elapsed(),
            error,
            seq: job.seq,
        });
        busy.store(false, Ordering::Release);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicBool, AtomicUsize};

    /// Deterministic xorshift64*, so a tail measurement is reproducible. An RNG
    /// dependency for a latency simulation would be a dependency in the tree
    /// forever, for four lines of arithmetic.
    struct Rng(u64);

    impl Rng {
        fn next_u64(&mut self) -> u64 {
            let mut x = self.0;
            x ^= x >> 12;
            x ^= x << 25;
            x ^= x >> 27;
            self.0 = x;
            x.wrapping_mul(0x2545_f491_4f6c_dd1d)
        }

        /// Uniform in [0, 1).
        fn next_unit(&mut self) -> f64 {
            (self.next_u64() >> 11) as f64 / (1_u64 << 53) as f64
        }
    }

    /// A path with a known latency distribution: a base cost, plus an occasional
    /// stall at a fixed probability. Two of these with independent seeds are the
    /// whole experiment.
    struct SimulatedPath {
        label: String,
        base: Duration,
        stall: Duration,
        stall_probability: f64,
        rng: std::sync::Mutex<Rng>,
        sends: AtomicUsize,
    }

    impl SimulatedPath {
        fn new(
            label: &str,
            base_us: u64,
            stall_ms: u64,
            stall_probability: f64,
            seed: u64,
        ) -> Self {
            Self {
                label: label.to_string(),
                base: Duration::from_micros(base_us),
                stall: Duration::from_millis(stall_ms),
                stall_probability,
                rng: std::sync::Mutex::new(Rng(seed)),
                sends: AtomicUsize::new(0),
            }
        }

        fn draw(&self) -> Duration {
            let roll = match self.rng.lock() {
                Ok(mut rng) => rng.next_unit(),
                // A poisoned lock here means a previous send panicked; treat the
                // draw as a non-stall rather than propagating.
                Err(poisoned) => poisoned.into_inner().next_unit(),
            };
            if roll < self.stall_probability {
                self.base + self.stall
            } else {
                self.base
            }
        }
    }

    impl Path for SimulatedPath {
        fn label(&self) -> &str {
            &self.label
        }

        fn send(&self, _frame: &[u8]) -> Result<(), String> {
            self.sends.fetch_add(1, Ordering::Relaxed);
            std::thread::sleep(self.draw());
            Ok(())
        }
    }

    struct AlwaysFails {
        label: String,
    }

    impl Path for AlwaysFails {
        fn label(&self) -> &str {
            &self.label
        }

        fn send(&self, _frame: &[u8]) -> Result<(), String> {
            Err("simulated path is down".to_string())
        }
    }

    struct Instant2;

    impl Path for Instant2 {
        fn label(&self) -> &str {
            "instant"
        }

        fn send(&self, _frame: &[u8]) -> Result<(), String> {
            Ok(())
        }
    }

    fn percentile(sorted: &[Duration], fraction: f64) -> Duration {
        if sorted.is_empty() {
            return Duration::ZERO;
        }
        let index = ((sorted.len() as f64 - 1.0) * fraction).round() as usize;
        sorted[index.min(sorted.len() - 1)]
    }

    // -----------------------------------------------------------------------
    // The claim: racing collapses the tail
    // -----------------------------------------------------------------------

    /// Base cost of a healthy send in the simulation.
    const SIM_BASE_US: u64 = 100;
    /// Added cost when a path stalls.
    const SIM_STALL_MS: u64 = 3;
    /// How often each path stalls, independently.
    const SIM_STALL_PROBABILITY: f64 = 0.10;
    /// Idle time between frames.
    ///
    /// Longer than the stall, and that is load-bearing rather than cosmetic. A
    /// path is skipped while it is mid-send, so back-to-back frames issued
    /// faster than a stall lasts would all find the stalled path busy — turning
    /// one stall into a burst of single-path races and making the two paths'
    /// failures correlated. The gap is what makes them independent, and it is
    /// also what control-frame traffic actually looks like: bursty and idle, not
    /// saturating. Under genuine saturation a slow path is simply absent, which
    /// `a_busy_path_is_skipped_rather_than_queued` covers directly.
    const SIM_GAP_MS: u64 = 4;

    fn collect_samples(racer: &Racer, iterations: usize) -> Vec<Duration> {
        let mut samples = Vec::with_capacity(iterations);
        for _ in 0..iterations {
            let outcome = racer
                .race(b"control-frame", Duration::from_secs(2))
                .expect("a 13 byte frame is raceable");
            assert!(outcome.delivered(), "the simulation has no failing paths");
            samples.push(outcome.elapsed);
            std::thread::sleep(Duration::from_millis(SIM_GAP_MS));
        }
        samples.sort_unstable();
        samples
    }

    #[test]
    fn the_race_collapses_the_tail() {
        // Two independent paths, each stalling 10% of the time. Raced, both must
        // stall on the same frame for a stall to be observed: 0.10 × 0.10 = 0.01.
        // So the single-path p95 sits inside the stall and the raced p95 does
        // not — that gap is the entire value proposition of this module, and it
        // is asserted rather than described.
        const ITERATIONS: usize = 300;

        let solo_racer = Racer::new(vec![Arc::new(SimulatedPath::new(
            "solo",
            SIM_BASE_US,
            SIM_STALL_MS,
            SIM_STALL_PROBABILITY,
            0x9E37_79B9,
        ))])
        .expect("one path is a valid racer");
        let solo_samples = collect_samples(&solo_racer, ITERATIONS);

        let paths: Vec<Arc<dyn Path>> = vec![
            Arc::new(SimulatedPath::new(
                "path-a",
                SIM_BASE_US,
                SIM_STALL_MS,
                SIM_STALL_PROBABILITY,
                0x1234_5678,
            )),
            Arc::new(SimulatedPath::new(
                "path-b",
                SIM_BASE_US,
                SIM_STALL_MS,
                SIM_STALL_PROBABILITY,
                0xDEAD_BEEF,
            )),
        ];
        let raced_racer = Racer::new(paths).expect("two paths are a valid racer");
        let raced_samples = collect_samples(&raced_racer, ITERATIONS);

        let solo_p50 = percentile(&solo_samples, 0.50);
        let solo_p95 = percentile(&solo_samples, 0.95);
        let solo_p99 = percentile(&solo_samples, 0.99);
        let raced_p50 = percentile(&raced_samples, 0.50);
        let raced_p95 = percentile(&raced_samples, 0.95);
        let raced_p99 = percentile(&raced_samples, 0.99);

        println!("solo   p50={solo_p50:?} p95={solo_p95:?} p99={solo_p99:?}");
        println!("raced  p50={raced_p50:?} p95={raced_p95:?} p99={raced_p99:?}");

        // The threshold sits between the base cost and the stall, far from both,
        // so scheduler noise cannot move a sample across it.
        let threshold = Duration::from_micros(1_500);

        assert!(
            solo_p95 > threshold,
            "the single-path p95 should sit inside the stall, got {solo_p95:?}"
        );
        assert!(
            raced_p95 < threshold,
            "the raced p95 should have escaped the stall, got {raced_p95:?}"
        );

        // The honest half of the claim: the median is not what improved. A
        // failure here means the simulation stopped being a tail experiment,
        // not that racing got better.
        assert!(
            raced_p50 < solo_p50 + Duration::from_millis(1),
            "the median moved unexpectedly: solo {solo_p50:?}, raced {raced_p50:?}"
        );
    }

    #[test]
    fn a_busy_path_is_skipped_rather_than_queued() {
        // The bug this design avoids: if a stalled path queued frames instead of
        // declining them, it would answer every race one race too late, stop
        // contributing, and leave the race single-path while still costing the
        // duplicate send. Skipping makes the degradation visible in `dispatched`
        // instead of hiding it in the latency distribution.
        struct Blocking {
            release: Arc<AtomicBool>,
        }
        impl Path for Blocking {
            fn label(&self) -> &str {
                "blocking"
            }
            fn send(&self, _frame: &[u8]) -> Result<(), String> {
                while !self.release.load(Ordering::Acquire) {
                    std::thread::sleep(Duration::from_millis(1));
                }
                Ok(())
            }
        }

        let release = Arc::new(AtomicBool::new(false));
        let paths: Vec<Arc<dyn Path>> =
            vec![Arc::new(Blocking { release: Arc::clone(&release) }), Arc::new(Instant2)];
        let racer = Racer::new(paths).expect("two paths are a valid racer");

        // First frame reaches both paths; the fast one wins and the blocking one
        // is left mid-send.
        let first =
            racer.race(b"frame", Duration::from_secs(1)).expect("a 5 byte frame is raceable");
        assert_eq!(first.dispatched, 2);
        assert_eq!(first.winner.as_deref(), Some("instant"));

        // The blocking path is still busy, so the next frames must go to the
        // fast path alone rather than piling up behind it.
        for _ in 0..5 {
            let next =
                racer.race(b"frame", Duration::from_secs(1)).expect("a 5 byte frame is raceable");
            assert_eq!(
                next.dispatched, 1,
                "a busy path was dispatched to; the channel is acting as a queue"
            );
            assert_eq!(next.winner.as_deref(), Some("instant"));
        }

        release.store(true, Ordering::Release);
    }

    #[test]
    fn a_stale_report_is_never_claimed_by_a_later_race() {
        // The invariant that makes channel reuse safe. A losing path keeps
        // sending after its race concluded and drops its report into the shared
        // channel afterwards. Without the sequence check the next race would
        // pull that stale *success* off the queue and report it as its own —
        // announcing a frame delivered on a path it never dispatched to, with a
        // latency that never happened.
        struct Slow;
        impl Path for Slow {
            fn label(&self) -> &str {
                "slow"
            }
            fn send(&self, _frame: &[u8]) -> Result<(), String> {
                std::thread::sleep(Duration::from_millis(150));
                Ok(())
            }
        }

        let paths: Vec<Arc<dyn Path>> = vec![Arc::new(Slow), Arc::new(Instant2)];
        let racer = Racer::new(paths).expect("two paths are a valid racer");

        // Race 1: both dispatched, the fast path wins, the slow path is left
        // mid-send and will report ~150ms from now.
        let first =
            racer.race(b"frame", Duration::from_secs(2)).expect("a 5 byte frame is raceable");
        assert_eq!(first.dispatched, 2);
        assert_eq!(first.winner.as_deref(), Some("instant"));
        let first_seq = first.attempts.first().map(|attempt| attempt.seq).unwrap_or_default();

        // Let the slow path finish and park its stale report in the channel.
        std::thread::sleep(Duration::from_millis(250));

        // Race 2: the stale report is now sitting in the shared channel, ahead
        // of anything this race produces.
        let second =
            racer.race(b"frame", Duration::from_secs(2)).expect("a 5 byte frame is raceable");

        assert_eq!(
            second.winner.as_deref(),
            Some("instant"),
            "a stale report from the previous race was claimed as this race's winner"
        );
        for attempt in &second.attempts {
            assert_ne!(
                attempt.seq, first_seq,
                "an attempt from race {first_seq} was collected into a later race"
            );
        }
    }

    #[test]
    fn all_paths_busy_is_backpressure_not_breakage() {
        // Distinct from PathsUnavailable: these paths are alive and will free
        // up, so the caller should retry or shed rather than rebuild the racer.
        struct Blocking {
            release: Arc<AtomicBool>,
        }
        impl Path for Blocking {
            fn label(&self) -> &str {
                "blocking"
            }
            fn send(&self, _frame: &[u8]) -> Result<(), String> {
                while !self.release.load(Ordering::Acquire) {
                    std::thread::sleep(Duration::from_millis(1));
                }
                Ok(())
            }
        }

        let release = Arc::new(AtomicBool::new(false));
        let racer = Racer::new(vec![Arc::new(Blocking { release: Arc::clone(&release) })])
            .expect("one path is a valid racer");

        // Occupies the only path, and times out rather than waiting for it.
        let first = racer
            .race(b"frame", Duration::from_millis(50))
            .expect("a missed deadline is an outcome, not an error");
        assert!(!first.delivered());

        assert!(
            matches!(
                racer.race(b"frame", Duration::from_millis(50)),
                Err(NetworkError::AllPathsBusy)
            ),
            "a fully occupied racer should report backpressure"
        );

        release.store(true, Ordering::Release);
    }

    // -----------------------------------------------------------------------
    // Bounds, degradation, and lifecycle
    // -----------------------------------------------------------------------

    #[test]
    fn frames_above_the_control_rung_are_refused() {
        let racer = Racer::new(vec![Arc::new(Instant2)]).expect("one path is a valid racer");

        let ok = vec![0_u8; MAX_RACED_FRAME_BYTES];
        assert!(racer.race(&ok, Duration::from_secs(1)).is_ok());

        let too_big = vec![0_u8; MAX_RACED_FRAME_BYTES + 1];
        match racer.race(&too_big, Duration::from_secs(1)) {
            Err(NetworkError::FrameTooLarge { bytes, limit }) => {
                assert_eq!(bytes, MAX_RACED_FRAME_BYTES + 1);
                assert_eq!(limit, MAX_RACED_FRAME_BYTES);
            }
            other => panic!("expected FrameTooLarge, got {other:?}"),
        }
    }

    #[test]
    fn a_race_needs_at_least_one_path() {
        assert!(matches!(Racer::new(Vec::new()), Err(NetworkError::NoPaths)));
    }

    #[test]
    fn one_live_path_still_wins_against_a_dead_one() {
        // Degradation, not failure: a broken path must not take the race with it.
        let paths: Vec<Arc<dyn Path>> =
            vec![Arc::new(AlwaysFails { label: "down".to_string() }), Arc::new(Instant2)];
        let racer = Racer::new(paths).expect("two paths are a valid racer");

        let outcome =
            racer.race(b"frame", Duration::from_secs(1)).expect("a 5 byte frame is raceable");

        assert_eq!(outcome.winner.as_deref(), Some("instant"));
        assert_eq!(outcome.dispatched, 2);
    }

    #[test]
    fn every_path_failing_reports_every_reason() {
        // "Undeliverable" without the per-path cause is not actionable, so a
        // total failure must wait for all of them rather than returning early.
        let paths: Vec<Arc<dyn Path>> = vec![
            Arc::new(AlwaysFails { label: "down-a".to_string() }),
            Arc::new(AlwaysFails { label: "down-b".to_string() }),
        ];
        let racer = Racer::new(paths).expect("two paths are a valid racer");

        let outcome =
            racer.race(b"frame", Duration::from_secs(1)).expect("a 5 byte frame is raceable");

        assert!(!outcome.delivered());
        assert_eq!(outcome.attempts.len(), 2, "a total failure lost a reason");
        for attempt in &outcome.attempts {
            assert!(!attempt.succeeded());
            assert_eq!(attempt.error.as_deref(), Some("simulated path is down"));
        }
    }

    #[test]
    fn a_deadline_yields_an_outcome_rather_than_an_error() {
        struct TooSlow;
        impl Path for TooSlow {
            fn label(&self) -> &str {
                "slow"
            }
            fn send(&self, _frame: &[u8]) -> Result<(), String> {
                std::thread::sleep(Duration::from_millis(400));
                Ok(())
            }
        }

        let racer = Racer::new(vec![Arc::new(TooSlow)]).expect("one path is a valid racer");
        let outcome = racer
            .race(b"frame", Duration::from_millis(40))
            .expect("a missed deadline is an outcome, not an error");

        assert!(!outcome.delivered());
        assert!(outcome.elapsed < Duration::from_millis(300), "the deadline was not honoured");
    }

    #[test]
    fn the_race_returns_without_waiting_for_the_slow_path() {
        // The mechanism, isolated: a fast path and a very slow one. If the racer
        // joined every path the elapsed time would be the slow one's.
        struct Slow;
        impl Path for Slow {
            fn label(&self) -> &str {
                "slow"
            }
            fn send(&self, _frame: &[u8]) -> Result<(), String> {
                std::thread::sleep(Duration::from_millis(300));
                Ok(())
            }
        }

        let paths: Vec<Arc<dyn Path>> = vec![Arc::new(Slow), Arc::new(Instant2)];
        let racer = Racer::new(paths).expect("two paths are a valid racer");

        let outcome =
            racer.race(b"frame", Duration::from_secs(2)).expect("a 5 byte frame is raceable");

        assert_eq!(outcome.winner.as_deref(), Some("instant"));
        assert!(
            outcome.elapsed < Duration::from_millis(100),
            "the race waited for the slow path: {:?}",
            outcome.elapsed
        );
        assert_eq!(
            outcome.attempts.len(),
            1,
            "the slow path should not have reported before the race concluded"
        );
    }

    #[test]
    fn counters_separate_delivered_from_undeliverable() {
        let paths: Vec<Arc<dyn Path>> = vec![Arc::new(AlwaysFails { label: "down".to_string() })];
        let racer = Racer::new(paths).expect("one path is a valid racer");

        for _ in 0..3 {
            let _ = racer.race(b"frame", Duration::from_millis(200));
        }
        let (raced, won) = racer.counters();
        assert_eq!(raced, 3);
        assert_eq!(won, 0, "a path that never delivers must not be counted as winning");
    }

    #[test]
    fn dropping_the_racer_joins_every_worker() {
        // A leaked worker thread would keep an Arc<dyn Path> alive, so the
        // strong count after the drop is the observable proof that it did not.
        let path = Arc::new(Instant2);
        let counted: Arc<dyn Path> = path.clone();
        assert_eq!(Arc::strong_count(&counted), 2);

        {
            let racer = Racer::new(vec![Arc::clone(&counted)]).expect("one path is a valid racer");
            let _ = racer.race(b"frame", Duration::from_secs(1));
            assert_eq!(racer.labels(), vec!["instant"]);
        }

        assert_eq!(
            Arc::strong_count(&counted),
            2,
            "a worker thread outlived the racer that owned it"
        );
    }
}
