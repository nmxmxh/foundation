#!/usr/bin/env zsh
set -euo pipefail

target="${1:-.}"
target="$(cd "$target" && pwd)"

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

manifests=(
  "$target/runtime-sdk/rust/Cargo.toml"
  "$target/runtime-native/rust/Cargo.toml"
)

checked=0
for manifest in "${manifests[@]}"; do
  if [[ ! -f "$manifest" ]]; then
    continue
  fi

  rel="${manifest#$target/}"
  echo "[RUN] Rust fmt: $rel"
  cargo fmt --manifest-path "$manifest" --all -- --check
  echo "[OK] Rust fmt: $rel"

  echo "[RUN] Rust clippy: $rel"
  cargo clippy --manifest-path "$manifest" --all-targets --all-features -- -D warnings
  echo "[OK] Rust clippy: $rel"
  checked=1
done

if (( checked == 0 )); then
  echo "[OK] no Foundation Rust manifests found"
  exit 0
fi

echo "Rust static analysis check passed"
