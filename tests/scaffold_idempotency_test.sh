#!/bin/bash
# Scaffold idempotency invariant: a fresh `init.sh` followed immediately by a
# default `update-project.sh` must produce zero drift in tracked files, except
# the LAST_UPDATED stamp inside .foundation metadata. Any other diff means the
# generator is non-deterministic or init and update disagree about the
# baseline — both are fleet-sync hazards.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
TMP_DIR="$(mktemp -d /tmp/ovasabi-foundation-idem.XXXXXX)"
PROJECT_DIR="$TMP_DIR/idem_project_v1"
source "$SCRIPT_DIR/testlib.sh"

cleanup() {
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

test_step "scaffold a fresh project"
"$FOUNDATION_DIR/init.sh" idem_project --project-dir "$PROJECT_DIR" --skip-deps >/dev/null

test_step "snapshot the fresh scaffold"
git -C "$PROJECT_DIR" init -q
git -C "$PROJECT_DIR" add -A
git -C "$PROJECT_DIR" -c user.name="foundation-idempotency-check" \
    -c user.email="check@foundation.invalid" \
    commit -qm "fresh scaffold baseline" --allow-empty >/dev/null

test_step "run a default update against the fresh scaffold"
"$FOUNDATION_DIR/scripts/update-project.sh" "$PROJECT_DIR" >/dev/null

test_step "assert zero drift outside .foundation metadata"
drift="$(git -C "$PROJECT_DIR" status --porcelain | grep -v '^ M \.foundation$' || true)"
if [[ -n "$drift" ]]; then
    echo "update-project.sh against a fresh scaffold produced drift:" >&2
    echo "$drift" >&2
    echo "--- diff (first 120 lines) ---" >&2
    git -C "$PROJECT_DIR" diff | head -120 >&2
    exit 1
fi

test_step "assert .foundation changed only LAST_UPDATED"
metadata_drift="$(git -C "$PROJECT_DIR" diff -- .foundation | grep -E '^[+-][^+-]' | grep -v '^[+-]LAST_UPDATED=' || true)"
if [[ -n "$metadata_drift" ]]; then
    echo ".foundation drifted beyond the LAST_UPDATED stamp:" >&2
    echo "$metadata_drift" >&2
    exit 1
fi

echo "foundation scaffold idempotency test passed"
