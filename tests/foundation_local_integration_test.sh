#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
ROOT="$(dirname "$FOUNDATION_DIR")"
PROFILE_DIR="${TMPDIR:-/tmp}/ovasabi-foundation-local-profiles"

echo "== foundation local integration: scaffold/enforcement =="
"$FOUNDATION_DIR/tests/scaffold_manifest_test.sh"
"$FOUNDATION_DIR/tests/coding_practices_check_test.sh"
"$FOUNDATION_DIR/tooling/scripts/server_kit_usage_check.sh" "$FOUNDATION_DIR"

echo "== foundation local integration: server-kit correctness =="
(
  cd "$FOUNDATION_DIR/server-kit/go"
  go test ./auth ./security ./profiling ./grpcsvc ./worker ./cache ./retry ./registry ./httpapi ./appbench
  go test -tags=perf ./grpcsvc ./chain
)

echo "== foundation local integration: runtime transport correctness =="
if [[ -d "$FOUNDATION_DIR/runtime-transport/ts/node_modules" ]]; then
  (cd "$FOUNDATION_DIR/runtime-transport/ts" && npm test)
else
  echo "skip runtime-transport TS tests: node_modules not installed"
fi

echo "== foundation local integration: runtime SDK correctness =="
if [[ -d "$FOUNDATION_DIR/runtime-sdk/ts/browser-host/node_modules" ]]; then
  (cd "$FOUNDATION_DIR/runtime-sdk/ts/browser-host" && npm run test -- --run)
else
  echo "skip runtime-sdk TS tests: node_modules not installed"
fi

echo "== foundation local integration: benchmark/profiling smoke =="
mkdir -p "$PROFILE_DIR"
(
  cd "$FOUNDATION_DIR/server-kit/go"
  go test \
    -bench='Benchmark(DispatchFrameOverBufconn|DispatchOverBufconn|DirectFrameClientDispatch|RouterDispatchFrameDirect|BinaryFrameAppendViewRoundTrip)$' \
    -benchmem \
    -run='^$' \
    -cpuprofile "$PROFILE_DIR/grpcsvc.cpu.out" \
    -memprofile "$PROFILE_DIR/grpcsvc.mem.out" \
    ./grpcsvc
  go test \
    -bench='BenchmarkRunParallel$' \
    -benchmem \
    -run='^$' \
    -cpuprofile "$PROFILE_DIR/chain.cpu.out" \
    -memprofile "$PROFILE_DIR/chain.mem.out" \
    ./chain
  go test \
    -bench='BenchmarkAppLane_' \
    -benchmem \
    -run='^$' \
    -cpuprofile "$PROFILE_DIR/appbench.cpu.out" \
    -memprofile "$PROFILE_DIR/appbench.mem.out" \
    ./appbench
)

if [[ -d "$FOUNDATION_DIR/runtime-sdk/ts/browser-host/node_modules" ]]; then
  (cd "$FOUNDATION_DIR/runtime-sdk/ts/browser-host" && npm run bench)
fi

echo "profiles written to $PROFILE_DIR"
echo "foundation local integration test passed"
