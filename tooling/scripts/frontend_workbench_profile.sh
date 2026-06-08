#!/bin/zsh
set -euo pipefail

target="${1:-.}"
target="$(cd "$target" && pwd)"
out_dir="${FRONTEND_WORKBENCH_PROFILE_DIR:-$target/benchmark-results}"
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
log_file="$out_dir/frontend_workbench_profile_${timestamp}.log"
summary_file="$out_dir/frontend_workbench_profile_${timestamp}.tsv"

first_existing_dir() {
  local path
  for path in "$@"; do
    if [[ -d "$path" ]]; then
      printf '%s\n' "$path"
      return 0
    fi
  done
  return 1
}

first_existing_file() {
  local path
  for path in "$@"; do
    if [[ -f "$path" ]]; then
      printf '%s\n' "$path"
      return 0
    fi
  done
  return 1
}

frontend_kit="$(first_existing_dir "$target/frontend-kit/ts" "$target/foundation/frontend-kit/ts" || true)"
run_vitest="$(first_existing_file "$target/tooling/scripts/run_vitest.sh" "$target/scripts/checks/run_vitest.sh" "$target/foundation/tooling/scripts/run_vitest.sh" || true)"

if [[ -z "${frontend_kit:-}" || -z "${run_vitest:-}" ]]; then
  echo "skip frontend workbench profile: frontend-kit or Vitest runner not found"
  exit 0
fi

if [[ ! -d "$frontend_kit/node_modules" ]]; then
  message="skip frontend workbench profile: node_modules not installed in ${frontend_kit#$target/}"
  if [[ "${FRONTEND_WORKBENCH_PROFILE_REQUIRED:-0}" == "1" || "${CI:-}" == "true" ]]; then
    echo "$message" >&2
    exit 1
  fi
  echo "$message"
  exit 0
fi

mkdir -p "$out_dir"
echo "frontend workbench profile log: ${log_file#$target/}"
echo "frontend workbench profile summary: ${summary_file#$target/}"

set +e
FOUNDATION_VITEST_EXPOSE_GC=1 "$run_vitest" "$frontend_kit" run src/runtimeWorkbench.profile.test.ts --reporter=verbose 2>&1 | tee "$log_file"
profile_status=${pipestatus[1]}
set -e

{
  echo "# metric	value	unit	source"
  awk -F '\t' '/^PROFILE\t/ {
    printf "%s\t%s\t%s\tfrontend-kit\n", $2, $3, $4
  }' "$log_file"
} >"$summary_file"

if [[ "$profile_status" -ne 0 ]]; then
  echo "frontend workbench profile failed; partial log retained"
  exit "$profile_status"
fi

echo "frontend workbench profile captured"
