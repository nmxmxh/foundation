#!/bin/zsh
# Interleaved on-device scroll capture for a Tauri WebView app (research doc §7).
#
# Prerequisites (see docs/ui_render_performance_handover.md, "Device setup"):
#   - emulator or device on adb, app installed from a `--features tauri/devtools` build
#   - adb forward tcp:9222 localabstract:webview_devtools_remote_<pid>
#
# Usage:
#   device/scrollGfx.sh <package> <route-url> <pairs> <variantA-css> <variantB-css>
# Empty CSS means "no injection". Example (tier A/B):
#   device/scrollGfx.sh com.ovasabi.choosechow http://tauri.localhost/profile 3 \
#     "" ":root{} /* tier via attribute below */"
# Rules baked in (ledger findings 3 and 6): one discarded warm-up pair, 15 s
# settle after navigation, variants interleaved, report every run (bimodal).
set -euo pipefail
PKG="$1"; URL="$2"; PAIRS="$3"; CSS_A="${4:-}"; CSS_B="${5:-}"
HERE="${0:A:h}"

run() {
  local label="$1" css="$2"
  node "$HERE/cdp.mjs" nav "$URL" >/dev/null; sleep 15
  if [[ -n "$css" ]]; then
    local js
    js=$(CSS="$css" node -e 'process.stdout.write(`(()=>{const s=document.createElement("style");s.textContent=${JSON.stringify(process.env.CSS)};document.head.append(s);return "ok"})()`)')
    node "$HERE/cdp.mjs" eval "$js" >/dev/null; sleep 2
  fi
  adb shell dumpsys gfxinfo "$PKG" reset >/dev/null
  for i in 1 2 3 4 5; do adb shell input swipe 540 1900 540 500 280; sleep 0.4; adb shell input swipe 540 500 540 1900 280; sleep 0.4; done
  printf '%-14s ' "$label"
  adb shell dumpsys gfxinfo "$PKG" | grep -E "Total frames rendered|Janky frames:|50th percentile|90th percentile|99th percentile" \
    | sed -E 's/Total frames rendered: /frames=/; s/Janky frames: ([0-9]+) \(([0-9.]+)%\)/janky=\2%/; s/([0-9]+)th percentile: /p\1=/' | tr '\n' ' '
  echo
}

run "warmup A" "$CSS_A"; run "warmup B" "$CSS_B"
for pair in $(seq 1 "$PAIRS"); do run "A $pair" "$CSS_A"; run "B $pair" "$CSS_B"; done
