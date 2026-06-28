#!/bin/bash
set -euo pipefail

target="${1:-.}"
manifest="$target/tooling/enforcement_manifest.tsv"
mode="${2:-check}"
failed=0

if [[ "$mode" == "--write" ]]; then
  if [[ "${HUMAN_SUPERVISED_CHECK_UPDATE:-0}" != "1" ]]; then
    echo "[FAIL] refusing to rewrite enforcement manifest without HUMAN_SUPERVISED_CHECK_UPDATE=1"
    echo "  This is intentional: practice checks are part of the architecture contract."
    exit 1
  fi
fi

hash_file() {
  local file="$1"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
  else
    echo "missing sha256 tool" >&2
    exit 2
  fi
}

default_paths() {
  printf '%s\n' \
    Makefile \
    rustfmt.toml \
    scripts/check-rust.sh \
    scripts/lib/foundation.sh \
    scripts/lib/scaffold.sh \
    tooling/practice_controls.psv \
    tooling/foundation_ownership.tsv \
    templates/Makefile \
    templates/scaffold.manifest.tsv \
    tests/scaffold_manifest_test.sh \
    tests/init_project_test.sh \
    tests/update_project_test.sh \
    tests/migration_seed_policy_test.sh \
    tests/lifecycle_contract_generator_test.sh \
    tests/frontend_commands_generator_test.sh
  find "$target/tooling/scripts" -maxdepth 1 -type f | while IFS= read -r file; do
    printf '%s\n' "${file#$target/}"
  done
}

if [[ "$mode" == "--write" ]]; then
  tmp_manifest="${manifest}.tmp"
  {
    echo "# path	sha256"
    default_paths | sort -u | while IFS= read -r rel; do
      [[ -n "$rel" ]] || continue
      file="$target/$rel"
      [[ -f "$file" ]] || continue
      printf '%s\t%s\n' "$rel" "$(hash_file "$file")"
    done
  } >"$tmp_manifest"
  mv "$tmp_manifest" "$manifest"
  echo "enforcement manifest updated: ${manifest#$target/}"
  exit 0
fi

if [[ ! -f "$manifest" ]]; then
  echo "[FAIL] enforcement manifest missing: ${manifest#$target/}"
  echo "  Run with HUMAN_SUPERVISED_CHECK_UPDATE=1 and --write during a supervised Foundation contract update."
  exit 1
fi

while IFS=$'\t' read -r rel expected; do
  [[ -n "${rel:-}" ]] || continue
  [[ "$rel" == \#* ]] && continue
  file="$target/$rel"
  if [[ ! -f "$file" ]]; then
    echo "[FAIL] protected enforcement file missing"
    echo "  $rel"
    failed=1
    continue
  fi
  actual="$(hash_file "$file")"
  if [[ "$actual" != "$expected" ]]; then
    echo "[FAIL] protected enforcement file changed"
    echo "  $rel"
    echo "  expected: $expected"
    echo "  actual:   $actual"
    failed=1
  else
    echo "[OK] $rel"
  fi
done <"$manifest"

if [[ "$failed" -ne 0 ]]; then
  echo "enforcement integrity check failed"
  echo "Changes to Foundation checks/scaffold enforcement require human-supervised manifest refresh:"
  echo "  HUMAN_SUPERVISED_CHECK_UPDATE=1 tooling/scripts/enforcement_integrity_check.sh . --write"
  exit 1
fi

echo "enforcement integrity check passed"
