/**
 * Scroll-profile fixture: a long feed built only from ui-minimal primitives.
 *
 * Query switches select the variant under test, so one page can be driven
 * through every combination by the profiling script:
 *
 *   ?sections=80      how many feed sections to render
 *   ?tier=low_power   write data-ui-tier on :root before first render
 *   ?cull=1           wrap each section in MinimalCullSection
 *
 * `window.__lab` is the measurement surface the script drives. It measures with
 * the same primitives an app would ship — frameTelemetry for intervals, a
 * long-animation-frame observer for main-thread blocking — so the lab measures
 * the instrument as well as the page.
 */

import { mountRoot } from "@ovasabi/frontend-kit";
import { startFrameTelemetry, type FrameStats } from "@ovasabi/runtime-browser";
import { Feed, feedOptions } from "./feed";

type LabSample = {
  stats: FrameStats;
  loaf: { count: number; blockingMs: number } | null;
  domNodes: number;
  scrollHeight: number;
};

declare global {
  interface Window {
    __lab?: { ready: boolean; start(): void; stop(): LabSample };
  }
}

const params = new URLSearchParams(location.search);
const tier = params.get("tier");
/*
 * ?skeleton= swaps the loading-placeholder animation design, so candidate
 * designs are compared interleaved in one session instead of across edits:
 *   minimal  the shipped MinimalSkeleton (default)
 *   bgpos    the previous design: a background-position sweep (never composites)
 *   pulse    an opacity pulse on the element itself (composited, no clip layer)
 *   static   the shipped markup with the sweep stopped
 *   none     no placeholder at all
 *   paused   the shipped sweep, paused while the placeholder is off screen
 *            (one shared IntersectionObserver toggles animation-play-state)
 * Lab-only CSS; it overrides the primitive through its data-minimal attribute.
 */
const skeleton = params.get("skeleton") ?? "minimal";

if (tier) document.documentElement.setAttribute("data-ui-tier", tier);
if (skeleton !== "minimal") {
  document.documentElement.setAttribute("data-lab-skeleton", skeleton);
  const style = document.createElement("style");
  style.textContent = `
@keyframes lab-bgpos { 0% { background-position: 100% 50%; } 100% { background-position: 0 50%; } }
@keyframes lab-pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.55; } }
:root[data-lab-skeleton="bgpos"] [data-minimal="Skeleton"] {
  background: linear-gradient(90deg, #ececef 0%, #f8f8f9 50%, #ececef 100%);
  background-size: 200% 100%;
  animation: lab-bgpos 1.2s linear infinite;
}
:root[data-lab-skeleton="pulse"] [data-minimal="Skeleton"] { animation: lab-pulse 1.2s ease-in-out infinite; }
:root:is([data-lab-skeleton="bgpos"], [data-lab-skeleton="pulse"]) [data-minimal="Skeleton"]::after { display: none; }
:root[data-lab-skeleton="static"] [data-minimal="Skeleton"]::after { animation: none; }
:root[data-lab-skeleton="none"] [data-minimal="Skeleton"] { display: none; }
:root[data-lab-skeleton="paused"] [data-minimal="Skeleton"]:not([data-lab-visible])::after { animation-play-state: paused; }
`;
  document.head.appendChild(style);
}
if (skeleton === "paused") {
  // One observer for every placeholder; started after React has committed the feed.
  const observer = new IntersectionObserver((entries) => {
    for (const entry of entries) entry.target.toggleAttribute("data-lab-visible", entry.isIntersecting);
  });
  requestAnimationFrame(() => {
    for (const element of document.querySelectorAll('[data-minimal="Skeleton"]')) observer.observe(element);
  });
}

let session: ReturnType<typeof startFrameTelemetry> | null = null;
let observer: PerformanceObserver | null = null;
let loaf: { count: number; blockingMs: number } | null = null;

window.__lab = {
  ready: false,
  start() {
    loaf = null;
    if (PerformanceObserver.supportedEntryTypes?.includes("long-animation-frame")) {
      loaf = { count: 0, blockingMs: 0 };
      observer = new PerformanceObserver((list) => {
        for (const entry of list.getEntries() as Array<PerformanceEntry & { blockingDuration?: number }>) {
          loaf!.count += 1;
          loaf!.blockingMs += entry.blockingDuration ?? 0;
        }
      });
      observer.observe({ type: "long-animation-frame" });
    }
    session = startFrameTelemetry("lab.scroll", { mark: false });
  },
  stop() {
    const stats = session?.stop();
    observer?.disconnect();
    observer = null;
    if (!stats) throw new Error("__lab.stop() called before start()");
    return {
      stats,
      loaf: loaf && { count: loaf.count, blockingMs: Math.round(loaf.blockingMs) },
      domNodes: document.getElementsByTagName("*").length,
      scrollHeight: document.documentElement.scrollHeight,
    };
  },
};

// Hydrates when the build prerendered the feed (loadProfile's prerender variant).
mountRoot(document.getElementById("root")!, <Feed {...feedOptions(location.search)} />);
requestAnimationFrame(() =>
  requestAnimationFrame(() => {
    window.__lab!.ready = true;
  }),
);
