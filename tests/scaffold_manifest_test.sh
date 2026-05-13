#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
MANIFEST="$FOUNDATION_DIR/templates/scaffold.manifest.tsv"
failed=0
shopt -s nocasematch

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
        always|docker|wasm|native) ;;
        *) fail "line $line_no invalid feature: $feature" ;;
    esac

    case "$mode" in
        overwrite|force|create) ;;
        *) fail "line $line_no invalid mode: $mode" ;;
    esac

    if [[ "$dest" == pkg || "$dest" == pkg/* ]]; then
        fail "line $line_no must not generate root pkg/: $dest"
    fi

    case "$source $dest" in
        *service-backed*|*service_backed*|*servicebacked*)
            fail "line $line_no must not scaffold service-backed core test assets: $source -> $dest"
            ;;
    esac
done < "$MANIFEST"

if find "$FOUNDATION_DIR/tooling/scripts" -maxdepth 1 -type f -iname '*service*backed*' | rg . >/dev/null; then
    fail "service-backed benchmark scripts must not live in tooling/scripts because scaffold copies that directory into project scripts/checks"
fi

if find "$FOUNDATION_DIR/templates" -type f -iname '*service*backed*' | rg . >/dev/null; then
    fail "service-backed core test assets must not live under templates because scaffolded projects only inherit project runtime files"
fi

if [[ "$failed" -ne 0 ]]; then
    echo "scaffold manifest test failed" >&2
    exit 1
fi

echo "scaffold manifest test passed"
