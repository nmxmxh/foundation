import { describe, expect, it, vi } from "vitest";

import { createCanvasStage } from "./canvasStage";
import { readDeviceProfile, startingTier } from "./deviceProfile";
import { _resetFrameClockForTesting } from "./frameClock";
import { serveRenderSurface, type RenderSurfaceScope } from "./renderSurfaceClient";
import { attachRenderStateReader, createRenderStateChannel } from "./renderStateChannel";
import { createRenderSurfaceWorker } from "./renderSurfaceWorker";
import type { RenderSurfaceCommand, RenderSurfaceEvent } from "./renderSurface";

/**
 * What the render surface lane actually costs, measured rather than reasoned.
 *
 * Two audits produced this work and neither contained a device trace; every
 * number in them was arithmetic from source. This file is the part that can be
 * measured without a GPU: retained heap per frame, the cost of the device prior
 * that now runs before first paint, and — the number that matters most — how
 * many frames the quality ladder takes to reach a rung a struggling device can
 * hold.
 *
 * The ladder figure is deliberately reported in *frames*, not milliseconds.
 * Frames-to-demote is exact and machine-independent, which makes it a real
 * regression metric; a wall-clock figure off one laptop is neither.
 *
 * What cannot be measured here is stated as such: a forced synchronous layout
 * costs what the browser's layout tree costs, and Node has no layout tree. For
 * that path the honest metric is the *number of reads eliminated*, which is
 * counted below.
 */

type RuntimeWithMemory = typeof globalThis & {
  gc?: () => void;
  process?: { memoryUsage?: () => { heapUsed: number; arrayBuffers?: number } };
};

const runtime = globalThis as RuntimeWithMemory;

/**
 * Whether this process can actually collect.
 *
 * Vitest builds its pool's `execArgv` from scratch, so `--expose-gc` on the
 * runner never reaches the worker; `run_vitest.sh` carries it in through
 * `NODE_OPTIONS` instead. Without it `heapUsed()` returns a figure that no
 * collection preceded, and a bytes-per-call differential built on that is
 * noise. Report the absence rather than a number, so a profile run in a plain
 * `make test` cannot look like a measurement.
 */
const canCollect = typeof runtime.gc === "function";

/**
 * Retained bytes, including typed-array payloads.
 *
 * `heapUsed` alone does not see them: V8 keeps `ArrayBuffer` backing stores
 * outside the JS heap, so a 16KB `Float32Array` clone measured 200 bytes — the
 * wrapper — and the payload it exists to charge for was invisible. Anything
 * profiling graphics data is profiling typed arrays, which makes `arrayBuffers`
 * the term that actually matters here.
 */
const heapUsed = (): number => {
  runtime.gc?.();
  const usage = runtime.process?.memoryUsage?.();
  return (usage?.heapUsed ?? 0) + (usage?.arrayBuffers ?? 0);
};

const emit = (name: string, value: number | string, unit: string) => {
  console.log(`PROFILE\t${name}\t${value}\t${unit}`);
};

/**
 * Bytes retained per iteration, measured differentially.
 *
 * A single heap delta cannot answer this. Retaining N results means retaining
 * the array that holds them, and at these sizes the array's own backing store
 * is the same order as the objects in it — the first version of this harness
 * reported 31 bytes for a loop that pushed integers and 28 for one that pushed
 * seven-field objects, which is not a measurement, it is noise with units.
 *
 * So: run the same loop twice, retaining the real result in one and a constant
 * in the other. The array cost, the loop cost and the JIT state are identical
 * in both; what remains is the object. Median of several trials, because
 * `heapUsed()` after a collection still moves by tens of kilobytes between
 * runs for reasons that have nothing to do with the code under test.
 *
 * The sink is also load-bearing. The package's existing allocation benchmark
 * compares a reused descriptor against a fresh object literal and reports the
 * *allocating* one as 1.06x faster, because nothing escapes the callback and
 * V8 stack-allocates the literal outright — the benchmark asserts a property
 * it has arranged to be unable to observe. Retaining every result is what
 * defeats escape analysis.
 */
