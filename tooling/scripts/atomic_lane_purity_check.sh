#!/bin/bash
set -euo pipefail

# CP-07.17 / CP-11A.3: AtomicLane transaction closures must stay pure DB work.
# Delegates to the stdlib-only Go AST analyzer next to this script so the check
# works identically in Foundation and in scaffolded apps (scripts/checks/).

target="${1:-.}"
# Resolve our own location via $0 — robust under bash and zsh (scaffolds invoke
# this as `zsh ./scripts/checks/atomic_lane_purity_check.sh`). We never source it.
script_dir="$(cd "$(dirname "$0")" && pwd)"
analyzer="$script_dir/atomic_lane_purity_check.go"

if ! command -v go >/dev/null 2>&1; then
  echo "[WARN] atomic lane purity check skipped: go toolchain unavailable" >&2
  exit 0
fi

if [[ ! -f "$analyzer" ]]; then
  echo "[FAIL] atomic lane purity analyzer missing: $analyzer" >&2
  exit 1
fi

exec go run "$analyzer" "$target"
