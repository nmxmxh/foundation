#!/bin/bash
set -euo pipefail

# Fast, bounded race detector gate for core memory and performance packages.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}/server-kit/go"

echo "Running targeted Go race detector gate on memory-sensitive packages..."
go test -race -count=1 -short \
  ./events \
  ./vectorstr \
  ./scratch \
  ./wsrouting \
  ./security \
  ./database \
  ./appbench

echo "Targeted Go race detector check passed."
