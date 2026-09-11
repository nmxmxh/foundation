# UI Render Performance Research

Status: research (handoff)  
Date: 2026-09-11  
Owner: Platform Architecture

## Purpose

Foundation's data plane is already native-class: zero-copy control buffers,
shared arenas, worker-owned WASM, borrowed Hermes views, and native plugin byte
lanes keep data and compute off the UI thread. The remaining gap between a
Foundation app and the best native apps is **pixels**: layout, style, paint,
raster, and composite for DOM/CSS user interfaces, in the browser and in the
Tauri WebView shells on iOS and Android.

This document is the research handoff for closing that gap. It applies
AAA-game and mobile-graphics practice to DOM rendering, maps each finding onto
the primitives Foundation already ships, and proposes the next-phase
primitives, rules, budgets, and evidence harness. Every optimization here helps
the web build as much as the native shells, because both run the same bundle on
the same engines.

Scope:

1. DOM/CSS UI rendering: scrolling, motion, style, paint, raster, composite,
   images, and input latency.
2. Both engines Foundation ships on: Chromium (Chrome, Android WebView) and
   WebKit (Safari, iOS WKWebView).
3. Integration with the canvas/WebGPU lane in
   [render_surface_lane.md](render_surface_lane.md), which this document does
   not repeat.

Not in scope: canvas/WebGPU pass internals ([gpu_practices.md](gpu_practices.md)),
transport lanes ([runtime_foundation.md](runtime_foundation.md)), and native
device plugins ([runtime_native.md](runtime_native.md)) except where they feed
rendering decisions.

## How To Use This Document

This is a research ledger entry, not adopted practice. Under the promotion
rules in [future_practices_research.md](future_practices_research.md), a finding
becomes Foundation practice only after it maps to a contract, invariant, test,
benchmark, scaffold default, or review checklist, and its owning document is
updated. Section 12 names the owning document for each proposal.

Read in this order:

1. Thesis and section 1 for the engine model, which explains why every rule
   below exists.
2. Section 3 for what Foundation already ships.
3. Section 5 for the proposals, each with invariants, an evidence gate, and a
   fallback.
4. Sections 6 and 7 before claiming any win: budgets and the evidence harness.

## Thesis

1. **Scrolling is already off the main thread on both engines.** WKWebView
   scrolls with UIKit's own `UIScrollView` in the app's UI process, so scroll
   physics are native. Chromium scrolls and runs compositor animations on its
   compositor thread. A WebView does not scroll badly by nature.
2. **Frames are lost to four causes**, and every rule in this document targets
   one of them:
   - main-thread work that delays the next frame: script, style recalculation,
     layout, runtime style injection, re-renders triggered by scroll;
   - raster and composite cost: blur, large translucent layers, shadows,
     oversized images, and overdraw on bandwidth-limited mobile GPUs;
   - memory pressure: too many composited layers, then tile eviction, then
     synchronous re-raster on the critical path;
   - first-use hitches: route chunks, fonts, image decode, pipeline warm-up.
3. **"Beating the best" has three parts:**
   - *match* native frame pacing by keeping per-frame work compositor-only;
   - *beat* native apps as they are usually built on waiting, because screens
     render from projections already in memory instead of a network round
     trip per screen;
   - *beat* native on predictability, with an explicit quality ladder that
     lowers detail before a frame is missed, which most native apps never ship.
4. **The irreducible limits:** the platform toolkit's raw raster speed,
   JavaScript-driven animation above 60 Hz inside WKWebView, and composing
   native views into a WebView screen on mobile. Where those matter,
   Foundation uses the canvas/WebGPU render surface lane or a full-screen
   native view launched from a plugin.

## 1. Engine Models: Where Frame Time Goes

### 1.1 Chromium (Chrome, Android WebView)

Android WebView is Chromium, updated independently of the OS through Google
Play, so the Chromium model applies to the Android shell and to Chrome on the
web.

1. RenderingNG pipeline: style, layout, paint (display list), commit (property
   trees and display list copied to the compositor thread), layerize, raster
   and decode into GPU tiles, activate, aggregate, draw. Raster runs out of
   process in the GPU process (Viz).
2. The compositor thread handles input, scrolling, and compositor animations,
   computes layerization, and schedules raster. Anything it can do alone
   survives a busy main thread; anything that needs a new main-thread frame
   (style, layout, paint) does not.
3. Layer squashing merges overlapping layers into one backing store to prevent
   "layer explosion". It cannot always apply, and every promoted layer costs GPU
   memory. A single 4096×4096 RGBA layer is 64 MB uncompressed; a few of those
   exhaust a mobile GPU budget.
4. When tile allocations exceed the per-process budget, the compositor evicts
   tiles and re-rasterizes them on demand, and the next frame waits. Memory
   pressure turns directly into jank.
