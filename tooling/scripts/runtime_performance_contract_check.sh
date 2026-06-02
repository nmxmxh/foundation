#!/bin/zsh
set -euo pipefail

target="${1:-.}"
target="$(cd "$target" && pwd)"
failed=0

ok() {
  echo "[OK] $1"
}

fail() {
  echo "[FAIL] $1"
  shift
  local line
  for line in "$@"; do
    [[ -n "$line" ]] && echo "  $line"
  done
  failed=1
}

first_existing() {
  local path
  for path in "$@"; do
    if [[ -e "$path" ]]; then
      printf '%s\n' "$path"
      return 0
    fi
  done
  return 1
}

check_exists() {
  local label="$1"
  local path="$2"
  if [[ -e "$path" ]]; then
    ok "$label"
  else
    fail "$label" "missing: ${path#$target/}"
  fi
}

check_contains() {
  local label="$1"
  local file="$2"
  local pattern="$3"
  if [[ -f "$file" ]] && grep -Fq -- "$pattern" "$file"; then
    ok "$label"
  else
    fail "$label" "missing pattern: $pattern" "file: ${file#$target/}"
  fi
}

docs_dir="$(first_existing "$target/docs/foundation" "$target/docs" || true)"
if [[ -z "${docs_dir:-}" ]]; then
  fail "performance docs directory exists" "expected docs/ or docs/foundation/"
else
  check_exists "low-level performance lab doc" "$docs_dir/performance_lab.md"
  check_exists "performance practices doc" "$docs_dir/performance_practices.md"
  check_exists "GPU practices doc" "$docs_dir/gpu_practices.md"
  check_exists "Rust runtime practices doc" "$docs_dir/rust_runtime_practices.md"

  check_contains "performance lab names CPU counter taxonomy" "$docs_dir/performance_lab.md" "CPU Counter Taxonomy"
  check_contains "performance lab names Go pprof and trace" "$docs_dir/performance_lab.md" "Go pprof/trace"
  check_contains "performance lab names Rust Miri and Loom" "$docs_dir/performance_lab.md" "Rust Miri/Loom"
  check_contains "performance lab names WebGPU/WGSL" "$docs_dir/performance_lab.md" "WebGPU/WGSL"
  check_contains "performance lab names CUDA and capture tools" "$docs_dir/performance_lab.md" "CUDA/Nsight"
  check_contains "performance practices separates hard bounds from statistics" "$docs_dir/performance_practices.md" "Worst-case vs statistical performance"
  check_contains "GPU practices require capture bundle schema" "$docs_dir/gpu_practices.md" "Capture Bundle Schema"
fi

performance_script="$(first_existing \
  "$target/tooling/scripts/performance_check.sh" \
  "$target/scripts/checks/performance_check.sh" \
  "$target/foundation/tooling/scripts/performance_check.sh" || true)"
if [[ -z "${performance_script:-}" ]]; then
  fail "performance check script exists" "expected tooling/scripts/performance_check.sh or scripts/checks/performance_check.sh"
else
  check_contains "performance runner supports profile directory" "$performance_script" "PROFILE_DIR"
  check_contains "performance runner emits machine metadata" "$performance_script" "machine.json"
  check_contains "performance runner supports runtime trace capture" "$performance_script" "TRACE"
  check_contains "performance runner captures Go traces" "$performance_script" "-trace"
  check_contains "performance runner captures block profiles" "$performance_script" "-blockprofile"
  check_contains "performance runner captures mutex profiles" "$performance_script" "-mutexprofile"
  check_contains "performance runner supports hardware counter lane" "$performance_script" "PERF_COUNTERS"
fi

rust_runtime_script="$(first_existing \
  "$target/tooling/scripts/rust_runtime_practices_check.sh" \
  "$target/scripts/checks/rust_runtime_practices_check.sh" \
  "$target/foundation/tooling/scripts/rust_runtime_practices_check.sh" || true)"
if [[ -z "${rust_runtime_script:-}" ]]; then
  fail "Rust runtime practices script exists" "expected rust_runtime_practices_check.sh"
else
  check_contains "Rust runtime script exposes Miri opt-in" "$rust_runtime_script" "RUST_RUNTIME_MIRI"
  check_contains "Rust runtime script exposes Loom opt-in" "$rust_runtime_script" "RUST_RUNTIME_LOOM"
fi

if [[ "$failed" -ne 0 ]]; then
  echo "runtime performance contract check failed"
  exit 1
fi

echo "runtime performance contract check passed"
