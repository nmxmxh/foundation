#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
MANIFEST="$FOUNDATION_DIR/templates/scaffold.manifest.tsv"
failed=0

fail() {
    echo "[FAIL] $1" >&2
    failed=1
}

[[ -f "$MANIFEST" ]] || {
    echo "missing scaffold manifest: $MANIFEST" >&2
    exit 1
}

line_no=0
while IFS=$'\t' read -r source dest profiles feature mode extra; do
    line_no=$((line_no + 1))
    [[ -z "${source:-}" || "${source:0:1}" == "#" ]] && continue

    if [[ -n "${extra:-}" ]]; then
        fail "line $line_no has more than five tab-separated fields"
        continue
    fi

    [[ -n "${source:-}" ]] || fail "line $line_no missing source"
    [[ -n "${dest:-}" ]] || fail "line $line_no missing destination"
    [[ -n "${profiles:-}" ]] || fail "line $line_no missing profiles"
    [[ -n "${feature:-}" ]] || fail "line $line_no missing feature"
    [[ -n "${mode:-}" ]] || fail "line $line_no missing mode"

    [[ -e "$FOUNDATION_DIR/$source" ]] || fail "line $line_no source missing: $source"

    case "$profiles" in
        all|full|backend|frontend|minimal|full,backend|full,frontend) ;;
        *) fail "line $line_no invalid profiles: $profiles" ;;
    esac

    case "$feature" in
        always|docker|wasm) ;;
        *) fail "line $line_no invalid feature: $feature" ;;
    esac

    case "$mode" in
        overwrite|force|create) ;;
        *) fail "line $line_no invalid mode: $mode" ;;
    esac

    if [[ "$dest" == pkg || "$dest" == pkg/* ]]; then
        fail "line $line_no must not generate root pkg/: $dest"
    fi
done < "$MANIFEST"

if [[ "$failed" -ne 0 ]]; then
    echo "scaffold manifest test failed" >&2
    exit 1
fi

echo "scaffold manifest test passed"
