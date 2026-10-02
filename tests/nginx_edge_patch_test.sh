#!/bin/bash
# Managed-patch contract for the nginx edge transport change.
#
# Two properties matter and neither is visible from the patch source:
#
#   1. Idempotence. A patch whose search string also occurs inside its own
#      replacement re-matches on every run and appends another copy, so a
#      project's nginx config grows on each foundation-update. replace_in_file
#      compares before and after, which catches a no-op but not this.
#
#   2. Convergence. A patched project must end up with the same directives as a
#      freshly scaffolded one, or the two drift apart silently.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
PATCHER="$FOUNDATION_DIR/tooling/scripts/scaffold_managed_patches.sh"
failed=0

pass() { echo "[OK] $1"; }
fail() {
    echo "[FAIL] $1" >&2
    shift
    for d in "$@"; do echo "  $d" >&2; done
    failed=1
}

[[ -f "$PATCHER" ]] || {
    echo "missing patcher: $PATCHER" >&2
    exit 1
}

WORK="$(mktemp -d "${TMPDIR:-/tmp}/nginx-patch-test.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

# A project in the shape foundation shipped before this change.
seed_legacy_project() {
    local dir="$1"
    mkdir -p "$dir/config"
    git -C "$FOUNDATION_DIR" show HEAD:templates/config/nginx.conf >"$dir/config/nginx.conf"
    git -C "$FOUNDATION_DIR" show HEAD:templates/config/default.conf.template >"$dir/config/default.conf.template"
    git -C "$FOUNDATION_DIR" show HEAD:templates/docker/start.sh >"$dir/start.sh"
    chmod +x "$dir/start.sh"
}

run_patch() {
    bash "$PATCHER" "$1" --only nginx_edge_transport 2>&1
}

# Prints the distinct directives of an nginx file, ignoring comments and order.
directive_set() {
    python3 - "$1" <<'PY'
import re, sys
text = re.sub(r"#.*", "", open(sys.argv[1]).read())
out = set()
for line in text.split("\n"):
    t = line.strip()
    if not t or t == "{" or t == "}":
        continue
    t = re.sub(r";$", "", t).strip()
    if t:
        out.add(re.sub(r"\s+", " ", t))
for t in sorted(out):
    print(t)
PY
}

seed_legacy_project "$WORK/project"
if grep -Fq 'upstream foundation_backend' "$WORK/project/config/default.conf.template"; then
    fail "fixture is not legacy: HEAD already carries the keepalive pool"
fi

first="$(run_patch "$WORK/project")"
[[ -n "$first" ]] || fail "first run reported no work"

# The patcher prints "[PATCH] no selected patch drift found" when a run changed
# nothing. Any other output means the patch re-applied to its own output.
readonly NO_DRIFT='[PATCH] no selected patch drift found'

second="$(run_patch "$WORK/project")"
if [[ "$second" != "$NO_DRIFT" ]]; then
    fail "second run is not idempotent" "output: $second"
else
    pass "second run is a no-op"
fi

third="$(run_patch "$WORK/project")"
[[ "$third" == "$NO_DRIFT" ]] || fail "third run drifted: $third"

# The patch must not grow a file across runs.
for key in proxy_max_temp_file_size limit_conn_zone gzip_min_length brotli_min_length \
    client_body_buffer_size brotli_dynamic gzip_disable; do
    count="$(grep -c -- "$key" "$WORK/project/config/nginx.conf" || true)"
    [[ "$count" -le 1 ]] || fail "directive duplicated in nginx.conf: $key" "count=$count"
done
count="$(grep -c -- 'upstream foundation_backend' "$WORK/project/config/default.conf.template" || true)"
[[ "$count" -eq 1 ]] || fail "upstream pool duplicated" "count=$count"
pass "no directive is duplicated after repeated runs"

