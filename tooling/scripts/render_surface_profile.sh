#!/bin/zsh
# Capture the render-surface lane's allocation, prior cost, and ladder latency.
#
# Modelled on frontend_workbench_profile.sh: same PROFILE line format, same
# benchmark-results/ artifacts, same skip-when-uninstalled behaviour. The one
# difference that matters is that this profile measures bytes per call rather
# than megabytes per batch, so it is useless without a real collection — see
# the NODE_OPTIONS note in run_vitest.sh.
set -euo pipefail

target="${1:-.}"
target="$(cd "$target" && pwd)"
out_dir="${RENDER_SURFACE_PROFILE_DIR:-$target/benchmark-results}"
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
log_file="$out_dir/render_surface_profile_${timestamp}.log"
summary_file="$out_dir/render_surface_profile_${timestamp}.tsv"

first_existing_dir() {
  local candidate
  for candidate in "$@"; do
    if [[ -d "$candidate" ]]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

first_existing_file() {
  local candidate
  for candidate in "$@"; do
    if [[ -f "$candidate" ]]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

browser_host="$(first_existing_dir "$target/runtime-sdk/ts/browser-host" "$target/foundation/runtime-sdk/ts/browser-host" || true)"
run_vitest="$(first_existing_file "$target/tooling/scripts/run_vitest.sh" "$target/foundation/tooling/scripts/run_vitest.sh" || true)"

if [[ -z "${browser_host:-}" || -z "${run_vitest:-}" ]]; then
  echo "skip render surface profile: browser-host or Vitest runner not found"
  exit 0
fi

if [[ ! -d "$browser_host/node_modules" ]]; then
  message="skip render surface profile: node_modules not installed in ${browser_host#$target/}"
  if [[ "${RENDER_SURFACE_PROFILE_REQUIRED:-0}" == "1" || "${CI:-}" == "true" ]]; then
    echo "$message" >&2
    exit 1
  fi
  echo "$message"
  exit 0
fi

mkdir -p "$out_dir"
echo "render surface profile log: ${log_file#$target/}"
echo "render surface profile summary: ${summary_file#$target/}"

set +e
FOUNDATION_VITEST_EXPOSE_GC=1 "$run_vitest" "$browser_host" run src/renderSurface.profile.test.ts --reporter=verbose 2>&1 | tee "$log_file"
profile_status=${pipestatus[1]}
set -e

{
  echo "# metric	value	unit	source"
  awk -F '\t' '/^PROFILE\t/ {
    printf "%s\t%s\t%s\trender-surface\n", $2, $3, $4
  }' "$log_file"
} >"$summary_file"

# A profile that could not collect is a profile that measured nothing. Say so
# loudly rather than shipping a TSV of zeroes into benchmark-results/.
if grep -q 'no-gc' "$summary_file"; then
  echo "render surface profile ran without --expose-gc; allocation figures are unmeasured" >&2
  exit 1
fi

if [[ "$profile_status" -ne 0 ]]; then
  echo "render surface profile failed; partial log retained"
  exit "$profile_status"
fi

echo "render surface profile captured"
