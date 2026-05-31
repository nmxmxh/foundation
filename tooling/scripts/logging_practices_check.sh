#!/bin/zsh
set -euo pipefail

target="${1:-.}"
failed=0

existing_roots() {
  local roots=()
  local candidate
  for candidate in "$@"; do
    [[ -e "$candidate" ]] && roots+=("$candidate")
  done
  printf '%s\n' "${roots[@]}"
}

check_exists() {
  local label="$1"
  local path="$2"
  if [[ -e "$path" ]]; then
    echo "[OK] $label"
  else
    echo "[FAIL] $label"
    echo "  missing: ${path#$target/}"
    failed=1
  fi
}

check_file_contains() {
  local label="$1"
  local file="$2"
  local pattern="$3"
  if [[ -f "$file" ]] && grep -Fq -- "$pattern" "$file"; then
    echo "[OK] $label"
  else
    echo "[FAIL] $label"
    echo "  missing pattern: $pattern"
    echo "  file: ${file#$target/}"
    failed=1
  fi
}

check_file_contains_any() {
  local label="$1"
  local file="$2"
  shift 2
  local pattern
  if [[ -f "$file" ]]; then
    for pattern in "$@"; do
      if grep -Fq -- "$pattern" "$file"; then
        echo "[OK] $label"
        return
      fi
    done
  fi
  echo "[FAIL] $label"
  echo "  missing one of: $*"
  echo "  file: ${file#$target/}"
  failed=1
}

check_any_file_contains() {
  local label="$1"
  local pattern="$2"
  shift 2
  local roots=("$@")
  local output=""
  if [[ "${#roots[@]}" -gt 0 ]]; then
    if command -v rg >/dev/null 2>&1; then
      output="$(rg -n "$pattern" "${roots[@]}" --glob '*.go' 2>/dev/null || true)"
    else
      output="$(grep -R -n -E --include='*.go' "$pattern" "${roots[@]}" 2>/dev/null || true)"
    fi
  fi
  if [[ -n "$output" ]]; then
    echo "[OK] $label"
  else
    echo "[FAIL] $label"
    echo "  missing pattern: $pattern"
    failed=1
  fi
}

check_no_match() {
  local label="$1"
  local pattern="$2"
  shift 2
  local roots=("$@")
  if [[ "${#roots[@]}" -eq 0 ]]; then
    echo "[OK] $label"
    return
  fi

  local output
  if command -v rg >/dev/null 2>&1; then
    output="$(rg -n "$pattern" "${roots[@]}" \
      --glob '*.go' \
      --glob '!**/foundation/**' \
      --glob '!**/server-kit/go/logger/**' \
      --glob '!**/*test.go' \
      --glob '!**/generated/**' \
      --glob '!**/testdata/**' \
      --glob '!**/.cache/**' \
      --glob '!**/vendor/**' 2>/dev/null || true)"
  else
    output="$(grep -R -n -E --include='*.go' "$pattern" "${roots[@]}" 2>/dev/null \
      | grep -Ev '/foundation/|/server-kit/go/logger/|_test[.]go:|/generated/|/testdata/|/[.]cache/|/vendor/' || true)"
  fi
  if [[ -n "$output" ]]; then
    echo "[FAIL] $label"
    echo "$output" | sed 's/^/  /' | head -60
    failed=1
  else
    echo "[OK] $label"
  fi
}

