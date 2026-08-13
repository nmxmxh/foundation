# The runtime transport: what was wrong, what it cost, and what changed

Status: current as of 2026-08-13. All figures are Apple M1 Pro, release builds,
warm. Reproduce with the commands in each section — every number here has one.

This document exists because the most expensive defect in the runtime transport
survived for months in plain sight, described accurately in its own comments,
and nobody costed it. The lesson generalises past this lane, so the reasoning is
recorded alongside the results.

## The shape of the problem

The runtime SDK's pitch is a fixed 4 KiB control buffer shared between a Go host
and a Rust kernel, with bulk payloads in a separately mapped arena. Zero
serialization, zero copies, a descriptor id crossing the control plane instead
of megabytes of data.

Two of those three claims were false for the `shm` transport.

**The control buffer was copied twice per exchange.** The Go host mapped it
(`syscall.Mmap`, `MAP_SHARED`). The Rust kernel opened the same file and used
`pread` into a freshly allocated `Vec`, processed the copy, then `pwrite` back.
Two syscalls, 8 KiB of copying and one heap allocation for a region that was
already addressable from both sides.

**The arena was copied more than that, and structurally.** A mapped writer
against a positional reader drifts — on darwin a freshly published descriptor
reads back as FREE, which looks exactly like a protocol race and is not one. The
Rust crate was `#![forbid(unsafe_code)]` and could not `mmap`, so the host gave
up its own mapping to match the kernel: the arena staged into a private buffer,
published with a positional write of everything staged, and read results back
through the file. That is an arena-sized allocation per worker, a copy of the
working set on every exchange, and an `msync` to make the two views agree.

**And the crossing itself was a pipe.** The host writes the unit id as a stdio
frame and blocks reading an acknowledgement — two context switches and four
syscalls, independent of payload size.

## Why it happened, which is the part worth keeping

Every step was locally reasonable.

The control buffer is 4096 bytes: `INPUT_MAX_BYTES` is 1024, which is **128
`float64`s**. Real batches are larger. Units handed real data failed on every
call — and failed *silently*, because the Go lane has a fallback for every
kernel, so exceeding the control payload was not an error. The native lane
quietly ceased to exist while continuing to be scheduled.

The fix was the arena, and it was the right fix for capacity. But it was built
on the existing exchange, so it inherited the pipe doorbell. Batch size was
solved; latency was never in scope, so it was never measured.

The consequence compounds. A crossing costing ~38.5 us only pays for batches
large enough to amortise it — roughly a thousand items. So lanes doing per-item
work over big batches kept Rust primary, and a lane whose payloads are a handful
of scalars had no way to justify crossing at all. It became a *verifier*: Go
computes, Rust recomputes, the two are compared, Go's value stands. That is what
a blocked offload path degrades into, and from the inside it reads as a
deliberate architectural choice.

**The generalisable rule: a cost nobody measured becomes a design constraint
nobody chose.** The absence of a transport cost table is what let a pool be
configured for `shm` without anyone asking what the doorbell cost.

## What changed

`ovrt_core::SharedMapping` — a safe `mmap` wrapper in the one crate that permits
unsafe (`ovrt-core` already owns the raw-pointer control-plane code in
`log_ring.rs`). `libc` directly rather than a mapping crate: it is a declaration
crate, so the call compiles to the syscall with nothing in between. This is the
workspace's first non-optional third-party dependency.

`ovrt-native` keeps `#![forbid(unsafe_code)]` and contains no unsafe. Both ends
of both regions now map. Deleted rather than worked around: the staging buffer,
the publish copy, `msync_unix.go`, `newSharedMemoryFile`, and
`sharedMemorySegment`'s `Sync`/`WriteAt`/`ReadAt`.

```bash
cargo run --release -p ovrt-native --bin shm_exchange_bench --manifest-path runtime-sdk/rust/Cargo.toml
```

| Exchange body | ns/op |
| :--- | ---: |
| control buffer, positional: `pread` + 4 KiB alloc + process + `pwrite` | 1903 |
| control buffer, mapped: process in place | 130 |
| arena slab read (64 KiB), positional: 4x `pread` descriptor + slab | 4781 |
| arena slab read (64 KiB), mapped: descriptor + slab borrow | 10 |

