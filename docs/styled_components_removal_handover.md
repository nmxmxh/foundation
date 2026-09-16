# Styled-Components Removal — Handover

Status: **complete 2026-09-16** — every app is off styled-components, the kit bridge is deleted and a ratchet holds the line. Kept as the record of how it was done, what proved it, and the traps; §5 lists what is still open (commits, optional follow-ups).
Goal: **remove styled-components from every Foundation app entirely**, moving all app styling to Linaria (build-time extracted CSS), without visual regressions, layout shift, or extra bytes.
Program context: [ui_render_performance_research.md](ui_render_performance_research.md) §14.6–§14.8 (why and how the kit moved) and §15 (deliverables audit); [ui_render_performance_handover.md](ui_render_performance_handover.md) (the wider render-performance program, lab and device tooling).

**Nothing is committed** in Foundation or any app. Never use `git checkout -- <path>`, `git stash` or `git reset` in these trees — they hold large uncommitted change sets (this has destroyed work twice). Undo your own edits by hand.

## 1. Where things stand

### Done in Foundation (verified)

| Area | State | Evidence |
| --- | --- | --- |
| `ui-minimal` on Linaria | **No styled-components in the kit.** `@linaria/react` `styled` everywhere; styles extracted by `@wyw-in-js/vite` (`prefixer: false`). | Lab `src/ssr/linariaPort.eval.test.ts`: 2,176/2,240 cases identical to the saved styled-components cascade; the other 64 are one accepted invalid-input shape (FieldGrid given an array). Kit CSS 7.9 KB brotli; JS 30.3 KB brotli. |
| Runtime-free build-time modules | `tokens.ts`, `motionStyles.ts`, `globalStyles.ts`, `variantRules.ts` import no React and use explicit `.ts` relative imports. | Required for vendored builds (§4 traps). |
| Theme | `MinimalThemeProvider` / `MinimalThemeScope` / `useMinimalTheme` use a React context. Base theme variables, tiers and reset are static extracted CSS; `MinimalGlobalStyles` renders a `<style>` only for a non-base theme. | |
| Bridge | **Deleted 2026-09-16** with the last app: `src/styledComponents.tsx`, the `./styled-components` export, the optional peer and the patch script's bridge branch are gone, and every app has been re-synced. | Kit `tsc --noEmit` clean; all 11 apps + Foundation pass the surface check |
| Ratchet | `frontend_surface_practices_check.mjs` fails on any `styled-components` import in the kit, the template **or an app's own `frontend/src`** (it reads `../frontend/src` when run against a project's vendored `foundation/`). | Negative-tested: planted an import in ChooseChow's `src`, watched it fail, restored by hand |
| Motion | framer-motion gone from the kit; CSS `minimalEnter`, `useMinimalPresence`, WAAPI `createMinimalTimeline` (GSAP-style positions, stagger, seek/reverse, hold). | Lab browser tests incl. `timeline.browser.test.ts` (9). |
| Template (new apps) | No styled-components: package, Vite/Vitest configs (wyw also transforms the app's own `src`), `App.tsx` on Linaria + `minimalVars`, `theme.ts` = `MinimalThemeProvider`. Styling guide §3 is the Linaria format. | `docs/styling_design_practices.md` §3. |
| Tooling | `frontend_manifest_sync.mjs` requires `@linaria/*`, `@wyw-in-js/vite`, Babel presets (7.29.x), no longer styled-components. `project_scaffold_check.sh` expects `@linaria/react`. `frontend_linaria_patch.mjs` + `patch_frontend_linaria` (in `scaffold_managed_patches.sh`) add the plugin and the bridge to existing apps, idempotently. | Dry-run on three app shapes; second run no-op. |
| Lab | Every lane transforms through wyw-in-js; ssr digests re-baselined on the Linaria build. | ssr 2/2, dom 6/6, browser 22/22, gpu 8/8. |

### Apps

| App | Kit synced to Linaria | Own styled-components files | Theme reads in templates | `css` | `keyframes` | `createGlobalStyle` | `.attrs` | `styled(Component)` | `as=` |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| chowdash_rider_v1 (ChooseChow) | **yes — A+B done 2026-09-16, production-verified**: `npm run build` clean (entry CSS 24.7 KB brotli), tsc 0, vitest 168/168; style equivalence **26/29 identical** (1 intentional `color-mix` for a hex-alpha brand shadow, 2 date-rollover renders). Editions still restain at runtime through `minimalVars`; `vitest.config.ts` gained `css: true` so tests can assert computed styles (trap 24) | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| ovasabi_v1 | **yes — A+B done 2026-09-15, production-verified**: `npm run build` clean 3/3 (entry CSS 7.5 KB brotli), tsc 0, vitest 256/256; style equivalence **16/18 identical** after normalisation, the 2 others are the per-instance variable union (PathwayCard accent, verified per card). React-free `styles/themeTokens.ts` split out (trap 19); discipline tests (`surfaceBoundary`, `squareCorners`) taught the new module and `minimalVars.radius` | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| pronto_v1 | **yes — A+B done 2026-09-15, production-verified**: reinstalled with npm; `npm run build` clean (CSS 11.7 KB brotli), tsc 0, vitest 306/306; style equivalence **24/24 identical**. `/docs` code samples use `${''}` for blank lines (trap 16) | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| trotters_v1 | **yes — A+B done 2026-09-15, production-verified**: `npm run build` clean (entry CSS 8.8 KB brotli), tsc 0, vitest 35/35; style equivalence **12/14 identical**, the 2 others are harness artifacts (per-instance variable union; SSR Suspense error text). Resolved theme for templates now comes from React-free `styles/themeValues.ts` (trap 19); framer-motion kept | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| reframe_v1 | **yes — A+B done 2026-09-15, styled-components removed** (framer-motion kept, app-owned). vitest 5/5; tsc = the pre-existing 90 in 9 files. `vite build` is blocked by a **pre-existing** uncommitted proto drift (`src/types/protos/identity/v1/identity.ts` exports only interfaces; `runtimeClient.ts` uses them as values) — a verification build with `shimMissingExports` extracts all 23 keyframes and every `data-*` rule, no styled residue. Manifest restored from HEAD + migration deltas (trap 15); pre-restore copy was template-shaped | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| global_value_exchange_net_v1 | **yes — A+B done 2026-09-15** (tsc clean, vitest 1/1, build clean). Own non-Minimal theme kept as build-time constants in `styles/tokens.ts`; globals in `styles/theme.ts` | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| docuos_v1 | **yes — A+B done 2026-09-15** (vitest 1/1, build clean; tsc: only the pre-existing 32 in `src/generated/prototypeRuntime.ts`). Button/Input variants → `data-*` rules | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| forest_v1 | **yes — A+B done 2026-09-15** (same files as docuos; vitest 1/1, build clean; tsc: only the pre-existing 2 generated errors) | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| civic_watch_ng_v1 | **yes — A+B done 2026-09-15, styled-components removed** (vitest 1/1, `vite build` clean, global block extracted; tsc: only the pre-existing 32 redeclarations in `src/generated/prototypeRuntime.ts`) | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| marketer_v1 | **yes — A+B done 2026-09-15** (tsc clean, vitest 1/1, build clean) | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| trader_v1 | **yes — A+B done 2026-09-15** (tsc clean, vitest 1/1, build clean) | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |

**The goal is met: 0 styled-components files and 0 `package.json` entries across all 11 apps (survey re-run 2026-09-16), the kit bridge is deleted, and a ratchet keeps it out.** The counts below were the starting inventory on 2026-09-15; "theme reads" counted `({ theme }) =>` / `props.theme` interpolations, which were the bulk of the work.

**Do not run a bare `update-project.sh --foundation-only` on an unmigrated app.** It vendors the Linaria kit, and without the plugin the app's build and tests break (Linaria's `styled` throws when not extracted).

