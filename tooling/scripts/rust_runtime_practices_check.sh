#!/bin/zsh
set -euo pipefail

target="${1:-.}"
target="$(cd "$target" && pwd)"
failed=0
tmp_output="$(mktemp "${TMPDIR:-/tmp}/ovasabi_rust_runtime_practices.XXXXXX")"
trap 'rm -f "$tmp_output"' EXIT

append_existing_dirs() {
  local array_name="$1"
  shift
  local dir
  for dir in "$@"; do
    if [[ -d "$dir" ]]; then
      eval "$array_name+=(\"\$dir\")"
    fi
  done
}

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

check_no_match_before_tests() {
  local label="$1"
  local pattern="$2"
  shift 2
  : >"$tmp_output"

  local root file
  for root in "$@"; do
    [[ -d "$root" ]] || continue
    while IFS= read -r file; do
      awk -v pattern="$pattern" '
        /^[[:space:]]*#\[cfg\(test\)\]/ { exit }
        $0 ~ pattern { printf "%s:%d:%s\n", FILENAME, NR, $0 }
      ' "$file" >>"$tmp_output"
    done < <(rg --files "$root" \
      -g '*.rs' \
      -g '!**/target/**' \
      -g '!**/node_modules/**' \
      -g '!**/src/bin/**' \
      -g '!**/benches/**' \
      -g '!**/examples/**')
  done

  if [[ -s "$tmp_output" ]]; then
    echo "[FAIL] $label"
    cat "$tmp_output"
    failed=1
  else
    echo "[OK] $label"
  fi
}

native_roots=()
sdk_roots=()
app_roots=()

append_existing_dirs native_roots \
  "$target/runtime-native/rust" \
  "$target/foundation/runtime-native/rust"
append_existing_dirs sdk_roots \
  "$target/runtime-sdk/rust" \
  "$target/foundation/runtime-sdk/rust"
append_existing_dirs app_roots \
  "$target/rust" \
  "$target/native/src-tauri"
scan_roots=("${sdk_roots[@]}" "${native_roots[@]}" "${app_roots[@]}")

run_optional_miri() {
  if [[ "${RUST_RUNTIME_MIRI:-0}" != "1" ]]; then
    echo "[OK] Miri runtime UB check is opt-in with RUST_RUNTIME_MIRI=1"
    return 0
  fi

  if ! command -v cargo >/dev/null 2>&1 || ! cargo miri --version >/dev/null 2>&1; then
    echo "[FAIL] RUST_RUNTIME_MIRI=1 requested but cargo miri is unavailable"
    failed=1
    return 0
  fi

  local root seen_roots=()
  for root in "${scan_roots[@]}"; do
    [[ -f "$root/Cargo.toml" ]] || continue
    seen_roots+=("$root")
    if (cd "$root" && cargo miri test); then
      echo "[OK] Miri runtime tests: ${root#$target/}"
    else
      echo "[FAIL] Miri runtime tests: ${root#$target/}"
      failed=1
    fi
  done

  if (( ${#seen_roots[@]} == 0 )); then
    echo "[OK] no Cargo runtime roots found for Miri"
  fi
}

run_optional_loom() {
  if [[ "${RUST_RUNTIME_LOOM:-0}" != "1" ]]; then
    echo "[OK] Loom concurrency model tests are opt-in with RUST_RUNTIME_LOOM=1"
    return 0
  fi

  if ! command -v cargo >/dev/null 2>&1; then
    echo "[FAIL] RUST_RUNTIME_LOOM=1 requested but cargo is unavailable"
    failed=1
    return 0
  fi

  local root found=0
  for root in "${scan_roots[@]}"; do
    [[ -f "$root/Cargo.toml" ]] || continue
    if ! rg -n '(^|[^A-Za-z0-9_])loom[[:space:]]*=' "$root/Cargo.toml" >/dev/null 2>&1; then
      continue
    fi
    found=1
    if (cd "$root" && cargo test --features loom); then
      echo "[OK] Loom feature tests: ${root#$target/}"
    else
      echo "[FAIL] Loom feature tests: ${root#$target/}"
      failed=1
    fi
  done

  if [[ "$found" -eq 0 ]]; then
    echo "[OK] no Loom-enabled Cargo runtime roots found"
  fi
}

if (( ${#scan_roots[@]} > 0 )); then
  check_no_match_before_tests \
    "RP production Rust runtime paths avoid unwrap/expect/panic/todo/dbg" \
    'unwrap[[:space:]]*[(]|expect[[:space:]]*[(]|panic![[:space:]]*[(]|todo![[:space:]]*[(]|dbg![[:space:]]*[(]' \
    "${scan_roots[@]}"
else
  echo "[OK] Rust production source roots absent"
fi

if (( ${#native_roots[@]} > 0 )); then
  for native_root in "${native_roots[@]}"; do
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
  done
else
  echo "[OK] runtime-native Rust crate absent"
fi

if (( ${#sdk_roots[@]} > 0 )); then
  for sdk_root in "${sdk_roots[@]}"; do
  check_has_match \
    "RP native runtime dispatch has bounded worker response waits" \
    'recv_timeout' \
    "$sdk_root/crates/ovrt-native/src/lib.rs"

  check_has_match \
    "RP stdio transport bounds frame allocation before payload read" \
    'read_frame_bounded' \
    "$sdk_root/crates/ovrt-native/src/stdio.rs"

  check_has_match \
    "RP native buffer rejects negative declared payload lengths" \
    'payload_length' \
    "$sdk_root/crates/ovrt-native/src/buffer.rs"

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
  done
else
  echo "[OK] runtime-sdk Rust workspace absent"
fi

if (( ${#app_roots[@]} > 0 )); then
  for app_root in "${app_roots[@]}"; do
    if [[ -f "$app_root/src/lib.rs" ]]; then
      check_no_match \
        "RP scaffolded app Rust lib avoids unsafe/unwrap/expect/panic/todo/dbg" \
        '\bunsafe\b|unwrap[[:space:]]*[(]|expect[[:space:]]*[(]|panic![[:space:]]*[(]|todo![[:space:]]*[(]|dbg![[:space:]]*[(]' \
        "$app_root/src/lib.rs"
    fi
  done
else
  echo "[OK] scaffolded app Rust roots absent"
fi

run_optional_miri
run_optional_loom

if [[ "$failed" -ne 0 ]]; then
  echo "rust runtime practices check failed"
  exit 1
fi

echo "rust runtime practices check passed"
