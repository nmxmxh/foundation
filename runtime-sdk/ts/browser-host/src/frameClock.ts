/**
 * Unified main-thread animation clock driven by the Foundation pulse.
 *
 * Consolidates multiple independent requestAnimationFrame loops into a single
 * pulse-backed frame scheduler. Executes subscribers within a single browser
 * animation frame while honoring per-subscriber cadence targets.
 */

import { BUFFER_TOTAL_BYTES, IDX_RUNTIME_TICK } from "./generated/runtimeBuffer";
import { createPulseManager, type PulseManager } from "./pulse/pulseManager";
import { PASS, markLane } from "./renderMarks";

export interface Tick {
  /** Timestamp in milliseconds (from performance.now()). */
  now: number;
  /** Elapsed time since the subscriber last ran. */
  delta: number;
}

export interface FrameSubscription {
  /** Adjust the cadence interval in milliseconds. */
  setCadence: (ms: number) => void;
  /** Cancel the subscription. Idempotent. */
  release: () => void;
}

export interface FrameClockOptions {
  /** Custom worker factory for pulse generation. */
  createWorker?: () => Worker;
  /** Target ticks per second. Defaults to 60. */
  targetTPS?: number;
}

interface Subscriber {
  run: (tick: Tick) => void;
  cadenceMs: number;
  lastRunAt: number;
}

const DEFAULT_TARGET_TPS = 60;
const subscribers = new Set<Subscriber>();

let pulse: PulseManager | null = null;
let scheduled = false;
let customWorkerFactory: (() => Worker) | undefined;
let targetTPS = DEFAULT_TARGET_TPS;
let diagnosticsSeen: { mode: string; degraded: boolean } | null = null;
let selfDriven = false;
let workerTicksReceived = 0;

/**
 * Configure global frame clock options before initialization.
 */
export function configureFrameClock(options: FrameClockOptions): void {
  if (options.createWorker) customWorkerFactory = options.createWorker;
  if (options.targetTPS && options.targetTPS > 0) targetTPS = options.targetTPS;
}

/**
 * Initialize or return the pulse manager instance.
 */
function ensurePulse(): PulseManager {
  if (pulse) return pulse;

  const manager = createPulseManager({
    defaultTPS: targetTPS,
    createWorker: customWorkerFactory,
    onDiagnostics: (diagnostics) => {
      diagnosticsSeen = { mode: diagnostics.mode, degraded: diagnostics.degraded };
      // "stopped" is the pulse before start(), not a degraded pulse.
      // ensurePulse() calls watchEpochs() before start(), and watchEpochs()
      // reports diagnostics; reading that "stopped" as a fallback latched every
      // clock onto animation frames at subscription — before its worker could
      // tick — while start()'s report then marked the lane "worker". Only a
      // started pulse says anything about the lane.
      if (diagnostics.mode === "stopped") return;
      if (diagnostics.degraded || diagnostics.mode !== "worker") {
        fallbackToFrames();
      }
      const fallback = diagnostics.degraded || diagnostics.mode !== "worker";
      markLane(PASS.clock, {
        lane: diagnostics.mode,
        cadence: diagnostics.targetTPS,
        fallback,
        reason:
          diagnostics.issues.map((issue) => issue.capability).join(",") ||
          // Every capability is present and the pulse still fell back: the
          // worker itself failed to load, which is the consumer-bundling case
          // pulseManager.ts describes. It must not read as healthy — ovasabi_v1
          // ran on the main-thread lane for its whole life with reason "ok".
          (fallback ? "worker unavailable: pass configureFrameClock({ createWorker })" : "ok"),
      });
    },
  });

  try {
    if (typeof SharedArrayBuffer !== "undefined") {
      const buffer = new SharedArrayBuffer(BUFFER_TOTAL_BYTES);
      manager.watchEpochs([IDX_RUNTIME_TICK], () => {
        workerTicksReceived++;
        handBackToWorker();
        schedule();
      });
      manager.start(buffer);
    } else {
      fallbackToFrames();
      markLane(PASS.clock, { lane: "frames", fallback: true, reason: "no shared memory" });
    }
  } catch {
    fallbackToFrames();
    markLane(PASS.clock, { lane: "frames", fallback: true, reason: "allocation failed" });
  }

  pulse = manager;
  return manager;
}