The arena row is the one that matters and it was the least visible cost in the
system. A slab read was never one `pread`: `Arena::descriptor` issued **four
separate four-byte positional reads** to assemble one table entry, each
allocating, before the slab read began. Reading a columnar batch cost a syscall
and an allocation *per column*. Mapped, the same read is an offset into memory
the host already wrote — a borrow, not a copy — which is why it no longer scales
with slab size.

### The end-to-end result, stated honestly

| `BenchmarkRustArenaRoundTrip` (pronto) | ns/op |
| :--- | ---: |
| before | 38,537 |
| after | ~22,000 |

43% off the crossing, and the body cost is essentially gone. But the break-even
batch only moved from roughly 1,750 items to roughly 1,000 — the same order of
magnitude. **The economics of offloading did not change.** The residual is the
stdio doorbell, which none of this touched.

That is the measurement confirming the diagnosis rather than contradicting it:
there is no longer anything else to blame. It is also the reason not to bank the
43% as if it changed the design space.

## The cold-start trap

Reproducible, and it nearly produced a wrong conclusion. A first run immediately
after rebuilding the kernel read **373,888 ns**; the next run, unchanged, read
**22,118 ns**. A 17x penalty from cold executable pages and first-touch faults
on the 8 MiB mapping.

Two consequences, both now handled:

- **Never benchmark immediately after a build.** Any transport comparison run
  that way is measuring page faults. A 4.7x "regression" was reported internally
  from exactly this before it was caught.
- **Warm the pool, not the first caller.** `warmMapping` walks one byte per page
  of the host's mappings at startup (measured 1.499 ms cold → 16.2 us warmed on
  an 8 MiB segment). The child is a separate address space, so `ProcessPool` also
  runs one throwaway exchange per worker (`warmupLocked`). It needs no
  configuration: an unknown unit id is answered with an in-band status code
  rather than a transport error, because the protocol has always had to tolerate
  a client sending any id. `WarmupUnitID` names a real unit to warm its code too.

The warm-up never fails a start, and is routed through `executeWithContext`
rather than the exchange directly — the exchanges ignore their context, so an
unbounded warm-up would let a kernel that hangs on its first call hang startup,
which is worse than the problem being solved.

## Choosing a transport

Measured in foundation, against a real child process — see *The evidence*
below for the harness and how to reproduce.

| Mode | Isolation | ns/op | B/op | allocs/op |
| :--- | :--- | ---: | ---: | ---: |
| `ffi` | none (same address space) | ~1,300 | 0 via `ExecuteInto` | 0 |
| `shm-epoch` | separate process | **3,211 – 4,472** | 472 | 8 |
| `shm` | separate process | 15,623 – 16,153 | 448 | 9 |
| `stdio` | separate process | 20,891 – 23,416 | 444 | 10 |

Three runs of 3,000 exchanges each, warm, M1 Pro. The remaining allocations are
host-side request bookkeeping, not transport; the kernel allocates nothing in
steady state on any of them.

`ffi` is still the fastest and always will be — it never leaves the address
space. But the gap to a process-isolated lane is no longer 17x: with the epoch
doorbell it is about 3x, which changes the answer for a pool that has any real
reason to want isolation. The question remains **which pools actually need a
separate address space**, but the cost of saying yes has dropped by a factor of
four.

### What isolation actually buys, precisely

A panicking *unit* does not need process isolation. `catch_unwind` wraps unit
dispatch at three levels in `ovrt-native`, and the panic is reported as status
code 2 with `IDX_PANIC_STATE` set. No profile sets `panic = "abort"`.

The remaining gap was a panic in the buffer handling *around* the unit — a
slicing bug, an overflow in a header offset. In a separate process that is a
dead child the pool restarts. Under `ffi` it would unwind into `extern "C"`,
where Rust must abort, taking the Go host with it: every other pool, every
in-flight request. `ovrt_ffi::process_buffer` now catches at the boundary too
and reports through the same error channel as any other failure.

What `ffi` still cannot survive: a segfault, a stack overflow, or an `abort()`
in the kernel — none of which unwinding can catch. If a pool's kernel is your
own Rust crate with `forbid(unsafe_code)`, those are close to unreachable. If it
links C, or does its own `unsafe`, process isolation is still earning its 21 us.

**So the default should be `ffi` for pools whose kernels are safe Rust, and
`shm` reserved for the ones that genuinely aren't.** That decision is worth
making per pool before investing in the epoch doorbell, because it changes how
much the doorbell is worth.

