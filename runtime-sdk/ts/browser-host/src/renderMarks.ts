/**
 * Stable performance markers for render and simulation passes.
 *
 * Implements game_runtime_practices.md section 121:
 * - Stable low-cardinality marker names.
 * - Entropy (facts, measurements, reason) kept strictly in details.
 * - Global window.__ovasabiRender snapshot for inspectability without DevTools profiling.
 */

export const PASS = {
  blackHole: "ovasabi.render.blackHole",
  orbit: "ovasabi.render.orbit",
  paper: "ovasabi.render.paper",
  field: "ovasabi.render.field",
  footer: "ovasabi.render.footer",
  clock: "ovasabi.render.clock",
} as const;

export type PassName = (typeof PASS)[keyof typeof PASS] | (string & {});

export interface LaneFacts {
  /** Selected execution lane: "webgpu", "webgl2", "wasm", "twin", "none", "2d". */
  lane: string;
  /** Active ladder tier, if defined. */
  tier?: number;
  /** Target frame cadence in frames per second. */
  cadence?: number;
  /** Viewport render scale fraction. */
  scale?: number;
  /** Iteration or compute budget per frame. */
  steps?: number;
  /** Flag marking fallback path activation. */
  fallback?: boolean;
  /** Reason for state transition or fallback. */
  reason?: string;
}

const snapshot: Record<string, LaneFacts & { at: number }> = {};

/**
 * Record a pass decision as a performance mark and runtime snapshot.
 */
export function markLane(pass: PassName, facts: LaneFacts): void {
  const at = typeof performance !== "undefined" && typeof performance.now === "function" ? Math.round(performance.now()) : Date.now();
  snapshot[pass] = { ...facts, at };
  try {
    performance.mark?.(pass, { detail: facts });
  } catch {
    // Unsupported options object in older environments is safely ignored.
  }
  if (typeof window !== "undefined") {
    (window as Window & { __ovasabiRender?: typeof snapshot }).__ovasabiRender = snapshot;
  }
}

/**
 * Return all recorded render lane markers.
 */
export function renderFacts(): Readonly<Record<string, LaneFacts & { at: number }>> {
  return snapshot;
}

/**
 * Clear recorded render lane markers (used primarily for test isolation).
 */
export function clearRenderFacts(): void {
  for (const key of Object.keys(snapshot)) {
    delete snapshot[key];
  }
}
