#!/bin/zsh
set -euo pipefail

target="${1:-.}"
target="$(cd "$target" && pwd)"

is_truthy() {
  case "${1:-}" in
    1|true|TRUE|yes|YES|on|ON) return 0 ;;
    *) return 1 ;;
  esac
}

strict_dependency_check() {
  is_truthy "${CI:-}" || is_truthy "${FOUNDATION_REQUIRE_TS_DEPS:-}"
}

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
  if ! rg -q '"sideEffects"[[:space:]]*:[[:space:]]*false' "$pkg_dir/package.json"; then
    echo "[FAIL] Foundation TypeScript package must declare sideEffects=false: $pkg/package.json" >&2
    exit 1
  fi
  echo "[OK] side-effect-free package metadata: $pkg"
  if [[ ! -f "$pkg_dir/package-lock.json" ]]; then
    echo "[FAIL] Foundation TypeScript package lockfile missing: $pkg/package-lock.json" >&2
    exit 1
  fi
  echo "[OK] reproducible package lock: $pkg"
  if [[ ! -d "$pkg_dir/node_modules" ]]; then
    if strict_dependency_check; then
      echo "[FAIL] TypeScript dependencies missing in strict mode: $pkg/node_modules" >&2
      echo "  Run: make install-ts-deps" >&2
      exit 1
    fi
    echo "[SKIP] TypeScript typecheck: $pkg (node_modules missing)"
    continue
  fi

  echo "[RUN] TypeScript typecheck: $pkg"
  npm --prefix "$pkg_dir" run typecheck
  echo "[OK] TypeScript typecheck: $pkg"
  checked=1
done

class_component_matches="$(rg -n 'extends[[:space:]]+(React\.)?(PureComponent|Component)' \
  "$target/frontend-kit/ts/src" "$target/ui-minimal/ts/src" \
  "$target/runtime-transport/ts/src" "$target/runtime-sdk/ts/browser-host/src" \
  "$target/config-contracts/ts/src" "$target/runtime-native/ts/src" \
  --glob '*.{ts,tsx}' --glob '!**/*.test.*' --glob '!**/*.bench.*' --glob '!**/generated/**' 2>/dev/null || true)"
if [[ -n "$class_component_matches" ]]; then
  echo "[FAIL] Foundation UI/runtime packages use functional composition; React class components require an explicit exception" >&2
  echo "$class_component_matches" >&2
  exit 1
fi
echo "[OK] no production React class components"

practice_checked=0
for native_index in "$target/runtime-native/ts/src/index.ts" "$target/foundation/runtime-native/ts/src/index.ts"; do
  if [[ ! -f "$native_index" ]]; then
    continue
  fi
  practice_checked=1
  echo "[RUN] TypeScript runtime-native frame bounds: ${native_index#$target/}"
  if ! rg -q "MAX_NATIVE_FRAME_BYTES" "$native_index"; then
    echo "[FAIL] runtime-native TS codec must define MAX_NATIVE_FRAME_BYTES in parity with Rust" >&2
    exit 1
  fi
  if ! rg -q "payloadLength > remainingBytes" "$native_index"; then
    echo "[FAIL] runtime-native TS response decoder must reject declared length overflow before slicing" >&2
    exit 1
  fi
  if ! rg -q "assertU32Length" "$native_index"; then
    echo "[FAIL] runtime-native TS request encoder must validate u32 payload and metadata lengths" >&2
    exit 1
  fi
  echo "[OK] TypeScript runtime-native frame bounds: ${native_index#$target/}"
done

if (( practice_checked == 0 )); then
  echo "[OK] no runtime-native TypeScript frame codec found"
fi

if (( checked == 0 )); then
  echo "[OK] no installed Foundation TypeScript packages found (local fallback mode)"
  exit 0
fi

echo "TypeScript static analysis check passed"
