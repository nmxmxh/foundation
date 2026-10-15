#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/foundation-movement.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT
source "$ROOT/tooling/scripts/command_timeout.sh"
real_go="$(command -v go)"
export GOCOVERDIR="${MOVEMENT_COVERAGE_DIR:-$TMP/coverage}"
mkdir -p "$GOCOVERDIR"
run_with_timeout 120 "$real_go" build -cover -o "$TMP/checker" "$ROOT/tooling/scripts/benchmark_movement_check.go"
mkdir -p "$TMP/bin" "$TMP/fixture/tooling" "$TMP/fixture/module/sample"
printf 'module fixture\n' > "$TMP/fixture/module/go.mod"
cat > "$TMP/bin/go" <<'SH'
#!/bin/bash
case "${MOVEMENT_FIXTURE_TESTS:-pass}" in
  missing) ;;
  skip) echo '--- SKIP: TestMovementFixture (0.00s)' ;;
  duplicate) printf '%s\n' '--- PASS: TestMovementFixture (0.00s)' '--- PASS: TestMovementFixture (0.00s)' ;;
  *) echo '--- PASS: TestMovementFixture (0.00s)' ;;
esac
cat "$MOVEMENT_FIXTURE_OUTPUT"
if [[ "${MOVEMENT_FIXTURE_FAIL:-0}" == 1 ]]; then
  echo 'fixture failed after valid measurements' >&2
  exit 2
fi
if [[ "${MOVEMENT_FIXTURE_SLEEP:-0}" == 1 ]]; then
  sleep 30
fi
SH
chmod +x "$TMP/bin/go"
export PATH="$TMP/bin:$PATH" MOVEMENT_FIXTURE_OUTPUT="$TMP/output"
unset HUMAN_SUPERVISED_CHECK_UPDATE
fixture="$TMP/fixture"
baseline="$fixture/tooling/benchmark_movement_baseline.psv"
sources="$fixture/tooling/benchmark_movement_sources.psv"
packages="$fixture/tooling/benchmark_movement_packages.tsv"
source_file="$fixture/module/sample/sample.go"
check=("$TMP/checker" check "$fixture" "$ROOT/tooling/scripts/command_timeout.sh")

save_contracts() {
  cp "$baseline" "$TMP/baseline.expected"
  cp "$sources" "$TMP/sources.expected"
}

reset_fixture() {
  printf 'module\t./sample\t^BenchmarkMovement\t^TestMovement\n' > "$packages"
  printf 'module|./sample|BenchmarkMovementFixture|copied-B/op|16\n' > "$baseline"
  printf 'package sample\nfunc covered(dst, src []byte) { copy(dst, src) }\n' > "$source_file"
  printf 'package sample\nimport "testing"\nfunc TestMovementFixture(t *testing.T) {}\nfunc BenchmarkMovementFixture(b *testing.B) {}\n' > "$fixture/module/sample/sample_test.go"
  printf 'module/sample/sample.go|covered|%064d\n' 0 > "$sources"
  printf 'module/sample/sample_test.go|TestMovementFixture|%064d\n' 0 >> "$sources"
  printf 'module/sample/sample_test.go|BenchmarkMovementFixture|%064d\n' 0 >> "$sources"
  HUMAN_SUPERVISED_CHECK_UPDATE=1 "$TMP/checker" write-audit "$fixture"
  printf 'BenchmarkMovementFixture-8 200 10 ns/op 16 copied-B/op 0 B/op 0 allocs/op\nPASS\n' > "$MOVEMENT_FIXTURE_OUTPUT"
  save_contracts
}

reject() {
  local label="$1"
  shift
  if "$@" > "$TMP/log" 2>&1; then
    echo "[FAIL] accepted $label" >&2
    cat "$TMP/log" >&2
    exit 1
  fi
  cmp -s "$baseline" "$TMP/baseline.expected" || { echo '[FAIL] changed movement budget' >&2; exit 1; }
  cmp -s "$sources" "$TMP/sources.expected" || { echo '[FAIL] changed source contract' >&2; exit 1; }
}

accept() {
  if ! "$@" > "$TMP/log" 2>&1; then
    cat "$TMP/log" >&2
    exit 1
  fi
}

reset_fixture
accept "${check[@]}"
reject 'missing command arguments' "$TMP/checker"
reject 'unsupported command mode' "$TMP/checker" unknown "$fixture"
reject 'unsupervised source update' "$TMP/checker" write-audit "$fixture"
reject 'missing test evidence' env MOVEMENT_FIXTURE_TESTS=missing "${check[@]}"
reject 'skipped accounting test' env MOVEMENT_FIXTURE_TESTS=skip "${check[@]}"
reject 'duplicate test evidence' env MOVEMENT_FIXTURE_TESTS=duplicate "${check[@]}"
reject 'process failure after measurements' env MOVEMENT_FIXTURE_FAIL=1 "${check[@]}"
grep -Fq 'fixture failed after valid measurements' "$TMP/log"

