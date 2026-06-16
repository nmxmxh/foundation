#!/bin/zsh
# coverage_ratchet_check.sh — enforce the ≥95% coverage policy as a ratchet.
#
# Policy (docs/testing_practices.md, AGENTS.md):
#   * New/changed production code targets 95% statement coverage.
#   * Legacy packages must improve toward 95% when touched and CANNOT regress.
#
# This check turns that stated policy into an enforced invariant:
#   * Every package carries a recorded floor in tooling/coverage_baseline.psv.
#   * A package measuring below its floor (minus tolerance) FAILS — no regression.
#   * A NEW package (absent from the baseline) below TARGET FAILS — new code is
#     held to 95% unless a supervised exception records an explicit lower floor.
#   * Floors only ever rise: `UPDATE=1 HUMAN_SUPERVISED_CHECK_UPDATE=1 ... --write`
#     ratchets each floor up to max(old, current). It never lowers a floor.
#
# Statement coverage is what `go test -cover` reports; the policy is enforced in
# those terms. Reaching TARGET on legacy packages is reported as gap pressure,
# not a hard gate, so the repo converges without a flag-day.
#
# Baseline format: tooling/coverage_baseline.psv, "package|floor|target".
# A package whose target is the literal "service" is service-lane: it cannot be
# meaningfully unit-covered (its logic is live-server glue exercised by
# service-backed tests, TE-38). Such packages are floor-gated only — no target
# pressure — and UPDATE preserves the "service" marker rather than resetting it.
set -euo pipefail

target="${1:-.}"
target="$(cd "$target" && pwd)"
mode="${2:-check}"

baseline="$target/tooling/coverage_baseline.psv"
TARGET="${COVERAGE_TARGET:-95.0}"
TOLERANCE="${COVERAGE_TOLERANCE:-0.5}"
GOCACHE="${FOUNDATION_GO_CACHE_DIR:-/tmp/ovasabi-foundation-go-build}"
export GOCACHE
mkdir -p "$GOCACHE"

failed=0
ok()   { echo "[OK] $1"; }
warn() { echo "[WARN] $1"; }
fail() { echo "[FAIL] $1"; shift; local d; for d in "$@"; do [[ -n "$d" ]] && echo "  $d"; done; failed=1; }

