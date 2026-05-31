#!/bin/bash

test_root() {
    if [[ -n "${ASSERT_ROOT:-}" ]]; then
        printf '%s\n' "$ASSERT_ROOT"
        return
    fi
    if [[ -n "${PROJECT_DIR:-}" ]]; then
        printf '%s\n' "$PROJECT_DIR"
        return
    fi
    pwd
}

assert_file() {
    local path="$1"
    local root
    root="$(test_root)"
    if [[ ! -e "$root/$path" ]]; then
        echo "missing expected file: $path" >&2
        exit 1
    fi
}

assert_absent() {
    local path="$1"
    local root
    root="$(test_root)"
    if [[ -e "$root/$path" ]]; then
        echo "unexpected file or directory: $path" >&2
        exit 1
    fi
}

assert_contains() {
    local path="$1"
    local pattern="$2"
    local root
    root="$(test_root)"
    if ! rg -n "$pattern" "$root/$path" >/dev/null 2>&1; then
        echo "expected $path to contain: $pattern" >&2
        exit 1
    fi
}

assert_not_contains() {
    local path="$1"
    local pattern="$2"
    local root
    root="$(test_root)"
    if rg -n "$pattern" "$root/$path" >/dev/null 2>&1; then
        echo "expected $path not to contain: $pattern" >&2
        exit 1
    fi
}
