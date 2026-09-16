# Paint Performance — Handover (fonts, prerender, splitting, images, device truth)

Status: opened 2026-09-16; Foundation side built and enforced, F1/F2/F4/F6 green fleet-wide 2026-09-16. F3 (fallback faces) and P1 (prerender) not adopted by any app yet.
Goal: **every app paints its first screen from HTML + CSS, with no layout shift and no font round trip on the critical path**, and the numbers are measured on a real device rather than inferred.

Predecessors: [styled_components_removal_handover.md](styled_components_removal_handover.md) (complete — extracted CSS is the precondition for everything here) and [ui_render_performance_handover.md](ui_render_performance_handover.md) (the wider program; §4 has the device setup). Evidence for every figure below is in [ui_render_performance_research.md](ui_render_performance_research.md) §15.3–§15.6.

**Nothing is committed** in Foundation or any app. Never use `git checkout -- <path>`, `git stash` or `git reset` in these trees.

## 1. Where things stand

Proven in the lab, adopted by **no app**:

| Lever | Lab result | Built and where |
| --- | --- | --- |
| Prerender the shell + hydrate | feed FCP 1,216 → 987 ms, TBT 40 → **0 ms** | `prerenderShell` (`@ovasabi/frontend-kit/vite`), `mountRoot` (`@ovasabi/frontend-kit`), template seeds `src/entry-server.tsx` |
| First screen only | FCP small 1,357 → 1,241 ms, TBT → 0 | `useFirstScreenCount` (`@ovasabi/frontend-kit`) |
| Critical CSS inlined | FCP mid **1,198 → 585 ms**, small **1,914 → 793 ms** (7.7 KB of 54 KB inlined) | `prerenderShell({ criticalCss: 'subset' })`, `vite/criticalCss.ts` |
| Same, on ChooseChow's real landing page | FCP small **5,024 → 664 ms**, mid 3,152 → 408 ms, 0 hydration mismatches | lab-only transforms in `frontend-lab/profile/apps/choosechow/` — the app itself was never changed |
| Size-adjusted fallback faces | ChooseChow load CLS **0.119 → 0** on the mid phone | `frontend-lab/profile/fontFallbackMetrics.mjs`, output in `frontend-lab/results/fonts/choosechow-fallbacks.css` |
| Compositor vs JS motion under a stall | CSS 36 / WAAPI 37 / JS rAF **1** changed frame in a 400 ms block | `frontend-lab/profile/stallTrace.mjs` |

What the styled-components removal already bought (measured 2026-09-16): styles are static files in `<head>`, JS per app is 9–47 KB brotli smaller, and no app injects style rules at runtime any more (the only remaining `insertRule` belongs to framer-motion in pronto, trotters and ChooseChow; ovasabi has none). That is the precondition for critical CSS: **you cannot inline what does not exist until script runs.**

## 2. The standard every app is held to

Each rule says what to do, why, and how it is checked. F-rules are fonts, P-rules paint.

**F1 — No third-party font origin, ever.** No `fonts.googleapis.com` / `fonts.gstatic.com` link, and no `@import` of one. A third-party stylesheet costs a DNS + TLS + round trip before the first glyph, and an `@import` inside CSS serialises it *behind* the stylesheet that contains it. Self-host in `public/fonts`, served from the app's own origin.
*Today: trotters and civic_watch_ng load Inter from Google; reframe `@import`s three families at the top of `styles/app.css`.*

**F2 — One variable `woff2` per family, subset by `unicode-range`.** No `.ttf` or `.woff` in `public/`. Split latin / latin-ext / vietnamese so a reader who needs only latin downloads only latin. ChooseChow is the reference: 9 files, 220 KB total, largest 40 KB.
*Today: reframe ships 3.9 MB of fonts and references two of them (136 KB `woff2`, 202 KB `woff`); the nine `.ttf` files (≈3.6 MB) are copied into the image and never requested.*

**F3 — `font-display: swap` plus a size-adjusted fallback face.** `swap` paints text immediately; the fallback's `size-adjust` / `ascent-override` keeps the swap from moving anything, which is what took ChooseChow's load CLS to 0. Generate with `fontFallbackMetrics.mjs`, list `"<family> Fallback"` directly after the web family in every stack.
*Today: no app carries fallback faces.*

