#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/foundation-go-analysis.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/checks" "$WORK/bin" "$WORK/project"
cp "$ROOT/tooling/scripts/go_static_analysis_check.sh" "$ROOT/tooling/scripts/command_timeout.sh" "$WORK/checks/"
printf 'module example.com/fixture\n' > "$WORK/project/go.mod"
printf 'package fixture\n' > "$WORK/project/fixture.go"
cat > "$WORK/bin/go" <<'SH'
#!/bin/sh
case "$1 $2" in
  'env GOBIN') printf '%s\n' "$ANALYSIS_FIXTURE_BIN" ;;
  'env GOPATH') printf '%s\n' "$ANALYSIS_FIXTURE_WORK/gopath" ;;
  'list -f') printf '%s\0' "$ANALYSIS_FIXTURE_WORK/project/fixture.go" ;;
  'list ./...') printf 'example.com/fixture\n' ;;
esac
SH
for tool in staticcheck gopls gosec; do
  printf '#!/bin/sh\nexit 0\n' > "$WORK/bin/$tool"
done
cat > "$WORK/bin/govulncheck" <<'SH'
#!/bin/sh
printf '%s\n' "$VULN_FIXTURE_OUTPUT"
exit "$VULN_FIXTURE_STATUS"
SH
chmod +x "$WORK/bin/"*
export PATH="$WORK/bin:$PATH" ANALYSIS_FIXTURE_BIN="$WORK/bin" ANALYSIS_FIXTURE_WORK="$WORK"
export GOCACHE="$WORK/cache" GO_ANALYSIS_CACHE_DIR="$WORK/analysis-cache"

check_status() {
  local wanted="$1" strict="$2" scanner="$3" output="$4" status=0
  CI=0 GOVULNCHECK_STRICT="$strict" GOPLS_CHECK_STRICT=1 VULN_FIXTURE_STATUS="$scanner" VULN_FIXTURE_OUTPUT="$output" \
    zsh "$WORK/checks/go_static_analysis_check.sh" "$WORK/project" > "$WORK/log" 2>&1 || status=$?
  if [[ "$status" != "$wanted" ]]; then
    cat "$WORK/log" >&2
    echo "expected scanner gate status $wanted, got $status" >&2
    exit 1
  fi
}
check_status 0 1 0 'No vulnerabilities found'
check_status 3 1 3 'Your code is affected by a vulnerability'
check_status 3 0 3 'Your code is affected; details at https://vuln.go.dev'
check_status 1 1 1 'fetching vulnerabilities: connection refused'
check_status 0 0 1 'fetching vulnerabilities: connection refused'
check_status 7 0 7 'scanner failed unexpectedly'
check_status 124 1 124 'scanner deadline exceeded'

# Discovery must remain independent of toolchains and module-cache permissions.
cat > "$WORK/bin/go" <<'SH'
#!/bin/sh
printf 'called\n' >> "$ANALYSIS_FIXTURE_WORK/unexpected-go-call"
exit 99
SH
GO_ANALYSIS_DISCOVERY_ONLY=1 zsh "$ROOT/tooling/scripts/go_static_analysis_check.sh" "$WORK/project" > "$WORK/discovery"
grep -Fxq '.' "$WORK/discovery"
[[ ! -e "$WORK/unexpected-go-call" ]]
[[ ! -d "$WORK/project/.cache" ]]
echo 'Go analysis exit propagation, offline fallback, and discovery isolation passed'