/**
 * Return the clock to its worker pulse once the worker is actually ticking.
 *
 * A module worker's first tick arrives only after its script has loaded, which
 * can take longer than `onFrame`'s grace window. That window used to latch:
 * once the clock had bridged onto its own animation frames it never went back,
 * so a healthy worker pulse ticked alongside a clock that ignored it — two
 * loops for one pacer — while the clock fact still read "worker". Only a
 * worker-mode, non-degraded pulse is handed back to; a main-thread pulse is a
 * timer, and animation frames pace better than it does.
 */
function handBackToWorker(): void {
  if (!selfDriven || diagnosticsSeen?.mode !== "worker" || diagnosticsSeen.degraded) return;
  selfDriven = false;
  markLane(PASS.clock, {
    lane: "worker",
    cadence: targetTPS,
    fallback: false,
    reason: "worker pulse arrived; frames bridge released",
  });
}

function fallbackToFrames(): void {
  if (selfDriven) return;
  selfDriven = true;
  const loop = (now?: number) => {
    // Released: the worker pulse has taken the clock back.
    if (!selfDriven) return;
    if (subscribers.size === 0) {
      selfDriven = false;
      return;
    }
    const current = typeof now === "number" ? now : (typeof performance !== "undefined" && typeof performance.now === "function" ? performance.now() : Date.now());
    run(current);
    if (typeof requestAnimationFrame === "function") {
      requestAnimationFrame(loop);
    } else {
      setTimeout(() => loop(), 1000 / targetTPS);
    }
  };
  if (typeof requestAnimationFrame === "function") {
    requestAnimationFrame(loop);
  } else {
    setTimeout(() => loop(), 1000 / targetTPS);
  }
}

function schedule(): void {
  if (scheduled) return;
  scheduled = true;
  if (typeof requestAnimationFrame === "function") {
    requestAnimationFrame(run);
  } else {
    setTimeout(() => run(performance.now()), 0);
  }
}

function run(now: number): void {
  scheduled = false;
  if (typeof document !== "undefined" && document.hidden) return;

  for (const subscriber of subscribers) {
    const delta = subscriber.lastRunAt === 0 ? subscriber.cadenceMs : now - subscriber.lastRunAt;
    if (delta < subscriber.cadenceMs - 2.5) continue;
    subscriber.lastRunAt = now;
    try {
      subscriber.run({ now, delta });
    } catch {
      // Individual subscriber errors are isolated so other passes continue uninterrupted.
    }
  }
}

/**
 * Register a callback to execute on scheduled clock frames.
 */
export function onFrame(runCallback: (tick: Tick) => void, cadenceMs = 1000 / 30): FrameSubscription {
  ensurePulse();
  const subscriber: Subscriber = { run: runCallback, cadenceMs, lastRunAt: 0 };
  subscribers.add(subscriber);
  schedule();

  if (selfDriven === false && subscribers.size > 0) {
    if (typeof SharedArrayBuffer === "undefined") {
      fallbackToFrames();
    } else {
      setTimeout(() => {
        if (!selfDriven && subscribers.size > 0 && workerTicksReceived === 0) {
          fallbackToFrames();
          // Honest while bridging: the pulse reported "worker" when it started,
          // but no tick has arrived, so animation frames are driving the clock.
          markLane(PASS.clock, {
            lane: "frames",
            cadence: targetTPS,
            fallback: true,
            reason: "no worker tick within 120 ms; bridging on animation frames",
          });
        }
      }, 120);
    }
  }

  return {
    setCadence(ms: number) {
      subscriber.cadenceMs = ms;
    },
    release() {
      subscribers.delete(subscriber);
    },
  };
}

/**
 * Diagnostic status inspection.
 */
export function frameClockMode(): { mode: string; degraded: boolean; subscribers: number } {
  return {
    mode: selfDriven ? "frames" : (diagnosticsSeen?.mode ?? "stopped"),
    degraded: diagnosticsSeen?.degraded ?? false,
    subscribers: subscribers.size,
  };
}

/**
 * Testing hook to reset frame clock state.
 */
export function _resetFrameClockForTesting(): void {
  pulse?.stop();
  pulse = null;
  scheduled = false;
  selfDriven = false;
  diagnosticsSeen = null;
  workerTicksReceived = 0;
  subscribers.clear();
}
