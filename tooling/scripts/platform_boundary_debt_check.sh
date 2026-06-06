#!/bin/zsh
set -euo pipefail

target="${1:-.}"
failed=0

if [[ "$target" == "." && -d "./foundation" && ! -f "./go.mod" && ! -f "./package.json" && ! -f "./Cargo.toml" ]]; then
  target="./foundation"
fi

map_budget=29
json_budget=47

map_allowed_files=(
  "runtime-transport/go/transport/value.go"
  "runtime-transport/go/transport/transport.go"
  "server-kit/go/metadata/metadata.go"
  "server-kit/go/redis/client.go"
  "server-kit/go/security/redaction.go"
  "server-kit/go/hermes/drift.go"
  "server-kit/go/hermes/indexes.go"
  "server-kit/go/extension/value.go"
)

json_allowed_files=(
  "runtime-transport/go/transport/value.go"
  "server-kit/go/auth/jwt.go"
  "server-kit/go/bulk/manager.go"
  "server-kit/go/cache/cache.go"
  "server-kit/go/database/database.go"
  "server-kit/go/database/persistence_helpers.go"
  "server-kit/go/database/postgres.go"
  "server-kit/go/database/record_data.go"
  "server-kit/go/domainerr/http.go"
  "server-kit/go/errors/errors.go"
  "server-kit/go/eventlog/eventlog.go"
  "server-kit/go/events/envelope.go"
  "server-kit/go/extension/value.go"
  "server-kit/go/featureflags/featureflags.go"
  "server-kit/go/grpcsvc/grpcsvc.go"
  "server-kit/go/healthcheck/healthcheck.go"
  "server-kit/go/hermes/drift.go"
  "server-kit/go/httpapi/dispatch_route.go"
  "server-kit/go/httpapi/helpers.go"
  "server-kit/go/httpapi/routes.go"
  "server-kit/go/metadata/metadata.go"
  "server-kit/go/observability/http.go"
  "server-kit/go/policy/policy.go"
  "server-kit/go/resilience/httpclient.go"
  "server-kit/go/versioning/versioning.go"
  "server-kit/go/worker/metadata.go"
)

is_allowed_file() {
  local file="$1"
  shift
  local allowed
  for allowed in "$@"; do
    if [[ "$file" == "$target/$allowed" || "$file" == "$allowed" ]]; then
      return 0
    fi
  done
  return 1
}

scan_pattern() {
  local label="$1"
  local pattern="$2"
  shift 2
  rg -n \
    --glob '!**/*_test.go' \
    --glob '!**/generated/**' \
    --glob '!**/testdata/**' \
    --glob '!**/node_modules/**' \
    --glob '!**/dist/**' \
    --glob '!**/target/**' \
    "$pattern" "$target/server-kit/go" "$target/runtime-transport/go" 2>/dev/null || true
}

check_allowed() {
  local label="$1"
  local matches="$2"
  shift 2
  local unexpected=()
  local line file
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    file="${line%%:*}"
    if ! is_allowed_file "$file" "$@"; then
      unexpected+=("$line")
    fi
  done <<< "$matches"

  if (( ${#unexpected[@]} > 0 )); then
    echo "[FAIL] $label outside approved boundary files"
    printf '%s\n' "${unexpected[@]}"
    failed=1
  else
    echo "[OK] $label remains inside approved boundary files"
  fi
}

check_budget() {
  local label="$1"
  local count="$2"
  local budget="$3"
  if (( count > budget )); then
    echo "[FAIL] $label count $count exceeds budget $budget"
    failed=1
  else
    echo "[OK] $label count $count <= budget $budget"
  fi
}

map_matches="$(scan_pattern "dynamic map" "map\\[string\\]any")"
json_matches="$(scan_pattern "json boundary" "json\\.(Marshal|Unmarshal|NewDecoder|NewEncoder|Encoder|Decoder)")"

map_count=$(printf '%s\n' "$map_matches" | sed '/^$/d' | wc -l | tr -d ' ')
json_count=$(printf '%s\n' "$json_matches" | sed '/^$/d' | wc -l | tr -d ' ')

check_budget "production map[string]any" "$map_count" "$map_budget"
check_budget "production JSON encode/decode" "$json_count" "$json_budget"
check_allowed "production map[string]any" "$map_matches" "${map_allowed_files[@]}"
check_allowed "production JSON encode/decode" "$json_matches" "${json_allowed_files[@]}"

echo "production map[string]any count: $map_count"
echo "production JSON encode/decode count: $json_count"
echo "JSON debt reduction priority: replace internal JSON round-trips first; keep explicit wire, HTTP, DB JSONB, JWT, cache codec, protojson, and typed value MarshalJSON boundaries classified."

if [[ "$failed" -ne 0 ]]; then
  echo "platform boundary debt check failed"
  exit 1
fi

echo "platform boundary debt check passed"
