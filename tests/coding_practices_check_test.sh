#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
WORKSPACE_DIR="$(dirname "$FOUNDATION_DIR")"

(
    cd "$WORKSPACE_DIR"
    "$FOUNDATION_DIR/tooling/scripts/coding_practices_check.sh"
) >/tmp/ovasabi-coding-practices-check-test.out

if ! rg -n "coding practices check passed" /tmp/ovasabi-coding-practices-check-test.out >/dev/null; then
    cat /tmp/ovasabi-coding-practices-check-test.out >&2
    echo "expected aggregate-root coding practices check to pass for canonical foundation scope" >&2
    exit 1
fi

echo "coding practices check test passed"
