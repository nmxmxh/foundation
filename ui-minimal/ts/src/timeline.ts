import { useLayoutEffect, useRef } from "react";

import { minimalMotionMs } from "./motionStyles";
import { minimalVars } from "./tokens";

/*
 * Scripted motion on the Web Animations API: entry, exit and timelines.
 *
 * CSS (`minimalEnter`, transitions) is still the first choice for anything a
 * state change can express. This is for what it cannot — a sequence, a
 * stagger across a list, an exit that must finish before something unmounts, a
 * scrubbed or reversible choreography — the jobs people reach for GSAP or
 * framer-motion to do.
 *
 * The shape is borrowed from GSAP's timeline, because it is the best API anyone
 * has shipped for this: tweens placed by position (`"<"`, `">"`, `"+=120"`,
 * labels), shared defaults, stagger, seek / progress / reverse / timeScale,
 * repeat and yoyo. The engine is not borrowed. Every tween is a native
 * `Element.animate()` animation, so the browser — not a JavaScript loop — runs
 * the frames, and `transform` / `opacity` / `translate` / `scale` / `rotate`
 * keep running on the compositor through a busy main thread. The timeline's
 * JavaScript runs when you call it and at iteration boundaries, never per
 * frame. Research doc section 14: framer-motion's per-frame style writes were
 * the jank this replaces, so a timeline that wrote style per frame would be the
 * same mistake with a nicer API.
 *
 * How the group stays in sync without a group effect (WAAPI Level 2 has not
 * shipped one): each tween's position is its effect `delay`, so every child's
 * `currentTime` *is* the timeline's time. Seeking sets them all to one number;
 * playing sets one shared `startTime`. A null-target master animation carries
 * the timeline's own duration, so `finished` and repeats come from the browser.
 */

/** A GSAP-style position: ms, `"<"`, `">"`, `"<120"`, `">-80"`, `"+=120"`, `"-=40"`, `"label"`, `"label+=80"`. */
export type MinimalTimelinePosition = number | string;

export type MinimalStagger = number | { each: number; from?: "start" | "end" | "center" };

export interface MinimalTweenOptions {
  /** Milliseconds. Defaults to the timeline's default, then `minimalMotionMs.standard`. */
  duration?: number;
  /** Any CSS easing. Defaults to the theme's entrance curve. */
  easing?: string;
  /** Milliseconds between targets when several are given. */
  stagger?: MinimalStagger;
  iterations?: number;
  direction?: PlaybackDirection;
  composite?: CompositeOperation;
  /**
   * Keep the end state after the timeline finishes (an exit that should stay
   * gone). Implemented by committing the final styles inline and cancelling
   * the animation, so nothing is held by an open-ended fill.
   */
  hold?: boolean;
}

export interface MinimalTimelineOptions {
  defaults?: MinimalTweenOptions;
  /** Build without playing. Call `play()` when ready. */
  paused?: boolean;
  /** Additional plays after the first. `Infinity` loops. */
  repeat?: number;
  /** Alternate direction on each repeat. */
  yoyo?: boolean;
  /**
   * `"honor"` (default): under `prefers-reduced-motion`, the timeline jumps to
   * its end state instead of moving. `"always"` does that unconditionally
   * (tests, a quality tier); `"ignore"` never does — for motion that carries
   * meaning, which should be rare.
   */
  reducedMotion?: "honor" | "always" | "ignore";
}

export interface MinimalTimeline {
  /** Total length of one iteration, in ms. */
  readonly duration: number;
  readonly labels: Readonly<Record<string, number>>;
  /**
   * Properties animated that the compositor cannot run, one entry each. Height,
   * width, colour and friends animate on the main thread, which is what this
   * module exists to avoid; they are allowed and reported, not refused.
   */
  readonly issues: readonly string[];
  /** Resolves when the timeline completes (after all repeats), or when it is cancelled. */
  readonly finished: Promise<void>;
  add(
    targets: MinimalTargets,
    keyframes: Keyframe[] | PropertyIndexedKeyframes,
    options?: MinimalTweenOptions,
    position?: MinimalTimelinePosition,
  ): MinimalTimeline;
  addLabel(name: string, position?: MinimalTimelinePosition): MinimalTimeline;
  /** Run a callback when the playhead passes this point going forward. */
  call(callback: () => void, position?: MinimalTimelinePosition): MinimalTimeline;
  play(): MinimalTimeline;
  pause(): MinimalTimeline;
  /** Flip direction and keep playing from the current time. */
  reverse(): MinimalTimeline;
  /** Jump to a time in ms, or to a label. Keeps the play state. */
  seek(position: number | string): MinimalTimeline;
  /** Jump to a fraction of one iteration (0–1). */
  progress(value: number): MinimalTimeline;
  /** Playback speed; negative plays backwards. */
  timeScale(rate: number): MinimalTimeline;
  /** Current time in ms. */
  time(): number;
  /** Jump to the end state (committing held tweens) and resolve `finished`. */
  finish(): MinimalTimeline;
  /** Stop and remove every animation; elements return to their CSS. */
  cancel(): MinimalTimeline;
}

