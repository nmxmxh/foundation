#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export GOCACHE="${GOCACHE:-/tmp/ovasabi-foundation-go-build}"
SCALE_BENCHTIME="${SCALE_BENCHTIME:-100x}"
LATENCY_BENCHTIME="${LATENCY_BENCHTIME:-1s}"

echo "== foundation Go performance guards =="
(
  cd "$ROOT/server-kit/go"
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
    mkdir -p /tmp/ovasabi-foundation-profiles
    go test -bench='Benchmark(DispatchOverBufconn|DispatchFrameOverBufconn|DirectFrameClientDispatch|RouterDispatchFrameDirect|BinaryFrameCodecRoundTrip|BinaryFrameAppendRoundTrip|BinaryFrameAppendViewRoundTrip|GeneratedProtoMarshalAppendRoundTrip|GeneratedProtoUnmarshalReset|GeneratedProtoUnmarshalMergeReuse)$' -benchmem \
      -cpuprofile /tmp/ovasabi-foundation-profiles/grpcsvc.cpu.out \
      -memprofile /tmp/ovasabi-foundation-profiles/grpcsvc.mem.out \
      ./grpcsvc
    go test -bench='BenchmarkRunParallel$' -benchmem \
      -cpuprofile /tmp/ovasabi-foundation-profiles/chain.cpu.out \
      -memprofile /tmp/ovasabi-foundation-profiles/chain.mem.out \
      ./chain
    go test -bench='BenchmarkAppLane_' -benchmem -run='^$' \
      -cpuprofile /tmp/ovasabi-foundation-profiles/appbench.cpu.out \
      -memprofile /tmp/ovasabi-foundation-profiles/appbench.mem.out \
      ./appbench
    echo "profiles written to /tmp/ovasabi-foundation-profiles"
  fi
)

echo "== foundation runtime-sdk Go benchmarks =="
(
  cd "$ROOT/runtime-sdk/go"
  go test -bench='BenchmarkBuffer' -benchmem -run='^$' ./runtimehost
)

echo "== foundation runtime-transport Go benchmarks =="
(
  cd "$ROOT/runtime-transport/go"
  go test -bench='Benchmark' -benchmem -run='^$' ./transport
)

if [[ -d "$ROOT/runtime-sdk/ts/browser-host/node_modules" ]]; then
	echo "== foundation runtime-sdk browser-host benchmarks =="
	"$ROOT/tooling/scripts/run_vitest.sh" "$ROOT/runtime-sdk/ts/browser-host" bench --run
else
	echo "skip runtime-sdk TS benchmarks: node_modules not installed"
fi

if command -v cargo >/dev/null 2>&1; then
  echo "== foundation runtime-sdk Rust native buffer benchmarks =="
  (
    cd "$ROOT/runtime-sdk/rust"
    cargo run -p ovrt-native --bin buffer_bench --release
  )
else
  echo "skip runtime-sdk Rust benchmarks: cargo not installed"
fi

if [[ -d "$ROOT/runtime-transport/ts/node_modules" ]]; then
	echo "== foundation runtime-transport TS tests =="
	"$ROOT/tooling/scripts/run_vitest.sh" "$ROOT/runtime-transport/ts" run
	"$ROOT/tooling/scripts/run_vitest.sh" "$ROOT/runtime-transport/ts" bench --run src/binaryEnvelope.bench.ts src/routing.bench.ts
else
	echo "skip runtime-transport TS tests: node_modules not installed"
fi
