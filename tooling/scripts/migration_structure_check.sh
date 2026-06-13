#!/bin/zsh
set -euo pipefail

target="${1:-.}"
migrations_dir="$target/migrations"

if [[ ! -d "$migrations_dir" ]]; then
  echo "[OK] no migrations directory present"
  exit 0
fi

typeset -a up_files
typeset -a down_files
up_files=("${(@f)$(cd "$migrations_dir" && ls -1 *.up.sql 2>/dev/null | sort)}")
down_files=("${(@f)$(cd "$migrations_dir" && ls -1 *.down.sql 2>/dev/null | sort)}")

if [[ ${#up_files[@]} -eq 0 && ${#down_files[@]} -eq 0 ]]; then
  echo "[FAIL] migrations directory exists but contains no paired migration files"
  exit 1
fi

failed=0
typeset -A widths
max_prefix=0

check_pair() {
  local file="$1"
  local counterpart="$2"
  local label="$3"

  if [[ ! -f "$migrations_dir/$counterpart" ]]; then
    echo "[FAIL] missing ${label} pair for $file"
    failed=1
  fi
}

for file in "${up_files[@]}"; do
  prefix="${file%%_*}"
  if ! printf '%s\n' "$prefix" | grep -Eq '^[0-9]+$'; then
    echo "[FAIL] migration prefix must be numeric: $file"
    failed=1
    continue
  fi

  widths[${#prefix}]=1
  if (( 10#$prefix > max_prefix )); then
    max_prefix=$((10#$prefix))
  fi
  check_pair "$file" "${file%.up.sql}.down.sql" "down"
done

for file in "${down_files[@]}"; do
  check_pair "$file" "${file%.down.sql}.up.sql" "up"
done

if [[ ${#widths[@]} -gt 1 ]]; then
  echo "[FAIL] mixed migration prefix widths detected; keep one zero-padding width per project"
  failed=1
else
  echo "[OK] consistent migration prefix width"
fi

first_up="${up_files[1]:-}"
if [[ -n "$first_up" ]]; then
  first_prefix="${first_up%%_*}"
  if (( 10#$first_prefix != 1 )); then
    echo "[FAIL] first migration prefix must start at 1: $first_up"
    failed=1
  else
    echo "[OK] first migration starts at 1"
  fi
  if [[ "$first_up" != *"_init.up.sql" && "$first_up" != *"_schema.up.sql" ]]; then
    echo "[FAIL] first migration should be an init/schema migration: $first_up"
    failed=1
  else
    echo "[OK] first migration is init/schema"
  fi
fi

for ((i=2; i<=${#up_files[@]}; i++)); do
  previous_prefix="${up_files[$((i-1))]%%_*}"
  current_prefix="${up_files[$i]%%_*}"
  if (( 10#$current_prefix <= 10#$previous_prefix )); then
    echo "[FAIL] migration prefixes must be strictly increasing: ${up_files[$((i-1))]} -> ${up_files[$i]}"
    failed=1
  fi
  if (( 10#$current_prefix != 10#$previous_prefix + 1 )); then
    echo "[FAIL] migration prefixes must not have gaps: ${up_files[$((i-1))]} -> ${up_files[$i]}"
    failed=1
  fi
done

if (( max_prefix > 3 )); then
  phase2_adr=""
  for decisions_dir in "$target/docs/decisions" "$target/docs/adr" "$target/docs/architecture/decisions"; do
    [[ -d "$decisions_dir" ]] || continue
    phase2_adr="$(rg -il "Phase 2|schema freeze|expand/contract|migration stream" "$decisions_dir" --glob '*.md' 2>/dev/null | head -1 || true)"
    [[ -n "$phase2_adr" ]] && break
  done

  if [[ -z "$phase2_adr" ]]; then
    echo "[FAIL] migrations 0004+ require a Phase 2 migration ADR"
    echo "  add docs/decisions/<date>-migration-phase-2.md with schema freeze, expand/contract, rollback, and backup policy"
    failed=1
  else
    echo "[OK] Phase 2 migration ADR present: ${phase2_adr#$target/}"
  fi

  migration_log="$target/docs/operations/migration_log.md"
  if [[ ! -f "$migration_log" ]]; then
    echo "[FAIL] Phase 2 migrations require docs/operations/migration_log.md"
    failed=1
  else
    echo "[OK] Phase 2 migration log present"
  fi
fi

if [[ "$failed" -ne 0 ]]; then
  echo "migration structure check failed"
  exit 1
fi

echo "migration structure check passed"
