#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
MANIFEST="${MANIFEST:-$FOUNDATION_DIR/templates/scaffold.manifest.tsv}"

usage() {
    cat <<'USAGE'
Usage:
  tooling/scripts/manifest_tool.sh list [--profile full|backend|frontend|all] [--feature always|docker|wasm|native] [--mode overwrite|force|create]
  tooling/scripts/manifest_tool.sh validate
  tooling/scripts/manifest_tool.sh explain

Lists and validates the scaffold manifest without mutating it.
USAGE
}

command="${1:-}"
if [[ -z "$command" ]]; then
    usage
    exit 2
fi
shift || true

case "$command" in
validate)
    "$FOUNDATION_DIR/tests/scaffold_manifest_test.sh"
    ;;
explain)
    cat <<'EXPLAIN'
scaffold.manifest.tsv columns:
  source       Foundation template/source path.
  destination Generated project destination path.
  profiles    Comma-separated project profiles: all, full, backend, frontend.
  feature     always, docker, wasm, or native.
  mode        overwrite, force, or create.

Mode meanings:
  overwrite   Managed scaffold file, updated on foundation-update.
  force       Managed file where regeneration must win over app drift.
  create      Seed only; existing app-owned files are preserved.

Run list filters before editing the manifest, then run validate.
EXPLAIN
    ;;
list)
    profile=""
    feature=""
    mode=""
    while [[ "$#" -gt 0 ]]; do
        case "$1" in
        --profile)
            profile="${2:-}"
            shift 2
            ;;
        --feature)
            feature="${2:-}"
            shift 2
            ;;
        --mode)
            mode="${2:-}"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "unknown manifest list option: $1" >&2
            usage
            exit 2
            ;;
        esac
    done
    awk -v profile="$profile" -v feature="$feature" -v mode="$mode" '
      BEGIN { FS = "\t"; OFS = "\t" }
      /^#/ || NF == 0 { next }
      NF != 5 { next }
      {
        if (profile != "") {
          split($3, profiles, ",")
          matched = 0
          for (i in profiles) {
            if (profiles[i] == profile || profiles[i] == "all") matched = 1
          }
          if (!matched) next
        }
        if (feature != "" && $4 != feature) next
        if (mode != "" && $5 != mode) next
        print $1, $2, $3, $4, $5
      }
    ' "$MANIFEST"
    ;;
*)
    echo "unknown manifest command: $command" >&2
    usage
    exit 2
    ;;
esac
