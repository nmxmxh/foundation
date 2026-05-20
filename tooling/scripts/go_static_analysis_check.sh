#!/usr/bin/env zsh
set -euo pipefail

target="${1:-.}"
target="$(cd "$target" && pwd)"

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
  \( -path '*/.git' -o -path '*/node_modules' -o -path '*/vendor' -o -path '*/tmp' -o -path '*/dist' -o -path '*/build' \) -prune \
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

go_cache="${GO_CACHE_DIR:-${TMPDIR:-/tmp}/ovasabi-go-build}"
mkdir -p "$go_cache"

for mod_dir in "${go_modules[@]}"; do
  rel="${mod_dir#$target/}"
  if [[ "$mod_dir" == "$target" ]]; then
    rel="."
  fi
  echo "[RUN] Go static analysis: $rel"

  (
    cd "$mod_dir"
    export GOCACHE="$go_cache"

    packages="$(go list ./...)"
    if [[ -z "$packages" ]]; then
      echo "[OK] no Go packages in $rel"
      exit 0
    fi

    go vet ./...
    staticcheck ./...
    : >"$gopls_file"
    go list -f '{{range .GoFiles}}{{printf "%s/%s%c" $.Dir . 0}}{{end}}{{range .TestGoFiles}}{{printf "%s/%s%c" $.Dir . 0}}{{end}}' ./... | tr -d '\n' >"$gopls_file"
    if [[ -s "$gopls_file" ]]; then
      xargs -0 gopls check <"$gopls_file"
    fi
    govulncheck ./...
    gosec -quiet -exclude-generated -exclude-dir=.cache -exclude-dir="$target/.cache" -exclude-dir="$go_cache" -exclude-rules=".*/ovasabi-go-build/.*:*;.*/go-build/.*:*;.*/generated/.*:*;.*\\.pb\\.go:*" ./...
  )

  echo "[OK] Go static analysis: $rel"
done

echo "Go static analysis check passed"
