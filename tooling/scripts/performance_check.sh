#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export GOCACHE="${GOCACHE:-/tmp/ovasabi-foundation-go-build}"
SCALE_BENCHTIME="${SCALE_BENCHTIME:-100x}"
LATENCY_BENCHTIME="${LATENCY_BENCHTIME:-1s}"
PROFILE_DIR="${PROFILE_DIR:-/tmp/ovasabi-foundation-profiles}"
TRACE="${TRACE:-0}"
PROFILE="${PROFILE:-0}"
PERF_COUNTERS="${PERF_COUNTERS:-0}"

first_existing_dir() {
  local path
  for path in "$@"; do
    if [[ -d "$path" ]]; then
      printf '%s\n' "$path"
      return 0
    fi
  done
  return 1
}

first_existing_file() {
  local path
  for path in "$@"; do
    if [[ -f "$path" ]]; then
      printf '%s\n' "$path"
      return 0
    fi
  done
  return 1
}

SERVER_KIT_GO="$(first_existing_dir "$ROOT/server-kit/go" "$ROOT/foundation/server-kit/go" || true)"
RUNTIME_SDK_GO="$(first_existing_dir "$ROOT/runtime-sdk/go" "$ROOT/foundation/runtime-sdk/go" || true)"
RUNTIME_TRANSPORT_GO="$(first_existing_dir "$ROOT/runtime-transport/go" "$ROOT/foundation/runtime-transport/go" || true)"
RUNTIME_SDK_TS_BROWSER_HOST="$(first_existing_dir "$ROOT/runtime-sdk/ts/browser-host" "$ROOT/foundation/runtime-sdk/ts/browser-host" || true)"
RUNTIME_TRANSPORT_TS="$(first_existing_dir "$ROOT/runtime-transport/ts" "$ROOT/foundation/runtime-transport/ts" || true)"
FRONTEND_KIT_TS="$(first_existing_dir "$ROOT/frontend-kit/ts" "$ROOT/foundation/frontend-kit/ts" || true)"
RUNTIME_SDK_RUST="$(first_existing_dir "$ROOT/runtime-sdk/rust" "$ROOT/foundation/runtime-sdk/rust" || true)"
RUN_VITEST="$(first_existing_file "$ROOT/tooling/scripts/run_vitest.sh" "$ROOT/scripts/checks/run_vitest.sh" "$ROOT/foundation/tooling/scripts/run_vitest.sh" || true)"
FRONTEND_WORKBENCH_PROFILE="$(first_existing_file "$ROOT/tooling/scripts/frontend_workbench_profile.sh" "$ROOT/scripts/checks/frontend_workbench_profile.sh" "$ROOT/foundation/tooling/scripts/frontend_workbench_profile.sh" || true)"

require_dir() {
  local label="$1"
  local path="$2"
  if [[ -z "$path" ]]; then
    echo "missing required module: $label" >&2
    exit 1
  fi
}

json_escape() {
  sed 's/\\/\\\\/g; s/"/\\"/g' <<<"$1"
}

tool_version() {
  local tool="$1"
  shift
  if command -v "$tool" >/dev/null 2>&1; then
    "$tool" "$@" 2>/dev/null | head -1
  else
    echo ""
  fi
}

emit_machine_metadata() {
  mkdir -p "$PROFILE_DIR"
  cat >"$PROFILE_DIR/machine.json" <<JSON
{
  "schema_version": "foundation.performance_machine.v1",
  "captured_at": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")",
  "uname": "$(json_escape "$(uname -a)")",
  "go_version": "$(json_escape "$(tool_version go version)")",
  "rustc_version": "$(json_escape "$(tool_version rustc --version)")",
  "cargo_version": "$(json_escape "$(tool_version cargo --version)")",
  "node_version": "$(json_escape "$(tool_version node --version)")",
  "profile_enabled": "${PROFILE}",
  "trace_enabled": "${TRACE}",
  "hardware_counters_enabled": "${PERF_COUNTERS}"
}
JSON
}