export type MinimalTargets = Element | null | undefined | ArrayLike<Element | null | undefined>;

/** Keyframes built only from compositor-friendly properties. */
export const minimalKeyframes = {
  fadeIn: [{ opacity: 0 }, { opacity: 1 }],
  fadeOut: [{ opacity: 1 }, { opacity: 0 }],
  popIn: [{ opacity: 0, scale: "0.97" }, { opacity: 1, scale: "1" }],
  popOut: [{ opacity: 1, scale: "1" }, { opacity: 0, scale: "0.97" }],
  slideUpIn: [{ opacity: 0, translate: "0 12px" }, { opacity: 1, translate: "0 0" }],
  slideDownOut: [{ opacity: 1, translate: "0 0" }, { opacity: 0, translate: "0 12px" }],
  sheetIn: [{ translate: "0 100%" }, { translate: "0 0" }],
  sheetOut: [{ translate: "0 0" }, { translate: "0 100%" }],
} satisfies Record<string, Keyframe[]>;

const COMPOSITED = new Set(["opacity", "transform", "translate", "scale", "rotate"]);

/*
 * `Element.animate()` does not accept `var()`, and the theme's motion tokens
 * are `var(--minimal-ease-*, <fallback>)` so that CSS picks up theme overrides.
 * Resolve them the way CSS would: the custom property's computed value on the
 * root element, or the declared fallback when the property is unset.
 */
const VAR_REFERENCE = /^var\(\s*(--[\w-]+)\s*(?:,\s*(.+))?\)$/;
const resolveTokenValue = (value: string): string => {
  const match = VAR_REFERENCE.exec(value.trim());
  if (!match) return value;
  const computed =
    typeof document !== "undefined" && document.documentElement
      ? getComputedStyle(document.documentElement).getPropertyValue(match[1]!).trim()
      : "";
  return computed || (match[2] ? resolveTokenValue(match[2]) : "linear");
};
const TIMING_KEYS = new Set(["offset", "easing", "composite", "computedOffset"]);

const toElements = (targets: MinimalTargets): Element[] => {
  if (!targets) return [];
  if (typeof (targets as Element).animate === "function") return [targets as Element];
  return Array.from(targets as ArrayLike<Element | null | undefined>).filter((t): t is Element => Boolean(t));
};

const staggerOffsets = (count: number, stagger: MinimalStagger | undefined): number[] => {
  const each = typeof stagger === "number" ? stagger : stagger?.each ?? 0;
  const from = typeof stagger === "object" ? stagger.from ?? "start" : "start";
  return Array.from({ length: count }, (_, i) => {
    const rank = from === "end" ? count - 1 - i : from === "center" ? Math.abs(i - (count - 1) / 2) : i;
    return rank * each;
  });
};

const prefersReducedMotion = () =>
  typeof matchMedia === "function" && matchMedia("(prefers-reduced-motion: reduce)").matches;

const propertiesOf = (keyframes: Keyframe[] | PropertyIndexedKeyframes): string[] => {
  const frames = Array.isArray(keyframes) ? keyframes : [keyframes];
  const names = new Set<string>();
  for (const frame of frames) for (const key of Object.keys(frame)) if (!TIMING_KEYS.has(key)) names.add(key);
  return [...names];
};

/**
 * Build a timeline. Tweens added without a position append to the end, like
 * GSAP. Nothing moves until the current task ends (or until `play()`, when
 * `paused`), so a whole sequence can be declared synchronously first.
 */