check_project_logging_wiring() {
  local startup_logger="$target/internal/startup/logger.go"
  local startup_dir="$target/internal/startup"
  local server_file="$target/internal/server/server.go"
  local middleware_file="$target/internal/server/middleware/middleware.go"
  [[ -f "$target/.foundation" ]] || return 0
  [[ -d "$target/internal" ]] || return 0

  check_file_contains "startup installs Foundation logger facade" "$startup_logger" "server-kit/go/logger"
  check_file_contains_any "startup installs process default logger" "$startup_logger" "logger.SetDefault" "logger.Install"
  check_file_contains_any "startup declares app runtime logger scope" "$startup_logger" "logger.RuntimeConfig" "logger.Config{"
  check_any_file_contains "dependencies expose Foundation logger" "Log[[:space:]]+(kitlogger|logger)\\.Logger" "$startup_dir"
  check_file_contains "server uses Foundation logger facade" "$server_file" "kitlogger.Logger"
  check_file_contains "middleware uses Foundation logger facade" "$middleware_file" "kitlogger.Logger"
  check_file_contains "middleware writes correlation header" "$middleware_file" "X-Correlation-ID"
  check_file_contains "middleware injects Foundation metadata" "$middleware_file" "metadata.IntoContext"
  check_file_contains "middleware logs with request context" "$middleware_file" "InfoContext(r.Context()"
}

check_template_logging_wiring() {
  local template_root="$target/templates/backend"
  [[ -d "$template_root" ]] || return 0

  check_file_contains "template startup installs Foundation logger facade" "$template_root/internal/startup/logger.go" "server-kit/go/logger"
  check_file_contains_any "template startup installs process default logger" "$template_root/internal/startup/logger.go" "logger.SetDefault" "logger.Install"
  check_file_contains_any "template startup declares app runtime logger scope" "$template_root/internal/startup/logger.go" "logger.RuntimeConfig" "logger.Config{"
  check_file_contains "template dependencies expose Foundation logger" "$template_root/internal/startup/dependencies.go" "Log           kitlogger.Logger"
  check_file_contains "template server uses Foundation logger facade" "$template_root/internal/server/server.go" "kitlogger.Logger"
  check_file_contains "template middleware uses Foundation logger facade" "$template_root/internal/server/middleware/middleware.go" "kitlogger.Logger"
  check_file_contains "template middleware writes correlation header" "$template_root/internal/server/middleware/middleware.go" "X-Correlation-ID"
  check_file_contains "template middleware injects Foundation metadata" "$template_root/internal/server/middleware/middleware.go" "metadata.IntoContext"
  check_file_contains "template middleware logs with request context" "$template_root/internal/server/middleware/middleware.go" "InfoContext(r.Context()"
}

check_foundation_logger_contract() {
  local kit=""
  if [[ -d "$target/foundation/server-kit/go" ]]; then
    kit="$target/foundation/server-kit/go"
  elif [[ -d "$target/server-kit/go" ]]; then
    kit="$target/server-kit/go"
  fi
  [[ -n "$kit" ]] || return 0

  check_exists "Foundation logger facade" "$kit/logger/logger.go"
  check_exists "Foundation logger handler" "$kit/logger/handler.go"
  check_exists "Foundation logger async handler" "$kit/logger/async_handler.go"
  check_exists "Foundation logger console handler" "$kit/logger/console_handler.go"
  check_exists "Foundation logger compact wire handler" "$kit/logger/cwf_handler.go"
  check_file_contains "logger exposes compact wire format" "$kit/logger/logger.go" "FormatCWF"
  check_file_contains "logger exposes declarative runtime config" "$kit/logger/logger.go" "RuntimeConfig"
  check_file_contains "logger exposes declarative installer" "$kit/logger/logger.go" "func Install"
  check_file_contains "logger exposes context-aware methods" "$kit/logger/logger.go" "InfoContext(ctx context.Context"
  check_file_contains "logger tracks async drops" "$kit/logger/logger.go" "Dropped() uint64"
  check_file_contains "logger enriches metadata context" "$kit/logger/logger.go" "metautil.FromContextOK"
  check_file_contains "logger redacts sensitive keys" "$kit/logger/handler.go" "sensitiveKeyFragments"
  check_file_contains "logger redacts authorization" "$kit/logger/handler.go" "authorization"
  check_file_contains "logger redacts password" "$kit/logger/handler.go" "password"
  check_file_contains "logger bounds string payloads" "$kit/logger/handler.go" "sanitizeString"
  check_file_contains "logger tests redaction and context enrichment" "$kit/logger/logger_test.go" "TestLoggerRedactsAndEnrichesContext"
  check_file_contains "logger tests compact wire output" "$kit/logger/logger_test.go" "TestLoggerCWFFormatIsRedactedAndParseable"
}