run_hardware_counter_smoke() {
  [[ "$PERF_COUNTERS" == "1" ]] || return 0
  emit_machine_metadata

  if command -v perf >/dev/null 2>&1; then
    (
      require_dir "server-kit/go" "$SERVER_KIT_GO"
      cd "$SERVER_KIT_GO"
      perf stat -x, \
        -e cycles,instructions,cache-references,cache-misses,branches,branch-misses,page-faults,context-switches \
        -o "$PROFILE_DIR/go-appbench-perf-stat.csv" \
        go test -bench='BenchmarkScale_LocalOperationMixLatency$' -benchmem -benchtime="$LATENCY_BENCHTIME" -run='^$' ./appbench
    )
  elif command -v xctrace >/dev/null 2>&1; then
    echo "skip hardware counter smoke: xctrace is available, but counter capture requires an explicit Instruments template and signed target"
  else
    echo "skip hardware counter smoke: set PERF_COUNTERS=1 on Linux with perf, or capture Intel VTune/AMD uProf/Instruments externally"
  fi
}

emit_machine_metadata

echo "== foundation Go performance guards =="
(
  require_dir "server-kit/go" "$SERVER_KIT_GO"
  cd "$SERVER_KIT_GO"
  go test -tags=perf ./grpcsvc ./chain
  go test -bench='Benchmark(DispatchOverBufconn|DispatchFrameOverBufconn|ClientDispatchFrameOverBufconn|RouterDispatchFrameDirect|DirectFrameClientDispatch|BoundFrameClientDispatch|BoundFrameClientDispatchTrusted|BinaryFrameCodecRoundTrip|BinaryFrameAppendRoundTrip|BinaryFrameAppendViewRoundTrip|GeneratedProtoMarshalAppendRoundTrip|GeneratedProtoUnmarshalReset|GeneratedProtoUnmarshalMergeReuse)$|BenchmarkRunParallel(Into)?$' -benchmem ./grpcsvc ./chain
  go test -bench='BenchmarkDecodeRequestBytesIntoCompleteReuse$' -benchmem -run='^$' ./protoapi
  go test -bench='BenchmarkTypedFrameAdapterDispatch(NoMetadata|Reuse)?$' -benchmem -run='^$' ./bootstrap
  go test -bench='BenchmarkAppLane_' -benchmem -run='^$' ./appbench
  go test -bench='BenchmarkScale1M_|BenchmarkScale_(MemoryDB|WebSocket|Event|Config)' -benchmem -benchtime="$SCALE_BENCHTIME" -run='^$' ./appbench
  go test -bench='BenchmarkScale_LocalOperationMixLatency$' -benchmem -benchtime="$LATENCY_BENCHTIME" -run='^$' ./appbench
  go test -bench='Benchmark' -benchmem -run='^$' ./cache ./circuitbreaker ./compress ./events ./metrics ./redis ./retry ./worker
  go test -bench='Benchmark(MemoryDB|Query|Exec)' -benchmem -run='^$' ./database
  go test -bench='BenchmarkRouter' -benchmem -run='^$' ./wsrouting
  go test -bench='Benchmark(TLSHandshake|ApplyPostQuantumTLS)' -benchmem -run='^$' ./security
  if [[ -n "${SERVICE_BACKED_DATABASE_URL:-}" && -n "${SERVICE_BACKED_REDIS_URL:-}" ]]; then
    go test -bench='BenchmarkServiceBacked' -benchmem -run='^$' ./servicebacked
  else
    echo "skip service-backed Redis/Postgres benchmarks: SERVICE_BACKED_DATABASE_URL and SERVICE_BACKED_REDIS_URL are not set"
  fi
  if [[ "${PROFILE:-0}" == "1" ]]; then
    mkdir -p "$PROFILE_DIR"
    go test -bench='Benchmark(DispatchOverBufconn|DispatchFrameOverBufconn|DirectFrameClientDispatch|RouterDispatchFrameDirect|BinaryFrameCodecRoundTrip|BinaryFrameAppendRoundTrip|BinaryFrameAppendViewRoundTrip|GeneratedProtoMarshalAppendRoundTrip|GeneratedProtoUnmarshalReset|GeneratedProtoUnmarshalMergeReuse)$' -benchmem \
      -cpuprofile "$PROFILE_DIR/grpcsvc.cpu.out" \
      -memprofile "$PROFILE_DIR/grpcsvc.mem.out" \
      ./grpcsvc
    go test -bench='BenchmarkRunParallel$' -benchmem \
      -cpuprofile "$PROFILE_DIR/chain.cpu.out" \
      -memprofile "$PROFILE_DIR/chain.mem.out" \
      ./chain
    go test -bench='BenchmarkAppLane_' -benchmem -run='^$' \
      -cpuprofile "$PROFILE_DIR/appbench.cpu.out" \
      -memprofile "$PROFILE_DIR/appbench.mem.out" \
      ./appbench
    echo "profiles written to $PROFILE_DIR"
  fi
  if [[ "${TRACE:-0}" == "1" ]]; then
    mkdir -p "$PROFILE_DIR"
    go test -bench='BenchmarkScale_LocalOperationMixLatency$' -benchmem -benchtime="$LATENCY_BENCHTIME" -run='^$' \
      -trace "$PROFILE_DIR/appbench.trace.out" \
      -blockprofile "$PROFILE_DIR/appbench.block.out" \
      -mutexprofile "$PROFILE_DIR/appbench.mutex.out" \
      ./appbench
    go test -bench='BenchmarkRouter' -benchmem -run='^$' \
      -trace "$PROFILE_DIR/wsrouting.trace.out" \
      -blockprofile "$PROFILE_DIR/wsrouting.block.out" \
      -mutexprofile "$PROFILE_DIR/wsrouting.mutex.out" \
      ./wsrouting
    echo "Go traces and blocking profiles written to $PROFILE_DIR"
  fi
)
run_hardware_counter_smoke

