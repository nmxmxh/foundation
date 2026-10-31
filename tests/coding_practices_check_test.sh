#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
WORKSPACE_DIR="$(dirname "$FOUNDATION_DIR")"

(
    cd "$WORKSPACE_DIR"
    "$FOUNDATION_DIR/tooling/scripts/coding_practices_check.sh"
) >/tmp/ovasabi-coding-practices-check-test.out

if ! rg -n "coding practices check passed" /tmp/ovasabi-coding-practices-check-test.out >/dev/null; then
    cat /tmp/ovasabi-coding-practices-check-test.out >&2
    echo "expected aggregate-root coding practices check to pass for canonical foundation scope" >&2
    exit 1
fi

# Negative test: the rg-backed gates once passed without searching (pattern and
# path arguments were swapped). A planted violation must now fail them.
plant_dir="$(mktemp -d "${TMPDIR:-/tmp}/cpgate.XXXXXX")"
trap 'rm -rf "$plant_dir"' EXIT
mkdir -p "$plant_dir/pkg"
cat >"$plant_dir/pkg/plant.go" <<'GO'
package plant

func f() {
	_, _ = scratch.AllocSlice[string](a, 1)
loop:
	goto loop
}
GO
if "$FOUNDATION_DIR/tooling/scripts/coding_practices_check.sh" "$plant_dir" >/tmp/ovasabi-coding-practices-plant.out 2>&1; then
    cat /tmp/ovasabi-coding-practices-plant.out >&2
    echo "expected planted violations to fail the coding practices check" >&2
    exit 1
fi
for label in "CP no goto in handwritten sources" "CP-49 scratch arena typed allocations hold no Go pointers"; do
    if ! grep -F "[FAIL] $label" /tmp/ovasabi-coding-practices-plant.out >/dev/null; then
        cat /tmp/ovasabi-coding-practices-plant.out >&2
        echo "expected gate to fire: $label" >&2
        exit 1
    fi
done

echo "coding practices check test passed"
