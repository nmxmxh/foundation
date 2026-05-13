#!/usr/bin/env bash
set -euo pipefail

target="${1:-.}"
strict="${GO_CONCURRENCY_STRICT:-0}"
max_findings="${GO_CONCURRENCY_MAX_FINDINGS:-8}"
review_count=0
tmp_findings="$(mktemp "${TMPDIR:-/tmp}/ovasabi_go_concurrency_review.XXXXXX")"
trap 'rm -f "$tmp_findings"' EXIT

rg_go() {
  rg -n \
    --glob '*.go' \
    --glob '!**/generated/**' \
    --glob '!**/*_test.go' \
    --glob '!**/node_modules/**' \
    --glob '!**/dist/**' \
    --glob '!**/target/**' \
    --glob '!**/tooling/scripts/**' \
    --glob '!**/docs/**' \
    --glob '!**/data/**' \
    "$@" "$target" 2>/dev/null || true
}

code_window() {
  local file="$1"
  local line="$2"
  local before="$3"
  local after="$4"
  local start=$((line - before))
  local end=$((line + after))
  if [[ "$start" -lt 1 ]]; then
    start=1
  fi
  sed -n "${start},${end}p" "$file"
}

add_finding() {
  local file="$1"
  local line="$2"
  local text="$3"
  text="${text#"${text%%[![:space:]]*}"}"
  echo "$file:$line: $text" >>"$tmp_findings"
}

emit_result() {
  local label="$1"
  if [[ -s "$tmp_findings" ]]; then
    echo "[REVIEW] $label"
    awk -v max="$max_findings" '!seen[$0]++ { print; count++; if (count >= max) exit }' "$tmp_findings"
    review_count=$((review_count + 1))
  else
    echo "[OK] $label"
  fi
  : >"$tmp_findings"
}

scan_lock_scope() {
  local file line text block held
  : >"$tmp_findings"
  while IFS=: read -r file line text; do
    [[ -n "${file:-}" && -n "${line:-}" ]] || continue
    block="$(code_window "$file" "$line" 0 80)"
    held="$(
      printf '%s\n' "$block" | awk '
        NR == 1 { print; next }
        /^func[[:space:]]/ { exit }
        /^}/ { exit }
        /^[[:space:]]*defer[[:space:]]+.*[.]R?Unlock[[:space:]]*[(]/ { print; next }
        /[.]R?Unlock[[:space:]]*[(]/ { exit }
        { print }
      '
    )"
    if printf '%s\n' "$held" | rg -q 'select[[:space:]]*[{]|(^|[^A-Za-z0-9_])case[[:space:]].*<-|<-[[:space:]]*[A-Za-z0-9_.]+|[A-Za-z0-9_.]+[[:space:]]*<-[^-]|[.]Wait[[:space:]]*[(]|Cond[.]Wait'; then
      add_finding "$file" "$line" "$text"
    fi
  done < <(rg_go '[.]R?Lock[[:space:]]*[(]')
  emit_result "Go lock scope avoids blocking channel/context/select/wait work"
}

scan_select_default() {
  local file line text block
  : >"$tmp_findings"
  while IFS=: read -r file line text; do
    [[ -n "${file:-}" && -n "${line:-}" ]] || continue
    block="$(code_window "$file" "$line" 0 35)"
    printf '%s\n' "$block" | rg -q 'default[[:space:]]*:' || continue
    if printf '%s\n' "$block" | rg -q 'concurrency:|RecordConcurrency|RecordWorker|RecordQueueDepth|return[[:space:]]+(errors[.]New|fmt[.]Errorf|err|nil|ctx[.]Err|[A-Za-z0-9_.]*Err)|queue full|send_rejected_full'; then
      continue
    fi
    add_finding "$file" "$line" "$text"
  done < <(rg_go 'select[[:space:]]*[{]')
  emit_result "Go select default has explicit drop/probe/error rationale"
}

scan_loop_goroutines() {
  local file line text block
  : >"$tmp_findings"
  while IFS=: read -r file line text; do
    [[ -n "${file:-}" && -n "${line:-}" ]] || continue
    block="$(code_window "$file" "$line" 4 0)"
    if printf '%s\n' "$block" | rg -q 'for[[:space:]].*[{]'; then
      add_finding "$file" "$line" "$text"
    fi
  done < <(rg_go 'go[[:space:]]+func[[:space:]]*[(][[:space:]]*[)][[:space:]]*[{]')
  emit_result "Go loop-launched anonymous goroutines copy inputs explicitly"
}

scan_timers() {
  local file line text block
  : >"$tmp_findings"
  while IFS=: read -r file line text; do
    [[ -n "${file:-}" && -n "${line:-}" ]] || continue
    block="$(code_window "$file" "$line" 0 14)"
    if printf '%s\n' "$block" | rg -q '[.]Stop[[:space:]]*[(]'; then
      continue
    fi
    add_finding "$file" "$line" "$text"
  done < <(rg_go 'time[.]New(Ticker|Timer)[[:space:]]*[(]')
  emit_result "Go timers/tickers have Stop ownership"
}

scan_close_ownership() {
  local file line text block
  : >"$tmp_findings"
  while IFS=: read -r file line text; do
    [[ -n "${file:-}" && -n "${line:-}" ]] || continue
    if printf '%s\n' "$text" | rg -q '^[[:space:]]*func[[:space:]]'; then
      continue
    fi
    block="$(code_window "$file" "$line" 18 4)"
    if printf '%s\n' "$text" | rg -q 'defer[[:space:]]+close[[:space:]]*[(]'; then
      continue
    fi
    if printf '%s\n' "$block" | rg -q 'once[.]Do[[:space:]]*[(]|[A-Za-z0-9_]*Once[.]Do[[:space:]]*[(]|closed[[:space:]]*=[[:space:]]*true'; then
      continue
    fi
    add_finding "$file" "$line" "$text"
  done < <(rg_go '(^|[^.A-Za-z0-9_])close[[:space:]]*[(]')
  emit_result "Go channel close ownership is single-owner or sync.Once guarded"
}

scan_lock_scope
scan_select_default
scan_loop_goroutines
scan_timers
scan_close_ownership

if [[ "$review_count" -gt 0 ]]; then
  echo "go concurrency practices review found $review_count area(s) for human review"
  echo "set GO_CONCURRENCY_STRICT=1 to fail on review findings"
  if [[ "$strict" == "1" || "$strict" == "true" ]]; then
    exit 1
  fi
else
  echo "go concurrency practices review passed with no broad review findings"
fi
