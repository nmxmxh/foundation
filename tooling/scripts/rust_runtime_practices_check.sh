#!/bin/zsh
set -euo pipefail

target="${1:-.}"
failed=0
tmp_output="/tmp/ovasabi_rust_runtime_practices.out"

check_has_match() {
  local label="$1"
  local pattern="$2"
  shift 2
  if rg -n "$pattern" "$@" >/dev/null 2>&1; then
    echo "[OK] $label"
  else
    echo "[FAIL] $label"
    failed=1
  fi
}

check_no_match() {
  local label="$1"
  local pattern="$2"
  shift 2
  if rg -n "$pattern" "$@" >"$tmp_output" 2>/dev/null; then
    echo "[FAIL] $label"
    cat "$tmp_output"
    failed=1
  else
    echo "[OK] $label"
  fi
}

native_root="$target/runtime-native/rust"
sdk_root="$target/runtime-sdk/rust"

if [[ -d "$native_root" ]]; then
  check_has_match \
    "RP runtime-native crate forbids unsafe code" \
    '^#!\[forbid\(unsafe_code\)\]' \
    "$native_root/src/lib.rs"

  check_no_match \
    "RP runtime-native native GPU registry avoids unsafe/unwrap/todo/dbg" \
    '\bunsafe\b|\bunwrap\s*\(|todo!\s*\(|dbg!\s*\(' \
    "$native_root/src/native_gpu.rs"

  check_has_match \
    "RP native GPU registry tests fd-backed private handles" \
    'registers_unix_fd_handle_without_exposing_fd' \
    "$native_root/src/native_gpu.rs"

  check_has_match \
    "RP native GPU registry tests opaque plugin private handles" \
    'registers_plugin_opaque_handle_without_exposing_token' \
    "$native_root/src/native_gpu.rs"

  check_has_match \
    "RP native GPU benchmark reports descriptor lifecycle" \
    'native-gpu-descriptor-contract' \
    "$native_root/src/bin/native_flow_sim.rs"

  check_has_match \
    "RP native GPU benchmark reports registry lifecycle" \
    'native-gpu-registry-lifecycle' \
    "$native_root/src/bin/native_flow_sim.rs"

  check_has_match \
    "RP native GPU benchmark reports plugin opaque lifecycle" \
    'native-gpu-plugin-opaque-lifecycle' \
    "$native_root/src/bin/native_flow_sim.rs"
else
  echo "[OK] runtime-native Rust crate absent"
fi

if [[ -d "$sdk_root" ]]; then
  check_has_match \
    "RP ovrt-core native GPU contract forbids unsafe code" \
    '^#!\[forbid\(unsafe_code\)\]' \
    "$sdk_root/crates/ovrt-core/src/native_gpu.rs"

  check_no_match \
    "RP ovrt-core native GPU contract avoids unwrap/todo/dbg" \
    '\bunwrap\s*\(|todo!\s*\(|dbg!\s*\(' \
    "$sdk_root/crates/ovrt-core/src/native_gpu.rs"

  check_has_match \
    "RP ovrt-core native GPU tests enum contract mapping" \
    'maps_all_native_gpu_enums_to_contract_codes' \
    "$sdk_root/crates/ovrt-core/src/native_gpu.rs"
else
  echo "[OK] runtime-sdk Rust workspace absent"
fi

rm -f "$tmp_output"

if [[ "$failed" -ne 0 ]]; then
  echo "rust runtime practices check failed"
  exit 1
fi

echo "rust runtime practices check passed"
