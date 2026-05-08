#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export GOCACHE="${GOCACHE:-/tmp/ovasabi-foundation-go-build}"

echo "== foundation Go performance guards =="
(
  cd "$ROOT/server-kit/go"
  go test -tags=perf ./grpcsvc ./chain
  go test -bench='Benchmark(DispatchOverBufconn|DispatchFrameOverBufconn|DirectFrameClientDispatch|RouterDispatchFrameDirect|BinaryFrameCodecRoundTrip|BinaryFrameAppendRoundTrip|BinaryFrameAppendViewRoundTrip|GeneratedProtoMarshalAppendRoundTrip)$|BenchmarkRunParallel$' -benchmem ./grpcsvc ./chain
  go test -bench='BenchmarkAppLane_' -benchmem -run='^$' ./appbench
  go test -bench='BenchmarkRouter' -benchmem -run='^$' ./wsrouting
  if [[ "${PROFILE:-0}" == "1" ]]; then
    mkdir -p /tmp/ovasabi-foundation-profiles
    go test -bench='Benchmark(DispatchOverBufconn|DispatchFrameOverBufconn|DirectFrameClientDispatch|RouterDispatchFrameDirect|BinaryFrameCodecRoundTrip|BinaryFrameAppendRoundTrip|BinaryFrameAppendViewRoundTrip|GeneratedProtoMarshalAppendRoundTrip)$' -benchmem \
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
  (
    cd "$ROOT/runtime-sdk/ts/browser-host"
    npm run bench -- --run
  )
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
  (
    cd "$ROOT/runtime-transport/ts"
    npm test
    npm run bench -- --run src/binaryEnvelope.bench.ts src/routing.bench.ts
  )
else
  echo "skip runtime-transport TS tests: node_modules not installed"
fi
