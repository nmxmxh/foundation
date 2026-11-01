#!/bin/bash
# Managed-patch contract for patch_static_precompression.
#
#   1. Convergence. A project scaffolded before the change gets the precompress
#      step right after its frontend build and gzip_static next to gzip, like a
#      fresh scaffold.
#   2. Idempotence. A second run changes nothing.
#   3. Guard. A Dockerfile that does not copy frontend-kit source gets no step,
#      because the script would be missing from the image and the build would fail.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
PATCHER="$FOUNDATION_DIR/tooling/scripts/scaffold_managed_patches.sh"
TEMPLATE_DOCKERFILE="$FOUNDATION_DIR/templates/docker/Dockerfile"
TEMPLATE_NGINX="$FOUNDATION_DIR/templates/config/nginx.conf"
STEP='RUN node foundation/frontend-kit/ts/scripts/precompress-static.mjs frontend/dist'
failed=0

pass() { echo "[OK] $1"; }
fail() {
    echo "[FAIL] $1" >&2
    shift
    for d in "$@"; do echo "  $d" >&2; done
    failed=1
}

WORK="$(mktemp -d "${TMPDIR:-/tmp}/static-precompress-patch-test.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

run_patch() { bash "$PATCHER" "$1" --only static_precompression 2>&1; }

# strip_change removes the precompress step, its comments, and gzip_static: the shape
# foundation shipped before this change.
strip_change() {
    python3 - "$1" "$2" <<'PY'
import sys
src, dst = sys.argv[1], sys.argv[2]
out = []
for line in open(src).read().split("\n"):
    s = line.strip()
    if "precompress-static.mjs" in s or s.startswith("gzip_static"):
        continue
    if s.startswith("# Precompress the built assets") or s.startswith("# and gzip_static instead") \
       or s.startswith("# percent smaller than on-the-fly") or s.startswith("# Serve the .gz copies") \
       or s.startswith("# accept only gzip then cost"):
        continue
    out.append(line)
open(dst, "w").write("\n".join(out))
PY
}

# step_follows_build prints 1 when the precompress step is the next instruction after the build.
step_follows_build() {
    python3 - "$1" "$STEP" <<'PY'
import re, sys
lines = open(sys.argv[1]).read().split("\n")
builds = [i for i, l in enumerate(lines) if re.match(r"^RUN cd frontend && .*npm run build\s*$", l)]
steps = [i for i, l in enumerate(lines) if l.strip() == sys.argv[2]]
ok = len(builds) == 1 and len(steps) == 1 and all(
    l.strip() == "" or l.lstrip().startswith("#") for l in lines[builds[0] + 1:steps[0]])
print(1 if ok else 0)
PY
}

seed() {
    local dir="$1"
    mkdir -p "$dir/config"
    strip_change "$TEMPLATE_DOCKERFILE" "$dir/Dockerfile"
    strip_change "$TEMPLATE_NGINX" "$dir/config/nginx.conf"
}

legacy="$WORK/legacy"
seed "$legacy"
if grep -Fq 'precompress-static' "$legacy/Dockerfile" || grep -q 'gzip_static' "$legacy/config/nginx.conf"; then
    fail "legacy fixture" "the derived legacy files still carry the change"
fi

out="$(run_patch "$legacy")"
[[ "$(step_follows_build "$legacy/Dockerfile")" == "1" ]] && pass "legacy Dockerfile gets the step right after the build" \
    || fail "legacy Dockerfile" "step missing, duplicated, or not after the build" "$out"
[[ "$(grep -c '^[[:space:]]*gzip_static on;' "$legacy/config/nginx.conf")" == "1" ]] && pass "legacy nginx gets one gzip_static" \
    || fail "legacy nginx" "gzip_static count is $(grep -c gzip_static "$legacy/config/nginx.conf")" "$out"
if grep -A3 '^[[:space:]]*gzip on;' "$legacy/config/nginx.conf" | grep -q 'gzip_static on;'; then
    pass "gzip_static sits next to gzip on"
else
    fail "nginx placement" "gzip_static is not beside gzip on"
fi

cp "$legacy/Dockerfile" "$WORK/Dockerfile.after"
cp "$legacy/config/nginx.conf" "$WORK/nginx.after"
second="$(run_patch "$legacy")"
if cmp -s "$WORK/Dockerfile.after" "$legacy/Dockerfile" && cmp -s "$WORK/nginx.after" "$legacy/config/nginx.conf" \
    && grep -q 'no selected patch drift' <<<"$second"; then
    pass "second run is a no-op"
else
    fail "idempotence" "second run changed a file or reported drift" "$second"
fi

fresh="$WORK/fresh"
mkdir -p "$fresh/config"
cp "$TEMPLATE_DOCKERFILE" "$fresh/Dockerfile"
cp "$TEMPLATE_NGINX" "$fresh/config/nginx.conf"
if grep -q 'no selected patch drift' <<<"$(run_patch "$fresh")" && cmp -s "$TEMPLATE_DOCKERFILE" "$fresh/Dockerfile"; then
    pass "fresh scaffold needs no patch"
else
    fail "fresh scaffold" "patch changed a fresh template"
fi

# A project that wraps its build in a timeout still gets the step after it.
wrapped="$WORK/wrapped"
seed "$wrapped"
perl -0pi -e 's/^RUN cd frontend && [^\n]*npm run build\n/RUN cd frontend && timeout -k 10s 300s npm run build\n/m' "$wrapped/Dockerfile"
run_patch "$wrapped" >/dev/null
[[ "$(step_follows_build "$wrapped/Dockerfile")" == "1" ]] && pass "a wrapped build line gets the step" \
    || fail "wrapped build" "step not placed after a timeout-wrapped build"

# Guard: without frontend-kit source in the image, the step would break the build.
guarded="$WORK/guarded"
seed "$guarded"
sed -i.bak '/^COPY foundation\/frontend-kit\/ts \.\/foundation\/frontend-kit\/ts/d' "$guarded/Dockerfile"
run_patch "$guarded" >/dev/null
if ! grep -Fq 'precompress-static' "$guarded/Dockerfile"; then
    pass "no step when the build does not copy frontend-kit source"
else
    fail "guard" "step added to a Dockerfile that cannot run it"
fi

exit "$failed"
