#!/bin/zsh
set -euo pipefail

root="${1:-.}"
catalogue="${2:-$root/scaffolded-projects.tsv}"
shift 2 || true

mode="all"
parallelism=1
while [[ $# -gt 0 ]]; do
  case "$1" in
    --help|-h)
      cat <<'EOF'
Usage: scripts/test-scaffolded-projects.sh [catalogue.tsv] [--all|--backend-only|--frontend-only] [--parallel N]

Runs scaffolded project checks from the catalogue with Foundation's local
toolchain discovery, isolated test Docker compose projects, and per-project logs.
EOF
      exit 0
      ;;
    --parallel)
      parallelism="${2:-}"
      if [[ -z "$parallelism" ]] || ! printf '%s\n' "$parallelism" | grep -Eq '^[1-9][0-9]*$'; then
        echo "--parallel requires a positive integer" >&2
        exit 1
      fi
      shift 2
      ;;
    --parallel=*)
      parallelism="${1#--parallel=}"
      if [[ -z "$parallelism" ]] || ! printf '%s\n' "$parallelism" | grep -Eq '^[1-9][0-9]*$'; then
        echo "--parallel requires a positive integer" >&2
        exit 1
      fi
      shift
      ;;
    --backend-only|--backend)
      mode="backend"
      shift
      ;;
    --frontend-only|--frontend)
      mode="frontend"
      shift
      ;;
    --all)
      mode="all"
      shift
      ;;
    *)
      echo "unknown option: $1" >&2
      exit 1
      ;;
  esac
done

root="$(cd "$root" && pwd)"
catalogue="$(cd "$(dirname "$catalogue")" && pwd)/$(basename "$catalogue")"
script_dir="$(cd "$(dirname "$0")" && pwd)"

if [[ -f "$script_dir/local_toolchain_env.sh" ]]; then
  source "$script_dir/local_toolchain_env.sh"
  ovasabi_toolchain_init "$root"
fi

if [[ ! -f "$catalogue" ]]; then
  echo "missing scaffolded project catalogue: $catalogue" >&2
  exit 1
fi

stamp="$(date +%Y%m%d_%H%M%S)_$$"
log_dir="$root/.cache/scaffolded-test-results/$stamp"
mkdir -p "$log_dir"
summary="$log_dir/summary.tsv"
: > "$summary"

test_env_name() {
  local slug="$1"
  print -r -- ".cache/test-env/${slug}_${stamp}.env"
}

compose_project_name() {
  local slug="$1"
  print -r -- "${slug}_${stamp}_test"
}

cleanup_project() {
  local slug="$1"
  local project="$2"
  if [[ -f "$project/Makefile" ]]; then
    (cd "$project" && TEST_COMPOSE_PROJECT_NAME="$(compose_project_name "$slug")" TEST_ENV_FILE="$(test_env_name "$slug")" make test-env-down </dev/null >/dev/null 2>&1 || true)
  fi
}

run_project_make() {
  local slug="$1"
  local project="$2"
  shift 2
  local log="$log_dir/${slug}.log"
  local status_file="$log_dir/${slug}.status"
  local line

  echo "== $slug =="
  cleanup_project "$slug" "$project"

  if (cd "$project" && TEST_COMPOSE_PROJECT_NAME="$(compose_project_name "$slug")" TEST_ENV_FILE="$(test_env_name "$slug")" make "$@" </dev/null) > "$log" 2>&1; then
    line="OK	$slug	$*	$log"
    print -r -- "$line" > "$status_file"
    print -r -- "$line"
    return 0
  fi

  line="FAIL	$slug	$*	$log"
  print -r -- "$line" > "$status_file"
  print -r -- "$line" >&2
  tail -160 "$log" >&2
  return 1
}

run_one_project() {
  local slug="$1"
  local relpath="$2"
  local project="$root/$relpath"
  local status_file="$log_dir/${slug}.status"
  local result=0

  if [[ ! -f "$project/.foundation" ]]; then
    print -r -- "FAIL	$slug	missing .foundation	$project" > "$status_file"
    print -r -- "FAIL	$slug	missing .foundation	$project" >&2
    return 1
  fi

  case "$mode" in
    backend)
      run_project_make "$slug" "$project" lint-foundation test-unit test-integration || result=1
      ;;
    frontend)
      run_project_make "$slug" "$project" test-frontend || result=1
      ;;
    all)
      run_project_make "$slug" "$project" test-all || result=1
      ;;
  esac

  cleanup_project "$slug" "$project"
  return "$result"
}

flush_parallel_batch() {
  local pid slug
  for pid in "${batch_pids[@]}"; do
    wait "$pid" || failed=1
  done
  for slug in "${batch_slugs[@]}"; do
    if [[ -f "$log_dir/${slug}.status" ]]; then
      cat "$log_dir/${slug}.status" >> "$summary"
    fi
  done
  batch_pids=()
  batch_slugs=()
}

failed=0
batch_pids=()
batch_slugs=()
while IFS=$'\t' read -r slug relpath _rest; do
  [[ -z "${slug:-}" || "$slug" == \#* ]] && continue

  if (( parallelism <= 1 )); then
    run_one_project "$slug" "$relpath" || failed=1
    if [[ -f "$log_dir/${slug}.status" ]]; then
      cat "$log_dir/${slug}.status" >> "$summary"
    fi
    continue
  fi

  run_one_project "$slug" "$relpath" &
  batch_pids+=("$!")
  batch_slugs+=("$slug")
  if (( ${#batch_pids[@]} >= parallelism )); then
    flush_parallel_batch
  fi
done < "$catalogue"

if (( ${#batch_pids[@]} > 0 )); then
  flush_parallel_batch
fi

echo "summary: $summary"
exit "$failed"