5. Google reports a 48% reduction in janky scrolls in Chrome on Android between
   2023 and 2026, from moving input handling off the browser main thread
   ("Input Vizard"). Whether and when each piece reaches Android WebView is not
   documented; treat it as an open question (section 11).

### 1.2 WebKit (Safari, iOS WKWebView)

1. WKWebView is multi-process: the app's UI process owns the viewport, the
   WebContent process owns the content, layer tree, and animation timing, and
   a GPU process and networking process sit beside them.
2. The WebContent process commits a remote layer tree to the UI process, which
   renders it with Core Animation. Scrolling is a real `UIScrollView` in the UI
   process; the UI process compensates for user interaction that the web
   process has not yet committed.
3. Consequence: iOS scroll physics are native. Jank on iOS shows up as the
   content process missing frames (checkerboarding, late updates, stalled
   JS-driven animation), not as bad scrolling.
4. `requestAnimationFrame` inside WKWebView is capped at 60 Hz by design, for
   power and compatibility, even on 120 Hz ProMotion devices. No public
   Info.plist key or `WKWebViewConfiguration`/`WKPreferences` property changes
   it, as of iOS/macOS 26. Safari has its own unlocked frame rate setting that
   does not apply to WKWebView. A community plugin unlocks it through private
   WebKit API; Foundation must not adopt it (section 10).

### 1.3 Display cadence and frame pacing

1. Budgets per refresh rate: 16.67 ms at 60 Hz, 11.1 ms at 90 Hz, 8.33 ms at
   120 Hz (see [game_runtime_practices.md](game_runtime_practices.md), frame
   budgets).
2. Android's UI toolkit renders ahead by one vsync at high refresh rates,
   giving about 21 ms to produce a frame at 90 Hz while keeping throughput.
   Native apps can declare an intended frame rate through the Android frame
   rate API (API 30+).
3. In Chromium, `requestAnimationFrame` generally follows the display refresh
   rate. In WKWebView it is capped at 60 Hz (1.2). Motion code must therefore
   scale by the timestamp delta, never a fixed step per callback, and must not
   assume a rate.
