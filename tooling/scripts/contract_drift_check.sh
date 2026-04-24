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

check_generated_if_present() {
  local source_relative="$1"
  local generated_relative="$2"

  local source generated
  if ! source="$(resolve_path "$source_relative")"; then
    echo "[FAIL] missing contract source"
    echo "  missing: $source_relative"
    failed=1
    return
  fi

  if ! generated="$(resolve_path "$generated_relative")"; then
    echo "[OK] generated artifact not committed: $generated_relative"
    return
  fi

  if [[ ! -s "$generated" ]]; then
    echo "[FAIL] generated artifact is empty"
    echo "  generated: $generated_relative"
    failed=1
    return
  fi

  if ! rg -n "generated|DO NOT EDIT|Code generated" "$generated" >/dev/null 2>&1; then
    echo "[FAIL] generated artifact is missing a generation marker"
    echo "  generated: $generated_relative"
    failed=1
    return
  fi

  echo "[OK] $generated_relative"
}

check_exists "runtime transport proto present" "foundation/runtime-transport/protos/transport/v1/envelope.proto"
check_exists "runtime transport generation script present" "foundation/runtime-transport/scripts/generate_bindings.sh"

check_generated_if_present \
  "foundation/runtime-transport/protos/transport/v1/envelope.proto" \
  "foundation/runtime-transport/go/generated/transport/v1/envelope.pb.go"
check_generated_if_present \
  "foundation/runtime-transport/protos/transport/v1/metadata.proto" \
  "foundation/runtime-transport/ts/src/generated/transport/v1/metadata.ts"

if resolve_path "foundation/runtime-sdk/protocols/system/v1/runtime_buffer.capnp" >/dev/null; then
  check_exists "runtime sdk generation script present" "foundation/runtime-sdk/scripts/generate_system_bindings.sh"
  check_exists "runtime shared arena contract present" "foundation/runtime-sdk/protocols/system/v1/runtime_shared_arena.capnp"
  check_generated_if_present \
    "foundation/runtime-sdk/protocols/system/v1/runtime_buffer.capnp" \
    "foundation/runtime-sdk/go/runtimehost/generated/runtime_buffer_gen.go"
  check_generated_if_present \
    "foundation/runtime-sdk/protocols/system/v1/runtime_buffer.capnp" \
    "foundation/runtime-sdk/ts/browser-host/src/generated/runtimeBuffer.ts"
  check_generated_if_present \
    "foundation/runtime-sdk/protocols/system/v1/runtime_buffer.capnp" \
    "foundation/runtime-sdk/rust/crates/ovrt-core/src/generated.rs"
else
  echo "[OK] runtime-sdk not vendored for this project"
fi

if [[ "$failed" -ne 0 ]]; then
  echo "contract drift check failed"
  exit 1
fi

echo "contract drift check passed"
