#!/usr/bin/env bash
# ws_netlab_tuning.sh compares kernel TCP settings and frame gzip levels on mobile links.
# It runs as root on a Linux host whose kernel offers BBR (for example the Colima VM).
# Docker Desktop's kernel offers only reno and cubic, so ws_netlab.sh cannot test BBR.
# Each variant runs in its own network namespace, with its own lo, netem qdisc, and sysctls.
# Usage: NETLAB_BIN=<dir with netlab and netlab.test> NETLAB_OUT=<file.jsonl> ws_netlab_tuning.sh
set -euo pipefail

bin="${NETLAB_BIN:?NETLAB_BIN must name the directory of the linux netlab binaries}"
out="${NETLAB_OUT:?NETLAB_OUT must name the results file}"
trials="${NETLAB_TRIALS:-7}"
: > "$out"

modprobe tcp_bbr
echo "kernel: $(uname -r); congestion available: $(cat /proc/sys/net/ipv4/tcp_available_congestion_control)"

# congestion|slow_start_after_idle|gzip level
variants=(
  "cubic|1|1"
  "cubic|0|1"
  "bbr|1|1"
  "bbr|0|1"
  "bbr|0|6"
  "bbr|0|9"
)
# name|one-way delay ms|rate kbit/s|loss percent|idle ms
profiles=(
  "4g-typical-ng|50|8000|0|1500"
  "3g-congested|100|1500|1|2000"
  "edge-2g|200|256|1|3000"
)

run_variant() {
  local cc="$1" ssai="$2" level="$3" part="$4"
  unshare -n bash -c '
    set -euo pipefail
    echo "$0" > /proc/sys/net/ipv4/tcp_congestion_control
    echo "$1" > /proc/sys/net/ipv4/tcp_slow_start_after_idle
    for entry in "${@:6}"; do
      IFS="|" read -r name delay rate loss idle <<<"$entry"
      "$3/netlab" set -delay "${delay}ms" -rate-kbit "$rate" -loss "$loss"
      NETLAB_PROFILE="$name" NETLAB_IDLE_MS="$idle" NETLAB_TRIALS="$5" NETLAB_GZIP_LEVEL="$2" \
        timeout 3600 "$3/netlab.test" -test.run "^TestWSNetlab$" -test.count=1 -test.v -test.timeout 60m 2>/dev/null \
        | grep "^{\"profile\"" >> "$4"
      echo "done $0 ssai=$1 gzip=$2 $name"
    done
  ' "$cc" "$ssai" "$level" "$bin" "$part" "$trials" "${profiles[@]}"
}

pids=()
for v in "${variants[@]}"; do
  IFS='|' read -r cc ssai level <<<"$v"
  part="$out.$cc-$ssai-$level"
  : > "$part"
  run_variant "$cc" "$ssai" "$level" "$part" &
  pids+=("$!")
done
for pid in "${pids[@]}"; do wait "$pid"; done
cat "$out".* > "$out" && rm -f "$out".*
echo "results: $out ($(wc -l <"$out") rows)"