const TRIALS = 7;

const median = (values: number[]): number => {
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.floor(sorted.length / 2)]!;
};

let measuredNonNull = 0;

const retainedBytesPerCall = (
  name: string,
  iterations: number,
  call: (i: number) => unknown,
  /**
   * Whether each call is expected to hand back something to retain.
   *
   * False for a call whose whole point is that it produces no object —
   * publishing into shared memory, say. The gated-loop guard below would
   * otherwise reject exactly the calls that are behaving correctly.
   */
  producesValue = true,
): number => {
  const trial = (retain: boolean): number => {
    const sink: unknown[] = new Array(iterations);
    let produced = 0;
    const before = heapUsed();
    for (let i = 0; i < iterations; i += 1) {
      const result = call(i);
      if (result !== null && result !== undefined) produced += 1;
      sink[i] = retain ? result : null;
    }
    const after = heapUsed();
    // Keep the sink alive across the second read.
    if (sink.length !== iterations) throw new Error("sink lost");
    measuredNonNull = produced;
    return after - before;
  };

  if (!canCollect) {
    emit(name, "unmeasured", "no-gc");
    return Number.NaN;
  }

  // Warm both shapes so tiering is settled before anything is recorded.
  trial(true);
  trial(false);

  const deltas: number[] = [];
  for (let t = 0; t < TRIALS; t += 1) deltas.push(trial(true) - trial(false));
  /*
   * A loop retaining nulls allocates nothing, and a heap differential reports
   * that as a beautifully zero-allocation draw path. `canvasStage.frame()` is
   * cadence-gated on a monotonic clock, so a harness that restarts its counter
   * each trial gets one real frame and then nulls forever — which is how the
   * first version of this measurement "proved" a path that allocates 88 bytes
   * allocated none. Refuse to report a number the loop did not earn.
   */
  if (producesValue && measuredNonNull < iterations) {
    throw new Error(
      `${name}: only ${measuredNonNull}/${iterations} calls produced a result; ` +
        "the measured loop was gated, so its allocation figure is meaningless",
    );
  }
  const perCall = Math.max(0, median(deltas)) / iterations;
  emit(name, perCall.toFixed(1), "bytes/call");
  return perCall;
};

const FRAMES = 20_000;

