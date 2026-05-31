#!/usr/bin/env zsh
set -euo pipefail

target="${1:-.}"
target="$(cd "$target" && pwd)"

if ! command -v npm >/dev/null 2>&1; then
  echo "[FAIL] missing npm for TypeScript analysis" >&2
  exit 1
fi

packages=(
  "runtime-transport/ts"
  "runtime-sdk/ts/browser-host"
  "runtime-native/ts"
  "frontend-kit/ts"
  "ui-minimal/ts"
  "config-contracts/ts"
)

checked=0
for pkg in "${packages[@]}"; do
  pkg_dir="$target/$pkg"
  if [[ ! -f "$pkg_dir/package.json" ]]; then
    continue
  fi
  if [[ ! -d "$pkg_dir/node_modules" ]]; then
    echo "[SKIP] TypeScript typecheck: $pkg (node_modules missing)"
    continue
  fi

  echo "[RUN] TypeScript typecheck: $pkg"
  npm --prefix "$pkg_dir" run typecheck
  echo "[OK] TypeScript typecheck: $pkg"
  checked=1
done

if (( checked == 0 )); then
  echo "[OK] no installed Foundation TypeScript packages found"
  exit 0
fi

echo "TypeScript static analysis check passed"
