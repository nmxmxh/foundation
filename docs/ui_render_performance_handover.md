# UI Render Performance — Session Handover

Status: handover, 2026-09-15  
Program: [ui_render_performance_research.md](ui_render_performance_research.md) — section 13 (Implementation Ledger) is the source of truth for state and evidence; this file is how to pick the work up.

**Nothing is committed** in Foundation, ChooseChow, or ovasabi_v1. Never use `git checkout -- <path>`, `git stash` or `git reset` in these trees; they hold other uncommitted work.

## 1. Goal and working rules

- Close the DOM/CSS pixel gap in the research doc, primitive by primitive, in Foundation first.
- **The user's direction:** rigorous testing, profiling, monitoring and experimentation happen in the Foundation-only `frontend-lab/` (like `servicebacked`, never vendored) *before* apps adopt anything.
- **Performance doctrine:** jank means one lane is overworked, so redistribute before inventing primitives. Quality stays adaptive but optimal. Emulator and desktop numbers are fine for tuning, labelled as estimates.
- **Evidence discipline:**
  - A gate that has not been seen failing against a planted violation is not a gate. Restore plants by hand, then re-run.
  - Interleave variants, discard a warm-up, report ranges. Device runs are bimodal.

## 2. What exists now (short; details and numbers in the ledger)

### Foundation (`/Users/okhai/Desktop/OVASABI STUDIOS/foundation`)

| Area | Files | State |
| --- | --- | --- |
| P1 `frameTelemetry` | `runtime-sdk/ts/browser-host/src/frameTelemetry.ts` (+test) | Shipped; direct rAF inside caller windows; display-relative slow frames; LoAF counts; renderMarks. |
| P2 `uiQuality` | `browser-host/src/uiQuality.ts` (+test) | Shipped; device prior, save-data, reduced motion, window-based hysteresis on slow-frame *share*, platform floor. |
| `frameClock` fixes | `browser-host/src/frameClock.ts`, `frameClock.diagnostics.test.ts`, `frameClock.bridge.test.ts` | (1) Degraded clock reports `worker unavailable…`, not `ok`. (2) **Subscription-time latch fixed.** Every consumer's clock latched onto animation frames because a pre-`start()` `stopped` diagnostic was read as a fallback; the bridge now releases on the first worker tick. Browser-host suite 243/243. |
| P5 option B | `ui-minimal/ts/src/theme.tsx` (`minimalVars`, CSS-var table, tier CSS), `variantRules.ts`, `primitives.tsx`, `interactions.tsx` | Shipped; proven render-equivalent. |
| P2/P3/P7 tier CSS | `theme.tsx` `:root[data-ui-tier=…]` blocks; `MinimalSkeleton` pause; `ModalBackdrop` via `--minimal-backdrop-filter` | Shipped. |
| P4 `MinimalCullSection` | `primitives.tsx` | Shipped primitive; not adopted by any app. |
| P7 lint + contracts | `tooling/scripts/frontend_surface_practices_check.mjs` | 7 new rules, each negative-tested; Foundation passes. Scans Foundation roots only. |
| Media / shell | `server-kit/go/objectstore/fs_store.go` (+`fs_store_content_type_test.go`); `templates/native/*` (COOP/COEP headers, README item 6); `tooling/scripts/project_scaffold_check.sh` (WARN on missing isolation headers) | Shipped. Android WebView ignores COOP/COEP (verified), so no sab lane on Android. |
| Lab | `frontend-lab/` (README, `vitest.config.ts`, `src/{ssr,dom,browser,gpu}`, `profile/`, `device/`, `baselines/`); `Makefile` targets `test-frontend-lab`, `test-frontend-lab-browser`; `tooling/foundation_ownership.tsv` row | `ssr` 1/1, `dom` 6/6, `browser` 12/12, `gpu` 4/4 (09-15). Profile lane writes capture bundles; `PROFILE_GPU=metal` for real raster, `PROFILE_SET=skeleton` for placeholder designs. No Makefile target for `gpu` yet (Makefile is a protected file). |
| Render-surface GPU fixes (09-15) | `browser-host/src/renderSurfaceClient.ts` (`RenderSurfacePass.settled`, backpressure), `renderSurfaceWorker.ts` (`releaseWhenIdleMs`, live-pass release, forwards `settled`); tests in both `.test.ts` files | Ledger findings 9–11. Browser-host 253/253. New tests negative-tested (plants failed exactly the 6 new behaviour tests). |
| Skeleton shimmer (09-15) | `ui-minimal/ts/src/primitives.tsx` `MinimalSkeleton`; SSR baseline; lab `tierTokens` test reads `::after`; new lint rule "keyframes animate only compositor properties" | Fixed (ledger finding 8): the sweep is a transform on `::after` **and pauses while off screen** (shared IntersectionObserver). Interleaved A/B: paused 0% slow, same as no animation; always-running designs 55–71% slow; cull 5%. Lab browser 13/13 incl. `skeletonOffscreen` (negative-tested). The general lesson for P3: an infinite animation must stop when nobody can see it, not just on low tiers. |
| Docs | `docs/ui_render_performance_research.md` (status, P1/P8 revisions, §10 lesson 2 addendum, §13 ledger with findings 1–6 and captures) | Current as of this handover. |

