#!/bin/bash
# Seed-drift contract: create-mode files get a seed ledger entry at scaffold
# time (template hash + rendered hash). A default update stays silent while
# templates are unchanged, warns when a template evolves (distinguishing
# unmodified from customized local copies), re-baselines only with
# --acknowledge-seed-drift, and reseeds deleted files with a fresh ledger row
# while saying so out loud. A destination listed in .foundation-seeds.ignore is
# never seeded again, which is the only way a project can decline a seed.
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

test_step "evolved template + unmodified local copy safely reseeds"
fake_template_hash
output="$(run_update)"
if ! echo "$output" | grep -q "Safely reseeded untouched project-owned file: $SENTINEL"; then
    echo "expected safe automatic reseed, got:" >&2
    echo "$output" | grep -i "drift" >&2 || echo "(no drift lines)" >&2
    exit 1
fi

test_step "no-auto-reseed preserves the prior warning behavior"
fake_template_hash
output="$(run_update --no-auto-reseed)"
if ! echo "$output" | grep -q "Seed drift: $SENTINEL.*unmodified"; then
    echo "expected unmodified-copy drift warning, got:" >&2
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

test_step "deleting a seeded file reseeds it, refreshes its row, and reports it"
rm "$PROJECT_DIR/$SENTINEL"
output="$(run_update)"
assert_file "$SENTINEL"
if ! echo "$output" | grep -q "Re-seeding a previously removed file: $SENTINEL"; then
    echo "resurrection of a removed seed was not reported:" >&2
    echo "$output" | grep -i "seed" >&2 || echo "(no seed lines)" >&2
    exit 1
fi
output="$(run_update)"
if echo "$output" | grep -q "Seed drift: $SENTINEL"; then
    echo "reseeded file still reports drift:" >&2
    echo "$output" | grep "Seed drift" >&2
    exit 1
fi

test_step "a tombstoned seed stays deleted and leaves the ledger"
cat >"$PROJECT_DIR/.foundation-seeds.ignore" <<IGNORE
# Destinations this project deliberately removed.
$SENTINEL
IGNORE
rm "$PROJECT_DIR/$SENTINEL"
output="$(run_update)"
if [[ -e "$PROJECT_DIR/$SENTINEL" ]]; then
    echo "tombstoned seed was written back to $SENTINEL" >&2
    exit 1
fi
if ! echo "$output" | grep -q "declined to seed"; then
    echo "expected a tombstone report, got:" >&2
    echo "$output" | grep -i "seed" >&2 || echo "(no seed lines)" >&2
    exit 1
fi
if awk -F'\t' -v d="$SENTINEL" '$1 == d { found = 1 } END { exit !found }' "$LEDGER"; then
    echo "tombstoned destination is still tracked in the seed ledger" >&2
    exit 1
fi

test_step "a tombstoned seed reports no drift"
output="$(run_update)"
if echo "$output" | grep -q "Seed drift: $SENTINEL"; then
    echo "tombstoned destination reported seed drift:" >&2
    echo "$output" | grep "Seed drift" >&2
    exit 1
fi

test_step "removing the tombstone lets the seed return"
rm "$PROJECT_DIR/.foundation-seeds.ignore"
run_update >/dev/null
assert_file "$SENTINEL"

# Regression: on 2026-08-25 a fleet update destroyed project code in six
# repositories. The backfill path records whatever is on disk as seeded_sha256,
# so a heavily customized create-mode file looked byte-identical to "what
# Foundation wrote". When the template later moved, AUTO_RESEED_UNTOUCHED read
# that as "unmodified since seeding" and overwrote the customization.
#
# A backfilled row must never be auto-reseeded, however unchanged it looks.
test_step "a backfilled row is never auto-reseeded, even when its hash matches"
CUSTOM="$PROJECT_DIR/$SENTINEL"
printf 'PROJECT OWNED CONTENT THAT MUST SURVIVE\n' >"$CUSTOM"
# Drop the row entirely, reproducing a project scaffolded before the ledger.
awk -F'\t' -v d="$SENTINEL" '$1 != d { print }' "$LEDGER" >"$LEDGER.tmp"
mv "$LEDGER.tmp" "$LEDGER"
# First update backfills the row from the customized file on disk.
run_update >/dev/null
if ! awk -F'\t' -v d="$SENTINEL" '$1 == d && $4 == "backfilled" { found = 1 } END { exit !found }' "$LEDGER"; then
    echo "backfilled row was not marked as backfilled:" >&2
    grep -F "$SENTINEL" "$LEDGER" >&2 || true
    exit 1
fi
# Now evolve the template. The pre-fix code reseeded here and lost the content.
fake_template_hash
output="$(run_update)"
if ! grep -q "PROJECT OWNED CONTENT THAT MUST SURVIVE" "$CUSTOM"; then
    echo "auto-reseed destroyed project-owned content in a backfilled row" >&2
    exit 1
fi
if echo "$output" | grep -q "Safely reseeded untouched project-owned file: $SENTINEL"; then
    echo "backfilled row was auto-reseeded; it must only ever warn" >&2
    exit 1
fi
# Any drift wording is acceptable here; the warning path never writes. The
# exact "backfilled" wording is not asserted because a managed patch edits
# AGENTS.md after the backfill records its hash, so this sentinel legitimately
# reports as customized rather than as unchanged-since-backfill.
if ! echo "$output" | grep -q "Seed drift: $SENTINEL"; then
    echo "expected a drift warning for a backfilled row, got:" >&2
    echo "$output" | grep -i "drift" >&2 || echo "(no drift lines)" >&2
    exit 1
fi

echo "foundation scaffold seed drift test passed"
