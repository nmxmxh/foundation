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
frontend_profile_script="$(first_existing \
  "$target/tooling/scripts/frontend_workbench_profile.sh" \
  "$target/scripts/checks/frontend_workbench_profile.sh" \
  "$target/foundation/tooling/scripts/frontend_workbench_profile.sh" || true)"
vitest_runner="$(first_existing \
  "$target/tooling/scripts/run_vitest.sh" \
  "$target/scripts/checks/run_vitest.sh" \
  "$target/foundation/tooling/scripts/run_vitest.sh" || true)"
benchmark_history_script="$(first_existing \
  "$target/tooling/scripts/benchmark_history.sh" \
  "$target/scripts/checks/benchmark_history.sh" \
  "$target/foundation/tooling/scripts/benchmark_history.sh" || true)"
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
  check_contains "performance runner resolves generated app module layout" "$performance_script" "foundation/server-kit/go"
  check_contains "performance runner resolves runtime-sdk module path" "$performance_script" "RUNTIME_SDK_GO"
  check_contains "performance runner resolves Vitest runner" "$performance_script" "RUN_VITEST"
  check_contains "performance runner resolves frontend-kit module path" "$performance_script" "FRONTEND_KIT_TS"
  check_contains "performance runner captures frontend workbench profile" "$performance_script" "FRONTEND_WORKBENCH_PROFILE"
fi

if [[ -z "${frontend_profile_script:-}" ]]; then
  fail "frontend workbench profile script exists" "expected tooling/scripts/frontend_workbench_profile.sh or scripts/checks/frontend_workbench_profile.sh"
else
  check_contains "frontend profile captures PROFILE metrics" "$frontend_profile_script" "PROFILE"
  check_contains "frontend profile writes benchmark results" "$frontend_profile_script" "benchmark-results"
fi

if [[ -z "${vitest_runner:-}" ]]; then
  fail "Vitest runner exists" "expected tooling/scripts/run_vitest.sh or scripts/checks/run_vitest.sh"
else
  check_contains "Vitest runner supports GC-exposed profile runs" "$vitest_runner" "FOUNDATION_VITEST_EXPOSE_GC"
fi

if [[ -z "${benchmark_history_script:-}" ]]; then
  fail "benchmark history script exists" "expected tooling/scripts/benchmark_history.sh or scripts/checks/benchmark_history.sh"
else
  check_contains "benchmark history captures frontend profile metrics" "$benchmark_history_script" "frontend-profile"
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

# Concurrency evidence gates: the lock-free projection lanes must be exercised
# under the Go data-race detector and the Rust Loom interleaving checker. These
# checks keep the gates wired so they cannot silently regress out of CI.
makefile="$(first_existing "$target/Makefile" "$target/foundation/Makefile" || true)"
if [[ -z "${makefile:-}" ]]; then
  fail "Makefile exists for concurrency gates" "expected Makefile"
else
  check_contains "Makefile defines -race test gate" "$makefile" "test-go-race:"
  check_contains "race gate runs the detector" "$makefile" "go test -race"
  check_contains "verify enforces the -race gate" "$makefile" "test test-go-race"
  check_contains "Makefile defines Loom interleaving gate" "$makefile" "test-rust-loom:"
fi

if [[ "$failed" -ne 0 ]]; then
  echo "runtime performance contract check failed"
  exit 1
fi

echo "runtime performance contract check passed"
