#!/bin/zsh
set -euo pipefail

# app_security_profile_check.sh
# Foundation owns the application security-profile contract, not concrete
# product postures. Generated applications own docs/security/profile.md.
#
# Usage: tooling/scripts/app_security_profile_check.sh [target-dir]

target="${1:-.}"
if [[ "$target" == "." && -d "./foundation" && ! -f "./go.mod" && ! -f "./package.json" && ! -f "./Cargo.toml" ]]; then
  target="./foundation"
fi
target="${target%/}"

failed=0
ok()   { echo "[OK] $1"; }
fail() { echo "[FAIL] $1"; shift; for d in "$@"; do [[ -n "$d" ]] && echo "  $d"; done; failed=1; }

is_foundation_core=0
if [[ -f "$target/templates/scaffold.manifest.tsv" && -d "$target/server-kit" && -d "$target/docs" && ! -f "$target/.foundation" ]]; then
  is_foundation_core=1
fi

required_headers=(
  "## Application Identity"
  "## Threat Surface Summary"
  "## Data Classification"
  "## Auth and Access Control"
  "## Incident Response"
  "## Review Checklist"
)

validate_profile() {
  local profile="$1"
  local kind="$2"
  local header
  local missing=()

  for header in "${required_headers[@]}"; do
    if ! grep -Fq "$header" "$profile"; then
      missing+=("$header")
    fi
  done

  if [[ ${#missing[@]} -gt 0 ]]; then
    fail "$kind security profile has required sections" \
      "missing: ${missing[*]}" \
      "file: ${profile#$target/}"
  else
    ok "$kind security profile has required sections"
  fi

  local reviewed_line
  reviewed_line="$(grep -E '^\\| Last reviewed' "$profile" 2>/dev/null | head -1 || true)"
  if [[ -z "$reviewed_line" ]]; then
    fail "$kind security profile has Last reviewed date" "file: ${profile#$target/}"
    return
  fi

  local reviewed_raw reviewed_date
  reviewed_raw="$(echo "$reviewed_line" | awk -F'|' '{print $3}' | sed -E 's/^ *//; s/ *$//')"
  if [[ "$kind" == "template" && "$reviewed_raw" == "{{TIMESTAMP}}" ]]; then
    ok "$kind security profile has renderable Last reviewed placeholder"
    return
  fi
  reviewed_date="$(printf '%s' "$reviewed_raw" | cut -c1-10)"
  if [[ "$reviewed_raw" == *"<!--"* || -z "$reviewed_raw" ]]; then
    fail "$kind security profile Last reviewed is unfilled" "file: ${profile#$target/}"
    return
  fi

  local days_ago
  days_ago="$(node - "$reviewed_date" <<'NODE'
const input = process.argv[2];
if (!/^\d{4}-\d{2}-\d{2}$/.test(input)) {
  process.exit(2);
}
const reviewed = Date.parse(`${input}T00:00:00Z`);
if (!Number.isFinite(reviewed)) {
  process.exit(2);
}
const days = Math.floor((Date.now() - reviewed) / 86400000);
process.stdout.write(String(days));
NODE
)" || {
    fail "$kind security profile Last reviewed date is invalid" \
      "expected YYYY-MM-DD or ISO timestamp, got: $reviewed_raw" \
      "file: ${profile#$target/}"
    return
  }

  if (( days_ago > 90 )); then
    fail "$kind security profile is stale" \
      "Last reviewed: $reviewed_raw ($days_ago days ago, max 90)" \
      "file: ${profile#$target/}"
  else
    ok "$kind security profile reviewed recently ($days_ago days ago)"
  fi
}

if [[ "$is_foundation_core" -eq 1 ]]; then
  profile="$target/templates/ops/security_profile.md"
  if [[ ! -f "$profile" ]]; then
    fail "app security profile template exists" "missing: templates/ops/security_profile.md"
  else
    ok "app security profile template exists"
    validate_profile "$profile" "template"
  fi
  if [[ -d "$target/docs/references/security" && -n "$(find "$target/docs/references/security" -type f -print -quit 2>/dev/null)" ]]; then
    fail "foundation avoids product security postures" \
      "remove files under docs/references/security; app-specific profiles belong in generated applications"
  else
    ok "foundation avoids product security postures"
  fi
else
  profile="$target/docs/security/profile.md"
  if [[ ! -f "$profile" ]]; then
    fail "app security profile exists" \
      "expected: docs/security/profile.md" \
      "create from the Foundation scaffold template and fill in the app-specific threat model"
  else
    ok "app security profile exists"
    validate_profile "$profile" "application"
  fi
fi

if [[ "$failed" -ne 0 ]]; then
  echo "app security profile check failed"
  exit 1
fi

echo "app security profile check passed"