### ChooseChow (`chowdash_rider_v1`)

- Vendored foundation synced (`--foundation-only`, never `--force`).
- **App changes:**
  - Layout: `components/chow/layout.ts` sets one content column and a gap under the header.
  - Loading-vs-empty: `lib/projectionHealth.ts` `useScopeLoaded`, used in `features/choosechow/trending.ts`.
  - Audience declarations: `internal/startup/projection_audience.go` declares the three mealplan scopes.
  - P7 tier adoption (done by a sub-agent, verified by grep):
    - One tier block in `styles/theme.ts`, plus `styles/uiTier.ts`.
    - `lib/renderQuality.ts` (+test) creates uiQuality in `main.tsx`, fed by one passive scroll listener and route changes.
    - `--chow-backdrop-filter`, `--chow-glass-*` and `--chow-shadow-*` tokens across ~20 components.
- `npx tsc --noEmit -p tsconfig.app.json` → 0 errors; `npx vitest run` → 36 files / 168 tests.
- **Trap:** the app's `npm run typecheck` checks nothing (root tsconfig is `files: []`). Use the `-p tsconfig.app.json` form.

### ovasabi_v1

- `frontend/src/lib/render/frameClock.ts` now passes its own pulse worker to `configureFrameClock`.
- Its *vendored* frameClock still has the latch until the app syncs Foundation. That sync is blocked: ovasabi_v1 has uncommitted edits inside vendored `foundation/server-kit`, so check with the user first.

## 3. How to run everything

```bash
# Foundation unit suites
cd runtime-sdk/ts/browser-host && npx vitest run && npx tsc --noEmit -p tsconfig.json
cd ui-minimal/ts && npx tsc --noEmit -p tsconfig.json
node tooling/scripts/frontend_surface_practices_check.mjs .

# Lab (cd frontend-lab; npm install once)
npx vitest run --project ssr        # cascade eval vs baselines/ui-minimal-cascade.digests.json
npx vitest run --project dom
npx vitest run --project browser    # headless Chromium, COOP/COEP; writes results/pulse-latency.json
npx vitest run --project gpu        # real GPU (ANGLE/Metal + WebGPU); render-surface lane; writes results/gpu/*.json
npx vitest run                      # all lanes
UPDATE_BASELINE=1 npx vitest run --project ssr   # only for an intended rendering change, say why
PROFILE_GPU=metal PROFILE_CPU_THROTTLE=6 PROFILE_TRACE=1 PROFILE_RUNS=3 npm run profile   # capture bundle in results/profile/<stamp>/
LOAD_RUNS=3 node profile/loadProfile.mjs   # builds csr + prerender variants, serves brotli, interleaves: bytes, FCP/LCP, TBT, CLS (load/scroll/tier), rules after FCP, hydration errors → results/load/<stamp>/
node profile/stallTrace.mjs                # P3 gate: screencast frames that change during a main-thread block (CSS / WAAPI / JS rAF) → results/stall/
STALL_DEVICE_CDP=http://127.0.0.1:9222 STALL_BLOCK_MS=1000 STALL_RUNS=5 node profile/stallTrace.mjs   # same on the emulator WebView (page written into about:blank; returns to the app URL)
npx vitest run --project browser src/browser/imageStability.browser.test.tsx   # P6: unsized <img> shifts 200 px, MinimalImage 0
LOAD_DEVICE=small LOAD_RUNS=4 node profile/loadProfile.mjs   # 360x640, CPU 6x, 3G; LOAD_DEVICE=mid is the default Lighthouse-mobile emulation
# Real app (ChooseChow landing): snapshot dist into results/apps/choosechow/baseline/, then
CHOW_CRITICAL=subset node profile/apps/choosechow/buildPrerender.mjs   # 0 | subset | all; CHOW_FONT_FALLBACK=1 adds size-adjusted fallbacks
LOAD_APP=profile/apps/choosechow.mjs LOAD_BUILDS=baseline,prerender-critical LOAD_DEVICE=small node profile/loadProfile.mjs
LOAD_APP=profile/apps/choosechow.mjs node profile/fontFallbackMetrics.mjs   # → results/fonts/choosechow-fallbacks.css
npx vitest run --project ssr src/ssr/criticalCss.test.ts src/ssr/prerenderCss.test.ts
node linaria/build.mjs && node linaria/measure.mjs   # Linaria spike: extraction + mount cost vs styled-components
```

