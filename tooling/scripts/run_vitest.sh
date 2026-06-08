#!/bin/zsh
set -euo pipefail

if [[ "$#" -lt 2 ]]; then
  echo "usage: run_vitest.sh <package-dir> <vitest-args...>" >&2
  exit 2
fi

package_dir="$1"
shift
package_dir="$(cd "$package_dir" && pwd)"

is_codex_bundled_node() {
  local node_path="$1"
  [[ "$node_path" == /Applications/Codex.app/Contents/Resources/node ]]
}

find_vitest_node() {
  local explicit="${TEST_NODE:-${BENCH_NODE:-${NODE_BIN:-}}}"
  if [[ -n "$explicit" ]]; then
    if [[ -x "$explicit" ]]; then
      printf '%s\n' "$explicit"
      return 0
    fi
    echo "configured Node is not executable: $explicit" >&2
    return 1
  fi

  local candidates=()
  if command -v node >/dev/null 2>&1; then
    candidates+=("$(command -v node)")
  fi
  candidates+=(
    /opt/homebrew/bin/node
    /opt/homebrew/opt/node/bin/node
    /opt/homebrew/opt/node@24/bin/node
    /opt/homebrew/opt/node@22/bin/node
    /opt/homebrew/opt/node@20/bin/node
    /usr/local/bin/node
  )

  local candidate
  for candidate in "${candidates[@]}"; do
    if [[ -x "$candidate" ]] && ! is_codex_bundled_node "$candidate"; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done

  return 1
}

if [[ ! -d "$package_dir/node_modules" ]]; then
  echo "Skipping ${package_dir}: node_modules not installed"
  exit 0
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
FOUNDATION_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
vitest_cli="$package_dir/node_modules/vitest/dist/cli.js"
if [[ ! -f "$vitest_cli" ]]; then
  shared_vitest="$FOUNDATION_DIR/runtime-sdk/ts/browser-host/node_modules/vitest/dist/cli.js"
  if [[ -f "$shared_vitest" ]]; then
    vitest_cli="$shared_vitest"
  else
    echo "Skipping ${package_dir}: vitest is not installed"
    exit 0
  fi
fi

if ! node_bin="$(find_vitest_node)"; then
  message="Skipping ${package_dir}: native Vitest/Rolldown bindings cannot run under Codex's bundled hardened Node; install a developer Node or set TEST_NODE/BENCH_NODE"
  if [[ "${CI:-}" == "1" ]]; then
    echo "$message" >&2
    exit 1
  fi
  echo "$message"
  exit 0
fi

(
  cd "$package_dir"
  args=("$@")
  if [[ "${FOUNDATION_VITEST_SERIAL:-1}" == "1" ]]; then
    has_workers=0
    has_file_parallelism=0
    for arg in "${args[@]}"; do
      case "$arg" in
      --maxWorkers|--maxWorkers=*|--poolOptions.*.maxThreads=*)
        has_workers=1
        ;;
      --fileParallelism|--fileParallelism=*|--no-file-parallelism)
        has_file_parallelism=1
        ;;
      esac
    done
    worker_count="${FOUNDATION_VITEST_WORKERS:-0}"
    if [[ "$has_workers" -eq 0 && "$worker_count" == <-> && "$worker_count" -gt 0 ]]; then
      args+=("--maxWorkers=${worker_count}")
    fi
    if [[ "$has_file_parallelism" -eq 0 ]]; then
      args+=("--no-file-parallelism")
    fi
  fi
  printf '[RUN] vitest %s %s\n' "$package_dir" "${args[*]}"
  node_args=()
  if [[ "${FOUNDATION_VITEST_EXPOSE_GC:-0}" == "1" ]]; then
    node_args+=("--expose-gc")
  fi
  "$node_bin" "${node_args[@]}" "$vitest_cli" "${args[@]}"
  printf '[OK] vitest %s\n' "$package_dir"
)
