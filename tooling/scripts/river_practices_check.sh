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

# find_code searches Go source with comment lines excluded.
#
# A prohibition check that greps raw source also matches the comment explaining
# why the practice is prohibited. That is worse than a false positive: it makes
# the check fire on the file that documents the rule, and the cheapest way to get
# green is to delete the explanation. Every "this must not appear" check below
# goes through here so a codebase can keep its reasoning.
#
# Line comments and single-line block comments are dropped; a banned call buried
# inside a multi-line block comment is accepted as the rare cost of not needing a
# Go parser in a shell script.
find_code() {
  local pattern="$1"
  shift
  local paths=()
  local candidate
  for candidate in "$@"; do
    [[ -e "$candidate" ]] && paths+=("$candidate")
  done
  (( ${#paths[@]} == 0 )) && return 1

  rg --no-heading --line-number --type go "$pattern" "${paths[@]}" 2>/dev/null \
    | grep -v -E ':[0-9]+:[[:space:]]*(//|\*|/\*)' \
    | grep -q .
}

# find_code_files lists Go files whose non-comment lines match a pattern.
find_code_files() {
  local pattern="$1"
  shift
  local paths=()
  local candidate
  for candidate in "$@"; do
    [[ -e "$candidate" ]] && paths+=("$candidate")
  done
  (( ${#paths[@]} == 0 )) && return 0

  rg --no-heading --line-number --type go "$pattern" "${paths[@]}" 2>/dev/null \
    | grep -v -E ':[0-9]+:[[:space:]]*(//|\*|/\*)' \
    | cut -d: -f1 \
    | sort -u
}

# check_absent fails when a banned pattern appears in real code.
check_absent() {
  local label="$1"
  local pattern="$2"
  local detail="$3"
  shift 3
  if find_code "$pattern" "$@"; then
    fail "$label" "$detail"
  else
    pass "$label"
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

check_absent "no sleep-based worker polling" \
  "time\\.Sleep\\(" \
  "worker paths should use River, blocking queues, or bounded context waits; a sleep inside a job holds its worker slot, so on a bounded queue it is a throughput ceiling rather than pacing" \
  "$target/internal/worker" "$target/cmd/worker"

check_absent "avoid raw client.InsertMany for unique jobs" \
  "client\\.InsertMany\\(" \
  "raw InsertMany triggers CTE array unnesting and index tuple lock contention on UniqueOpts; use workerkit.Engine.EnqueueMany or individual client.Insert loops" \
  "$target/internal" "$target/cmd"

# River owns the lifecycle of its own table. A hand-rolled DELETE races River's
# `FOR UPDATE SKIP LOCKED` job fetch, so it degrades the queue it is trying to
# tidy — and a predicate wide enough to catch completed rows has repeatedly been
# wide enough to catch `retry` rows too, silently destroying work River intended
# to retry and turning a transient upstream failure into lost jobs. Configure
# River's JobCleaner instead.
check_absent "no hand-rolled river_job deletion" \
  "(?i)delete\\s+from\\s+river_job" \
  "River's JobCleaner owns job retention; a manual DELETE contends with River's job fetch and can destroy retry-state rows" \
  "$target/internal" "$target/cmd"

# An Insert with UniqueOpts reports a rejected duplicate through the *result*
# (rivertype.JobInsertResult.UniqueSkippedAsDuplicate), not through an error.
# Treating every result as enqueued reports work that was never scheduled — which
# is precisely the signal that would otherwise show ingestion having stalled.
#
# Scoped to a file that both declares UniqueOpts and performs its own insert.
#
# Per file, not per tree: river.NewPeriodicJob returns UniqueOpts from a
# constructor callback and River does the inserting, so a project whose only
# unique jobs are periodic has no result to inspect anywhere. Matching
# "some file inserts" AND "some file uses UniqueOpts" across a whole repo flagged
# exactly that shape, and satisfying it would have meant adding unreachable code
# to appease a checker — the failure mode that teaches people to distrust checks.
unique_insert_files="$(comm -12 \
  <(find_code_files "\\.Insert(Many)?\\(" "$target/internal" "$target/cmd") \
  <(find_code_files "UniqueOpts" "$target/internal" "$target/cmd"))"

if [[ -n "$unique_insert_files" ]]; then
  if find_code "UniqueSkippedAsDuplicate" "$target/internal" "$target/cmd"; then
    pass "unique job inserts inspect UniqueSkippedAsDuplicate"
  else
    fail "unique job inserts inspect UniqueSkippedAsDuplicate" \
      "$(echo "$unique_insert_files" | head -3 | sed "s|^$target/||" | tr '\n' ' ')calls Insert with UniqueOpts but never reads UniqueSkippedAsDuplicate; a uniqueness rejection arrives in the result, not as an error, so counting results reports work that was not enqueued"
  fi
fi


if [[ "$failed" -ne 0 ]]; then
  echo "river practices check failed"
  exit 1
fi

echo "river practices check passed"