### `ExecuteInto`, and why `Execute` was odd

`FFIPool` had only `Execute`, which allocated twice per call: an output copy,
and a 4 KiB error buffer that is almost never written. A lane whose argument is
"no copies across the boundary" was paying the error path's cost at the rate of
the success path. `ExecuteInto` writes into a caller-owned destination, the
error buffer is pooled, and the control buffer pool now holds `*Buffer` rather
than `*[]byte` — wrapping a pooled slice with `NewBuffer` per call put a fresh
descriptor on the heap to describe memory that was already pooled.

Steady state is now **zero allocations per call**, asserted by
`TestFFIPoolExecuteIntoAllocatesNothingInSteadyState`. A short destination is an
error rather than a truncation: units return packed binary records, so a short
result decodes as a valid smaller one and would surface as quietly missing data.

## The epoch doorbell

Landed, opt-in, and off by default: `ProcessTransportSharedMemoryEpoch`
(`"shm-epoch"`). `ProcessTransportAuto` never selects it. A transport that
silently upgraded itself would move every pool at once; ask for it per pool,
soak it, then change the default deliberately.

The crossing is now a store. The host writes the route and payload into the
mapping and increments `IDX_INPUT_WRITTEN`; the kernel is already watching,
observes the change, runs the unit in place, and increments
`IDX_OUTPUT_WRITTEN`. Neither process enters the kernel on a warm exchange.

### The route

The unit id used to travel as a pipe frame. With no pipe in the hot path it
travels in the buffer, in a new named region — `OFFSET_ROUTE_BYTES = 64`,
`ROUTE_MAX_BYTES = 64` — claimed from the gap that always existed between the 16
epoch slots (64 bytes) and the header integers at 128. Purely additive: no
existing offset moved, and the Go, Rust and TypeScript lanes regenerate from the
same schema. An id that does not fit is refused rather than truncated, because a
truncated route resolves to a different unit or to none.

### The three risks, and what each cost

**Death detection.** A dead kernel used to close stdout and fail the blocking
read. With no read in the hot path, dead and slow produce the same silent slot.
The pipe survives, demoted to one job: the host runs a supervisor goroutine that
reaps the child and clears `childRunning`, and the wait consults it while
parked. On the kernel side a thread watches stdin for EOF. `IDX_PANIC_STATE`
covers a panicking kernel; only this covers a `SIGKILL`. The liveness check runs
*after* the epoch load, never before — a kernel that published a result and then
exited did the work, and calling that a lost peer would discard a completed
exchange.

**Spin-waiting burns cores.** Bounded spin, then a doubling sleep ladder capped
at 200us. `spinIterations` is per pool because call rates differ by orders of
magnitude.

**macOS has no futex.** There is no futex here at all, on any platform, and that
is a decision rather than a gap. Parking only happens on the slow path: when the
peer answers in the time a crossing is supposed to take, the spin wins and the
waiter never sleeps — a futex would not make that faster. When the spin is
exhausted the peer is doing real work, and the difference between a futex wake
and 100us of sleep granularity is noise against it. Avoiding it removes both
`futex`'s Linux-only-ness and macOS's private `__ulock_wait`, leaving one
implementation tested identically on both. If a workload ever parks often *and*
cares about wake latency, a futex belongs behind `ovrt_core::epoch`, not spread
through callers.

### Two bugs the tests found before the transport shipped

**A wholesale buffer copy clobbers the doorbell.** `sharedMemoryExchange` copies
all 4096 bytes into the mapping, which is harmless when nothing signals through
the epoch region. Here those words *are* the channel: the copy reset
`IDX_INPUT_WRITTEN` to the caller's value and the exchange hung until timeout.
The epoch exchange copies from `OFFSET_HEADER_INTS` onward.

**Announce-then-snapshot loses the first exchange.** The kernel published
`IDX_KERNEL_READY` and then took its baseline snapshot of the input epoch. The
host publishes its warm-up call the instant the child is spawned, so that
snapshot could already include the publication, leaving the kernel waiting for a
change that had been and gone. The kernel now snapshots first and announces
second, and the host waits for `IDX_KERNEL_READY` before publishing anything.
The symptom was one timed-out exchange at startup and nothing wrong afterwards.

### The ordering test, and why the first version was worthless