4. Foundation's `frameClock` already unifies loops into one pulse-scheduled
   browser frame, which is the web equivalent of a game engine's single frame
   pacer (Android's Swappy library plays that role for native games).

### 1.4 Mobile GPUs

1. Mali, Adreno, PowerVR, and Apple GPUs are tile-based: the screen is split
   into small tiles rendered in on-chip memory before being written out. On-chip
   blending is cheap; bandwidth to main memory is the scarce resource,
   especially on low-cost SoCs.
2. Overdraw (drawing the same pixel several times) and large translucent
   surfaces multiply bandwidth. `backdrop-filter: blur()` must read and filter
   what lies beneath the element every time that content changes, so a blurred
   element fixed over scrolling content re-blurs on every scroll frame.
3. Blur cost scales with radius and area. Keeping radii at or below about
   12–16 px and not stacking blurred surfaces in one scroll area keeps it
   tolerable on low-end Android; large radii over large areas do not.
4. `will-change` promotes a layer ahead of time and costs GPU memory for as
   long as it is set. [styling_design_practices.md](styling_design_practices.md)
   already forbids permanent `will-change`.

### 1.5 Target devices and thermal reality

1. For Nigerian products the performance floor is set by Transsion: Tecno,
   Infinix, and itel together hold roughly two thirds of the Nigerian market,
   led by the sub-$100 segment. Samsung (about 12%) and Apple (about 9%)
   follow. The floor device is a low-cost Android phone, not a flagship.
2. Low-cost devices also throttle under sustained load. Android's Dynamic
   Performance Framework (ADPF) gives native code thermal headroom (a value of
   1.0 means throttling has begun) and performance-hint sessions that report
   actual against target frame duration so the SoC can allocate resources.
   A WebView app cannot reach ADPF directly, but a Foundation native plugin
   can (proposal P8).

## 2. Platform Capability Matrix

Support as researched in September 2026. "WebView" means Android WebView
(Chromium) and "WKWebView" means iOS/iPadOS 26.

| Capability | Chromium / Android WebView | WebKit / WKWebView | Foundation use | Fallback |
| --- | --- | --- | --- | --- |
| Compositor scrolling | Yes (compositor thread) | Yes (native `UIScrollView`) | Baseline | n/a |
| `transform`/`opacity` CSS and WAAPI animations | Compositor | Compositor | Default motion lane | None needed |
| `content-visibility: auto` | Chrome 85+ | Safari 18+ (Baseline 2024) | Offscreen culling (P4) | Render normally |
| Scroll-driven animations | Chrome 115+ | Safari 26.0+; compositor-threaded from 26.4 | Scroll-linked motion without JS (P3) | Static state |
| View Transitions (same document) | Yes | Yes; Safari 26.2 fixed fixed/sticky clipping | Route and shared-element transitions (P3) | Instant swap |
| `requestAnimationFrame` rate | Follows display | Capped at 60 Hz | Timestamp-scaled motion only | n/a |
| Long Animation Frames API | Chrome 123+ | Not available | Main-thread jank attribution (P1) | Frame-interval sampler |
| `scheduler.yield()` | Chrome 129+ (Firefox 142+) | Not available | Chunk long work (P9) | `setTimeout(0)` yield |
| `decoding="async"` / `img.decode()` | Yes | Yes | Jank-free image reveal (P6) | Plain load |
| `createImageBitmap` in workers | Yes | Yes | Off-thread decode for canvas (P6) | Main-thread decode |
| `OffscreenCanvas` | Yes | Yes | Render surface lane | Main-thread canvas |
| WebGPU | Chrome 113+; WebView status unverified | Safari 26 enables it; WKWebView status unverified | Render surface lane, optional | WebGL / 2D |

WebGPU in both WebViews is marked unverified: secondary sources claim neither
WebView enables it by default, and caniwebview.com lists both as unknown. The
render surface lane already probes capability and falls back, so Foundation
must never assume it (section 11).

Adjacent ecosystem facts that affect Foundation's frontend:

1. React Compiler 1.0 (October 2025) inserts memoization automatically at
   build time; Meta reports up to 12% faster loads and navigations and some
   interactions over 2.5× faster, with neutral memory.
2. styled-components entered maintenance mode in March 2025. React's guidance
   is that runtime style injection is always slower than extracted styles.
   Foundation's styling doc adopts styled-components, so this is an
   architecture decision (section 8), not a drop-in change.

## 3. What Foundation Already Ships

| Primitive | Location | What it gives | Gap for the DOM lane |
| --- | --- | --- | --- |
| `frameClock` (`configureFrameClock`, `onFrame`, `frameClockMode`) | `runtime-sdk/ts/browser-host` | One pulse-scheduled browser frame shared by all loops, with cadence | Only canvas/visual loops use it today; no DOM frame telemetry |
| Pulse worker (`createPulseManager`) | `runtime-sdk/ts/browser-host/src/pulse` | Worker-driven epoch pulse | Not consumed by UI motion |
| Render surface (`createRenderSurfaceHost`, `prewarmRenderSurface`, `probeRenderSurface`) | `runtime-sdk/ts/browser-host` | Worker-owned `OffscreenCanvas`/WebGPU raster, quality ladder (demote after 24 late frames, promote after 180 on-time), allocation-free frames | Canvas only; its ladder semantics are not reused for CSS effects |
| `canvasStage` | `runtime-sdk/ts/browser-host` | 2D canvas without layout thrash, observer culling, cadence gating | Canvas only |
| `renderMarks` (`PASS`, `markLane`, `renderFacts`) | `runtime-sdk/ts/browser-host` | Stable low-cardinality pass markers | No DOM passes (style, scroll, route) defined |
| Motion helpers (`createStandardTransition`, `createPageTransitionVariants`, `useMinimalMotion`) | `ui-minimal/ts/src/motion.ts` | Tokenized durations and easings, reduced-motion aware | Implemented on framer-motion (JS); the styling doc's own order puts CSS transitions and WAAPI first |
| Projection worker pipeline, `runtimeExternalStore`, `lazyPage`, `chunkGate` | `frontend-kit/ts/src` | Off-main-thread projection work, store subscription, route chunk gating | No virtualization or culling primitive over projections |
| Transport lane order (`sab`, `wasm`, `native`, …) and `RuntimeSharedArena` | `runtime-transport`, `runtime-sdk` | Zero-copy data to workers when cross-origin isolated | Shells must set COOP/COEP for the `sab` lane to exist |
| Frame budgets, culling, platform profiles, hitch ledger | [game_runtime_practices.md](game_runtime_practices.md) | The doctrine this document applies | Written for canvas and visual loops; not yet translated to DOM/CSS |
| Motion Design System | [styling_design_practices.md](styling_design_practices.md) §6 | CSS-first order, `transform`/`opacity` only, no permanent `will-change`, pause offscreen loops | Review-enforced only; no lint or runtime check |

The doctrine is already right. The gap is that the DOM lane has no primitives,
no telemetry, and no enforcement to match the canvas lane.

## 4. AAA Technique Translation

| AAA technique | DOM/WebView equivalent | Foundation primitive |
| --- | --- | --- |
| Per-lane frame budget, p95/p99/max hitch | Long Animation Frames, frame-interval sampling, native `gfxinfo` framestats | `frameTelemetry` (P1) feeding `renderMarks` |
| Frustum / occlusion culling | `content-visibility: auto` with `contain-intrinsic-size`; `IntersectionObserver` | `cullSection` (P4) |
| Object pooling | List virtualization with DOM node recycling | `virtualList` over projections (P4) |
| LOD and mipmaps | `srcset`/`sizes`, low-quality placeholders, progressive detail | Image pipeline (P6) |
| Texture streaming, upload budget | `img.decode()` before reveal, per-frame decode queue, worker `createImageBitmap` | Image pipeline (P6) |
| Shader / PSO warm-up | Route chunk prefetch, font preload, first-interaction warm-up | Warm-up and hitch ledger (P9) |
| Overdraw and transparency control | Few translucent layers, no blur over scrolling content on low tiers, `contain: paint` | Raster budget rules (P7) |
| Draw-call batching, atlases | Build-time extracted CSS, SVG symbol sprites, fewer promoted layers | Styling decision (section 8), P7 |
| Dynamic resolution scaling | Quality tiers switching CSS effects: blur, shadow, motion density, overscan | `uiQuality` tiers (P2) |
| Fixed timestep and interpolation | Timestamp-scaled motion; compositor animations; no rAF-driven layout | Compositor-first motion (P3) |
| Async compute | Worker projections over shared memory; main thread renders only | Existing lanes plus cross-origin isolation in shells |
| Single frame pacer (Swappy) | One `frameClock`, no per-component rAF loops | Existing `frameClock`, extended to DOM (P1, P3) |
| Thermal and power management (ADPF) | Thermal headroom and low-power mode read by a native plugin | Native WebView plugin (P8) feeding P2 |
| Input latency | Passive listeners, `touch-action`, no forced layout in handlers, INP | Input rules in P3 and P9 |
| Capture-backed testing | Perfetto, Web Inspector timelines, screenshot diffs | Evidence harness (section 7) |

## 5. Proposed Primitives And Rules

Each proposal lists its purpose, a sketch, invariants, an evidence gate, and a
fallback. Names and signatures are proposals for the next phase, not shipped
API.

### P1. `frameTelemetry` — DOM frame evidence

Why: the canvas lane measures itself; the DOM lane is blind. Without numbers
every other proposal is a guess.

Shape (browser-host):

```ts
type FrameStats = {
  frames: number;
  p50Ms: number; p95Ms: number; p99Ms: number; maxMs: number;
  slowFrames: number;   // interval > 1 display frame (16.7 ms at 60 Hz)
  frozenFrames: number; // interval > 700 ms, matching Android vitals
  longAnimationFrames: number; // LoAF entries > 50 ms, where supported
  visibleOnly: true;
};
startFrameTelemetry(scope: string, options?: { sampleRate?: number }): () => FrameStats;
```

Invariants:

1. Samples only while `document.visibilityState === "visible"`. Browsers pause
   animation frames in hidden documents, so hidden samples are fiction.
2. Uses `PerformanceObserver` for `long-animation-frame` where available, and a
   `frameClock`-driven interval sampler otherwise; never a second rAF loop.
3. Bounded ring buffers; zero cost when disabled; low-cardinality scope names
   through `renderMarks`.
4. Reports slow and frozen counts with the same thresholds as Android vitals
   (16 ms and 700 ms) so web, WebView, and native evidence line up.

Evidence gate: the scripted scroll scenario (section 7) reproduces the same
p95 within ±10% across three runs on the same device.

Fallback: when neither LoAF nor visibility is available, the report carries
`degraded: true` and is excluded from gates.

### P2. `uiQuality` — device and runtime quality tiers for CSS effects

Why: game engines ship device profiles; Foundation's
[game_runtime_practices.md](game_runtime_practices.md) already names the tiers
(`preview`, `balanced`, `high`, `low_power`, `reduced_motion`). The render
surface ladder implements them for canvas; CSS effects have nothing.

Shape:

```ts
type UiTier = "high" | "balanced" | "low_power" | "reduced_motion";
// Writes data-ui-tier on :root; CSS selects cheap or rich effects by attribute.
createUiQuality(inputs: {
  frameStats: () => FrameStats;       // P1
  thermalHeadroom?: () => number;     // P8, native shells only
  lowPowerMode?: () => boolean;       // P8
}): { tier(): UiTier; subscribe(fn: (t: UiTier) => void): () => void };
```

Inputs: `prefers-reduced-motion`, `navigator.deviceMemory` and
`hardwareConcurrency` where exposed, Save-Data, measured frame stats, and native
thermal and power state.

Invariants:

1. Hysteresis copied from the render surface ladder: demote after sustained
   late frames, promote only after a long on-time run. Tiers never flap.
2. Tiers change detail, never correctness: blur radius, shadow style, motion
   density, list overscan, image resolution. Never data, auth, or commands.
3. Every tier change is logged with its reason (game runtime profile rule 4).

Evidence gate: on the floor device, forcing `low_power` removes all slow frames
in the scroll scenario that `high` produces.

Fallback: static tier from media queries alone.

### P3. Compositor-first motion

Why: WKWebView caps JavaScript-driven animation at 60 Hz and any main-thread
stall freezes it on both engines. Compositor animations survive both.
[styling_design_practices.md](styling_design_practices.md) §6 already orders
CSS transitions, then WAAPI, then Motion; `ui-minimal`'s helpers invert that by
building on framer-motion.

Rules:

1. Motion tokens (durations, easings, offsets) are exported as CSS custom
   properties from the theme, so CSS transitions and WAAPI use the same tokens
   as today's framer-motion helpers.
2. Default lanes by job:
   - state feedback and enter/exit: CSS transitions or `@starting-style`;
   - scripted sequences: WAAPI on `transform`/`opacity`;
   - route and shared-element transitions: View Transitions;
   - scroll-linked effects (header collapse, parallax, progress):
     scroll-driven animations;
   - framer-motion only for layout animation and gesture physics.
3. No `requestAnimationFrame`-driven style or layout writes for UI motion. Any
   remaining JS motion scales by timestamp delta.
4. Infinite animations (skeleton shimmer, pulses) pause offscreen and in the
   `low_power`/`reduced_motion` tiers.
5. No exit-gated animation around route boundaries: a route that waits for an
   exit animation stalls navigation, and a hidden document never finishes it.

Evidence gate: during a forced 100 ms main-thread block, compositor motion keeps
its frame interval, measured with P1.

Fallback: instant state change.

### P4. `cullSection` and `virtualList` — culling and pooling

Why: game engines win by not drawing what cannot matter (game runtime practices,
data reduction). Foundation has no DOM equivalent, and long feeds, menus,
calendars, and ledgers render every row.

Shape:

1. `cullSection`: wraps a section in `content-visibility: auto` with a
   remembered `contain-intrinsic-size` (the last measured size), so skipped
   content keeps its scroll height.
2. `virtualList`: windowing driven by projection indices from
   `runtimeExternalStore`, with recycled row nodes, overscan chosen by P2 tier,
   and stable keys.

Invariants:

1. Culled content stays findable and accessible (Safari 26.x fixed find-in-page
   for skipped content; test it).
2. No scroll-position jumps: intrinsic sizes come from measurement, not guesses.
3. Windowing happens off the main thread when the projection is large (worker
   projection pipeline); the main thread only mounts the visible window.

Evidence gate: DOM node count on the longest list stays flat as the list grows
10×, and scroll p95 does not regress.

Fallback: plain rendering below a row threshold.

### P5. Styling runtime lane

Why: runtime CSS-in-JS creates classes and rules during render, on the main
thread, and its `<style>` injection forces `style-src 'unsafe-inline'` in native
shells. See section 8 for the decision.

Measurable target: zero CSS rules injected after first paint during scroll and
navigation.

### P6. Image pipeline

Why: image decode on the main thread and oversized images are classic scroll
jank on mobile, and every decoded image costs GPU memory.

Rules:

1. Serve sized sources (`srcset`/`sizes`; modern formats where the backend can
   produce them) so decoded pixels match displayed pixels.
2. `decoding="async"` everywhere; `img.decode()` before revealing an image that
   animates in, so reveal never waits on decode.
3. Canvas and render-surface images decode in workers with `createImageBitmap`.
4. A per-frame decode queue during active scroll, prioritized by viewport
   distance, with prefetch-and-decode for the likely next screen.
5. A per-viewport decoded-pixel budget, set from the first baseline.

Evidence gate: no long animation frame attributed to image decode in the scroll
scenario; decoded pixels at or below the budget.

Fallback: lazy loading only.

### P7. Raster budget rules

Rules, enforceable by lint or review:

1. `backdrop-filter` only on surfaces that are not fixed over scrolling content,
   or only in the `high` tier; lower tiers use a solid translucent background.
2. Blur radius at most 16 px; never stack blurred surfaces in one scroll area.
3. Cards in scrolling lists use `contain: layout paint` and a cheap shadow
   treatment; no animated shadows or filters.
4. `will-change` only during a gesture or animation, removed afterwards (styling
   doc §6).
5. A composited-layer budget per screen, set from the first baseline and
   checked with DevTools layer counts.

Evidence gate: the floor device holds the scroll budget with the `high` tier's
effects enabled on every surface that keeps them.

### P8. Native WebView tuning and device signals (runtime-native)

Why: some knobs exist only on the native side, and Tauri exposes supported
hooks: Android plugins receive the `WebView` in `load(webView)`
(`PluginManager.onWebViewCreated`), and Rust reaches the platform view through
`Webview::with_webview` (`PlatformWebview`).

Proposals:

1. Debug builds only: `WebView.setWebContentsDebuggingEnabled(true)` on
   Android and `WKWebView.isInspectable = true` on iOS 16.4+, so the evidence
   harness can inspect release-like builds.
2. Measure before adopting: Android's `setOffscreenPreRaster` (faster display,
   more memory) and renderer priority policy.