**GPU backend trap (09-15):** without `PROFILE_GPU=metal` the profile rasterises on SwiftShader, a CPU renderer. The `browser` project is SwiftShader too; only the `gpu` project sees the real GPU. Every raster number captured before 09-15 was software rendering (ledger finding 7).

Lab notes:

- Playwright 1.62.1 reuses cached `chromium-1234`, which reports Chromium 151.
- Browser tests write files with `commands.writeFile` from `vitest/browser`, with paths relative to the lab root. `@vitest/browser/context` breaks in the browser pool.
- Vitest browser mode does not forward `console` output to the terminal.

## 4. Device setup (Android emulator)

- **Emulator:**
  - SDK at `/opt/homebrew/share/android-commandlinetools`; AVD `choosechow` (arm64, Android 16, WebView 133, 1080×2400, 420 dpi, 4 cores / 2 GB).
  - The AVD config has the GPU off; always launch with `-gpu host`:
    `ANDROID_SDK_ROOT=$SDK $SDK/emulator/emulator -avd choosechow -gpu host -no-snapshot-save -no-boot-anim &`
- **JDK:** `openjdk@17` (Homebrew formula). The temurin cask needs sudo and fails from an agent shell.
- **Inspectable release build** (signs with the upload key and installs over the store build). Never ship it:

  ```bash
  cd chowdash_rider_v1/native
  export JAVA_HOME=/opt/homebrew/opt/openjdk@17/libexec/openjdk.jdk/Contents/Home ANDROID_HOME=$SDK ANDROID_SDK_ROOT=$SDK NDK_HOME=$SDK/ndk/28.2.13676358 PATH="$JAVA_HOME/bin:$PATH"
  npm run android:build -- --apk --target aarch64 --features tauri/devtools
  adb install -r src-tauri/gen/android/app/build/outputs/apk/universal/release/app-universal-release.apk
  ```

  Build a clean release (no `--features`) before anything leaves the machine.
- **DevTools:** `adb forward tcp:9222 localabstract:webview_devtools_remote_$(adb shell pidof com.ovasabi.choosechow | tr -d '\r')`, then:
  - `node frontend-lab/device/cdp.mjs eval '<js>'` or `nav '<url>'`
  - `node frontend-lab/device/cdp-trace.mjs out.json 9` while swiping
  - `frontend-lab/device/scrollGfx.sh` for interleaved `gfxinfo` A/B
  - `node frontend-lab/device/scrollMatrix.mjs` — instrumented, interleaved variant matrix: gfxinfo summary + framestats phases, page rAF/LoAF, style/layout deltas, layer count, WebSocket frames mid-swipe, **and the run's host load / guest memory**. `ROUTES=/orders VARIANTS=baseline,plain-list PAIRS=3`. Always include `plain-list`: it is the control that says whether the run is evidence.
  - `node frontend-lab/device/layerPaintProfile.mjs` and `paintShadowDepth.mjs` — **per-layer paint replay** (`LayerTree.makeSnapshot` + `profileSnapshot`): what the PAGE costs to raster, independent of the device's fill speed. This is the instrument that found finding 12; whole-frame A/B cannot see content cost on either emulator lane.
  - `node frontend-lab/device/soak.mjs` — degradation soak: user-like tours, then app + WebView-control + non-WebView (Settings) samples with memory, to tell a degraded device from a degraded app.
  - `frontend-lab/device/coldScroll.sh <pkg> <runs> <settle> [route]` — cold-start ledger; the route is reached in-app so the launch stays cold.
