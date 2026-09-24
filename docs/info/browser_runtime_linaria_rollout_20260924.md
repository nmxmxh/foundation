# Browser Runtime and Linaria Delivery

Date: 2026-09-24
Scope: Foundation Core, scaffold delivery, and local managed projects.

## Rust build delivery

The Rust SDK changes use ordinary Foundation module synchronization.
The project Makefile requires a managed patch because ordinary updates preserve this force-managed file.

The new patch replaces the known Foundation `build-rust-wasm` target after module synchronization.
The default recipe produces scalar and shared WASM artifacts. It requires no feature flag.
It uses the builder in `foundation/runtime-sdk/scripts/build_browser_wasm.sh`.
Shared builds require a nightly Rust toolchain with `rust-src`.

The patch validates the complete old target before replacement.
Custom build targets receive a review message. Other Makefile targets remain unchanged.
All twelve inspected projects contained the recognized old target.
The normal update integration test verifies module delivery, target replacement, and the patch ledger.

This focused rollout applied the Linaria correction only.
Projects receive the Rust build patch through their next ordinary Foundation update.
Reframe contains a custom browser worker integration. It remains compatible with the existing imported-buffer fallback.

## Linaria diagnosis

Two development paths bypassed style extraction:

1. The WyW filters rejected source URLs containing Vite query strings.
2. Dependency prebundling processed `@ovasabi/ui-minimal` outside the WyW transform pipeline.

Ovasabi and Chowdash already combined both corrections in their Vite configurations.
Their working Vite files remained byte-identical. Their Vitest configurations still required correction.

The maintained fix accepts query strings and excludes `@ovasabi/ui-minimal` from dependency prebundling.
The root exclusion also covers package subpaths. React remains eligible for optimization.
The patch retains `transformLibraries: true` and preserves unrelated project configuration.

The previous managed patch skipped files that already imported WyW.
The replacement parses TypeScript and upgrades existing configurations without executing them.
Dynamic, conflicting, or ambiguous configuration receives a review message without partial edits.

See [development extraction](../styling_design_practices.md#development-extraction) for the maintained configuration and upstream references.

## Applied local changes

| Project | Vite | Vitest |
| --- | --- | --- |
| chowdash_rider_v1 | Existing fix preserved | Updated |
| civic_watch_ng_v1 | Updated | Updated |
| docuos_v1 | Updated | Updated |
| forest_v1 | Updated | Updated |
| global_value_exchange_net_v1 | Updated | Updated |
| marketer_v1 | Updated | Updated |
| metered_v1 | Updated | Updated |
| ovasabi_v1 | Existing fix preserved | Updated |
| pronto_v1 | Updated | Updated |
| reframe_v1 | Updated | Updated |
| trader_v1 | Updated | Updated |
| trotters_v1 | Updated | Updated |

The rollout changed 22 configuration files across 12 projects.
Each change entered the project's `.foundation-patches.tsv` ledger.
Every result matched its staged candidate hash. Every second patch run made no changes.
Pre-edit copies were retained at the backup location recorded in the audit.

The Core audit at `benchmark-results/linaria_projects_20260924.json` records paths, hashes, patch output, and idempotency results.
Projects without a `.foundation` marker were outside this managed rollout.

## Regression evidence

The browser test uses the real `@ovasabi/ui-minimal` package, an application style, and a queried entry URL.
It checks visible rendering, computed CSS, and a style edit without a page reload.

| Configuration | Vite 7.3.6 | Vite 8.3.0 |
| --- | --- | --- |
| Original | Runtime tag error | Runtime tag error |
| Package exclusion only | Runtime tag error | Runtime tag error |
| Query filter only | Runtime tag error | Runtime tag error |
| Combined correction | Cold render and CSS HMR pass | Cold render and CSS HMR pass |

Both runs used WyW 2.5.1 and Chromium.
Core reports: `benchmark-results/linaria_dev_20260924_vite7.json` and `benchmark-results/linaria_dev_20260924_vite8.json`.

Validation completed:

- Six managed patch regression tests passed, including custom configuration preservation and idempotency.
- `make check-update-project` passed with an old Rust target and stale Linaria configuration.
- `make check-managed-patch-hygiene check-doc-references` passed.
- Frontend lab type checking passed.
- `git diff --check` passed.

Core rendering CI now runs `npm --prefix frontend-lab run test:linaria-dev`.
The tests validate the shared development pipeline. Full application test suites were outside this focused validation.

## Contract and ownership evidence

- Contract: the default Rust build emits shared and scalar artifacts. Linaria's component API remains unchanged.
- Invariant: managed edits preserve project domain code, custom configuration, and unrelated build targets.
- Fallback: unsupported patch shapes receive review messages. Runtime capability fallback follows the documented browser ABI contract.
- Boundary: Core owns the helpers and templates. Projects receive narrow, recorded configuration edits.
- Guard: patch tests, normal update integration, and Chromium development tests cover future delivery.
- Documentation: this report, styling practices, and the scaffold ownership boundary describe the changes.

See [scaffold delivery](../scaffold_ownership_boundary.md#browser-runtime-and-linaria-delivery-2026-09-24)
and the [browser ABI contract](../runtime_sab_capnp_contracts.md).