3. Device signals for P2: thermal headroom and performance-hint sessions
   through ADPF on Android; low-power mode and thermal state on iOS. Exposed as
   typed values through the existing runtime-native command allowlist, never
   JSON streams.
4. Cross-origin isolation (COOP/COEP) in the Tauri shells so the `sab` lane
   exists there. Requires `Cross-Origin-Resource-Policy` or CORS on
   cross-origin images, because WKWebView has no `credentialless` mode.

Invariants: no private APIs; debug-only flags never ship in release; every knob
measured before and after with P1.

### P9. Warm-up and hitch ledger for the UI

Why: first-use hitches are product bugs even when averages are fine (game
runtime practices, frame budgets rule 2).

Rules:

1. Prefetch the next likely route chunk in idle time (`chunkGate`/`lazyPage`),
   and warm the render surface where one is coming (`prewarmRenderSurface`).
2. Preload the display and body font subsets; self-host fonts.
3. Chunk long main-thread work with `scheduler.yield()` where supported and a
   timeout yield elsewhere.
4. Record first-interaction costs per route (first tap, first open, first
   scroll) in a hitch ledger through `renderMarks`.

### P10. Evidence harness

See section 7. It is a proposal because Foundation has no DOM-lane capture
bundle today.

## 6. Budgets And Acceptance Gates