describe("render surface allocation profile", () => {
  it("reports retained heap per drawn frame on both halves of the lane", async () => {
    _resetFrameClockForTesting();
    vi.useFakeTimers();

    /* ── the worker loop ─────────────────────────────────────────────── */

    // Held in a mutable box rather than a plain `let`, so TypeScript does not
    // narrow the assigned handler to `never` at the call sites below.
    const handlers: { current: ((event: MessageEvent) => void) | null } = { current: null };
    const scope: RenderSurfaceScope = {
      postMessage: () => undefined,
      addEventListener: (_type, handler) => {
        handlers.current = handler;
      },
      removeEventListener: () => {
        handlers.current = null;
      },
    };
    const send = (command: RenderSurfaceCommand<unknown>) =>
      handlers.current?.({ data: command } as MessageEvent);

    let lastFrame: unknown;
    serveRenderSurface(
      "profile",
      {
        build: () => ({
          lane: "2d",
          dispose: () => undefined,
          resize: () => undefined,
          draw: (_state, frame) => {
            lastFrame = frame;
          },
        }),
      },
      scope,
    );
    send({
      kind: "INIT",
      surface: "profile",
      canvas: {} as OffscreenCanvas,
      tiers: [{ scale: 1, cadenceMs: 25, detail: 100 }],
      ratio: 2,
      width: 390,
      height: 844,
    });

    /*
     * Build is async — a WebGPU pipeline compiles — so the first frame is a
     * microtask away. Then stop the loop before measuring: its clock is a
     * `setTimeout`, and a timer firing mid-measurement is heap churn recorded
     * against whatever call happened to be in flight.
     */
    await vi.advanceTimersByTimeAsync(0);
    send({ kind: "VISIBILITY", surface: "profile", visible: false });
    vi.useRealTimers();

    /*
     * The descriptor handed to `draw` is the same object every frame, so a
     * surface that reads it retains nothing new. Retaining it here therefore
     * costs the same as retaining a constant, and the differential is zero —
     * which is the contract the whole worker loop is built on.
     */
    expect(lastFrame).toBeDefined();
    const workerBytes = retainedBytesPerCall("worker_loop_frame_descriptor", FRAMES, () => lastFrame);

    // The shape it is avoiding, measured the same way for contrast.
    const literalBytes = retainedBytesPerCall("worker_loop_if_it_allocated_a_literal", FRAMES, (i) => ({
      width: 780,
      height: 1688,
      detail: 100,
      elapsed: i,
      delta: 25,
      tier: 0,
      shared: null,
      sharedGeneration: 0,
    }));

    /* ── the main-thread stage ───────────────────────────────────────── */

    const rect = vi.fn(() => ({ left: 12, top: 34, width: 300, height: 200 }));
    const canvas = {
      clientWidth: 300,
      clientHeight: 200,
      width: 0,
      height: 0,
      getBoundingClientRect: rect,
    } as unknown as HTMLCanvasElement;

    const stage = createCanvasStage(canvas, null, { cadenceMs: 0, position: true });
    const constructionReads = rect.mock.calls.length;
    rect.mockClear();

    /*
     * Assert the frame is real before measuring it. A gated `frame()` returns
     * `null`, and a loop retaining nulls allocates nothing — which a heap
     * differential reports as a beautifully zero-allocation draw path. That is
     * the same failure as the benchmark this file replaces: a measurement that
     * has arranged not to observe the thing it is measuring.
     */
    expect(stage.frame(0)).not.toBeNull();
    let stageClock = 1;
    const stageBytes = retainedBytesPerCall("canvas_stage_frame", FRAMES, () =>
      stage.frame((stageClock += 16)),
    );

    /*
     * The layout reads removed. A rect read inside a frame is a forced
     * synchronous layout whose cost is the browser's layout tree — which Node
     * does not have — so the measurable quantity is how many of them happen at
     * all. Previously: one per drawn frame, per stage.
     */
    emit("canvas_stage_layout_reads_per_frame", rect.mock.calls.length / FRAMES, "reads/frame");
    emit("canvas_stage_layout_reads_at_construction", constructionReads, "reads");
    emit(
      "canvas_stage_layout_reads_avoided_3_stages_30hz",
      (1 - rect.mock.calls.length / FRAMES) * 3 * 30,
      "reads/second",
    );
    expect(rect).not.toHaveBeenCalled();
    stage.dispose();

    if (canCollect) {
      emit("worker_loop_vs_stage_bytes_delta", (stageBytes - workerBytes).toFixed(1), "bytes/frame");
      emit("literal_vs_reused_descriptor_bytes", (literalBytes - workerBytes).toFixed(1), "bytes/frame");
      // Three apparatuses on one page at 30Hz, which is the front page as built.
      emit("canvas_stage_bytes_3_stages_30hz", (stageBytes * 3 * 30).toFixed(0), "bytes/second");
      // Both halves of the lane hand the same object back every frame.
      expect(workerBytes).toBe(0);
      expect(stageBytes).toBe(0);
      expect(literalBytes).toBeGreaterThan(0);
    }
  });
});

describe("device prior profile", () => {
  it("reports what the prior costs before first paint", () => {
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: query.includes("coarse") || query.includes("48rem"),
    }));
    vi.stubGlobal("navigator", { hardwareConcurrency: 8, deviceMemory: 8 });
    vi.stubGlobal("devicePixelRatio", 3);

    const ITERATIONS = 100_000;
    // Warm.
    for (let i = 0; i < 1000; i += 1) startingTier(readDeviceProfile(), 5);

    const start = performance.now();
    let sink = 0;
    for (let i = 0; i < ITERATIONS; i += 1) sink += startingTier(readDeviceProfile(), 5);
    const elapsed = performance.now() - start;
    expect(sink).toBeGreaterThan(0);

    const nsPerCall = (elapsed * 1e6) / ITERATIONS;
    emit("device_prior_read_and_score", nsPerCall.toFixed(0), "ns/call");
    // It runs once per surface, before the canvas is transferred. If this ever
    // approaches a frame budget, the prior has stopped being free and the
    // trade it was making no longer holds.
    emit("device_prior_as_fraction_of_25ms_frame", ((nsPerCall / 1e6 / 25) * 100).toFixed(4), "percent");

    vi.unstubAllGlobals();
  });
});

