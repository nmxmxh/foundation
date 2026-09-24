#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
manifest="${1:-$script_dir/../rust/Cargo.toml}"
output="${2:-$script_dir/../ts/browser-host/fixtures/browser-abi}"
profile="${3:-release}"
package="${4:-}"
if [[ $# -eq 0 ]]; then package="ovrt-parity-fixture"; fi
target_dir="$(cd "$(dirname "$manifest")" && pwd)/target"
toolchain="${RUST_WASM_TOOLCHAIN:-nightly}"

if [[ "$profile" != release && "$profile" != debug ]]; then
    echo "browser WASM profile must be release or debug" >&2
    exit 1
fi

build_args=(--locked --manifest-path "$manifest" --target wasm32-unknown-unknown)
if [[ -n "$package" ]]; then build_args+=(-p "$package"); fi
if [[ "$profile" == release ]]; then build_args+=(--release); fi

mkdir -p "$output"
CARGO_TARGET_DIR="$target_dir" cargo build "${build_args[@]}"
shopt -s nullglob
artifacts=("$target_dir/wasm32-unknown-unknown/$profile/"*.wasm)
if [[ ${#artifacts[@]} -eq 0 ]]; then
    echo "no browser WASM artifacts were produced" >&2
    exit 1
fi
for artifact in "${artifacts[@]}"; do cp "$artifact" "$output/$(basename "$artifact")"; done

# Rebuild std so allocator synchronization uses WASM atomics.
CARGO_TARGET_DIR="$target_dir/browser-shared" \
RUSTFLAGS="-C target-feature=+atomics,+bulk-memory,+mutable-globals -C link-arg=--shared-memory -C link-arg=--import-memory -C link-arg=--export-memory -C link-arg=--initial-memory=2097152 -C link-arg=--max-memory=134217728" \
cargo "+$toolchain" build -Z build-std=std,panic_abort "${build_args[@]}"
for artifact in "${artifacts[@]}"; do
    name="$(basename "$artifact" .wasm)"
    cp "$target_dir/browser-shared/wasm32-unknown-unknown/$profile/$name.wasm" "$output/$name.shared.wasm"
done