Proposed gates, measured with P1 on the floor device class unless stated.

| Gate | Target | Basis |
| --- | --- | --- |
| Scroll and animation, p95 frame interval | ≤ 16.7 ms at 60 Hz | Display budget |
| Scroll and animation, p99 | ≤ 25 ms | Hitch headroom (proposed) |
| Maximum hitch during scripted scroll | ≤ 50 ms | LoAF threshold |
| Frozen frames | 0 | Android vitals, > 700 ms |
| Runtime CSS rules injected after first paint | 0 | P5 |
| Long animation frames attributed to image decode | 0 | P6 |
| Route change from in-memory projection to first paint | ≤ 100 ms perceived (proposed) | "Beat native on waiting" |
| 90/120 Hz devices | All UI motion on the compositor | WKWebView rAF cap |

Composited-layer and decoded-pixel budgets are deliberately unset: they must
come from the first baseline, not from guesses.

## 7. Evidence Harness

Device matrix:

1. Floor: a current Transsion device (Tecno/Infinix) on a low-cost SoC.
2. Mid: a Samsung A-series device.
3. iPhone with ProMotion, iOS 26.
4. Android emulator for CI smoke only. Emulator and desktop numbers never count
   as evidence for a gate.

Tools:

1. Android: `adb shell dumpsys gfxinfo <package> framestats`, Perfetto traces,
   and Chrome DevTools remote debugging (`chrome://inspect`) with WebView
   debugging enabled in debug builds.
