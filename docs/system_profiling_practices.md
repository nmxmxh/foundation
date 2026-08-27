# System Profiling Practices

Status: baseline
Date: 2026-08-26
Owner: Runtime Performance

## Purpose

Foundation already knows how to benchmark itself. `performance_practices.md`
holds the rules, `performance_lab.md` holds the evidence contract,
`foundation_benchmarks.md` holds the numbers, and `server-kit/go/profiling`
exposes the in-process pprof surface. All of that measures the process from
inside.

This document owns the lane below that: what the operating system can tell you
about a Foundation process that the process cannot tell you about itself —
CPU samples across every thread, hardware counters, syscall and page-fault
attribution, and the stack traces that connect a line of Go or Rust to the
kernel work it caused.

It also collects the practices that fall out of profiling mechanics. Knowing
how a profiler resolves a symbol, unwinds a stack, and drops an event changes
how you build and lay out code, not just how you measure it.

Reference: Julia Evans, *Profiling & Tracing with perf*
(<https://wizardzines.com/zines/perf/>) and Brendan Gregg's perf material
(<https://brendangregg.com/perf.html>).

Related docs:

- `foundation/docs/performance_practices.md`
- `foundation/docs/performance_lab.md`
- `foundation/docs/foundation_benchmarks.md`
- `foundation/docs/optimization_points.md`
- `foundation/docs/columnar_null_algebra.md`
- `foundation/docs/game_runtime_practices.md`
- `foundation/docs/frontend_runtime_workbench.md`
- `foundation/docs/runtime_native.md`

## The four questions

System profiling answers four different questions, and each has one cheapest
tool. Reaching past the cheapest one is the most common waste in this lane.

| Question | Tool | Cost | Foundation lane |
| --- | --- | --- | --- |
| What is hot *right now*? | `perf top` | bounded by sample rate | any live process pinned at 100% CPU |
| What was hot *during this window*? | `perf record` + `perf report` / flamegraph | bounded by sample rate | benchmark runs, load tests, worker drains |
| How many of *X* happened? | `perf stat` | near-zero for a few counters | IPC, cache, branch, page-fault claims |
| Which *kernel events* did we cause? | `perf trace`, `perf record -e` | scales with event rate | syscall counts, transport-ladder rung proof, I/O attribution |

Two of these sample and two of them do not, and the distinction is the whole
cost model:

- **Sampling** (`perf top`, `perf record`) interrupts at a fixed frequency —
  99 or 49 Hz is a normal choice — and records what was running. Overhead is
  set by `-F`, not by how busy the program is.
- **Event recording** (`perf record -e`, `perf stat -e`, `perf trace`) tries to
  observe *every* occurrence. Overhead scales with the event rate, and on a
  syscall-heavy workload that is not a rounding error: counting every syscall
  made `find` run roughly six times slower in the zine's measurements, and
  `strace` on the same shape of workload costs about 10x.

The rule that follows is Foundation's existing measurement discipline applied
to the measuring tool itself: **the observer is part of the system**. Sample
first. Narrow to specific events second. Never enable exhaustive event
recording on a production path without a measured overhead number.

## What perf needs from a Foundation binary

A profiler turns an instruction-pointer address into a function name using the
binary's symbol table, and turns a single address into a stack using frame
pointers or DWARF unwind data. Both are build-time decisions. Get them wrong
and the profile is a column of hex addresses that no amount of load testing
will improve.

### Symbols

Foundation's shipped build paths strip symbols:

- `templates/docker/Dockerfile` — `-ldflags="-s -w"`
- `templates/Makefile.cicd` — `-ldflags="-s -w"`
- `templates/github/workflows/ci.yml` — `-ldflags="-s -w"`
- `templates/Makefile` (Go/WASM target) — `-ldflags="-s -w"`

That is the correct default for an image you ship and the wrong default for an
image you profile. `-s` drops the symbol table; `-w` drops DWARF. A stripped
Go binary still profiles under pprof (Go carries its own runtime metadata), but
`perf`, `perf annotate`, flamegraphs, and any external symbolizer lose the
function names.

The practice is not "stop stripping". It is: **every stripping site must have a
named unstripped counterpart for the same build SHA**. Either build the
profiling image without `-ldflags="-s -w"`, or keep the unstripped binary as a
build artifact so a `perf.data` captured against the shipped image can be
symbolized after the fact. `tooling/profiling_symbol_sites.tsv` records every
known stripping site and its symbolization path; `PERFLAB-05` fails when a new
site appears without one.

Generated projects get the symbolizable counterpart as a target:

```bash
make build-profiling
```

It builds `bin/server-profiling` without `-ldflags="-s -w"` and prints the
Rust profiling invocation. Build it from the same commit as the image you are
profiling, or the symbols describe a different binary.

### Stack unwinding

`-g` collects stack traces, and it needs one of two things:

- **Frame pointers.** Go keeps them on amd64 and arm64, so `perf record -g` on
  a Foundation Go service works out of the box.
- **DWARF.** `--call-graph dwarf` unwinds without frame pointers, at a higher
  capture cost and a larger `perf.data`.

Rust release builds omit frame pointers by default. A profiling build of
`runtime-native` or `runtime-sdk/rust` should set
`RUSTFLAGS="-C force-frame-pointers=yes -C debuginfo=1"` rather than paying
DWARF unwind cost on every sample.

### JIT and interpreted lanes

perf resolves symbols from a symbol table, which a JIT does not have. Node
bridges the gap by writing `/tmp/perf-$PID.map`:

```bash
node --perf-basic-prof scripts/whatever.mjs
```

That covers Foundation's Node-side tooling. It does **not** cover browser work
or the Go/WASM bundle: `main.wasm` runs inside a JS engine, so perf sees the
engine, not the module. Browser and WASM performance claims belong to
`frontend_runtime_workbench.md` and the workbench profile lane, not to perf.

### Kernel and platform matching

- perf is versioned with the kernel (`linux-tools-$(uname -r)`), and its flags
  move between versions. A profiling runbook must record the kernel version.
- Inside a container you are profiling the **host** kernel. Container kernel
  frames are host kernel frames; the isolation is in namespaces, not in the
  scheduler.
- macOS has no perf. Darwin development boxes use `xctrace`, Instruments, or
  `sample`. `tooling/scripts/performance_check.sh` already reflects this: its
  `PERF_COUNTERS=1` lane runs `perf stat` when perf exists and prints an
  explicit skip otherwise. **A macOS capture is never evidence for a Linux
  claim** — record the platform or the number is not usable.

## Reading a profile without being fooled

1. **Attribution is not causation.** "100% of samples in `run_awesome_function`"
   says where the cycles went, not why there are so many of them. The
   next question is always the cumulative-work counter from
   `performance_lab.md`: how many candidates, bytes, rows, or round trips
   produced those cycles.
2. **Kernel frames in your stack trace are normal and informative.** They mean
   your code made a syscall or took a page fault, and the kernel functions name
   what that syscall actually did. The zine's example is exact: `aesni_enc1`
   burning CPU because the filesystem is encrypted — a cost that belongs to the
   storage configuration, not to the program. In Foundation the same reading
   applies to `hermessnapshot`'s zero-copy lane (which of reflink,
   `copy_file_range`, or userspace copy actually ran shows up as kernel
   frames), to the shm doorbell lane, and to WebSocket write paths. **Kernel
   frames are how you prove which rung of the transport ladder executed.**
3. **`perf annotate` is off by one.** Instruction-level attribution has skid.
   Use it to identify the hot *loop*, never to rewrite a specific line.
4. **Dropped events invalidate counts.** perf hands events to userspace through
   a fixed-size ring buffer; when records arrive faster than perf drains them,
   the kernel drops them and warns. A profile with lost events is a sample of a
   sample — fine for "where is it hot", useless as a count. Lower `-F` or
   narrow `-e` and recapture.
5. **Hex addresses are a build problem, not unknown code.** An unresolved
   address means a stripped binary or a missing JIT map. Fix the build and
   recapture rather than reasoning about the address.
6. **An idle profile is still evidence.** This is the existing rule in
   `performance_practices.md`; at the system level it means checking
   context switches, page faults, and syscall counts before concluding a path
   has no bottleneck.

## Practices this lane produces

Profiling mechanics constrain design. These are the coding and optimization
rules that follow from how the tools actually work.

1. **Build for observability; strip for shipping.** Treat the stripped image
   and the symbolized build as two outputs of one build SHA. Symbols are not
   debug bloat, they are the difference between a profile and a hex dump.
2. **Keep function boundaries the profiler can name.** Aggressive inlining,
   giant anonymous closures, and megafunctions collapse a profile into one
   frame. Foundation's low-cardinality, hierarchical marker rule is the same
   rule one level down: hot paths belong in named functions whose names mean
   something in a flamegraph.
3. **Design so cost becomes a countable kernel event.** Batching does not only
   amortize overhead — it converts a diffuse per-item cost into one countable
   syscall, which `perf stat -e 'syscalls:sys_enter_*'` can then verify. A lane
   whose cost cannot be counted cannot be defended.
4. **Use IPC to pick the optimization family before writing code.**
   `perf stat` gives instructions-per-cycle. Low IPC with high cache misses is
   memory-bound: change the data layout (this is exactly the ground the
   columnar/bitmap engine in `columnar_null_algebra.md` occupies). High IPC and
   still slow means you are executing too many instructions: change the
   algorithm. Reaching for the wrong family is the most expensive mistake in
   this lane, and one counter run distinguishes them.
5. **Branch misses are how a masking-versus-branching argument gets settled.**
   The flat-versus-branchy swing recorded in the columnar work is a
   `branch-misses` number, not an opinion. Any claim that predication beats
   branching must carry the counter.
6. **Context switches are a concurrency-design signal.** A high count points at
   channel ping-pong, lock convoys, or handoff-per-item designs. Pair it with
   the pprof block and mutex profiles — and remember those profiles return
   empty until their rates are armed (`server-kit/go/profiling`).
7. **Page-fault counters verify prewarming.** The first-use hitch rule in
   `game_runtime_practices.md` is checkable: prewarm should move faults out of
   the interactive window, and `perf stat -e page-faults` shows whether it did.
8. **Every observer states its overhead.** Sampled before exhaustive,
   `perf trace` before `strace`, a few counters before `-ddd`. Always-on
   tracing in a Foundation service must carry a measured overhead number in its
   runbook.
9. **Every ring buffer Foundation owns exposes a drop counter.** The
   kernel/perf ring buffer is the same shape as the shm doorbell lane and the
   SAB transport: a bounded buffer that sheds under pressure. perf's honesty
   comes from warning about lost events. A Foundation buffer that drops
   silently is strictly worse than one that drops loudly.
10. **Escalate along the cheapest ladder.** `perf top` → `perf stat` →
    `perf record` → `perf trace` → eBPF/bcc. This mirrors the transport ladder:
    pick the lowest-cost tool that answers the question, and record why you had
    to climb.

## Profiling as metadata: what is actually cacheable

Everything above treats a profile as an event — someone runs perf, reads the
output, changes code, throws the capture away. That is the wrong shape for a
system where every unit of work already has a name.

A profile is data *about a named function*. Foundation names its functions
twice and joins them nowhere:

- **At build time** as a Go package and benchmark. `tooling/benchmark_baseline.psv`
  keys cost as `package|benchmark`.
- **At run time** as a contract command. `EventEnvelope.event_type` carries
  `<domain>:<action>:vN:<state>`, alongside `correlation_id` and
  `schema_version`, and `MetadataPreserved` guarantees it survives every lane.

So the raw material for cached, per-function cost metadata is already in place.
What is missing is the key that joins the two naming systems, and the
discipline about which numbers may be cached at all.

### Half of performance is cacheable, and Foundation already knows which half

`tooling/scripts/benchmark_ratchet_check.sh` states the rule in its own header,
and it is the same line the perf toolchain draws:

> allocs/op is an exact per-iteration count of allocation events. It is a
> property of the code, not the machine. … ns/op is machine-dependent and is
> NOT gated.

`perf stat` counters split the same way. Instructions retired, syscall counts,
page faults, and branch counts are close to deterministic per unit of work.
Cycles, wall time, cache-miss *rates*, and p99 depend on the neighbours, the
thermal state, and what was already resident in cache. This is exactly why
perf samples rather than measures.

| Cacheable — a property of the code | Not cacheable — a property of the machine |
| --- | --- |
| allocs/op, bytes/op | ns/op, p50/p95/p99 |
| instructions retired, syscalls per batch | cycles, IPC as an absolute number |
| complexity class, bytes touched per input byte | cache-miss rate under a different neighbour |
| candidates inspected per result, round trips | first-use hitch on a cold page cache |
| the shape of the hot path: which frames, in what nesting | which frame won this particular run |

**Cache the invariant, measure the variable.** A cost profile that records
counts and shapes is a durable fact about a function that only a code change
can invalidate. A cost profile that records nanoseconds is a fact about one
afternoon on one machine, and caching it produces the worst kind of
documentation: a confident number that is quietly wrong.

### The inversion: the function names itself

perf spends its entire architecture on symbolization — instruction pointer, to
stack, to symbol table, to a name — and most of the ways it fails are failures
of that chain: a stripped binary, a JIT with no symbol table, an interpreter
that only ever reports itself.

Foundation does not have that problem at the level that matters. **The command
names itself in metadata and carries the name through every lane by contract.**
Attributing cost to `billing:invoice:v1` needs no symbol table, no frame
pointers, and no DWARF. It needs the envelope that is already there.

This gives the two profiles complementary jobs, joined by `correlation_id`:

- perf answers *which machine function* burned the cycles — and loses that
  answer when the binary is stripped.
- The envelope answers *which contract function* asked for the work — and
  survives stripping, JIT, WASM, and the FFI boundary intact.

Neither is sufficient. `runtime.mapassign` with no caller is not actionable,
and "the invoice command got slower" is not actionable either. The pair is.
This also softens `PERFLAB-05` in a useful direction: a stripped production
image still yields command-level attribution, so the registry exists to
recover the machine half, not to make profiling possible at all.

### Three homes, one identity

The temptation, once cost is metadata, is to put cost *in the envelope*. Do
not. The envelope is per-request, hot, tenant-visible, and cardinality-
sensitive, and measured cost violates all four properties at once: it charges
every request for measurement, it explodes cardinality against Foundation's own
low-cardinality rule, and it hands a tenant a timing side channel.

Cost metadata has three homes, and only the first is on the request path:

| Home | Holds | Lifetime |
| --- | --- | --- |
| `EventEnvelope` metadata | the *identity* — `event_type`, `correlation_id`, `schema_version` — and at most a low-cardinality cost class (interactive / bounded / batch) | per request |
| Contract manifest (`docs/references/lifecycle/lifecycle_contract.json`) | *declared* cost: deadline, queue bound, retry cap, allocation budget, complexity class, replay-safety | per build, generated, deterministic |
| Benchmark ledger and baseline | *measured* cost: counts under the ratchet, timings as drift | per merge, re-measured |

Declared cost lives in the contract. Measured cost lives in the ledger. The
envelope carries only the key that joins them. A cached cost profile is a claim
scoped to `(contract id, code SHA, input class, platform)` — change any of the
four and it is stale, which is why the ratchet re-measures on every merge
rather than trusting the baseline indefinitely. **Cost metadata that does not
carry its key is not metadata, it is folklore.**

### What a named cost profile buys

Once a contract function carries a cached cost descriptor, it stops being
bookkeeping and starts making decisions:

1. Admission control and shedding by declared budget, not queue depth alone.
2. Batching and coalescing where the descriptor says the work is linear in
   items and replay-safe — the same predicate `foundation_nervous_system.md`
   already uses to decide dedupe eligibility for read commands. Cache policy
   and cost metadata are the same metadata.
3. Prewarm lists derived from first-use cost rather than guessed.
4. Transport-ladder rung selection with the cost of each rung declared.
5. Agents reading a declared cost class before proposing an optimization,
   instead of inferring intent from the code.

The failure mode to design against is Goodhart: the moment the runtime routes
on declared cost, under-declaring becomes an incentive. Declared budgets must
stay gated against measured evidence, which is the shape the allocation ratchet
already enforces — the declaration is checked, not trusted.

## Runbooks

### Go service or benchmark under load (Linux)

```bash
perf top -F 49
```

```bash
perf record -F 99 -g -p "$PID" -- sleep 10
```

The trailing `sleep` is the bounded-capture idiom: profile an existing PID for
a fixed window instead of waiting on Ctrl-C. Then:

```bash
perf report --stdio
```

```bash
perf script | stackcollapse-perf.pl | flamegraph.pl > /tmp/foundation-flame.svg
```

Counter triage on a benchmark, which is what `PERF_COUNTERS=1` already runs
inside `tooling/scripts/performance_check.sh`:

```bash
perf stat -e cycles,instructions,cache-references,cache-misses,branches,branch-misses,page-faults,context-switches -- go test -bench=. -benchmem -run='^$' ./appbench
```

Syscall attribution for a transport or storage claim:

```bash
perf trace -p "$PID"
```

### Rust native unit

```bash
RUSTFLAGS="-C force-frame-pointers=yes -C debuginfo=1" cargo build --release
```

```bash
perf record -F 99 -g -- ./target/release/<unit> --bench
```

### Node-side tooling

```bash
node --perf-basic-prof scripts/whatever.mjs
```

```bash
perf record -F 99 -g -p "$PID" -- sleep 10
```

### macOS fallback

```bash
sample "$PID" 10 -file /tmp/foundation-sample.txt
```

Instruments or `xctrace` for counters. Record the platform on the artifact and
do not carry the number into a Linux claim.

## Controls

| Control | Rule | Evidence |
| --- | --- | --- |
| `PERFLAB-05` | Every symbol-stripping build site is registered in `tooling/profiling_symbol_sites.tsv` with a symbolization path. | `profiling-symbol-site` |
| `PERFLAB-06` | perf captures in checked-in scripts and docs are bounded: `perf record`/`perf top` pin a sample frequency, and system-wide captures carry a duration. | `bounded-perf-capture` |
| `PERFLAB-07` | `strace` does not appear in Foundation scripts without an explicit overhead acknowledgement; `perf trace` is the default syscall lane. | `observer-overhead-ack` |
| `PERFLAB-08` | Profiling artifacts (`perf.data`, folded stacks, flamegraph SVGs, pprof dumps) are git-ignored, never committed. | `profile-artifact-ignore` |

Enforced by `tooling/scripts/system_profiling_check.sh` (`make check-system-profiling`).

## Evidence template

A system-profiling claim in a PR or benchmark note carries:

1. Platform: OS, kernel version, container or host, CPU model, core count.
2. Tool and version, plus sample frequency or event list.
3. Build: SHA, stripped or unstripped, frame-pointer flags.
4. Workload: what was running, for how long, at what load.
5. Result: the hot frames or counter values, and the lost-event count if any.
6. The cumulative-work counters from `performance_lab.md` that explain the
   cycles.
7. What changed, and the same capture after the change.
