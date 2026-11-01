#!/bin/bash
# Managed-patch contract for patch_compose_tcp_idle_restart.
#
#   1. Convergence. A project scaffolded before the change ends up with the same
#      sysctl on its frontend and server services as a fresh scaffold.
#   2. Idempotence. A second run changes nothing.
#   3. Merge. A service that already has a sysctls map or list gets one entry in
#      it, never a second sysctls key, and its other entries survive.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(dirname "$SCRIPT_DIR")"
PATCHER="$FOUNDATION_DIR/tooling/scripts/scaffold_managed_patches.sh"
TEMPLATE="$FOUNDATION_DIR/templates/docker/docker-compose.yml"
failed=0

pass() { echo "[OK] $1"; }
fail() {
    echo "[FAIL] $1" >&2
    shift
    for d in "$@"; do echo "  $d" >&2; done
    failed=1
}

WORK="$(mktemp -d "${TMPDIR:-/tmp}/compose-tcp-patch-test.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

run_patch() { bash "$PATCHER" "$1" --only compose_tcp_idle_restart 2>&1; }

# sysctl_of <compose> <service> prints that service's tcp_slow_start_after_idle and sysctls count.
sysctl_of() {
    python3 - "$1" "$2" <<'PY'
import re, sys, yaml
text = open(sys.argv[1]).read().replace("{{PROJECT_NAME}}", "app")
doc = yaml.safe_load(text)
svc = doc["services"][sys.argv[2]]
raw = open(sys.argv[1]).read()
block = re.search(r"\n  %s:\n(.*?)(?=\n  \S|\Z)" % re.escape(sys.argv[2]), raw, re.S).group(1)
sysctls = svc.get("sysctls") or {}
if isinstance(sysctls, list):
    sysctls = dict(item.split("=", 1) for item in sysctls)
print(sysctls.get("net.ipv4.tcp_slow_start_after_idle"), block.count("sysctls:"), len(sysctls))
PY
}

# A project in the shape foundation shipped before the change.
legacy="$WORK/legacy"
mkdir -p "$legacy"
python3 - "$TEMPLATE" "$legacy/docker-compose.yml" <<'PY'
import sys
lines = open(sys.argv[1]).read().split("\n")
out, skip = [], 0
for line in lines:
    if "tcp_slow_start_after_idle" in line or line.strip() == "sysctls:":
        continue
    if line.lstrip().startswith("# Browser TCP connections end") or line.lstrip().startswith("# WebSocket gaps") \
       or line.lstrip().startswith("# (docs/foundation") or line.lstrip().startswith("# Same TCP setting"):
        continue
    out.append(line)
open(sys.argv[2], "w").write("\n".join(out))
PY

if grep -q tcp_slow_start_after_idle "$legacy/docker-compose.yml"; then
    fail "legacy fixture" "the derived legacy compose still carries the sysctl"
fi

out="$(run_patch "$legacy")"
for svc in frontend server; do
    got="$(sysctl_of "$legacy/docker-compose.yml" "$svc")"
    if [[ "$got" == "0 1 1" ]]; then
        pass "legacy $svc converges to tcp_slow_start_after_idle 0"
    else
        fail "legacy $svc convergence" "got (value, sysctls keys, entries) = $got" "$out"
    fi
done
for svc in frontend server; do
    want="$(sysctl_of "$TEMPLATE" "$svc")"
    got="$(sysctl_of "$legacy/docker-compose.yml" "$svc")"
    [[ "$want" == "$got" ]] && pass "patched $svc matches a fresh scaffold" || fail "patched $svc vs template" "template $want, patched $got"
done
if [[ "$(sysctl_of "$legacy/docker-compose.yml" app-postgres)" == "None 0 0" ]]; then
    pass "unrelated services are untouched"
else
    fail "unrelated services" "app-postgres gained sysctls"
fi

snapshot="$WORK/after-first"
cp "$legacy/docker-compose.yml" "$snapshot"
second="$(run_patch "$legacy")"
if cmp -s "$snapshot" "$legacy/docker-compose.yml" && grep -q 'no selected patch drift' <<<"$second"; then
    pass "second run is a no-op"
else
    fail "idempotence" "second run changed the file or reported drift" "$second"
fi

fresh="$WORK/fresh"
mkdir -p "$fresh"
cp "$TEMPLATE" "$fresh/docker-compose.yml"
if grep -q 'no selected patch drift' <<<"$(run_patch "$fresh")" && cmp -s "$TEMPLATE" "$fresh/docker-compose.yml"; then
    pass "fresh scaffold needs no patch"
else
    fail "fresh scaffold" "patch changed a fresh template"
fi

# Existing sysctls in map form and list form keep their entries and gain one.
merge="$WORK/merge"
mkdir -p "$merge"
python3 - "$legacy/../legacy/docker-compose.yml" "$merge/docker-compose.yml" <<'PY'
import sys
src = open(sys.argv[1]).read()
# Undo the patch on a copy, then give the services their own sysctls.
src = src.replace("    sysctls:\n      net.ipv4.tcp_slow_start_after_idle: 0\n", "")
src = src.replace("-server\n", "-server\n    sysctls:\n      net.core.somaxconn: 4096\n", 1)
src = src.replace("-frontend\n", "-frontend\n    sysctls:\n      - net.ipv4.tcp_fin_timeout=15\n", 1)
open(sys.argv[2], "w").write(src)
PY
merged="$(run_patch "$merge")"
if [[ "$(sysctl_of "$merge/docker-compose.yml" server)" == "0 1 2" ]]; then
    pass "server map gains the entry and keeps its own"
else
    fail "server map merge" "got $(sysctl_of "$merge/docker-compose.yml" server)" "$merged"
fi
if [[ "$(sysctl_of "$merge/docker-compose.yml" frontend)" == "0 1 2" ]]; then
    pass "frontend list gains the entry and keeps its own"
else
    fail "frontend list merge" "got $(sysctl_of "$merge/docker-compose.yml" frontend)" "$merged"
fi

exit "$failed"