**F4 — Preload only what the first screen paints**, `as="font" type="font/woff2" crossorigin`, one or two faces. More preloads compete with the CSS and the HTML for the same early bandwidth.
*Today: ChooseChow, pronto and ovasabi each preload two faces and already satisfy this; reframe preloads nothing (and has no self-hosted face to preload).*

**F5 — `@font-face` is reachable without JavaScript.** Either in `index.html` or in the extracted stylesheet (a `css` block from `@linaria/core`). It must never sit behind a component render.
*Today: satisfied everywhere — ChooseChow's `styles/fonts.ts` is now a `:global()` block in the build's CSS; pronto and ovasabi declare theirs in `index.html`.*

**F6 — No unused face ships.** `public/` is copied verbatim into the image; an unreferenced file is deploy weight and a supply-chain surface.
*Today: reframe's nine `.ttf`, and ovasabi's `Anybody[wdth,wght]-latin.woff2` and `-latin-ext.woff2` — 102 KB of duplicates left behind when the files were renamed to `Anybody-latin*.woff2` in September.*

**P1 — Prerender the shell and hydrate it.** `prerenderShell({ entry: 'src/entry-server.tsx', routes: [...], criticalCss: 'subset' })` in `vite.config.ts`, `mountRoot` in `main.tsx`, a `render(url)` that is identical on server and client for that URL: no `window`, no `Date.now()`, no randomness, no signed-in data; `useSyncExternalStore` needs its server snapshot; anything data-dependent renders a skeleton **of the final size**. Prerender every route whose first screen does not need a session.

**P2 — The prerendered HTML is the first screen, not the page.** `useFirstScreenCount(first, total, step)`. A single commit of a long list was a 70–77 ms long task at CPU 6x; per-frame growth keeps it off the main thread's critical path.

**P3 — Route-level code splitting, with a budget.** Every route that is not the landing route loads as its own chunk (`createLazyPage` from `@ovasabi/frontend-kit`). Budget: **entry chunk ≤ 120 KB brotli**, and no single chunk above 150 KB.
*Today: ChooseChow 53 routes, 0 lazy, entry 187 KB br; pronto 22 routes, 0 lazy, entry 116 KB br; ovasabi 5 lazy, 64 KB; trotters 12 lazy, 65 KB.*

**P4 — An image always knows its box before its pixels arrive.** Use `MinimalImage` (intrinsic `width`/`height`, or an explicit aspect ratio). An unsized image moved the content below it by 200 px in the §13 measurement; `MinimalImage` moved it 0.

**P5 — Motion runs on the compositor.** `transform` / `opacity` only, in CSS or WAAPI; JS-driven geometry per frame freezes under a main-thread stall (1 changed frame against 36). Already enforced by `frontend_surface_practices_check.mjs` ("keyframes animate only compositor properties").

## 3. Per-app inventory (fonts remeasured 2026-09-16, after the F-rule pass)

Every app passes F1, F2, F4 and F6. **No app carries fallback faces (F3)**, and
no app has adopted P1.

| App | Fonts | Prerender | Splitting | Notes |
| --- | --- | --- | --- | --- |
| chowdash_rider_v1 (ChooseChow) | 9 files / 220 KB, subset, `swap`, 2 preloads | none | 53 routes, **0 lazy**, entry 187 KB br | The lab proved 5,024 → 664 ms here; highest value in the fleet |
| pronto_v1 | 12 files / 184 KB, `@font-face` in HTML, 2 preloads | none | 22 routes, 0 lazy, entry 116 KB br | Writes 98 route HTML files at build; prerender fits that pipeline, but its splash screen inside `#root` must move out first |
| ovasabi_v1 | 9 files / 448 KB, 8 faces in HTML, 2 preloads | none | 5 lazy, entry (vendor) 64 KB br | Was 552 KB; two superseded `Anybody[wdth,wght]` duplicates (102 KB) deleted |
| trotters_v1 | Inter self-hosted, 2 files / 202 KB, 1 preload | none | 12 lazy, entry 65 KB br | Was Google Fonts |
| reframe_v1 | 11 files / 256 KB: Fraunces, Instrument Sans, IBM Plex Mono, InterDisplay | none | lazy routes present | Was a Google `@import` plus 3.9 MB of `public/fonts`, of which 3.7 MB was never requested. Go build still blocked by pre-existing proto drift |
| civic_watch_ng_v1 | Inter self-hosted, 4 files / 423 KB, 1 preload | none | single page | Was Google Fonts; italic faces declared, fetched only if italic text renders |
| docuos_v1, forest_v1, global_value_exchange_net_v1, marketer_v1, trader_v1 | no app fonts (kit stacks only) | none | scaffold | Adopt P1 when they grow a first screen worth painting |