# Measure statement coverage for every package across all Go module roots.
# Emits "package<TAB>pct" lines; skips packages with no test files / no statements.
measure() {
  local root mod
  for root in server-kit/go runtime-transport/go runtime-sdk/go config-contracts/go; do
    [[ -f "$target/$root/go.mod" ]] || continue
    mod="$(awk 'NR==1{print $2}' "$target/$root/go.mod")"
    ( cd "$target/$root" && go test -cover ./... 2>/dev/null ) | awk -v mod="$mod" '
      /\[no test files\]/ { next }
      /\[no statements\]/ { next }
      {
        pct=""; pkg=""
        for (i=1;i<=NF;i++) {
          if ($i=="coverage:") { p=$(i+1); gsub(/%/,"",p); if (p ~ /^[0-9]+(\.[0-9]+)?$/) pct=p }
          if ($i ~ ("^" mod)) { pkg=$i }
        }
        # Generated code (protobuf, etc.) is not hand-authored; do not gate it.
        if (pkg ~ /\/generated\//) next
        if (pkg!="" && pct!="") printf "%s\t%s\n", pkg, pct
      }'
  done
}

typeset -A current
while IFS=$'\t' read -r pkg pct; do
  [[ -n "$pkg" ]] && current[$pkg]="$pct"
done < <(measure)

if [[ "${#current[@]}" -eq 0 ]]; then
  fail "coverage measurement produced no packages" "is the Go toolchain available?"
  echo "coverage ratchet check failed"
  exit 1
fi

# --write / UPDATE: regenerate the baseline, ratcheting each floor UP only.
if [[ "$mode" == "--write" || "${UPDATE:-0}" == "1" ]]; then
  if [[ "${HUMAN_SUPERVISED_CHECK_UPDATE:-0}" != "1" ]]; then
    echo "[FAIL] refusing to rewrite coverage baseline without HUMAN_SUPERVISED_CHECK_UPDATE=1"
    echo "  The baseline is part of the testing contract; floors only rise under supervision."
    exit 1
  fi
  typeset -A old_floor
  typeset -A old_target
  if [[ -f "$baseline" ]]; then
    while IFS='|' read -r pkg floor tgt; do
      [[ -z "$pkg" || "$pkg" == \#* ]] && continue
      old_floor[$pkg]="$floor"
      old_target[$pkg]="$tgt"
    done < "$baseline"
  fi
  tmp="$baseline.tmp"
  {
    echo "# package|floor|target"
    for pkg in ${(ok)current}; do
      cur="${current[$pkg]}"
      prev="${old_floor[$pkg]:-0}"
      newfloor="$(awk -v a="$prev" -v b="$cur" 'BEGIN{printf "%.1f", (a>b?a:b)}')"
      # Preserve a manually recorded target (e.g. "service" for service-lane
      # packages); only new packages default to the numeric TARGET.
      echo "$pkg|$newfloor|${old_target[$pkg]:-$TARGET}"
    done
  } > "$tmp"
  mv "$tmp" "$baseline"
  echo "coverage baseline updated: ${baseline#$target/} (${#current[@]} packages)"
  exit 0
fi

if [[ ! -f "$baseline" ]]; then
  fail "coverage baseline missing" "${baseline#$target/}" \
       "Generate under supervision: HUMAN_SUPERVISED_CHECK_UPDATE=1 UPDATE=1 tooling/scripts/coverage_ratchet_check.sh ."
  echo "coverage ratchet check failed"
  exit 1
fi

# Verify every recorded package against its floor.
typeset -A seen
below_target=0
total_targeted=0
service_count=0
while IFS='|' read -r pkg floor tgt; do
  [[ -z "$pkg" || "$pkg" == \#* ]] && continue
  seen[$pkg]=1
  short="${pkg##*/server-kit/go/}"; short="${short##*ovasabi_foundation/}"
  cur="${current[$pkg]:-}"
  if [[ -z "$cur" ]]; then
    warn "package in baseline not measured: $short (deleted or no tests?)"
    continue
  fi
  # Regression gate applies to every package, including service-lane ones:
  # current must not drop below the recorded floor (minus tolerance).
  if awk -v c="$cur" -v f="$floor" -v t="$TOLERANCE" 'BEGIN{exit !(c < f - t)}'; then
    fail "coverage regressed: $short" "floor ${floor}%  measured ${cur}%  (tolerance ${TOLERANCE}%)"
    continue
  fi
  # Service-lane packages (redis, database, ...) cannot reach the unit target in
  # `go test -cover`; their real coverage lives in service-backed tests (TE-38).
  # They are floor-gated only, with no "to go" target pressure.
  if [[ "$tgt" == "service" ]]; then
    service_count=$((service_count + 1))
    ok "$short ${cur}% [service-lane] floor ${floor}% (unit lane; full coverage via service-backed tests)"
    continue
  fi
  total_targeted=$((total_targeted + 1))
  # Pressure report: still short of target.
  if awk -v c="$cur" -v t="$tgt" 'BEGIN{exit !(c < t)}'; then
    below_target=$((below_target + 1))
    ok "$short ${cur}% (floor ${floor}%, ${tgt}% target — $(awk -v c="$cur" -v t="$tgt" 'BEGIN{printf "%.1f", t-c}')% to go)"
  else
    ok "$short ${cur}% ✓ at target"
  fi
done < "$baseline"

# New packages (with tests) absent from the baseline must meet TARGET.
for pkg in ${(ok)current}; do
  [[ -n "${seen[$pkg]:-}" ]] && continue
  short="${pkg##*/server-kit/go/}"; short="${short##*ovasabi_foundation/}"
  cur="${current[$pkg]}"
  if awk -v c="$cur" -v t="$TARGET" 'BEGIN{exit !(c < t)}'; then
    fail "new package below ${TARGET}% target: $short" "measured ${cur}%" \
         "Add tests, or record a supervised floor via UPDATE=1 if this is an approved exception."
  else
    ok "$short ${cur}% ✓ new package meets target"
  fi
done

at_target=$((total_targeted - below_target))
echo "coverage: ${at_target}/${total_targeted} targeted packages at ${TARGET}% target (${service_count} service-lane, floor-gated)"

if [[ "$failed" -ne 0 ]]; then
  echo "coverage ratchet check failed"
  exit 1
fi
echo "coverage ratchet check passed"
