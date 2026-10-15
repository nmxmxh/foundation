#!/bin/bash
# Preserve artifact checks for files, directories, nested paths, and missing evidence.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$(mktemp -d "${TMPDIR:-/tmp}/foundation-evidence.XXXXXX")"
trap 'rm -rf "$fixture"' EXIT
mkdir -p "$fixture/docs" "$fixture/benchmark-results/capture" "$fixture/tooling/scripts" "$fixture/templates"
printf 'package fixture\nfunc BenchmarkEvidenceFixture() {}\n' > "$fixture/sample_test.go"
printf 'measured\n' > "$fixture/benchmark-results/capture/sample.txt"
printf 'measured\n' > "$fixture/benchmark-results/flat.txt"
check_case() {
    local reference="$1" expected="$2" actual=0
    printf 'Evidence: `%s`.\n' "$reference" > "$fixture/docs/foundation_benchmarks.md"
    bash "$root/tooling/scripts/benchmark_evidence_check.sh" "$fixture" > "$fixture/output.txt" 2>&1 || actual=$?
    if [[ "$actual" -ne "$expected" ]]; then
        cat "$fixture/output.txt"
        echo "artifact case failed: $reference; exit $actual, expected $expected" >&2
        exit 1
    fi
    if [[ "$expected" -ne 0 ]]; then
        grep -Fq "$reference" "$fixture/output.txt"
    fi
}
check_case benchmark-results/flat.txt 0
check_case benchmark-results/capture/ 0
check_case benchmark-results/capture/sample.txt 0
check_case benchmark-results/cap_ 1
check_case benchmark-results/missing.txt 1
check_case benchmark-results/missing/ 1
check_case benchmark-results/capture/missing.txt 1
printf 'measured\n' > "$fixture/benchmark-results/family_1.txt"
check_case benchmark-results/family_ 0
echo 'benchmark evidence fixtures passed: 8 cases'