**The kit names families it does not ship.** `ui-minimal`'s default tokens are
`"Fraunces"` (display), `"Instrument Sans"` / `"Inter"` (body) and
`"IBM Plex Mono"` (mono), and Foundation vendors none of them. An app using kit
defaults therefore renders in the system fallback unless it self-hosts those
faces itself — which is why three apps had reached for Google Fonts. Deciding
whether Foundation ships the trio or the tokens stop naming them is open.

## 4. Order of work

F1, F2, F4 and F6 are **done** across the fleet and enforced by
`make check-frontend-surface-practices`. What is left is the part no check can
assert:

1. **F3 everywhere — fallback faces.** No app has them, and this is the rule
   that actually moved a number: ChooseChow's load CLS went 0.119 → 0 on the mid
   phone only because a size-adjusted fallback stopped the swap from shifting
   anything. `swap` without it *causes* the shift. Generate with
   `frontend-lab/profile/fontFallbackMetrics.mjs`; do ChooseChow first, where the
   fixtures already exist.
2. **ChooseChow — P1 + P2 + critical CSS, then P3.** The measured 5,024 → 664 ms
   baseline and the largest entry chunk in the fleet (187 KB br, 53 routes, 0
   lazy). Its own Vite plugins mean the prerender patch reports `manual`.
3. **pronto — move the splash screen out of `#root`, then P1.** Its route-page
   plugin already writes 98 HTML files, so prerendering each is a natural
   extension; but `mountRoot` would otherwise try to hydrate markup React never
   rendered.
4. **The eight scaffold-shaped apps — P1 by managed patch.** `patch_frontend_prerender`
   handles these without a decision; each still needs its routes chosen and its
   first screen checked for `Date.now()`, `window` and signed-in data.
5. **reframe — unblock the Go build** (generated `identity.ts` exports types the
   code uses as values, `bootstrap` redeclares `RouteCatalog`, `startup` reads a
   config field that does not exist) before any of this can be measured there.

## 5. How to prove it

```bash
# Lab, both variants, brotli, interleaved: bytes, FCP/LCP, TBT, CLS, rules after FCP
cd foundation/frontend-lab && LOAD_RUNS=3 node profile/loadProfile.mjs
LOAD_DEVICE=small LOAD_RUNS=3 node profile/loadProfile.mjs        # 360×640, CPU 6x, 3G
LOAD_APP=choosechow LOAD_DEVICE=small node profile/loadProfile.mjs # real production build + fixtures

# Fallback metrics for a family (writes results/fonts/<app>-fallbacks.css)
node profile/fontFallbackMetrics.mjs

# Motion under a main-thread stall (screencast frames, not script timings)
node profile/stallTrace.mjs
```

A run with a page error is not a result. Report brotli sizes from built files — the lab's static server does not compress.

Device truth is the open half: the emulator setup is in the render-performance handover §4, and the same configuration has measured p50 17 ms and ~100 ms on different runs. Before trusting any device number, record thermal state, whether a projection delta arrived mid-run, and page-side rAF **and** platform `gfxinfo` for the same run; a physical floor device is still owed.

## 6. What Foundation owes — done 2026-09-16

