#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
OUT_DIR="${TMPDIR:-/tmp}/ovasabi-frontend-prototype-generator"
OUT_FILE="$OUT_DIR/prototypeRuntime.ts"
CUSTOM_PROTO_ROOT="$OUT_DIR/protos"
CUSTOM_OUT_FILE="$OUT_DIR/customPrototypeRuntime.ts"

mkdir -p "$OUT_DIR"
mkdir -p "$CUSTOM_PROTO_ROOT/demo/v1"

cat >"$CUSTOM_PROTO_ROOT/demo/v1/account.proto" <<'EOF'
syntax = "proto3";

package demo.v1;

import "google/protobuf/timestamp.proto";

enum AccountStatus {
  ACCOUNT_STATUS_UNSPECIFIED = 0;
  ACCOUNT_STATUS_ACTIVE = 1;
  ACCOUNT_STATUS_SUSPENDED = 2;
}

message AccountState {
  string id = 1;
  string email = 2;
  repeated string tags = 3;
  map<string, string> attributes = 4;
  AccountStatus status = 5;
  optional string external_url = 6;
  google.protobuf.Timestamp observed_at = 7;
}
EOF

node "$FOUNDATION_DIR/tooling/scripts/generate_frontend_prototype_runtime.mjs" \
  --proto-root "$FOUNDATION_DIR/templates/api/protos" \
  --out "$OUT_FILE" \
  --include-template >/tmp/ovasabi-frontend-prototype-generator.out

node "$FOUNDATION_DIR/tooling/scripts/generate_frontend_prototype_runtime.mjs" \
  --proto-root "$FOUNDATION_DIR/templates/api/protos" \
  --out "$OUT_FILE" \
  --include-template \
  --check >/tmp/ovasabi-frontend-prototype-generator-check.out

for expected in \
  "exampleEntitySchema" \
  "exampleEntityRuntimeConstants" \
  "createExampleEntityDummyFactory" \
  "createCachedExampleEntityDummyRecords" \
  "createExampleEntityStore" \
  "connectExampleEntityLiveProjection" \
  "registerExampleEntityStore" \
  "useExampleEntitySnapshot" \
  "exampleEntityFixtureStates" \
  "exampleEntityBenchmarkFixtures" \
  "prototypeBenchmarkFixtures" \
  "prototypeDomains" \
  "prototypeRuntimeCache" \
  "prototypeSchemaRuntimeConstants" \
  "createPrototypeTenantStores" \
  "createDummyRecords" \
  "example.examples.store.apply.1000" \
  "example.examples.store.reset.1000" \
  "example.examples.store.selector.1000" \
  "\"organization_id\"" \
  "\"kind\": \"uuid\"" \
  "\"kind\": \"timestamp\"" \
  "\"kind\": \"text\""; do
  if ! rg -n "$expected" "$OUT_FILE" >/dev/null; then
    cat "$OUT_FILE" >&2
    echo "missing frontend prototype generator output: $expected" >&2
    exit 1
  fi
done

node "$FOUNDATION_DIR/tooling/scripts/generate_frontend_prototype_runtime.mjs" \
  --proto-root "$CUSTOM_PROTO_ROOT" \
  --out "$CUSTOM_OUT_FILE" >/tmp/ovasabi-frontend-prototype-generator-custom.out

node "$FOUNDATION_DIR/tooling/scripts/generate_frontend_prototype_runtime.mjs" \
  --proto-root "$CUSTOM_PROTO_ROOT" \
  --out "$CUSTOM_OUT_FILE" \
  --check >/tmp/ovasabi-frontend-prototype-generator-custom-check.out

for expected in \
  "accountStateSchema" \
  "\"collection\": \"accounts\"" \
  "\"domain\": \"demo\"" \
  "\"enumValues\"" \
  "ACCOUNT_STATUS_ACTIVE" \
  "\"kind\": \"email\"" \
  "\"kind\": \"object\"" \
  "\"kind\": \"url\"" \
  "\"kind\": \"timestamp\"" \
  "\"repeated\": true" \
  "\"optional\": true" \
  "prototypeDomains" \
  "createPrototypeTenantStores"; do
  if ! rg -n "$expected" "$CUSTOM_OUT_FILE" >/dev/null; then
    cat "$CUSTOM_OUT_FILE" >&2
    echo "missing custom frontend prototype generator output: $expected" >&2
    exit 1
  fi
done

echo "frontend prototype generator test passed"