export const createMinimalTimeline = (options: MinimalTimelineOptions = {}): MinimalTimeline => {
  const canAnimate = typeof document !== "undefined" && typeof Element !== "undefined" && "animate" in Element.prototype;
  const defaults = options.defaults ?? {};
  const animations: Animation[] = [];
  const held: Animation[] = [];
  const callbacks: Animation[] = [];
  const callbackRecords: Array<{ at: number; fired: boolean; run: () => void }> = [];
  const labels: Record<string, number> = {};
  const issues: string[] = [];
  let duration = 0;
  let previousStart = 0;
  let previousEnd = 0;
  let rate = 1;
  let playing = !options.paused;
  let repeatsLeft = options.repeat ?? 0;
  let settled = false;
  let resolveFinished: () => void = () => undefined;
  const finished = new Promise<void>((resolve) => (resolveFinished = resolve));
  let scheduled = false;

  // The master clock: no target, just the timeline's duration and `finished`.
  const master = canAnimate ? new Animation(new KeyframeEffect(null, [], { duration: 0, fill: "both" }), document.timeline) : null;

  const resolvePosition = (position: MinimalTimelinePosition | undefined): number => {
    if (position === undefined) return duration;
    if (typeof position === "number") return Math.max(0, position);
    const text = position.trim();
    let match = /^([<>])\s*([+-]?\d+(?:\.\d+)?)?$/.exec(text);
    if (match) return Math.max(0, (match[1] === "<" ? previousStart : previousEnd) + Number(match[2] ?? 0));
    match = /^([+-])=\s*(\d+(?:\.\d+)?)$/.exec(text);
    if (match) return Math.max(0, duration + (match[1] === "+" ? 1 : -1) * Number(match[2]));
    match = /^([A-Za-z_][\w-]*)\s*(?:([+-])=\s*(\d+(?:\.\d+)?))?$/.exec(text);
    if (match) {
      const base = labels[match[1]!] ?? (labels[match[1]!] = duration);
      return Math.max(0, base + (match[2] ? (match[2] === "+" ? 1 : -1) * Number(match[3]) : 0));
    }
    throw new Error(`minimal timeline: unrecognised position "${position}"`);
  };

  const syncMaster = () => {
    if (!master) return;
    (master.effect as KeyframeEffect).updateTiming({ duration });
  };

  const all = () => (master ? [master, ...animations, ...callbacks] : [...animations, ...callbacks]);

  /** Put every child at `time` and, if playing, start them together from there. */
  const apply = (time: number) => {
    const list = all();
    if (!playing) {
      for (const animation of list) {
        animation.pause();
        animation.currentTime = time;
      }
      return;
    }
    const now = document.timeline.currentTime;
    for (const animation of list) {
      animation.playbackRate = rate;
      if (typeof now === "number") {
        // One shared start time is what keeps the group in lockstep.
        animation.startTime = now - time / rate;
      } else {
        animation.currentTime = time;
        animation.play();
      }
    }
  };

  const commitHeld = () => {
    for (const animation of held) {
      try {
        animation.commitStyles();
      } catch {
        // Detached target: nothing to keep.
      }
      animation.cancel();
    }
    held.length = 0;
  };

  const settle = () => {
    if (settled) return;
    settled = true;
    /*
     * A callback placed at (or near) the very end finishes in the same frame as
     * the master clock, and the browser may dispatch the master's finish first.
     * Settling going forward therefore flushes any callback the playhead has
     * passed but whose event has not arrived, so `await finished` never beats it.
     */
    if (rate > 0) for (const record of callbackRecords) if (!record.fired && record.at <= duration) record.run();
    commitHeld();
    resolveFinished();
  };

  const onMasterFinish = () => {
    if (settled) return;
    if (repeatsLeft > 0) {
      repeatsLeft -= 1;
      if (options.yoyo) {
        rate = -rate;
        apply(rate < 0 ? duration : 0);
      } else {
        apply(rate < 0 ? duration : 0);
      }
      return;
    }
    settle();
  };
  if (master) master.onfinish = onMasterFinish;

  // Start on the next microtask, after the caller has declared the sequence.
  const scheduleStart = () => {
    if (scheduled || !canAnimate) return;
    scheduled = true;
    queueMicrotask(() => {
      const reduce = options.reducedMotion === "always" || (options.reducedMotion !== "ignore" && prefersReducedMotion());
      if (reduce) {
        timeline.finish();
        return;
      }
      if (playing) apply(rate < 0 ? duration : 0);
    });
  };

  const timeline: MinimalTimeline = {
    get duration() {
      return duration;
    },
    labels,
    issues,
    finished,

    add(targets, keyframes, tweenOptions = {}, position) {
      const merged = { ...defaults, ...tweenOptions };
      const elements = toElements(targets);
      const start = resolvePosition(position);
      const tweenDuration = merged.duration ?? minimalMotionMs.standard;
      const offsets = staggerOffsets(elements.length, merged.stagger);
      const iterations = merged.iterations ?? 1;

      for (const property of propertiesOf(keyframes)) {
        if (!COMPOSITED.has(property)) {
          const note = `"${property}" is not a compositor property; it animates on the main thread`;
          if (!issues.includes(note)) issues.push(note);
        }
      }

      let end = start;
      elements.forEach((element, index) => {
        const delay = start + offsets[index]!;
        if (!canAnimate) return;
        const animation = element.animate(keyframes, {
          duration: tweenDuration,
          delay,
          easing: resolveTokenValue(merged.easing ?? minimalVars.motion.easeEntrance),
          iterations,
          direction: merged.direction,
          composite: merged.composite,
          // Backwards holds the first frame through the delay, so staggered and
          // later tweens do not flash their end state before they start.
          fill: merged.hold ? "both" : "backwards",
        });
        animation.pause();
        animations.push(animation);
        if (merged.hold) held.push(animation);
        end = Math.max(end, delay + tweenDuration * (Number.isFinite(iterations) ? iterations : 1));
      });
      if (elements.length === 0) end = start + tweenDuration;

      previousStart = start;
      previousEnd = end;
      duration = Math.max(duration, end);
      syncMaster();
      scheduleStart();
      return timeline;
    },

    addLabel(name, position) {
      labels[name] = resolvePosition(position);
      return timeline;
    },

    call(callback, position) {
      const at = resolvePosition(position);
      const record = {
        at,
        fired: false,
        run: () => {
          if (record.fired) return;
          record.fired = true;
          callback();
        },
      };
      callbackRecords.push(record);
      if (canAnimate) {
        const marker = new Animation(new KeyframeEffect(null, [], { duration: 0, delay: at, fill: "both" }), document.timeline);
        marker.onfinish = () => {
          if (rate > 0) record.run();
        };
        marker.pause();
        callbacks.push(marker);
      }
      duration = Math.max(duration, at);
      syncMaster();
      scheduleStart();
      return timeline;
    },

    play() {
      playing = true;
      const now = timeline.time();
      apply(rate > 0 && now >= duration ? 0 : rate < 0 && now <= 0 ? duration : now);
      return timeline;
    },

    pause() {
      const now = timeline.time();
      playing = false;
      apply(now);
      return timeline;
    },

    reverse() {
      const now = timeline.time();
      rate = -rate;
      playing = true;
      apply(now);
      return timeline;
    },

    seek(position) {
      const target = typeof position === "number" ? position : labels[position];
      if (target === undefined) throw new Error(`minimal timeline: unknown label "${position}"`);
      apply(Math.min(Math.max(target, 0), duration));
      return timeline;
    },

    progress(value) {
      return timeline.seek(Math.min(Math.max(value, 0), 1) * duration);
    },

    timeScale(next) {
      if (next === 0) return timeline.pause();
      const now = timeline.time();
      rate = next;
      apply(now);
      return timeline;
    },

    time() {
      const value = master?.currentTime;
      return typeof value === "number" ? value : 0;
    },

    finish() {
      repeatsLeft = 0;
      playing = false;
      apply(rate < 0 ? 0 : duration);
      settle();
      return timeline;
    },

    cancel() {
      repeatsLeft = 0;
      for (const animation of all()) animation.cancel();
      held.length = 0;
      settled = true;
      resolveFinished();
      return timeline;
    },
  };

  return timeline;
};

