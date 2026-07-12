#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
TMP_DIR="$(mktemp -d /tmp/ovasabi-foundation-dry-run.XXXXXX)"
PROJECT_DIR="$TMP_DIR/dry_run_v1"
trap 'rm -rf "$TMP_DIR"' EXIT

"$FOUNDATION_DIR/init.sh" dry_run --project-dir "$PROJECT_DIR" --skip-deps >/dev/null
git -C "$PROJECT_DIR" init -q
git -C "$PROJECT_DIR" add -A
git -C "$PROJECT_DIR" -c user.name=foundation-dry-run -c user.email=check@foundation.invalid commit -qm baseline

before="$(git -C "$PROJECT_DIR" status --porcelain=v1)"
"$FOUNDATION_DIR/scripts/update-project.sh" "$PROJECT_DIR" --dry-run --validate --verify-idempotence >/dev/null
after="$(git -C "$PROJECT_DIR" status --porcelain=v1)"
if [[ "$before" != "$after" ]]; then
    echo "dry-run changed project state" >&2
    git -C "$PROJECT_DIR" status --short >&2
    exit 1
fi

echo "foundation update dry-run invariant passed"
