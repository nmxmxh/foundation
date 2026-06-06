#!/bin/zsh
set -euo pipefail

target="${1:-.}"
failed=0

if [[ "$target" == "." && -d "./foundation" && ! -f "./go.mod" && ! -f "./package.json" && ! -f "./Cargo.toml" ]]; then
  target="./foundation"
fi
target="${target%/}"

docs_dir="$target/docs"
if [[ -d "$target/docs/foundation" ]]; then
  docs_dir="$target/docs/foundation"
elif [[ ! -d "$docs_dir" && -d "$target/foundation/docs" ]]; then
  docs_dir="$target/foundation/docs"
fi

matrix="$target/tooling/practice_controls.psv"
if [[ ! -f "$matrix" && -f "$target/foundation/tooling/practice_controls.psv" ]]; then
  matrix="$target/foundation/tooling/practice_controls.psv"
fi

typeset -A matrix_ids
typeset -A expected_ids
typeset -A seen_ids

ok() {
  echo "[OK] $1"
}

fail() {
  echo "[FAIL] $1"
  shift
  local detail
  for detail in "$@"; do
    [[ -n "$detail" ]] && echo "  $detail"
  done
  failed=1
}

relative_path() {
  local path="$1"
  path="${path#$target/}"
  path="${path#./}"
  echo "$path"
}

doc_exists() {
  local owner_doc="$1"
  local base
  base="$(basename "$owner_doc")"
  [[ -f "$target/$owner_doc" ]] && return 0
  [[ -f "$target/foundation/$owner_doc" ]] && return 0
  [[ -f "$docs_dir/$base" ]] && return 0
  return 1
}

script_exists() {
  local script_ref="$1"
  local base
  base="$(basename "$script_ref")"
  [[ -f "$target/$script_ref" ]] && return 0
  [[ -f "$target/foundation/$script_ref" ]] && return 0
  [[ -f "$target/scripts/checks/$base" ]] && return 0
  return 1
}

add_expected_from_doc() {
  local doc="$1"
  local prefix="$2"
  [[ -f "$doc" ]] || {
    fail "source doc for $prefix controls" "missing: $(relative_path "$doc")"
    return
  }

  local id
  while IFS= read -r id; do
    [[ -n "$id" ]] || continue
    expected_ids["${id:u}"]=1
  done < <(rg -o "^### ${prefix}-[0-9A-Za-z]+" "$doc" 2>/dev/null | awk '{print $2}')
}

if [[ ! -f "$matrix" ]]; then
  fail "practice controls matrix" "missing: tooling/practice_controls.psv"
else
  ok "practice controls matrix exists"
fi

if [[ -f "$matrix" ]]; then
  header="$(sed -n '1p' "$matrix")"
  expected_header="# control_id|owner_doc|category|risk|automation|enforcement|evidence|merge_gate"
  if [[ "$header" == "$expected_header" ]]; then
    ok "practice controls matrix header"
  else
    fail "practice controls matrix header" "expected: $expected_header" "actual: $header"
  fi

  line_no=0
  while IFS='|' read -r control_id owner_doc category risk automation enforcement evidence merge_gate rest; do
    line_no=$((line_no + 1))
    [[ "$line_no" -eq 1 ]] && continue
    [[ -z "${control_id:-}" || "$control_id" == \#* ]] && continue

    control_id="${control_id:u}"
    if [[ -n "${seen_ids[$control_id]:-}" ]]; then
      fail "duplicate control id" "$control_id at line $line_no"
      continue
    fi
    seen_ids["$control_id"]=1
    matrix_ids["$control_id"]=1

    if [[ -n "${rest:-}" ]]; then
      fail "extra matrix columns" "$control_id line $line_no"
    fi
    if ! printf '%s\n' "$control_id" | grep -Eq '^(CP|TE|AOC|EVID|FPR|AISEC|PERFLAB|RUNTIME|FORMAL|OPS|PROJFRESH|CTRL)-[0-9A-Z]+$'; then
      fail "control id format" "$control_id line $line_no"
    fi
    if [[ -z "${owner_doc:-}" ]] || ! doc_exists "$owner_doc"; then
      fail "control owner doc exists" "$control_id -> ${owner_doc:-<empty>}"
    fi
    case "${risk:-}" in
      low|medium|high|critical) ;;
      *) fail "control risk class" "$control_id risk=${risk:-<empty>}" ;;
    esac
    case "${automation:-}" in
      strong|partial|contextual|human) ;;
      *) fail "control automation class" "$control_id automation=${automation:-<empty>}" ;;
    esac
    case "${merge_gate:-}" in
      yes|no|contextual) ;;
      *) fail "control merge gate" "$control_id merge_gate=${merge_gate:-<empty>}" ;;
    esac
    [[ -n "${category:-}" ]] || fail "control category" "$control_id category is empty"
    [[ -n "${evidence:-}" ]] || fail "control evidence" "$control_id evidence is empty"
    [[ -n "${enforcement:-}" ]] || fail "control enforcement" "$control_id enforcement is empty"

    token=""
    script_ref=""
    for token in ${(s:;:)enforcement}; do
      if [[ "$token" == script:* ]]; then
        script_ref="${token#script:}"
        if ! script_exists "$script_ref"; then
          fail "control script exists" "$control_id -> $script_ref"
        fi
      fi
    done
  done < "$matrix"
fi

add_expected_from_doc "$docs_dir/coding_practices.md" "CP"
add_expected_from_doc "$docs_dir/testing_practices.md" "TE"

for required in CTRL-01 AOC-01 EVID-01 FPR-01 AISEC-01 PERFLAB-01 RUNTIME-01 FORMAL-01 OPS-01 PROJFRESH-01; do
  expected_ids["$required"]=1
done

id=""
for id in "${(@k)expected_ids}"; do
  display_id="${id//\"/}"
  if [[ -n "${matrix_ids[$id]:-}" ]]; then
    ok "control mapped $display_id"
  else
    fail "control mapped $display_id" "missing from $(relative_path "$matrix")"
  fi
done

if [[ "$failed" -ne 0 ]]; then
  echo "practice controls check failed"
  exit 1
fi

echo "practice controls check passed"