check_foundation_log_plane_contract() {
  local kit=""
  if [[ -d "$target/foundation/server-kit/go" ]]; then
    kit="$target/foundation/server-kit/go"
  elif [[ -d "$target/server-kit/go" ]]; then
    kit="$target/server-kit/go"
  fi
  [[ -n "$kit" ]] || return 0
  [[ -d "$kit/hermes" ]] || return 0

  check_exists "Foundation durable event log package" "$kit/eventlog/eventlog.go"
  check_file_contains "event log stores binary envelopes" "$kit/eventlog/eventlog.go" "Envelope         []byte"
  check_file_contains "event log publishes pending facts" "$kit/eventlog/eventlog.go" "PublishPending"
  check_file_contains "event log writes Redis stream envelope field" "$kit/eventlog/eventlog.go" "DefaultStreamField"
  check_no_match "event log does not import operational logging packages" '(^|[[:space:]])"log/slog"|(^|[[:space:]])"log"|server-kit/go/logger|go\.uber\.org/zap' "$kit/eventlog"
  check_file_contains "Hermes consumes typed projection facts" "$kit/hermes/contract.go" "RecordMutationBatch"
  check_file_contains "Hermes rejects text log payloads" "$kit/hermes/contract.go" "PayloadEncodingProtobuf"
  check_file_contains "Hermes accepts terminal facts only" "$kit/hermes/contract.go" "TerminalState"
  check_no_match "Hermes does not import operational logging packages" '(^|[[:space:]])"log/slog"|(^|[[:space:]])"log"|server-kit/go/logger|go\.uber\.org/zap' "$kit/hermes"

  local service_test="$kit/servicebacked/hermes_test.go"
  if [[ -f "$service_test" ]]; then
    check_file_contains "service-backed flow proves Postgres Redis Hermes drift" "$service_test" "TestServiceBackedHermesPostgresRedisDriftProof"
    check_file_contains "service-backed flow writes Redis stream envelope" "$service_test" "NewRedisStreamEnvelopeSource"
    check_file_contains "service-backed flow verifies drift" "$service_test" "CheckDrift"
  fi
}

app_roots=()
while IFS= read -r root; do
  app_roots+=("$root")
done < <(existing_roots \
  "$target/cmd" \
  "$target/internal" \
  "$target/backend/cmd" \
  "$target/backend/internal" \
  "$target/templates/backend" \
)

check_no_match "application code avoids raw slog imports" '(^|[[:space:]])"log/slog"' "${app_roots[@]}"
check_no_match "application code avoids raw stdlib log imports" '(^|[[:space:]])"log"' "${app_roots[@]}"
check_no_match "application code avoids Zap imports" 'go\.uber\.org/zap' "${app_roots[@]}"
check_no_match "application code avoids package-level raw logging calls" '\b(slog\.(Debug|Info|Warn|Error|Log|SetDefault|NewTextHandler|NewJSONHandler)|log\.(Fatal|Fatalf|Panic|Panicf|Print|Printf|Println))\b' "${app_roots[@]}"
check_no_match "application code avoids fallback logger construction" 'fallback.*NewDefault|NewDefault\(\).*fallback|defaultLogger,[[:space:]]*err[[:space:]]:=.*NewDefault' "${app_roots[@]}"
check_no_match "application startup avoids unscoped default logger construction" '(kitlogger|logger)\.NewDefault\(\)' "$target/internal/startup" "$target/backend/internal/startup" "$target/templates/backend/internal/startup"
check_template_logging_wiring
check_project_logging_wiring
check_foundation_logger_contract
check_foundation_log_plane_contract

if [[ "$failed" -ne 0 ]]; then
  echo "logging practices check failed"
  exit 1
fi

echo "logging practices check passed"
