#!/usr/bin/env bash
set -euo pipefail

# Audit application and Foundation lockfiles before compilation.
target="$(cd "${1:-.}" && pwd)"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$script_dir/command_timeout.sh"

candidates=(
  "$target" "$target/rust" "$target/native/src-tauri"
  "$target/runtime-sdk/rust" "$target/runtime-native/rust"
  "$target/foundation/runtime-sdk/rust" "$target/foundation/runtime-native/rust"
)
checked=0
for directory in "${candidates[@]}"; do
  [[ -f "$directory/Cargo.toml" ]] || continue
  if [[ ! -f "$directory/Cargo.lock" ]]; then
    echo "[FAIL] missing Cargo.lock: $directory" >&2
    echo 'Generate and review the lockfile before the security audit.' >&2
    exit 1
  fi
  if ! command -v cargo-audit >/dev/null 2>&1; then
    echo '[FAIL] cargo-audit is required; install the version pinned in Core CI.' >&2
    exit 1
  fi
  echo "[RUN] Rust dependency audit: ${directory#$target/}/Cargo.lock"
  run_with_timeout "${RUST_AUDIT_TIMEOUT_SEC:-120}" cargo audit --file "$directory/Cargo.lock"
  checked=$((checked + 1))
done

echo "[OK] Rust dependency audit completed: $checked lockfiles"