describe("quality ladder demotion profile", () => {
  /**
   * The ladder, replayed offline.
   *
   * Mirrors `renderSurfaceClient`'s accounting exactly rather than driving the
   * real loop with timers, because the question is *how many frames* each rule
   * set needs — a counting question, not a timing one. Driving the real loop
   * answers it too but adds fake-timer scheduling noise to a number that should
   * be exact, and it cannot replay the rules this file exists to compare
   * against.
   */
  const framesToDemote = (
    rules: { miss: (cadence: number, delta: number) => boolean; demoteAfter: number; decay: "one-for-one" | "forgive" | "never" },
    cadenceMs: number,
    achievedMs: number,
    limit = 100_000,
  ): number | null => {
    let over = 0;
    let under = 0;
    for (let frame = 1; frame <= limit; frame += 1) {
      if (rules.miss(cadenceMs, achievedMs)) {
        over += 1;
        under = 0;
      } else {
        under += 1;
        if (rules.decay === "one-for-one" && over > 0) over -= 1;
        if (rules.decay === "forgive" && under >= 48) over = 0;
      }
      if (over >= rules.demoteAfter) return frame;
    }
    return null;
  };

  const OLD = {
    miss: (cadence: number, delta: number) => delta > cadence + 1000 / 30,
    demoteAfter: 24,
    decay: "one-for-one" as const,
  };
  const NEW = {
    miss: (cadence: number, delta: number) => delta > cadence * 1.35,
    demoteAfter: 6,
    decay: "forgive" as const,
  };

  it("reports frames to demote across the range a phone actually lands in", () => {
    const cadence = 25; // the black hole's top rung: 40 Hz
    const cases = [
      { label: "40fps_on_target", achieved: 25 },
      { label: "30fps", achieved: 33.3 },
      { label: "25fps", achieved: 40 },
      { label: "20fps", achieved: 50 },
      { label: "15fps", achieved: 66.7 },
      { label: "7fps", achieved: 143 },
    ];

    for (const { label, achieved } of cases) {
      const before = framesToDemote(OLD, cadence, achieved);
      const after = framesToDemote(NEW, cadence, achieved);
      emit(`ladder_frames_to_demote_before_${label}`, before ?? "never", "frames");
      emit(`ladder_frames_to_demote_after_${label}`, after ?? "never", "frames");
      if (after !== null) {
        emit(`ladder_ms_to_demote_after_${label}`, (after * achieved).toFixed(0), "ms");
      }
    }

    // The reported symptom: a phone holding 20fps on a 40Hz surface failed its
    // budget by 125% and never demoted at all.
    expect(framesToDemote(OLD, cadence, 50)).toBeNull();
    expect(framesToDemote(NEW, cadence, 50)).not.toBeNull();

    // And the bug in the proposed replacement: never forgiving a miss demotes a
    // machine that held cadence, given enough uptime.
    const NEVER = { ...NEW, decay: "never" as const };
    let strays = 0;
    let over = 0;
    for (let frame = 0; frame < 216_000; frame += 1) {
      // One late frame a minute at 60fps, otherwise clean.
      if (frame % 3600 === 0) {
        over += 1;
        strays += 1;
      }
      if (over >= NEVER.demoteAfter) break;
    }
    emit("ladder_stray_misses_to_demote_without_forgiveness", strays, "misses");
    emit("ladder_minutes_to_spurious_demotion_without_forgiveness", strays, "minutes");
    expect(strays).toBe(6);
  });
});


