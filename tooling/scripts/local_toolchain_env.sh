#!/bin/sh
# Source this file from scaffold checks and local runners.
# It normalizes tool discovery and cache locations without assuming an IDE shell.

ovasabi_toolchain_prepend_path_dir() {
  dir="$1"
  [ -n "$dir" ] || return 0
  [ -d "$dir" ] || return 0
  case ":$PATH:" in
    *":$dir:"*) ;;
    *) PATH="$dir:$PATH"; export PATH ;;
  esac
}

ovasabi_toolchain_append_path_dir() {
  dir="$1"
  [ -n "$dir" ] || return 0
  [ -d "$dir" ] || return 0
  case ":$PATH:" in
    *":$dir:"*) ;;
    *) PATH="$PATH:$dir"; export PATH ;;
  esac
}

ovasabi_toolchain_discover_go_bins() {
  for dir in /opt/homebrew/bin /usr/local/go/bin /usr/local/bin; do
    if [ -x "$dir/go" ]; then
      ovasabi_toolchain_prepend_path_dir "$dir"
      break
    fi
  done

  command -v go >/dev/null 2>&1 || return 0

  gobin="$(go env GOBIN 2>/dev/null || true)"
  gopath="$(go env GOPATH 2>/dev/null || true)"

  ovasabi_toolchain_prepend_path_dir "$gobin"
  if [ -n "$gopath" ]; then
    old_ifs="$IFS"
    IFS=:
    for entry in $gopath; do
      ovasabi_toolchain_prepend_path_dir "$entry/bin"
    done
    IFS="$old_ifs"
  fi
}

ovasabi_toolchain_prefer_local_node() {
  node_path="$(command -v node 2>/dev/null || true)"
  case "$node_path" in
    /Applications/Codex.app/*|"")
      for dir in \
        "$HOME/.volta/bin" \
        "$HOME/.local/share/mise/shims" \
        "$HOME/.asdf/shims" \
        /opt/homebrew/bin \
        /usr/local/bin
      do
        if [ -x "$dir/node" ]; then
          ovasabi_toolchain_prepend_path_dir "$dir"
          return 0
        fi
      done

      for node_bin in "$HOME"/.nvm/versions/node/*/bin/node; do
        [ -x "$node_bin" ] || continue
        ovasabi_toolchain_prepend_path_dir "$(dirname "$node_bin")"
      done
      ;;
  esac
}

ovasabi_toolchain_discover_docker() {
  if [ -n "${DOCKER_BIN:-}" ]; then
    export DOCKER_BIN
    return 0
  fi

  if command -v docker >/dev/null 2>&1; then
    DOCKER_BIN="$(command -v docker)"
  elif [ -x /Applications/Docker.app/Contents/Resources/bin/docker ]; then
    DOCKER_BIN="/Applications/Docker.app/Contents/Resources/bin/docker"
    ovasabi_toolchain_prepend_path_dir "/Applications/Docker.app/Contents/Resources/bin"
  else
    DOCKER_BIN="docker"
  fi
  export DOCKER_BIN
}

ovasabi_toolchain_cache_root() {
  dir="$1"
  while [ -n "$dir" ] && [ "$dir" != "/" ]; do
    if [ -f "$dir/scaffolded-projects.tsv" ]; then
      printf '%s\n' "$dir"
      return 0
    fi
    if [ -d "$dir/foundation/tooling/scripts" ] && [ -f "$dir/scripts/update-scaffolded-projects.sh" ]; then
      printf '%s\n' "$dir"
      return 0
    fi
    dir="$(dirname "$dir")"
  done
  printf '%s\n' "$1"
}

ovasabi_toolchain_init() {
  project_root="${1:-$(pwd)}"
  cache_root="$(ovasabi_toolchain_cache_root "$project_root")"

  ovasabi_toolchain_discover_go_bins
  ovasabi_toolchain_prefer_local_node
  ovasabi_toolchain_discover_docker

  codex_resources="/Applications/Codex.app/Contents/Resources"
  if ! command -v rg >/dev/null 2>&1 && [ -x "$codex_resources/rg" ]; then
    ovasabi_toolchain_append_path_dir "$codex_resources"
  fi

  mkdir -p "$cache_root/.cache/go-build" \
    "$cache_root/.cache/go" \
    "$cache_root/.cache/go/pkg/mod" \
    "$cache_root/.cache/go-analysis/staticcheck" \
    "$cache_root/.cache/npm" \
    "$cache_root/.cache/yarn" 2>/dev/null || true

  GOCACHE="${GOCACHE:-$cache_root/.cache/go-build}"
  GOPATH="${OVASABI_GOPATH:-$cache_root/.cache/go}"
  GOMODCACHE="${GOMODCACHE:-$GOPATH/pkg/mod}"
  GO_CACHE_DIR="${GO_CACHE_DIR:-$GOCACHE}"
  GO_ANALYSIS_CACHE_DIR="${GO_ANALYSIS_CACHE_DIR:-$cache_root/.cache/go-analysis}"
  STATICCHECK_CACHE="${STATICCHECK_CACHE:-$GO_ANALYSIS_CACHE_DIR/staticcheck}"
  XDG_CACHE_HOME="${XDG_CACHE_HOME:-$cache_root/.cache}"
  npm_config_cache="${npm_config_cache:-$cache_root/.cache/npm}"
  YARN_CACHE_FOLDER="${YARN_CACHE_FOLDER:-$cache_root/.cache/yarn}"

  ovasabi_toolchain_prepend_path_dir "$GOPATH/bin"

  export GOCACHE GOPATH GOMODCACHE GO_CACHE_DIR GO_ANALYSIS_CACHE_DIR STATICCHECK_CACHE XDG_CACHE_HOME
  export npm_config_cache YARN_CACHE_FOLDER
}
