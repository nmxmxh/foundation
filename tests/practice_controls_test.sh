#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/foundation-controls.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/docs" "$TMP/tooling"
printf '### CP-01\n' > "$TMP/docs/coding_practices.md"
printf '### TE-01\n' > "$TMP/docs/testing_practices.md"
matrix="$TMP/tooling/practice_controls.psv"
printf '# control_id|owner_doc|category|risk|automation|enforcement|evidence|merge_gate\n' > "$matrix"
for control in CP-01 TE-01 CTRL-01 AOC-01 EVID-01 FPR-01 AISEC-01 PERFLAB-01 RUNTIME-01 FORMAL-01 OPS-01 PROJFRESH-01 MATH-01; do
  printf '%s|coding_practices.md|fixture|low|human|review|fixture|yes\n' "$control" >> "$matrix"
done
check=(zsh "$ROOT/tooling/scripts/practice_controls_check.sh" "$TMP")
"${check[@]}" > "$TMP/log" 2>&1
cp "$matrix" "$TMP/valid"
tail -1 "$matrix" >> "$TMP/duplicate"
cat "$TMP/duplicate" >> "$matrix"
if "${check[@]}" > "$TMP/log" 2>&1; then
  echo '[FAIL] practice controls accepted a duplicate control' >&2
  exit 1
fi
grep -Fq 'duplicate control id' "$TMP/log"
cp "$TMP/valid" "$matrix"
printf '### CP-02\n' >> "$TMP/docs/coding_practices.md"
if "${check[@]}" > "$TMP/log" 2>&1; then
  echo '[FAIL] practice controls accepted an unmapped control' >&2
  exit 1
fi
grep -Fq '[FAIL] control mapped CP-02' "$TMP/log"
echo 'practice controls regression tests passed'