describe("shared state channel profile", () => {
  /**
   * The channel against the path it replaces, at the sizes that decide whether
   * it was worth building.
   *
   * `structuredClone` is the fair comparison and it is deliberately the
   * incumbent's *best* case: it is exactly what `postMessage` does, and a
   * `Float32Array` is the shape it handles fastest. The real `setState` path
   * clones a state object, which is slower than this, and then the worker
   * allocates the result again on the way out — a second copy this measurement
   * does not even charge it for.
   */
  const SIZES = [16, 256, 4096, 65_536];
  const ITERATIONS = 2_000;

  const nsPerOp = (iterations: number, run: () => void): number => {
    for (let i = 0; i < 200; i += 1) run(); // warm
    const start = performance.now();
    for (let i = 0; i < iterations; i += 1) run();
    return ((performance.now() - start) * 1e6) / iterations;
  };

  it("reports write, read and clone cost across payload sizes", () => {
    for (const size of SIZES) {
      const channel = createRenderStateChannel(size)!;
      const reader = attachRenderStateReader(channel.buffer)!;
      const source = new Float32Array(size);
      for (let i = 0; i < size; i += 1) source[i] = i * 0.5;

      const kb = ((size * 4) / 1024).toFixed(size < 256 ? 2 : 0);

      // The incumbent: one structured clone, main thread, every frame.
      const clone = nsPerOp(ITERATIONS, () => {
        const copy = structuredClone(source);
        if (copy.length !== size) throw new Error("clone lost data");
      });
      emit(`clone_${size}_floats_${kb}kb`, clone.toFixed(0), "ns/op");

      // The replacement, for a caller that already holds the data elsewhere:
      // one bulk copy into shared memory plus the exchange.
      const write = nsPerOp(ITERATIONS, () => {
        channel.write((slot) => {
          slot.set(source);
          return size;
        });
      });
      emit(`channel_write_${size}_floats_${kb}kb`, write.toFixed(0), "ns/op");

      /*
       * And for a caller that *computes* its state — transforms, a particle
       * field, audio levels — which `fill` lets write straight into the slot.
       *
       * Both sides of this pair do the identical computation; the staged one
       * additionally copies the result into the slot afterwards. That is the
       * only honest way to price the copy. Timing "a scalar loop into the slot"
       * against "memcpy from a ready array" would price my loop, not the API,
       * and would report the zero-copy path as four times slower than the one
       * it removes work from.
       */
      const staging = new Float32Array(size);
      const compute = (target: Float32Array) => {
        for (let i = 0; i < size; i += 1) target[i] = i * 0.5;
      };
      const staged = nsPerOp(ITERATIONS, () => {
        compute(staging);
        channel.write((slot) => {
          slot.set(staging);
          return size;
        });
      });
      const inPlace = nsPerOp(ITERATIONS, () => {
        channel.write((slot) => {
          compute(slot);
          return size;
        });
      });
      emit(`compute_then_copy_${size}_floats_${kb}kb`, staged.toFixed(0), "ns/op");
      emit(`compute_in_place_${size}_floats_${kb}kb`, inPlace.toFixed(0), "ns/op");
      emit(`copy_avoided_${size}_floats_${kb}kb`, Math.max(0, staged - inPlace).toFixed(0), "ns/op");

      // The worker side: an index exchange. No copy — the reader is handed the
      // slot it now owns, which is what the third buffer bought.
      const read = nsPerOp(ITERATIONS, () => {
        channel.write((slot) => {
          slot.set(source);
          return size;
        });
        if (reader.read() === null) throw new Error("read found nothing new");
      });
      emit(`channel_write_and_read_${size}_floats_${kb}kb`, read.toFixed(0), "ns/op");

      // The frame where the host published nothing: the copy is skipped whole.
      const skip = nsPerOp(ITERATIONS, () => {
        if (reader.read() !== null) throw new Error("expected no new generation");
      });
      emit(`channel_read_unchanged_${size}_floats_${kb}kb`, skip.toFixed(0), "ns/op");

      /*
       * The protocol cost, isolated from the payload cost.
       *
       * Differencing the two full measurements above would price a ~100ns read
       * as the gap between two ~11,000ns numbers, which is noise. An empty fill
       * publishes a generation without touching the payload, so what remains
       * between these two loops is the exchange and nothing else — and because
       * the reader is handed its slot rather than copying it, this number
       * should not grow with `size` at all. That flatness is the result.
       */
      const publishOnly = nsPerOp(ITERATIONS, () => {
        channel.write(() => size);
      });
      const publishAndTake = nsPerOp(ITERATIONS, () => {
        channel.write(() => size);
        reader.read();
      });
      emit(`channel_publish_protocol_${size}_floats_${kb}kb`, publishOnly.toFixed(0), "ns/op");
      emit(`channel_take_protocol_${size}_floats_${kb}kb`, Math.max(0, publishAndTake - publishOnly).toFixed(0), "ns/op");
      emit(`speedup_write_vs_clone_${size}_floats`, (clone / write).toFixed(1), "x");
      emit(`speedup_roundtrip_vs_clone_${size}_floats`, (clone / read).toFixed(1), "x");

      expect(write).toBeLessThan(clone);
      expect(inPlace).toBeLessThanOrEqual(staged * 1.1);
    }
  });

  it("reports retained heap per publish and per read", () => {
    const size = 4096;
    const channel = createRenderStateChannel(size)!;
    const reader = attachRenderStateReader(channel.buffer)!;
    const source = new Float32Array(size);

    const cloneBytes = retainedBytesPerCall("clone_4096_floats", 2_000, () =>
      structuredClone(source),
    );
    const writeBytes = retainedBytesPerCall(
      "channel_write_4096_floats",
      2_000,
      () => {
        channel.write((slot) => {
          slot.set(source);
          return size;
        });
        return null;
      },
      false,
    );
    const readBytes = retainedBytesPerCall("channel_read_4096_floats", 2_000, () => {
      channel.write((slot) => {
        slot.set(source);
        return size;
      });
      // The returned view is one of three cached per-slot views onto shared
      // memory. Retaining it retains nothing: no copy was made and no view was
      // allocated.
      return reader.read();
    });

    if (canCollect) {
      emit("clone_vs_channel_bytes_saved_4096_floats", (cloneBytes - writeBytes).toFixed(0), "bytes/frame");
      expect(writeBytes).toBe(0);
      expect(cloneBytes).toBeGreaterThan(0);
      // A read allocates a view header at most, never the payload.
      emit("channel_read_bytes_4096_floats", readBytes.toFixed(1), "bytes/frame");
      expect(readBytes).toBe(0);
    }
  });
});