The requirement was a test that fails if the acquire ordering is removed. The
first attempt ran against a real `mmap`'d segment under `go test -race`,
replaced `observeEpoch`'s atomic load with a plain deref — and passed.

**Go's race detector does not instrument memory obtained from `syscall.Mmap`.**
The runtime does not know the region exists, so no ordering test over a real
mapping can fail under `-race`, whatever you do to it.

`TestEpochOrderingPublishesThePayloadBeforeTheEpoch` therefore runs over a heap
buffer. The epoch helpers do not care where their bytes come from, so the
property is tested and the detector's coverage is kept. Verified both ways:

| Sabotage | Result |
| :--- | :--- |
| `observeEpoch` returns `*slot` instead of `atomic.LoadUint32` | `WARNING: DATA RACE` under `-race` |
| publish moved above the payload write | fails without `-race`: `round 46: byte 0 is generation 45, want 46` |

An earlier version of the test also modelled a free-running writer and tore its
own payload — which measures overwrite, not visibility. The protocol is strictly
ping-pong, and the test now is too. `TestEpochExchangeRoundTripsThroughASharedMapping`
covers the same protocol over a real mapping, where the detector cannot follow.

## The evidence

Foundation had no runnable kernel of its own. Its process tests re-exec the test
binary as a fake, which is right for protocol coverage and worthless for
latency — the fake's pages are already resident and it is the same binary the
test runs in. Every transport number this project had therefore came from an
application repo, which is the wrong place to keep evidence about foundation's
own transport. `runtime-sdk/rust/crates/ovrt-native/src/bin/reference_kernel.rs`
is that missing counterpart: one echo unit, straight into `serve_transport`, so
the only thing that differs between runs is `OVRT_RUNTIME_TRANSPORT`.

```bash
cargo build --release -p ovrt-native --bin reference_kernel \
  --manifest-path runtime-sdk/rust/Cargo.toml
export OVRT_REFERENCE_KERNEL=$PWD/runtime-sdk/rust/target/release/reference_kernel
cd runtime-sdk/go
go test ./runtimehost/ -run '^$' -bench 'BenchmarkTransport' -benchtime 3000x -count 3
OVRT_SOAK_EXCHANGES=1000000 go test ./runtimehost/ -run 'TestEpochTransport' -timeout 30m
```

The tests skip without `OVRT_REFERENCE_KERNEL` rather than invoking cargo
themselves: a test that shells out to a compiler is slow, fails for reasons
unrelated to what it asserts, and hides which binary it measured.

### Soak: 10^6 exchanges, no epoch loss

```text
1000000 exchanges in 4.013209375s (4013 ns/op)
```

Every response is checked, not just the count, and every payload differs. That
matters: a missed epoch does not surface as an error — the host reads the slot's
previous contents and gets the *previous exchange's* result. With a constant
input that is byte-identical to the expected answer, so a soak using one payload
would pass while silently dropping exchanges.

### `SIGKILL` mid-exchange

Detected in **0.01s** against a deliberately absurd one-hour exchange timeout,
so a test that passed by timing out could not be mistaken for one that passed by
detecting. Verified by sabotage as well as by passing: forcing `childAlive` to
return `true` fails the test with *"supervisor did not observe the killed
child"*. Under `stdio` and `shm` this detection is free — the blocking read
fails. Under the epoch doorbell nothing fails on its own, because a dead kernel
and a slow one write the same nothing to the slot.

### What the numbers say

The doorbell was the whole cost, as predicted. `shm` → `shm-epoch` is
16,000 ns → ~4,000 ns, and what remains is not a pipe: it is the host-side
request path, the spin before the kernel answers, and the scheduler getting the
kernel thread back onto a core. The original ~500 ns estimate assumed a
same-core handoff with both sides already spinning; 4 us is what it costs with a
real process, a real scheduler and a bounded spin that gives up rather than
burning a core.

## The crossover, and the default that caused it

The evidence above used an echo unit, and an echo unit **flatters the epoch
doorbell by construction**: the doorbell wins by spinning until the reply lands,
and with zero compute the reply always lands inside the spin. Downstream
measurement against real kernels found the other side of that — 3x faster for a
microsecond-scale unit, and **38% slower** for one taking ~570us per call.

That reproduces in foundation now, via `runtime.busy` in the reference kernel,
which burns a caller-specified number of microseconds before replying (a spin,
not a sleep: a sleeping kernel would yield its core to the host's spin and
measure a contention pattern that does not occur when a kernel actually
computes).

