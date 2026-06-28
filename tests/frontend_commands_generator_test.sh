#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
GEN="$FOUNDATION_DIR/tooling/scripts/generate_frontend_commands.mjs"
OUT_DIR="${TMPDIR:-/tmp}/ovasabi-frontend-commands-generator"
CATALOG="$OUT_DIR/route_catalog.json"
OUT_FILE="$OUT_DIR/runtimeRoutes.ts"

rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

cat >"$CATALOG" <<'EOF'
{
  "schema_version": "1.0",
  "generated_by": "server-kit/go/httpapi.BuildRouteCatalog",
  "routes": [
    { "method": "POST", "path": "/v1/user/create", "event_type": "user:create:v1:requested", "required_capability": "user.write", "permission": "write" },
    { "method": "GET", "path": "/v1/user/list", "event_type": "user:list_users:v1:requested", "required_capability": "user.view", "permission": "view" },
    { "method": "PUT", "path": "/media/upload", "event_type": "media:upload:v1:requested", "required_capability": "media.write", "permission": "write" }
  ]
}
EOF

node "$GEN" --catalog "$CATALOG" --out "$OUT_FILE" >"$OUT_DIR/gen.out"

# Re-run in --check mode: a freshly generated file must be reported current.
node "$GEN" --catalog "$CATALOG" --out "$OUT_FILE" --check >"$OUT_DIR/check.out"

for expected in \
  "DO NOT EDIT" \
  "import { createRouteRegistry, type RouteRegistry, type RuntimeRoute }" \
  "export const runtimeRoutes: readonly RuntimeRoute" \
  "eventType: \"user:create:v1:requested\"" \
  "path: \"/media/upload\"" \
  "permission: \"view\"" \
  "export type AppEventType =" \
  "createAppRouteRegistry"; do
  if ! rg -F -n "$expected" "$OUT_FILE" >/dev/null; then
    cat "$OUT_FILE" >&2
    echo "missing frontend commands generator output: $expected" >&2
    exit 1
  fi
done

# Custom non-event route (the transfer upload) must be present — proof the
# Go-authoritative catalog captures what proto derivation could not.
if ! rg -n "media:upload:v1:requested" "$OUT_FILE" >/dev/null; then
  echo "custom route missing from generated registry" >&2
  exit 1
fi

# Determinism: a second generation byte-matches the first.
node "$GEN" --catalog "$CATALOG" --out "$OUT_DIR/runtimeRoutes2.ts" >/dev/null
if ! diff -q "$OUT_FILE" "$OUT_DIR/runtimeRoutes2.ts" >/dev/null; then
  echo "generator output is not deterministic" >&2
  exit 1
fi

# Stale detection: tamper and expect --check to fail.
echo "// tampered" >>"$OUT_FILE"
if node "$GEN" --catalog "$CATALOG" --out "$OUT_FILE" --check >/dev/null 2>&1; then
  echo "stale --check unexpectedly passed" >&2
  exit 1
fi

# Empty catalog -> empty registry with AppEventType = never.
EMPTY_CATALOG="$OUT_DIR/empty.json"
EMPTY_OUT="$OUT_DIR/emptyRoutes.ts"
cat >"$EMPTY_CATALOG" <<'EOF'
{ "schema_version": "1.0", "generated_by": "test", "routes": [] }
EOF
node "$GEN" --catalog "$EMPTY_CATALOG" --out "$EMPTY_OUT" >/dev/null
if ! rg -n "export type AppEventType = never;" "$EMPTY_OUT" >/dev/null; then
  echo "empty catalog should yield AppEventType = never" >&2
  exit 1
fi

# Missing catalog -> skip cleanly (no output written, exit 0).
MISSING_OUT="$OUT_DIR/missingRoutes.ts"
node "$GEN" --catalog "$OUT_DIR/does-not-exist.json" --out "$MISSING_OUT" >/dev/null
if [[ -f "$MISSING_OUT" ]]; then
  echo "missing catalog should not produce output" >&2
  exit 1
fi

# Invalid permission -> hard failure.
BAD_CATALOG="$OUT_DIR/bad.json"
cat >"$BAD_CATALOG" <<'EOF'
{ "schema_version": "1.0", "generated_by": "test", "routes": [
  { "method": "POST", "path": "/v1/x", "event_type": "x:create:v1:requested", "required_capability": "x.write", "permission": "superuser" }
] }
EOF
if node "$GEN" --catalog "$BAD_CATALOG" --out "$OUT_DIR/badRoutes.ts" >/dev/null 2>&1; then
  echo "invalid permission should fail generation" >&2
  exit 1
fi

echo "frontend commands generator test passed"
