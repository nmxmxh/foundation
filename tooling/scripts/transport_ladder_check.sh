#!/bin/zsh
# transport_ladder_check.sh — enforce the transport ladder.
#
# docs/performance_practices.md ("Network and transport performance") defines a
# ladder: pick the lowest layer that preserves the required process boundary.
# The ladder was doctrine with no enforcement. This check gives it two forms of
# teeth:
#
#   1. Rung parity. Every rung the doc names must exist in code with a real
#      implementation and a portable fallback. A ladder whose lower rungs are
#      only names in prose cannot be chosen at runtime, and the doc would be
#      quietly lying about what the framework offers.
#   2. Rule 1 enforcement, via the AST analyzer next to this script: a
#      same-process frame handler must not reach for gRPC, HTTP, Redis, or JSON.
#
# Delegating rule 1 to a stdlib-only Go analyzer follows the same pattern as
# atomic_lane_purity_check.sh so the check behaves identically in Foundation and
# in scaffolded apps (scripts/checks/).
set -euo pipefail

target="${1:-.}"
target="$(cd "$target" && pwd)"
failed=0

ok() { echo "[OK] $1"; }
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

check_contains() {
  local label="$1" file="$2" pattern="$3"
  if [[ -f "$file" ]] && grep -Fq -- "$pattern" "$file"; then
    ok "$label"
  else
    fail "$label" "missing pattern: $pattern" "file: ${file#$target/}"
  fi
}

check_exists() {
  local label="$1" path="$2"
  if [[ -e "$path" ]]; then
    ok "$label"
  else
    fail "$label" "missing: ${path#$target/}"
  fi
}

# -- 1. The doc must still define the ladder ---------------------------------

docs_dir="$(first_existing "$target/docs/foundation" "$target/docs" || true)"
if [[ -z "${docs_dir:-}" ]]; then
  fail "performance docs directory exists" "expected docs/ or docs/foundation/"
else
  practices="$docs_dir/performance_practices.md"
  check_contains "ladder section present" "$practices" "Network and transport performance"
  check_contains "ladder rule 1 present" "$practices" "Same-process hot dispatch"
  check_contains "ladder names the same-host rungs" "$practices" "Same-host trusted native compute"
fi

# -- 2. Every rung the doc names must exist in code ---------------------------

runtimehost="$(first_existing \
  "$target/runtime-sdk/go/runtimehost" \
  "$target/foundation/runtime-sdk/go/runtimehost" || true)"
if [[ -z "${runtimehost:-}" ]]; then
  fail "runtimehost module exists" "expected runtime-sdk/go/runtimehost"
else
  pool="$runtimehost/process_pool.go"
  # ffi / shm / stdio are the same-host rungs of the ladder. If the doc offers
  # them, the runtime must actually be able to select them.
  check_contains "rung: ffi is a selectable transport"   "$pool" "ProcessTransportFFI"
  check_contains "rung: stdio is a selectable transport" "$pool" "ProcessTransportStdio"
  check_contains "rung: shm is a selectable transport"   "$pool" "ProcessTransportSharedMemory"
  # A rung without a portable fallback is a portability trap, not a rung.
  check_exists "rung: shared memory has a real implementation" "$runtimehost/shared_memory_unix.go"
  check_exists "rung: shared memory has a portable fallback"   "$runtimehost/shared_memory_unsupported.go"
fi

kernellane="$(first_existing \
  "$target/server-kit/go/kernellane" \
  "$target/foundation/server-kit/go/kernellane" || true)"
if [[ -z "${kernellane:-}" ]]; then
  fail "kernellane module exists" "expected server-kit/go/kernellane"
else
  # The kernel accelerator pattern: a real fast path, a capability probe, and a
  # behaviour-preserving fallback. Each accelerator must keep all three.
  check_exists "accelerator: kernel zero-copy fast path" "$kernellane/zerocopy_linux.go"
  check_exists "accelerator: kernel zero-copy fallback"  "$kernellane/zerocopy_other.go"
  check_contains "accelerator: zero-copy capability probe" "$kernellane/kernellane.go" "ZeroCopyFileSupported"
  check_contains "accelerator: multipath TCP rung" "$kernellane/mptcp.go" "SetMultipathTCP"
fi

# -- 3. Rule 1, enforced on the AST ------------------------------------------

script_dir="$(cd "$(dirname "$0")" && pwd)"
analyzer="$script_dir/transport_ladder_check.go"

if [[ ! -f "$analyzer" ]]; then
  fail "transport ladder analyzer exists" "missing: $analyzer"
elif ! command -v go >/dev/null 2>&1; then
  echo "[WARN] transport ladder rule 1 skipped: go toolchain unavailable"
else
  if ! go run "$analyzer" "$target"; then
    failed=1
  fi
fi

if [[ "$failed" -ne 0 ]]; then
  echo "transport ladder check failed"
  exit 1
fi

echo "transport ladder check passed"
