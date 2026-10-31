# Rust Dependency Update: Wasmtime 36.0.20 (2026-11-01)

## Result and scope

Commit `5c236c4` moves the optional Wasmtime host from 36.0.19 to 36.0.20.
The Cranelift and Wasmtime crates move together (0.123.19 to 0.123.20).
The update stays within the Wasmtime 36 series and changes no other lockfile entry.
The `wasm-runtime` feature remains optional.

## Evidence

- `cargo audit` on `runtime-sdk/rust/Cargo.lock` loaded 1,395 advisories and reported no vulnerability for 129 crates.
- The advisory database was the local copy at audit time. Re-run `cargo audit` with a fresh fetch before a release.
- `cargo test --workspace` and `cargo clippy --workspace --all-targets -D warnings` pass.

## Limits

The original commit did not record why 36.0.20 was needed.
This note does not claim that 36.0.20 fixes a specific advisory.
`docs/security_practices.md` and `docs/rust_runtime_practices.md` now state 36.0.20 as the minimum.
