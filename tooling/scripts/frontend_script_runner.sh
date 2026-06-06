#!/bin/zsh
set -euo pipefail

target="${1:-.}"
script_name="${2:-test}"
target="$(cd "$target" && pwd)"
script_dir="$(cd "$(dirname "$0")" && pwd)"

if [[ -f "$script_dir/local_toolchain_env.sh" ]]; then
  source "$script_dir/local_toolchain_env.sh"
  ovasabi_toolchain_init "$target"
fi

frontend_root="$target/frontend"
if [[ ! -f "$frontend_root/package.json" && -f "$target/package.json" ]]; then
  frontend_root="$target"
fi

if [[ ! -f "$frontend_root/package.json" ]]; then
  echo "No frontend package; skipping frontend $script_name"
  exit 0
fi

package_json="$frontend_root/package.json"
package_manager="npm"
if grep -Eq '"packageManager"[[:space:]]*:[[:space:]]*"pnpm@' "$package_json" || [[ -f "$frontend_root/pnpm-lock.yaml" ]]; then
  package_manager="pnpm"
elif grep -Eq '"packageManager"[[:space:]]*:[[:space:]]*"yarn@' "$package_json" || [[ -f "$frontend_root/yarn.lock" ]]; then
  package_manager="yarn"
elif grep -Eq '"packageManager"[[:space:]]*:[[:space:]]*"npm@' "$package_json" || [[ -f "$frontend_root/package-lock.json" ]]; then
  package_manager="npm"
fi

if ! grep -Eq "\"$script_name\"[[:space:]]*:" "$package_json"; then
  echo "Frontend script '$script_name' not declared; skipping"
  exit 0
fi

if ! command -v "$package_manager" >/dev/null 2>&1; then
  echo "[FAIL] missing frontend package manager: $package_manager" >&2
  echo "  project: $frontend_root" >&2
  echo "  install a normal local Node.js toolchain, then rerun: make test-frontend" >&2
  echo "  Codex's bundled node is intentionally not treated as a full frontend toolchain." >&2
  exit 1
fi

node_bin="$(command -v node 2>/dev/null || true)"
if [[ "$node_bin" == /Applications/Codex.app/* ]] && grep -Eq '"(vite|vitest|@vitejs/|rollup)"' "$package_json"; then
  echo "[FAIL] frontend $script_name is using Codex's bundled Node runtime" >&2
  echo "  node: $node_bin" >&2
  echo "  Vite/Rollup loads native optional packages on macOS; Codex's signed Node process can reject those dylibs." >&2
  echo "  Use a normal local Node.js installation from Homebrew, Volta, fnm, nvm, mise, or asdf." >&2
  echo "  The Foundation runner will prefer that local Node automatically when it is on disk." >&2
  exit 1
fi

echo "[RUN] frontend $script_name: ${frontend_root#$target/}"
if [[ -f "$script_dir/run_vitest.sh" ]] && grep -Eq "\"$script_name\"[[:space:]]*:[[:space:]]*\"[^\"]*vitest" "$package_json"; then
  case "$script_name" in
  test)
    zsh "$script_dir/run_vitest.sh" "$frontend_root" run
    exit $?
    ;;
  test:coverage)
    zsh "$script_dir/run_vitest.sh" "$frontend_root" run --coverage
    exit $?
    ;;
  esac
fi
cd "$frontend_root"
"$package_manager" run "$script_name"