for value in 17 32 NaN +Inf -1 1.5 bad 1e999999999; do
  printf 'BenchmarkMovementFixture-8 200 10 ns/op %s copied-B/op 0 B/op 0 allocs/op\n' "$value" > "$MOVEMENT_FIXTURE_OUTPUT"
  reject "invalid or excessive count $value" "${check[@]}"
done
printf 'BenchmarkMovementFixture-8 200 10 ns/op 1.6e1 copied-B/op 0 B/op 0 allocs/op\n' > "$MOVEMENT_FIXTURE_OUTPUT"
accept "${check[@]}"
printf 'BenchmarkMovementFixture-8 200 10 ns/op 0 copied-B/op 0 B/op 0 allocs/op\n' > "$MOVEMENT_FIXTURE_OUTPUT"
accept "${check[@]}"
printf 'BenchmarkMovementFixture-8 200 10 ns/op 0 B/op 0 allocs/op\n' > "$MOVEMENT_FIXTURE_OUTPUT"
reject 'missing metric' "${check[@]}"
printf 'PASS\n' > "$MOVEMENT_FIXTURE_OUTPUT"
reject 'missing benchmark' "${check[@]}"
printf 'BenchmarkMovementFixture-8 200 10 ns/op 16 copied-B/op 16 copied-B/op\n' > "$MOVEMENT_FIXTURE_OUTPUT"
reject 'duplicate metric' "${check[@]}"
reset_fixture
cat "$MOVEMENT_FIXTURE_OUTPUT" > "$TMP/row"
cat "$TMP/row" >> "$MOVEMENT_FIXTURE_OUTPUT"
reject 'duplicate benchmark' "${check[@]}"

reset_fixture
for value in NaN -1 1.5 bad 1e999999999; do
  printf 'module|./sample|BenchmarkMovementFixture|copied-B/op|%s\n' "$value" > "$baseline"
  save_contracts
  reject "invalid baseline count $value" "${check[@]}"
done
reset_fixture
printf 'module|./sample|BenchmarkMovementFixture|ns/op|16\n' > "$baseline"
save_contracts
reject 'ordinary timing metric in movement budget' "${check[@]}"
reset_fixture
printf 'module|./sample|BenchmarkMovementFixture|copied-B/op|16 \n' > "$baseline"
save_contracts
reject 'padded budget field' "${check[@]}"
reset_fixture
awk 'BEGIN { for (i=0; i<70000; i++) printf "x"; print "" }' > "$baseline"
save_contracts
reject 'oversized contract record' "${check[@]}"
reset_fixture
cat "$baseline" > "$TMP/row"
cat "$TMP/row" >> "$baseline"
save_contracts
reject 'duplicate budget row' "${check[@]}"
reset_fixture
printf 'module|./sample|BenchmarkMovementFixture|copied-B/op|16|extra\n' > "$baseline"
save_contracts
reject 'malformed budget row' "${check[@]}"
reset_fixture
: > "$baseline"
save_contracts
reject 'empty budget manifest' "${check[@]}"
reset_fixture
mv "$baseline" "$TMP/removed.baseline"
if "${check[@]}" > "$TMP/log" 2>&1; then echo '[FAIL] accepted missing budget manifest' >&2; exit 1; fi
mv "$TMP/removed.baseline" "$baseline"

