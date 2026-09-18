# UI Render Performance Research

Status: in implementation (see section 13, the ledger)  
Date: 2026-09-11, ledger updated 2026-09-14  
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
2. Uses `PerformanceObserver` for `long-animation-frame` where available, and
   reads `requestAnimationFrame` timestamps directly for intervals, only inside a
   window the caller opens and closes. *Revised 2026-09-14:* the original
   "`frameClock`-driven sampler, never a second rAF loop" was wrong for
   measurement. `frameClock` ticks come from the pulse worker, so its callback
   gap is pulse timing plus frame timing (a drifted tick reports a skipped frame
   nobody saw), and it is capped at its target rate, so 90/120 Hz displays are
   invisible through it. Work still goes through `frameClock`; the instrument
   does not.
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
   *Finding 2026-09-14:* Android WebView ignores COOP/COEP on responses the shell
   serves itself (`shouldInterceptRequest`). Verified on WebView 133: headers
   present, `crossOriginIsolated` false, `SharedArrayBuffer` undefined. The
   Android shell cannot get the `sab` lane this way; the headers stay for parity
   and the runtime's fallback lane carries it. WKWebView is unverified.

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

Decision (2026-09-11, platform architect): **B, then C** is accepted. The
Linaria migration will happen; step B lands first.

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
   for this reason. It happened again on 2026-09-14 in a subtler form: an
   embedded browser pane that is not on screen is a hidden document, and
   Chromium *throttled* rather than paused it — `requestAnimationFrame` ran at
   ~1 Hz (1,003–1,008 ms intervals) while worker-owned render surfaces kept
   drawing and publishing facts. A probe that did not check visibility reported
   a clean, slow page. `frameTelemetry` drops those intervals because
   `visibilityState` reads `hidden`; ad-hoc probes must do the same.
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

## 13. Implementation Ledger

Picking the work up in a new session: start with
[ui_render_performance_handover.md](ui_render_performance_handover.md) (how to
run the lab and device tooling, open experiments, pending user decisions).

The working record of this document. A row moves to **shipped** when code and a
test exist, to **measured** when a before/after capture exists, and to
**promoted** when its owning document (section 12) carries the rule. Emulator
and desktop captures are estimates: they drive tuning, and are labelled as such.

