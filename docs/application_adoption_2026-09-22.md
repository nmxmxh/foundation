# Application Adoption Evidence

Date: 2026-09-22
Status: adopted in Pronto and Ovasabi; local Ovasabi frontend verified

## Scope

The official updater synchronized both projects from the current Foundation working tree.
It updated shared modules, schemas, documentation, checks, and managed patches.
Application customizations remained intact. Neither update used `--force`.
Both projects passed the updater's repeat-update check and the scaffold validator.
Seed drift remains visible for customized files. Those files were not re-baselined.

The synchronized modules include the native binding contracts and the refined render scheduler.
They also include prior Core improvements since the projects' September 16 update.
The concurrent `pushdelivery` module was included. Its Core tests passed before compatibility validation.
No push delivery service was activated by this adoption.

Frontend dependencies resolve through existing local package links.
The installed scheduler and native binding sources match Core by SHA-256.
Go module resolution completed in both projects. Pronto gained two checksum entries.

## Application Results

| Application | Adoption | Validation |
| --- | --- | --- |
| Ovasabi | Updated render worker, completion callbacks, elapsed-time pointer response, and 60 FPS highest tier. | 271 frontend tests, 15 backend packages, frontend build, and server/worker builds passed. |
| Pronto | Updated native runtime and binding APIs, with a permanent native test and benchmark. | 348 frontend tests, 39 backend packages across the initial run and translation recheck, frontend build, and server/worker builds passed. |

Pronto's native kernel, fusion bridge, and surprise packages also passed the race detector with the actual Rust library.
Its binding test verifies the numerical result and rejects an unsupported zero-copy requirement.
The test requires `PRONTO_FUSION_LIB`; an explicitly supplied invalid library fails.
Existing Go authority, arena dispatch, and fallback behavior remain intact.
The adoption does not route production Pronto requests through a new remote binding endpoint.
The authenticated distributed execution evidence remains in the Core service-backed suite.

Pronto's permanent benchmark used five samples, each with a 300-millisecond measurement interval.
Median direct FFI execution was 696.5 nanoseconds, with zero allocations.
Median checked binding execution was 868.1 nanoseconds, with one eight-byte allocation from response validation.
This measures the enforcement overhead. It does not establish an application latency improvement.
The [raw benchmark](evidence/native_bindings_2026-09-22/adoption_pronto_native.txt) preserves every sample.

## Local Runtime Verification

Ovasabi's existing frontend container serves `frontend/dist` through a read-only bind mount.
The production build therefore activated the adopted worker without rebuilding the image.
The configured build mode is `VITE_COMING_SOON=0`.
The page at `http://localhost:5173/` rendered the black hole and current application content.
The browser console contained no captured warnings or errors during the smoke check.
HTTP checks matched the built entry scripts, stylesheet, and graphics worker byte-for-byte.

The container retains its existing unhealthy status because `/healthz` proxies the absent backend server.
Pronto has no active local container. No backend service, migration, or remote deployment was started.
Both applications' server and worker entrypoints compile against their adopted modules.

The earlier controlled graphics result remains 17.4% more completed frames at fixed 1440p quality.
It is a reference workload result, not a new full-page FPS measurement.
See [Graphics FPS Refinement](graphics_fps_refinement.md) for sampling limits and results.

## Regression Repairs And Follow-Ups

Pronto's pricing test expected an obsolete implementation term in visible copy.
It now verifies the existing wallet guidance and payment documentation link.
Prices, payment logic, and visible copy remain unchanged.

The initial backend run failed only at the live DeepL check, which returned an exhausted-quota response.
That test contained an embedded credential. The fallback credential was removed.
The live check now requires `DEEPL_API_KEY`; local translation checks pass without external credentials.
The live provider check remains unverified after the quota failure.
WARNING: Rotate the exposed DeepL credential. Removing it from the current file does not remove repository history.

Frontend suites retain existing style-plugin warnings, JSDOM scroll warnings, and a delayed Vite shutdown.
Both suites exit successfully.
The scaffold validators retain existing native-shell header advisories.
Optional prerender adoption remains an explicit managed-patch advisory for the customized Vite configurations.

## Contract And Evidence

The adopted Core binding API is additive. Application request and event contracts did not change.
Ownership, tenant authorization, bounded execution, copy accounting, and GPU completion semantics remain the required invariants.
The scope includes Core distribution, application graphics, native compatibility tests, and validation repairs.
Application source fingerprints confirm that the pre-existing Ovasabi home changes were preserved.

The [adoption record](evidence/native_bindings_2026-09-22/application_adoption.json) contains source hashes, update reports, checks, and served asset hashes.
Application notes reside in Pronto's decisions directory and Ovasabi's handover directory.
