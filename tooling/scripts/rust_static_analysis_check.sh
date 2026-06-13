#!/usr/bin/env zsh
set -euo pipefail

target="${1:-.}"
target="$(cd "$target" && pwd)"

manifests=()
typeset -A seen_manifests=()

add_manifest() {
  local manifest="$1"
  [[ -f "$manifest" ]] || return 0

  local dir canonical
  dir="$(cd "$(dirname "$manifest")" && pwd -P)"
  canonical="$dir/Cargo.toml"
  [[ -n "${seen_manifests[$canonical]:-}" ]] && return 0

  seen_manifests[$canonical]=1
  manifests+=("$canonical")
}

manifest_candidates=(
  "$target/Cargo.toml"
  "$target/rust/Cargo.toml"
  "$target/native/src-tauri/Cargo.toml"
  "$target/runtime-sdk/rust/Cargo.toml"
  "$target/runtime-native/rust/Cargo.toml"
  "$target/foundation/runtime-sdk/rust/Cargo.toml"
  "$target/foundation/runtime-native/rust/Cargo.toml"
)

for manifest in "${manifest_candidates[@]}"; do
  add_manifest "$manifest"
done

if (( ${#manifests[@]} == 0 )); then
  echo "[OK] no Rust manifests found"
  exit 0
fi

required_tools=(cargo rustfmt)
missing_tools=()
for tool in "${required_tools[@]}"; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    missing_tools+=("$tool")
  fi
done

if (( ${#missing_tools[@]} > 0 )); then
  echo "[FAIL] missing required Rust analysis tools: ${missing_tools[*]}" >&2
  exit 1
fi

clippy_lints=(
  -D warnings
  -D unsafe-op-in-unsafe-fn
  -D clippy::undocumented_unsafe_blocks
  -D clippy::missing_safety_doc
)

temp_dist_dirs=()
temp_dist_files=()

cleanup_tauri_frontend_dist() {
  local file dir
  for file in "${temp_dist_files[@]}"; do
    [[ -f "$file" ]] && rm -f "$file"
  done
  for dir in "${temp_dist_dirs[@]}"; do
    [[ -d "$dir" ]] && rmdir "$dir" >/dev/null 2>&1 || true
  done
}
trap cleanup_tauri_frontend_dist EXIT

prepare_tauri_frontend_dist() {
  local manifest="$1"
  [[ "$manifest" == */native/src-tauri/Cargo.toml ]] || return 0

  local app_root dist index
  app_root="$(cd "$(dirname "$manifest")/../.." && pwd -P)"
  dist="$app_root/frontend/dist"
  index="$dist/index.html"

  if [[ ! -d "$dist" ]]; then
    mkdir -p "$dist"
    printf '<!doctype html><html><head><meta charset="utf-8"><title>Static Analysis</title></head><body></body></html>\n' > "$index"
    temp_dist_dirs+=("$dist")
    return 0
  fi

  if [[ ! -f "$index" ]]; then
    printf '<!doctype html><html><head><meta charset="utf-8"><title>Static Analysis</title></head><body></body></html>\n' > "$index"
    temp_dist_files+=("$index")
  fi
}

checked=0
for manifest in "${manifests[@]}"; do
  rel="${manifest#$target/}"
  prepare_tauri_frontend_dist "$manifest"

  echo "[RUN] Rust fmt: $rel"
  cargo fmt --manifest-path "$manifest" --all -- --check
  echo "[OK] Rust fmt: $rel"

  echo "[RUN] Rust clippy: $rel"
  cargo clippy --manifest-path "$manifest" --all-targets --all-features -- "${clippy_lints[@]}"
  echo "[OK] Rust clippy: $rel"
  checked=1
done

echo "Rust static analysis check passed"
