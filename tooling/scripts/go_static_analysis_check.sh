#!/usr/bin/env zsh
set -euo pipefail

target="${1:-.}"
target="$(cd "$target" && pwd)"
script_dir="${0:A:h}"

if [[ -f "$script_dir/local_toolchain_env.sh" ]]; then
  source "$script_dir/local_toolchain_env.sh"
  ovasabi_toolchain_init "$target"
fi

prepend_path_dir() {
  local dir="$1"
  [[ -n "$dir" && -d "$dir" ]] || return 0
  case ":$PATH:" in
    *":$dir:"*) ;;
    *) export PATH="$dir:$PATH" ;;
  esac
}

discover_go_tool_bins() {
  command -v go >/dev/null 2>&1 || return 0

  local gobin gopath
  gobin="$(go env GOBIN 2>/dev/null || true)"
  gopath="$(go env GOPATH 2>/dev/null || true)"

  prepend_path_dir "$gobin"
  if [[ -n "$gopath" ]]; then
    local entry
    for entry in ${(s/:/)gopath}; do
      prepend_path_dir "$entry/bin"
    done
  fi
}

discover_go_tool_bins

ensure_writable_dir_or_tmp() {
  local candidate="$1"
  local fallback="$2"
  if [[ -n "$candidate" ]]; then
    mkdir -p "$candidate" 2>/dev/null || true
    if { : >"$candidate/.ovasabi-write-test" } 2>/dev/null; then
      rm -f "$candidate/.ovasabi-write-test"
      printf '%s\n' "$candidate"
      return 0
    fi
  fi
  mkdir -p "$fallback"
  printf '%s\n' "$fallback"
}

is_truthy() {
  case "${1:-}" in
    1|true|TRUE|yes|YES|on|ON) return 0 ;;
    *) return 1 ;;
  esac
}

strict_vuln_check() {
  is_truthy "${CI:-}" || is_truthy "${GOVULNCHECK_STRICT:-}" || is_truthy "${FOUNDATION_STRICT_SECURITY:-}"
}

run_with_timeout() {
  local timeout_sec="$1"
  shift
  if command -v perl >/dev/null 2>&1; then
    perl -e '
      use strict;
      use warnings;
      use POSIX ":sys_wait_h";

      my $timeout = shift @ARGV;
      my @cmd = @ARGV;
      my $pid = fork();
      die "fork failed: $!\n" unless defined $pid;

      if ($pid == 0) {
        setpgrp(0, 0) or die "setpgrp failed: $!\n";
        exec @cmd or die "exec failed: $!\n";
      }

      my $deadline = time() + $timeout;
      while (1) {
        my $done = waitpid($pid, WNOHANG);
        if ($done == $pid) {
          my $status = $?;
          if ($status & 127) {
            exit 128 + ($status & 127);
          }
          exit($status >> 8);
        }
        if ($done == -1) {
          print STDERR "waitpid failed: $!\n";
          exit 1;
        }
        if (time() >= $deadline) {
          kill "TERM", -$pid;
          select(undef, undef, undef, 0.5);
          kill "KILL", -$pid;
          waitpid($pid, 0);
          print STDERR "command timed out after ${timeout}s: @cmd\n";
          exit 124;
        }
        select(undef, undef, undef, 0.2);
      }
    ' "$timeout_sec" "$@"
  else
    "$@"
  fi
}

run_analysis_step() {
  local label="$1"
  local timeout_sec="$2"
  shift 2
  echo "[RUN] $label"
  run_with_timeout "$timeout_sec" "$@"
  echo "[OK] $label"
}

run_govulncheck() {
  local output_file
  output_file="$(mktemp "${TMPDIR:-/tmp}/govulncheck.XXXXXX")"

  if run_with_timeout "${GO_ANALYSIS_TOOL_TIMEOUT_SEC:-120}" govulncheck ./... >"$output_file" 2>&1; then
    rm -f "$output_file"
    return 0
  fi

  local exit_code=$?
  if grep -Eqi 'fetching vulnerabilities|vuln[.]go[.]dev|no such host|network is unreachable|i/o timeout|TLS handshake timeout|connection refused|temporary failure in name resolution' "$output_file"; then
    if strict_vuln_check; then
      cat "$output_file"
      rm -f "$output_file"
      return "$exit_code"
    fi

    echo "[WARN] govulncheck skipped: vulnerability database unavailable in local/offline mode" >&2
    echo "[WARN] set GOVULNCHECK_STRICT=1 or run under CI=true to fail closed" >&2
    sed -n '1,6p' "$output_file" >&2
    rm -f "$output_file"
    return 0
  fi

  cat "$output_file"
  rm -f "$output_file"
  return "$exit_code"
}

required_tools=(go staticcheck gopls govulncheck gosec)
missing_tools=()
for tool in "${required_tools[@]}"; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    missing_tools+=("$tool")
  fi
done

