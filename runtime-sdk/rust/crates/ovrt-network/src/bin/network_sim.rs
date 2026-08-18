//! Report-only simulation harness for the hedged race lane.
//!
//! Racing is a trade, not an improvement: it buys tail latency and it pays in
//! duplicated bytes. A harness that reported only the latency would be arguing
//! one side of its own case, so every run prints the cost next to the benefit —
//! wire bytes, amplification factor, and the per-race allocation budget that
//! `docs/performance_practices.md` treats as a contract.
//!
//! Two lanes, because they answer different questions:
//!
//! - `sim` drives paths with a controlled latency distribution. Only a simulated
//!   path lets the stall probability be *known*, which is what makes the p²
//!   claim measurable rather than anecdotal.
//! - `udp` drives real loopback sockets through the real interface-binding path.
//!   It cannot tell you about tails, because loopback has none worth measuring;
//!   it tells you the syscall floor, which is the number that says whether the
//!   racer's own overhead is significant against a real send.
//!
//! Usage:
//!
//! ```text
//! cargo run --release -p ovrt-network --bin network_sim
//! cargo run --release -p ovrt-network --bin network_sim -- --lane udp
//! cargo run --release -p ovrt-network --bin network_sim -- \
//!     --iterations 500 --frame-bytes 1024 --stall-pct 5 --loss-pct 2
//! ```

