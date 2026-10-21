#!/usr/bin/env bash
# Standalone tools must compile explicitly and stay outside application packages.
set -euo pipefail
core_root="$(cd "$(dirname "$0")/.." && pwd)"
tools_tmp="$(mktemp -d "${TMPDIR:-/tmp}/foundation-go-tools.XXXXXX")"
trap 'rm -rf "$tools_tmp"' EXIT
printf 'module test.local/standalone\n\ngo 1.26\n' > "$tools_tmp/go.mod"
for tool in benchmark_movement_check.go consolidate_go_tests.go; do
 cp "$core_root/tooling/scripts/$tool" "$tools_tmp/$tool"
 go test "$tools_tmp/$tool"
done
packages="$(cd "$tools_tmp" && GOWORK=off go list ./...)"
if [[ -n "$packages" ]]; then
 printf 'Standalone tools entered package discovery: %s\n' "$packages" >&2
 exit 1
fi
printf 'Standalone Go tool isolation passed\n'
