#!/bin/zsh
set -euo pipefail

target="${1:-.}"
foundation_file="$target/.foundation"
failed=0

fail() {
  local label="$1"
  local detail="${2:-}"
  echo "[FAIL] $label"
  [[ -n "$detail" ]] && echo "  $detail"
  failed=1
}

pass() {
  echo "[OK] $1"
}

check_exists() {
  local label="$1"
  local path="$2"
  if [[ -e "$path" ]]; then
    pass "$label"
  else
    fail "$label" "missing: ${path#$target/}"
  fi
}

check_pattern() {
  local label="$1"
  local pattern="$2"
  local file_path="$3"
  if rg -n "$pattern" "$file_path" >/dev/null 2>&1; then
    pass "$label"
  else
    fail "$label" "expected pattern in ${file_path#$target/}: $pattern"
  fi
}

if [[ ! -f "$foundation_file" ]]; then
  pass "foundation metadata not present; using unknown profile"
  PROFILE="${PROFILE:-unknown}"
else
  set -a
  source "$foundation_file"
  set +a
fi

if [[ "${PROFILE:-}" != "full" && "${PROFILE:-}" != "backend" ]]; then
  pass "river practices not required for ${PROFILE:-unknown} profile"
  exit 0
fi

check_exists "worker command" "$target/cmd/worker/main.go"
check_exists "worker registry" "$target/internal/worker/registry.go"
check_exists "periodic job registry" "$target/internal/worker/periodic_jobs.go"

if [[ -f "$target/go.mod" ]]; then
  check_pattern "river module dependency" "github.com/riverqueue/river\\s" "$target/go.mod"
  check_pattern "river pgx driver dependency" "github.com/riverqueue/river/riverdriver/riverpgxv5" "$target/go.mod"
else
  fail "go module" "missing: go.mod"
fi

if [[ -f "$target/cmd/worker/main.go" ]]; then
  check_pattern "worker initializes River client" "river\\.NewClient" "$target/cmd/worker/main.go"
  check_pattern "worker uses pgx River driver" "riverpgxv5\\.New" "$target/cmd/worker/main.go"
  check_pattern "worker has bounded shutdown" "context\\.WithTimeout" "$target/cmd/worker/main.go"
fi

if [[ -f "$target/internal/worker/registry.go" ]]; then
  check_pattern "worker registry exposes RegisterAll" "func RegisterAll\\(" "$target/internal/worker/registry.go"
  check_pattern "worker registry exposes DefaultQueueConfig" "func DefaultQueueConfig\\(" "$target/internal/worker/registry.go"
  check_pattern "queue limits are configurable" "QUEUE_WORKERS_|Config\\)" "$target/internal/worker/registry.go"
fi

if rg -n "time\\.Sleep\\(" "$target/internal/worker" "$target/cmd/worker" >/dev/null 2>&1; then
  fail "no sleep-based worker polling" "worker paths should use River, blocking queues, or bounded context waits"
else
  pass "no sleep-based worker polling"
fi

if [[ "$failed" -ne 0 ]]; then
  echo "river practices check failed"
  exit 1
fi

echo "river practices check passed"
