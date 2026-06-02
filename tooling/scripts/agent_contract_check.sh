#!/bin/zsh
set -euo pipefail

target="${1:-.}"
failed=0

if [[ "$target" == "." && -d "./foundation" && ! -f "./go.mod" && ! -f "./package.json" && ! -f "./Cargo.toml" ]]; then
  target="./foundation"
fi

target="${target%/}"

relative_path() {
  local path="$1"
  path="${path#$target/}"
  path="${path#./}"
  echo "$path"
}

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

check_exists() {
  local label="$1"
  local path="$2"
  if [[ -e "$path" ]]; then
    ok "$label"
  else
    fail "$label" "missing: $(relative_path "$path")"
  fi
}

check_file_contains() {
  local label="$1"
  local file="$2"
  local pattern="$3"
  if [[ -f "$file" ]] && grep -Fq -- "$pattern" "$file"; then
    ok "$label"
  else
    fail "$label" "missing pattern: $pattern" "file: $(relative_path "$file")"
  fi
}

check_optional_file_contains() {
  local label="$1"
  local file="$2"
  local pattern="$3"
  [[ -f "$file" ]] || return 0
  check_file_contains "$label" "$file" "$pattern"
}

docs_dir="$target/docs"
if [[ -d "$target/docs/foundation" ]]; then
  docs_dir="$target/docs/foundation"
fi

check_exists "agent operating contract doc" "$docs_dir/agent_operating_contract.md"
check_exists "future practices research doc" "$docs_dir/future_practices_research.md"
check_exists "foundation docs index" "$docs_dir/README.md"
check_file_contains "docs index links agent contract" "$docs_dir/README.md" "agent_operating_contract.md"
check_file_contains "docs index links future practices research" "$docs_dir/README.md" "future_practices_research.md"
check_file_contains "agent contract defines definition of done" "$docs_dir/agent_operating_contract.md" "Definition of Done"
check_file_contains "agent contract defines evidence ledger" "$docs_dir/agent_operating_contract.md" "Evidence Ledger"
check_file_contains "research ledger maps document gaps" "$docs_dir/future_practices_research.md" "Document Gap Map"

check_exists "agents guide" "$target/AGENTS.md"
check_file_contains "agents guide links agent contract" "$target/AGENTS.md" "agent_operating_contract.md"
check_file_contains "agents guide carries definition of done" "$target/AGENTS.md" "Definition of Done"

if [[ -f "$target/CLAUDE.md" ]]; then
  check_file_contains "Claude guide links agent contract" "$target/CLAUDE.md" "agent_operating_contract.md"
elif [[ -f "$target/templates/CLAUDE.md" ]]; then
  check_file_contains "Claude template links agent contract" "$target/templates/CLAUDE.md" "agent_operating_contract.md"
else
  fail "Claude guide or template" "missing: CLAUDE.md or templates/CLAUDE.md"
fi

check_exists "root README" "$target/README.md"
check_file_contains "root README describes agent-native workflow" "$target/README.md" "Agent-Native Workflow"

check_exists "Makefile" "$target/Makefile"
check_file_contains "Makefile exposes agent contract check" "$target/Makefile" "check-agent-contract"

check_optional_file_contains "domain guide links agent contract" "$target/.agents/DOMAIN_GUIDE.md" "agent_operating_contract.md"
check_optional_file_contains "post-init checklist links agent contract" "$target/.agents/POST_INIT.md" "agent_operating_contract.md"
check_optional_file_contains "domain guide template links agent contract" "$target/templates/agents/DOMAIN_GUIDE.md" "agent_operating_contract.md"
check_optional_file_contains "post-init template links agent contract" "$target/templates/agents/POST_INIT.md" "agent_operating_contract.md"

if [[ -d "$target/scripts/checks" ]]; then
  check_exists "scaffolded agent contract check" "$target/scripts/checks/agent_contract_check.sh"
fi

if [[ -d "$target/tooling/scripts" ]]; then
  check_exists "foundation agent contract check" "$target/tooling/scripts/agent_contract_check.sh"
fi

if [[ "$failed" -ne 0 ]]; then
  echo "agent contract check failed"
  exit 1
fi

echo "agent contract check passed"