## 2. The plan

Two phases per app. Phase A keeps the app shipping on the Linaria kit; phase B removes styled-components from the app's own code. Small apps can do both at once.

### Phase A — adopt the Linaria kit (proven on ChooseChow)

1. `cmp` any vendored-foundation local edits against Foundation before syncing (`update-project.sh` uses `rsync --delete` per module). ovasabi_v1's and trotters_v1's "local edits" turned out byte-identical to Foundation.
2. `./scripts/update-project.sh ../<app> --foundation-only`
3. `node tooling/scripts/frontend_linaria_patch.mjs ../<app>/frontend` — adds the Vite/Vitest plugin; follow any `manual` line (an app whose `vite.config.ts` is not in scaffold shape — pronto, ovasabi, ChooseChow — needs the plugin added by hand, and its wyw `include` widened to the app's own `src`).
4. `node tooling/scripts/frontend_manifest_sync.mjs templates/frontend/package.json ../<app>/frontend/package.json`, then install with the app's package manager. This also moves the app to Vitest 5 → `src/test/setup.ts` must import `'@testing-library/jest-dom/vitest'`.
5. Tests that render inside `MinimalThemeProvider` directly → the app's `AppThemeProvider` (ChooseChow had 2).
6. `npx tsc --noEmit -p tsconfig.app.json` (the app's `npm run typecheck` checks nothing — root tsconfig is `files: []`), `npx vitest run`, `npx vite build`.

### Phase B — remove styled-components from app code

Per module, convert to the Linaria format in `docs/styling_design_practices.md` §3:

| styled-components | Linaria replacement |
| --- | --- |
| `import styled, { css } from 'styled-components'` | `import { styled } from '@linaria/react'` |
| `${({ theme }) => theme.color.x}` (and `space`, `radius`, `shadow`, `typography`, `zIndex`, `breakpoint`, `focus`, `control`, `overlay`, `motion`) | `${minimalVars.color.x}` — `minimalVars` covers every theme group; values are `var(--minimal-…, fallback)`, and an app theme passed to `MinimalThemeProvider` overrides them through `MinimalGlobalStyles`. Import from `@ovasabi/ui-minimal/tokens` in build-time helpers. |
| Theme keys the app added beyond the ui-minimal theme shape | No `minimalVars` entry exists. Either map to the nearest ui-minimal token, or declare app-level CSS variables in one global `css` block and read them as literals. **Check each app's `styles/theme.ts` for extra keys first.** |
| `${({ $x }) => $x ? 'a' : 'b'}` in a property value | Allowed (becomes a per-element CSS variable). For discrete variants prefer `variantRules` + `data-*` attributes — cheaper for the style engine. |
| Prop function returning a whole block (`css\`…\``) | `variantRules(name, values, block)` with `data-minimal-<name>`/app-owned `data-*` attribute, or split into two components. Linaria cannot compile a block-returning function. |
| `css\`…\`` fragment interpolated into templates | Plain template string constant (evaluated at build time). |
| `keyframes\`…\`` + `${name}` reference | `@keyframes name { … }` inside the template that uses it (Linaria scopes the name). A keyframe under `:global()` referenced by name elsewhere **does not resolve**. |
| `createGlobalStyle` | `css\`:global() { … }\`` from `@linaria/core` in a module the app imports; dynamic values → CSS variables. |
| `.attrs(...)` | Set the attributes in the component that renders the styled element. |
| `styled(Component)` | Supported if the component accepts `className` (and `style` for prop variables). |
| `as=` | Supported. |
| `useTheme()` / `ThemeProvider` | `useMinimalTheme()` / `MinimalThemeProvider`. |
| Interpolated callbacks in templates (`${list.map(x => …)}`) | Hoist to a module constant — wyw-in-js cannot hoist expressions that reference a function parameter. |

When an app has no styled-components left:
- remove `styled-components` from its `package.json`, Vite/Vitest aliases and dedupe, and any `DefaultTheme` augmentation;
- make `AppThemeProvider` plain `MinimalThemeProvider` (drop the bridge);
- widen its wyw `include` to the app's own `src` (the template's regex already does: `/[\\/]src[\\/].*\.[jt]sx?$/`).

~~When **every** app is done~~ — **done 2026-09-16**: the kit's `styledComponents.tsx`, its `./styled-components` export and the optional peer are deleted, the bridge branch is out of `frontend_linaria_patch.mjs`, every app is re-synced, and the surface check carries the ratchet (negative-tested).

### Suggested order

1. ~~Non-production apps~~ — **done 2026-09-15:** trader_v1, marketer_v1, civic_watch_ng_v1, docuos_v1, forest_v1, global_value_exchange_net_v1, reframe_v1 (styled-components gone from code and `package.json`).
2. ~~Production apps~~ — **done 2026-09-16:** pronto_v1 (after its reinstall), trotters_v1, ovasabi_v1, ChooseChow. Each carries the before/after style comparison (§3); the non-production apps were verified by build, tests and extracted-CSS inspection only, so re-run the harness on one of them if a doubt ever arises.

Patterns learned on reframe that the production apps will hit: a `$prop` passed to `styled(KitComponent)` or `styled(motion.x)` is forwarded (Linaria does not strip transient props) — use `data-*`; a prop value followed by a non-unit suffix (`${fn}22` hex alpha) must be folded into the function; an app's own non-Minimal theme object can stay as build-time constants in a runtime-free `styles/tokens.ts`.

## 3. How to prove no regression

- **Build-level:** `tsc -p tsconfig.app.json`, the app's tests, `vite build` with no wyw-in-js errors.
- **Style equivalence:** `frontend-lab/migration/styled-components/` holds the app-level harness used for the four production apps, built on the lab's `cascade.ts`. Copy `equivalence.snapshot.test.tsx.template` into `<app>/frontend/src/__style_equivalence__.snapshot.test.tsx` with a `__style_cases__.tsx` beside it (`cases/*.cases.tsx` are the four written so far — routes, providers, a per-case `setup` for session/edition), then:
  - **before** (must precede the conversion): `STYLE_EQ_MODE=before STYLE_EQ_OUT=<dir> [STYLE_EQ_CSS=<phase-A dist>/assets] npx vitest run src/__style_equivalence__.snapshot.test.tsx` — pass `STYLE_EQ_CSS` when the app is already on the Linaria kit, so the kit's extracted rules sit under the styled-components sheet;
  - **after**: build with `cssMinify: false, manifest: true` into a scratch dir (minified CSS drops the attribute quotes the cascade matches on, and the manifest gives chunk-dependency CSS order), then run the same command with `STYLE_EQ_MODE=after STYLE_EQ_CSS=<dist>/assets`;
  - compare: the run writes `comparison.json`; `normalized_compare.mjs <dir> "<attrs>"` folds the migration's own key-shape changes (`&&`, `data-*` qualifiers, `:not([data-…])`, whitespace, quotes, repeated keyframes). Every remaining diff must be explained against the built CSS — see trap 22 for what the harness cannot see.
  Results: pronto 24/24, trotters 12/14, ovasabi 16/18, ChooseChow 26/29 identical; every other case explained (harness artifact, an intentional `color-mix`, or a date-dependent render).
- **Browser-level:** load the production build in the lab's load profile (`frontend-lab/profile/loadProfile.mjs` pattern: CPU 4x, Slow 4G, Metal) — CLS must stay 0, rules injected after first paint 0, and record bytes (brotli) before/after.
- **Visual spot checks** on key screens (the in-app Browser pane, or the emulator for ChooseChow — see the render-performance handover §4).
- A gate that has not been seen failing is not a gate: plant a violation once, watch it fail, restore by hand.

## 4. Traps already paid for

1. **wyw-in-js evaluates template imports at build time from the file's real path.** For a vendored kit that path is `app/foundation/ui-minimal/ts/src`, where React and extensionless TS imports do not resolve. Build-time modules must be runtime-free and use explicit `.ts` imports. The same applies to app helpers imported into templates.
2. **Inline `type` import specifiers** (`import { type X }`) break wyw-in-js's evaluation parser — use `import type`.
3. **Callbacks inside template interpolations** cannot be hoisted ("identifier … is a function parameter") — move them to module constants. A hoisting script that inserts in reverse order corrupts the file; recompute positions after each insert.
4. **`:global()` keyframes are still scoped** — declare keyframes in the template that uses them.
5. **Vendor prefixes:** always `prefixer: false`.
6. **The kit's provider no longer feeds styled-components.** Any app code or test that renders inside `MinimalThemeProvider` directly and reads `theme` in a styled template gets `undefined` (ChooseChow: `reading 'md'` / `'borderSubtle'`).
7. **Manifest sync bumps Vitest 4 → 5**; jest-dom matcher types then need `@testing-library/jest-dom/vitest`.
8. **Babel presets:** the verified line is 7.29.x (8.x untested with wyw-in-js here).
9. **Harness normalisation traps** (if you extend the equivalence harness): resolve `var(--minimal-…)` fallbacks *before* normalising whitespace; resolve Linaria per-element variables from the element itself before any case-wide union (repeated components share variable names).
10. **Grep-based safety checks** that search for a word also match comments — check import statements.
11. **Lab test timing:** running several lab projects at once can fail the frame-clock worker-lane test on its 120 ms grace window; re-run the browser project alone before believing it.
12. **Lab static servers do not compress** — report gzip/brotli from built files, not transfer bytes.
13. **Known noise on every migrated app, not a regression:** wyw-in-js warns "Runtime require() fallback during eval" for the kit's `tokens.ts` / `variantRules.ts` / `motionStyles.ts`, and `vitest run` ends with "close timed out … something prevents 2 Vite servers from exiting" (open file handles), still exit 0. ChooseChow and civic_watch_ng_v1 both show it; unmigrated apps do not. Worth fixing in the kit (`importOverrides`), but do not chase it per app.
14. **The sync is `rsync --delete`, so an agent's permission classifier may refuse it** as irreversible. Check local edits first (step A1), then have the user run or approve it.
15. **A stale `node_modules` can hide a clobbered `package.json`.** reframe_v1's uncommitted manifest had been replaced by the template's (dropping `capnp-es`, `@ovasabi/runtime-browser`, `@ovasabi/config-contracts`, the codegen `prebuild` scripts, and moving React 18 → 19, router 6 → 7), yet tsc/tests passed because `node_modules` still held the old install; the migration's `npm install` pruned it and the build broke. Before installing, run `git diff HEAD -- frontend/package.json` and make sure every change is one this migration intends.
16. **wyw-in-js 2.5.1 (the latest release) deletes blank lines from every module it transforms.** `transform/esm/transform/generators/transform.js` `normalizeOxcPreparedESM` runs `.replace(/\n{2,}/g, "\n")` over the emitted code, template literals included. Harmless in CSS; a real content change in any displayed multi-line string (pronto's `/docs` code samples lost their blank lines in the production bundle). Write a blank line as `${''}` on its own line (same string value, survives the collapse). Scan an app with `frontend-lab/migration/styled-components/blankline_scan.mjs` — most hits are CSS templates; read each. Worth an upstream report.
17. **`babelOptions` is not a wyw 2.5.1 option.** It is ignored at runtime and fails `tsc -b` (excess property), so `npm run build` broke in the template and every app that had it. Removed from the template, the patch script and all app configs. Always verify with the app's real `npm run build`, not just `vite build` + `tsc -p tsconfig.app.json` (neither typechecks the Vite config).
18. **`styled(KitComponent)` wrappers must win by specificity, not by order.** styled-components always injected a wrapper after the kit; with extracted CSS the order depends on how lazy chunks load. Put wrapper declarations under `&& { … }` (and `&& child`, `&&:hover`). Seen on trotters' `AuthCard`/`OptionCard`, ovasabi's `Empty`/`Filter`/`Shell`, reframe's wrappers.
19. **A template that interpolates a module importing the kit index fails the build** ("dependency graph is incomplete"), because wyw evaluates it and reaches React. Split React-free values out: trotters `styles/appTheme.ts` + `styles/themeValues.ts` (`createMinimalTheme` from `@ovasabi/ui-minimal/tokens`, which is the same function the index re-exports); ovasabi `styles/themeTokens.ts` (palette, layout, breakpoints, type stack), re-exported by `styles/theme.ts`. Type `appTheme` from the tokens module too — even an `import type` from the kit index pulls React into the graph.
20. **Transient props are forwarded by Linaria.** `$prop` on `styled(Link)`, `styled(NavLink)` or `styled(motion.x)` reaches the DOM. Use `data-*` + static rules, or wrap the component with an `omitTransientProps` helper (trotters/ovasabi `src/lib/omitTransientProps.tsx`) when a prop value is interpolated. `styled(NavLink)` also needs a type cast (NavLink's `className` may be a function). A prop function that returns a whole declaration (`${p => p.x && 'width: 10px;'}`) or a non-unit suffix (`${p => p.c}22`) must be rewritten.
21. **Module-level `theme.motion.*Duration}s` reads** map to `minimalVars.motion.micro|standard|slow` (the variables carry the `s`). `theme.spacing.X` maps one-to-one to `minimalVars.spacing.X` (the deprecated names keep their own offset variables).
22. **Equivalence harness limits** (`frontend-lab/migration/styled-components/`): it has no specificity model — rules win by sheet order, so build with `manifest: true` and let it order CSS by chunk dependency; it cannot see per-instance values for descendant rules (a case-wide variable union), so repeated components with different `$prop` values show a false diff; SSR error text embedded in markup differs by engine. `normalized_compare.mjs` folds `&&` doubled keys, migration-added attribute qualifiers and `a/b` vs `a / b` whitespace. Each remaining diff must be explained against the built CSS.
23. **wyw can fail a build intermittently** with "Resetting transform and evaluation caches … because the dependency graph is incomplete" on a kit module (`motionStyles.ts` → `tokens.ts`). ovasabi's `npm run build` failed once and then passed 3/3 unchanged. Re-run before chasing it; a failure that repeats is trap 19.
24. **Extracted CSS is not applied in jsdom by default.** A test that asserts a computed style (`getComputedStyle(x).position`) passed under styled-components, which injected its sheet at runtime, and fails afterwards. Set `test.css: true` in the app's `vitest.config.ts` so Vitest applies the stylesheets the build emits (done for ChooseChow). Source-scanning tests need updating too: a test that greps for `` keyframes` `` must read the inline `@keyframes` instead.
25. **Snapshots capture the day.** A page that renders a date ("Sep 15 – Sep 21", a weekday column) differs between a before snapshot taken yesterday and an after render today. Two ChooseChow cases differ for exactly this reason. Capture both sides on the same day, or read the diff before believing it.
26. **Harness markup traps** (fixed in the shared copy, worth knowing): resolving a token whose value carries double quotes (a font stack) into an HTML attribute ends the attribute and shifts every element index — substitute single quotes; keyframes inlined into several templates repeat, so compare keyframe sets; the cascade normaliser strips the space after `)`, which glues a resolved variable to the next token (`0sinfinite`), so compare values whitespace- and quote-insensitively.

## 5. Waiting on the user

- ~~Enforcement manifest refresh~~ — **done 2026-09-16** (twice: after the first round of protected-file changes, and again after `frontend_linaria_patch.mjs` lost the bridge branch and `frontend_surface_practices_check.mjs` gained the ratchet). `tooling/scripts/enforcement_integrity_check.sh .` passes. Refresh again if a protected file changes: `HUMAN_SUPERVISED_CHECK_UPDATE=1 … --write`, snapshotting the manifest first and diffing after.
- ~~pronto_v1 reinstall~~ — **done**: `rm -rf node_modules && npm ci` (the lockfile's own manager); the stale pnpm tree is gone.
- **Commits** in Foundation and every touched app, in reviewable slices (kit port; tooling/template; per-app phase A; per-app phase B; kit bridge deletion + ratchet). **Nothing is committed yet.**
- **Optional follow-ups this migration exposed**, none blocking: report the wyw-in-js blank-line bug upstream (trap 16) and pin a fixed release when one lands; decide whether `@babel/preset-*` should stay in the frontend manifest now that wyw 2.5.1 ignores `babelOptions` (trap 17); docuos_v1, forest_v1 and civic_watch_ng_v1 had a red `npm run build` **before** this work (generated `prototypeRuntime.ts` redeclarations) and still do.

## 6. Commands

```bash
# Re-run the per-app styled-components survey (from the OVASABI STUDIOS root)
for repo in chowdash_rider_v1 civic_watch_ng_v1 docuos_v1 forest_v1 global_value_exchange_net_v1 marketer_v1 ovasabi_v1 pronto_v1 reframe_v1 trader_v1 trotters_v1; do
  s=$repo/frontend/src; files=$(grep -rlE --include='*.ts' --include='*.tsx' "from ['\"]styled-components['\"]" $s 2>/dev/null)
  echo "$repo $(echo "$files" | grep -c . ) files, $(echo "$files" | xargs grep -oE '\(\{ *theme[^}]*\}\) *=>|props\.theme' 2>/dev/null | wc -l | tr -d ' ') theme reads"
done

# Foundation checks
cd foundation/ui-minimal/ts && npx tsc --noEmit -p tsconfig.json
node foundation/tooling/scripts/frontend_surface_practices_check.mjs foundation
cd foundation/frontend-lab && npx vitest run --project ssr && npx vitest run --project dom && npx vitest run --project browser

# Kit build + sizes, and the port equivalence proof
cd foundation/frontend-lab && node src/ssr/buildKit.mjs
npx vitest run --project ssr src/ssr/linariaPort.eval.test.ts

# Load profile (production build, CLS/bytes/rules) — pattern to adapt for app builds
LOAD_RUNS=3 node profile/loadProfile.mjs
```

## 7. Still open from the render-performance audit (§15)

Not part of this goal, but touched by it: paint before JavaScript (link the extracted CSS in the HTML head, prerender the shell — Linaria makes the CSS a static file), P6 image pipeline with intrinsic sizes (the remaining CLS risk in real apps), the P3 motion stall trace, device runs of P1/P2, ChooseChow's 192 KB-brotli main chunk (code-splitting), overlays onto `<dialog>`/`popover`, View Transitions for the calendar.
