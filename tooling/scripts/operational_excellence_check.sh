#!/bin/zsh
set -euo pipefail

target="${1:-.}"
target="$(cd "$target" && pwd)"
failed=0

ok() {
  echo "[OK] $1"
}

fail() {
  echo "[FAIL] $1"
  shift
  local line
  for line in "$@"; do
    [[ -n "$line" ]] && echo "  $line"
  done
  failed=1
}

first_existing() {
  local path
  for path in "$@"; do
    if [[ -e "$path" ]]; then
      printf '%s\n' "$path"
      return 0
    fi
  done
  return 1
}

check_exists() {
  local label="$1"
  local path="$2"
  if [[ -e "$path" ]]; then
    ok "$label"
  else
    fail "$label" "missing: ${path#$target/}"
  fi
}

check_contains() {
  local label="$1"
  local file="$2"
  local pattern="$3"
  if [[ -f "$file" ]] && grep -Fq -- "$pattern" "$file"; then
    ok "$label"
  else
    fail "$label" "missing pattern: $pattern" "file: ${file#$target/}"
  fi
}

docs_dir="$(first_existing "$target/docs/foundation" "$target/docs" || true)"
if [[ -z "${docs_dir:-}" ]]; then
  fail "operations docs directory exists" "expected docs/ or docs/foundation/"
else
  delivery_doc="$docs_dir/delivery_metrics_practices.md"
  check_exists "delivery metrics practices doc" "$delivery_doc"
  check_contains "delivery doc names DORA" "$delivery_doc" "DORA"
  check_contains "delivery doc names SPACE" "$delivery_doc" "SPACE"
  check_contains "delivery doc names OpenTelemetry semantic conventions" "$delivery_doc" "OpenTelemetry semantic conventions"
  check_contains "delivery doc names SLSA" "$delivery_doc" "SLSA"
  check_contains "delivery doc names SBOM" "$delivery_doc" "SBOM"
  check_contains "delivery doc names provenance" "$delivery_doc" "provenance"
fi

metrics_script="$(first_existing \
  "$target/tooling/scripts/ci_delivery_metrics.mjs" \
  "$target/scripts/checks/ci_delivery_metrics.mjs" \
  "$target/foundation/tooling/scripts/ci_delivery_metrics.mjs" || true)"
if [[ -z "${metrics_script:-}" ]]; then
  fail "delivery metrics collector exists" "expected ci_delivery_metrics.mjs"
else
  check_contains "metrics collector emits DORA block" "$metrics_script" "dora:"
  check_contains "metrics collector emits SPACE block" "$metrics_script" "space:"
  check_contains "metrics collector emits observability block" "$metrics_script" "observability:"
  check_contains "metrics collector emits supply-chain block" "$metrics_script" "supply_chain:"
  check_contains "metrics collector records OTel semconv version" "$metrics_script" "otel_semconv_version"
  check_contains "metrics collector records SBOM path" "$metrics_script" "sbom_path"
  check_contains "metrics collector records SLSA provenance path" "$metrics_script" "slsa_provenance_path"
fi

security_workflow="$(first_existing \
  "$target/templates/github/workflows/security.yml" \
  "$target/.github/workflows/security.yml" || true)"
if [[ -z "${security_workflow:-}" ]]; then
  fail "security workflow exists" "expected templates/github/workflows/security.yml or .github/workflows/security.yml"
else
  check_contains "security workflow generates SBOM" "$security_workflow" "anchore/sbom-action"
  check_contains "security workflow emits SPDX JSON" "$security_workflow" "spdx-json"
fi

ci_workflow="$(first_existing \
  "$target/templates/github/workflows/ci.yml" \
  "$target/.github/workflows/ci.yml" || true)"
if [[ -z "${ci_workflow:-}" ]]; then
  fail "CI workflow exists" "expected templates/github/workflows/ci.yml or .github/workflows/ci.yml"
else
  check_contains "CI captures delivery metrics" "$ci_workflow" "ci_delivery_metrics.mjs"
  check_contains "CI uploads delivery metrics artifact" "$ci_workflow" "delivery-metrics/ci-event.json"
fi

if [[ "$failed" -ne 0 ]]; then
  echo "operational excellence check failed"
  exit 1
fi

echo "operational excellence check passed"
