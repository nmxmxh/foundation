# Graphics FPS Refinement

Date: 2026-09-22
Status: implemented; reference GPU measurements captured
Owner: Runtime Performance

## What Changed

Foundation resumes missed render slots when the outstanding GPU work completes.
The previous scheduler waited until another full cadence slot.
A small budget overrun could therefore produce a large frame gap.
The new wake preserves the current cadence, quality floor, and single outstanding frame.
Visibility changes retain the GPU fence. Retiring a pass invalidates that fence.

Ovasabi's highest black-hole tier now targets 60 FPS instead of 40 FPS.
Its resolution and 118 integration steps remain unchanged.
Pointer following uses elapsed time, preserving the original response across cadences.
The orbit integrator already advances from elapsed time.
Lower tiers retain their existing conservative targets.

## Research Applied To This Implementation

| Finding | Application |
| --- | --- |
| GPU timestamps alone can disagree with complete workload throughput. | Compare completed frames, CPU submission cost, and completion gaps at identical quality. |
| Frame-time variance matters alongside average FPS. | Record p95 and p99 completion gaps, including missed slots. |
| Dedicated workers can support animation callbacks with an owner window. | Correct the obsolete runtime comment. Keep explicit timer cadences for this bounded decorative lane. |
| Small buffer uploads already have an efficient portable path. | Retain the 64-byte uniform upload through `writeBuffer`. |
| Render bundles mainly reduce repeated CPU command work. | Defer bundling: measured CPU draw cost is much smaller than submission-to-completion cost. |

Sources, accessed 2026-09-22:

- [WebGPU timing and throughput](https://webgpufundamentals.org/webgpu/lessons/webgpu-timing.html)
- [Rendering frame budgets and variance](https://web.dev/articles/speed-rendering)
- [Worker animation callbacks](https://developer.mozilla.org/en-US/docs/Web/API/DedicatedWorkerGlobalScope/requestAnimationFrame)
- [WebGPU buffer uploads](https://toji.dev/webgpu-best-practices/buffer-uploads)
- [Render bundle tradeoffs](https://toji.dev/webgpu-best-practices/render-bundles.html)

The existing shader already reuses its pipeline, bind group, and uniform storage.
It also uses one fullscreen triangle and bounds each ray to its declared step count.
These properties make pixel work and scheduling stronger candidates than additional transport abstractions.

## Controlled Workload

The lab imports Ovasabi's actual black-hole pass and the Foundation package export.
It compares the current scheduler with a frozen pre-change fixture.
The fixture changes only imports to public package exports.
Both schedulers receive identical inputs and completion callbacks.

Each sample excludes one warm-up second and measures five seconds.
Six samples alternate A/B, B/A, and A/B order.
The camera retains its initial orbit pose, with zero simulation delta and shader time fixed at 30 seconds.
Every sample uses 118 integration steps, one quality tier, and fixed backing dimensions.
No quality demotion can inflate these results.
The page remains visible. The lab rejects samples interrupted by a hidden page.

The reference host is an Apple M1 Pro running macOS and Chromium 153 through the in-app browser.
WebGPU and cross-origin isolation were available.
The measurements count completed render frames, not compositor presentation or hardware timestamp queries.
No mobile hardware or thermal endurance run is represented.

## Measurements

| Workload | Previous scheduler | Refined scheduler | Interpretation |
| --- | ---: | ---: | --- |
| 1920 × 1080, target 60 FPS | 59.98 FPS | 59.95 FPS | Both meet the target within sampling variation. |
| 2560 × 1440, target 60 FPS | 50.37 FPS | 59.12 FPS | About 17.4% more completed frames at identical quality. |
| 2560 × 1440, p95 completion gap | 32.72 ms | 21.11 ms | About 35.5% smaller p95 gap. |

Values are medians across three samples per implementation.
Each 1440p baseline sample achieved 49.17–53.19 FPS. Refined samples achieved 58.98–59.19 FPS.
Both implementations held the maximum outstanding frame count at one.
Median submission-to-completion costs remained approximately 12.3–12.6 ms at 1440p.
This supports the scheduling explanation; the shader performs the same work.
The [sample record](evidence/native_bindings_2026-09-22/fps_samples.json) preserves counts, durations, and frame-gap percentiles.
The capture uses an upper empirical order statistic, with its exact index formula recorded beside the samples.

A separate 1080p comparison changes only the requested cadence with the refined scheduler.
Median completed FPS increased from 39.94 to 59.97, approximately 50.2%.
All six samples retained identical resolution, shader detail, and a maximum outstanding frame count of one.
This target increase consumes available GPU capacity. It is distinct from reducing idle time through the scheduler fix.

## Evidence And Compatibility

Public Core signatures and wire messages remain unchanged by this refinement.
The intentional behavior change is earlier recovery after GPU completion.
Ovasabi's highest default frame target changes. Its lower quality tiers remain available.
The scope includes the Core render worker and application-owned Ovasabi graphics files.
Pronto's native computation path is outside this graphics change.

Regression tests cover missed-slot recovery, changed cadence floors, and visibility transitions with outstanding GPU work.
Existing retirement, completion failure, timeout, quality, and ownership tests remain active.
Pointer tests compare equal wall-clock responses at 24, 30, 40, 60, and 120 FPS.
Existing shader parity tests guard both WebGPU and WebGL sources.
All 268 Core browser tests and the Core type check passed.
The focused render-worker coverage is 92.57% statements and 95.29% lines, above the existing package thresholds.
All 19 statements changed by this refinement were covered.
Application TypeScript and lab TypeScript checks passed.
Ovasabi passed 33 targeted graphics, capability, and lifecycle tests against updated Core.
Existing style-plugin warnings and a delayed Vite shutdown remained visible in the lifecycle run.

Ovasabi source uses its existing completion callback contract.
Both reference applications now contain the updated Core package.
Ovasabi's local frontend serves the rebuilt worker through its existing development mount.
See [Application Adoption Evidence](application_adoption_2026-09-22.md) for application checks and deployment boundaries.

## Reproduction

From Foundation Core:

```sh
make graphics-binding-lab OVASABI_PROJECT=/absolute/path/to/ovasabi_v1
```

Open `http://127.0.0.1:5187/fps.html`.
Choose the scheduler comparison and a backing resolution.
Keep the page visible until all six samples complete.
Save the evidence with the page control.
The separate target comparison measures 40 versus 60 FPS with the refined scheduler.

## Next Evidence Boundaries

1. Measure full-page presentation and interaction during scrolling, with all visible surfaces active.
2. Repeat the fixed-quality suite on representative Android, iPhone, integrated, and discrete GPUs.
3. Measure sustained temperature and power before increasing conservative mobile targets.
4. Profile expensive orbit poses before changing integration arithmetic or introducing approximation.
5. Compare display-aligned worker scheduling for interactive surfaces, with a timer fallback.

Increasing the target can increase GPU work per second.
The adaptive ladder, visibility stop, and backing limits remain essential controls.
A general device guarantee requires the device matrix above; these local results do not establish one.