1. **A managed patch to adopt the prerender in an existing app** — `tooling/scripts/frontend_prerender_patch.mjs`, run by `patch_frontend_prerender` in `scaffold_managed_patches.sh`. It adds `prerenderShell` last in the plugins array and swaps `createRoot(...).render(...)` for `mountRoot`, and it refuses rather than guesses in three cases: an app with its own Vite plugins (ChooseChow, pronto, ovasabi — those routes need a human to confirm the first screen is identical on both sides), an app whose `index.html` paints inside `#root` (pronto's splash screen), and an entry that is not a `createRoot` call (reframe re-exports `./app/main`). Idempotent; verified against all eleven apps.
2. **A font check** in `frontend_surface_practices_check.mjs`, reaching app code through `../frontend` the way the styled-components ratchet does. Enforces F1 (no third-party origin, `@import` included), F2 (`woff2` only), F3 (every `@font-face` declares `font-display`), F4 (at most two preloaded faces) and F6 (nothing unreferenced ships). Comments are stripped first — pronto's `index.html` explains in prose why it does *not* use Google Fonts, and a naive grep reports that as a violation.
3. **The check is now wired to run.** It was vendored into every app as `scripts/checks/` and invoked by nothing: no app Makefile had a target, so the styled-components ratchet had never executed outside Foundation. `check-frontend-surface-practices` is now in the template Makefile and in `FOUNDATION_LINT_CHECKS`, so `make lint` runs it.
4. **`docs/styling_design_practices.md` §12** carries the F- and P-rules, so new work starts compliant.
5. **Two bugs in `prerenderShell` that only a real build could find**, both of which would have hit the first app to adopt P1 — see §7, traps 8 and 9.

6. **A managed patch for the Tauri shell** — `tooling/scripts/native_shell_patch.mjs`, run by `patch_native_shell`. `native/package.json` and the three `tauri*.conf.json` files are create-mode too, so the same gap had stranded ten of eleven apps on four scaffold rules: the before-commands said `cd ../../frontend` (one level past the frontend, because they run from `native/`, not `native/src-tauri/`) and `npm run tauri`, which Tauri's mobile build phases call, did not exist. Only the `cd` segment is rewritten, so an app's own environment in that command survives — ChooseChow keeps its `VITE_APP_EDITION`. `frontendDist` is never touched (ovasabi builds to `dist/client`). The patched `package.json` is parsed before it is written; a text edit that would not round-trip is reported as a manual step instead.

Still open: `fontFallbackMetrics.mjs` graduates from the lab to `tooling/scripts/` once a second app needs it (F3 is the rule no check can enforce, and the one that actually moved CLS). And `ui-minimal`'s default tokens name four families Foundation does not ship — see §3.

## 7. Traps already paid for

1. **Prerender only what is identical on both sides.** Time, randomness, `window`, signed-in state and un-snapshotted `useSyncExternalStore` all produce hydration mismatches. A date in the first screen is enough (two ChooseChow equivalence cases differed for exactly that reason when captured across midnight).
2. **Critical CSS is a superset, never a subset.** `criticalCss.ts` drops a rule only when every selector names a class the markup lacks; conditional at-rules are filtered inside and referenced keyframes kept. `"subset"` matched full inlining on paint (585 vs 592 ms) and keeps the stylesheet cacheable — prefer it.
3. **Without critical CSS, prerender's FCP is hostage to a stylesheet round trip** on a jittery link: the same build measured 2,072 ms and 1,204 ms in different sessions. Inlining removed the variance along with the wait.
4. **Preloading more than the first screen needs makes FCP worse**, not better: preloads compete with the HTML and CSS for the same early bytes.
5. **`public/` is copied verbatim.** Deleting an unused font file is a real size win in the image even though no browser ever requested it.
6. **The emulator's screencast tops out near 25 frames/s**, so a stall-trace block must be 1 s there rather than 400 ms, and stalled captures must be dropped rather than averaged.
7. **framer-motion still injects rules at runtime** in the three apps that use it. That is animation, not styling, but it means "0 rules injected after first paint" must be measured, not assumed, in those apps.
8. **The SSR pass must bundle React and the router together.** `prerenderShell` defaulted to bundling only `@ovasabi/*`. The scaffold aliases `react` and `react-dom` to absolute paths, which Vite bundles, while a bare `react-router-dom` stayed external and loaded React a second time from `node_modules`; the renderer set the hook dispatcher on one copy and the router read it from the other, and the build died on `Cannot read properties of null (reading 'useContext')`. The default is now `noExternal: true`.
9. **The plugin recursed into its own SSR build.** Its `apply` guard reads `env.isSsrBuild`, which Vite derives from `build.ssr` when it resolves the config — and `build.ssr` was being set later, in a `config()` hook. The SSR build therefore re-read `vite.config.ts`, applied `prerenderShell` again, and recursed until the heap was exhausted. `build.ssr` is now set on the inline config, where the guard can see it. Both of these are invisible to a typechecker and to every test that does not run `vite build`.

## 8. Still open from the render-performance audit

P4 `virtualList`; P6 rules 3–5 (worker decode, decode queue, pixel budget — sizing is done); P8 device signals (ADPF / iOS thermal); P9 warm-up and hitch ledger; overlays onto `<dialog>`/`popover`; View Transitions for the calendar. ChooseChow's entry chunk (P3 above) is the largest single number left in the fleet.