| Item | State | Where | Evidence and notes |
| --- | --- | --- | --- |
| P1 `frameTelemetry` | shipped | `runtime-sdk/ts/browser-host/src/frameTelemetry.ts` | 9 unit tests (display-relative slow frames incl. 120 Hz, hidden time excluded, LoAF counts, bounded ring, degraded runs, renderMarks). Not yet wired into an app or a gate. |
| P5 option B, theme reads | shipped | `ui-minimal/ts/src/theme.tsx` (`minimalVars`) | 359 theme interpolations became `var()` references; 1,122/1,122 render cases identical. |
| P5 option B, block variants | shipped | `ui-minimal/ts/src/variantRules.ts` | 16 block-returning prop interpolations became `:where([data-minimal-*])` rules; 2,240/2,240 cases identical under a per-element cascade harness (negative-tested). ~108 value-position prop functions remain by design (Linaria compiles those to variables). |
| P5 option C, Linaria | open | — | Decided (section 8). Needs a build pipeline change; blocked on nothing. |
| P3 rule 1, motion tokens as CSS | partial | `ui-minimal` theme | Durations and easings (`--minimal-ease-*`) exported; helpers still framer-motion. |
| P8 rule 1, inspection | shipped (local) | `tauri android build --features tauri/devtools` | No file change needed; build flag only, never for release. |
| P8 rule 4, COOP/COEP | shipped, **ineffective on Android** | Foundation native template, ChooseChow shell | Headers set; Android WebView does not isolate (see P8). Media route now sends CORP `cross-origin`; FSStore infers content types. |
| Section 7 harness | partial | session tooling, not yet in repo | adb + DevTools protocol: `gfxinfo` scroll scenario, runtime CSS A/B, plain-list control, Chromium trace, LoAF attribution. To be committed as tooling. |
| P2 `uiQuality` | shipped | `runtime-sdk/ts/browser-host/src/uiQuality.ts` | 9 unit tests: device-prior start (desktop high, flagship phone balanced, 4-core/2 GB low_power), save-data cap, reduced motion pinned, burst demote, forgive, slow promote, degraded/short windows ignored, platform floor, reasons logged. Demotes on slow-frame *share* per window (finding 5 below). Not yet consumed by an app: ChooseChow does not depend on browser-host. |
| P2 tier CSS in ui-minimal | shipped | `ui-minimal/ts/src/theme.tsx` | `low_power` swaps `--minimal-shadow-*` to 2–12 px blur and sets `--minimal-backdrop-filter: none`; `reduced_motion` drops backdrop blur. No component changed (option B dividend). Untiered render unchanged: 2,240/2,240. |
| P3 rule 4, pause loops | shipped (ui-minimal) | `MinimalSkeleton` | Shimmer stops on `low_power` and `reduced_motion`. Offscreen pausing waits on P4. |
| `frameClock` honest diagnostics | shipped | `browser-host/src/frameClock.ts` | A pulse that falls back with every capability present now reports `worker unavailable: pass configureFrameClock({ createWorker })` instead of `ok`. Regression test mocks full capabilities and a failing worker (`frameClock.diagnostics.test.ts`); browser-host suite 241/241. |
| `frameClock` subscription-time latch | fixed | `browser-host/src/frameClock.ts` | Root cause of the lab's `frames`-instead-of-`worker` failure. `ensurePulse()` calls `watchEpochs()` before `start()`; `watchEpochs()` reports diagnostics with the pulse still `stopped`; the clock treated any non-`worker` mode as a fallback, so **every consumer's clock latched onto animation frames at subscription**, and `start()`'s next report marked the fact `worker`/`ok`. The 120 ms grace window could never engage and nothing handed back. Fixed: `stopped` is ignored; a real no-tick grace window bridges on frames and marks the fact `frames`/`bridging`; the first worker tick hands the clock back and re-marks `worker`; the tick count resets with the clock. Evidence: `frameClock.bridge.test.ts` failed against the latch (fact read `worker`/`ok` while mode read `frames`) and passes after (clock tests 6/6, browser-host 243/243); the lab's frame-clock lane passes 2/2 in isolated Chromium. **Not a slow worker:** the lab's pulse latency probe (headless Chromium 151, isolated) measured the first worker tick at 57 ms, well inside the 120 ms window, with 12–20 ms intervals at 60 TPS; its diagnostics sequence `stopped → worker → worker → stopped` shows the pre-start `stopped` report that caused the latch. The frames bridge stays as protection for genuinely slow boots on weak devices. |
| ovasabi_v1 pulse worker wired | worker loads; clock latched until sync | `ovasabi_v1/frontend/src/lib/render/frameClock.ts` | The app shipped a correct `pulse.worker.ts` that nothing passed to the clock, so the single frame pacer ran on the main-thread lane for its whole life while its fact read `reason: "ok"`. Now configured on first import, and `pulse.worker.ts` loads 200. **Correction:** the `lane: "worker"` fact seen afterwards came from the pulse manager, not from what drives the clock — ovasabi_v1's vendored `frameClock` still has the subscription-time latch recorded in the next row, so its clock is self-driven on animation frames until that app syncs Foundation (blocked on its uncommitted vendored `server-kit` edits). Render surfaces on that page: `blackHole` WebGPU tier 0 at 40 Hz scale 0.62, `paper` WebGPU at 4 Hz, `footer` WebGL2 fallback at 20 Hz (desktop Chromium, cross-origin isolated). |
| P4 `cullSection` | shipped (primitive) | `ui-minimal` `MinimalCullSection` | `content-visibility: auto` + `contain-intrinsic-size: auto var(--minimal-cull-estimate)`; the estimate enters as a variable on `style`, so the rule stays static. SSR probe verified markup and CSS; 2,240/2,240 existing cases unchanged. Browser behaviour (skipping, find-in-page, scroll stability) needs the real-browser lab. Not yet adopted by an app. |
| P4 `virtualList` | open | — | — |
| Frontend lab (section 7, P10) | shipped, lanes 1–2 | `frontend-lab/` (foundation-only, like `servicebacked`) | Outside the sync allowlist; `tooling/foundation_ownership.tsv` marks it `foundation-test` / `foundation-only`; `make test-frontend-lab` and `make test-frontend-lab-browser`. `dom` lane (jsdom + Testing Library): 6/6 — cull markup and props, `uiQuality` on the real document element, reduced-motion and save-data read from the platform. `browser` lane (headless Chromium 1234, Playwright 1.62.1, Vitest browser mode): 8/8 — computed card shadow, backdrop blur and shimmer switch per tier (`low_power` cheap; `reduced_motion` keeps shadows; `balanced` unchanged), cull sections skip offscreen content, restore on scroll, hold scroll height, and remember real size under a wrong estimate. Pins React 18.3.1 runtime with React 19 types, matching `ui-minimal`. Negative-tested against planted source violations (tier selector renamed; `content-visibility: visible`): exactly the low_power tier test and both skip tests failed, the other five held; plants restored by hand and re-verified (browser 8/8, surface check, render harness 2,240/2,240). Lanes added since: `ssr` cascade eval (digest baseline 202 KB; negative-tested — a one-word Card shadow plant failed every Card case and printed the exact `box-shadow` change); frame-clock worker lane and pulse latency probe under COOP/COEP (isolation guarded by its own test); scripted scroll profile (`npm run profile`, capture bundle in `results/profile/`). Full lab 19/19. First profile bundle (Chromium 151, 412×915 @2.625x, CPU 1x, 80 sections, 3 runs after 1 warm-up): every variant p50 8.3 ms / p95 ≈ 9 ms / 0% slow / 0 LoAF, P1 repeatability PASS (p95 spread 0–3%) — repeatable, but a desktop at 1x does not separate variants, and node count is flat by design (`content-visibility` skips rendering, not nodes). Throttled, traced bundle (same setup, CPU 6x, P1 repeatability PASS, spread 0–1%): baseline p50 16.4 ms / p95 25 ms / **71% slow**; `cull`, `low_power` and `low_power+cull` all p50 8.3 ms / p95 ≈ 9 ms / **0% slow**. Median trace work: baseline style 952 ms, paint 1,556 ms, raster 1,010 ms; `low_power` style 0, paint 17.6 ms (−99%), raster 66 ms (−93%); `cull` style 245 ms (−74%), paint 720 ms (−54%) but raster 1,308 ms (+30%, open question). **P2's evidence gate — forcing `low_power` removes the slow frames `high` produces — passes in the lab.** |
| P2 on device (ChooseChow, emulator) | wired; gate not met on device | ChooseChow P7 build | `uiQuality` wrote `data-ui-tier="low_power"`, reason `device prior` (4 cores / 2 GB); no infinite animations running; loading fix confirmed (Home shows chef of the week and chefs rail, empty dish rail hidden). Interleaved Profile runs, 1 warm-up + 3 pairs: `low_power` p50 53–77 ms, `high` p50 17–69 ms, janky 7–79% on both, bimodal as before — **no separation on this screen**. Profile's cost on the emulator is not dominated by tier-controlled effects; next device step is a trace of which layers and paints remain. |
| P6 image pipeline, intrinsic sizes | shipped (primitive + lint) | `ui-minimal` `MinimalImage`; `frontend_surface_practices_check.mjs`; `frontend-lab/src/browser/imageStability.browser.test.tsx` | `MinimalImage`'s type has no unsized form: `width`+`height` (the browser derives the ratio and scales the box to the column) or `aspectRatio` (fills its column). Defaults `loading="lazy"`, `decoding="async"`; `priority` → eager + `fetchpriority="high"`; `reveal` fades in only after `img.decode()`; a plain `<img className>`, so every attribute reaches the element. Chromium geometry, 400 px column, 800×400 image not yet loaded at first measure: **unsized `<img>` moved the content below it 200 px; `MinimalImage` 0 px** (both forms). Lint: an `<img>` without `width` and `height` fails. Rules 3–5 (worker decode, decode queue, pixel budget) still open. |
| P7 raster rules and lint | shipped (Foundation sources) | `tooling/scripts/frontend_surface_practices_check.mjs` | Rules: no `transition: all`; no permanent `will-change`; `backdrop-filter` only through `var(--minimal-backdrop-filter, …)`; infinite animations carry a `low_power` + `reduced_motion` guard in the same template. Contracts: the `low_power` tier block swaps shadow and blur tokens; `uiQuality` divides `slowFrames` by `frames` and reads no percentile; a degraded `frameClock` names the failure. Each rule negative-tested against a planted violation (7/7 fail); Foundation passes. Scans Foundation roots only — app-code enforcement is still open. Enforcement manifest refresh pending (human-supervised). |
| P7 in ChooseChow app styles | shipped (unmeasured) | ChooseChow frontend | All 17 `backdrop-filter` read `--chow-backdrop-filter` (none on `low_power`/`reduced_motion`, with more opaque glass fills so text stays legible); heavy shadows through `--chow-shadow-*` and `minimalVars.shadow.*` (31 raw theme reads converted, so ui-minimal's tier swap reaches app code); 8 infinite animations paused on low tiers; 4 `transition: all` replaced with explicit lists. `createUiQuality` runs once before first render; windows come from one passive capture scroll listener on `#main-content` (300 ms idle) and route changes (first route skipped as cold). `tsc -p tsconfig.app.json` 0 errors; vitest 36 files / 167 tests. Lesson: the app's `npm run typecheck` checks nothing (root tsconfig is `files: []` with references), so earlier "typecheck passes" claims made with it were unverified. Open: `balanced` has no CSS; `contain: layout paint` on list cards needs a visual check. |
| P9 warm-up and hitch ledger | open | — | — |
| Lab GPU backend (finding 7) | fixed in harness | `frontend-lab/profile/scrollProfile.mjs` (`PROFILE_GPU`), `vitest.config.ts` `gpu` project | Headless Chromium's default backend is **SwiftShader, a CPU Vulkan**: WebGL2 renderer "SwiftShader driver", no WebGPU adapter (a *fallback* one with `--enable-unsafe-webgpu`). Every raster number in the 09-14 bundles above was software raster, throttled with the page. Same scenario, CPU 6x, 3 runs after a warm-up, on ANGLE/Metal (M1 Pro): baseline RasterTask **50 ms vs 1,531 ms** on SwiftShader; `cull` still adds GPU work (Raster 61 vs 50, GPUTask 217 vs 158 ms) but in absolute terms it is ~70 ms per pass. Bundles now record the renderer and adapter (capture schema, `gpu_practices.md`). Emulator WebView 133 (`-gpu host`): `navigator.gpu` present, `requestAdapter()` null; WebGL2 through the emulator's GLES translator — **no WebGPU on this Android WebView** (section 11 question 1, emulator only). |
| Skeleton shimmer (finding 8) | fixed; measured | `frontend-lab/profile` (`PROFILE_GPU=metal`) | Tier profile after the fix (Metal, CPU 6x, 3 runs after a warm-up, P1 repeatability PASS): baseline p50 8.5 / p95 17.5 ms / **28% slow** (was 54–65% with the always-running sweep), style 213 ms, PrePaint 526 ms; `cull` 9% slow; `low_power` and `low_power+cull` 0% slow. What remains in baseline is the on-screen sweep's layer upkeep (PrePaint) — the tier still has a job, but a small one. |
| Skeleton shimmer, shipped design | fixed | `ui-minimal` `MinimalSkeleton`; `frontend-lab/profile` `PROFILE_SET=skeleton`; `frontend-lab/src/browser/skeletonOffscreen.browser.test.tsx` | **Shipped design: the sweep pauses while the placeholder is off screen** (one shared `IntersectionObserver`, 25% margin, marks `data-minimal-offscreen`; `animation-play-state: paused` on `::after`; no observer ⇒ runs as before). Second interleaved A/B, same settings, 7 variants: `paused` p50 8.3 / p95 9.2 ms / **0% slow**, style 0, repeatable (1% spread) — identical to `static` and `none`; `transform+cull` 5% slow, p95 9.3–15.9, style 176 ms, not repeatable; always-running transform 62%, bgpos 71%, pulse 55% slow. Browser test: off-screen marked and paused, on-screen running, resumes on scroll; planting a wrong attribute in the pause rule failed it. SSR/DOM lanes unchanged (no winning-style change). History below. |
| Skeleton shimmer, history | superseded | — | On the real GPU, `low_power` took baseline UpdateLayoutTree 876 → 0 ms and Paint 1,477 → 18 ms per pass. Zero style work can only come from stopping an animation: the sweep animated `background-position`, which never composites, so 240 skeletons restyled and repainted every frame. **The lab's P2 separation was the shimmer, not shadows or blur** — which is why the ChooseChow Profile screen, with no infinite animations, did not separate (handover experiment 1). First attempt: highlight on `::after` animating `transform`. Against the *previous session's* bgpos bundle it looked worse (p50 15.8 → 24.9 ms), but cross-session comparisons drift. **Interleaved in one session** (`PROFILE_SET=skeleton`, CPU 6x, Metal, 3 runs after a warm-up): bgpos p95 33.9 ms / 68% slow (style 2,217, paint 930, raster 35, GPU 120 ms); transform p95 26.0 / 59% (style 816, prepaint 815, layerize 701, paint 18); opacity pulse p95 25.9 / 53% (style 2,021, layerize 161, paint 15); static and none both p50 8.3 / p95 9.2 / **0% slow**. So the transform design is better than bgpos (paint −98%, p95 −23%), but **no animated design is cheap: 240 running animations cost 0.8–2.2 s of main-thread work per pass whether or not they composite**, and none reach budget. The lever is not the property; it is not running animations nobody can see. Offscreen-paused and cull variants under test. Repeatability fails for every animated variant (frame timing is noisy while 240 animations run), so these are ranges, not gates. SSR re-baselined for the attempt (64 cases, all `MinimalSkeleton`); lint tier-guard rule negative-tested against the `::after` selector. |
| Render-surface lane on real GPU (finding 9) | shipped (lab lane) | `frontend-lab/src/gpu/` | New `gpu` lab project (ANGLE/Metal, WebGPU): a lab worker built from the SDK (`createRenderSurfaceWorker` + `serveRenderSurface`) with a fixed-cost fragment load, 4/4. Measured: shared worker **1 device for 3 surfaces**; prewarm first frame **p50 10 ms vs 32 ms cold** (5 interleaved pairs after a warm-up). |
| GPU backpressure (finding 10) | fixed | `browser-host` `renderSurfaceClient.ts` (`RenderSurfacePass.settled`) | **The ladder could not see the GPU.** WebGPU `draw` returns at `queue.submit`, and the loop timed `draw`. Load 40× on real hardware: GPU 1,276 ms p50 / 2,528 ms max per frame, the loop reported 40.2 Hz, rung 0, **101 frames queued**. With `settled` (`queue.onSubmittedWorkDone()`), a tick finding the last frame unsettled draws nothing and counts as a miss: same load demoted to rung 1, GPU p50 15.7 ms, 39.2 Hz, ≤1 frame in flight; light load untouched. 2 s watchdog; epoch guard for a retired pass's late frame; shared-worker wrapper forwards `settled`. 4 unit tests; planting the old behaviour failed exactly those. Open: WebGL2 has no working GPU barrier yet (`gl.finish()` ~0 ms at 40× on ANGLE/Metal; a fence never signalled in the lab). |
| Shared device release (finding 11) | fixed | `browser-host` `renderSurfaceWorker.ts` (`releaseWhenIdleMs`) | Real hardware: three hosts disposed, every pass retired, **device still alive** — release was keyed to `serve()` disposers, which production workers never call. Now keyed to live passes (a build in flight counts) plus an idle window (default 10 s, sized by the prewarm numbers above). Lab: kept inside the window, then `releases: 1`, driver `lost: "destroyed"`. 6 unit tests; planting the old behaviour failed the 3 release tests. |
| **Rounded + shadow is the device cost (finding 12)** | fixed (Foundation + ChooseChow) | `ui-minimal/ts/src/globalStyles.ts` tier blocks; ChooseChow `styles/appReset.ts`; harness `frontend-lab/device/{layerPaintProfile,paintShadowDepth,scrollMatrix,soak,coldScroll}.mjs` | **A blurred shadow behind a ROUNDED rect costs ~25 ms of raster per screen; the same shadow on a square rect costs ~1 ms.** Measured on device (Android 16, WebView 133, ChooseChow Orders, 16 shadowed elements, per-layer paint replay via `LayerTree.makeSnapshot` + `profileSnapshot`, 3 repeats × 2 interleaved rounds, spread ≤3%): baseline two-layer 68 ms; 20 px blur 41; 8 px 32; 3 px 30; **0-blur hairline 6.9**; no shadow 5.8; no shadow + 1 px border 5.2; **8 px blur with `border-radius: 0` → 6.7**. Rounded corners alone are free (`no-radius` with the shadow kept: 5.3 ms), and each shadow layer repeats the penalty (two-layer ≈ 2 × one-layer). Generalises: Orders 68 → 5.8, mealplan 30 → 1.8, profile 22 → 1.3, subscriptions 23 → 1.1 with shadows off, i.e. **shadows are >90% of page paint on every screen measured**. Fix: the cheap tiers draw elevation with an unblurred offset edge instead of a small blur (the old low-power values — "a few pixels of blur" — still paid the full penalty: 30 ms vs 6.9), and `balanced` (which had no CSS, §5 open question 6) now collapses multi-layer shadows to one. After, same method: Orders low_power 31 → **7.2 ms**, profile low_power → **2.1**; balanced Orders 68 → **31**, profile 22 → **10.7**. This is the mechanism behind "blur and shadow removal helps on some runs only" (capture note 4): earlier A/Bs changed shadows while leaving rounded corners, and read whole-frame time, which on this emulator cannot see it. |
| Why whole-frame A/B could not find it | method | `frontend-lab/device/scrollMatrix.mjs`, `soak.mjs` | Two emulator lanes both hide content cost. **Host GPU:** everything draws at 17 ms p50 / ~2% janky, plain list and real page alike — an M1 Pro fills the page's shadows inside budget, so no variant separates. **SwiftShader** (CPU raster, weak-GPU proxy): every variant *and the plain-list control* read p50 ~61 ms, because full-screen software fill (~33 ms GPU phase) dominates; the `draw` phase is noisy at 2–13 ms for the same variant. Per-layer paint replay is the instrument that works, because it times the page's recorded paint ops rather than the device's fill. |
| Bimodal device runs — resolved as environment (finding 5 follow-up) | closed | `frontend-lab/device/soak.mjs`, run logs in `results/device/` | The 60–200 ms runs are **not the app**. A 45-minute soak (44 user-like tours, gated on a plain-list control and a non-WebView control) held 17 ms p50 throughout, with app PSS flat at 94 MB and the WebView renderer 261 → 280 MB. The earlier slow sessions were a **degraded emulator**: in that state the plain-list control itself read p50 89–200 ms, force-stopping the app did not recover it, and only an emulator reboot did. Host contention (a Docker VM at a full core, load 10) was present when it appeared but the state outlived it; an attempted repro was inconclusive (Docker failed to restart cleanly). **Harness rule, effective now: every device capture must carry a plain-list control and the host load / guest memory of the run (`scrollMatrix.mjs` records them); a run whose control is over budget is not evidence.** Snapshot restore was ruled out — the emulator's own log shows the quick-boot snapshot failed to load, so both sessions were cold boots. |
| ovasabi_v1 `blackHolePass` audit | open (app) | `ovasabi_v1/frontend/src/lib/render/passes/blackHolePass.ts` | Requests its own adapter and device per pass inside the shared worker and `dispose` never destroys it (a device leaked per remount; violates `gpu_practices.md` rule 8 and the disposal contract, whose lint scans Foundation only); allocates a render-pass descriptor per frame; no `settled`, so it is exposed to finding 10. Fix after the ovasabi_v1 Foundation sync is unblocked. |

### ChooseChow captures (emulator estimates, 2026-09-11 to 09-14)

Pixel-class AVD, Android 16, WebView 133, host GPU, Profile screen, scripted
scroll (5 swipe pairs), `gfxinfo`:

| Build / condition | p50 | p99 | Janky |
| --- | --- | --- | --- |
| Release before option B (09-11) | 200 ms | 650 ms | 100% |
| After option B + shell headers (09-14) | 89–117 ms | 200–500 ms | 90% |
| Same, blur and shadows disabled at runtime | 81–117 ms | 250–350 ms | 72–92% |
| Control: plain 200-row list, same WebView | 23 ms | 150 ms | 5% |

What the captures say, and what they do not:

1. The WebView on this emulator can scroll near budget (control), so the cost
   is the screen's content, not the host.
2. The overworked lane is raster and composite: `DrawFn_DrawGL` on the app's
   render thread dominates the trace, and long animation frames carry almost no
   script (blocking ≈ 0 ms on most runs). JavaScript is not the bottleneck.
3. Runs are bimodal: the same configuration measured 17 ms and 121 ms p50 on
   consecutive runs. Single runs are not evidence; interleave baseline and
   variant, settle 15 s after navigation, and report ranges.
4. Blur and shadow removal helps on some runs only, so it is not the whole
   story; the remaining candidates are layer count and invalidation of the
   sticky, rounded, shadowed header over the scroller.
5. Page-side frame timing under-reports compositor jank. In the same scroll,
   `requestAnimationFrame` intervals read p50 17 ms (p90 50–117 ms) while
   `gfxinfo` read p50 93–150 ms: the renderer main thread kept its cadence and
   the app's render thread did not. The slow-frame *share* still separates a bad
   screen (22–34%) from the control (~5%). Consequences: P2 demotes on slow-frame
   share per window, never on the median; inside native shells the platform's
   frame metrics (P8) are the authoritative input; and a P1 report from a WebView
   is a lower bound, not the user's experience.
6. **Most of the slow numbers were cold.** Four interleaved baseline/low-power
   pairs, 15 s settle each, same screen: the *baseline* alone drifted from p50
   117 ms (95% janky) to 17–27 ms (8–42% janky) across the session, and the
   emulated low-power effects beat baseline clearly in only one pair. The first
   minutes after install or launch pay JIT, shader-cache, and raster-cache
   warm-up that steady state does not. Harness rule, effective now: run the
   scenario to warm before any capture, record cold and warm separately, and
   treat the cold run as a P9 first-use hitch rather than as scroll performance.
   Warm steady state for ChooseChow Profile is near budget on this emulator;
   cold start is the real defect to chase.

## 14. Dependency Audit: framer-motion and styled-components (2026-09-15)

Question from the platform architect: there was a complaint about framer-motion
and styled-components; do they need to go, and can native elements reduce the
dependencies?

The complaint has a record. ChooseChow commit `dc4c567` moved its modal from
framer-motion to CSS keyframes "for smoother transitions", and `9c370b4` removed
`AnimatePresence` from page routing. Both were fixes for felt jank, made in the
app, without a measurement to say why. This section supplies the why.

All numbers below: frontend lab, headless Chromium 151 on ANGLE/Metal (M1 Pro),
`frontend-lab/src/gpu/runtimeCost.gpu.test.tsx` and
`results/bundle/measure.mjs` (rolldown, minified, production). Desktop numbers:
the ratios transfer, the milliseconds do not — a floor Android phone is several
times slower, and its WebView parses JavaScript slower still.

### 14.1 What they weigh

| Bundle (min + gzip) | gzip |
| --- | --- |
| framer-motion, as `ui-minimal` imports it (`motion`, `AnimatePresence`, `useScroll`, `useSpring`, `useTransform`, `useVelocity`, `useReducedMotion`) | **47.6 KB** |
| framer-motion, `m` + `LazyMotion(domAnimation)` | 31.5 KB |
| styled-components (+ stylis) | 12.2 KB |
| react + react-dom | 43.6 KB |
| `@ovasabi/ui-minimal`, everything, dependencies bundled | 125.4 KB |

**framer-motion as used is heavier than React and ReactDOM together**, and
`motion-dom` alone is the largest module in ui-minimal's bundle. For a 125 KB
UI kit, roughly 38% is animation runtime.

### 14.2 Which lane motion actually runs on

Measured by counting writes to each element's `style` attribute while it
animates (a JavaScript-driven animation writes once per frame; a CSS or WAAPI
animation writes nothing) and reading the properties each WAAPI animation
carries:

| Shape | Where ui-minimal / apps use it | style writes | Compositor animation carries |
| --- | --- | ---: | --- |
| framer `opacity` + `scale` | `popVariants` (FloatingPanel, ActionModal), tooltips | 49 | opacity only |
| framer `opacity` + `y` | `slideUpVariants` (Header, DisplaySection), `pageVariants`, ChooseChow's old modal | 51 | opacity only |
| framer `height: auto` | `MinimalExplainer` | 50 | opacity only |
| framer spring `scale` | `spring` (SegmentedControl, calendar selection) | 61 | nothing |
| CSS transition `opacity` + `translate` | — | **0** | opacity, translate |
| CSS `interpolate-size` `height: auto` | — | **0** | height |

**framer-motion runs independent transforms (`x`, `y`, `scale`, `rotate`),
`height: auto` and springs from JavaScript on the main thread, every frame.**
Only opacity is handed to WAAPI. Motion's own performance guide says the same:
individual transforms animate CSS variables, "and currently these are not
accelerated". A main-thread stall — a projection delta, a route chunk, React
committing — therefore freezes these animations mid-flight, and on a floor
phone the stall is the common case. That is the ChooseChow complaint, measured.

Not yet measured: frame-level proof that the CSS versions keep time through a
stall (a timeline's `currentTime` cannot be read across a synchronous block on
the main thread; it needs a trace — handover experiment list).

### 14.3 What it costs per instance

| Workload | cold mount | warm mount | style writes |
| --- | ---: | ---: | ---: |
| 300 cards as `styled(motion.section)` with a mount fade (MinimalCard's shape) | 26.3 ms | 8.6 ms | 300 |
| 300 cards as `styled.section` + CSS `@starting-style` fade | **6.8 ms** | **3.4 ms** | **0** |
| 1,000 buttons, styled-components interpolated props | 21.3 ms script, 24 rules injected | 7.0 ms | — |
| 1,000 buttons, static styled template + CSS variables | 11.6 ms, 1 rule | 6.7 ms | — |
| 1,000 buttons, plain class + CSS variables | **6.6 ms**, 0 rules | **4.9 ms** | — |
| Enter/exit open, framer `AnimatePresence` | 3.0 ms script to first frame | | |
| Enter/exit open, CSS `@starting-style` / WAAPI | 1.3 / 1.1 ms | | |

Every `MinimalCard` is a framer-motion component, so a feed pays framer's
per-instance setup once per card: **3.9× the cold mount cost** of the same card
with a CSS fade. styled-components' cost is concentrated on the cold path
(3.2× plain CSS on first mount, converging once its rule cache is warm).

One counter-intuitive result: changing inline CSS custom properties on 1,000
elements cost *more* style recalculation (7.4 ms) than swapping interpolated
classes (3.5 ms). Per-element variables are right for continuous values; for
discrete variants, attribute or class selectors — ui-minimal's `variantRules`
(`:where([data-minimal-*])`) — are cheaper. Option B already made that choice.

### 14.4 Native replacements, site by site

Support floor: Android WebView is evergreen Chromium (the emulator runs 133).
iOS shells target **iOS 14.0**; WKWebView follows the OS's Safari. iOS 26 is
~79% of iPhones and iOS 18 ~14% (June 2026); devices on iOS 16 or older are
~8%. Anything below Safari 17.5 gets the P3 fallback — the instant state change.

| ui-minimal site | framer feature | Native replacement | Engine floor |
| --- | --- | --- | --- |
| `MinimalButton` hover/tap scale | `whileHover`, `whileTap` | `:hover` / `:active` + `transition: scale` | all |
| `Chevron` rotate | `animate={{ rotate }}` | `transition: rotate` on an attribute | Safari 14.1 |
| Button `Spinner` | infinite JS `rotate` | CSS `@keyframes` rotate, paused off screen and on low tiers (finding 8) | all |
| `MinimalCard`, `Header`, `DisplaySection` mount fade/slide | `variants` + `initial`/`animate` | `@starting-style` + `transition` on `opacity`/`translate` | Safari 17.5, Chrome 117 |
| `FloatingPanel`, `Tooltip`, `ActionModal` enter **and exit** | `AnimatePresence` | `<dialog>` / `popover` in the top layer, `transition-behavior: allow-discrete` on `display` and `overlay`; exit plays before removal with no React presence tracking | popover Safari 17, `@starting-style` 17.5 |
| Tooltip / dropdown placement | `getBoundingClientRect` + state (a forced layout per open) | CSS anchor positioning | Safari 26, Chrome 125; keep the measured path as fallback |
| `MinimalExplainer` expand | `height: auto` | `grid-template-rows: 0fr → 1fr` transition (all engines), `interpolate-size` where supported | all / Chrome 129 |
| Calendar month slide | keyed `AnimatePresence` | same-document View Transitions (`document.startViewTransition`) | Safari 18, Chrome 111 |
| Calendar selection, `SegmentedControl` indicator | `layoutId`, `layout` (FLIP: measures on every render) | the indicator already knows `$index`: `translate: calc(var(--index) * 100%)` + transition; or a `view-transition-name` | all / Safari 18 |
| `useMinimalScrollFeedback` | `useScroll` + `useVelocity` + `useSpring` | **no users in Foundation, ChooseChow or ovasabi_v1**; remove, or a scroll-driven animation if a use appears | Safari 26, Chrome 115 |
| `motion.ts` token helpers | `Transition`/`Variants` objects | CSS custom properties already exported (`--minimal-ease-*`, durations) | all |

Every site has a native equivalent. None needs gesture physics or drag, which
is the one job P3 reserved for a JavaScript motion library.

### 14.5 Recommendation

1. **framer-motion: remove it from `ui-minimal`.** It is the largest dependency,
   it runs the shapes ui-minimal uses on the main thread, it costs 2.5–3.9× per
   mounted instance, and every use has a native replacement on the engines the
   products target. Apps that want gesture physics may still import it
   themselves; the kit should not make every app pay for it. Order: `MinimalCard`
   and button motion first (highest instance count), then overlays
   (`<dialog>`/`popover`), then calendar/segmented (View Transitions,
   index-driven translate), then delete `useMinimalScrollFeedback`.
2. **styled-components: keep the decided path — B, then C (Linaria).** Its runtime
   cost is real but concentrated on the cold path and on interpolated props,
   which option B already removed from ui-minimal; the remaining reasons to go
   are the 12 KB, the CSP exception, and maintenance mode. Linaria keeps the
   `styled` syntax, so the migration is mechanical. Do not replace it with
   another runtime library.
3. **Native elements are not only a dependency win.** `<dialog>` and `popover`
   bring the top layer, focus handling, `Escape` and light dismiss that
   ui-minimal currently reimplements in JavaScript, and anchor positioning
   removes the forced layout each tooltip open pays today.
4. **Gate it.** Extend `frontend_surface_practices_check.mjs`: no new
   `framer-motion` import under `ui-minimal/` once the migration starts (ratchet),
   and no `motion.*` independent transforms in Foundation sources.

### 14.6 Implementation (landed 2026-09-15, uncommitted)

Decision (platform architect, 2026-09-15): **breaking removal now**, and keep
the iOS 14.0 deployment target — older devices must stay compatible and
efficient, not merely tolerated.

What changed in `ui-minimal`:

1. `framer-motion` is no longer a peer dependency or an import. `motion.ts` is
   CSS: `minimalEnter.{fade, pop, slideUp, page, tooltip, slideX}` style
   fragments (keyframes on insertion, `backwards` fill, individual `translate`
   / `scale` so component `transform`s are never overridden, off for reduced
   motion and the `reduced_motion` tier, off per element with
   `data-minimal-enter="false"`), `minimalMotionMs`, `useMinimalReducedMotion`,
   and `useMinimalMotion()` → `{ reducedMotion, ms }`.
2. `useMinimalPresence(open, exitMs)` (`presence.ts`) replaces
   `AnimatePresence` where an exit is real (`MinimalExplainer`, via a
   `grid-template-rows` transition).
3. Public types: `MinimalHeader/Button/Card/DisplaySection/ScrollMain` props
   extend React's HTML attributes, not `HTMLMotionProps`. `Card`, `Header` and
   `DisplaySection` gain `enter?: boolean` (replaces `initial={false}`).
   Removed: the `create*Transition` / `create*Variants` helpers,
   `useMinimalScrollFeedback`, `MinimalScrollFeedbackSurface` (no users).
4. Per site: Chevron and spinner are CSS (spinner stops on low tiers);
   Button hover/press were already CSS and the framer layer on top is gone;
   SegmentedControl's indicator is an index-driven `translateX` transition;
   the calendar month panel enters with `slideX` (the simultaneous exit slide
   was dropped); calendar selection pops in (the spring between days was
   dropped); overlays enter with `pop`/`tooltip`/`fade`.

Two things the audit found while doing it:

- **The overlay exits never ran.** `ActionModal`, `Tooltip` and the dropdown
  panel return `null` or unmount outside their `AnimatePresence`, so framer's
  exit variants were dead code; enter-only CSS is full parity.
- **framer made server-rendered cards invisible.** The SSR cascade shows the
  old `MinimalCard` HTML carried `style="opacity:0"` until hydration, and
  buttons carried a framer-added `tabindex="0"`. Both are gone.

Bundle, same rolldown measurement as 14.1: `@ovasabi/ui-minimal` with its
dependencies **125.4 KB → 80.9 KB gzip (−35%)**; `motion-dom` and
`framer-motion` are gone from the graph.

Mount, 300 cards, interleaved (lab `gpu` project): the shipped `MinimalCard`
now mounts **11.6 ms cold with 0 style writes**, against 26.4 ms and 300 writes
for the framer-motion shape. Warm it is 10.1 ms against 9.1 ms, and a plain
card with a trivial template is 2.5 ms — but those are not like for like: the
shipped card carries its full styled-components template (surface variants,
tiers, theme provider). That remaining gap is the styled-components runtime,
which is the case for option C (Linaria) rather than for more motion work.

Verification: ui-minimal `tsc` clean; lab `ssr` re-baselined (only the changed
components moved — Card, Header, DisplaySection, Button, Dropdown, TimePicker,
SegmentedControl, Explainer, Calendar, ScrollMain; `ScrollFeedbackSurface`
removed), `dom` 6/6, `browser` 13/13; surface lint passes, with the new ratchet
"ui-minimal imports no JavaScript animation runtime" negative-tested (a planted
import failed it). Template (`templates/frontend`) and
`frontend_manifest_sync.mjs` no longer require or alias framer-motion; apps that
import it for their own code keep it.

Apps (the 11 vendoring projects were surveyed; three used the retired API):

| App | Change | Evidence |
| --- | --- | --- |
| ChooseChow | synced; 23 `initial={false}` → `enter={false}` (incl. two on a styled `MinimalCard`); `PageTransition` is a CSS transform-only keyframe; `useChoreography` builds its framer transition from theme tokens; route-transition source test pins the CSS form | `tsc -p tsconfig.app.json` clean, vitest 36 files / 168 tests |
| trotters_v1 | synced (its 23 local vendored edits were byte-identical to Foundation); `PageTransition` and `motion.ts` build framer variants from theme tokens (it relies on `AnimatePresence` exits) | `tsc` clean, vitest 6 files / 35 tests |
| pronto_v1 | synced; `CwfViewer` uses `minimalEnter.slideUp` (framer import dropped); `ExecutiveBulletinBoard` owns its row fade (keeps framer for `layout="position"`) | Differential `tsc`: against the synced vendored source 12 errors, all harness-only missing Node types; the same harness against the stale install shows those 12 plus the `minimalEnter` error. **Its own `tsc` and tests need a reinstall** — `node_modules` is a stale pnpm tree while the lockfile is npm's |

### 14.7 Linaria spike and animation primitives (2026-09-15)

**Linaria (option C) — the pipeline works.** `frontend-lab/linaria/` builds a
MinimalCard-shaped component with `@linaria/react` 8.2.0 and `@wyw-in-js/vite`
2.5.1 (Node ≥ 22.12; the machine runs 24.1). wyw-in-js evaluated
`minimalVars` from Foundation's TypeScript source outside the project root, the
extracted CSS carries the theme variables and fallbacks, keyframes are scoped
automatically, and prop interpolations became per-instance CSS variables
(`padding: var(--l1abmdf9-0)`). A bundle with only the Linaria card contained
neither `styled-components` nor `stylis`. Build 0.7–1.9 s for the spike.

Mount, production build, 300 cards, real GPU, 5 interleaved fresh pages (cold,
median) plus 45 repeat mounts (warm, fastest):

| Engine | cold script | cold style + layout | rules injected | warm script | warm style + layout |
| --- | ---: | ---: | ---: | ---: | ---: |
| Linaria card | **6.7 ms** | 12.3 ms | **0** | **0.6 ms** | 8.0 ms |
| styled-components `MinimalCard` | 9.4 ms | 13.5 ms | 10 | 1.7 ms | 8.4 ms |

Script time falls 29% cold and 65% warm; style and layout are the engine's and
barely move, as expected. Caveat: the spike card is simpler than the shipped
`MinimalCard` (fewer surface variants), so the gap for real primitives is an
estimate until one is ported.

What the migration has to solve, found by the spike and the inventory:

1. **Every consumer's build and test runner needs the wyw-in-js plugin.**
   Linaria's `styled` throws at runtime without extraction, so each app's
   `vite.config.ts` and `vitest.config.ts` (and the lab's `ssr` cascade eval,
   which reads styled-components' SSR sheet) must change together. That is a
   template + `update-project.sh` change, not a ui-minimal change alone.
2. **Evaluation pulls runtime modules.** Importing `@ovasabi/ui-minimal`'s
   index made wyw-in-js evaluate `motion.ts` (styled-components) and
   `runtimeStyle.tsx` (react) at build time — it worked, with warnings. Token
   modules should be importable without React or a styling runtime
   (`importOverrides` or a `tokens` entry point).
3. **ui-minimal's styled-components surface:** 141 `styled.*`, 34 `css`, 8
   `keyframes`, 3 `createGlobalStyle` (→ `:global()`), 8 `ThemeProvider` / 1
   `useTheme` (→ CSS-variable scopes; the components already read only
   `minimalVars`), 11 `as` (supported). The three block-returning functions are
   inside `variantRules`, evaluated at build time, so they are compatible.
4. **Apps keep styled-components for their own code** (ChooseChow 61 files,
   ovasabi_v1 50) until they migrate; both can coexist, so the kit can move
   first.

**Animation primitives.** `ui-minimal/ts/src/timeline.ts`:
`createMinimalTimeline` (GSAP-shaped positions `"<"`, `">"`, `"+=n"`, labels;
stagger from start/end/centre; seek, progress, reverse, timeScale, repeat,
yoyo; forward callbacks), `minimalEntry` / `minimalExit`, `minimalKeyframes`
presets on compositor properties only, and `useMinimalTimeline`. Every tween
is a native `Element.animate()`; the group is kept in lockstep by using each
tween's `delay` as its position and one shared `startTime`, with a null-target
master animation for `finished` and repeats. No per-frame JavaScript; `hold`
commits end styles and cancels instead of leaving open-ended fills; reduced
motion jumps to the end; non-compositor properties are reported in `issues`.
Two traps fixed while testing: `Element.animate()` rejects `var()` easings
(tokens are resolved from the root's computed custom properties), and a
callback at the very end could lose the race with `finished`. Lab browser lane:
9 tests.

Still open: overlays onto `<dialog>` / `popover` (`@starting-style` exits where
supported, instant below), anchor positioning with the measured fallback, View
Transitions for the calendar, trotters' JS page transitions, a trace proving
CSS motion keeps frames through a main-thread stall, and the same runtime-cost
tests on the emulator WebView.

### 14.8 Linaria port of ui-minimal (landed 2026-09-15, uncommitted)

Decision (platform architect, 2026-09-15): proceed with option C.

**The kit no longer uses styled-components.** Every `styled.*` in
`primitives.tsx` and `interactions.tsx` is `@linaria/react`'s; styles are
extracted at build time by `@wyw-in-js/vite`. What changed to make that
possible:

1. `css` fragments and `variantRules` return plain strings (evaluated at build
   time). No fragment contained a runtime prop function, so this was
   mechanical.
2. Keyframes live inside the template that uses them (Linaria scopes keyframe
   names per component; a keyframe declared under `:global()` and referenced by
   name elsewhere does not resolve — spike finding). `minimalEnter` fragments
   carry their own `@keyframes`.
3. `theme.tsx`: the global stylesheet (base-theme variables, quality tiers,
   reset) is static extracted CSS; `MinimalThemeProvider` / `MinimalThemeScope`
   / `useMinimalTheme` use a React context; `MinimalGlobalStyles` renders a
   small `<style>` only for a non-base theme. Build-time values come from the
   runtime-free `tokens.ts`.
4. `@ovasabi/ui-minimal/styled-components` (`MinimalStyledThemeBridge`): mount
   it inside `MinimalThemeProvider` and application styled-components keep
   receiving the resolved theme (123 app files read it). styled-components is
   now an optional peer.
5. Two extractor constraints found and fixed: inline `type` import specifiers
   (`import { type X }`) break wyw-in-js's evaluation parser — split into
   `import type`; and build-time interpolations containing callbacks must be
   hoisted out of the template into module constants (20 in primitives, 1 in
   interactions).
6. Vendor prefixes off (`prefixer: false`): no target engine needs them.

**Equivalence proof.** `frontend-lab/src/ssr/linariaPort.eval.test.ts` builds
the kit as an app would, renders the full 2,240-case sweep from the built
module against the one extracted stylesheet, and compares every case with the
styled-components cascade saved before the port (winning value of every
property on every element, keyframe bodies, markup): **2,176 identical; the 64
others are one accepted shape** — `MinimalFieldGrid` given an array where it
takes a number, which both engines turn into garbage differently. The harness
gained engine-neutral canonicalisation (component identity classes, Linaria's
per-element custom properties resolved with the element's own value first,
keyframe names replaced by bodies, invalid HTML attributes dropped on both
sides). The digest eval now renders the built kit and was re-baselined on that
proof.

Side effects on markup, all improvements: sweep props no longer leak into the
DOM as junk attributes (`options="[object Object]"`), because Linaria forwards
only valid HTML props.

**Sizes** (production library build, `node src/ssr/buildKit.mjs`): extracted
CSS **62.4 KB → 7.9 KB brotli**, loadable in `<head>` before any script; kit JS
140.1 KB → 30.3 KB brotli, with neither styled-components nor stylis in it.

**ChooseChow pilot (2026-09-15): green.** Vendored sync, `frontend_linaria_patch.mjs`
(Vite + Vitest plugin, `AppThemeProvider` bridge), manifest sync, `npm install`:
`tsc -p tsconfig.app.json` clean, vitest **36 files / 168 tests**, production
build succeeds. Production sizes: CSS 61.1 KB → **7.9 KB brotli** (static
file); `ui` chunk (styled-components still used by the app, plus ui-minimal)
48.2 KB br; `vendor` 15.2 KB br; the app's main chunk **192.1 KB br (938 KB
raw)** — which is now the dominant loading cost and a code-splitting job, not a
kit one. The pilot forced four fixes, each now in Foundation:

1. Build-time evaluation of a *vendored* kit runs from its real path
   (`app/foundation/ui-minimal/ts/src`), where neither React nor extensionless
   TypeScript imports resolve. The modules a template reads are now
   runtime-free (`tokens.ts`, `motionStyles.ts`, `globalStyles.ts`,
   `variantRules.ts`) and import each other with explicit `.ts` extensions
   (every app's tsconfig already allows them; Node 24 strips types natively).
2. Tests that render inside `MinimalThemeProvider` directly lose the
   styled-components theme their app's own templates read; they must use the
   app's bridged `AppThemeProvider` (2 files in ChooseChow; none elsewhere).
3. The manifest sync moved the app from Vitest 4 to the template's pinned 5,
   whose jest-dom matcher types come from `@testing-library/jest-dom/vitest`
   (template `src/test/setup.ts` updated).
4. `frontend_linaria_patch.mjs` + `patch_frontend_linaria` in
   `scaffold_managed_patches.sh` deliver the plugin and bridge in place,
   idempotently, and report shapes they will not rewrite (dry-run on three app
   shapes: extra plugins, semicolon style, no `AppThemeProvider` alias).

**New adoptions start on Linaria (2026-09-15).** The frontend template no longer
depends on styled-components: `package.json` drops it, the Vite/Vitest configs
drop its alias and dedupe and let wyw-in-js transform the app's own `src` as
well as the kit, `App.tsx` is written with `@linaria/react` and `minimalVars`,
`styles/theme.ts` is `MinimalThemeProvider` again with no styled-components
type augmentation, `frontend_manifest_sync.mjs` no longer requires it, and
`project_scaffold_check.sh` checks for `@linaria/react` instead. The styling
guide's §3 is now the Linaria format. What remains of styled-components in
Foundation is deliberate: the opt-in `@ovasabi/ui-minimal/styled-components`
bridge (optional peer) for existing apps' own code, the managed patch that
installs it, and the lab's styled-components reference used by the equivalence
proof.

**Still to do for apps:** every app's `vite.config.ts` and `vitest.config.ts`
need the wyw-in-js plugin (template + `update-project.sh`), the Linaria and
Babel preset dependencies, Node ≥ 22.12 in CI/Docker (core CI already runs 24;
the template Dockerfile builds on `node:22-alpine`), and
`MinimalStyledThemeBridge` in each app's root so their own styled-components
keep the theme. Until then, a synced app cannot build the ported kit.

## 15. Deliverables Audit (2026-09-15)

The platform architect asked whether this program has achieved its
deliverables, with the bar set at stability (no unnecessary layout shift),
minimal bytes and loading overhead, and extreme hardware and styling sympathy.
This is the honest answer, proposal by proposal, then gate by gate.

### 15.1 Proposals

| Item | Built | Gate met | Evidence / what is missing |
| --- | --- | --- | --- |
| P1 `frameTelemetry` | yes | lab yes; device no | Lab p95 repeatable ±10% (PASS, Metal and SwiftShader). No floor-device run; finding 5 says a WebView P1 report is a lower bound. |
| P2 `uiQuality` | yes (+ ChooseChow) | lab yes; device no | Lab separation was the non-composited shimmer (finding 8), now fixed at the source; after the fix baseline is 28% slow, `low_power` 0%. `balanced` still has no CSS. No thermal/power inputs (P8). |
| P3 compositor-first motion | mostly | **lab yes** (§15.5) | Tokens as CSS ✓; framer-motion removed from the kit ✓ (§14.6); CSS enter + WAAPI timeline ✓ (§14.7); infinite loops pause off screen and on low tiers ✓; no exit-gated routes in ChooseChow ✓; **stall gate measured**: through a 400 ms main-thread block CSS and WAAPI motion kept presenting changed frames (36–37), JS rAF motion presented 1. **Missing:** View Transitions (calendar, routes), scroll-driven animations (none used). |
| P4 cull / virtualList | cull only | partly | `MinimalCullSection` shipped, skip/restore/scroll-height tested in Chromium; find-in-page and a11y tree untested; not adopted by an app. `virtualList` not built. The raster cost `cull` adds is small on a real GPU (finding 7). |
| P5 styling runtime | B done, C spiked | this page yes | Option B landed; **0 CSS rules injected after first paint** on the load profile (51 at FCP, 51 after scroll). Linaria extraction proven and a runtime-free `tokens` entry split out; the kit has not moved yet. Navigation-time injection in a real app is unmeasured. |
| P6 image pipeline | intrinsic sizes | lab yes (sizing) | `MinimalImage` (no unsized form in its type) + lint on unsized `<img>`; measured 200 px → 0 px shift (§13). Worker decode, decode queue and pixel budget not built; apps have not adopted it. |
| P7 raster rules | yes (lint) | lab only | Rules and gates in `frontend_surface_practices_check.mjs` (tier guard, keyframe properties, backdrop token, no permanent will-change, no framer in the kit), each negative-tested. `contain: layout paint` on list cards and the composited-layer budget are not set. |
| P8 native tuning | partly | no | Inspection flag ✓; COOP/COEP set but ineffective on Android WebView (verified). No ADPF/thermal/low-power signals. |
| P9 warm-up / hitch ledger | surfaces only | no | `prewarmRenderSurface` (10 vs 32 ms first frame). No UI hitch ledger; ChooseChow cold start (launch 12.8 s) remains the biggest user-facing defect. |
| P10 evidence harness | yes | — | Lab lanes `ssr` / `dom` / `browser` / `gpu`, scroll profile, load profile (new, below), device CDP tooling. |
| Graphics lane | yes | lab yes | GPU backpressure (`settled`), shared-device idle release, disposal — all on real hardware (findings 9–11). WebGL2 has no GPU barrier yet. |

### 15.2 Gates (section 6)

| Gate | Target | Status |
| --- | --- | --- |
| Scroll p95 / p99 | ≤ 16.7 / 25 ms on the floor device | Lab (CPU 6x, Metal) baseline p95 17.5 ms, `low_power` 9.2 ms. **Floor device: not measured**; emulator estimates only. |
| Max hitch, frozen frames | ≤ 50 ms, 0 | Lab: 0 LoAF on every variant. Device: unmeasured. |
| Runtime CSS rules after first paint | 0 | **Met** on the lab page (load profile). |
| LoAF attributed to image decode | 0 | Not measured: the lab feed has no images yet. Layout shift from images is gated (`MinimalImage`, lint, browser test). |
| Paint before JavaScript (§15.4 target) | FCP < 1 s on the floor emulation | **Met on the lab page**: 987 ms prerendered vs 1,216 ms client-rendered (§15.5). |
| Route change to first paint | ≤ 100 ms perceived | Not measured. |
| 90/120 Hz motion on the compositor | all UI motion | Kit motion is CSS/WAAPI on compositor properties (0 style writes measured); apps still use framer in places. |

### 15.3 Stability, bytes and loading (new measurements)

`frontend-lab/profile/loadProfile.mjs`: production build of the lab feed, served
with COOP/COEP, loaded on the real GPU with a floor-device emulation (CPU 4x,
Slow 4G, 412×915 @2.625x, touch), 3 runs after a warm-up.

| Metric | Result |
| --- | --- |
| CLS during load | **0** |
| CLS during a scripted scroll | **0** |
| CLS on a quality-tier change (`low_power`) | **0** — the tier swaps paint-only tokens, never geometry |
| FCP = LCP | 2,035 ms |
| Total blocking time (long tasks > 50 ms) | 53 ms |
| Script on the wire | 227.9 KB raw → **71.0 KB gzip / 62.2 KB brotli** (React, ReactDOM, styled-components and the ui-minimal it uses) |
| CSS rules at FCP / after load / after scroll | 51 / 51 / 51 |

What these say, and do not:

1. The kit itself is stable: nothing it does moves layout after first paint,
   including a tier change and `MinimalCullSection`-free scrolling.
2. First paint is **entirely JavaScript**: the HTML is an empty `#root`, so FCP
   waits on 62 KB brotli of script over Slow 4G, then parse and a client
   render. The largest lever on loading is not a smaller dependency but paint
   before JavaScript — prerendered or server-rendered shell HTML with the
   extracted CSS (which Linaria makes possible: its CSS is a static file that
   can be in the first response).
3. This page has no images, web fonts or data loading, which is where real
   apps shift. Stability is not proven for ChooseChow or ovasabi_v1 until the
   same profile runs against their production builds with seeded data.

### 15.4 What is left, ordered by what the hardware and the user feel

1. ~~**Paint before JavaScript**~~ — built and measured on the lab page (§15.5).
   Open: adoption by apps (ChooseChow's signed-in screens need a
   signed-out shell per route), and a `react-dom/static` prerender for routes
   that suspend.
2. ~~**P6 image pipeline with intrinsic sizes**~~ — sizing built and gated
   (§15.5). Open: sized sources (`srcset` from the media backend), worker
   decode, app adoption (ChooseChow `ChowThumb`).
3. **Load profile against real apps** (ChooseChow, ovasabi_v1 production builds
   with seeded data), including web fonts (`font-display`, size-adjusted
   fallbacks) and skeleton-to-content swaps.
4. ~~**The P3 stall trace**~~ — lab and emulator (§15.5). Open: floor-device
   runs of P1/P2 on real hardware.
5. ~~**Linaria migration of the kit**~~ — done (§14.8); the fleet's own
   styled-components code is in `styled_components_removal_handover.md`.
6. P9 hitch ledger and ChooseChow cold start.

### 15.5 Paint before JavaScript, image stability and the stall trace (2026-09-15)

**Paint before JavaScript.** `prerenderShell` (`frontend-kit/ts/vite`,
exported as `@ovasabi/frontend-kit/vite`) runs after the client build: it builds
the app's `entry-server` for SSR *with the app's own Vite config* (same aliases,
same wyw transform, so Linaria class names match the client build), calls its
`render(url)` per route, and writes the markup into the built HTML's `#root`.
Vite already links the extracted CSS in the `<head>`, so the first response is
paintable HTML + one stylesheet. `mountRoot` (`@ovasabi/frontend-kit`) hydrates
when the root has markup and client-renders otherwise (dev). The frontend
template wires all three (`src/entry-server.tsx` is a new create-mode seed).

`loadProfile.mjs` now builds both variants and loads them interleaved, served
brotli-11 as a production edge would (earlier bundles were uncompressed and
overstated every text byte ~4–7×), CPU 4x, Slow 4G, Metal, 3 runs each after a
warm-up, 40 feed sections:

| Metric | Client-rendered | Prerendered + hydrate |
| --- | --- | --- |
| FCP = LCP | 1,216 ms | **987 ms** (−19%) |
| TBT (long tasks > 50 ms) | 40 ms | **0 ms** — hydration adopts DOM instead of building it |
| App ready (two frames after mount) | 942 ms | 856 ms |
| CLS load / scroll / tier | 0 / 0 / 0 | 0 / 0 / 0 |
| Hydration mismatches | — | 0 |
| HTML on the wire | 0.5 KB | 1.1 KB (72.7 KB decoded) |
| CSS / script on the wire | 7.3 / 50.3 KB | 7.3 / 50.3 KB |
| CSS rules at FCP / injected after | 305 / 0 | 305 / 0 |

Uncompressed, the same pair read 1,975 → 1,465 ms: the HTML and CSS bytes were
the floor. With compression the remaining FCP is the Slow 4G round trips for
HTML then CSS. Next levers, in order: inline the critical CSS for the first
screen (removes one round trip), and prerender only the first screen rather
than the whole feed.

Rules for apps: render nothing during `render` that differs on the client for
that URL (no `window`, time, randomness, signed-in data); `useSyncExternalStore`
needs its server snapshot; anything data-dependent renders a skeleton **of the
final size** so hydration and data arrival shift nothing.

**Image stability.** See the §13 row: an unsized image moved the content below
it by the image's full scaled height (200 px); `MinimalImage` in either sizing
form moved it 0 px. Lint rule "images reserve their box before they load".

**Stall trace (P3 gate).** `frontend-lab/profile/stallTrace.mjs` measures what
the viewer sees, not what script reads: `Page.startScreencast` frames
(produced from the compositor's output and acknowledged outside the renderer
main thread), hashed, counting frames that *changed* inside a synchronous
main-thread block. One box sliding 240 px three ways, interleaved, 3 runs, block
400 ms, headless Chromium on ANGLE/Metal:

| Variant | Changed frames in the 400 ms block | Same span before the block |
| --- | --- | --- |
| CSS `@keyframes` on `transform` | **36** | 35 |
| WAAPI `element.animate` on `transform` | **37** | 36 |
| JS `requestAnimationFrame` writing `style.transform` | **1** | 36 |

Emulator (ChooseChow devtools WebView, Android 16, `-gpu host`; the page is
written into `about:blank` because the app WebView blocks cleartext localhost).
Its screencast delivers only ~25 frames/s, so the block is 1 s. Valid runs only:
the capture stalled after two rounds (0 frames even *before* the block, one
block stretched to 3.8 s), and the script now drops such runs.

| Variant (1 s block) | Changed frames in block, run 1 / run 2 | Same span before |
| --- | --- | --- |
| CSS `@keyframes` | 14 / 13 | 25 / 26 |
| WAAPI | 20 / 14 | 29 / 31 |
| JS rAF | 1 / 2 | 23 / 17 |

Same direction as the lab on a WebView: compositor motion kept about half to
two thirds of the capture's rate through the stall, JS motion froze. Two valid
pairs is a direction, not a gate; a physical floor device is still owed.

Compositor motion kept its cadence through the block; main-thread motion froze
for all of it — the ChooseChow complaint about framer-driven motion (§14.2),
now as a trace rather than an argument.

### 15.6 First screen only, critical CSS, small phones and a real app (2026-09-15)

**Levers built** (all in `@ovasabi/frontend-kit`):

- `useFirstScreenCount(first, total, step)`: `first` items in the prerendered
  HTML and during hydration, then `step` more per frame. A single commit of the
  remainder was one 70–77 ms long task at CPU 6x — transitions yield during
  render, never during commit — so it grows a few items per frame instead.
- `prerenderShell({ criticalCss: "subset" })` inlines only the rules that can
  match the prerendered markup (`vite/criticalCss.ts`: a rule is dropped only
  when every selector names a class the markup lacks; conditional at-rules
  filtered inside, referenced keyframes kept; unit-tested). The full stylesheet
  is **preloaded at its original place in the head** and promoted in place by
  a tiny external module (CSP-safe), with a `<noscript>` fallback. `"all"`
  inlines the whole stylesheet instead.
- `render(url)` may return `{ html, head }` — e.g. styled-components'
  `ServerStyleSheet` tags — and `ssrConfig` carries inline config (aliases,
  Rollup externals, transforms) into the SSR build, which otherwise starts from
  the config file alone.
- Load profile: `LOAD_DEVICE=small` (360×640 @2x, CPU 6x, 3G 300 ms / 750 kbps)
  next to `mid` (412×915, CPU 4x, Slow 4G); app mode (`LOAD_APP=`) serving a real
  production build with fixtures and media over the throttled network, blocking
  and recording any external host; LCP element, per-font arrival times,
  hydration and page errors per run. A run with a page error is not a result.

**Lab feed**, brotli, interleaved:

| Variant | FCP mid | FCP small | TBT small | HTML (br) |
| --- | --- | --- | --- | --- |
| Client-rendered | 1,198 ms | 1,914 ms | 115 ms | 0.5 KB |
| Prerender, whole feed | 992 ms | 1,357 ms | 5 ms | 1.1 KB |
| Prerender, first screen | 934 ms | 1,241 ms | 0 | 0.8 KB |
| + critical CSS (7.7 of 54 KB) | **585 ms** | **793 ms** | **0** | 2.7 KB |
| + whole CSS inlined | 592 ms | 815 ms | 0 | 7.8 KB |

CLS 0 and 0 hydration mismatches in every variant; no long task in the
first-screen variants after the per-frame expansion. Critical subset matches
full inlining on paint and keeps the full stylesheet cacheable.

**ChooseChow's signed-out landing page** (production build; seed catalogue as
fixtures, since production hides seed rows; lab-only transforms, no app edits —
`frontend-lab/profile/apps/choosechow/`), 3 interleaved runs:

| Build | FCP small | FCP mid | TBT small | CLS small / mid | HTML (br) |
| --- | --- | --- | --- | --- | --- |
| Baseline (client-rendered) | 5,024 ms | 3,152 ms | 246 ms | 0.119 / 0.093 | 2 KB |
| Prerender (styled-components SSR) | 2,072 ms¹ | 2,132 ms¹ | 286 ms | 0.077 / 0.076 | 9.5 KB |
| Prerender + critical CSS | **664 ms** | **408 ms** | 254 ms | 0.138 / 0.076 | 10.3 KB |
| Prerender + whole CSS inlined | 724 ms | 448 ms | 371 ms | 0.138 / 0.076 | 16.6 KB |

¹ The same build measured 1,204 / 648 ms in the previous session: without
critical CSS, paint waits on a stylesheet round trip on a jittery emulated
link. Critical CSS removes that wait and the variance with it.

What the app profile found:

1. **First paint: 7.6× faster on a small phone** (5,024 → 664 ms), with 0
   hydration mismatches. Script is unchanged (256 KB brotli, 1.14 MB decoded),
   so TBT and time-to-interactive are not improved — that is code splitting.
2. **An early paint catches more font swaps.** The hero block moves 34 px
   (one wrapped line) each time a web font arrives. Correction to a claim made
   during this work: the higher CLS of the critical variant was first blamed
   on the moved stylesheet overriding app styles; the whole-inline variant,
   with no late stylesheet, shows the same shift at the same moment, so the
   cause is paint landing before the first font rather than after it. (Keeping
   the full stylesheet at its original place in the cascade is still correct
   and is what shipped.)
3. **styled-components re-inserts `@font-face` on hydration — measured.** It
   moves server-rendered `<style data-styled>` rules into its own sheet and
   removes the originals; removing a sheet destroys its font faces. Per-font
   arrivals in the profile: Plus Jakarta Sans and Instrument Sans both download
   at ~1.25 s and **again at ~5.9 s** on the small phone (~0.6 s and ~3.5 s on
   mid), and the hero re-wraps each time. Fonts belong in static CSS (the
   Linaria migration does this), never in runtime CSS-in-JS.
4. **Data-driven label swap.** The hero pill reads "Hot-delivery riders" until
   the kitchens list lands and "3 cooking right now" after: a different length
   re-wraps it (94 → 61 px). Reserve its box (one line, fixed height).
5. **Third-party image host on the first screen.** Curated fallbacks load from
   `images.unsplash.com` — on a real phone a DNS + TLS setup per visit. Three seed
   photos are ~950 KB 1024×1024 JPEGs. Self-host and size (`MinimalImage`).
6. **Size-adjusted fallback faces** (`frontend-lab/profile/fontFallbackMetrics.mjs`):
   each woff2 measured in Chromium (`measureText` width, font ascent/descent) against
   local fallbacks at regular and bold, emitting `size-adjust`, `ascent-override`,
   `descent-override`. One set cannot serve both platforms: Arial-tuned values were
   2.5% wide on Roboto and 9% on Roboto Bold, so each web font gets an
   Arial/Helvetica family and a Roboto family, both listed in the stack.
   **Result: no CLS change on their own** (small 0.1376 → 0.1373, mid 0.0763 →
   0.0763, 4 interleaved runs): while the faces are destroyed and recreated by
   the CSS-in-JS library, matching the fallback's metrics does not stop the
   hero re-wrapping. They are necessary for a swap to be invisible, not
   sufficient.
7. **Fonts as static CSS fixes it.** Lab variant `prerender-critical-fonts-static`:
   `FontFaces` renders nothing, and the same `@font-face` rules plus the fallback
   faces sit in a static `<style>` in the head. Each font now downloads once.
   4 interleaved runs:

   | Build | FCP small | CLS small | FCP mid | CLS mid |
   | --- | --- | --- | --- | --- |
   | Baseline | 5,020 ms | 0.119 | 3,196 ms | 0.093 |
   | Prerender + critical CSS | 724 ms | 0.138 | 416 ms | 0.076 |
   | + fallback faces | 652 ms | 0.137 | 400 ms | 0.076 |
   | + fallback faces, fonts static | **640 ms** | **0.062** | **400 ms** | **0** |

   Mid phone: zero layout shift during load. Small phone: one shift left — the
   hero heading still re-wraps by one line at 360 px when the first font lands
   (the fallback's average width is close, not exact, for that string at that
   width). Remaining options: `font-display: optional` on the display face (no
   swap after first paint on a slow first visit; the fallback holds), or tune
   `size-adjust` on the hero copy itself. TBT is unchanged by all of this
   (~250–290 ms small): that is script, and needs code splitting.

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

Dependencies (section 14):

- [Motion: web animation performance guide](https://motion.dev/docs/performance)
- [Motion: reduce bundle size / LazyMotion](https://motion.dev/docs/react-reduce-bundle-size)
- [styled-components: library entering maintenance mode](https://github.com/orgs/styled-components/discussions/5568)
- [Sanity: styled-components maintenance mode, a 40% faster fork](https://www.sanity.io/blog/cut-styled-components-into-pieces-this-is-our-last-resort)
- [css-in-js-bench (report)](https://jantimon.github.io/css-in-js-bench/)
- [The state of zero-runtime CSS-in-JS, mid-2026](https://dx-styles.dev/blog/state-of-zero-runtime-css-in-js/)
- [Linaria](https://github.com/callstack/linaria)
- [Can I use: @starting-style](https://caniuse.com/mdn-css_at-rules_starting-style)
- [Can I WebView: popover](https://caniwebview.com/features/web-feature-popover/)
- [Can I WebView: view transitions](https://caniwebview.com/features/web-feature-view-transitions/)
- [Announcing Interop 2026 (WebKit)](https://webkit.org/blog/17818/announcing-interop-2026/)
- [Chrome: animate to height: auto](https://developer.chrome.com/docs/css-ui/animate-to-height-auto)
- [Statista: iPhone share by iOS version](https://www.statista.com/statistics/565270/apple-devices-ios-version-share-worldwide/)
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