2. iOS: Safari Web Inspector timelines with `isInspectable` enabled in debug
   builds.
3. Web: Long Animation Frames and Event Timing in real-user monitoring.
4. Screenshot diffs for any change to effects or motion (game runtime
   practices, capture-backed testing).

Scenarios, scripted and repeatable:

1. Scroll the main feed down and back at a fixed velocity.
2. Open and close a detail sheet or modal.
3. Route change to and from a list screen.
4. Add to cart with its feedback motion.
5. A cold start to first interactive screen.

Capture bundle, following [performance_lab.md](performance_lab.md) and
[gpu_practices.md](gpu_practices.md): build SHA, device, OS and WebView version,
tier, feature flags, scenario, raw frame intervals, and the trace file.

## 8. Styling Runtime Decision (ADR Input)

[styling_design_practices.md](styling_design_practices.md) §3 standardizes on
styled-components. The research above says runtime injection costs main-thread
time on every new prop value and forces `'unsafe-inline'` in the Tauri shells,
and the library is in maintenance mode. That is a decision for the platform
architect, framed here as options:

| Option | What changes | Cost | Result |
| --- | --- | --- | --- |
| A. Keep styled-components as is | Nothing | None | Runtime cost, CSP exception, and maintenance risk remain |
| B. CSS-variable discipline inside styled-components | Theme and prop values move to `var(--token)` and CSS custom properties set through `style`; templates become static | Mechanical; keeps the §3 format | Far fewer runtime rules; still a runtime library |
| C. Zero-runtime `styled` API (Linaria on WyW-in-JS) | Same `styled`/`css` tagged-template syntax, extracted to static CSS at build | Build pipeline change, after B | No runtime injection; the CSP exception can go |
| D. Different zero-runtime system (vanilla-extract, StyleX, Panda) | New authoring format | Largest rewrite | Same outcome as C, with a larger migration |

Recommendation for the ADR: **B, then C.** Step B is valuable on its own and is
exactly the preparation C needs, because extracted styles cannot call theme
functions at runtime. The theme already exports tokens as CSS variables (styling
doc §4.3), so B mostly means referencing variables that already exist. C keeps
the §3 authoring format, so the styling doc changes its engine, not its rules.

## 9. First Adopter: ChooseChow Baseline

`chowdash_rider_v1` (ChooseChow) is the first adopter and validation target.
Baseline taken 2026-09-11, before any proposal here:

Measured on the dashboard (desktop preview; not gate evidence):

