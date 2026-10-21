# Rust Security Remediation: 2026-10-20

## Result and scope

The optional Wasmtime host now requires Wasmtime 36.0.19.
All twelve registered projects received the reviewed runtime dependency metadata and Rust audit controls.
Final audits cover 42 lockfiles with 18 unique contents.
They report zero entries in RustSec's vulnerability category.
Informational maintenance and unsoundness warnings remain visible.
These results do not establish deployed protection or complete runtime safety.

## Critical Wasmtime findings

Core and its project references previously locked Wasmtime 36.0.14.
The baseline audit reports these four advisories:

- `RUSTSEC-2026-0380`: Cranelift index overflow can cause miscompilation and sandbox escape.
- `RUSTSEC-2026-0379`: Large AArch64 branch tables can cause sandbox escape.
- `RUSTSEC-2026-0316`: Dynamic record lifting can exceed hostcall fuel accounting.
- `RUSTSEC-2026-0363`: Component handle tables have insufficient shared accounting.

The [index advisory](https://github.com/bytecodealliance/wasmtime/security/advisories/GHSA-897f-g5gm-fmxm)
and [branch advisory](https://github.com/bytecodealliance/wasmtime/security/advisories/GHSA-mghf-wjfp-p5xm)
identify 36.0.19 as a patched release.
The existing `wasm-runtime` feature remains optional.
Its minimum version and lockfile now prevent resolution to the affected 36.0.14 release.
The update stays within the existing Wasmtime 36 series.

## Additional application repairs

Native shells contained vulnerable quick-xml versions through plist 1.9.0.
Their lockfiles now select plist 1.10.2 and quick-xml 0.42.0.
The update also removes the anyhow 1.0.102 unsoundness warning through a compatible patch.
Metered had a native manifest without a lockfile; the dependency update generated its reviewed lockfile.

Pronto's RSS kernel now requires quick-xml 0.41.
The new parser emits separate reference events.
Its adapter now preserves named references, numeric references, whitespace, complete query strings, and first-link selection.
New regression tests cover those behaviors and invalid character references.
See the [CPU advisory](https://rustsec.org/advisories/RUSTSEC-2026-0194.html)
and [allocation advisory](https://rustsec.org/advisories/RUSTSEC-2026-0195.html).

Reframe now uses capnp and capnpc 0.24.1.
The [Cap'n Proto advisory](https://rustsec.org/advisories/RUSTSEC-2025-0143.html) requires at least capnp 0.24.0.
The generator now declares required schema constructors as unsafe.
Handwritten code denies unsafe operations.
Generated modules retain narrowly scoped allowances for the pinned generator's static schema tables.
Schema round-trip and malformed-message tests preserve the checked decoding boundary.
No schema, event, or public wire format changed.

## Regression protection and ownership

`rust_dependency_check.sh` audits discovered application and Foundation lockfiles before Rust static analysis.
Each scanner command has a 120-second default deadline.
Missing lockfiles, scanner failures, stale databases, and vulnerability entries stop the gate.
Informational warnings are reported without suppression.
Core CI and generated security workflows install cargo-audit 0.22.2.
CP-28 now names the Rust audit gate as automated supply-chain evidence.
The security workflow audits lockfiles without requiring platform-specific native compilation.

The managed `rust_security` patch copies only reviewed dependency metadata and audit controls.
It does not overwrite application source code or unrelated vendored runtime changes.
All twelve updates pass a second idempotence check.
The scaffold manifest already owns the security workflow.
The existing tooling copy distributes both audit scripts; their destinations and ownership modes remain unchanged.
Pronto and Reframe own their application manifests and parser or schema adapters.

## Audit coverage

Cargo-audit used a fresh RustSec checkout with this commit:

`e6f94f3760d5e6c54c1b05f204002263bc025524`

Each unique lockfile was audited once; SHA-256 maps each result to every identical file.
No architecture or operating-system filters were applied.
The baseline and final JSON reports are separate.
Final audits used `--no-fetch` against the checkout fetched during this run.
The actual Core gate separately fetched its advisory database and passed.

| Registered project | Lockfiles audited | Vulnerability entries |
| --- | ---: | ---: |
| chowdash | 3 | 0 |
| civic | 3 | 0 |
| docuos | 3 | 0 |
| forest | 3 | 0 |
| global | 4 | 0 |
| marketer | 3 | 0 |
| ovasabi | 4 | 0 |
| pronto | 4 | 0 |
| reframe | 4 | 0 |
| trader | 3 | 0 |
| trotters | 3 | 0 |
| metered | 3 | 0 |

Two additional audited lockfiles belong to Foundation Core.
Discovery excludes build targets, dependency caches, node_modules, and Git metadata.
Separate repositories outside the registered fleet are not covered.

## Validation

- Core SDK all-features tests passed: 169 tests, including WASM execution, parity, and Loom cases.
- Core native runtime tests passed: 25 tests.
- Core strict Clippy and formatting checks passed under rustc 1.95.0.
- Pronto RSS tests passed: 45 tests, including three new parser regressions.
- Pronto's changed crate passed Clippy with warnings denied.
- Reframe schema round-trips passed across all six schemas.
- Reframe rejects malformed frames across all six decoders; seven focused tests and strict Clippy passed.
- A full Chowdash native-shell compilation check passed with the updated plist dependency.
- Reframe's scoped workspace tests passed with the separate FFmpeg image test excluded.
- Rust audit regression tests passed for scanner status propagation and missing-lock rejection.
- The managed patch regression verifies exact metadata copying and a stable second pass.
- Fresh scaffold idempotence, manifest validation, and version consistency passed.

Detailed logs and JSON reports reside in `test-results/rust-security-2026-10-20/`.
The host's complete `1.95` toolchain reports rustc 1.95.0, matching the CI compiler.
The separate local `1.95.0` installation was incomplete and could not locate its standard library.
Validation used the complete installation; the incomplete installation was not removed.

## Remaining limits

Linux native dependencies retain `RUSTSEC-2024-0429` for glib 0.18.5 as an informational unsoundness warning.
Its patched line is glib 0.20 or later.
The GTK 3 dependency chain currently requires the older line.
A platform migration or reviewed upstream backport remains necessary to remove that warning.
No advisory ignore or fabricated patched version was added.
Maintenance warnings also remain for proc-macro-error and the unic text crates.

Reframe's complete workspace suite has a separate image-render test failure.
The test expected WebP output but received its SVG fallback through the local FFmpeg path.
The image crate does not depend on capnp.
This run does not claim that the complete Reframe suite passed.

WARNING: Existing native hosts retain their linked Wasmtime and parser libraries.
Rebuild and replace affected servers, native hosts, libraries, and desktop bundles before claiming deployed protection.
Browser engines and embedded WebViews require their own vendor updates.
No production deployment or desktop bundle replacement occurred.