- **Sign-in:** the agent must not enter passwords. Ask the user to sign in on the emulator. The seeded test accounts (`tayo@` / `amara@choosechow.com`, `demo1234`) exist only in local seeded databases, not production.
- **ovasabi_v1 in Docker:** `docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d app-postgres app-redis migrate server`.
  - Port 5173 is dual-bound: `localhost` reaches the Vite dev server, `127.0.0.1` reaches the stale nginx container.
  - Use `/demo` once per browser session to pass the coming-soon gate.
  - The browser pane must be visible when measuring: hidden panes throttle rAF to ~1 Hz.

## 5. Open questions and next experiments (prioritised)

1. ~~**Why P2 separates in the lab but not on the device.**~~ **Answered 09-17 (ledger finding 12): a blurred shadow behind a rounded rect costs ~25 ms of raster per screen; the same shadow on a square rect costs ~1 ms.** Shadows are >90% of page paint on every ChooseChow screen measured. The cheap tiers now draw an unblurred edge (Orders low_power 31 → 7.2 ms paint; balanced 68 → 31). Still open: whether `balanced` should also go blur-free (it still costs 31 ms/screen on Orders), and iOS/WKWebView, where Core Graphics may not have the same cliff — measure before assuming.
   - **Instrument:** `frontend-lab/device/layerPaintProfile.mjs` and `paintShadowDepth.mjs` (per-layer paint replay via `LayerTree.profileSnapshot`). Whole-frame gfxinfo A/B **cannot** see content cost on either emulator lane (host GPU draws everything at 17 ms; SwiftShader saturates at ~61 ms including the control) — see the method row in the ledger.

2. ~~**Bimodal device runs.**~~ **Closed 09-17: it is the emulator, not the app.** A 45-minute soak (44 tours, plain-list control plus a non-WebView Settings control, memory tracked) held 17 ms p50 with flat memory; the slow sessions were a degraded emulator in which the *control itself* read 89–200 ms and only a reboot recovered it. **Harness rule: every device capture carries a plain-list control and the run's host load / guest memory (`scrollMatrix.mjs` records them); a run whose control is over budget is not evidence.**

3. **`cull` raises raster by 30% in the lab** while cutting style by 74% and paint by 54%. Find out whether it's re-raster on section restore, `contain-intrinsic-size` placeholders, or tile churn. Try different `estimatedSize` values and a larger overscan margin. The answer decides when apps should use `MinimalCullSection`.
4. **Page-side vs platform frame truth (finding 5).** rAF p50 17 ms coexists with gfxinfo p50 ~100 ms. Build a lab or device harness that records both for the same run, and decide what `uiQuality` should consume inside native shells (P8 platform metrics bridge).
5. **Lab coverage gaps:**
   - `uiQuality` hysteresis in a real browser under forced jank (CPU throttling toggled mid-run).
   - `frameTelemetry` accuracy against CDP frame timings.
   - `MinimalCullSection` find-in-page and accessibility-tree checks.
   - The ssr eval covers the base theme only; add sweeps under `MinimalThemeScope` and a dark theme.
   - ~~The render-surface lane has no lab lane.~~ Done 09-15: `frontend-lab/src/gpu/` (findings 9–11: GPU backpressure via `RenderSurfacePass.settled`, shared-device idle release, prewarm 10 vs 32 ms). Still open there: **WebGL2 has no GPU barrier** (`gl.finish()` ~0 ms under 40× load on ANGLE/Metal, fence never signalled). Try `clientWaitSync` with a timeout, `EXT_disjoint_timer_query_webgl2`, or measure presentation instead. Also: a promote test (load dropped mid-run), GPU allocation per frame (count descriptor/object churn in the worker), and the lane on the emulator's WebView (WebGL2 only there).
   - ovasabi_v1 `blackHolePass`: a device per pass that is never destroyed, a pass descriptor allocated per frame, and no `settled`. Fix once that app can sync Foundation.