/** Play one exit and resolve when it has finished; the end state is kept. */
export const minimalExit = (
  targets: MinimalTargets,
  keyframes: Keyframe[] | PropertyIndexedKeyframes = minimalKeyframes.fadeOut,
  options: MinimalTweenOptions & Pick<MinimalTimelineOptions, "reducedMotion"> = {},
): Promise<void> => {
  const { reducedMotion, ...tween } = options;
  const timeline = createMinimalTimeline({ reducedMotion, defaults: { easing: minimalVars.motion.easeExit } });
  timeline.add(targets, keyframes, { duration: minimalMotionMs.micro, ...tween, hold: true });
  return timeline.finished;
};

/** Play one entrance; resolves when finished. Elements return to their CSS afterwards. */
export const minimalEntry = (
  targets: MinimalTargets,
  keyframes: Keyframe[] | PropertyIndexedKeyframes = minimalKeyframes.fadeIn,
  options: MinimalTweenOptions & Pick<MinimalTimelineOptions, "reducedMotion"> = {},
): Promise<void> => {
  const { reducedMotion, ...tween } = options;
  const timeline = createMinimalTimeline({ reducedMotion });
  timeline.add(targets, keyframes, tween);
  return timeline.finished;
};

/**
 * Build a timeline after the component's DOM exists, and cancel it on unmount
 * or when `deps` change. `build` receives the timeline; return nothing.
 */
export const useMinimalTimeline = (
  build: (timeline: MinimalTimeline) => void,
  deps: readonly unknown[],
  options?: MinimalTimelineOptions,
) => {
  const ref = useRef<MinimalTimeline | null>(null);
  useLayoutEffect(() => {
    const timeline = createMinimalTimeline(options);
    build(timeline);
    ref.current = timeline;
    return () => {
      timeline.cancel();
      ref.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);
  return ref;
};
