#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
PROJECT_DIR="$(mktemp -d /tmp/ovasabi-foundation-migrations.XXXXXX)"
source "$SCRIPT_DIR/testlib.sh"

cleanup() {
    rm -rf "$PROJECT_DIR"
}
trap cleanup EXIT

mkdir -p "$PROJECT_DIR/frontend" "$PROJECT_DIR/migrations"
sed "s|{{MODULE_PATH}}|github.com/ovasabi/foundation_migration_fixture|g" \
    "$FOUNDATION_DIR/templates/backend/go.mod.template" > "$PROJECT_DIR/go.mod"
printf '{"scripts":{"build":"vite build","lint":"eslint .","test":"vitest run"}}\n' > "$PROJECT_DIR/frontend/package.json"
printf 'CREATE TABLE example (id uuid PRIMARY KEY);\n' > "$PROJECT_DIR/migrations/0001_schema.up.sql"
printf 'DROP TABLE IF EXISTS example;\n' > "$PROJECT_DIR/migrations/0001_schema.down.sql"

cat > "$PROJECT_DIR/.foundation" <<EOF
FOUNDATION_VERSION=0.0.0
FOUNDATION_PATH=$(printf '%q' "$FOUNDATION_DIR")
CREATED_AT=2026-01-01T00:00:00Z
PROFILE=full
PROJECT_NAME=foundation_migration_fixture
GO_MODULE=github.com/ovasabi/foundation_migration_fixture
WITH_DOCKER=true
WITH_WASM=true
BASELINE_GENERATION=legacy
EOF

test_step "update migration fixture from current foundation"
"$FOUNDATION_DIR/scripts/update-project.sh" "$PROJECT_DIR"

if [[ -e "$PROJECT_DIR/migrations/000001_init.up.sql" || -e "$PROJECT_DIR/migrations/000001_init.down.sql" ]]; then
    echo "seed init migration should not be added when project-owned migrations exist" >&2
    exit 1
fi

test_step "validate existing project-owned migration sequence"
"$FOUNDATION_DIR/tooling/scripts/migration_structure_check.sh" "$PROJECT_DIR"

mv "$PROJECT_DIR/migrations/0001_schema.up.sql" "$PROJECT_DIR/migrations/0002_schema.up.sql"
mv "$PROJECT_DIR/migrations/0001_schema.down.sql" "$PROJECT_DIR/migrations/0002_schema.down.sql"
test_step "validate first-prefix rejection"
if "$FOUNDATION_DIR/tooling/scripts/migration_structure_check.sh" "$PROJECT_DIR" >"$PROJECT_DIR/migration_check.log" 2>&1; then
    echo "migration structure check should reject first migration prefixes that do not start at 1" >&2
    exit 1
fi
if ! rg -n "first migration prefix must start at 1" "$PROJECT_DIR/migration_check.log" >/dev/null; then
    echo "migration structure failure should explain the first-prefix rule" >&2
    exit 1
fi

echo "foundation migration seed policy test passed"
