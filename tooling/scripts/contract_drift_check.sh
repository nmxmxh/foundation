#!/bin/zsh
set -euo pipefail

target="${1:-.}"
failed=0

resolve_path() {
  local relative="$1"
  if [[ -e "$target/$relative" ]]; then
    printf '%s\n' "$target/$relative"
    return 0
  fi
  if [[ "$relative" == foundation/* && -e "$target/${relative#foundation/}" ]]; then
    printf '%s\n' "$target/${relative#foundation/}"
    return 0
  fi
  return 1
}

check_exists() {
  local label="$1"
  local relative="$2"
  if resolve_path "$relative" >/dev/null; then
    echo "[OK] $label"
  else
    echo "[FAIL] $label"
    echo "  missing: $relative"
    failed=1
  fi
}

check_generated_pair() {
  local source_relative="$1"
  shift
  local source
  if ! source="$(resolve_path "$source_relative")"; then
    echo "[FAIL] missing contract source"
    echo "  missing: $source_relative"
    failed=1
    return
  fi

  local generated_relative generated
  for generated_relative in "$@"; do
    if ! generated="$(resolve_path "$generated_relative")"; then
      echo "[FAIL] missing generated contract artifact"
      echo "  source: $source_relative"
      echo "  missing: $generated_relative"
      failed=1
      continue
    fi
    if [[ "$source" -nt "$generated" ]]; then
      echo "[FAIL] generated artifact is older than source"
      echo "  source: $source_relative"
      echo "  generated: $generated_relative"
      failed=1
    else
      echo "[OK] $generated_relative"
    fi
  done
}

check_exists "runtime transport proto present" "foundation/runtime-transport/protos/transport/v1/envelope.proto"
check_exists "runtime sdk system contract present" "foundation/runtime-sdk/protocols/system/v1/runtime_buffer.capnp"

check_generated_pair \
  "foundation/runtime-transport/protos/transport/v1/envelope.proto" \
  "foundation/runtime-transport/go/generated/transport/v1/envelope.pb.go" \
  "foundation/runtime-transport/ts/src/generated/transport/v1/envelope.ts"

check_generated_pair \
  "foundation/runtime-transport/protos/transport/v1/metadata.proto" \
  "foundation/runtime-transport/go/generated/transport/v1/metadata.pb.go" \
  "foundation/runtime-transport/ts/src/generated/transport/v1/metadata.ts"

check_generated_pair \
  "foundation/runtime-sdk/protocols/system/v1/runtime_buffer.capnp" \
  "foundation/runtime-sdk/go/runtimehost/generated/runtime_buffer_gen.go" \
  "foundation/runtime-sdk/ts/browser-host/src/generated/runtimeBuffer.ts" \
  "foundation/runtime-sdk/rust/crates/ovrt-core/src/generated.rs"

if [[ "$failed" -ne 0 ]]; then
  echo "contract drift check failed"
  exit 1
fi

echo "contract drift check passed"