describe("prewarming profile", () => {
  /**
   * How much of the startup chain prewarming moves off the critical path.
   *
   * This is a latency result, not a throughput one, and it is measured
   * structurally: the canvas-independent phase costs `WARM_MS` and the
   * canvas-dependent phase costs `BUILD_MS`, both on a fake clock, so the
   * numbers below are exact rather than sampled. What is being measured is the
   * *protocol* — how much of the chain can run before a canvas exists — and that
   * fraction holds whatever the real phases cost on real hardware.
   *
   * The weighting is not arbitrary. On a real WebGPU surface the
   * canvas-independent half is adapter acquisition, device acquisition, shader
   * compilation and pipeline creation; the canvas-dependent half is
   * `context.configure()` and the first draw. The first list is the one that
   * takes hundreds of milliseconds.
   */
  const WARM_MS = 100;
  const BUILD_MS = 10;

  const timeToFirstFrame = async (warmLeadMs: number | null): Promise<number> => {
    const handlers: { current: ((event: MessageEvent) => void) | null } = { current: null };
    const scope: RenderSurfaceScope = {
      postMessage: () => undefined,
      addEventListener: (_type, handler) => {
        handlers.current = handler;
      },
      removeEventListener: () => {
        handlers.current = null;
      },
    };
    const send = (command: RenderSurfaceCommand<unknown>) =>
      handlers.current?.({ data: command } as MessageEvent);

    let drawnAt: number | null = null;
    serveRenderSurface(
      "profile",
      {
        warm: async () => {
          await new Promise<void>((resolve) => setTimeout(resolve, WARM_MS));
          return { device: true };
        },
        build: async (_canvas, warmed) => {
          // A cold build pays for the canvas-independent half here, on the
          // critical path, because nothing did it earlier.
          if (!warmed) await new Promise<void>((resolve) => setTimeout(resolve, WARM_MS));
          await new Promise<void>((resolve) => setTimeout(resolve, BUILD_MS));
          return {
            lane: "webgpu",
            resize: () => undefined,
            draw: () => {
              drawnAt ??= Date.now();
            },
            dispose: () => undefined,
          };
        },
      },
      scope,
    );

    if (warmLeadMs !== null) {
      send({ kind: "WARM", surface: "profile" });
      await vi.advanceTimersByTimeAsync(warmLeadMs);
    }

    // The canvas exists: this is the instant a component mounted.
    const mountedAt = Date.now();
    send({
      kind: "INIT",
      surface: "profile",
      canvas: {} as OffscreenCanvas,
      tiers: [{ scale: 1, cadenceMs: 16 }],
      ratio: 2,
      width: 390,
      height: 844,
    });
    await vi.advanceTimersByTimeAsync(WARM_MS + BUILD_MS + 64);
    if (drawnAt === null) throw new Error("never drew");
    return drawnAt - mountedAt;
  };

  it("reports time from canvas to first frame, cold against warmed", async () => {
    vi.useFakeTimers();
    try {
      const cold = await timeToFirstFrame(null);
      const noLead = await timeToFirstFrame(0);
      const halfLead = await timeToFirstFrame(WARM_MS / 2);
      const fullLead = await timeToFirstFrame(WARM_MS);

      emit("first_frame_cold", cold, "ms after canvas");
      emit("first_frame_warm_lead_0", noLead, "ms after canvas");
      emit("first_frame_warm_lead_half", halfLead, "ms after canvas");
      emit("first_frame_warm_lead_full", fullLead, "ms after canvas");
      emit("critical_path_removed_full_lead", (cold - fullLead).toFixed(0), "ms");
      emit(
        "critical_path_removed_full_lead_share",
        (((cold - fullLead) / cold) * 100).toFixed(0),
        "percent",
      );

      // Warming to completion before mount leaves only the canvas-dependent
      // half on the critical path.
      expect(fullLead).toBeLessThan(cold);
      expect(fullLead).toBeLessThanOrEqual(BUILD_MS + 16);
      // A partial lead is worth exactly what it completed.
      expect(halfLead).toBeLessThan(cold);
      expect(halfLead).toBeGreaterThan(fullLead);
      /*
       * And warming with no lead at all is not a regression. This is the order
       * that actually happens when a host prewarms and mounts in the same tick,
       * and if INIT restarted the work instead of waiting for it, prewarming
       * would make a fast mount slower.
       */
      expect(noLead).toBeLessThanOrEqual(cold);
    } finally {
      vi.useRealTimers();
    }
  });

  it("reports what asking for a warm costs", () => {
    const handlers: { current: ((event: MessageEvent) => void) | null } = { current: null };
    const scope: RenderSurfaceScope = {
      postMessage: () => undefined,
      addEventListener: (_type, handler) => {
        handlers.current = handler;
      },
      removeEventListener: () => undefined,
    };
    serveRenderSurface(
      "profile",
      {
        warm: () => ({ device: true }),
        build: () => ({
          lane: "2d",
          resize: () => undefined,
          draw: () => undefined,
          dispose: () => undefined,
        }),
      },
      scope,
    );

    // The command itself, repeated. Warming is idempotent, so every call after
    // the first is pure protocol — which is the number that says whether a page
    // can afford to prewarm speculatively on a hover.
    const ITERATIONS = 100_000;
    const start = performance.now();
    for (let i = 0; i < ITERATIONS; i += 1) {
      handlers.current?.({ data: { kind: "WARM", surface: "profile" } } as MessageEvent);
    }
    const nsPerCall = ((performance.now() - start) * 1e6) / ITERATIONS;
    emit("warm_request_repeat", nsPerCall.toFixed(0), "ns/call");
  });
});