reset_fixture
printf 'package sample\ntype fixture struct{}\nfunc (f *fixture) covered(dst, src []byte) { copy(dst, src) }\n' > "$source_file"
sed 's/|covered|/|(*fixture).covered|/' "$sources" > "$TMP/modified"
mv "$TMP/modified" "$sources"
HUMAN_SUPERVISED_CHECK_UPDATE=1 "$TMP/checker" write-audit "$fixture"
save_contracts
accept "${check[@]}"
reset_fixture
printf 'package sample\n// Comment before the function.\nfunc covered(dst, src []byte) {\n\n// Copy the payload.\ncopy(dst, src)\n}\n' > "$source_file"
accept "${check[@]}"
printf 'package sample\nfunc covered(dst, src []byte) { copy(dst, src); copy(dst, src) }\n' > "$source_file"
reject 'unmetered additional copy' "${check[@]}"
grep -Fq 'movement source changed' "$TMP/log"
printf 'package sample\nfunc covered(dst, src []byte) { helper(dst, src) }\n' > "$source_file"
reject 'changed helper call' "${check[@]}"
printf 'package sample\nfunc covered(dst, src []uint64) { copy(dst, src) }\n' > "$source_file"
reject 'changed function signature' "${check[@]}"
printf 'package sample\nfunc renamed(dst, src []byte) { copy(dst, src) }\n' > "$source_file"
reject 'missing source function' "${check[@]}"
rm "$source_file"
reject 'missing source file' "${check[@]}"
reject 'failed source refresh' env HUMAN_SUPERVISED_CHECK_UPDATE=1 "$TMP/checker" write-audit "$fixture"
reset_fixture
: > "$sources"
save_contracts
reject 'empty source manifest' "${check[@]}"
reset_fixture
cat "$sources" > "$TMP/row"
cat "$TMP/row" >> "$sources"
save_contracts
reject 'duplicate source row' "${check[@]}"
reset_fixture
printf 'package sample\nfunc covered(dst, src []byte)\n' > "$source_file"
reject 'source function without a body' "${check[@]}"
reset_fixture
sed 's@module/sample/sample.go@../sample.go@' "$sources" > "$TMP/modified"
mv "$TMP/modified" "$sources"
save_contracts
reject 'source outside target root' "${check[@]}"
reset_fixture
sed '/|TestMovementFixture|/d' "$sources" > "$TMP/modified"
mv "$TMP/modified" "$sources"
save_contracts
reject 'no audited accounting tests' "${check[@]}"
reset_fixture
sed '/|BenchmarkMovementFixture|/d' "$sources" > "$TMP/modified"
mv "$TMP/modified" "$sources"
save_contracts
reject 'unaudited benchmark fixture' "${check[@]}"
reset_fixture
printf 'module\t./sample\t^BenchmarkMovement\t^TestMovement$\n' > "$packages"
reject 'test selection matching no audited tests' "${check[@]}"
reset_fixture
cat "$packages" > "$TMP/row"
cat "$TMP/row" >> "$packages"
reject 'duplicate package row' "${check[@]}"
reset_fixture
rm "$fixture/module/go.mod"
reject 'missing module' "${check[@]}"
printf 'module fixture\n' > "$fixture/module/go.mod"

reset_fixture
printf 'module\t./sample\t^Benchmark\t^TestMovement\n' > "$packages"
reject 'unrestricted benchmark selection' "${check[@]}"
printf 'module\t./sample\t^BenchmarkMovement[\t^TestMovement\n' > "$packages"
reject 'invalid benchmark expression' "${check[@]}"
: > "$packages"
reject 'empty package manifest' "${check[@]}"
reset_fixture
printf 'module\t./unbudgeted\t^BenchmarkMovement\t^TestMovement\n' >> "$packages"
reject 'package without movement ceilings' "${check[@]}"
reset_fixture
printf 'BenchmarkMovementFixture-8 200 10 ns/op 16 copied-B/op trailing\n' > "$MOVEMENT_FIXTURE_OUTPUT"
reject 'malformed metric pairs' "${check[@]}"
printf 'BenchmarkMovementFixture-8 0 10 ns/op 16 copied-B/op\n' > "$MOVEMENT_FIXTURE_OUTPUT"
reject 'zero measured iterations' "${check[@]}"
printf 'BenchmarkMovementFixture-8 200 10 ns/op 16 copied-B/op 4 unknown/op\n' > "$MOVEMENT_FIXTURE_OUTPUT"
reject 'unbudgeted movement metric' "${check[@]}"
printf '%s\n' '--- PASS: TestUnrelated (0.00s)' BenchmarkMovementFixture 'BenchmarkMovementFixture-8 200 10 ns/op 16 copied-B/op' > "$MOVEMENT_FIXTURE_OUTPUT"
accept "${check[@]}"
reset_fixture
awk 'BEGIN { for (i=0; i<70000; i++) printf "x"; print "" }' > "$MOVEMENT_FIXTURE_OUTPUT"
reject 'oversized process output record' "${check[@]}"
reset_fixture
cat > "$TMP/short_timeout.sh" <<SH
run_with_timeout() {
  shift
  bash -c 'source "\$1"; shift; run_with_timeout 1 "\$@"' movement \
    "$ROOT/tooling/scripts/command_timeout.sh" "\$@"
}
SH
reject 'timed out process' env MOVEMENT_FIXTURE_SLEEP=1 "$TMP/checker" check "$fixture" "$TMP/short_timeout.sh"
grep -Fq 'command timed out after 1s' "$TMP/log"
echo 'movement guard regression tests passed'
"$real_go" tool covdata percent -i="$GOCOVERDIR"
