# Frontend Lab

Foundation-only. Like `server-kit/go/servicebacked`, this directory is never
vendored into generated projects: `scripts/lib/scaffold.sh` syncs an allowlist
of modules and this is not one of them, and `tooling/foundation_ownership.tsv`
classifies it `foundation-test` / `foundation-only`.

It exists because Foundation's frontend packages (`ui-minimal`, `frontend-kit`,
`runtime-sdk/ts/browser-host`) ship primitives whose contract is a *rendering*
contract — a quality tier swaps computed shadows, a cull section skips paint, a
frame clock runs on a worker — and none of that is observable from a Node unit
test. Projects receive the primitives; the heavy apparatus that proves them
stays here. A behaviour is heavily tested, measured and profiled in this lab
before any project is asked to adopt it.

## Lanes

| Lane | Environment | Proves | Command |
| --- | --- | --- | --- |
| `ssr` | Node, styled-components SSR | Styling regression eval: every `Minimal*` component across a ~2,200-case prop sweep, reduced to the styles that win on each element, must hash to `baselines/ui-minimal-cascade.digests.json`. | `npx vitest run --project ssr` |
| `dom` | jsdom + Testing Library | Component and hook behaviour: markup, props, attributes written to the real document element, subscriptions and cleanup. | `npm test` |
| `browser` | Headless Chromium (Playwright via Vitest browser mode), cross-origin isolated | What only a real engine can say: computed styles under `data-ui-tier`, `content-visibility` skipping and scroll stability, the frame clock actually running on its worker pulse, pulse worker first-tick latency. | `npm run test:browser` |
| `gpu` | Headless Chromium on the **real GPU** (`--use-angle=metal --enable-unsafe-webgpu`), cross-origin isolated; one file at a time | The render-surface lane under real pipelines: adapter and features recorded, GPU backpressure (the ladder demotes under GPU overload and keeps ≤1 frame in flight), one device for several surfaces and its idle release (driver reports `destroyed`), prewarm vs cold first frame. Results in `results/gpu/`. macOS only as configured. | `npx vitest run --project gpu` |

**The GPU backend is not what you think.** Headless Chromium's default is
SwiftShader — a CPU implementation of Vulkan — with no WebGPU adapter. The
`ssr`, `dom` and `browser` lanes never needed a GPU; anything that measures
raster, composite or GPU work must use the `gpu` project or
`PROFILE_GPU=metal`, and record the renderer it got.

The browser lane serves COOP/COEP, so `SharedArrayBuffer` and module workers
exist exactly as in a production page; `isolation.browser.test.ts` fails first
if that ever stops being true, because every worker-lane test would otherwise
pass while testing a fallback.

### Baselines and results

- `baselines/` is committed. To accept an intended rendering change:
  `UPDATE_BASELINE=1 npx vitest run --project ssr`, and say why in the change.
- `results/` is not committed. Each `ssr` run writes the full cascaded
  snapshot there (and keeps the previous one), so a digest mismatch can be
  explained style by style. Browser tests write measurements there through
  `commands.writeFile` from `vitest/browser` — paths resolve from this
  directory, and browser mode does not forward console output.

### Profiling

`npm run profile` drives a scripted scroll through tier and cull variants and
writes a section 7 capture bundle to `results/profile/<stamp>/bundle.json`.
Knobs: `PROFILE_GPU` (`swiftshader` default, `metal`), `PROFILE_CPU_THROTTLE`,
`PROFILE_TRACE=1`, `PROFILE_RUNS`, `PROFILE_WARMUP`, `PROFILE_SECTIONS`.
Findings and open experiments are tracked in
`docs/ui_render_performance_research.md` section 13 and
`docs/ui_render_performance_handover.md`.

## What counts as evidence

- A lab result is evidence about Foundation's primitives on a desktop engine.
  It is not evidence about a floor device: device captures (Android emulator
  estimates, physical devices) are recorded separately in the research ledger.
- A test that has not been seen to fail is not yet a gate. When adding one,
  plant the violation it guards against and watch it go red once.
- Chromium in this lab is the revision cached for Playwright 1.62.1
  (`chromium-1234`); record it with any number you publish.

## Setup

```bash
cd frontend-lab && npm install
```

Playwright 1.62.1 reuses the Chromium already in `~/Library/Caches/ms-playwright`.
On a machine without it: `npx playwright install chromium`.