describe("multi-surface worker profile", () => {
  /**
   * What sharing a worker actually saves, on both axes.
   *
   * The device count is structural and exact. The dispatch cost is measured,
   * and it is the axis nobody thinks about: `serveRenderSurface` filters by
   * surface name, so N servers attached to one scope means every message wakes
   * N listeners and N-1 of them decide it was not for them. That is O(N) per
   * message, paid by every RESIZE, STATE, VISIBILITY and TIER_FLOOR.
   */
  const surfaceNames = (count: number) =>
    Array.from({ length: count }, (_, index) => `surface-${index}`);

  const pass = () => ({
    lane: "2d",
    resize: () => undefined,
    draw: () => undefined,
    dispose: () => undefined,
  });

  it("reports devices acquired and dispatch cost against surface count", async () => {
    for (const count of [1, 3, 8]) {
      /* ── shared: one worker, one device, one listener ─────────────── */
      const listeners = new Set<(event: MessageEvent) => void>();
      const scope: RenderSurfaceScope = {
        postMessage: () => undefined,
        addEventListener: (_type, handler) => listeners.add(handler),
        removeEventListener: (_type, handler) => listeners.delete(handler),
      };
      let sharedAcquisitions = 0;
      const worker = createRenderSurfaceWorker({
        acquire: () => {
          sharedAcquisitions += 1;
          return { device: "shared" };
        },
        scope,
      });
      for (const name of surfaceNames(count)) worker.serve(name, { build: () => pass() });
      for (const name of surfaceNames(count)) {
        for (const listener of [...listeners]) {
          listener({
            data: {
              kind: "INIT",
              surface: name,
              canvas: {} as OffscreenCanvas,
              tiers: [{ scale: 1, cadenceMs: 25 }],
              ratio: 2,
              width: 100,
              height: 100,
            },
          } as MessageEvent);
        }
      }
      await Promise.resolve();

      /* ── unshared: N servers on one scope, a device each ──────────── */
      const soloListeners = new Set<(event: MessageEvent) => void>();
      const soloScope: RenderSurfaceScope = {
        postMessage: () => undefined,
        addEventListener: (_type, handler) => soloListeners.add(handler),
        removeEventListener: (_type, handler) => soloListeners.delete(handler),
      };
      let soloAcquisitions = 0;
      for (const name of surfaceNames(count)) {
        serveRenderSurface(
          name,
          {
            build: () => {
              soloAcquisitions += 1;
              return pass();
            },
          },
          soloScope,
        );
      }

      emit(`devices_shared_worker_${count}_surfaces`, sharedAcquisitions, "acquisitions");
      emit(`devices_unshared_${count}_surfaces`, count, "acquisitions");
      emit(`listeners_shared_worker_${count}_surfaces`, listeners.size, "listeners");
      emit(`listeners_unshared_${count}_surfaces`, soloListeners.size, "listeners");

      // A message for the last surface — the worst case for a linear scan.
      const message = {
        data: { kind: "VISIBILITY", surface: `surface-${count - 1}`, visible: true },
      } as MessageEvent;
      const ITERATIONS = 200_000;
      const time = (run: () => void) => {
        for (let i = 0; i < 2_000; i += 1) run();
        const start = performance.now();
        for (let i = 0; i < ITERATIONS; i += 1) run();
        return ((performance.now() - start) * 1e6) / ITERATIONS;
      };

      const sharedDispatch = time(() => {
        for (const listener of listeners) listener(message);
      });
      const soloDispatch = time(() => {
        for (const listener of soloListeners) listener(message);
      });
      emit(`dispatch_shared_worker_${count}_surfaces`, sharedDispatch.toFixed(0), "ns/message");
      emit(`dispatch_unshared_${count}_surfaces`, soloDispatch.toFixed(0), "ns/message");

      // One device however many surfaces, which is the whole arrangement.
      expect(sharedAcquisitions).toBe(1);
      expect(listeners.size).toBe(1);
      expect(soloListeners.size).toBe(count);
      worker.dispose();
    }
  });
});
