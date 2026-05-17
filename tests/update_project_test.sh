#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
PROJECT_DIR="$(mktemp -d /tmp/ovasabi-foundation-update.XXXXXX)"

cleanup() {
    rm -rf "$PROJECT_DIR"
}
trap cleanup EXIT

assert_file() {
    local path="$1"
    if [[ ! -e "$PROJECT_DIR/$path" ]]; then
        echo "missing expected file: $path" >&2
        exit 1
    fi
}

assert_contains() {
    local path="$1"
    local pattern="$2"
    if ! rg -n "$pattern" "$PROJECT_DIR/$path" >/dev/null 2>&1; then
        echo "expected $path to contain: $pattern" >&2
        exit 1
    fi
}

mkdir -p "$PROJECT_DIR/frontend" "$PROJECT_DIR/pkg"
sed "s|{{MODULE_PATH}}|github.com/ovasabi/foundation_update_fixture|g" \
    "$FOUNDATION_DIR/templates/backend/go.mod.template" > "$PROJECT_DIR/go.mod"
printf '{"scripts":{"build":"vite build","lint":"eslint .","test":"vitest run"}}\n' > "$PROJECT_DIR/frontend/package.json"

cat > "$PROJECT_DIR/.foundation" <<EOF
FOUNDATION_VERSION=0.0.0
FOUNDATION_PATH=$(printf '%q' "$FOUNDATION_DIR")
CREATED_AT=2026-01-01T00:00:00Z
PROFILE=full
PROJECT_NAME=foundation_update_fixture
GO_MODULE=github.com/ovasabi/foundation_update_fixture
WITH_DOCKER=true
WITH_WASM=false
BASELINE_GENERATION=legacy
EOF

"$FOUNDATION_DIR/scripts/update-project.sh" "$PROJECT_DIR" >/dev/null

assert_contains ".foundation" "^WITH_WASM=true$"
assert_contains ".foundation" "^BASELINE_GENERATION=manifest-v4$"
assert_file ".cursorrules"
assert_file "cmd/worker/main.go"
assert_file "cmd/docgen/main.go"
assert_file "internal/bootstrap/services.go"
assert_file "internal/worker/registry.go"
assert_file "internal/worker/periodic_jobs.go"
assert_file "wasm/main.go"
assert_file "foundation/runtime-sdk/go/go.mod"
assert_file "docs/foundation/foundation_architecture_contract.md"
assert_file "migrations/000001_init.up.sql"
assert_file "migrations/000001_init.down.sql"
assert_file "Dockerfile"
assert_file "docker-compose.yml"
assert_file "docker-compose.dev.yml"
assert_file "frontend/tsconfig.json"
assert_file "frontend/vite.config.ts"

if [[ -e "$PROJECT_DIR/pkg" ]]; then
    echo "expected empty pkg/ to be removed" >&2
    exit 1
fi

"$PROJECT_DIR/scripts/checks/project_scaffold_check.sh" "$PROJECT_DIR"
"$PROJECT_DIR/scripts/checks/river_practices_check.sh" "$PROJECT_DIR"

echo "foundation update-project test passed"
