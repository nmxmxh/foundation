#!/bin/zsh
set -euo pipefail

target="${1:-.}"
failed=0
tmp_output="/tmp/ovasabi_te_check.out"

check_no_match() {
  local label="$1"
  local pattern="$2"
  shift 2
  if rg -n \
    --glob '!**/node_modules/**' \
    --glob '!**/dist/**' \
    --glob '!**/target/**' \
    --glob '!**/coverage/**' \
    "$pattern" "$@" >"$tmp_output" 2>/dev/null; then
    echo "[FAIL] $label"
    cat "$tmp_output"
    failed=1
  else
    echo "[OK] $label"
  fi
}

check_has_match() {
  local label="$1"
  local pattern="$2"
  shift 2
  if rg -n \
    --glob '!**/node_modules/**' \
    --glob '!**/dist/**' \
    --glob '!**/target/**' \
    --glob '!**/coverage/**' \
    "$pattern" "$@" >/dev/null 2>&1; then
    echo "[OK] $label"
  else
    echo "[FAIL] $label"
    failed=1
  fi
}

if rg --files "$target" | rg -q '(\.test|\.spec)\.(ts|tsx|js|jsx)$'; then
  check_no_match "TE no focused TypeScript tests" "(\b(describe|it|test)\.only\s*\(|\.only\s*\()" "$target" \
    --glob '*.test.ts' --glob '*.test.tsx' --glob '*.spec.ts' --glob '*.spec.tsx' \
    --glob '*.test.js' --glob '*.test.jsx' --glob '*.spec.js' --glob '*.spec.jsx'

  check_no_match "TE no skipped TypeScript tests" "\b(describe|it|test)\.skip\s*\(" "$target" \
    --glob '*.test.ts' --glob '*.test.tsx' --glob '*.spec.ts' --glob '*.spec.tsx' \
    --glob '*.test.js' --glob '*.test.jsx' --glob '*.spec.js' --glob '*.spec.jsx'

  check_no_match "TE TypeScript tests avoid long fixed waits" "setTimeout\s*\([^,\n]+,\s*([2-9][0-9]{3,}|[1-9][0-9]{4,})\s*\)" "$target" \
    --glob '*.test.ts' --glob '*.test.tsx' --glob '*.spec.ts' --glob '*.spec.tsx' \
    --glob '*.test.js' --glob '*.test.jsx' --glob '*.spec.js' --glob '*.spec.jsx' \
    --glob '!**/e2e/**' --glob '!**/load/**'

  check_no_match "TE TypeScript tests avoid uncontrolled Math.random" "\bMath\.random\s*\(" "$target" \
    --glob '*.test.ts' --glob '*.test.tsx' --glob '*.spec.ts' --glob '*.spec.tsx' \
    --glob '*.test.js' --glob '*.test.jsx' --glob '*.spec.js' --glob '*.spec.jsx'
else
  echo "[OK] no TypeScript test files present"
fi

if rg --files "$target" | rg -q '_test\.go$'; then
  check_no_match "TE Go tests avoid extreme fixed sleeps outside load tests" "time\.Sleep\s*\(\s*([5-9][0-9]{2}|[1-9][0-9]{3,})\s*\*\s*time\.Millisecond|time\.Sleep\s*\(\s*([2-9]|[1-9][0-9]+)\s*\*\s*time\.(Second|Minute)|time\.Sleep\s*\(\s*time\.(Second|Minute)\s*\)" "$target" \
    --glob '*_test.go' --glob '!**/tests/load/**' --glob '!**/load/**'

  if [[ -d "$target/api/protos" || -d "$target/server-kit/go/contracttest" || -d "$target/tests/contract" ]]; then
    check_has_match "TE lifecycle/contract tests present for event contracts" "(VerifyProducer|VerifyConsumer|lifecycle|:requested|:success|:failed)" "$target" \
      --glob '*_test.go' --glob '!**/node_modules/**'
  fi

  check_no_match "TE Go tests avoid package-global random without explicit source" "\brand\.(Int|Intn|Int31|Int31n|Int63|Int63n|Float32|Float64|Perm|Shuffle|NormFloat64|ExpFloat64)\s*\(" "$target" \
    --glob '*_test.go' --glob '!**/tests/load/**' --glob '!**/load/**' --glob '!**/benchmark/**'
else
  echo "[OK] no Go test files present"
fi

if rg --files "$target" | rg -q '\.go$'; then
  check_no_match "TE percentile helpers use conservative nearest-rank math" "float64\(len\([^)]+\)-1\)\s*\*|\(n\s*\*\s*percentile\)\s*/\s*100" "$target" \
    --glob '*.go' --glob '!**/node_modules/**' --glob '!**/vendor/**'
fi

for performance_check in \
  "$target/tooling/scripts/performance_check.sh" \
  "$target/foundation/tooling/scripts/performance_check.sh" \
  "$target/scripts/checks/performance_check.sh"; do
  if [[ -f "$performance_check" ]]; then
    check_has_match "TE performance check separates latency percentile duration" "LATENCY_BENCHTIME" "$performance_check"
  fi
done

if [[ -f "$target/templates/frontend/package.json" ]]; then
  check_has_match "TE frontend scaffold includes Testing Library React" '"@testing-library/react"' "$target/templates/frontend/package.json"
  check_has_match "TE frontend scaffold includes user-event" '"@testing-library/user-event"' "$target/templates/frontend/package.json"
  check_has_match "TE frontend scaffold includes jsdom" '"jsdom"' "$target/templates/frontend/package.json"
  check_has_match "TE frontend scaffold includes property testing" '"fast-check"' "$target/templates/frontend/package.json"
fi

if [[ -f "$target/templates/Makefile" ]]; then
  check_has_match "TE scaffold lint runs testing practices check" "check-testing-practices|testing_practices_check\.sh" "$target/templates/Makefile"
fi

if rg --files "$target" | rg -q '(^|/)features/.*\.feature$'; then
  check_has_match "TE acceptance feature files have normal acceptance entry point" "(acceptance|test-acceptance|acceptance-generator|gherkin-parser)" "$target" \
    --glob 'Makefile' --glob '*.mk' --glob '*.sh' --glob 'package.json' --glob '!**/node_modules/**'
  check_has_match "TE acceptance feature files have mutation entry point" "(acceptance-mutation|mutation-acceptance|gherkin-mutator)" "$target" \
    --glob 'Makefile' --glob '*.mk' --glob '*.sh' --glob 'package.json' --glob '!**/node_modules/**'
else
  echo "[OK] no acceptance feature files present"
fi

if [[ "$failed" -ne 0 ]]; then
  echo "testing practices check failed"
  exit 1
fi

echo "testing practices check passed"
