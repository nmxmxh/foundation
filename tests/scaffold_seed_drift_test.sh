#!/bin/bash
# Seed-drift contract: create-mode files get a seed ledger entry at scaffold
# time (template hash + rendered hash). A default update stays silent while
# templates are unchanged, warns when a template evolves (distinguishing
# unmodified from customized local copies), re-baselines only with
# --acknowledge-seed-drift, and reseeds deleted files with a fresh ledger row.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
TMP_DIR="$(mktemp -d /tmp/ovasabi-foundation-drift.XXXXXX)"
PROJECT_DIR="$TMP_DIR/drift_project_v1"
LEDGER="$PROJECT_DIR/.foundation-seeds.tsv"
SENTINEL="AGENTS.md"
source "$SCRIPT_DIR/testlib.sh"

cleanup() {
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

run_update() {
    "$FOUNDATION_DIR/scripts/update-project.sh" "$PROJECT_DIR" "$@" 2>&1
}

fake_template_hash() {
    # Rewrite the sentinel's recorded template hash so update sees an
    # "evolved" template without mutating real Foundation templates.
    awk -F'\t' -v OFS='\t' -v d="$SENTINEL" \
        '$1 == d { $2 = "0000000000000000000000000000000000000000000000000000000000000000" } { print }' \
        "$LEDGER" >"$LEDGER.tmp"
    mv "$LEDGER.tmp" "$LEDGER"
}

test_step "fresh init seeds the ledger"
"$FOUNDATION_DIR/init.sh" drift_project --project-dir "$PROJECT_DIR" --skip-deps >/dev/null
assert_file ".foundation-seeds.tsv"
if ! awk -F'\t' -v d="$SENTINEL" '$1 == d { found = 1 } END { exit !found }' "$LEDGER"; then
    echo "seed ledger is missing a row for $SENTINEL" >&2
    exit 1
fi

test_step "default update is silent while templates are unchanged"
output="$(run_update)"
if echo "$output" | grep -q "Seed drift"; then
    echo "update warned about seed drift on an unchanged scaffold:" >&2
    echo "$output" | grep "Seed drift" >&2
    exit 1
fi

test_step "evolved template + unmodified local copy warns with reseed hint"
fake_template_hash
output="$(run_update)"
if ! echo "$output" | grep -q "Seed drift: $SENTINEL.*unmodified"; then
    echo "expected unmodified-copy drift warning, got:" >&2
    echo "$output" | grep -i "drift" >&2 || echo "(no drift lines)" >&2
    exit 1
fi

test_step "evolved template + customized local copy warns with review hint"
fake_template_hash
printf '\n<!-- project customization -->\n' >>"$PROJECT_DIR/$SENTINEL"
output="$(run_update)"
if ! echo "$output" | grep -q "Seed drift: $SENTINEL.*customized"; then
    echo "expected customized-copy drift warning, got:" >&2
    echo "$output" | grep -i "drift" >&2 || echo "(no drift lines)" >&2
    exit 1
fi

test_step "acknowledge re-baselines and silences the warning"
output="$(run_update --acknowledge-seed-drift)"
if ! echo "$output" | grep -q "Seed drift acknowledged: $SENTINEL"; then
    echo "expected acknowledgement message, got:" >&2
    echo "$output" | grep -i "drift" >&2 || echo "(no drift lines)" >&2
    exit 1
fi
output="$(run_update)"
if echo "$output" | grep -q "Seed drift"; then
    echo "drift warning persisted after acknowledgement:" >&2
    echo "$output" | grep "Seed drift" >&2
    exit 1
fi

test_step "deleting a seeded file reseeds it and refreshes its ledger row"
rm "$PROJECT_DIR/$SENTINEL"
run_update >/dev/null
assert_file "$SENTINEL"
output="$(run_update)"
if echo "$output" | grep -q "Seed drift: $SENTINEL"; then
    echo "reseeded file still reports drift:" >&2
    echo "$output" | grep "Seed drift" >&2
    exit 1
fi

echo "foundation scaffold seed drift test passed"
