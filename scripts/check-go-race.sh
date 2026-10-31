#!/bin/bash
set -euo pipefail

# Fast, bounded race detector gate for memory-sensitive and concurrent packages.
# Packages are chosen for shared state, pooling, atomics, or arenas. -short keeps
# the expensive fixtures (for example the full-size decompression bomb) in make test.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}/server-kit/go"

echo "Running targeted Go race detector gate on memory-sensitive packages..."
go test -race -count=1 -short \
  ./auth \
  ./cache \
  ./circuitbreaker \
  ./compress \
  ./database \
  ./events \
  ./featureflags \
  ./hermes \
  ./httpserver/middleware \
  ./metadata \
  ./projectiongw \
  ./scratch \
  ./security \
  ./vectorstr \
  ./worker \
  ./wsrouting

echo "Targeted Go race detector check passed."