echo "== foundation runtime-sdk Go benchmarks =="
(
  require_dir "runtime-sdk/go" "$RUNTIME_SDK_GO"
  cd "$RUNTIME_SDK_GO"
  go test -bench='BenchmarkBuffer' -benchmem -run='^$' ./runtimehost
)

echo "== foundation runtime-transport Go benchmarks =="
(
  require_dir "runtime-transport/go" "$RUNTIME_TRANSPORT_GO"
  cd "$RUNTIME_TRANSPORT_GO"
  go test -bench='Benchmark' -benchmem -run='^$' ./transport
)

if [[ -n "$RUNTIME_SDK_TS_BROWSER_HOST" && -n "$RUN_VITEST" && -d "$RUNTIME_SDK_TS_BROWSER_HOST/node_modules" ]]; then
	echo "== foundation runtime-sdk browser-host benchmarks =="
	"$RUN_VITEST" "$RUNTIME_SDK_TS_BROWSER_HOST" bench --run
else
	echo "skip runtime-sdk TS benchmarks: node_modules not installed"
fi

if [[ -n "$FRONTEND_KIT_TS" && -n "$RUN_VITEST" && -d "$FRONTEND_KIT_TS/node_modules" ]]; then
	echo "== foundation frontend-kit workbench benchmarks =="
	"$RUN_VITEST" "$FRONTEND_KIT_TS" bench --run src/runtimeWorkbench.bench.ts
else
	echo "skip frontend-kit workbench benchmarks: node_modules not installed"
fi

if [[ -n "$FRONTEND_WORKBENCH_PROFILE" ]]; then
	echo "== foundation frontend-kit workbench allocation profile =="
	"$FRONTEND_WORKBENCH_PROFILE" "$ROOT"
else
	echo "skip frontend workbench profile: script not found"
fi

if command -v cargo >/dev/null 2>&1; then
  echo "== foundation runtime-sdk Rust native buffer benchmarks =="
  if [[ -n "$RUNTIME_SDK_RUST" ]]; then
    (
      cd "$RUNTIME_SDK_RUST"
      cargo run -p ovrt-native --bin buffer_bench --release
    )
  else
    echo "skip runtime-sdk Rust benchmarks: runtime-sdk/rust module not found"
  fi
else
  echo "skip runtime-sdk Rust benchmarks: cargo not installed"
fi

if [[ -n "$RUNTIME_TRANSPORT_TS" && -n "$RUN_VITEST" && -d "$RUNTIME_TRANSPORT_TS/node_modules" ]]; then
	echo "== foundation runtime-transport TS tests =="
	"$RUN_VITEST" "$RUNTIME_TRANSPORT_TS" run
	"$RUN_VITEST" "$RUNTIME_TRANSPORT_TS" bench --run src/binaryEnvelope.bench.ts src/routing.bench.ts
else
	echo "skip runtime-transport TS tests: node_modules not installed"
fi