if (( ${#missing_tools[@]} > 0 )); then
  echo "[FAIL] missing required Go analysis tools: ${missing_tools[*]}" >&2
  echo "Install with:" >&2
  echo "  go install honnef.co/go/tools/cmd/staticcheck@latest" >&2
  echo "  go install golang.org/x/tools/gopls@latest" >&2
  echo "  go install golang.org/x/vuln/cmd/govulncheck@latest" >&2
  echo "  go install github.com/securego/gosec/v2/cmd/gosec@latest" >&2
  exit 1
fi

is_foundation_repo=0
if [[ -d "$target/server-kit/go" && -d "$target/runtime-transport/go" && -d "$target/tooling/scripts" ]]; then
  is_foundation_repo=1
fi

module_file="$(mktemp "${TMPDIR:-/tmp}/go-modules.XXXXXX")"
gopls_file="$(mktemp "${TMPDIR:-/tmp}/go-files.XXXXXX")"
trap 'rm -f "$module_file" "$gopls_file"' EXIT

find "$target" \
  \( -path '*/.git' -o -path '*/.cache' -o -path '*/node_modules' -o -path '*/vendor' -o -path '*/tmp' -o -path '*/dist' -o -path '*/build' \) -prune \
  -o -name go.mod -type f -print | sort >"$module_file"

go_modules=()
while IFS= read -r mod_file; do
  mod_dir="${mod_file:h}"
  rel="${mod_dir#$target/}"
  if [[ "$mod_dir" == "$target" ]]; then
    rel="."
  fi

  if (( ! is_foundation_repo )) && [[ "$rel" == foundation/* ]]; then
    continue
  fi
  if (( ! is_foundation_repo )) && [[ "$rel" == server-kit/* || "$rel" == runtime-transport/* || "$rel" == runtime-sdk/* || "$rel" == config-contracts/* ]]; then
    continue
  fi

  go_modules+=("$mod_dir")
done <"$module_file"

if (( ${#go_modules[@]} == 0 )); then
  echo "[OK] no Go modules found"
  exit 0
fi

go_cache="$(ensure_writable_dir_or_tmp "${GO_CACHE_DIR:-${GOCACHE:-}}" "${TMPDIR:-/tmp}/ovasabi-go-build")"
go_path="${GO_PATH_DIR:-}"
analysis_cache="$(ensure_writable_dir_or_tmp "${GO_ANALYSIS_CACHE_DIR:-${XDG_CACHE_HOME:-}}" "${TMPDIR:-/tmp}/ovasabi-go-analysis-cache")"
mkdir -p "$go_cache" "$analysis_cache/staticcheck"
if [[ -n "$go_path" ]]; then
  mkdir -p "$go_path"
fi

for mod_dir in "${go_modules[@]}"; do
  rel="${mod_dir#$target/}"
  if [[ "$mod_dir" == "$target" ]]; then
    rel="."
  fi
  echo "[RUN] Go static analysis: $rel"

  (
    cd "$mod_dir"
    export GOCACHE="$go_cache"
    if [[ -n "$go_path" ]]; then
      export GOPATH="$go_path"
    fi
    export XDG_CACHE_HOME="$analysis_cache"
    export STATICCHECK_CACHE="$analysis_cache/staticcheck"

    packages="$(go list ./...)"
    if [[ -z "$packages" ]]; then
      echo "[OK] no Go packages in $rel"
      exit 0
    fi

    run_analysis_step "go vet: $rel" "${GO_ANALYSIS_TOOL_TIMEOUT_SEC:-120}" go vet ./...
    run_analysis_step "staticcheck: $rel" "${GO_ANALYSIS_TOOL_TIMEOUT_SEC:-120}" staticcheck ./...
    : >"$gopls_file"
    go list -f '{{range .GoFiles}}{{printf "%s/%s%c" $.Dir . 0}}{{end}}{{range .TestGoFiles}}{{printf "%s/%s%c" $.Dir . 0}}{{end}}' ./... | tr -d '\n' >"$gopls_file"
    if [[ -s "$gopls_file" ]]; then
      echo "[RUN] gopls check: $rel"
      run_with_timeout "${GO_ANALYSIS_TOOL_TIMEOUT_SEC:-120}" xargs -0 gopls check <"$gopls_file"
      echo "[OK] gopls check: $rel"
    fi
    echo "[RUN] govulncheck: $rel"
    run_govulncheck
    echo "[OK] govulncheck: $rel"
    run_analysis_step "gosec: $rel" "${GO_ANALYSIS_TOOL_TIMEOUT_SEC:-120}" gosec -quiet -exclude-generated -exclude-dir=.cache -exclude-dir="$target/.cache" -exclude-dir="$go_cache" -exclude-rules=".*/ovasabi-go-build/.*:*;.*/go-build/.*:*;.*/generated/.*:*;.*\\.pb\\.go:*" ./...
  )

  echo "[OK] Go static analysis: $rel"
done

wasm_compile() {
  local module_dir="$1"
  local package_path="$2"
  local output_name="$3"

  if [[ ! -d "$module_dir" ]]; then
    return 0
  fi

  (
    cd "$module_dir"
    export GOCACHE="$go_cache"
    GOWORK=off GOOS=js GOARCH=wasm go test -c -o "${TMPDIR:-/tmp}/${output_name}.wasm" "$package_path"
  )
}

if (( is_foundation_repo )); then
  echo "[RUN] Go js/wasm portability compile checks"
  wasm_compile "$target/server-kit/go" "./healthcheck" "ovasabi-healthcheck"
  wasm_compile "$target/runtime-sdk/go" "./runtimehost" "ovasabi-runtimehost"
  echo "[OK] Go js/wasm portability compile checks"
elif [[ -d "$target/foundation/server-kit/go" || -d "$target/foundation/runtime-sdk/go" ]]; then
  echo "[RUN] vendored Foundation Go js/wasm portability compile checks"
  wasm_compile "$target/foundation/server-kit/go" "./healthcheck" "ovasabi-vendored-healthcheck"
  wasm_compile "$target/foundation/runtime-sdk/go" "./runtimehost" "ovasabi-vendored-runtimehost"
  echo "[OK] vendored Foundation Go js/wasm portability compile checks"
fi

echo "Go static analysis check passed"