use std::alloc::{GlobalAlloc, Layout, System};
use std::net::UdpSocket;
use std::sync::atomic::{AtomicU64, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use ovrt_network::{interface, NetworkError, Path, Racer};

// ---------------------------------------------------------------------------
// Allocation accounting
// ---------------------------------------------------------------------------

/// Wraps the system allocator and counts what passes through it.
///
/// `allocs/op` is the currency this repository's benchmark ratchet gates on,
/// because it is a property of the code rather than of the machine: it does not
/// move with CPU model, thermal state, or a noisy CI neighbour. Measuring it
/// here means the racer's per-frame cost is a number that can be held to.
struct CountingAllocator;

static ALLOC_COUNT: AtomicU64 = AtomicU64::new(0);
static ALLOC_BYTES: AtomicU64 = AtomicU64::new(0);

// SAFETY: every method forwards directly to the system allocator with the
// caller's own layout, adding only relaxed counter arithmetic. The allocator
// contract is therefore whatever System guarantees, unchanged.
unsafe impl GlobalAlloc for CountingAllocator {
    unsafe fn alloc(&self, layout: Layout) -> *mut u8 {
        ALLOC_COUNT.fetch_add(1, Ordering::Relaxed);
        ALLOC_BYTES.fetch_add(layout.size() as u64, Ordering::Relaxed);
        // SAFETY: `layout` is the caller's, forwarded unmodified.
        unsafe { System.alloc(layout) }
    }

    unsafe fn dealloc(&self, ptr: *mut u8, layout: Layout) {
        // SAFETY: `ptr` and `layout` are the caller's, forwarded unmodified.
        unsafe { System.dealloc(ptr, layout) }
    }

    unsafe fn realloc(&self, ptr: *mut u8, layout: Layout, new_size: usize) -> *mut u8 {
        ALLOC_COUNT.fetch_add(1, Ordering::Relaxed);
        ALLOC_BYTES.fetch_add(new_size as u64, Ordering::Relaxed);
        // SAFETY: all three arguments are the caller's, forwarded unmodified.
        unsafe { System.realloc(ptr, layout, new_size) }
    }
}

#[global_allocator]
static GLOBAL: CountingAllocator = CountingAllocator;

#[derive(Clone, Copy)]
struct AllocSnapshot {
    count: u64,
    bytes: u64,
}

impl AllocSnapshot {
    fn take() -> Self {
        Self {
            count: ALLOC_COUNT.load(Ordering::Relaxed),
            bytes: ALLOC_BYTES.load(Ordering::Relaxed),
        }
    }

    fn since(self, earlier: Self) -> Self {
        Self {
            count: self.count.saturating_sub(earlier.count),
            bytes: self.bytes.saturating_sub(earlier.bytes),
        }
    }
}

// ---------------------------------------------------------------------------
// Deterministic randomness
// ---------------------------------------------------------------------------

/// xorshift64*, seeded per path.
///
/// A run has to be reproducible for its numbers to be worth comparing against
/// last week's, and an RNG crate would be a permanent dependency for four lines
/// of arithmetic in a report-only binary.
struct Rng(u64);

impl Rng {
    /// Builds a generator from a path index.
    ///
    /// The index goes through a splitmix64 finalizer first, and that step is
    /// load-bearing rather than decorative. xorshift64* has weak avalanche from
    /// nearby seeds: seeding paths 0, 1 and 2 with values differing in a few
    /// bits leaves their first few hundred outputs visibly correlated, which
    /// makes independent paths stall *together*. Since the entire claim under
    /// measurement is that independent paths do not stall together, a correlated
    /// seed does not add noise to the result — it silently argues against the
    /// thing being measured. The finalizer decorrelates the seeds before the
    /// generator ever runs.
    fn for_path(index: usize) -> Self {
        let mut z = 0x9E37_79B9_7F4A_7C15_u64.wrapping_mul(index as u64 + 1);
        z = (z ^ (z >> 30)).wrapping_mul(0xBF58_476D_1CE4_E5B9);
        z = (z ^ (z >> 27)).wrapping_mul(0x94D0_49BB_1331_11EB);
        Self(z ^ (z >> 31))
    }

    fn next_unit(&mut self) -> f64 {
        let mut x = self.0;
        x ^= x >> 12;
        x ^= x << 25;
        x ^= x >> 27;
        self.0 = x;
        ((x.wrapping_mul(0x2545_f491_4f6c_dd1d)) >> 11) as f64 / (1_u64 << 53) as f64
    }
}

/// Measures what `thread::sleep` actually costs at the configured base latency.
///
/// Every simulated path is a sleep, so the instrument has a resolution and the
/// report is not trustworthy without it. If the measured floor is close to the
/// differences being claimed, the run is reporting the scheduler rather than the
/// racer.
fn measure_timer_floor(base_us: u64, samples: usize) -> (Duration, Duration) {
    let mut observed = Vec::with_capacity(samples);
    for _ in 0..samples {
        let started = std::time::Instant::now();
        std::thread::sleep(Duration::from_micros(base_us));
        observed.push(started.elapsed());
    }
    observed.sort_unstable();
    (percentile(&observed, 0.50), percentile(&observed, 0.99))
}

// ---------------------------------------------------------------------------
// Paths
// ---------------------------------------------------------------------------

/// A path with a known latency distribution and a known failure rate.
struct SimulatedPath {
    label: String,
    base: Duration,
    stall: Duration,
    stall_probability: f64,
    loss_probability: f64,
    rng: Mutex<Rng>,
    bytes_sent: AtomicUsize,
}

impl Path for SimulatedPath {
    fn label(&self) -> &str {
        &self.label
    }

    fn send(&self, frame: &[u8]) -> Result<(), String> {
        let (stall_roll, loss_roll) = match self.rng.lock() {
            Ok(mut rng) => (rng.next_unit(), rng.next_unit()),
            Err(poisoned) => {
                let mut rng = poisoned.into_inner();
                (rng.next_unit(), rng.next_unit())
            }
        };

        // Bytes are counted before the loss check on purpose: a frame that is
        // dropped downstream still crossed the wire and still cost bandwidth.
        // Counting only successes would understate the price of racing, which
        // is the number this harness exists to keep honest.
        self.bytes_sent.fetch_add(frame.len(), Ordering::Relaxed);

        let delay =
            if stall_roll < self.stall_probability { self.base + self.stall } else { self.base };
        std::thread::sleep(delay);

        if loss_roll < self.loss_probability {
            return Err("simulated loss".to_string());
        }
        Ok(())
    }
}

/// A path that puts the frame on a real loopback UDP socket.
///
/// The socket is bound to the loopback interface through the crate's own binding
/// path, so a `udp` run exercises `interface::bind_socket_to_interface` for real
/// rather than trusting its unit test.
struct UdpPath {
    label: String,
    socket: UdpSocket,
    target: std::net::SocketAddr,
    bound_to_interface: bool,
    bytes_sent: AtomicUsize,
}

impl Path for UdpPath {
    fn label(&self) -> &str {
        &self.label
    }

    fn send(&self, frame: &[u8]) -> Result<(), String> {
        self.bytes_sent.fetch_add(frame.len(), Ordering::Relaxed);
        self.socket.send_to(frame, self.target).map(|_| ()).map_err(|error| error.to_string())
    }
}

// ---------------------------------------------------------------------------
// Statistics
// ---------------------------------------------------------------------------

fn percentile(sorted: &[Duration], fraction: f64) -> Duration {
    if sorted.is_empty() {
        return Duration::ZERO;
    }
    let index = ((sorted.len() as f64 - 1.0) * fraction).round() as usize;
    sorted[index.min(sorted.len() - 1)]
}

fn micros(duration: Duration) -> f64 {
    duration.as_secs_f64() * 1_000_000.0
}

struct Row {
    paths: usize,
    p50: Duration,
    p95: Duration,
    p99: Duration,
    p999: Duration,
    worst: Duration,
    undelivered_pct: f64,
    degraded_pct: f64,
    wire_bytes: usize,
    allocs_per_race: f64,
    bytes_per_race: f64,
}

/// The percentile above which a stall is still expected to appear, given that
/// every path must stall for the race to stall.
///
/// This is the number that makes the table readable. With a 10% per-path stall
/// and two paths, co-stall probability is 1% and the stall therefore lands
/// exactly on p99 — so a p99 sitting inside the stall is the arithmetic working,
/// not the racer failing. Without this column a reader draws the opposite
/// conclusion from a correct result, which is worse than reporting nothing.
fn costall_percentile(stall_pct: f64, paths: usize) -> f64 {
    let probability = (stall_pct / 100.0).powi(paths as i32);
    100.0 * (1.0 - probability)
}

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

struct Config {
    lane: String,
    iterations: usize,
    frame_bytes: usize,
    base_us: u64,
    stall_ms: u64,
    stall_pct: f64,
    loss_pct: f64,
    gap_ms: u64,
    max_paths: usize,
}

impl Default for Config {
    fn default() -> Self {
        Self {
            lane: "sim".to_string(),
            iterations: 400,
            frame_bytes: 512,
            base_us: 100,
            stall_ms: 3,
            stall_pct: 10.0,
            loss_pct: 0.0,
            // Longer than the stall, deliberately. A path is skipped while it is
            // mid-send, so frames issued faster than a stall lasts would all find
            // the stalled path busy — one stall becomes a burst of single-path
            // races and the two paths' failures stop being independent. The gap
            // is what keeps the experiment measuring what it claims to.
            gap_ms: 4,
            max_paths: 3,
        }
    }
}

fn parse_config() -> Result<Config, String> {
    let mut config = Config::default();
    let args: Vec<String> = std::env::args().skip(1).collect();

    let mut index = 0;
    while index < args.len() {
        let flag = args[index].as_str();
        let value = args.get(index + 1).ok_or_else(|| format!("{flag} needs a value"))?;
        let parse_usize =
            || -> Result<usize, String> { value.parse().map_err(|_| format!("{flag}: {value}")) };
        let parse_u64 =
            || -> Result<u64, String> { value.parse().map_err(|_| format!("{flag}: {value}")) };
        let parse_f64 =
            || -> Result<f64, String> { value.parse().map_err(|_| format!("{flag}: {value}")) };

        match flag {
            "--lane" => config.lane = value.clone(),
            "--iterations" => config.iterations = parse_usize()?,
            "--frame-bytes" => config.frame_bytes = parse_usize()?,
            "--base-us" => config.base_us = parse_u64()?,
            "--stall-ms" => config.stall_ms = parse_u64()?,
            "--stall-pct" => config.stall_pct = parse_f64()?,
            "--loss-pct" => config.loss_pct = parse_f64()?,
            "--gap-ms" => config.gap_ms = parse_u64()?,
            "--max-paths" => config.max_paths = parse_usize()?,
            other => return Err(format!("unknown flag {other}")),
        }
        index += 2;
    }

    if config.frame_bytes > ovrt_network::MAX_RACED_FRAME_BYTES {
        return Err(format!(
            "--frame-bytes {} exceeds the {} byte racing bound; frames above the control rung \
             are striped, not raced",
            config.frame_bytes,
            ovrt_network::MAX_RACED_FRAME_BYTES
        ));
    }
    if config.max_paths == 0 {
        return Err("--max-paths must be at least 1".to_string());
    }
    Ok(config)
}

// ---------------------------------------------------------------------------
// Lanes
// ---------------------------------------------------------------------------

fn build_simulated_paths(config: &Config, count: usize) -> Vec<Arc<SimulatedPath>> {
    (0..count)
        .map(|index| {
            Arc::new(SimulatedPath {
                label: format!("sim-{index}"),
                base: Duration::from_micros(config.base_us),
                stall: Duration::from_millis(config.stall_ms),
                stall_probability: config.stall_pct / 100.0,
                loss_probability: config.loss_pct / 100.0,
                // Decorrelated, fixed seeds: independent draws, reproducible run.
                rng: Mutex::new(Rng::for_path(index)),
                bytes_sent: AtomicUsize::new(0),
            })
        })
        .collect()
}

fn build_udp_paths(count: usize) -> Result<(Vec<Arc<UdpPath>>, UdpSocket), String> {
    let sink = UdpSocket::bind("127.0.0.1:0").map_err(|error| error.to_string())?;
    let target = sink.local_addr().map_err(|error| error.to_string())?;

    let loopback = interface::enumerate()
        .map_err(|error| error.to_string())?
        .into_iter()
        .find(|candidate| candidate.is_loopback());

    let mut paths = Vec::with_capacity(count);
    for index in 0..count {
        let socket = UdpSocket::bind("127.0.0.1:0").map_err(|error| error.to_string())?;

        // Exercise the real binding path. A refusal is reported, not fatal: the
        // whole point of the probe is that binding is a capability, not a given.
        let mut bound_to_interface = false;
        if let Some(loopback) = loopback.as_ref() {
            #[cfg(unix)]
            {
                use std::os::fd::AsRawFd;
                bound_to_interface =
                    interface::bind_socket_to_interface(socket.as_raw_fd(), loopback).is_ok();
            }
        }

        paths.push(Arc::new(UdpPath {
            label: format!("udp-{index}"),
            socket,
            target,
            bound_to_interface,
            bytes_sent: AtomicUsize::new(0),
        }));
    }
    Ok((paths, sink))
}

// ---------------------------------------------------------------------------
// Measurement
// ---------------------------------------------------------------------------

fn measure(
    racer: &Racer,
    config: &Config,
    frame: &[u8],
    expected_paths: usize,
) -> Result<(Vec<Duration>, usize, usize), String> {
    let mut samples = Vec::with_capacity(config.iterations);
    let mut undelivered = 0;
    let mut degraded = 0;

    for _ in 0..config.iterations {
        match racer.race(frame, Duration::from_secs(5)) {
            Ok(outcome) => {
                if !outcome.delivered() {
                    undelivered += 1;
                }
                if outcome.dispatched < expected_paths {
                    degraded += 1;
                }
                samples.push(outcome.elapsed);
            }
            // Backpressure is a measurable state, not a crash: every path was
            // still mid-send. Recorded as both undelivered and degraded so it
            // shows up in the report rather than skewing the latency samples.
            Err(NetworkError::AllPathsBusy) => {
                undelivered += 1;
                degraded += 1;
            }
            Err(error) => return Err(error.to_string()),
        }
        if config.gap_ms > 0 {
            std::thread::sleep(Duration::from_millis(config.gap_ms));
        }
    }

    samples.sort_unstable();
    Ok((samples, undelivered, degraded))
}

fn row_from(
    paths: usize,
    config: &Config,
    samples: &[Duration],
    undelivered: usize,
    degraded: usize,
    wire_bytes: usize,
    allocs: AllocSnapshot,
) -> Row {
    let iterations = config.iterations.max(1) as f64;
    Row {
        paths,
        p50: percentile(samples, 0.50),
        p95: percentile(samples, 0.95),
        p99: percentile(samples, 0.99),
        p999: percentile(samples, 0.999),
        worst: samples.last().copied().unwrap_or(Duration::ZERO),
        undelivered_pct: undelivered as f64 * 100.0 / iterations,
        degraded_pct: degraded as f64 * 100.0 / iterations,
        wire_bytes,
        allocs_per_race: allocs.count as f64 / iterations,
        bytes_per_race: allocs.bytes as f64 / iterations,
    }
}

fn print_report(config: &Config, rows: &[Row], notes: &[String]) {
    println!();
    println!("foundation runtime-network — hedged race, report-only");
    println!(
        "lane={} iterations={} frame={}B base={}us stall={}ms@{:.1}% loss={:.1}% gap={}ms",
        config.lane,
        config.iterations,
        config.frame_bytes,
        config.base_us,
        config.stall_ms,
        config.stall_pct,
        config.loss_pct,
        config.gap_ms,
    );
    println!();

    println!(
        "{:>5}  {:>9}  {:>9}  {:>9}  {:>9}  {:>9}  {:>8}  {:>7}  {:>8}  {:>10}  {:>6}  {:>9}  \
         {:>7}",
        "paths",
        "p50",
        "p95",
        "p99",
        "p99.9",
        "max",
        "stall>p",
        "undlv%",
        "degrade%",
        "wire B",
        "amp",
        "allocs/op",
        "B/op"
    );

    let baseline_wire = rows.first().map(|row| row.wire_bytes).unwrap_or(0) as f64;
    for row in rows {
        let amplification =
            if baseline_wire > 0.0 { row.wire_bytes as f64 / baseline_wire } else { 0.0 };
        println!(
            "{:>5}  {:>8.1}u  {:>8.1}u  {:>8.1}u  {:>8.1}u  {:>8.1}u  {:>7.2}%  {:>6.1}%  \
             {:>7.1}%  {:>10}  {:>5.2}x  {:>9.1}  {:>7.0}",
            row.paths,
            micros(row.p50),
            micros(row.p95),
            micros(row.p99),
            micros(row.p999),
            micros(row.worst),
            costall_percentile(config.stall_pct, row.paths),
            row.undelivered_pct,
            row.degraded_pct,
            row.wire_bytes,
            amplification,
            row.allocs_per_race,
            row.bytes_per_race,
        );
    }

    println!();
    println!(
        "how to read `stall>p`: every path must stall for the race to stall, so with a {:.1}% \
         per-path",
        config.stall_pct
    );
    println!(
        "stall the co-stall probability is {:.1}%^paths. A stall is EXPECTED at and above the \
         listed",
        config.stall_pct
    );
    println!(
        "percentile and should be absent below it. A p99 inside the stall at 2 paths is the \
         arithmetic"
    );
    println!(
        "working, not the racer failing — compare each row at a percentile below its own \
         `stall>p`."
    );

    println!();
    if let (Some(first), Some(last)) = (rows.first(), rows.last()) {
        // Compared at p95, which sits below the co-stall percentile for every
        // path count this harness sweeps. Comparing at p99 would pit a
        // single-path row against a raced row on the one percentile where the
        // raced row is expected to stall, and understate the result by
        // construction.
        let tail_gain = if last.p95.is_zero() {
            0.0
        } else {
            micros(first.p95) / micros(last.p95).max(f64::MIN_POSITIVE)
        };
        let median_gain = if last.p50.is_zero() {
            0.0
        } else {
            micros(first.p50) / micros(last.p50).max(f64::MIN_POSITIVE)
        };
        let cost = if first.wire_bytes > 0 {
            last.wire_bytes as f64 / first.wire_bytes as f64
        } else {
            0.0
        };
        println!(
            "verdict: {} paths vs 1 — p95 {tail_gain:.1}x better, p50 {median_gain:.2}x, \
             bandwidth {cost:.2}x",
            last.paths
        );
        println!(
            "         racing is a tail trade. A p50 near 1.00x is the expected result, not a \
             failed one."
        );
    }
    for note in notes {
        println!("note:    {note}");
    }
    println!();
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

fn main() {
    let config = match parse_config() {
        Ok(config) => config,
        Err(error) => {
            eprintln!("network_sim: {error}");
            std::process::exit(2);
        }
    };

    let frame = vec![0xA5_u8; config.frame_bytes];
    let mut rows = Vec::with_capacity(config.max_paths);
    let mut notes = Vec::new();

    if config.lane == "sim" {
        // Calibrate before measuring. Every simulated path is a sleep, so this
        // is the instrument's own resolution, and a reader who does not know it
        // cannot tell a result from an artefact.
        let (floor_p50, floor_p99) = measure_timer_floor(config.base_us, 200);
        notes.push(format!(
            "timer floor: sleep({}us) actually takes p50={:.1}us p99={:.1}us — the base latency \
             below is this, not {}us",
            config.base_us,
            micros(floor_p50),
            micros(floor_p99),
            config.base_us
        ));
    }

    match config.lane.as_str() {
        "sim" => {
            for count in 1..=config.max_paths {
                let paths = build_simulated_paths(&config, count);
                let racer = match Racer::new(
                    paths.iter().map(|path| path.clone() as Arc<dyn Path>).collect(),
                ) {
                    Ok(racer) => racer,
                    Err(error) => {
                        eprintln!("network_sim: {error}");
                        std::process::exit(1);
                    }
                };

                let before = AllocSnapshot::take();
                let measured = measure(&racer, &config, &frame, count);
                let allocs = AllocSnapshot::take().since(before);

                match measured {
                    Ok((samples, undelivered, degraded)) => {
                        let wire = paths
                            .iter()
                            .map(|path| path.bytes_sent.load(Ordering::Relaxed))
                            .sum::<usize>();
                        rows.push(row_from(
                            count,
                            &config,
                            &samples,
                            undelivered,
                            degraded,
                            wire,
                            allocs,
                        ));
                    }
                    Err(error) => {
                        eprintln!("network_sim: {error}");
                        std::process::exit(1);
                    }
                }
            }
        }
        "udp" => {
            notes.push(
                "loopback has no tail worth measuring; this lane reports the syscall floor and \
                 proves the binding path runs end to end"
                    .to_string(),
            );
            for count in 1..=config.max_paths {
                let (paths, sink) = match build_udp_paths(count) {
                    Ok(built) => built,
                    Err(error) => {
                        eprintln!("network_sim: {error}");
                        std::process::exit(1);
                    }
                };
                if count == 1 {
                    let bound = paths.iter().filter(|path| path.bound_to_interface).count();
                    notes.push(format!(
                        "interface binding: probe={}, sockets actually bound={}/{}",
                        interface::interface_binding_supported(),
                        bound,
                        paths.len()
                    ));
                }

                // Drains the sink so the socket buffer cannot fill and start
                // failing sends, which would measure the receiver, not the send.
                //
                // The stop flag and the read timeout are both required, and the
                // reason is a bug this harness had: `try_clone` duplicates the
                // descriptor, so dropping the original sink leaves the clone
                // open and a blocking `recv_from` waits for a packet that will
                // never arrive. The thread became unjoinable and the run hung.
                // A thread with no termination path is not a detail — it is the
                // same rule the racer's own workers follow.
                let stop = Arc::new(std::sync::atomic::AtomicBool::new(false));
                let drain_stop = Arc::clone(&stop);
                let drain_handle = sink.try_clone().ok().map(|socket| {
                    let _ = socket.set_read_timeout(Some(Duration::from_millis(100)));
                    std::thread::spawn(move || {
                        let mut scratch = vec![0_u8; 65_536];
                        while !drain_stop.load(Ordering::Acquire) {
                            // A timeout is the expected case once the sweep
                            // stops sending; it is how this loop stays
                            // interruptible, not an error.
                            let _ = socket.recv_from(&mut scratch);
                        }
                    })
                });

                let racer = match Racer::new(
                    paths.iter().map(|path| path.clone() as Arc<dyn Path>).collect(),
                ) {
                    Ok(racer) => racer,
                    Err(error) => {
                        eprintln!("network_sim: {error}");
                        std::process::exit(1);
                    }
                };

                let before = AllocSnapshot::take();
                let measured = measure(&racer, &config, &frame, count);
                let allocs = AllocSnapshot::take().since(before);

                stop.store(true, Ordering::Release);
                if let Some(handle) = drain_handle {
                    let _ = handle.join();
                }
                drop(sink);

                match measured {
                    Ok((samples, undelivered, degraded)) => {
                        let wire = paths
                            .iter()
                            .map(|path| path.bytes_sent.load(Ordering::Relaxed))
                            .sum::<usize>();
                        rows.push(row_from(
                            count,
                            &config,
                            &samples,
                            undelivered,
                            degraded,
                            wire,
                            allocs,
                        ));
                    }
                    Err(error) => {
                        eprintln!("network_sim: {error}");
                        std::process::exit(1);
                    }
                }
            }
        }
        other => {
            eprintln!("network_sim: unknown lane {other}; expected sim or udp");
            std::process::exit(2);
        }
    }

    print_report(&config, &rows, &notes);
}
