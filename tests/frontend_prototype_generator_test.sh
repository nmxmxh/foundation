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

# Generated identifiers come from the bare message name and schema keys from
# domain.collection, so a collision must fail generation (and --check, which is
# what make check-contract-drift runs) instead of emitting a file that
# redeclares identifiers and does not compile.
COLLIDE_ROOT="$OUT_DIR/collide"
COLLIDE_OUT_FILE="$OUT_DIR/collidePrototypeRuntime.ts"
COLLIDE_LOG="$OUT_DIR/collide.out"
rm -rf "$COLLIDE_ROOT"

write_entity_proto() {
  local file="$1" package="$2" message="$3" field="$4"
  mkdir -p "$(dirname "$file")"
  cat >"$file" <<EOF
syntax = "proto3";

package $package;

message $message {
  string id = 1;
  string $field = 2;
}
EOF
}

expect_collision() {
  local root="$1"
  shift
  local mode
  for mode in generate check; do
    local args=(--proto-root "$root" --out "$COLLIDE_OUT_FILE")
    if [[ "$mode" == "check" ]]; then
      args+=(--check)
    fi
    rm -f "$COLLIDE_OUT_FILE"
    if node "$FOUNDATION_DIR/tooling/scripts/generate_frontend_prototype_runtime.mjs" "${args[@]}" >"$COLLIDE_LOG" 2>&1; then
      cat "$COLLIDE_LOG" >&2
      echo "frontend prototype generator ($mode) accepted colliding schemas under $root" >&2
      exit 1
    fi
    if [[ -e "$COLLIDE_OUT_FILE" ]]; then
      echo "frontend prototype generator ($mode) wrote output despite a collision under $root" >&2
      exit 1
    fi
    local expected
    for expected in "$@"; do
      if ! rg -F -q -- "$expected" "$COLLIDE_LOG"; then
        cat "$COLLIDE_LOG" >&2
        echo "frontend prototype generator ($mode) collision report missing: $expected" >&2
        exit 1
      fi
    done
  done
}

# Two domains declaring the same entity name.
write_entity_proto "$COLLIDE_ROOT/cross/billing/v1/billing.proto" billing.v1 Subscription plan
write_entity_proto "$COLLIDE_ROOT/cross/mealplan/v1/mealplan.proto" mealplan.v1 Subscription zone
expect_collision "$COLLIDE_ROOT/cross" \
  "entity name collision" \
  "billing/v1/billing.proto (billing.v1.Subscription)" \
  "mealplan/v1/mealplan.proto (mealplan.v1.Subscription)"

# One entity declared in two versions of a domain.
write_entity_proto "$COLLIDE_ROOT/versions/billing/v1/billing.proto" billing.v1 Subscription plan
write_entity_proto "$COLLIDE_ROOT/versions/billing/v2/billing.proto" billing.v2 Subscription plan
expect_collision "$COLLIDE_ROOT/versions" \
  "billing/v1/billing.proto (billing.v1.Subscription)" \
  "billing/v2/billing.proto (billing.v2.Subscription)"

# Distinct identifiers that map to one domain.collection key.
write_entity_proto "$COLLIDE_ROOT/key/billing/v1/billing.proto" billing.v1 Subscription plan
write_entity_proto "$COLLIDE_ROOT/key/billing/v1/ledger.proto" billing.v1 SubscriptionRecord entry
expect_collision "$COLLIDE_ROOT/key" \
  "schema key collision" \
  "\"billing.subscriptions\"" \
  "billing/v1/billing.proto (billing.v1.Subscription)" \
  "billing/v1/ledger.proto (billing.v1.SubscriptionRecord)"

echo "frontend prototype generator test passed"