6. ~~**`balanced` tier has no CSS.**~~ Shipped 09-17: `balanced` collapses multi-layer shadows to one layer (ledger finding 12; Orders paint 68 → 31 ms, profile 22 → 10.7). Open: whether it should drop blur entirely like `low_power`, which is a design call — blur-free would take Orders to ~7 ms.
7. **P7 enforcement in app code.** The surface check scans Foundation roots only. ChooseChow's P7 conformance was verified by grep this session; decide whether a project-side check ships (script checks do reach apps).
8. **Not started:** P4 `virtualList`, P6 rules 3–5 (worker decode, decode queue, pixel budget; sizing is done), P8 device signals (ADPF / iOS thermal), P9 warm-up and hitch ledger (cold start is the real ChooseChow defect: launch `TotalTime` 12.8 s on the P7 build, first scroll runs ~100 ms frames), P5 option C (Linaria), iOS simulator verification (COOP/COEP in WKWebView, 120 Hz questions).

### Dependency program (added 09-15; research doc section 14)

Measured: framer-motion as ui-minimal imports it is 47.6 KB gzip (more than React + ReactDOM), runs `y`/`scale`/`height:auto`/springs from JS every frame (49–61 style writes per animation; CSS 0), and `MinimalCard` as a motion component mounts 3.9× slower cold than a CSS-fade card. styled-components: 12.2 KB, cost concentrated on cold mount and interpolated props. Recommendation in §14.5: remove framer-motion from ui-minimal; keep styled-components on the B → C (Linaria) path.

**Landed 09-15 (research doc §14.6):** framer-motion removed from ui-minimal (breaking, by decision); CSS `minimalEnter` + `useMinimalPresence`; `enter` prop replaces `initial={false}`; scroll-feedback API deleted; template and manifest sync no longer require framer; surface-check ratchet (negative-tested); lab ssr re-baselined, dom 6/6, browser 13/13. Apps synced and migrated: ChooseChow (tsc clean, 168/168), trotters_v1 (tsc clean, 35/35), pronto_v1 (differential tsc clean; **needs a reinstall** — stale pnpm `node_modules`, npm lockfile). iOS target stays 14.0 by decision: enter animations are keyframes, which play there too.

**Also 09-15 (research doc §14.7):** ovasabi_v1's 42 vendored edits were byte-identical to Foundation (nothing to upstream); it is now synced (tsc clean, 256/256). Linaria spike in `frontend-lab/linaria/` (`node linaria/build.mjs`, then `node linaria/measure.mjs`): extraction from Foundation source works; script −29% cold / −65% warm vs the styled-components card, 0 injected rules. `createMinimalTimeline` / `minimalEntry` / `minimalExit` / `useMinimalTimeline` shipped in ui-minimal with 9 browser tests.

**Linaria port landed in the kit (research doc §14.8):** ui-minimal is on `@linaria/react`; `tokens.ts` entry; theme via React context; `@ovasabi/ui-minimal/styled-components` bridge; lab lanes run through `@wyw-in-js/vite` (`prefixer: false`). Proof: `npx vitest run --project ssr src/ssr/linariaPort.eval.test.ts` — 2,176/2,240 identical to the saved styled-components cascade (`results/ui-minimal-cascade.styled-components.reference.json`), 64 accepted FieldGrid invalid-input cases. Kit CSS 7.9 KB brotli, JS 30.3 KB brotli. Lab: ssr 2/2, dom 6/6, browser 22/22.

**Fleet-wide styled-components removal has its own handover: [styled_components_removal_handover.md](styled_components_removal_handover.md)** (complete 2026-09-16: every app on Linaria, kit bridge deleted, ratchet added).

**The remaining paint work — fonts, prerender + critical CSS, code splitting, image sizing, device truth — has its own handover: [frontend_paint_performance_handover.md](frontend_paint_performance_handover.md)** (standards every app is held to, per-app inventory, order of work, what Foundation still needs).

**New apps start on Linaria:** the frontend template carries no styled-components (package, configs, `App.tsx`, `theme.ts`); styling guide §3 is the Linaria format; the manifest sync no longer requires styled-components; the scaffold check expects `@linaria/react`. The only styled-components left in Foundation is the opt-in bridge for existing apps, its managed patch, and the lab's equivalence reference.