1. 374 DOM nodes, depth 13: the DOM is light.
2. 205 CSS rules, all injected at runtime by styled-components in one `<style>`
   element.
3. 4 elements with `backdrop-filter`.

Codebase audit:

| Signal | Count | Notes |
| --- | --- | --- |
| styled-components prop interpolations | ~1,884 | Candidates for P5 option B |
| `backdrop-filter` uses | 17 | Fixed over scroll in `AppTopNav`, `CartFab`, `PageHeader`, `SearchField` (P7) |
| Blur radii | 3–16 px, mostly 10 px | Within the P7 radius rule; placement is the issue |
| `box-shadow` | 75 | P7 card treatment |
| Infinite animations | 8 | `styles/skeleton.ts` shimmer (P3 rule 4); `LandingPage`, which the native shell skips |
| Virtualized lists | 0 | P4 candidates: `MealCalendarPage`, `ChefKitchen`, `WalletPage`, `MenuBuilderPage` |
| `content-visibility` / `contain` | 0 | P4 |
| Fonts | Self-hosted, subset by `unicode-range` | Done (P9 rule 2) |
| Tauri shell COOP/COEP | Not set | The `sab` lane is unavailable in the shells (P8 rule 4) |

The packaged Android release is 10.4 MB, of which 7.5 MB is the native library
carrying the compressed web bundle, so these optimizations do not trade against
download size.

## 10. Anti-Patterns And Lessons

1. **Private API frame-rate unlocks.** Unlocking 120 Hz in WKWebView through
   private WebKit API risks App Store rejection and breaks silently across OS
   updates. Put motion on the compositor instead.
2. **Measuring in a hidden document.** Animation frames pause in hidden
   documents; a script waiting on them never completes and a sampler records
   nothing. This happened while gathering this baseline. P1 invariant 1 exists
   for this reason.
3. **Desktop or emulator numbers as proof.** They do not represent the floor
   device's GPU bandwidth, thermals, or memory.
4. **Permanent `will-change` and blanket layer promotion.** Layer memory leads
   to tile eviction, which leads to jank.
5. **Per-component rAF loops.** Use one `frameClock`.
6. **Animating layout properties or `transition: all`.** Already forbidden by
   the styling doc; needs lint enforcement.
7. **Exit-gated route transitions.** They stall navigation and never finish in
   hidden documents.
8. **Fixed-step JS motion.** Motion that advances per callback runs at
   different speeds at 60, 90, and 120 Hz; scale by timestamp.

## 11. Open Questions

1. WebGPU availability in Android WebView and WKWebView on current OS versions:
   probe on the device matrix and record the result in
   [gpu_practices.md](gpu_practices.md).
2. Do compositor-driven CSS animations reach 120 Hz inside WKWebView on
   ProMotion devices, given the rAF cap? Measure on device.
3. Which of Chrome's input-path jank work ("Input Vizard") applies to Android
   WebView, and from which WebView version?
4. Android WebView renderer process model and memory limits on the floor
   device class, including Android Go editions.
5. View Transitions snapshot cost on the floor device: when does a transition
   cost more than it gives?
6. React Compiler 1.0 interaction with styled-components and with Linaria
   extraction.
7. Real-user monitoring for native shells: can LoAF and P1 data flow through
   the existing metrics lanes without a new transport?

## 12. Handoff Sequencing And Ownership

| Phase | Work | Promotes into |
| --- | --- | --- |
| A. Instrument | P1 `frameTelemetry`, section 7 harness and first baseline, P8 debug inspection flags | [game_runtime_practices.md](game_runtime_practices.md), [performance_lab.md](performance_lab.md) |
| B. Cheap wins | P7 raster rules, P2 tiers (static, then measured), P4 `cullSection`, P3 rule 4 (pause loops) | [styling_design_practices.md](styling_design_practices.md), [game_runtime_practices.md](game_runtime_practices.md) |
| C. Motion and styling | P3 compositor-first motion in `ui-minimal`, section 8 ADR, P5 option B then C, P4 `virtualList` | [styling_design_practices.md](styling_design_practices.md), [render_surface_lane.md](render_surface_lane.md) |
| D. Native and assets | P8 device signals and COOP/COEP, P6 image pipeline, P9 warm-up and hitch ledger | [runtime_native.md](runtime_native.md), [performance_practices.md](performance_practices.md) |

Each phase closes with a before/after capture bundle on the floor device, and
each promoted rule gets enforcement where it is low-noise: a lint for animated
layout properties and permanent `will-change`, a scaffold check for shell
COOP/COEP, and a CI assertion that no runtime style rules appear after first
paint once P5 lands.

## Sources

Engines and pacing:

- [RenderingNG architecture (Chrome for Developers)](https://developer.chrome.com/docs/chromium/renderingng-architecture)
- [Key data structures in RenderingNG](https://developer.chrome.com/docs/chromium/renderingng-data-structures)
- [GPU accelerated compositing in Chrome](https://www.chromium.org/developers/design-documents/gpu-accelerated-compositing-in-chrome/)
- [How cc works (Chromium docs)](https://chromium.googlesource.com/chromium/src/+/master/docs/how_cc_works.md)
- [GPU memory limits in Chrome compositing](https://www.browser-rendering.com/compositing-and-gpu-acceleration/hardware-acceleration-limits/gpu-memory-limits-in-chrome-compositing/)
- [Smoother scrolling: how we halved scroll jank in Chrome on Android](https://blog.google/chromium/smoother-scrolling-how-we-halved-scroll-jank-in-chrome-on-android/)
- [WebKit introduction (multi-process architecture)](https://github.com/WebKit/WebKit/blob/main/Introduction.md)
- [WebKit GPU process](https://trac.webkit.org/wiki/GPUProcess)
- [Apple Developer Forums: WKWebView 120Hz support](https://developer.apple.com/forums/thread/773222)
- [WebKit bug 173434: 120Hz requestAnimationFrame](https://bugs.webkit.org/show_bug.cgi?id=173434)
- [High refresh rate rendering on Android](https://android-developers.googleblog.com/2020/04/high-refresh-rate-rendering-on-android.html)
- [Optimize refresh rates (Android games)](https://developer.android.com/games/optimize/display-refresh-rate-change)
- [MDN: requestAnimationFrame](https://developer.mozilla.org/docs/Web/API/Window/requestAnimationFrame)

Mobile GPUs, devices, and thermals:

- [The Mali GPU: an abstract machine, part 2 — tile-based rendering](https://developer.arm.com/community/arm-community-blogs/b/mobile-graphics-and-gaming-blog/posts/the-mali-gpu-an-abstract-machine-part-2---tile-based-rendering)
- [backdrop-filter costs and limits](https://empire-ui.com/blog/backdrop-filter-css)
- [Optimize thermal and CPU performance with ADPF](https://developer.android.com/games/optimize/adpf)
- [Best practices for ADPF](https://developer.android.com/games/optimize/adpf/best-practices-adpf)
- [Slow rendering (Android vitals)](https://developer.android.com/topic/performance/vitals/render)
- [TechCabal: Nigeria smartphone market, Q2 2025](https://techcabal.com/2025/09/05/nigeria-smartphone-market-recovery-q2-2025/)
- [Intelpoint: Tecno share in Nigeria, February 2025](https://intelpoint.co/insights/tecno-has-the-highest-share-among-phone-brands-in-nigeria-at-23-55-as-of-february-2025/)

Web platform:

- [WebKit features in Safari 26.0](https://webkit.org/blog/17333/webkit-features-in-safari-26-0/)
- [WebKit features for Safari 26.2](https://webkit.org/blog/17640/webkit-features-for-safari-26-2/)
- [WebKit features for Safari 26.4](https://webkit.org/blog/17862/webkit-features-for-safari-26-4/)
- [A guide to scroll-driven animations with just CSS (WebKit)](https://webkit.org/blog/17101/a-guide-to-scroll-driven-animations-with-just-css/)
- [MDN: content-visibility](https://developer.mozilla.org/en-US/docs/Web/CSS/Reference/Properties/content-visibility)
- [Long Animation Frames API (W3C)](https://www.w3.org/TR/long-animation-frames/)
- [Long Animation Frames API (Chrome for Developers)](https://developer.chrome.com/docs/web-platform/long-animation-frames)
- [MDN: Scheduler.yield()](https://developer.mozilla.org/en-US/docs/Web/API/Scheduler/yield)
- [Can I use: scheduler.yield](https://caniuse.com/mdn-api_scheduler_yield)
- [What does the image decoding attribute actually do?](https://www.tunetheweb.com/blog/what-does-the-image-decoding-attribute-actually-do/)
- [Can I WebView: WebGPU](https://caniwebview.com/features/web-feature-webgpu/)
- [WebGPU implementation status (gpuweb)](https://github.com/gpuweb/gpuweb/wiki/Implementation-Status)

Frameworks and styling:

- [React Compiler v1.0](https://react.dev/blog/2025/10/07/react-compiler-1)
- [Sanity: styled-components maintenance mode](https://www.sanity.io/blog/cut-styled-components-into-pieces-this-is-our-last-resort)
- [Linaria](https://github.com/callstack/linaria)
- [WyW-in-JS](https://wyw-in-js.dev/)

Native hooks and inspection:

- [Tauri mobile plugin development](https://v2.tauri.app/develop/plugins/develop-mobile/)
- [Tauri discussion: native view embedding](https://github.com/tauri-apps/tauri/discussions/11918)
- [Remote debugging WebViews (Chrome DevTools)](https://developer.chrome.com/docs/devtools/remote-debugging/webviews)
- [Enabling the inspection of web content in apps (WebKit)](https://webkit.org/blog/13936/enabling-the-inspection-of-web-content-in-apps/)