# A patched project must match a freshly scaffolded one.
diff_directives() {
    local a="$1" b="$2" label="$3"
    local only_a only_b
    only_a="$(comm -23 <(directive_set "$a") <(directive_set "$b"))"
    only_b="$(comm -13 <(directive_set "$a") <(directive_set "$b"))"
    if [[ -n "$only_a" || -n "$only_b" ]]; then
        fail "$label directives diverge from the template" \
            "only in patched: ${only_a:-<none>}" \
            "only in template: ${only_b:-<none>}"
    else
        pass "$label matches the template directive set"
    fi
}

diff_directives "$WORK/project/config/nginx.conf" "$FOUNDATION_DIR/templates/config/nginx.conf" "nginx.conf"
diff_directives "$WORK/project/config/default.conf.template" \
    "$FOUNDATION_DIR/templates/config/default.conf.template" "default.conf.template"

# The rendered config must parse: balanced braces and no unterminated directive.
# nginx -t is unavailable here, so this catches the failure modes a text edit
# can introduce: a lost newline joining two blocks, or a dropped semicolon.
check_structure() {
    local file="$1" label="$2"
    python3 - "$file" <<'PY' || fail "$label is not structurally valid"
import re, sys
text = re.sub(r"#.*", "", open(sys.argv[1]).read())
depth = 0
low = 0
for ch in text:
    if ch == "{":
        depth += 1
    elif ch == "}":
        depth -= 1
        low = min(low, depth)
if depth != 0 or low < 0:
    sys.exit(1)
for line in text.split("\n"):
    t = line.strip()
    if t and not t.endswith((";", "{", "}")):
        sys.exit(1)
PY
    pass "$label is structurally valid"
}

# Render the server template the way start.sh does, then validate it.
export UPSTREAM_HOST=server UPSTREAM_PORT=8080 UPSTREAM_KEEPALIVE_CONNECTIONS=64 \
    UPSTREAM_KEEPALIVE_REQUESTS=1000 UPSTREAM_KEEPALIVE_TIMEOUT=60s
envsubst '${UPSTREAM_HOST} ${UPSTREAM_PORT} ${UPSTREAM_KEEPALIVE_CONNECTIONS} ${UPSTREAM_KEEPALIVE_REQUESTS} ${UPSTREAM_KEEPALIVE_TIMEOUT}' \
    <"$WORK/project/config/default.conf.template" >"$WORK/project/rendered.conf"

if grep -Fq '${' "$WORK/project/rendered.conf"; then
    fail "rendered config still carries an unsubstituted variable" \
        "$(grep -o '\${[A-Z_]*}' "$WORK/project/rendered.conf" | sort -u | tr '\n' ' ')"
else
    pass "every template variable is substituted"
fi

# An empty keepalive value would silently disable the pool, which is the whole
# point of the change.
if grep -Eq 'keepalive +0;|keepalive +;' "$WORK/project/rendered.conf"; then
    fail "rendered keepalive pool is empty"
else
    pass "rendered keepalive pool has a size"
fi

# Every variable the server template references must be substituted by start.sh.
missing=""
for var in $(grep -o '\${[A-Z_][A-Z_]*}' "$WORK/project/config/default.conf.template" | tr -d '${}' | sort -u); do
    grep -Fq "$var" "$WORK/project/start.sh" || missing="$missing $var"
done
[[ -z "$missing" ]] || fail "start.sh never exports or substitutes:$missing"
pass "start.sh covers every template variable"

check_structure "$WORK/project/rendered.conf" "rendered default.conf"
check_structure "$WORK/project/config/nginx.conf" "patched nginx.conf"

# The global config must carry no ${VAR}: it is not rendered by start.sh, so a
# variable there would reach nginx verbatim and the container would not start.
if grep -Fq '${' "$WORK/project/config/nginx.conf"; then
    fail "global nginx.conf carries an unsubstituted variable" \
        "$(grep -o '\${[A-Z_]*}' "$WORK/project/config/nginx.conf" | sort -u | tr '\n' ' ')"
else
    pass "global nginx.conf needs no substitution"
fi

if [[ "$failed" -ne 0 ]]; then
    echo "nginx managed-patch test failed" >&2
    exit 1
fi
echo "nginx managed-patch test passed"