**ChooseChow is migrated (pilot green: tsc clean, 168/168, production build; research doc §14.8).** The other vendoring apps still carry the styled-components kit — **do not `--foundation-only` sync them without the rest of the recipe**, or their build breaks.

Per-app recipe (what the pilot proved):

1. `./scripts/update-project.sh ../<app> --foundation-only` (cmp vendored local edits first — see memory).
2. `node tooling/scripts/frontend_linaria_patch.mjs ../<app>/frontend` (or a full update, which runs `patch_frontend_linaria`). Follow any `manual` line.
3. `node tooling/scripts/frontend_manifest_sync.mjs templates/frontend/package.json ../<app>/frontend/package.json`, then the app's package manager install. This also moves the app to Vitest 5: switch `src/test/setup.ts` to `import '@testing-library/jest-dom/vitest'`.
4. Tests that render inside `MinimalThemeProvider` directly: use the app's `AppThemeProvider`.
5. `tsc -p tsconfig.app.json`, tests, production build.

Known manual cases: civic_watch_ng_v1 (1 file) and reframe_v1 (2 files) mount `MinimalThemeProvider` directly in app code; docuos_v1, forest_v1 and global_value_exchange_net_v1 have no `AppThemeProvider` alias (global_value mounts styled-components' `ThemeProvider` itself); pronto_v1 needs a clean reinstall (stale pnpm `node_modules`, npm lockfile).

Protected files changed (need `HUMAN_SUPERVISED_CHECK_UPDATE=1 tooling/scripts/enforcement_integrity_check.sh . --write`): `tooling/scripts/scaffold_managed_patches.sh`, `tooling/scripts/frontend_surface_practices_check.mjs`, possibly the templates.

**Landed 09-15 (research doc §15.5): paint before JavaScript, image stability, stall trace.**

- `prerenderShell` (`@ovasabi/frontend-kit/vite`) + `mountRoot` (`@ovasabi/frontend-kit`); the template wires both and seeds `src/entry-server.tsx` (create mode). Lab, brotli, CPU 4x, Slow 4G: FCP 1,216 → **987 ms**, TBT 40 → 0, CLS 0, 0 hydration mismatches. Existing apps opt in by hand: their `vite.config.ts` / `main.tsx` are create-mode, so a sync does not touch them. ChooseChow needs a signed-out first screen per route before it can prerender.
- `MinimalImage` (type requires `width`+`height` or `aspectRatio`; lazy + async decode by default; `priority`; `reveal` after decode) and the lint rule "images reserve their box before they load". Browser test: unsized `<img>` 200 px shift, `MinimalImage` 0.
- Stall trace: lab (Metal) CSS 36 / WAAPI 37 / JS rAF 1 changed frames in a 400 ms block. Emulator WebView: see §15.5 (screencast there tops out near 25 frames/s, so use a 1 s block).
- Traps: an app WebView blocks cleartext `http://localhost` (use `about:blank` + `document.write`); `loadProfile` numbers before 09-15 15:11 were uncompressed; a vendored kit has no `node_modules`, so the plugin resolves the *app's* Vite from `config.root`; `npm install` in `frontend-kit/ts` prunes packages not in its `package.json`.

**Landed 09-15 (research doc §15.6): first screen only, critical CSS, small phones, ChooseChow profiled.**

- `useFirstScreenCount` (per-frame expansion; one big commit was a 70–77 ms long task at CPU 6x), `prerenderShell` `criticalCss: "subset" | "all"`, `{ html, head }` render results, `ssrConfig`. Template uses `criticalCss: 'subset'`. Lab small phone: FCP 1,914 → 793 ms, TBT 115 → 0.
- ChooseChow landing, prerendered with styled-components SSR + critical CSS (lab build, app untouched): small phone FCP **5,024 → 664 ms**, mid 3,152 → 408 ms, 0 hydration mismatches. Not improved: 256 KB brotli script, TBT ~250 ms small. CLS sources found: font swaps (styled-components re-inserts `@font-face` on hydration → fonts load twice), the hero pill's label swap when data lands, and the paint now landing before the first font. Size-adjusted fallback faces measured per platform (Arial and Roboto families).
- Traps: a lab transform that starts a line with `(` in a no-semicolon codebase calls the previous statement (`vC(...) is not a function`) — a page error means the run is invalid; the SSR build of an app that aliases React to a path needs React, ReactDOM and the router as Rollup externals together (else two Reacts, null dispatcher); `ssr.external` does not work for imports from outside the app root (use `build.rollupOptions.external`); styled-components as an SSR external loads its CJS build (`styled.nav is not a function`); moving a stylesheet to the end of `<body>` changes its cascade order against CSS-in-JS styles; a registered-but-unbuilt app build profiles a 404 page and looks fast (the profiler now refuses); emulated-3G medians of one variant move ±50% across sessions — compare interleaved only.

Font result (research §15.6 point 7): moving `@font-face` out of styled-components into static CSS, with the fallback faces, took ChooseChow's load CLS to **0 on the mid phone** and 0.119 → 0.062 on the small one (one hero re-wrap left at 360 px; options: `font-display: optional` on the display face, or size-adjust tuned on the hero copy). Fallback faces alone changed nothing while the faces were being re-inserted.

Then, in order: apply the font fix in ChooseChow itself (static `@font-face` + fallback faces from `results/fonts/choosechow-fallbacks.css`, reserve the hero pill's box), adopt the prerender in the app itself (entry-server + hydrate in `main.tsx`), code-split the 256 KB-brotli script so TBT follows paint, and self-host the Unsplash fallbacks.

Next steps, in order:

1. Overlays: `ActionModal` → `<dialog>`, `FloatingPanel`/`Tooltip` → `popover`, exits via `transition-behavior: allow-discrete` where supported (instant below Safari 17.5). Browser-lane tests for focus, `Escape`, light dismiss and exit animation. Anchor positioning with the measured path as fallback. Keep iOS 14 working: feature-detect `HTMLDialogElement.prototype.showModal` and the `popover` attribute.
2. Calendar: View Transitions for the month swap and selection (dropped in the port) where supported.
3. Missing evidence: ~~the stall trace~~ (done, §15.5); the runtime-cost tests on the emulator WebView; before/after of a real ChooseChow screen; the load profile against a real app's production build.
4. Apps' own framer-motion: ChooseChow still imports it in ~9 files, trotters for page transitions, pronto for list reordering. Migrate case by case with the §14.4 table; gesture/layout reordering may legitimately keep it.
5. The other 8 vendoring apps were not synced (no retired API in use); they pick the change up on their next `--foundation-only` sync. ovasabi_v1 remains blocked on its vendored `server-kit` edits.

## 6. Waiting on the user

- **Enforcement manifest refresh** (protected files changed: Makefile, scaffold check, surface check, ownership tsv, and others):
  `HUMAN_SUPERVISED_CHECK_UPDATE=1 tooling/scripts/enforcement_integrity_check.sh . --write`
- **Seed catalogue decision:** production dishes are all seed rows. The projection hides them; REST serves them. Pick one: hide them everywhere, or show them for now.
- **Deploy order:** the ChooseChow backend (media CORP, FSStore content types, mealplan audiences) before any shell build that relies on them.
- **ovasabi_v1 Foundation sync:** blocked on its uncommitted vendored `server-kit` edits.
- **Commits** in all three repos. Suggested split:
  - Foundation: primitives + tests, lab, lint/ownership/Makefile, docs.
  - ChooseChow: layout/loading/audience, P7 tier adoption, vendored sync.
  - ovasabi_v1: the one-file clock wiring.

## 7. Traps recorded this session

Each is also in agent memory.

- **ChooseChow's empty catalogue** is the seed filter (`notSeeded`), not stale projections. The REST dish proto has no metadata, so seed rows look real over REST.
- **Media in native shells:** server-kit CORP `same-origin` plus FSStore `octet-stream` blanked media in the shipped app.
- **`frameClock` facts:** `frameClockMode().mode` is what drives the clock; the renderMarks fact used to lie. Check both.
- **Cascade harness:** `sc-*` ids are a module-order counter; `:where()` guards; stylis hoists nested rules after declarations (shorthand/longhand trap); token arithmetic becomes string concatenation.
- **`checkVisibility({ contentVisibilityAuto: true })`** is only false for descendants of a skipping element.
- **Hidden documents:** browser panes and backgrounded tabs throttle or pause rAF, so a probe without a visibility check reports a slow, clean page.
- **Cold vs warm:** the first minutes after install or launch are cold. Warm first, and record cold separately as a P9 hitch.
