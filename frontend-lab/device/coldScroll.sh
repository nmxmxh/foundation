#!/bin/zsh
# Cold-start scroll ledger (research doc P9): force-stop, launch, then time the
# first swipes on the landing route separately from later ones.
#   device/coldScroll.sh <package> <runs> [settle-seconds] [route]
# The route is reached in-app (pushState + popstate over the forwarded
# DevTools socket) so the launch itself is still cold.
# Prints one line per swipe block: launch TotalTime, then gfxinfo for blocks
# 1 (first swipes after launch), 2 and 3 (same screen, progressively warmer).
set -euo pipefail
PKG="$1"; RUNS="${2:-3}"; SETTLE="${3:-6}"; ROUTE="${4:-}"
HERE="${0:A:h}"
block() {
  adb shell dumpsys gfxinfo "$PKG" reset >/dev/null
  for i in 1 2; do adb shell input swipe 540 1900 540 500 280; sleep 0.7; adb shell input swipe 540 500 540 1900 280; sleep 0.7; done
  adb shell dumpsys gfxinfo "$PKG" | grep -E "Total frames rendered|Janky frames:|50th percentile|90th percentile|99th percentile|50th gpu percentile" \
    | sed -E 's/Total frames rendered: /frames=/; s/Janky frames: ([0-9]+) \(([0-9.]+)%\)/janky=\2%/; s/([0-9]+)th percentile: /p\1=/; s/([0-9]+)th gpu percentile: /gpu_p\1=/' | tr '\n' ' '
}
for run in $(seq 1 "$RUNS"); do
  adb shell am force-stop "$PKG"; sleep 2
  total=$(adb shell am start -W -n "$PKG/.MainActivity" | grep TotalTime | tr -d '\r')
  if [[ -n "$ROUTE" ]]; then
    local_pid=""
    for i in $(seq 1 30); do local_pid=$(adb shell pidof "$PKG" | tr -d '\r'); [[ -n "$local_pid" ]] && break; sleep 0.5; done
    adb forward --remove-all; adb forward tcp:9222 "localabstract:webview_devtools_remote_$local_pid" >/dev/null
    for i in $(seq 1 40); do curl -s 127.0.0.1:9222/json | grep -q '"page"' && break; sleep 0.5; done
    sleep 2
    node "$HERE/cdp.mjs" eval "(()=>{history.pushState({},'','$ROUTE');dispatchEvent(new PopStateEvent('popstate'));return 1})()" >/dev/null
  fi
  sleep "$SETTLE"
  echo "run $run $total"
  for b in 1 2 3; do printf '  block %s  ' "$b"; block; echo; done
done
