#!/usr/bin/env zsh
set -euo pipefail

target="${1:-.}"
target="$(cd "$target" && pwd)"

resolve_tooling_script() {
  local script="$1"
  local candidates=(
    "$target/tooling/scripts/$script"
    "$target/scripts/checks/$script"
    "$target/foundation/tooling/scripts/$script"
  )

  if [[ -n "${FOUNDATION_PATH:-}" ]]; then
    candidates+=("$FOUNDATION_PATH/tooling/scripts/$script")
  fi

  local candidate
  for candidate in "${candidates[@]}"; do
    if [[ -x "$candidate" ]]; then
      echo "$candidate"
      return 0
    fi
  done

  echo "[FAIL] unable to locate $script for $target" >&2
  return 1
}

discover_manifests() {
  local manifest
  local candidates=(
    "$target/Cargo.toml"
    "$target/rust/Cargo.toml"
    "$target/native/src-tauri/Cargo.toml"
    "$target/runtime-sdk/rust/Cargo.toml"
    "$target/runtime-native/rust/Cargo.toml"
    "$target/foundation/runtime-sdk/rust/Cargo.toml"
    "$target/foundation/runtime-native/rust/Cargo.toml"
  )
  local -A seen=()
  local manifests=()
  local dir canonical

  for manifest in "${candidates[@]}"; do
    [[ -f "$manifest" ]] || continue
    dir="$(cd "$(dirname "$manifest")" && pwd -P)"
    canonical="$dir/Cargo.toml"
    [[ -n "${seen[$canonical]:-}" ]] && continue
    seen[$canonical]=1
    manifests+=("$canonical")
  done

  printf '%s\n' "${manifests[@]}"
}

echo "[RUN] Rust static analysis"
static_script="$(resolve_tooling_script rust_static_analysis_check.sh)"
"$static_script" "$target"

echo "[RUN] Rust runtime practice checks"
runtime_script="$(resolve_tooling_script rust_runtime_practices_check.sh)"
"$runtime_script" "$target"

manifests=("${(@f)$(discover_manifests)}")
for manifest in "${manifests[@]}"; do
  [[ -n "$manifest" ]] || continue
  rel="${manifest#$target/}"
  echo "[RUN] Rust tests: $rel"
  cargo test --manifest-path "$manifest" --all-features
done

echo "Rust issue checks passed"
