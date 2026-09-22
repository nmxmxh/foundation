#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/foundation-timeout.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT
source "$ROOT/tooling/scripts/command_timeout.sh"

expect_status() {
  local expected="$1" actual=0
  shift
  "$@" > "$TMP/output" 2>&1 || actual=$?
  if [[ "$actual" != "$expected" ]]; then
    cat "$TMP/output" >&2
    echo "expected status $expected, received $actual" >&2
    exit 1
  fi
}

expect_status 0 run_with_timeout 2 /bin/bash -c 'printf "%s\n" "$1"' -- 'literal argument with spaces'
grep -Fxq 'literal argument with spaces' "$TMP/output"
expect_status 7 run_with_timeout 2 /bin/bash -c 'echo failure-diagnostic >&2; exit 7'
grep -Fq 'failure-diagnostic' "$TMP/output"
expect_status 137 run_with_timeout 2 /bin/bash -c 'kill -KILL $$'
expect_status 64 run_with_timeout 0 /bin/bash -c 'exit 0'
expect_status 64 run_with_timeout invalid /bin/bash -c 'exit 0'
expect_status 64 run_with_timeout 99999 /bin/bash -c 'exit 0'
expect_status 64 run_with_timeout
expect_status 64 run_with_timeout 2
expect_status 127 run_with_timeout 2 "$TMP/missing-command"
(
  PATH="$TMP"
  expect_status 127 run_with_timeout 2 /bin/bash -c 'exit 0'
)

export TIMEOUT_CHILD_MARKER="$TMP/child-survived"
expect_status 124 run_with_timeout 1 /bin/bash -c '
  trap "" TERM
  (sleep 3; printf survived > "$TIMEOUT_CHILD_MARKER") &
  wait
'
grep -Fq 'command timed out after 1s' "$TMP/output"
sleep 2
[[ ! -e "$TIMEOUT_CHILD_MARKER" ]] || { echo 'timed-out descendant survived' >&2; exit 1; }
zsh -c 'source "$1"; run_with_timeout 2 /bin/bash -c "exit 0"' -- "$ROOT/tooling/scripts/command_timeout.sh"

FOUNDATION_DIR="$ROOT"
source "$ROOT/scripts/lib/foundation.sh"
expect_status 0 foundation_run_dependency_command /bin/bash -c 'exit 0'
FOUNDATION_DEPENDENCY_TIMEOUT_SEC=1 expect_status 124 foundation_run_dependency_command /bin/sleep 3
FOUNDATION_DEPENDENCY_TIMEOUT_SEC=invalid expect_status 64 foundation_run_dependency_command /bin/bash -c 'exit 0'
echo 'command timeout regression tests passed'