```bash
go test ./runtimehost/ -run '^$' -bench ServiceTimeSweep -benchtime 400x
```

| Kernel service time | `shm` | `shm-epoch` (MaxSleep 200us) | `shm-epoch` (MaxSleep 20us) |
| ---: | ---: | ---: | ---: |
| 0us | 34,414 | 4,858 | 5,755 |
| 10us | 27,661 | 17,310 | 15,808 |
| 100us | 126,095 | 110,092 | 114,847 |
| 500us | 537,074 | 707,539 *(-32%)* | 532,357 |
| 2,000us | 2,058,221 | 2,244,044 *(-9%)* | 2,038,360 |

### It was the cap, not the transport

A parked waiter overshoots the reply by up to `MaxSleep`. That is a direct tax
on every exchange the spin does not catch, and at 200us it was large enough to
lose to a pipe — which cannot overshoot, because it blocks once and the peer's
write wakes it.

The trade is lopsided: a tighter cap buys accuracy with extra wakeups, and
wakeups are cheap. Sweeping the cap at 500us of service time gives 607us at
125us, 530us at 25us, 526us at 5us — the crossover disappears well before the
cost of waking shows up. **`DefaultEpochMaxSleep` is now 20us**, which costs
nothing at the fast end (the spin still catches those) and removes the
regression at the slow end.

This also revises something claimed earlier in this document. The argument for
having no futex was that parking only happens on the slow path, where wake
latency is noise against the work. That was wrong in one direction: wake
latency on the slow path was 6–32% of the call, which is not noise. It happened
to be fixable by capping the sleep rather than by adding a futex, so the
conclusion survives — but it survived by measurement, not by the reasoning that
produced it.

### `EpochWaitTuning`, which now exists

`epochWaitPolicy`'s own field doc said "tunable per pool" while
`defaultEpochWaitPolicy` was the only constructor and nothing on
`ProcessPoolOptions` reached it — a comment describing an intention rather than
the code. `ProcessPoolOptions.EpochWaitTuning` closes that, with
`SpinIterations` (zero means default, negative means none) and `MaxSleep`.
`TestEpochWaitTuningReachesTheExchange` pins the wiring so the doc cannot drift
from the behaviour again.

With the corrected default most pools should not need it. Reach for it when the
kernel answers in microseconds and the pool is latency-critical (raise
`SpinIterations`), or when the kernel computes for milliseconds and cores are
contended (`SpinIterations: -1`, since the spin will never catch that reply).

### The rule

Keyed on expected kernel service time, and measured per pool rather than
assumed. With `MaxSleep` at 20us the epoch doorbell is faster than the pipe up
to about 100us of service time and level with it beyond, so it is no longer a
lane that can make a pool slower. It is still not a blanket default: `Auto`
resolves to `shm`, and a pool that has no latency problem has no reason to adopt
a newer protocol.

**Any measurement taken against `shm-epoch` before this change was taken with
the 200us cap and should be repeated.**

## What remains

Not the transport. The default still resolves to `shm`, deliberately —
`ProcessTransportAuto` never selects `shm-epoch`, and moving it is a decision to
make after this has run under a real workload rather than a reference echo unit.

The open question is the one this whole exercise surfaced: **which pools need a
separate address space at all.** `ffi` is 3x faster than the best process
transport and the panic boundary is now closed. For a kernel that is safe Rust
with `forbid(unsafe_code)`, the isolation is buying protection against failures
that are close to unreachable. That is a per-pool judgement, and it is now a
judgement with numbers attached.

## Practice notes this produced

1. **Cost every transport before choosing one.** The table above is the artefact
   whose absence caused this.
2. **A silent fallback is worse than an error.** Exceeding the control payload
   disabled the native lane invisibly. Prefer a loud failure at the boundary.
3. **Never benchmark immediately after a build.**
4. **`#![forbid(unsafe_code)]` is a placement decision, not a prohibition.**
   The constraint shaped a whole transport around positional I/O when the answer
   was to put twelve lines of `mmap` in the crate that already permits unsafe.
5. **Production code may not call `*_for_test` helpers.** The `shm` serve loop
   called `read_frame_for_test`/`write_frame_for_test`; the naming was the tell
   that the loop was never meant to be a hot path. Those helpers are now
   `#[cfg(test)]`, so the compiler enforces it.
