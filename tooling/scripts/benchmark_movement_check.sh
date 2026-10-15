#!/bin/bash
# Run separate accounting builds after ordinary allocation measurements.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
target="$(cd "${1:-.}" && pwd)"
source "$script_dir/command_timeout.sh"
export GOCACHE="${FOUNDATION_GO_CACHE_DIR:-/tmp/ovasabi-foundation-go-build}"
run_with_timeout 600 go run "$script_dir/benchmark_movement_check.go" \
  "${2:-check}" "$target" "$script_dir/command_timeout.sh"
